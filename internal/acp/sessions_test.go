package acp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/backend"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

// newEnvAgent returns an Agent with HOME/XDG_* isolated so workspace
// creation uses throwaway data directories. dataDir may be empty; the
// agent then resolves it lazily from a live workspace.
func newEnvAgent(t *testing.T, dataDir string) *Agent {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	return NewAgent(backend.New(context.Background(), nil, nil), dataDir)
}

// newSessionOn creates an ACP session for cwd and returns its ID.
func newSessionOn(t *testing.T, a *Agent, cwd string) acpsdk.SessionId {
	t.Helper()
	resp, err := a.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)
	return resp.SessionId
}

// closeSessionOn closes a session through the agent.
func closeSessionOn(t *testing.T, a *Agent, sessionID acpsdk.SessionId) {
	t.Helper()
	_, err := a.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: sessionID})
	require.NoError(t, err)
}

// agentDataDir returns the data directory backing the given live
// session, so a second agent can be constructed against the same
// history (simulating a process restart).
func agentDataDir(t *testing.T, a *Agent, sessionID acpsdk.SessionId) string {
	t.Helper()
	a.mu.Lock()
	sess := a.sessions[sessionID]
	a.mu.Unlock()
	require.NotNil(t, sess)
	dd := sess.workspace.Cfg.Config().Options.DataDirectory
	require.NotEmpty(t, dd)
	return dd
}

// swapCoordinator points a live session's workspace at a scripted
// coordinator, wires agent notifications to a capture client, and
// registers workspace-detach cleanup.
func swapCoordinator(t *testing.T, a *Agent, sessionID acpsdk.SessionId, scripted *scriptedCoordinator) *captureClient {
	t.Helper()
	a.mu.Lock()
	sess := a.sessions[sessionID]
	a.mu.Unlock()
	require.NotNil(t, sess)
	scripted.app = sess.workspace.App
	sess.workspace.AgentCoordinator = scripted
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })
	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)
	return cap
}

// streamedText folds the captured agent text updates for a session into
// a single string, under the capture client's lock.
func (c *captureClient) streamedText(sid acpsdk.SessionId) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sb strings.Builder
	for _, u := range c.updates {
		if u.SessionId != sid || u.Update.AgentMessageChunk == nil || u.Update.AgentMessageChunk.Content.Text == nil {
			continue
		}
		sb.WriteString(u.Update.AgentMessageChunk.Content.Text.Text)
	}
	return sb.String()
}

func TestListSessionsEmpty(t *testing.T) {
	a := newTestAgent()
	resp, err := a.ListSessions(context.Background(), acpsdk.ListSessionsRequest{})
	require.NoError(t, err)
	require.Empty(t, resp.Sessions)
	require.Nil(t, resp.NextCursor)
}

func TestListSessionsLive(t *testing.T) {
	ctx := context.Background()
	a := newEnvAgent(t, "")
	cwd1, cwd2 := t.TempDir(), t.TempDir()
	sid1 := newSessionOn(t, a, cwd1)
	sid2 := newSessionOn(t, a, cwd2)
	t.Cleanup(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, s := range a.sessions {
			a.backend.DetachClient(s.workspace.ID, a.clientID)
		}
	})

	resp, err := a.ListSessions(ctx, acpsdk.ListSessionsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 2)
	byID := map[acpsdk.SessionId]string{}
	for _, s := range resp.Sessions {
		byID[s.SessionId] = s.Cwd
	}
	require.Equal(t, cwd1, byID[sid1])
	require.Equal(t, cwd2, byID[sid2])

	// Filtering by cwd narrows the result to that workspace's sessions.
	resp, err = a.ListSessions(ctx, acpsdk.ListSessionsRequest{Cwd: acpsdk.Ptr(cwd1)})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 1)
	require.Equal(t, sid1, resp.Sessions[0].SessionId)
	require.Equal(t, cwd1, resp.Sessions[0].Cwd)
	require.NotNil(t, resp.Sessions[0].Title)
	require.NotNil(t, resp.Sessions[0].UpdatedAt)
}

func TestListSessionsPersistedAfterClose(t *testing.T) {
	ctx := context.Background()
	a := newEnvAgent(t, "")
	cwd := t.TempDir()
	sid := newSessionOn(t, a, cwd)
	dd := agentDataDir(t, a, sid)
	closeSessionOn(t, a, sid)
	require.Empty(t, a.backend.ListWorkspaces())

	// A fresh connection over the same data directory still lists the
	// closed session: session/list must survive process restarts.
	b := NewAgent(backend.New(context.Background(), nil, nil), dd)
	resp, err := b.ListSessions(ctx, acpsdk.ListSessionsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 1)
	info := resp.Sessions[0]
	require.Equal(t, sid, info.SessionId)
	require.Equal(t, cwd, info.Cwd)
	require.NotNil(t, info.Title)
}

func TestLoadSessionUnknown(t *testing.T) {
	ctx := context.Background()
	a := NewAgent(backend.New(context.Background(), nil, nil), t.TempDir())
	_, err := a.LoadSession(ctx, acpsdk.LoadSessionRequest{
		Cwd:        t.TempDir(),
		SessionId:  acpsdk.SessionId("sess_nonexistent"),
		McpServers: []acpsdk.McpServer{},
	})
	require.Error(t, err)
	var re *acpsdk.RequestError
	require.True(t, errors.As(err, &re))
	require.Equal(t, -32002, re.Code)
}

