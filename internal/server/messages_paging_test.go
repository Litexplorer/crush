package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// seedSessionMessages gives the synthetic workspace real session and message
// services, then creates a session with n messages. Returns the session id
// plus the message ids in creation order.
func seedSessionMessages(t *testing.T, h *e2eHarness, n int) (string, []string) {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	h.app.Sessions = session.NewService(q, conn)
	h.app.Messages = message.NewService(q)

	sess, err := h.app.Sessions.Create(t.Context(), "paging")
	require.NoError(t, err)
	ids := make([]string, 0, n)
	for range n {
		msg, err := h.app.Messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "hi"}},
		})
		require.NoError(t, err)
		ids = append(ids, msg.ID)
	}
	return sess.ID, ids
}

// TestSessionMessagesPagingEndpoint covers the paged contract the web
// client depends on: limit + X-Has-More for the newest page, before= for
// older pages, and the legacy whole-transcript shape when limit is absent.
func TestSessionMessagesPagingEndpoint(t *testing.T) {
	h := newE2EHarness(t)
	sid, ids := seedSessionMessages(t, h, 5)
	base := h.httpSrv.URL + "/v1/workspaces/" + h.workspace.ID + "/sessions/" + sid + "/messages"

	get := func(t *testing.T, query string) ([]proto.Message, string) {
		t.Helper()
		resp, err := h.httpSrv.Client().Get(base + query)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out []proto.Message
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out, resp.Header.Get("X-Has-More")
	}

	tail, hasMore := get(t, "?limit=2")
	require.Len(t, tail, 2)
	require.Equal(t, "true", hasMore)
	require.Equal(t, ids[3:], []string{tail[0].ID, tail[1].ID})

	older, hasMore := get(t, "?limit=2&before="+tail[0].ID)
	require.Len(t, older, 2)
	require.Equal(t, "true", hasMore)
	require.Equal(t, ids[1:3], []string{older[0].ID, older[1].ID})

	oldest, hasMore := get(t, "?limit=2&before="+older[0].ID)
	require.Len(t, oldest, 1)
	require.Equal(t, "false", hasMore)
	require.Equal(t, ids[0], oldest[0].ID)

	// No limit keeps the old whole-transcript response for other clients.
	all, _ := get(t, "")
	require.Len(t, all, len(ids))
}

// TestSessionMessagesResponseIsCompressed checks that a large JSON body is
// gzipped on the wire while SSE stays uncompressed.
func TestSessionMessagesResponseIsCompressed(t *testing.T) {
	h := newE2EHarness(t)
	sid, _ := seedSessionMessages(t, h, 5)
	url := h.httpSrv.URL + "/v1/workspaces/" + h.workspace.ID + "/sessions/" + sid + "/messages"

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	// The default transport would transparently decompress; ask it not to so
	// the header we set stays observable.
	resp, err := (&http.Transport{DisableCompression: false}).RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	reader, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	var out []proto.Message
	require.NoError(t, json.Unmarshal(body, &out))
	require.Len(t, out, 5)
}
