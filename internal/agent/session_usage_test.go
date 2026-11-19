package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// TestUpdateSessionTokenCountersAccumulatesCache pins the prompt cache split:
// the cache buckets are running sums (a hit rate needs totals), unlike the
// context counters which describe the latest request only.
func TestUpdateSessionTokenCountersAccumulatesCache(t *testing.T) {
	t.Parallel()

	s := &session.Session{}

	updateSessionTokenCounters(s, fantasy.Usage{
		InputTokens:         1_000,
		CacheReadTokens:     8_000,
		CacheCreationTokens: 200,
		OutputTokens:        50,
	})
	require.Equal(t, int64(8_000), s.CacheReadTokens)
	require.Equal(t, int64(200), s.CacheWriteTokens)
	require.Equal(t, int64(1_000), s.UncachedInputTokens)

	updateSessionTokenCounters(s, fantasy.Usage{
		InputTokens:     2_000,
		CacheReadTokens: 9_000,
		OutputTokens:    100,
	})

	require.Equal(t, int64(17_000), s.CacheReadTokens)
	require.Equal(t, int64(200), s.CacheWriteTokens)
	require.Equal(t, int64(3_000), s.UncachedInputTokens)
}

// TestUpdateSessionTokenCountersAccumulatesTotal pins the split between the
// per-request counters (which drive the context percentage) and the session
// lifetime total.
func TestUpdateSessionTokenCountersAccumulatesTotal(t *testing.T) {
	t.Parallel()

	s := &session.Session{}

	updateSessionTokenCounters(s, fantasy.Usage{InputTokens: 1000, CacheReadTokens: 200, OutputTokens: 50})

	require.Equal(t, int64(1200), s.PromptTokens)
	require.Equal(t, int64(50), s.CompletionTokens)
	require.Equal(t, int64(1250), s.TotalTokens)

	// The per-request counters are overwritten by the latest request, the
	// lifetime total keeps growing.
	updateSessionTokenCounters(s, fantasy.Usage{InputTokens: 3000, OutputTokens: 400})

	require.Equal(t, int64(3000), s.PromptTokens)
	require.Equal(t, int64(400), s.CompletionTokens)
	require.Equal(t, int64(4650), s.TotalTokens)
}

// TestUpdateSessionTokenCountersTotalSurvivesContextReset mirrors the
// summarization path, which rewrites the context counters but must not lose
// how much the session has consumed overall.
func TestUpdateSessionTokenCountersTotalSurvivesContextReset(t *testing.T) {
	t.Parallel()

	s := &session.Session{}
	updateSessionTokenCounters(s, fantasy.Usage{InputTokens: 1000, OutputTokens: 100})
	require.Equal(t, int64(1100), s.TotalTokens)

	s.PromptTokens = 0
	s.CompletionTokens = 20

	require.Equal(t, int64(1100), s.TotalTokens)
}
