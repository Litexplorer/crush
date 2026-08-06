package acp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

// permissionEnv builds an agent with one live session, wires a capture
// client, and returns the pieces for permission / file-system tests.
func permissionEnv(t *testing.T) (*Agent, *session, acpsdk.SessionId, *captureClient, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	cwd := t.TempDir()
	a := newEnvAgent(t, "")
	sid := newSessionOn(t, a, cwd)
	sess := sessionFor(t, a, sid)
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })
	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)
	return a, sess, sid, cap, cwd
}

func TestNormalModeForwardsPermissionToClient(t *testing.T) {
	_, sess, sid, cap, _ := permissionEnv(t)

	// Allow-once grants the request and carries the right shape.
	cap.permissionReply = func(req acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected("allow-once")}, nil
	}
	require.True(t, requestPermission(t, sess, "bash", "execute"))
	cap.mu.Lock()
	require.Len(t, cap.permissionRequests, 1)
	req := cap.permissionRequests[0]
	cap.mu.Unlock()
	require.Equal(t, sid, req.SessionId)
	require.Equal(t, "bash (execute)", *req.ToolCall.Title)
	require.NotNil(t, req.ToolCall.Kind)
	require.Equal(t, acpsdk.ToolKindExecute, *req.ToolCall.Kind)
	require.Len(t, req.Options, 3)
	require.Equal(t, acpsdk.PermissionOptionKindAllowOnce, req.Options[0].Kind)
	require.Equal(t, acpsdk.PermissionOptionKindAllowAlways, req.Options[1].Kind)
	require.Equal(t, acpsdk.PermissionOptionKindRejectOnce, req.Options[2].Kind)

	// Reject (cancelled outcome) denies the request.
	cap.permissionReply = func(acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, nil
	}
	require.False(t, requestPermission(t, sess, "bash", "execute"))

	// Client errors deny the request (safety first).
	cap.permissionReply = func(acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{}, errors.New("client exploded")
	}
	require.False(t, requestPermission(t, sess, "bash", "execute"))

	// A silent client times out and the request is denied.
	old := permissionRequestTimeout
	permissionRequestTimeout = 150 * time.Millisecond
	defer func() { permissionRequestTimeout = old }()
	cap.permissionReply = func(acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		time.Sleep(600 * time.Millisecond)
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected("allow-once")}, nil
	}
	require.False(t, requestPermission(t, sess, "bash", "execute"))
}

func TestPlanModeDoesNotAskClient(t *testing.T) {
	a, sess, sid, cap, _ := permissionEnv(t)
	_, err := a.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{SessionId: sid, ModeId: modePlan})
	require.NoError(t, err)
	require.False(t, requestPermission(t, sess, "bash", "execute"))
	cap.mu.Lock()
	defer cap.mu.Unlock()
	require.Empty(t, cap.permissionRequests)
}

func TestFileClientRead(t *testing.T) {
	a, sess, _, cap, _ := permissionEnv(t)

	// Client advertises no FS support: sentinel error (local fallback).
	a.setClientCapabilities(acpsdk.ClientCapabilities{})
	fc := &fileClient{a: a}
	_, err := fc.ReadTextFile(context.Background(), sess.sessionID, "/x.txt", nil, nil)
	require.ErrorIs(t, err, errClientUnavailable)

	// Client supports reads and provides content.
	a.setClientCapabilities(acpsdk.ClientCapabilities{Fs: acpsdk.FileSystemCapabilities{ReadTextFile: true}})
	cap.readReply = func(req acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
		return acpsdk.ReadTextFileResponse{Content: "hello from client"}, nil
	}
	content, err := fc.ReadTextFile(context.Background(), sess.sessionID, "/abs/notes.txt", nil, nil)
	require.NoError(t, err)
	require.Equal(t, "hello from client", content)
	cap.mu.Lock()
	require.Len(t, cap.readRequests, 1)
	got := cap.readRequests[0]
	cap.mu.Unlock()
	require.Equal(t, acpsdk.SessionId(sess.sessionID), got.SessionId)
	require.Equal(t, "/abs/notes.txt", got.Path)

	// Client errors propagate (tool falls back to local IO).
	cap.readReply = func(acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
		return acpsdk.ReadTextFileResponse{}, errors.New("client read failed")
	}
	_, err = fc.ReadTextFile(context.Background(), sess.sessionID, "/abs/notes.txt", nil, nil)
	require.Error(t, err)
}

