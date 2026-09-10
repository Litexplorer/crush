package acp

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

// sessionFor returns the live session state for sid.
func sessionFor(t *testing.T, a *Agent, sid acpsdk.SessionId) *session {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	sess, ok := a.sessions[sid]
	require.True(t, ok)
	return sess
}

// requestPermission drives a permission request through the workspace
// permission service and returns whether it was granted.
func requestPermission(t *testing.T, sess *session, tool, action string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan struct {
		granted bool
		err     error
	}, 1)
	go func() {
		g, err := sess.workspace.Permissions.Request(ctx, permission.CreatePermissionRequest{
			SessionID: sess.sessionID,
			ToolName:  tool,
			Action:    action,
		})
		out <- struct {
			granted bool
			err     error
		}{granted: g, err: err}
	}()
	select {
	case o := <-out:
		require.NoError(t, o.err)
		return o.granted
	case <-ctx.Done():
		t.Fatal("permission request timed out")
		return false
	}
}

// findConfigOption returns the select option with the given ID.
func findConfigOption(t *testing.T, opts []acpsdk.SessionConfigOption, id acpsdk.SessionConfigId) *acpsdk.SessionConfigOptionSelect {
	t.Helper()
	for _, o := range opts {
		if o.Select != nil && o.Select.Id == id {
			return o.Select
		}
	}
	require.FailNow(t, "config option not found: %s", id)
	return nil
}

func TestInitializeAdvertisesAuth(t *testing.T) {
	a := newTestAgent()
	resp, err := a.Initialize(context.Background(), acpsdk.InitializeRequest{ProtocolVersion: 1})
	require.NoError(t, err)
	require.NotNil(t, resp.AgentCapabilities.Auth.Logout)
	require.Len(t, resp.AuthMethods, 1)
	require.NotNil(t, resp.AuthMethods[0].Agent)
	require.Equal(t, authMethodID, resp.AuthMethods[0].Agent.Id)
}

func TestNewSessionAdvertisesModesAndConfigOptions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := newEnvAgent(t, "")
	resp, err := a.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Modes)
	require.Equal(t, modeNormal, resp.Modes.CurrentModeId)
	require.Len(t, resp.Modes.AvailableModes, len(availableModes))
	require.NotEmpty(t, resp.ConfigOptions)
	require.NotNil(t, findConfigOption(t, resp.ConfigOptions, configOptionMode))
	t.Cleanup(func() { a.backend.DetachClient(sessionFor(t, a, resp.SessionId).workspace.ID, a.clientID) })
}

func TestSetSessionMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := newEnvAgent(t, "")
	sid := newSessionOn(t, a, t.TempDir())
	sess := sessionFor(t, a, sid)
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })
	require.Equal(t, modeNormal, sess.mode)

	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)

	ctx := context.Background()
	for _, want := range []acpsdk.SessionModeId{modePlan, modeYolo, modeNormal} {
		_, err := a.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{SessionId: sid, ModeId: want})
		require.NoError(t, err)
		require.Equal(t, want, sessionFor(t, a, sid).mode)
	}

	// Unsupported modes are rejected and leave the mode unchanged.
	_, err := a.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{SessionId: sid, ModeId: "bogus"})
	require.Error(t, err)
	var re *acpsdk.RequestError
	require.True(t, errors.As(err, &re))
	require.Equal(t, -32602, re.Code)
	require.Equal(t, modeNormal, sessionFor(t, a, sid).mode)

	// The client received a current_mode_update per applied change.
	require.Eventually(t, func() bool {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		var got []string
		for _, u := range cap.updates {
			if u.SessionId == sid && u.Update.CurrentModeUpdate != nil {
				got = append(got, string(u.Update.CurrentModeUpdate.CurrentModeId))
			}
		}
		return slices.Equal(got, []string{"plan", "yolo", "normal"})
	}, 2*time.Second, 10*time.Millisecond)
}

