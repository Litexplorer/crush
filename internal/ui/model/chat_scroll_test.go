package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/stretchr/testify/require"
)

// fakeChatItem is a minimal chat.MessageItem for exercising list-level
// behavior (id index, scrolling) without full message rendering.
type fakeChatItem struct {
	*list.Versioned
	id string
}

var _ chat.MessageItem = (*fakeChatItem)(nil)
var _ list.Focusable = (*fakeChatItem)(nil)

func (f *fakeChatItem) ID() string                    { return f.id }
func (f *fakeChatItem) Render(width int) string       { return f.id }
func (f *fakeChatItem) RawRender(width int) string    { return f.id }
func (f *fakeChatItem) SetFocused(bool)               {}
func (f *fakeChatItem) Finished() bool                { return true }

// TestChat_ScrollToMessage verifies that ScrollToMessage scrolls the chat
// view to the item with the given message ID, selects it when selectable,
// and is a no-op for unknown IDs.
func TestChat_ScrollToMessage(t *testing.T) {
	com := common.DefaultCommon(nil)
	c := NewChat(com, config.ScrollbarDefault)

	items := []chat.MessageItem{
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m2"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m3"},
	}
	c.SetMessages(items...)
	c.SetSize(100, 10)

	require.Equal(t, 0, c.list.Offset())
	require.Nil(t, c.ScrollToMessage("missing"))
	require.Equal(t, 0, c.list.Offset())

	cmd := c.ScrollToMessage("m3")
	require.NotNil(t, cmd) // scrollbar hide tick
	// Rows above m3: item0 (1) + gap (1) + item1 (1) + gap (1).
	require.Equal(t, 4, c.list.Offset())
	require.Equal(t, 2, c.list.Selected())

	// Scrolling to an item leaves selection on it when selectable.
	cmd = c.ScrollToMessage("m1")
	require.NotNil(t, cmd)
	require.Equal(t, 0, c.list.Offset())
	require.Equal(t, 0, c.list.Selected())
}
