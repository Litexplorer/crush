package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSubAgentToolListExcludesAgentTool is a regression test for a runaway
// goroutine/CPU bug: the TUI pegged every core (thousands of goroutines all
// queued on one mutex) because the task sub-agent was allowed to spawn
// further task sub-agents.
//
// buildAgent kicks off its tool-list build on the readiness errgroup, and
// buildTools gated the `agent` tool on agent.AllowedTools alone, without
// consulting isSubAgent. agentTool builds a *task* agent, and that task
// agent's tool build passes through the same gate, so the moment the task
// allowlist contains "agent" the build recurses asynchronously without
// bound. Upstream only avoids this by accident: its task agent uses a
// read-only allowlist that happens not to contain "agent".
//
// The contract asserted here is explicit: a sub-agent's tool list never
// contains the agent tool, whatever its allowlist says.
func TestSubAgentToolListExcludesAgentTool(t *testing.T) {
	env := testEnv(t)

	crushJSON := `{
  "options": {"disable_default_providers": true, "disable_provider_auto_update": true},
  "providers": {"mock": {"id": "mock", "name": "Mock", "type": "openai",
    "base_url": "http://127.0.0.1:9/v1", "api_key": "test-key",
    "models": [{"id": "mock-model", "name": "Mock", "context_window": 8192, "default_max_tokens": 128}]}},
  "models": {"large": {"provider": "mock", "model": "mock-model"},
             "small": {"provider": "mock", "model": "mock-model"}}
}`
	require.NoError(t, os.WriteFile(filepath.Join(env.workingDir, "crush.json"), []byte(crushJSON), 0o644))

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	coord := &coordinator{
		cfg:         cfg,
		sessions:    env.sessions,
		messages:    env.messages,
		permissions: env.permissions,
		history:     env.history,
		filetracker: *env.filetracker,
	}

	taskCfg := cfg.Config().Agents[config.AgentTask]
	require.Contains(t, taskCfg.AllowedTools, AgentToolName,
		"precondition: the task allowlist carries the agent tool, which is what made the build recurse")

	list, err := coord.buildTools(context.Background(), taskCfg, true)
	require.NoError(t, err)

	for _, tool := range list {
		require.NotEqual(t, AgentToolName, tool.Info().Name,
			"a sub-agent must not be offered the agent tool: it would spawn sub-agents without bound")
	}
}
