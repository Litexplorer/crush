package acp

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/message"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

// streamScriptCoordinator is an agent.Coordinator stub that publishes a
// test-scripted sequence of message states through the app's real
// message service, mirroring the production pipeline's reasoning, text,
// tool call, and tool result events (US-020 / US-021).
type streamScriptCoordinator struct {
	app *app.App
	run func(ctx context.Context, sessionID string) error
}

func (c *streamScriptCoordinator) Run(ctx context.Context, sessionID, _ string, _ ...message.Attachment) (*fantasy.AgentResult, error) {
	if err := c.run(ctx, sessionID); err != nil {
		return nil, err
	}
	return &fantasy.AgentResult{}, nil
}

func (c *streamScriptCoordinator) RunAccepted(ctx context.Context, _ *agent.AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return c.Run(ctx, sessionID, prompt, attachments...)
}

func (c *streamScriptCoordinator) BeginAccepted(string) *agent.AcceptedRun       { return nil }
func (c *streamScriptCoordinator) Cancel(string)                                 {}
func (c *streamScriptCoordinator) CancelAll()                                    {}
func (c *streamScriptCoordinator) IsSessionBusy(string) bool                     { return false }
func (c *streamScriptCoordinator) IsBusy() bool                                  { return false }
func (c *streamScriptCoordinator) QueuedPrompts(string) int                      { return 0 }
func (c *streamScriptCoordinator) QueuedPromptsList(string) []string             { return nil }
func (c *streamScriptCoordinator) ClearQueue(string)                             {}
func (c *streamScriptCoordinator) Summarize(context.Context, string) error       { return nil }
func (c *streamScriptCoordinator) Model() agent.Model                            { return agent.Model{} }
func (c *streamScriptCoordinator) UpdateModels(context.Context) error            { return nil }
func (c *streamScriptCoordinator) GenerateTitle(context.Context, string, string) {}

var _ agent.Coordinator = (*streamScriptCoordinator)(nil)

// newStreamPromptHarness mirrors newPromptHarness for
// streamScriptCoordinator: it creates a session and injects the
// workspace app so the scripted run publishes through the same message
// service the ACP layer subscribes to.
func newStreamPromptHarness(t *testing.T, cwd string, scripted *streamScriptCoordinator) (*Agent, acpsdk.SessionId) {
	t.Helper()

	a := newTestAgent()
	resp, err := a.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)

	a.mu.Lock()
	sess := a.sessions[resp.SessionId]
	a.mu.Unlock()
	require.NotNil(t, sess)
	scripted.app = sess.workspace.App
	sess.workspace.AgentCoordinator = scripted
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })

	return a, resp.SessionId
}

// TestPromptStreamsThinking verifies US-020: ReasoningContent deltas
// are relayed as agent_thought_chunk updates, in order, ahead of the
// text deltas, and no thought chunk is sent once the thinking text
// stops growing.
func TestPromptStreamsThinking(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	scripted := &streamScriptCoordinator{}
	scripted.run = func(ctx context.Context, sessionID string) error {
		svc := scripted.app.Messages
		asst, err := svc.Create(ctx, sessionID, message.CreateMessageParams{
			Role:  message.Assistant,
			Parts: []message.ContentPart{},
		})
		if err != nil {
			return err
		}
		// 1. thinking only, no text yet.
		asst.Parts = []message.ContentPart{message.ReasoningContent{Thinking: "Let me "}}
		if err := svc.Update(ctx, asst); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		// 2. thinking grows, text begins.
		asst.Parts = []message.ContentPart{
			message.ReasoningContent{Thinking: "Let me think about this carefully."},
			message.TextContent{Text: "Here is "},
		}
		if err := svc.Update(ctx, asst); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		// 3. thinking unchanged, text grows (no new thought chunk).
		asst.Parts = []message.ContentPart{
			message.ReasoningContent{Thinking: "Let me think about this carefully."},
			message.TextContent{Text: "Here is the answer."},
		}
		if err := svc.Update(ctx, asst); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		asst.Parts = append(asst.Parts, message.Finish{Reason: message.FinishReasonEndTurn})
		return svc.Update(ctx, asst)
	}
	a, sessionID := newStreamPromptHarness(t, t.TempDir(), scripted)
	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)

	resp, err := a.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.NoError(t, err)
	require.Equal(t, acpsdk.StopReasonEndTurn, resp.StopReason)

	require.Eventually(t, func() bool {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		var thought, text strings.Builder
		thoughtChunks, textChunks := 0, 0
		lastThoughtIdx, firstTextIdx := -1, -1
		for i, u := range cap.updates {
			if u.SessionId != sessionID {
				continue
			}
			if c := u.Update.AgentThoughtChunk; c != nil && c.Content.Text != nil {
				thought.WriteString(c.Content.Text.Text)
				thoughtChunks++
				lastThoughtIdx = i
			}
			if c := u.Update.AgentMessageChunk; c != nil && c.Content.Text != nil {
				text.WriteString(c.Content.Text.Text)
				textChunks++
				if firstTextIdx < 0 {
					firstTextIdx = i
				}
			}
		}
		return thought.String() == "Let me think about this carefully." &&
			text.String() == "Here is the answer." &&
			thoughtChunks == 2 && textChunks == 2 &&
			// Thought deltas precede the text deltas they annotate.
			lastThoughtIdx < firstTextIdx
	}, 3*time.Second, 20*time.Millisecond)
}

