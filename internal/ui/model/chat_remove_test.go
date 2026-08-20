package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/stretchr/testify/require"
)

func TestChat_RemoveMessage_PreservesViewport(t *testing.T) {
	com := common.DefaultCommon(nil)
	c := NewChat(com, config.ScrollbarDefault)

	items := []chat.MessageItem{
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m2"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m3"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m4"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m5"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m6"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m7"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m8"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m9"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m10"},
	}
	c.SetMessages(items...)

	// Tall viewport: all items visible, scrolled to bottom. SetMessages
	// only scrolls; selection starts unset (-1).
	c.SetSize(100, 30)
	require.Equal(t, 0, c.list.Offset())
	require.Equal(t, -1, c.list.Selected())

	// Select m5 (index 4) explicitly, then delete it.
	c.list.SetSelected(4)
	require.Equal(t, "m5", c.list.SelectedItem().(chat.MessageItem).ID())
	c.RemoveMessage("m5")

	require.Equal(t, 9, c.list.Len(), "m5 removed, 9 remain")
	require.Equal(t, 4, c.list.Selected(), "selection should land on the item that took m5's slot (m6)")

	// Now delete m6 which now sits at index 4 (the selected item).
	c.RemoveMessage("m6")
	require.Equal(t, 4, c.list.Selected(), "deleting selected item should keep a valid selection")

	// Delete the last item; selection should clamp to the new last item.
	c.list.SetSelected(7) // m10 at index 7
	c.RemoveMessage("m10")
	require.Equal(t, 6, c.list.Selected(), "selection clamped after deleting last item")
}

func TestChat_RemoveMessage_KeepsSelectionInView(t *testing.T) {
	com := common.DefaultCommon(nil)
	c := NewChat(com, config.ScrollbarDefault)

	items := make([]chat.MessageItem, 0, 20)
	for i := 1; i <= 20; i++ {
		items = append(items, &fakeChatItem{Versioned: list.NewVersioned(), id: "m" + string(rune('0'+i/10)) + string(rune('0'+i%10))})
	}
	c.SetMessages(items...)

	// Small viewport: only a few items visible.
	c.SetSize(100, 5)
	c.list.SetSelected(15)

	// Deleting an item above the viewport must shift the viewport up so
	// the selection stays visible.
	c.RemoveMessage("m05") // index 4, well above viewport
	require.Equal(t, 14, c.list.Selected(), "selection index shifts down by one")
}

func TestChat_RemoveMessage_SelectedInViewOffsetStable(t *testing.T) {
	com := common.DefaultCommon(nil)
	c := NewChat(com, config.ScrollbarDefault)

	// 20 items, small viewport (5 lines) so only ~4 items visible.
	items := make([]chat.MessageItem, 0, 20)
	for i := 1; i <= 20; i++ {
		id := "m" + string(rune('a'+i-1))
		items = append(items, &fakeChatItem{Versioned: list.NewVersioned(), id: id})
	}
	c.SetMessages(items...)
	c.SetSize(100, 5)

	// Select m15 (index 14), scroll it into view, remember the offset.
	c.list.SetSelected(14)
	c.list.ScrollToSelected()
	offsetBefore := c.list.Offset()
	require.Greater(t, offsetBefore, 0, "scrolled into view => offset > 0")
	require.True(t, c.list.SelectedItemInView(), "m15 visible before delete")

	// Delete m15 itself; the next item (m16) slides into its slot and
	// must stay selected and in view.
	c.RemoveMessage("m15")
	require.Equal(t, 14, c.list.Selected(), "selection stays on the item that slid into the slot")
	require.True(t, c.list.SelectedItemInView(), "replacement selection still in view")
}