func TestSessionModeGatesPermissionRequests(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := newEnvAgent(t, "")
	sid := newSessionOn(t, a, t.TempDir())
	sess := sessionFor(t, a, sid)
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })

	// Default (normal) mode grants permission requests.
	require.True(t, requestPermission(t, sess, "bash", "exec"))

	// Plan mode denies them (read-only).
	_, err := a.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{SessionId: sid, ModeId: modePlan})
	require.NoError(t, err)
	require.False(t, requestPermission(t, sess, "bash", "exec"))

	// Yolo mode grants them again.
	_, err = a.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{SessionId: sid, ModeId: modeYolo})
	require.NoError(t, err)
	require.True(t, requestPermission(t, sess, "bash", "exec"))
}

func TestSetSessionConfigOption(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := newEnvAgent(t, "")
	sid := newSessionOn(t, a, t.TempDir())
	sess := sessionFor(t, a, sid)
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })

	ctx := context.Background()
	assertInvalid := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err)
		var re *acpsdk.RequestError
		require.True(t, errors.As(err, &re))
		require.Equal(t, -32602, re.Code)
	}

	// Unknown config IDs are rejected.
	_, err := a.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{SessionId: sid, ConfigId: "bogus", Value: "x"},
	})
	assertInvalid(t, err)

	// Boolean variant for a select-only option is rejected.
	_, err = a.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		Boolean: &acpsdk.SetSessionConfigOptionBoolean{SessionId: sid, ConfigId: configOptionMode, Value: true},
	})
	assertInvalid(t, err)

	// Setting the mode through config options mirrors setSessionMode.
	resp, err := a.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{SessionId: sid, ConfigId: configOptionMode, Value: acpsdk.SessionConfigValueId(modePlan)},
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.ConfigOptions)
	require.Equal(t, modePlan, sessionFor(t, a, sid).mode)
	modeOpt := findConfigOption(t, resp.ConfigOptions, configOptionMode)
	require.Equal(t, acpsdk.SessionConfigValueId(modePlan), modeOpt.CurrentValue)

	// Model values must be "provider/model".
	_, err = a.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{SessionId: sid, ConfigId: configOptionModel, Value: "nofrac"},
	})
	assertInvalid(t, err)

	// Unknown providers are rejected.
	_, err = a.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{SessionId: sid, ConfigId: configOptionModel, Value: "nope/model-x"},
	})
	assertInvalid(t, err)

	// A configured provider is applied and persisted to the workspace
	// config; the scripted coordinator makes the model rebuild a no-op.
	// "hyper" is a known provider, so the config reload accepts it.
	require.NoError(t, sess.workspace.Cfg.SetProviderAPIKey(config.ScopeWorkspace, "hyper", "sk-test"))
	scripted := &scriptedCoordinator{}
	scripted.app = sess.workspace.App
	sess.workspace.AgentCoordinator = scripted

	resp, err = a.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{SessionId: sid, ConfigId: configOptionModel, Value: "hyper/model-x"},
	})
	require.NoError(t, err)
	modelOpt := findConfigOption(t, resp.ConfigOptions, configOptionModel)
	require.Equal(t, acpsdk.SessionConfigValueId("hyper/model-x"), modelOpt.CurrentValue)
	cfg := sess.workspace.Cfg.Config()
	m, ok := cfg.Models[config.SelectedModelTypeLarge]
	require.True(t, ok)
	require.Equal(t, "hyper", m.Provider)
	require.Equal(t, "model-x", m.Model)
}

func TestLogoutClearsProviderCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := newEnvAgent(t, "")
	sid := newSessionOn(t, a, t.TempDir())
	sess := sessionFor(t, a, sid)
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })

	require.NoError(t, sess.workspace.Cfg.SetProviderAPIKey(config.ScopeGlobal, "hyper", "sk-test"))
	p, ok := sess.workspace.Cfg.Config().Providers.Get("hyper")
	require.True(t, ok)
	require.Equal(t, "sk-test", p.APIKey)

	_, err := a.Logout(context.Background(), acpsdk.LogoutRequest{})
	require.NoError(t, err)

	// The credentials are gone: the provider either disappeared from the
	// config or retains an empty API key.
	if p, ok := sess.workspace.Cfg.Config().Providers.Get("hyper"); ok {
		require.Empty(t, p.APIKey)
	}
}
