package model

import (
	"strconv"
	"testing"

	"github.com/charmbracelet/crush/internal/ui/chat"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

// TestManualScrollSuppressesFollow verifies that manual scroll mode keeps the
// view pinned where the user left it: follow is suppressed even when the view
// is at the bottom, and the Draw re-anchor does not pull the view back down as
// streaming content grows.
func TestManualScrollSuppressesFollow(t *testing.T) {
	t.Parallel()

	u := newTestUI()

	msgs := make([]chat.MessageItem, 0, 60)
	for i := range 59 {
		msgs = append(msgs, testMessageItem{
			id:   "m-" + strconv.Itoa(i),
			text: "message " + strconv.Itoa(i),
		})
	}
	streaming := &mutableMessageItem{id: "streaming", lines: 1, version: 1}
	msgs = append(msgs, streaming)
	u.chat.SetMessages(msgs...)
	u.updateLayoutAndSize()

	u.chat.ScrollToBottom()
	require.True(t, u.chat.AtBottom())

	u.chat.SetManualScroll(true)
	require.False(t, u.chat.Follow(),
		"manual scroll mode must suppress follow even when pinned to the bottom")

	// Simulate streaming: grow the last item's height. Without manual scroll
	// the Draw re-anchor would keep the view pinned to the bottom.
	streaming.lines = 20
	streaming.version++

	scr := uv.NewScreenBuffer(u.width, u.height)
	u.chat.Draw(scr, u.layout.main)

	require.False(t, u.chat.AtBottom(),
		"with manual scroll the view must not re-anchor to the bottom as content grows")
}

// TestManualScrollKeepsExplicitScrollFunctional verifies that explicit user
// scroll actions (scroll to bottom/top) still work while manual scroll mode is
// enabled; only automatic follow is suppressed.
func TestManualScrollKeepsExplicitScrollFunctional(t *testing.T) {
	t.Parallel()

	u := newTestUI()

	msgs := make([]chat.MessageItem, 0, 60)
	for i := range 60 {
		msgs = append(msgs, testMessageItem{
			id:   "m-" + strconv.Itoa(i),
			text: "message " + strconv.Itoa(i),
		})
	}
	u.chat.SetMessages(msgs...)
	u.updateLayoutAndSize()

	u.chat.SetManualScroll(true)
	u.chat.ScrollToTop()
	require.False(t, u.chat.Follow())
	require.False(t, u.chat.AtBottom())

	u.chat.ScrollToBottom()
	require.True(t, u.chat.AtBottom(), "explicit scroll to bottom must still work in manual scroll mode")
	require.False(t, u.chat.Follow(), "follow must stay suppressed even after scrolling to the bottom")
}

// TestManualScrollToggleFollowSeam verifies the toggle boundary: disabling
// manual scroll restores follow reporting immediately.
func TestManualScrollToggleFollowSeam(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.ScrollToBottom() // enter follow mode
	require.True(t, u.chat.Follow())

	u.chat.SetManualScroll(true)
	require.False(t, u.chat.Follow())

	u.chat.SetManualScroll(false)
	require.True(t, u.chat.Follow(), "disabling manual scroll must restore follow reporting")
}
