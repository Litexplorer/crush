package acp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/version"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

// newTestAgent returns an Agent backed by an isolated backend with no
// idle-shutdown callback.
func newTestAgent() *Agent {
	return NewAgent(backend.New(context.Background(), nil, nil), "")
}

// TestP3MethodsHandleInvalidInputs covers the P3 methods (US-010..012)
// on a fresh agent with no sessions: they must return well-formed
// errors, never MethodNotFound or panics.
func TestP3MethodsHandleInvalidInputs(t *testing.T) {
	ctx := context.Background()
	a := newTestAgent()

	assertRequestError := func(t *testing.T, err error, code int) {
		t.Helper()
		require.Error(t, err)
		var re *acpsdk.RequestError
		require.True(t, errors.As(err, &re))
		require.Equal(t, code, re.Code)
	}

	t.Run("authenticate unknown method", func(t *testing.T) {
		_, err := a.Authenticate(ctx, acpsdk.AuthenticateRequest{MethodId: "nope"})
		assertRequestError(t, err, -32602)
	})

	t.Run("authenticate known method", func(t *testing.T) {
		_, err := a.Authenticate(ctx, acpsdk.AuthenticateRequest{MethodId: authMethodID})
		require.NoError(t, err)
	})

	t.Run("logout without sessions", func(t *testing.T) {
		_, err := a.Logout(ctx, acpsdk.LogoutRequest{})
		require.NoError(t, err)
	})

	t.Run("setSessionMode unknown session", func(t *testing.T) {
		_, err := a.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{SessionId: "sess_nope", ModeId: modeNormal})
		assertRequestError(t, err, -32002)
	})

	t.Run("setSessionConfigOption without value", func(t *testing.T) {
		_, err := a.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{})
		assertRequestError(t, err, -32602)
	})
}

func TestAgentInitialize(t *testing.T) {
	ctx := context.Background()

	t.Run("valid handshake", func(t *testing.T) {
		a := newTestAgent()
		resp, err := a.Initialize(ctx, acpsdk.InitializeRequest{ProtocolVersion: 1})
		require.NoError(t, err)
		require.Equal(t, acpsdk.ProtocolVersion(acpsdk.ProtocolVersionNumber), resp.ProtocolVersion)
		require.NotNil(t, resp.AgentInfo)
		require.Equal(t, "Crush", resp.AgentInfo.Name)
		require.Equal(t, version.Version, resp.AgentInfo.Version)
		require.NotNil(t, resp.AgentCapabilities.SessionCapabilities.Close)
	})

	t.Run("version negotiation", func(t *testing.T) {
		a := newTestAgent()
		resp, err := a.Initialize(ctx, acpsdk.InitializeRequest{ProtocolVersion: 99})
		require.NoError(t, err)
		require.Equal(t, acpsdk.ProtocolVersion(acpsdk.ProtocolVersionNumber), resp.ProtocolVersion)
	})

	t.Run("invalid protocol version", func(t *testing.T) {
		a := newTestAgent()
		_, err := a.Initialize(ctx, acpsdk.InitializeRequest{ProtocolVersion: 0})
		require.Error(t, err)
		var re *acpsdk.RequestError
		require.True(t, errors.As(err, &re))
		require.Equal(t, -32602, re.Code)
	})

	t.Run("stores client capabilities", func(t *testing.T) {
		a := newTestAgent()
		_, err := a.Initialize(ctx, acpsdk.InitializeRequest{
			ProtocolVersion: 1,
			ClientCapabilities: acpsdk.ClientCapabilities{
				Terminal: true,
				Fs:       acpsdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			},
		})
		require.NoError(t, err)

		a.mu.Lock()
		defer a.mu.Unlock()
		require.True(t, a.clientCapabilities.Terminal)
		require.True(t, a.clientCapabilities.Fs.ReadTextFile)
		require.True(t, a.clientCapabilities.Fs.WriteTextFile)
	})
}

func TestAgentNewSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	ctx := context.Background()

	newSession := func(t *testing.T, a *Agent, cwd string) acpsdk.NewSessionResponse {
		t.Helper()
		resp, err := a.NewSession(ctx, acpsdk.NewSessionRequest{
			Cwd:        cwd,
			McpServers: []acpsdk.McpServer{},
		})
		require.NoError(t, err)
		return resp
	}

	t.Run("creates workspace and session", func(t *testing.T) {
		a := newTestAgent()
		cwd := t.TempDir()
		resp := newSession(t, a, cwd)
		require.NotEmpty(t, resp.SessionId)

		a.mu.Lock()
		sess, ok := a.sessions[resp.SessionId]
		a.mu.Unlock()
		require.True(t, ok)
		require.NotNil(t, sess.workspace)
		require.Equal(t, cwd, sess.workspace.Path)
		require.NotEmpty(t, sess.sessionID)

		wss := a.backend.ListWorkspaces()
		require.Len(t, wss, 1)
		require.Equal(t, sess.workspace.ID, wss[0].ID)

		t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })
	})

	t.Run("multiple sessions are independent", func(t *testing.T) {
		a := newTestAgent()
		r1 := newSession(t, a, t.TempDir())
		r2 := newSession(t, a, t.TempDir())
		require.NotEqual(t, r1.SessionId, r2.SessionId)

		a.mu.Lock()
		s1 := a.sessions[r1.SessionId]
		s2 := a.sessions[r2.SessionId]
		a.mu.Unlock()
		require.NotNil(t, s1)
		require.NotNil(t, s2)
		require.NotEqual(t, s1.workspace.ID, s2.workspace.ID)
		require.Len(t, a.backend.ListWorkspaces(), 2)

		t.Cleanup(func() {
			a.backend.DetachClient(s1.workspace.ID, a.clientID)
			a.backend.DetachClient(s2.workspace.ID, a.clientID)
		})
	})

	t.Run("duplicate cwd reuses workspace", func(t *testing.T) {
		a := newTestAgent()
		cwd := t.TempDir()
		r1 := newSession(t, a, cwd)
		r2 := newSession(t, a, cwd)
		require.NotEqual(t, r1.SessionId, r2.SessionId)

		a.mu.Lock()
		s1 := a.sessions[r1.SessionId]
		s2 := a.sessions[r2.SessionId]
		a.mu.Unlock()
		require.NotNil(t, s1)
		require.NotNil(t, s2)
		require.Equal(t, s1.workspace.ID, s2.workspace.ID)
		require.Len(t, a.backend.ListWorkspaces(), 1)

		t.Cleanup(func() {
			a.backend.DetachClient(s1.workspace.ID, a.clientID)
			a.backend.DetachClient(s2.workspace.ID, a.clientID)
		})
	})

	t.Run("invalid directory", func(t *testing.T) {
		a := newTestAgent()
		_, err := a.NewSession(ctx, acpsdk.NewSessionRequest{
			Cwd:        filepath.Join(t.TempDir(), "does-not-exist"),
			McpServers: []acpsdk.McpServer{},
		})
		require.Error(t, err)
		var re *acpsdk.RequestError
		require.True(t, errors.As(err, &re))
		require.Equal(t, -32002, re.Code)
		require.Empty(t, a.backend.ListWorkspaces())
	})

	t.Run("relative cwd", func(t *testing.T) {
		a := newTestAgent()
		_, err := a.NewSession(ctx, acpsdk.NewSessionRequest{
			Cwd:        "relative/path",
			McpServers: []acpsdk.McpServer{},
		})
		require.Error(t, err)
		var re *acpsdk.RequestError
		require.True(t, errors.As(err, &re))
		require.Equal(t, -32602, re.Code)
		require.Empty(t, a.backend.ListWorkspaces())
	})
}
