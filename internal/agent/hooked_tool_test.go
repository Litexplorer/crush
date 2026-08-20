package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/stretchr/testify/require"
)

// fakeTool records the context it was invoked with so tests can assert on
// values stamped onto it by the hookedTool decorator.
type fakeTool struct {
	name   string
	called bool
	gotCtx context.Context
	resp   fantasy.ToolResponse
}

func (f *fakeTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: f.name}
}

func (f *fakeTool) Run(ctx context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	f.called = true
	f.gotCtx = ctx
	return f.resp, nil
}

func (f *fakeTool) ProviderOptions() fantasy.ProviderOptions     { return nil }
func (f *fakeTool) SetProviderOptions(_ fantasy.ProviderOptions) {}

// newRunner builds a hooks.Runner from a single HookConfig, running the
// config-loader path that compiles the matcher regex.
func newRunner(t *testing.T, cmd string) *hooks.Runner {
	t.Helper()
	cfg := &config.Config{
		Hooks: map[string][]config.HookConfig{
			hooks.EventPreToolUse: {{Command: cmd}},
		},
	}
	require.NoError(t, cfg.ValidateHooks())
	return hooks.NewRunner(cfg.Hooks[hooks.EventPreToolUse], t.TempDir(), t.TempDir())
}

// newPostRunner builds a PostToolUse-only runner from a single HookConfig.
func newPostRunner(t *testing.T, cmd string) *hooks.Runner {
	t.Helper()
	cfg := &config.Config{
		Hooks: map[string][]config.HookConfig{
			hooks.EventPostToolUse: {{Command: cmd}},
		},
	}
	require.NoError(t, cfg.ValidateHooks())
	return hooks.NewRunner(cfg.Hooks[hooks.EventPostToolUse], t.TempDir(), t.TempDir())
}

func TestHookedTool_AllowStampsHookApproval(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "view", resp: fantasy.NewTextResponse("ok")}
	runner := newRunner(t, `echo '{"decision":"allow"}'`)
	tool := newHookedTool(inner, runner, nil)

	_, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-1", Name: "view"})
	require.NoError(t, err)
	require.True(t, inner.called, "inner tool should have run")

	// The inner tool's permission service can now treat call-1 as pre-approved.
	svc := permission.NewPermissionService(t.TempDir(), false, nil)
	granted, err := svc.Request(inner.gotCtx, permission.CreatePermissionRequest{
		SessionID:  "s1",
		ToolCallID: "call-1",
		ToolName:   "view",
		Action:     "read",
		Path:       t.TempDir(),
	})
	require.NoError(t, err)
	require.True(t, granted, "hook allow should bypass the permission prompt")
}

func TestHookedTool_SilentDoesNotStampApproval(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "view", resp: fantasy.NewTextResponse("ok")}
	runner := newRunner(t, `exit 0`) // no stdout, no decision
	tool := newHookedTool(inner, runner, nil)

	_, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-2", Name: "view"})
	require.NoError(t, err)
	require.True(t, inner.called)

	// With no hook opinion, a fresh permission request has nothing stamped
	// and must fall through to the normal flow. We verify by checking that
	// the context does not look pre-approved for this call ID: sending a
	// request that no subscriber resolves will block until cancelled.
	svc := permission.NewPermissionService(t.TempDir(), false, nil)
	ctx, cancel := context.WithCancel(inner.gotCtx)
	cancel()
	granted, err := svc.Request(ctx, permission.CreatePermissionRequest{
		SessionID:  "s1",
		ToolCallID: "call-2",
		ToolName:   "view",
		Action:     "read",
		Path:       t.TempDir(),
	})
	require.Error(t, err, "no approval stamped => request should reach the prompt path")
	require.False(t, granted)
}

func TestHookedTool_DenySkipsInnerTool(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "bash"}
	runner := newRunner(t, `echo "blocked" >&2; exit 2`)
	tool := newHookedTool(inner, runner, nil)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-3", Name: "bash"})
	require.NoError(t, err)
	require.False(t, inner.called, "denied call must not reach the inner tool")
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "blocked")
}

