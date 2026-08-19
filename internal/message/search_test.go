package message

import (
	"database/sql"
	"testing"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func TestSearchMessages_EmptyQuery(t *testing.T) {
	svc, _ := newTestService(t)

	for _, query := range []string{"", "   ", "\t\n"} {
		results, err := svc.SearchMessages(t.Context(), query, 10)
		require.NoError(t, err)
		require.Empty(t, results)
	}
}

// TestSearchMessages_MatchesAndReturnsSessionTitles seeds two sessions
// with messages and verifies keyword matching, case insensitivity,
// session titles, ordering, and the limit.
func TestSearchMessages_MatchesAndReturnsSessionTitles(t *testing.T) {
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	q := db.New(conn)
	sessions := session.NewService(q, conn)
	svc := NewService(q)

	s1, err := sessions.Create(t.Context(), "session one")
	require.NoError(t, err)
	s2, err := sessions.Create(t.Context(), "session two")
	require.NoError(t, err)

	// s1: one hit, one miss, one case-variant hit.
	hit1 := seedSearchMessage(t, svc, s1.ID, "the quick brown fox")
	seedSearchMessage(t, svc, s1.ID, "nothing to see here")
	hit2 := seedSearchMessage(t, svc, s1.ID, "QUICK search results")
	// s2: one hit and one empty-content message.
	hit3 := seedSearchMessage(t, svc, s2.ID, "another quick fox")
	seedSearchMessage(t, svc, s2.ID, "")

	// Give each message a distinct created_at so ordering is
	// deterministic: hit3 newest, hit1 oldest.
	orderCreatedAt(t, conn, hit1, 100)
	orderCreatedAt(t, conn, hit2, 200)
	orderCreatedAt(t, conn, hit3, 300)

	results, err := svc.SearchMessages(t.Context(), "quick", 10)
	require.NoError(t, err)
	require.Len(t, results, 3)

	require.Equal(t, hit3, results[0].Message.ID)
	require.Equal(t, "session two", results[0].SessionTitle)

	require.Equal(t, hit2, results[1].Message.ID)
	require.Equal(t, "session one", results[1].SessionTitle)

	require.Equal(t, hit1, results[2].Message.ID)
	require.Equal(t, "session one", results[2].SessionTitle)

	// LIMIT applies.
	limited, err := svc.SearchMessages(t.Context(), "quick", 2)
	require.NoError(t, err)
	require.Len(t, limited, 2)
	require.Equal(t, hit3, limited[0].Message.ID)
	require.Equal(t, hit2, limited[1].Message.ID)

	// No match returns empty without error.
	misses, err := svc.SearchMessages(t.Context(), "zzzz-not-present", 10)
	require.NoError(t, err)
	require.Empty(t, misses)
}

func seedSearchMessage(t *testing.T, svc Service, sessionID, text string) string {
	t.Helper()
	msg, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: text}},
	})
	require.NoError(t, err)
	return msg.ID
}

// orderCreatedAt rewrites a message's created_at so tests can control
// the sort order regardless of wall-clock resolution.
func orderCreatedAt(t *testing.T, conn *sql.DB, messageID string, createdAt int64) {
	t.Helper()
	_, err := conn.ExecContext(t.Context(),
		"UPDATE messages SET created_at = ? WHERE id = ?", createdAt, messageID)
	require.NoError(t, err)
}
