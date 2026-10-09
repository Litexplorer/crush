package model

import (
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// headerTestCommon builds a Common whose coder agent resolves to a model with
// a known context window, so the header renders a context percentage.
func headerTestCommon(t *testing.T) *common.Common {
	t.Helper()

	const provider = "test-provider"

	cfg := &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Agents: map[string]config.Agent{
			config.AgentCoder: {Model: config.SelectedModelTypeLarge},
		},
		Models: map[config.SelectedModelType]config.SelectedModel{
			config.SelectedModelTypeLarge: {Provider: provider, Model: "test-model"},
		},
	}
	cfg.Providers.Set(provider, config.ProviderConfig{
		Models: []catwalk.Model{{ID: "test-model", ContextWindow: 200_000}},
	})

	return common.DefaultCommon(&testWorkspace{cfg: cfg})
}

// TestRenderHeaderDetailsAppendsSessionTotalAfterPercentage pins the ordering
// the header relies on: context percentage first, session lifetime total after.
func TestRenderHeaderDetailsAppendsSessionTotalAfterPercentage(t *testing.T) {
	t.Parallel()

	sess := &session.Session{
		ID:               "s1",
		PromptTokens:     60_000,
		CompletionTokens: 24_000,
		TotalTokens:      1_234_000,
	}

	out := ansi.Strip(renderHeaderDetails(headerTestCommon(t), sess, 0, false, 200, nil, ""))

	require.Contains(t, out, "42%")
	require.Contains(t, out, "1.2M")
	require.Less(t, strings.Index(out, "42%"), strings.Index(out, "1.2M"),
		"lifetime total must render after the context percentage")
	require.Equal(t, 3, strings.Count(out, " • "), "cwd, percentage and total are separate fields")
}

// TestRenderHeaderDetailsOmitsZeroSessionTotal keeps the header clean for
// sessions that have not reported usage yet.
func TestRenderHeaderDetailsOmitsZeroSessionTotal(t *testing.T) {
	t.Parallel()

	sess := &session.Session{
		ID:               "s1",
		PromptTokens:     60_000,
		CompletionTokens: 24_000,
	}

	out := ansi.Strip(renderHeaderDetails(headerTestCommon(t), sess, 0, false, 200, nil, ""))

	require.Contains(t, out, "42%")
	require.Equal(t, 2, strings.Count(out, " • "))
}

// TestRenderHeaderDetailsMarksEstimatedTotal verifies the lifetime total
// carries the same "~" marker as the context percentage when usage is
// estimated rather than reported by the provider.
func TestRenderHeaderDetailsMarksEstimatedTotal(t *testing.T) {
	t.Parallel()

	sess := &session.Session{
		ID:               "s1",
		PromptTokens:     60_000,
		CompletionTokens: 24_000,
		TotalTokens:      1_234_000,
		EstimatedUsage:   true,
	}

	out := ansi.Strip(renderHeaderDetails(headerTestCommon(t), sess, 0, false, 200, nil, ""))

	require.Contains(t, out, "~42%")
	require.Contains(t, out, "~1.2M")
}
