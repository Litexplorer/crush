package acp

import (
	"context"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/config"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

// seedUsageModel gives the harness workspace a large model with a known
// context window and seeds the session's token usage and cost, so a
// usage_update payload has deterministic values.
func seedUsageModel(t *testing.T, a *Agent, sessionID acpsdk.SessionId, promptTokens, completionTokens, cost float64, estimated bool) {
	t.Helper()

	a.mu.Lock()
	sess := a.sessions[sessionID]
	a.mu.Unlock()
	require.NotNil(t, sess)

	cfg := sess.workspace.Cfg.Config()
	cfg.Providers.Set("usage-test", config.ProviderConfig{
		ID:     "usage-test",
		Name:   "Usage Test",
		Models: []catwalk.Model{{ID: "usage-model", ContextWindow: 200_000}},
	})
	sess.workspace.Cfg.OverridePreferredModel(
		config.SelectedModelTypeLarge,
		config.SelectedModel{Provider: "usage-test", Model: "usage-model"},
	)

	crushSess, err := sess.workspace.App.Sessions.Get(context.Background(), sess.sessionID)
	require.NoError(t, err)
	crushSess.PromptTokens = int64(promptTokens)
	crushSess.CompletionTokens = int64(completionTokens)
	crushSess.Cost = cost
	crushSess.EstimatedUsage = estimated
	_, err = sess.workspace.App.Sessions.Save(context.Background(), crushSess)
	require.NoError(t, err)
}

// usageUpdates returns the usage_update notifications received by cap.
func usageUpdates(cap *captureClient) []acpsdk.SessionUsageUpdate {
	cap.mu.Lock()
	defer cap.mu.Unlock()
	var out []acpsdk.SessionUsageUpdate
	for _, u := range cap.updates {
		if u.Update.UsageUpdate != nil {
			out = append(out, *u.Update.UsageUpdate)
		}
	}
	return out
}

func TestPromptSendsUsageUpdate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	scripted := &scriptedCoordinator{chunks: []string{"hi"}}
	a, sessionID := newPromptHarness(t, t.TempDir(), scripted)
	seedUsageModel(t, a, sessionID, 1_000, 2_000, 0.5, false)

	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)

	_, err := a.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(usageUpdates(cap)) == 1
	}, 3*time.Second, 20*time.Millisecond)

	usage := usageUpdates(cap)
	require.Equal(t, 1, len(usage))
	require.Equal(t, 3_000, usage[0].Used)
	require.Equal(t, 200_000, usage[0].Size)
	require.NotNil(t, usage[0].Cost)
	require.Equal(t, 0.5, usage[0].Cost.Amount)
	require.Equal(t, "USD", usage[0].Cost.Currency)
}

func TestPromptUsageUpdateSkipsWithoutModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	scripted := &scriptedCoordinator{chunks: []string{"hi"}}
	a, sessionID := newPromptHarness(t, t.TempDir(), scripted)

	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)

	_, err := a.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.NoError(t, err)

	// Wait past the prompt so a spurious update would have been sent.
	time.Sleep(200 * time.Millisecond)
	require.Empty(t, usageUpdates(cap))
}

func TestPromptUsageUpdateEstimatedUsage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	scripted := &scriptedCoordinator{chunks: []string{"hi"}}
	a, sessionID := newPromptHarness(t, t.TempDir(), scripted)
	seedUsageModel(t, a, sessionID, 7, 3, 0, true)

	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)

	_, err := a.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(usageUpdates(cap)) == 1
	}, 3*time.Second, 20*time.Millisecond)

	usage := usageUpdates(cap)
	require.Equal(t, 10, usage[0].Used)
	require.Equal(t, 200_000, usage[0].Size)
}

func TestResumeSessionSendsUsageUpdate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	ctx := context.Background()
	cwd := t.TempDir()

	// First connection: create a session, persist a large model with a
	// known context window plus token usage, then close so the second
	// connection must restore everything from disk.
	a := newEnvAgent(t, "")
	sid := newSessionOn(t, a, cwd)
	a.mu.Lock()
	sess := a.sessions[sid]
	a.mu.Unlock()
	require.NoError(t, sess.workspace.Cfg.SetConfigFields(config.ScopeWorkspace, map[string]any{
		"providers.openai": config.ProviderConfig{
			ID:     "openai",
			Name:   "OpenAI",
			Models: []catwalk.Model{{ID: "usage-model", ContextWindow: 200_000}},
		},
		"providers.openai.api_key": "test-key",
		"models.large":             config.SelectedModel{Provider: "openai", Model: "usage-model"},
	}))
	crushSess, err := sess.workspace.App.Sessions.Get(ctx, sess.sessionID)
	require.NoError(t, err)
	crushSess.PromptTokens = 400
	crushSess.CompletionTokens = 600
	_, err = sess.workspace.App.Sessions.Save(ctx, crushSess)
	require.NoError(t, err)
	dd := agentDataDir(t, a, sid)
	closeSessionOn(t, a, sid)

	// A fresh connection resumes the session and the client receives
	// the current usage right away, before any prompt runs.
	b := NewAgent(backend.New(context.Background(), nil, nil), dd)
	t.Cleanup(b.CloseAll)
	cap := &captureClient{}
	cleanup := connectPeers(b, cap)
	t.Cleanup(cleanup)
	_, err = b.ResumeSession(ctx, acpsdk.ResumeSessionRequest{
		Cwd:        cwd,
		SessionId:  sid,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(usageUpdates(cap)) == 1
	}, 3*time.Second, 20*time.Millisecond)

	usage := usageUpdates(cap)
	require.Equal(t, 1, len(usage))
	require.Equal(t, 1_000, usage[0].Used)
	require.Equal(t, 200_000, usage[0].Size)
	require.NotNil(t, usage[0].Cost)
	require.Equal(t, 0.0, usage[0].Cost.Amount)
}