// TestPromptStreamsToolCalls verifies US-021: tool calls are announced
// as tool_call updates, completed when the provider finishes streaming
// input, and settled with tool_call_update carrying the result (failed
// on error). Directly-finished calls skip the pending phase, duplicate
// results are dropped, and results for unknown calls are ignored.
func TestPromptStreamsToolCalls(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	scripted := &streamScriptCoordinator{}
	scripted.run = func(ctx context.Context, sessionID string) error {
		svc := scripted.app.Messages
		asst, err := svc.Create(ctx, sessionID, message.CreateMessageParams{
			Role:  message.Assistant,
			Parts: []message.ContentPart{},
		})
		if err != nil {
			return err
		}
		// 1. call_1 announced while still streaming input (pending).
		asst.Parts = []message.ContentPart{
			message.ToolCall{ID: "call_1", Name: "bash", Finished: false},
		}
		if err := svc.Update(ctx, asst); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		// 2. call_1 input complete.
		asst.Parts = []message.ContentPart{
			message.ToolCall{ID: "call_1", Name: "bash", Input: `{"command":"ls"}`, Finished: true},
		}
		if err := svc.Update(ctx, asst); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		// 3. call_2 appears already finished (no pending phase).
		asst.Parts = []message.ContentPart{
			message.ToolCall{ID: "call_1", Name: "bash", Input: `{"command":"ls"}`, Finished: true},
			message.ToolCall{ID: "call_2", Name: "edit", Input: `{"path":"a.txt"}`, Finished: true},
		}
		if err := svc.Update(ctx, asst); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		// 4. call_1 result (success).
		if _, err := svc.Create(ctx, sessionID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "call_1", Name: "bash", Content: "file.txt"},
			},
		}); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		// 5. call_2 result (error).
		if _, err := svc.Create(ctx, sessionID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "call_2", Name: "edit", Content: "permission denied", IsError: true},
			},
		}); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		// 6. duplicate call_1 result: must not settle twice.
		if _, err := svc.Create(ctx, sessionID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "call_1", Name: "bash", Content: "dup"},
			},
		}); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		// 7. result for a never-announced call: must be dropped.
		if _, err := svc.Create(ctx, sessionID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "call_ghost", Name: "bash", Content: "x"},
			},
		}); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		asst.Parts = append(asst.Parts, message.Finish{Reason: message.FinishReasonEndTurn})
		return svc.Update(ctx, asst)
	}
	a, sessionID := newStreamPromptHarness(t, t.TempDir(), scripted)
	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)

	resp, err := a.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.NoError(t, err)
	require.Equal(t, acpsdk.StopReasonEndTurn, resp.StopReason)

	var starts []acpsdk.SessionUpdateToolCall
	var updates []acpsdk.SessionToolCallUpdate
	require.Eventually(t, func() bool {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		starts = starts[:0]
		updates = updates[:0]
		for _, u := range cap.updates {
			if u.SessionId != sessionID {
				continue
			}
			if u.Update.ToolCall != nil {
				starts = append(starts, *u.Update.ToolCall)
			}
			if u.Update.ToolCallUpdate != nil {
				updates = append(updates, *u.Update.ToolCallUpdate)
			}
		}
		return len(starts) == 2 && len(updates) == 4
	}, 3*time.Second, 20*time.Millisecond)

	// call_1: announced pending, execute kind, no input yet.
	require.Equal(t, acpsdk.ToolCallId("call_1"), starts[0].ToolCallId)
	require.Equal(t, "bash", starts[0].Title)
	require.Equal(t, acpsdk.ToolCallStatusPending, starts[0].Status)
	require.Equal(t, acpsdk.ToolKindExecute, starts[0].Kind)
	require.Nil(t, starts[0].RawInput)

	// call_2: announced already finished -> in_progress, edit kind.
	require.Equal(t, acpsdk.ToolCallId("call_2"), starts[1].ToolCallId)
	require.Equal(t, "edit", starts[1].Title)
	require.Equal(t, acpsdk.ToolCallStatusInProgress, starts[1].Status)
	require.Equal(t, acpsdk.ToolKindEdit, starts[1].Kind)
	require.Equal(t, map[string]any{"path": "a.txt"}, starts[1].RawInput)

	// updates arrive in event order: call_1 input-complete, call_2
	// input-complete, call_1 result, call_2 failed result.
	require.Equal(t, acpsdk.ToolCallId("call_1"), updates[0].ToolCallId)
	require.NotNil(t, updates[0].Status)
	require.Equal(t, acpsdk.ToolCallStatusCompleted, *updates[0].Status)
	require.Equal(t, map[string]any{"command": "ls"}, updates[0].RawInput)

	require.Equal(t, acpsdk.ToolCallId("call_2"), updates[1].ToolCallId)
	require.NotNil(t, updates[1].Status)
	require.Equal(t, acpsdk.ToolCallStatusCompleted, *updates[1].Status)

	require.Equal(t, acpsdk.ToolCallId("call_1"), updates[2].ToolCallId)
	require.NotNil(t, updates[2].Status)
	require.Equal(t, acpsdk.ToolCallStatusCompleted, *updates[2].Status)
	require.Equal(t, "file.txt", updates[2].RawOutput)
	require.Len(t, updates[2].Content, 1)
	require.Equal(t, "file.txt", updates[2].Content[0].Content.Content.Text.Text)

	require.Equal(t, acpsdk.ToolCallId("call_2"), updates[3].ToolCallId)
	require.NotNil(t, updates[3].Status)
	require.Equal(t, acpsdk.ToolCallStatusFailed, *updates[3].Status)
	require.Equal(t, "permission denied", updates[3].RawOutput)
}
