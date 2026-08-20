package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// hookedTool wraps a fantasy.AgentTool to run PreToolUse hooks before
// delegating to the inner tool, and PostToolUse hooks after it returns.
type hookedTool struct {
	inner      fantasy.AgentTool
	runner     *hooks.Runner
	postRunner *hooks.Runner
}

func newHookedTool(inner fantasy.AgentTool, runner, postRunner *hooks.Runner) *hookedTool {
	return &hookedTool{inner: inner, runner: runner, postRunner: postRunner}
}

// wrapToolsWithHooks returns a tool slice with each entry wrapped in a
// hookedTool. Returns the original slice unchanged when both runners are
// nil or when isSubAgent is true — sub-agents never fire hooks, the
// top-level invocation of the sub-agent tool itself is wrapped on the
// caller's side.
func wrapToolsWithHooks(tools []fantasy.AgentTool, runner, postRunner *hooks.Runner, isSubAgent bool) []fantasy.AgentTool {
	if (runner == nil && postRunner == nil) || isSubAgent {
		return tools
	}
	out := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		out[i] = newHookedTool(tool, runner, postRunner)
	}
	return out
}

func (h *hookedTool) Info() fantasy.ToolInfo {
	return h.inner.Info()
}

func (h *hookedTool) ProviderOptions() fantasy.ProviderOptions {
	return h.inner.ProviderOptions()
}

func (h *hookedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	h.inner.SetProviderOptions(opts)
}

func (h *hookedTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	sessionID := tools.GetSessionFromContext(ctx)

	// PreToolUse is optional (e.g. only PostToolUse hooks configured).
	var result hooks.AggregateResult
	if h.runner != nil {
		var err error
		result, err = h.runner.Run(ctx, hooks.EventPreToolUse, sessionID, call.Name, call.Input)
		if err != nil {
			slog.Warn("Hook execution error, proceeding with tool call",
				"tool", call.Name, "error", err)
		}
	}

	if result.Decision == hooks.DecisionDeny || result.Halt {
		reason := fmt.Sprintf("Tool call blocked by hook. Reason: %s", result.Reason)
		if result.Halt {
			reason = fmt.Sprintf("Turn halted by hook. Reason: %s", result.Reason)
		}
		resp := fantasy.NewTextErrorResponse(reason)
		// Halt ends the whole turn; a plain deny only blocks this tool
		// call so the model can see the error and try something else.
		resp.StopTurn = result.Halt
		resp.Metadata = hookMetadataJSON(result)
		return resp, nil
	}

	if result.UpdatedInput != "" {
		call.Input = result.UpdatedInput
	}

	// An explicit allow from a hook pre-approves the permission prompt for
	// this tool call. Deny is already handled above; silence falls through
	// to the normal permission flow.
	if result.Decision == hooks.DecisionAllow {
		ctx = permission.WithHookApproval(ctx, call.ID)
	}

	resp, err := h.inner.Run(ctx, call)
	if err != nil {
		return resp, err
	}

	if h.postRunner != nil {
		postResult, postErr := h.postRunner.RunWithToolResponse(ctx, hooks.EventPostToolUse, sessionID, call.Name, call.Input, buildToolResponseJSON(resp, err))
		if postErr != nil {
			slog.Warn("PostToolUse hook execution error",
				"tool", call.Name, "error", postErr)
		} else if postResult.HookCount > 0 {
			// PostToolUse hooks cannot block the tool (it already ran),
			// so only halt and context are honored. Deny, updated_input
			// and the block exit code are ignored at this stage.
			if postResult.Halt {
				resp.StopTurn = true
			}
			if postResult.Context != "" {
				if resp.Content != "" {
					resp.Content += "\n"
				}
				resp.Content += postResult.Context
			}
			resp.Metadata = mergeHookMetadata(resp.Metadata, postResult)
			slog.Debug("PostToolUse hooks completed",
				"tool", call.Name, "hooks", postResult.HookCount, "halt", postResult.Halt)
		}
	}

	if result.Context != "" {
		if resp.Content != "" {
			resp.Content += "\n"
		}
		resp.Content += result.Context
	}

	resp.Metadata = mergeHookMetadata(resp.Metadata, result)
	return resp, nil
}

// maxToolResponseContent caps how much of a tool's output is passed to
// PostToolUse hooks. Oversized output is truncated with a marker so hooks
// stay fast and payloads stay small.
const maxToolResponseContent = 200 * 1024

// buildToolResponseJSON renders a tool's result as the JSON object passed
// to PostToolUse hooks in the payload's tool_response field.
func buildToolResponseJSON(resp fantasy.ToolResponse, err error) string {
	content := resp.Content
	truncated := false
	if len(content) > maxToolResponseContent {
		content = content[:maxToolResponseContent]
		truncated = true
	}

	var errMsg string
	if err != nil {
		errMsg = err.Error()
	}

	data, marshalErr := json.Marshal(map[string]any{
		"content":   content,
		"error":     errMsg,
		"metadata":  resp.Metadata,
		"truncated": truncated,
	})
	if marshalErr != nil {
		return ""
	}
	return string(data)
}

// buildHookMetadata creates a HookMetadata from an AggregateResult.
func buildHookMetadata(result hooks.AggregateResult) hooks.HookMetadata {
	return hooks.HookMetadata{
		HookCount:    result.HookCount,
		Decision:     result.Decision.String(),
		Halt:         result.Halt,
		Reason:       result.Reason,
		InputRewrite: result.UpdatedInput != "",
		Hooks:        result.Hooks,
	}
}

// hookMetadataJSON builds a JSON string containing only the hook metadata.
func hookMetadataJSON(result hooks.AggregateResult) string {
	meta := buildHookMetadata(result)
	data, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return `{"hook":` + string(data) + `}`
}

// mergeHookMetadata injects hook metadata into existing tool metadata.
// Successive calls accumulate: hooks from later results are appended to
// the existing hook list rather than replacing it, so PreToolUse and
// PostToolUse indicators both survive on the same tool call.
func mergeHookMetadata(existing string, result hooks.AggregateResult) string {
	if result.HookCount == 0 {
		return existing
	}
	meta := buildHookMetadata(result)
	if existing == "" {
		existing = "{}"
	}

	// A hook object already exists — append our hooks to it instead of
	// replacing the whole key.
	if gjson.Get(existing, "hook").Exists() {
		hookCount := gjson.Get(existing, "hook.hook_count").Int() + int64(meta.HookCount)
		out := existing
		for _, hi := range meta.Hooks {
			hiJSON, err := json.Marshal(hi)
			if err != nil {
				continue
			}
			out, err = sjson.SetRaw(out, "hook.hooks.-1", string(hiJSON))
			if err != nil {
				return existing
			}
		}
		out, err := sjson.Set(out, "hook.hook_count", hookCount)
		if err != nil {
			return existing
		}
		return out
	}

	data, err := json.Marshal(meta)
	if err != nil {
		return existing
	}
	merged, err := sjson.SetRaw(existing, "hook", string(data))
	if err != nil {
		return existing
	}
	return merged
}
