package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestListPagePagesBackwardsFromNewest verifies the cursor contract the web
// session view relies on: newest page first, then pages strictly older than
// the cursor, with hasMore telling the caller when to stop.
func TestListPagePagesBackwardsFromNewest(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)

	ids := make([]string, 0, 5)
	for range 5 {
		msg, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: "hi"}},
		})
		require.NoError(t, err)
		ids = append(ids, msg.ID)
	}

	// Newest page: created_at has one-second resolution here, so ordering
	// must come from seq, not timestamps.
	tail, hasMore, err := svc.ListPage(t.Context(), sessionID, ListPageParams{Limit: 2})
	require.NoError(t, err)
	require.Len(t, tail, 2)
	require.True(t, hasMore)
	require.Equal(t, ids[3:], []string{tail[0].ID, tail[1].ID})

	// Page before the oldest loaded message.
	older, hasMore, err := svc.ListPage(t.Context(), sessionID, ListPageParams{Limit: 2, BeforeID: tail[0].ID})
	require.NoError(t, err)
	require.Len(t, older, 2)
	require.True(t, hasMore)
	require.Equal(t, ids[1:3], []string{older[0].ID, older[1].ID})

	// Oldest page reports no more history.
	first, hasMore, err := svc.ListPage(t.Context(), sessionID, ListPageParams{Limit: 2, BeforeID: older[0].ID})
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.False(t, hasMore)
	require.Equal(t, ids[0], first[0].ID)

	// Pages do not repeat the cursor message.
	seen := map[string]int{}
	for _, page := range [][]Message{tail, older, first} {
		for _, msg := range page {
			seen[msg.ID]++
		}
	}
	for id, count := range seen {
		require.Equal(t, 1, count, "message %s returned by more than one page", id)
	}
	require.Len(t, seen, len(ids))
}

// TestListPageDefaultsAndEmpty covers the two degenerate inputs a client can
// send: no limit and an unknown cursor.
func TestListPageDefaultsAndEmpty(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)

	empty, hasMore, err := svc.ListPage(t.Context(), sessionID, ListPageParams{Limit: 10})
	require.NoError(t, err)
	require.Empty(t, empty)
	require.False(t, hasMore)

	for range 3 {
		_, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: "hi"}},
		})
		require.NoError(t, err)
	}

	// Zero limit falls back to the default page size instead of returning
	// nothing.
	got, hasMore, err := svc.ListPage(t.Context(), sessionID, ListPageParams{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.False(t, hasMore)

	// A cursor that no longer exists (compacted session) must not fail the
	// read; it falls back to the newest page.
	got, _, err = svc.ListPage(t.Context(), sessionID, ListPageParams{Limit: 2, BeforeID: "missing"})
	require.NoError(t, err)
	require.Len(t, got, 2)
}
