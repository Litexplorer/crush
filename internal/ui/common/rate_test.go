package common

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApproxTokens(t *testing.T) {
	require.EqualValues(t, 0, approxTokens(""))
	// ASCII packs about four characters per token.
	require.EqualValues(t, 1, approxTokens("abcd"))
	require.EqualValues(t, 25, approxTokens(string(make([]byte, 100))))
	// Non-ASCII is close to one token per rune.
	require.EqualValues(t, 4, approxTokens("你好世界"))
}

func TestRateFromSamples(t *testing.T) {
	base := time.Now()

	t.Run("spans the trailing window", func(t *testing.T) {
		// 100 tokens accumulated two seconds ago and still 100 one second
		// ago, 250 now: the last second only saw the 150 token delta.
		samples := []rateSample{
			{at: base.Add(-2 * time.Second), tokens: 100},
			{at: base.Add(-time.Second), tokens: 100},
			{at: base, tokens: 250},
		}
		require.InDelta(t, 150, rateFromSamples(samples, base), 0.5)
	})

	t.Run("ramps up before a full window of history", func(t *testing.T) {
		samples := []rateSample{
			{at: base.Add(-400 * time.Millisecond), tokens: 0},
			{at: base, tokens: 80},
		}
		require.InDelta(t, 200, rateFromSamples(samples, base), 0.5)
	})

	t.Run("decays to zero once the stream stops", func(t *testing.T) {
		// Last output landed 1.5s ago, so nothing is being emitted now.
		samples := []rateSample{
			{at: base.Add(-2 * time.Second), tokens: 0},
			{at: base.Add(-1500 * time.Millisecond), tokens: 50},
		}
		require.InDelta(t, 0, rateFromSamples(samples, base), 0.001)
	})

	t.Run("needs two samples", func(t *testing.T) {
		require.InDelta(t, 0, rateFromSamples([]rateSample{{at: base, tokens: 10}}, base), 0.001)
	})
}

func TestObserveTokens(t *testing.T) {
	StartRate()

	ObserveTokens("abcd", "")
	ObserveTokens("abcd efgh", "")
	require.Empty(t, TokensPerSecond(),
		"a sub-millisecond history must not be divided into a rate")

	time.Sleep(rateMinSpan + 20*time.Millisecond)
	ObserveTokens(strings.Repeat("a", 400), "")

	// ~100 tokens of history over ~520ms, so three digits are expected.
	got := TokensPerSecond()
	require.NotEmpty(t, got)
	require.True(t, strings.HasSuffix(got, " tok/s"), "unexpected format %q", got)
	n, err := strconv.Atoi(strings.TrimSuffix(got, " tok/s"))
	require.NoError(t, err)
	require.Greater(t, n, 0)
	require.Less(t, n, 1000, "burst token counts must be averaged over the window")
}

func TestObserveTokensResetsOnBackwardsJump(t *testing.T) {
	StartRate()

	ObserveTokens("hello world", "thinking")
	ObserveTokens("hello world and more", "thinking further")
	require.Len(t, tokenRate.samples, 2)

	// A retried provider request rewinds the accumulated content, so the
	// window must restart instead of reporting a negative rate.
	ObserveTokens("", "")
	require.Len(t, tokenRate.samples, 1)
}

func TestStartRateClearsWindow(t *testing.T) {
	StartRate()
	ObserveTokens("hello", "")
	ObserveTokens("hello world", "")
	ObserveTokens("hello world again", "")

	StartRate()
	require.Empty(t, tokenRate.samples)
	require.Empty(t, TokensPerSecond())
}