func TestWrapToolsWithHooks(t *testing.T) {
	t.Parallel()

	runner := newRunner(t, `exit 0`)
	inputs := []fantasy.AgentTool{&fakeTool{name: "a"}, &fakeTool{name: "b"}}

	t.Run("top-level agent wraps every tool", func(t *testing.T) {
		t.Parallel()
		out := wrapToolsWithHooks(inputs, runner, nil, false)
		require.Len(t, out, len(inputs))
		for i, tool := range out {
			_, ok := tool.(*hookedTool)
			require.Truef(t, ok, "tool %d should be a *hookedTool", i)
		}
	})

	t.Run("sub-agent skips the wrap", func(t *testing.T) {
		t.Parallel()
		out := wrapToolsWithHooks(inputs, runner, nil, true)
		require.Equal(t, inputs, out, "sub-agent tools should be returned unwrapped")
		for _, tool := range out {
			_, isHooked := tool.(*hookedTool)
			require.False(t, isHooked, "sub-agent tool should not be wrapped")
		}
	})

	t.Run("nil runner skips the wrap for both agent kinds", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, inputs, wrapToolsWithHooks(inputs, nil, nil, false))
		require.Equal(t, inputs, wrapToolsWithHooks(inputs, nil, nil, true))
	})
}

func TestHookedTool_PostToolUse_AppendsContext(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "write", resp: fantasy.NewTextResponse("done")}
	post := newPostRunner(t, `echo '{"context":"post-hook saw the tool run"}'`)
	tool := newHookedTool(inner, nil, post)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-4", Name: "write"})
	require.NoError(t, err)
	require.True(t, inner.called)
	require.Contains(t, resp.Content, "done")
	require.Contains(t, resp.Content, "post-hook saw the tool run")
	require.False(t, resp.StopTurn, "plain context must not halt the turn")
}

func TestHookedTool_PostToolUse_HaltStopsTurn(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "bash", resp: fantasy.NewTextResponse("ok")}
	post := newPostRunner(t, `echo "turn must stop" >&2; exit 49`)
	tool := newHookedTool(inner, nil, post)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-5", Name: "bash"})
	require.NoError(t, err)
	require.True(t, inner.called, "inner tool must have run before the post hook")
	require.True(t, resp.StopTurn, "halt should stop the turn")
	require.Contains(t, resp.Content, "ok", "tool output must survive the halt")
}

func TestHookedTool_PostToolUse_DenyIgnored(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "bash", resp: fantasy.NewTextResponse("ok")}
	post := newPostRunner(t, `echo "too late to block" >&2; exit 2`)
	tool := newHookedTool(inner, nil, post)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-6", Name: "bash"})
	require.NoError(t, err)
	require.True(t, inner.called)
	require.False(t, resp.StopTurn, "post-tool deny must not stop the turn")
	require.Contains(t, resp.Content, "ok", "tool output must be unaffected")
}

func TestHookedTool_PostToolUse_NoPostRunnerIsNoop(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "view", resp: fantasy.NewTextResponse("ok")}
	tool := newHookedTool(inner, nil, nil)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-7", Name: "view"})
	require.NoError(t, err)
	require.True(t, inner.called)
	require.Equal(t, "ok", resp.Content)
	require.False(t, resp.StopTurn)
}

func TestHookedTool_PostToolUse_HookErrorNonBlocking(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "bash", resp: fantasy.NewTextResponse("ok")}
	post := newPostRunner(t, `echo "boom" >&2; exit 3`)
	tool := newHookedTool(inner, nil, post)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-8", Name: "bash"})
	require.NoError(t, err)
	require.True(t, inner.called)
	require.Contains(t, resp.Content, "ok", "hook failure must not affect tool output")
	require.False(t, resp.StopTurn)
}

func TestHookedTool_PostToolUse_ReceivesToolResponse(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "bash", resp: fantasy.NewTextResponse("the-tool-output")}
	// The hook reads tool_response from stdin and echoes part of it back
	// as context so the test can assert the payload reached the hook.
	post := newPostRunner(t, `python3 -c 'import json,sys; p=json.load(sys.stdin); print(json.dumps({"context": p["tool_response"]["content"]}))'`)
	tool := newHookedTool(inner, nil, post)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-9", Name: "bash"})
	require.NoError(t, err)
	require.True(t, inner.called)
	require.Contains(t, resp.Content, "the-tool-output", "hook should see tool output via tool_response")
}
