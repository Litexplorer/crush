package acp

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/message"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

// scriptedCoordinator is an agent.Coordinator stub that publishes user
// and assistant messages through the app's real message service, the
// same pipeline the production sessionAgent uses, so the ACP streaming
// path is exercised over the real pubsub.
type scriptedCoordinator struct {
	app *app.App

	// chunks are streamed as successive assistant message states before
	// the run finishes.
	chunks []string
	// block, when non-nil, parks the run here until it is closed or the
	// context is canceled (used by the cancel test).
	block chan struct{}

	enteredCh   chan struct{}
	enteredOnce sync.Once
}

// entered reports whether a run has entered the coordinator.
func (c *scriptedCoordinator) entered() bool {
	if c.enteredCh == nil {
		return false
	}
	select {
	case <-c.enteredCh:
		return true
	default:
		return false
	}
}

func (c *scriptedCoordinator) Run(ctx context.Context, sessionID, prompt string, _ ...message.Attachment) (*fantasy.AgentResult, error) {
	if c.enteredCh != nil {
		c.enteredOnce.Do(func() { close(c.enteredCh) })
	}
	_, err := c.app.Messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: prompt}},
	})
	if err != nil {
		return nil, err
	}

	asst, err := c.app.Messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: ""}},
	})
	if err != nil {
		return nil, err
	}

	var accumulated string
	for _, chunk := range c.chunks {
		accumulated += chunk
		asst.Parts = []message.ContentPart{message.TextContent{Text: accumulated}}
		if err := c.app.Messages.Update(ctx, asst); err != nil {
			return nil, err
		}
		// Let the message service debounce tick fire between chunks so
		// each lands as its own event.
		time.Sleep(50 * time.Millisecond)
	}

	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
		}
	}

	select {
	case <-ctx.Done():
		asst.Parts = append(asst.Parts, message.Finish{Reason: message.FinishReasonCanceled})
		_ = c.app.Messages.Update(ctx, asst)
		return nil, context.Canceled
	default:
	}

	asst.Parts = append(asst.Parts, message.Finish{Reason: message.FinishReasonEndTurn})
	_ = c.app.Messages.Update(ctx, asst)
	return &fantasy.AgentResult{}, nil
}

func (c *scriptedCoordinator) RunAccepted(ctx context.Context, _ *agent.AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return c.Run(ctx, sessionID, prompt, attachments...)
}

func (c *scriptedCoordinator) BeginAccepted(string) *agent.AcceptedRun       { return nil }
func (c *scriptedCoordinator) Cancel(string)                                 {}
func (c *scriptedCoordinator) CancelAll()                                    {}
func (c *scriptedCoordinator) IsSessionBusy(string) bool                     { return false }
func (c *scriptedCoordinator) IsBusy() bool                                  { return false }
func (c *scriptedCoordinator) QueuedPrompts(string) int                      { return 0 }
func (c *scriptedCoordinator) QueuedPromptsList(string) []string             { return nil }
func (c *scriptedCoordinator) ClearQueue(string)                             {}
func (c *scriptedCoordinator) Summarize(context.Context, string) error       { return nil }
func (c *scriptedCoordinator) Model() agent.Model                            { return agent.Model{} }
func (c *scriptedCoordinator) UpdateModels(context.Context) error            { return nil }
func (c *scriptedCoordinator) GenerateTitle(context.Context, string, string) {}

var _ agent.Coordinator = (*scriptedCoordinator)(nil)

// captureClient is an acpsdk.Client that records every session update
// notification it receives; the remaining methods are no-ops.
type captureClient struct {
	mu      sync.Mutex
	updates []acpsdk.SessionNotification
}

func (c *captureClient) SessionUpdate(_ context.Context, params acpsdk.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, params)
	return nil
}

func (c *captureClient) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, nil
}

func (c *captureClient) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, nil
}

func (c *captureClient) RequestPermission(context.Context, acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	return acpsdk.RequestPermissionResponse{}, nil
}

func (c *captureClient) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, nil
}

func (c *captureClient) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, nil
}

func (c *captureClient) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, nil
}

func (c *captureClient) ReleaseTerminal(context.Context, acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, nil
}

func (c *captureClient) WaitForTerminalExit(context.Context, acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, nil
}

var _ acpsdk.Client = (*captureClient)(nil)

// connectPeers wires an agent-side and a client-side connection over
// io.Pipes so agent notifications reach the capture client. The
// returned cleanup closes both pipes, shutting down the connections.
func connectPeers(a *Agent, cap *captureClient) func() {
	agentOutR, agentOutW := io.Pipe()
	clientOutR, clientOutW := io.Pipe()

	agentConn := acpsdk.NewAgentSideConnection(a, agentOutW, clientOutR)
	a.Attach(agentConn)
	acpsdk.NewClientSideConnection(cap, clientOutW, agentOutR)

	return func() {
		_ = agentOutW.Close()
		_ = clientOutR.Close()
		_ = clientOutW.Close()
		_ = agentOutR.Close()
	}
}