func TestFileClientWrite(t *testing.T) {
	a, sess, _, cap, _ := permissionEnv(t)

	// No FS support: sentinel error.
	a.setClientCapabilities(acpsdk.ClientCapabilities{})
	fc := &fileClient{a: a}
	err := fc.WriteTextFile(context.Background(), sess.sessionID, "/abs/new.txt", "content")
	require.ErrorIs(t, err, errClientUnavailable)

	// Client supports writes.
	a.setClientCapabilities(acpsdk.ClientCapabilities{Fs: acpsdk.FileSystemCapabilities{WriteTextFile: true}})
	cap.writeReply = func(req acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
		return acpsdk.WriteTextFileResponse{}, nil
	}
	require.NoError(t, fc.WriteTextFile(context.Background(), sess.sessionID, "/abs/new.txt", "content"))
	cap.mu.Lock()
	require.Len(t, cap.writeRequests, 1)
	got := cap.writeRequests[0]
	cap.mu.Unlock()
	require.Equal(t, acpsdk.SessionId(sess.sessionID), got.SessionId)
	require.Equal(t, "/abs/new.txt", got.Path)
	require.Equal(t, "content", got.Content)

	// Client errors propagate.
	cap.writeReply = func(acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
		return acpsdk.WriteTextFileResponse{}, errors.New("client write failed")
	}
	require.Error(t, fc.WriteTextFile(context.Background(), sess.sessionID, "/abs/new.txt", "content"))
}

func TestSessionSetsFileClientHook(t *testing.T) {
	_, sess, _, _, _ := permissionEnv(t)
	require.NotNil(t, sess.workspace.Cfg.Overrides().FileClient)
}

// viewToolCtx returns a context carrying the tools session ID.
func viewToolCtx(sessionID string) context.Context {
	return context.WithValue(context.Background(), tools.SessionIDContextKey, sessionID)
}

func TestViewToolPrefersClientRead(t *testing.T) {
	a, sess, _, cap, cwd := permissionEnv(t)
	a.setClientCapabilities(acpsdk.ClientCapabilities{Fs: acpsdk.FileSystemCapabilities{ReadTextFile: true}})
	fc := &fileClient{a: a}
	tool := tools.NewViewTool(sess.workspace.LSPManager, sess.workspace.Permissions, sess.workspace.App.FileTracker, nil, cwd, fc)

	// Client provides the content (e.g. an unsaved buffer): used as-is.
	cap.readReply = func(acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
		return acpsdk.ReadTextFileResponse{Content: "client-buffered line\n"}, nil
	}
	resp := runView(t, tool, viewToolCtx(sess.sessionID), cwd, "buffered.txt")
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "client-buffered line")
	_, err := os.Stat(filepath.Join(cwd, "buffered.txt"))
	require.True(t, os.IsNotExist(err), "client read must not require a local file")

	// Client unavailable: falls back to the local file system.
	local := filepath.Join(cwd, "local.txt")
	require.NoError(t, os.WriteFile(local, []byte("local line\n"), 0o644))
	cap.readReply = func(acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
		return acpsdk.ReadTextFileResponse{}, errors.New("client read failed")
	}
	resp = runView(t, tool, viewToolCtx(sess.sessionID), cwd, "local.txt")
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "local line")

	// No FS capability: falls back to the local file system.
	a.setClientCapabilities(acpsdk.ClientCapabilities{})
	resp = runView(t, tool, viewToolCtx(sess.sessionID), cwd, "local.txt")
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "local line")
}

func TestWriteToolPrefersClientWrite(t *testing.T) {
	a, sess, _, cap, cwd := permissionEnv(t)
	a.setClientCapabilities(acpsdk.ClientCapabilities{Fs: acpsdk.FileSystemCapabilities{WriteTextFile: true}})
	// Skip permission prompts so the write flow reaches the client path.
	sess.workspace.Permissions.SetSkipRequests(true)
	fc := &fileClient{a: a}
	tool := tools.NewWriteTool(nil, sess.workspace.Permissions, sess.workspace.App.History, sess.workspace.App.FileTracker, cwd, fc)

	// Client accepts the write: no local file is created.
	cap.writeReply = func(acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
		return acpsdk.WriteTextFileResponse{}, nil
	}
	resp := runWrite(t, tool, viewToolCtx(sess.sessionID), cwd, "new.txt", "client-write")
	require.False(t, resp.IsError)
	cap.mu.Lock()
	require.Len(t, cap.writeRequests, 1)
	got := cap.writeRequests[0]
	cap.mu.Unlock()
	require.Equal(t, filepath.Join(cwd, "new.txt"), got.Path)
	require.Equal(t, "client-write", got.Content)
	_, err := os.Stat(filepath.Join(cwd, "new.txt"))
	require.True(t, os.IsNotExist(err), "client write must not create a local file")

	// Client rejects: falls back to the local file system.
	cap.writeReply = func(acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
		return acpsdk.WriteTextFileResponse{}, errors.New("client write failed")
	}
	resp = runWrite(t, tool, viewToolCtx(sess.sessionID), cwd, "fallback.txt", "local-write")
	require.False(t, resp.IsError)
	b, err := os.ReadFile(filepath.Join(cwd, "fallback.txt"))
	require.NoError(t, err)
	require.Equal(t, "local-write", string(b))
}

func runView(t *testing.T, tool fantasy.AgentTool, ctx context.Context, cwd, name string) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(tools.ViewParams{FilePath: name})
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "v1", Name: tools.ViewToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

func runWrite(t *testing.T, tool fantasy.AgentTool, ctx context.Context, cwd, name, content string) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(tools.WriteParams{FilePath: name, Content: content})
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "w1", Name: tools.WriteToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

var _ = config.FileClient((*fileClient)(nil))
var _ = permission.PermissionRequest{}
