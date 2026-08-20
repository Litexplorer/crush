package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// bothRunners builds a tool with PreToolUse and PostToolUse runners.
func bothRunners(t *testing.T, preCmd, postCmd string) *hookedTool {
	t.Helper()
	cfg := &config.Config{Hooks: map[string][]config.HookConfig{
		hooks.EventPreToolUse:  {{Command: preCmd}},
		hooks.EventPostToolUse: {{Command: postCmd}},
	}}
	require.NoError(t, cfg.ValidateHooks())
	pre := hooks.NewRunner(cfg.Hooks[hooks.EventPreToolUse], t.TempDir(), t.TempDir())
	post := hooks.NewRunner(cfg.Hooks[hooks.EventPostToolUse], t.TempDir(), t.TempDir())
	return newHookedTool(&fakeTool{name: "bash", resp: fantasy.NewTextResponse("ok")}, pre, post)
}

func TestHookedTool_Metadata_MergesPreAndPost(t *testing.T) {
	t.Parallel()

	tool := bothRunners(t, `echo '{"context":"pre"}'`, `echo '{"context":"post"}'`)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-m1", Name: "bash"})
	require.NoError(t, err)

	hookCount := gjson.Get(resp.Metadata, "hook.hook_count").Int()
	require.Equal(t, int64(2), hookCount, "both pre and post hooks must be counted")
	require.True(t, gjson.Get(resp.Metadata, `hook.hooks.#(name=~"pre")`).Exists() ||
		len(gjson.Get(resp.Metadata, "hook.hooks").Array()) == 2, "two hook entries expected")
	require.Len(t, gjson.Get(resp.Metadata, "hook.hooks").Array(), 2)
}

func TestHookedTool_Metadata_SinglePostRunner(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "view", resp: fantasy.NewTextResponse("content")}
	post := newPostRunner(t, `echo '{"context":"post only"}'`)
	tool := newHookedTool(inner, nil, post)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-m2", Name: "view"})
	require.NoError(t, err)
	require.Equal(t, int64(1), gjson.Get(resp.Metadata, "hook.hook_count").Int())
	require.Len(t, gjson.Get(resp.Metadata, "hook.hooks").Array(), 1)
}

func TestHookedTool_Metadata_NoHooks(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "view", resp: fantasy.NewTextResponse("content")}
	tool := newHookedTool(inner, nil, nil)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-m3", Name: "view"})
	require.NoError(t, err)
	require.NotContains(t, resp.Metadata, "hook", "no hooks configured => no hook metadata")
}