// newPromptHarness creates an agent with a real backend workspace for
// the given cwd and swaps in a scripted coordinator, returning the
// agent, the ACP session ID, and a cleanup that detaches the workspace.
func newPromptHarness(t *testing.T, cwd string, scripted *scriptedCoordinator) (*Agent, acpsdk.SessionId) {
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

func TestPromptUnknownSession(t *testing.T) {
	a := newTestAgent()
	_, err := a.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId("sess_unknown"),
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.Error(t, err)
	var re *acpsdk.RequestError
	require.True(t, errors.As(err, &re))
	require.Equal(t, -32002, re.Code)
}

func TestPromptEmptyText(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a, sessionID := newPromptHarness(t, t.TempDir(), &scriptedCoordinator{})
	_, err := a.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{},
	})
	require.Error(t, err)
	var re *acpsdk.RequestError
	require.True(t, errors.As(err, &re))
	require.Equal(t, -32602, re.Code)
}

func TestPromptStreamsText(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	scripted := &scriptedCoordinator{
		chunks: []string{"Hello, ", "I am ", "Crush."},
	}
	a, sessionID := newPromptHarness(t, t.TempDir(), scripted)
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
		var text strings.Builder
		for _, u := range cap.updates {
			if u.SessionId != sessionID || u.Update.AgentMessageChunk == nil || u.Update.AgentMessageChunk.Content.Text == nil {
				return false
			}
			text.WriteString(u.Update.AgentMessageChunk.Content.Text.Text)
		}
		return text.String() == "Hello, I am Crush."
	}, 3*time.Second, 20*time.Millisecond)

	cap.mu.Lock()
	require.Len(t, cap.updates, 3)
	cap.mu.Unlock()
}

func TestPromptCancelTerminates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	scripted := &scriptedCoordinator{block: make(chan struct{}), enteredCh: make(chan struct{})}
	a, sessionID := newPromptHarness(t, t.TempDir(), scripted)
	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		resp acpsdk.PromptResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := a.Prompt(ctx, acpsdk.PromptRequest{
			SessionId: sessionID,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
		})
		done <- result{resp, err}
	}()

	// Let the run get going, then cancel.
	require.Eventually(t, func() bool { return scripted.entered() }, 2*time.Second, 10*time.Millisecond)
	cancel()

	select {
	case r := <-done:
		require.Error(t, r.err)
		require.True(t, errors.Is(r.err, context.Canceled), "want cancelled error, got %v", r.err)
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not terminate within 2s of cancel")
	}

	// No further session updates may arrive after the prompt terminated.
	time.Sleep(100 * time.Millisecond)
	cap.mu.Lock()
	defer cap.mu.Unlock()
	require.Empty(t, cap.updates)
}

func TestCancelUnknownSession(t *testing.T) {
	a := newTestAgent()
	err := a.Cancel(context.Background(), acpsdk.CancelNotification{
		SessionId: acpsdk.SessionId("sess_unknown"),
	})
	require.Error(t, err)
	var re *acpsdk.RequestError
	require.True(t, errors.As(err, &re))
	require.Equal(t, -32002, re.Code)
}

func TestCloseSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	scripted := &scriptedCoordinator{}
	a, sessionID := newPromptHarness(t, t.TempDir(), scripted)

	// Close the session and verify the workspace claim is released.
	_, err := a.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: sessionID})
	require.NoError(t, err)
	require.Empty(t, a.backend.ListWorkspaces())

	// Re-close and all subsequent access must fail with a clear error.
	_, err = a.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: sessionID})
	require.Error(t, err)
	var re *acpsdk.RequestError
	require.True(t, errors.As(err, &re))
	require.Equal(t, -32002, re.Code)

	_, err = a.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.Error(t, err)
	require.True(t, errors.As(err, &re))
	require.Equal(t, -32002, re.Code)
}

func TestCloseSessionSharedWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := newTestAgent()
	cwd := t.TempDir()

	newSession := func() acpsdk.SessionId {
		resp, err := a.NewSession(context.Background(), acpsdk.NewSessionRequest{
			Cwd:        cwd,
			McpServers: []acpsdk.McpServer{},
		})
		require.NoError(t, err)
		return resp.SessionId
	}

	s1 := newSession()
	s2 := newSession()
	a.mu.Lock()
	shared := a.sessions[s1].workspace.ID
	require.Equal(t, shared, a.sessions[s2].workspace.ID)
	a.mu.Unlock()

	// Closing one of the shared sessions must not tear down the
	// workspace while the other session still holds a claim.
	_, err := a.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: s1})
	require.NoError(t, err)
	require.Len(t, a.backend.ListWorkspaces(), 1)

	_, err = a.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: s2})
	require.NoError(t, err)
	require.Empty(t, a.backend.ListWorkspaces())
}

func TestCloseAllReleasesSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	scripted := &scriptedCoordinator{}
	a, s1 := newPromptHarness(t, t.TempDir(), scripted)

	a.mu.Lock()
	before := len(a.sessions)
	a.mu.Unlock()
	require.Equal(t, 1, before)

	a.CloseAll()

	a.mu.Lock()
	require.Empty(t, a.sessions)
	a.mu.Unlock()
	require.Empty(t, a.backend.ListWorkspaces())

	// Sessions are gone: subsequent requests return a clear error.
	_, err := a.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: s1,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.Error(t, err)
}
