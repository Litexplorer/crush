package message

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestService_DeleteTurn(t *testing.T) {
	t.Parallel()
	svc, sessionID := newTestService(t, WithDebounce(time.Millisecond))

	mustCreate := func(role MessageRole, text string) Message {
		m, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
			Role: role,
			Parts: []ContentPart{
				TextContent{Text: text},
			},
		})
		require.NoError(t, err)
		return m
	}

	// Build: user turn 1 -> assistant reply -> tool call -> user turn 2.
	u1 := mustCreate(User, "first turn")
	_ = mustCreate(Assistant, "reply 1")
	_ = mustCreate(Assistant, "tool result")
	u2 := mustCreate(User, "second turn")

	// Deleting anchored at the assistant reply resolves to turn 1 and
	// keeps the following user turn.
	deleted, err := svc.DeleteTurn(t.Context(), sessionID, u1.ID)
	require.NoError(t, err)
	require.Len(t, deleted, 3, "turn 1 = user + 2 assistant messages")
	require.Equal(t, u1.ID, deleted[0].ID)

	remaining, err := svc.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, remaining, 1, "only turn 2 remains")
	require.Equal(t, u2.ID, remaining[0].ID)

	// Unknown anchor is an error.
	_, err = svc.DeleteTurn(t.Context(), sessionID, "missing-id")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}
