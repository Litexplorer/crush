package list

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// filterableStub is a minimal FilterableItem: trackedItem supplies Item
// (Render/Finished) and Filter gives the fuzzy-match text.
type filterableStub struct {
	*trackedItem
}

func (s filterableStub) Filter() string { return s.body }

func newFilterableStub(body string) filterableStub {
	return filterableStub{newTrackedItem(body, body, true)}
}

// TestFilterableList_FirstRenderShowsItems guards a poisoned-scroll-offset
// bug: constructing a FilterableList with no items calls List.SetItems with
// an empty slice, which clamped the offset to min(0, -1) == -1. Later
// SetItems calls only ever preserved that -1 (min(-1, n-1) == -1), and
// List.Render stops at the first item when the index is negative, so the
// list rendered blank until something called ScrollToTop — in the TUI that
// meant the dialog looked empty until the user typed a character.
func TestFilterableList_FirstRenderShowsItems(t *testing.T) {
	f := NewFilterableList()
	require.GreaterOrEqual(t, f.offsetIdx, 0,
		"the scroll offset must never go negative; an empty construction used to leave it at -1")

	f.SetItems(newFilterableStub("alpha"), newFilterableStub("beta"))
	f.SetSize(40, 10)

	out := f.Render()
	require.Contains(t, out, "alpha", "first render must show the items already in the list")
	require.Contains(t, out, "beta")
}
