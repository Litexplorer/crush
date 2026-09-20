package anim

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestFrameInterval covers the shared animation clock: every Anim in the
// UI is driven by a single tea.Tick chain at this interval.
func TestFrameInterval(t *testing.T) {
	t.Parallel()
	require.Equal(t, time.Second/time.Duration(fps), FrameInterval())
}

// TestAdvanceMovesStep verifies that each Advance call advances the frame
// step counter and wraps it so the prerendered frames loop.
func TestAdvanceMovesStep(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "test", Size: 5})

	require.Equal(t, int64(0), a.framesSinceStart.Load())
	for range prerenderedFrames * 3 {
		require.True(t, a.Advance(), "an advance must report that output changed")
	}
	require.Equal(t, int64(prerenderedFrames*3), a.framesSinceStart.Load(),
		"every frame must be counted")
	require.Less(t, int(a.step.Load()), len(a.cyclingFrames),
		"step must wrap within the prerendered frame range")
}

// TestAdvanceInitializesBirth verifies that the birth animation completes
// after maxBirthSteps frames and that the ellipsis only animates once all
// characters have been initialized.
func TestAdvanceInitializesBirth(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "test", Size: 5, Label: "Generating"})
	require.False(t, a.initialized.Load())

	ellipsisBefore := int(a.ellipsisStep.Load())
	for range maxBirthSteps - 1 {
		a.Advance()
	}
	require.False(t, a.initialized.Load(), "birth must not complete before maxBirthSteps frames")

	a.Advance()
	require.True(t, a.initialized.Load())

	for i := 1; i <= ellipsisAnimSpeed*2; i++ {
		a.Advance()
	}
	require.NotEqual(t, ellipsisBefore, int(a.ellipsisStep.Load()),
		"the ellipsis must animate once initialized")
}

// TestAdvanceIndependentInstances verifies that two Anim instances advance
// their own counters; the shared clock simply calls Advance on each.
func TestAdvanceIndependentInstances(t *testing.T) {
	t.Parallel()

	a1 := New(Settings{ID: "a1", Size: 5})
	a2 := New(Settings{ID: "a2", Size: 5})

	a1.Advance()
	require.Equal(t, int64(1), a1.framesSinceStart.Load())
	require.Equal(t, int64(0), a2.framesSinceStart.Load())

	a2.Advance()
	require.Equal(t, int64(1), a2.framesSinceStart.Load())
}

// TestSuffixRendersBeforeLabel guards the layout of an animated line that
// carries a dynamic suffix: the suffix hugs the cycling characters and the
// label follows it, so a live metric is not pushed past a long label.
func TestSuffixRendersBeforeLabel(t *testing.T) {
	t.Parallel()

	newSettled := func(suffix func() string) *Anim {
		a := New(Settings{ID: "test-suffix", Size: 3, Label: "Thinking", Suffix: suffix})
		// Skip the staggered birth animation so every character is present.
		a.framesSinceStart.Store(int64(maxBirthSteps))
		a.initialized.Store(true)
		return a
	}

	got := ansi.Strip(newSettled(func() string { return "42 tok/s" }).Render())
	require.Contains(t, got, "42 tok/s")
	require.Contains(t, got, "Thinking")
	require.Less(t, strings.Index(got, "42 tok/s"), strings.Index(got, "Thinking"),
		"the suffix must sit to the right of the marquee, ahead of the label")

	empty := ansi.Strip(newSettled(func() string { return "" }).Render())
	require.NotContains(t, empty, "tok/s")
	require.Contains(t, empty, "Thinking")
}
