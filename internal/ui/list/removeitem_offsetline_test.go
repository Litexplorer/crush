package list

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// testItem is a fixed-height item for offset math tests.
type offsetItem struct {
	*Versioned
	label  string
	height int
}

func (o offsetItem) ID() string                 { return o.label }
func (o offsetItem) Render(width int) string    { return o.label }
func (o offsetItem) RawRender(width int) string { return o.label }
func (o offsetItem) SetFocused(bool)            {}
func (o offsetItem) Finished() bool             { return true }

func TestRemoveItem_ResetsOffsetLineWhenDeletingFirstVisible(t *testing.T) {
	t.Parallel()

	l := NewList()
	l.SetGap(1)
	l.SetItems(
		offsetItem{Versioned: NewVersioned(), label: "a", height: 3},
		offsetItem{Versioned: NewVersioned(), label: "b", height: 3},
		offsetItem{Versioned: NewVersioned(), label: "c", height: 3},
		offsetItem{Versioned: NewVersioned(), label: "d", height: 3},
		offsetItem{Versioned: NewVersioned(), label: "e", height: 3},
	)
	l.SetSize(100, 6)

	// Scroll so the first visible item (b) is clipped by 2 lines:
	// viewport shows b's last line + c + d + e's top.
	l.ScrollToIndex(1)
	l.offsetLine = 2 // simulate a partially clipped first visible item
	require.Equal(t, 1, l.offsetIdx)
	require.Equal(t, 2, l.offsetLine)

	// Delete the first visible item (b). c slides into b's slot and must
	// start at the top of the viewport (offsetLine resets to 0). Without
	// the reset, the stale 2-line clip would shift the viewport up.
	l.RemoveItem(1)
	require.Equal(t, 1, l.offsetIdx, "next item slides into the same slot")
	require.Equal(t, 0, l.offsetLine, "stale clip from the removed item must be dropped")
}

func TestRemoveItem_KeepsOffsetWhenDeletingBelowViewport(t *testing.T) {
	t.Parallel()

	l := NewList()
	l.SetGap(1)
	l.SetItems(
		offsetItem{Versioned: NewVersioned(), label: "a", height: 3},
		offsetItem{Versioned: NewVersioned(), label: "b", height: 3},
		offsetItem{Versioned: NewVersioned(), label: "c", height: 3},
		offsetItem{Versioned: NewVersioned(), label: "d", height: 3},
		offsetItem{Versioned: NewVersioned(), label: "e", height: 3},
	)
	l.SetSize(100, 6)
	l.offsetIdx = 1
	l.offsetLine = 1

	// Delete an item below the viewport; offset must stay put.
	l.RemoveItem(3)
	require.Equal(t, 1, l.offsetIdx)
	require.Equal(t, 1, l.offsetLine, "deleting below viewport must not touch offsetLine")
}

func TestRemoveItem_ClampsWhenDeletingLastVisible(t *testing.T) {
	t.Parallel()

	l := NewList()
	l.SetGap(1)
	l.SetItems(
		offsetItem{Versioned: NewVersioned(), label: "a", height: 3},
		offsetItem{Versioned: NewVersioned(), label: "b", height: 3},
	)
	l.SetSize(100, 6)
	l.offsetIdx = 1 // viewport shows only the last item

	l.RemoveItem(1) // delete the only visible item
	require.Equal(t, 0, l.offsetIdx, "offset clamps to the new last item")
	require.Equal(t, 0, l.offsetLine)
}
