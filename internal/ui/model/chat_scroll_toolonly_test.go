package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/stretchr/testify/require"
)

// TestChat_ScrollToMessage_ToolOnlyMessage reproduces the search-jump
// regression: an assistant message rendered only as tool items (no text
// content) registers its items under the tool call IDs, so scrolling by
// the message ID must fall back to the item's MessageID.
func TestChat_ScrollToMessage_ToolOnlyMessage(t *testing.T) {
	com := common.DefaultCommon(nil)
	c := NewChat(com, config.ScrollbarDefault)

	items := []chat.MessageItem{
		&fakeChatItem{Versioned: list.NewVersioned(), id: "u1"},
		// Tool-only assistant messages: their rendered items carry the
		// tool call ID as their own ID and the message ID as MessageID.
		&fakeChatItem{Versioned: list.NewVersioned(), id: "tc-a1", messageID: "a1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "u2"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "tc-a2", messageID: "a2"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "tc-a3", messageID: "a3"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "u3"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "tc-a4", messageID: "a4"},
	}
	c.SetMessages(items...)
	c.SetSize(100, 3) // small viewport so mid-list hits require scrolling

	// A hit in the middle of the list: view must scroll off the top.
	cmd := c.ScrollToMessage("a2")
	require.NotNil(t, cmd, "ScrollToMessage by message ID must resolve tool-only messages")
	require.True(t, c.list.Offset() > 0, "view must scroll off the top for a mid-list hit")
	require.False(t, c.list.AtBottom(), "a mid-list hit must not leave the view pinned to the bottom")

	// A subsequent SetSize (as loadSessionMsg triggers via
	// updateLayoutAndSize) must not re-anchor to the bottom: the jump
	// disables follow mode.
	offsetBefore := c.list.Offset()
	c.SetSize(100, 3)
	require.Equal(t, offsetBefore, c.list.Offset(), "SetSize must not reset the jump scroll position")

	// Unknown message ID stays a no-op.
	require.Nil(t, c.ScrollToMessage("nope"))
}
