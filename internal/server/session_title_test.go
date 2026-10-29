package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// renameStubSessions stores the renamed title in memory and serves it back
// through Get, so the handler can be exercised without a database.
type renameStubSessions struct {
	session.Service
	sess session.Session
}

func (s *renameStubSessions) Rename(_ context.Context, _ string, title string) error {
	s.sess.Title = title
	return nil
}

func (s *renameStubSessions) Get(_ context.Context, _ string) (session.Session, error) {
	return s.sess, nil
}

// buildRenameController wires a controller around a single stubbed session.
func buildRenameController(t *testing.T, sess session.Session) (*controllerV1, string) {
	t.Helper()

	b := backend.New(context.Background(), nil, nil)
	wsID := uuid.New().String()
	a := &app.App{}
	a.Sessions = &renameStubSessions{sess: sess}
	backend.InsertWorkspaceForTest(b, &backend.Workspace{ID: wsID, Path: t.TempDir(), App: a})

	return &controllerV1{backend: b, server: &Server{backend: b}}, wsID
}

func putTitleRequest(t *testing.T, wsID, sid, body string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/v1/workspaces/"+wsID+"/sessions/"+sid+"/title", strings.NewReader(body))
	req.SetPathValue("id", wsID)
	req.SetPathValue("sid", sid)
	return req
}

func TestPutWorkspaceSessionTitleRenames(t *testing.T) {
	t.Parallel()
	const sid = "s-rename"
	c, wsID := buildRenameController(t, session.Session{ID: sid, Title: "old title", TotalTokens: 42})

	rec := httptest.NewRecorder()
	c.handlePutWorkspaceSessionTitle(rec, putTitleRequest(t, wsID, sid, `{"title":"  crush-代码开发  "}`))

	require.Equal(t, http.StatusOK, rec.Code)
	var got proto.Session
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "crush-代码开发", got.Title, "title should be trimmed and stored")
	require.EqualValues(t, 42, got.TotalTokens, "usage counters must survive a rename")
}

func TestPutWorkspaceSessionTitleRejectsEmpty(t *testing.T) {
	t.Parallel()
	const sid = "s-empty"
	c, wsID := buildRenameController(t, session.Session{ID: sid, Title: "keep me"})

	rec := httptest.NewRecorder()
	c.handlePutWorkspaceSessionTitle(rec, putTitleRequest(t, wsID, sid, `{"title":"   "}`))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "title is required")
}