func TestLoadSessionCwdMismatch(t *testing.T) {
	ctx := context.Background()
	a := newEnvAgent(t, "")
	cwd := t.TempDir()
	sid := newSessionOn(t, a, cwd)
	dd := agentDataDir(t, a, sid)
	closeSessionOn(t, a, sid)

	b := NewAgent(backend.New(context.Background(), nil, nil), dd)

	// A session belongs to its cwd; loading it from another directory
	// must fail with a not-found error.
	_, err := b.LoadSession(ctx, acpsdk.LoadSessionRequest{
		Cwd:        t.TempDir(),
		SessionId:  sid,
		McpServers: []acpsdk.McpServer{},
	})
	require.Error(t, err)
	var re *acpsdk.RequestError
	require.True(t, errors.As(err, &re))
	require.Equal(t, -32002, re.Code)

	// The matching cwd loads fine, and loading an already-live session
	// is idempotent.
	_, err = b.LoadSession(ctx, acpsdk.LoadSessionRequest{
		Cwd:        cwd,
		SessionId:  sid,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)
	b.mu.Lock()
	sessB := b.sessions[sid]
	b.mu.Unlock()
	require.NotNil(t, sessB)
	t.Cleanup(func() { b.backend.DetachClient(sessB.workspace.ID, b.clientID) })
	_, err = b.LoadSession(ctx, acpsdk.LoadSessionRequest{
		Cwd:        cwd,
		SessionId:  sid,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)
}

func TestLoadSessionRestoresHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ctx := context.Background()
	cwd := t.TempDir()

	// Turn one: a live session with real message history.
	scriptedA := &scriptedCoordinator{chunks: []string{"hello "}}
	a, sessionID := newPromptHarness(t, cwd, scriptedA)
	capA := &captureClient{}
	cleanup := connectPeers(a, capA)
	t.Cleanup(cleanup)
	_, err := a.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("first turn")},
	})
	require.NoError(t, err)
	require.Equal(t, "hello ", capA.streamedText(sessionID))

	dd := agentDataDir(t, a, sessionID)
	a.mu.Lock()
	sessA := a.sessions[sessionID]
	a.mu.Unlock()
	histBefore, err := sessA.workspace.App.Messages.List(ctx, string(sessionID))
	require.NoError(t, err)
	require.Len(t, histBefore, 2) // user + assistant

	// "Restart": the connection closes and a fresh one opens against the
	// same data directory.
	closeSessionOn(t, a, sessionID)

	b := NewAgent(backend.New(context.Background(), nil, nil), dd)
	_, err = b.LoadSession(ctx, acpsdk.LoadSessionRequest{
		Cwd:        cwd,
		SessionId:  sessionID,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)

	// The restored session streams a new turn and keeps growing the
	// same message thread.
	scriptedB := &scriptedCoordinator{chunks: []string{"world"}}
	capB := swapCoordinator(t, b, sessionID, scriptedB)
	_, err = b.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("second turn")},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return capB.streamedText(sessionID) == "world"
	}, 3*time.Second, 20*time.Millisecond)

	b.mu.Lock()
	sessB := b.sessions[sessionID]
	b.mu.Unlock()
	require.NotNil(t, sessB)
	histAfter, err := sessB.workspace.App.Messages.List(ctx, string(sessionID))
	require.NoError(t, err)
	require.Len(t, histAfter, len(histBefore)+2) // history + new user/assistant turn
}

func TestResumeSessionUnknown(t *testing.T) {
	ctx := context.Background()
	a := NewAgent(backend.New(context.Background(), nil, nil), t.TempDir())
	_, err := a.ResumeSession(ctx, acpsdk.ResumeSessionRequest{
		Cwd:        t.TempDir(),
		SessionId:  acpsdk.SessionId("sess_nonexistent"),
		McpServers: []acpsdk.McpServer{},
	})
	require.Error(t, err)
	var re *acpsdk.RequestError
	require.True(t, errors.As(err, &re))
	require.Equal(t, -32002, re.Code)
}

func TestResumeSessionThenPromptStreams(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ctx := context.Background()
	cwd := t.TempDir()

	a := newEnvAgent(t, "")
	sid := newSessionOn(t, a, cwd)
	dd := agentDataDir(t, a, sid)
	closeSessionOn(t, a, sid)

	// A fresh connection resumes the session and immediately streams a
	// new prompt through the standard US-004 path.
	b := NewAgent(backend.New(context.Background(), nil, nil), dd)
	_, err := b.ResumeSession(ctx, acpsdk.ResumeSessionRequest{
		Cwd:        cwd,
		SessionId:  sid,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)

	scripted := &scriptedCoordinator{chunks: []string{"resumed "}, enteredCh: make(chan struct{})}
	cap := swapCoordinator(t, b, sid, scripted)
	_, err = b.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sid,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("continue")},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return cap.streamedText(sid) == "resumed "
	}, 3*time.Second, 20*time.Millisecond)
}
