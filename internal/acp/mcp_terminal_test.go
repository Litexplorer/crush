package acp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestInitializeAdvertisesMcpCapabilities(t *testing.T) {
	a := newTestAgent()
	resp, err := a.Initialize(context.Background(), acpsdk.InitializeRequest{ProtocolVersion: 1})
	require.NoError(t, err)
	require.True(t, resp.AgentCapabilities.McpCapabilities.Http)
	require.True(t, resp.AgentCapabilities.McpCapabilities.Sse)
	require.False(t, resp.AgentCapabilities.McpCapabilities.Acp)
}

func TestMcpServerConfigTranslation(t *testing.T) {
	t.Run("stdio", func(t *testing.T) {
		cfg, name, ok := mcpServerConfig(acpsdk.McpServer{
			Stdio: &acpsdk.McpServerStdio{
				Name:    "srv",
				Command: "/bin/echo",
				Args:    []string{"-n"},
				Env:     []acpsdk.EnvVariable{{Name: "A", Value: "B"}},
			},
		})
		require.True(t, ok)
		require.Equal(t, "srv", name)
		require.Equal(t, config.MCPStdio, cfg.Type)
		require.Equal(t, "/bin/echo", cfg.Command)
		require.Equal(t, []string{"-n"}, cfg.Args)
		require.Equal(t, map[string]string{"A": "B"}, cfg.Env)
	})

	t.Run("http", func(t *testing.T) {
		cfg, name, ok := mcpServerConfig(acpsdk.McpServer{
			Http: &acpsdk.McpServerHttpInline{
				Name: "remote",
				Url:  "https://mcp.example.com",
				Headers: []acpsdk.HttpHeader{
					{Name: "Authorization", Value: "Bearer x"},
				},
			},
		})
		require.True(t, ok)
		require.Equal(t, "remote", name)
		require.Equal(t, config.MCPHttp, cfg.Type)
		require.Equal(t, "https://mcp.example.com", cfg.URL)
		require.Equal(t, map[string]string{"Authorization": "Bearer x"}, cfg.Headers)
	})

	t.Run("acp transport skipped", func(t *testing.T) {
		_, _, ok := mcpServerConfig(acpsdk.McpServer{
			Acp: &acpsdk.McpServerAcpInline{Name: "over-acp", Id: "x"},
		})
		require.False(t, ok)
	})
}

// newSessionWithServers creates an ACP session declaring MCP servers.
func newSessionWithServers(t *testing.T, a *Agent, cwd string, servers []acpsdk.McpServer) acpsdk.SessionId {
	t.Helper()
	resp, err := a.NewSession(context.Background(), acpsdk.NewSessionRequest{Cwd: cwd, McpServers: servers})
	require.NoError(t, err)
	return resp.SessionId
}

func TestNewSessionRegistersMcpServers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	old := mcpServerToolWait
	mcpServerToolWait = 200 * time.Millisecond
	defer func() { mcpServerToolWait = old }()

	a := newEnvAgent(t, "")
	cwd := t.TempDir()
	sid := newSessionWithServers(t, a, cwd, []acpsdk.McpServer{
		{Stdio: &acpsdk.McpServerStdio{Name: "p5srv", Command: "true"}},
		{Acp: &acpsdk.McpServerAcpInline{Name: "over-acp", Id: "acp-1"}},
	})
	sess := sessionFor(t, a, sid)
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })

	cfg := sess.workspace.Cfg.Config()
	// The stdio server is registered; the ACP-transport server is skipped.
	m, ok := cfg.MCP["p5srv"]
	require.True(t, ok)
	require.Equal(t, config.MCPStdio, m.Type)
	require.Equal(t, "true", m.Command)
	_, exists := cfg.MCP["over-acp"]
	require.False(t, exists)

	// Recorded for removal on close.
	a.mu.Lock()
	require.Equal(t, []string{"p5srv"}, a.workspaceMCPs[sess.workspace.ID])
	a.mu.Unlock()
}

func TestCloseSessionUnregistersMcpServersWhenLast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	old := mcpServerToolWait
	mcpServerToolWait = 200 * time.Millisecond
	defer func() { mcpServerToolWait = old }()

	a := newEnvAgent(t, "")
	cwd := t.TempDir()
	servers := []acpsdk.McpServer{{Stdio: &acpsdk.McpServerStdio{Name: "p5srv", Command: "true"}}}

	sid1 := newSessionWithServers(t, a, cwd, servers)
	sess1 := sessionFor(t, a, sid1)
	sid2 := newSessionWithServers(t, a, cwd, servers)
	sess2 := sessionFor(t, a, sid2)
	t.Cleanup(func() { a.backend.DetachClient(sess1.workspace.ID, a.clientID) })

	// Closing the first keeps the server (the second session still uses it).
	closeSessionOn(t, a, sid1)
	_, ok := sess1.workspace.Cfg.Config().MCP["p5srv"]
	require.True(t, ok)

	// Closing the last session removes it.
	closeSessionOn(t, a, sid2)
	_, ok = sess2.workspace.Cfg.Config().MCP["p5srv"]
	require.False(t, ok)

	a.mu.Lock()
	require.Empty(t, a.workspaceMCPs[sess1.workspace.ID])
	a.mu.Unlock()
}

func TestSessionSetsTerminalRunner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := newEnvAgent(t, "")
	cap := &captureClient{}
	cleanup := connectPeers(a, cap)
	t.Cleanup(cleanup)
	a.setClientCapabilities(acpsdk.ClientCapabilities{Terminal: true})
	sid := newSessionOn(t, a, t.TempDir())
	sess := sessionFor(t, a, sid)
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })
	require.NotNil(t, sess.workspace.Cfg.Overrides().TerminalRunner)
}

func TestTerminalClientLifecycle(t *testing.T) {
	a, sess, _, cap, _ := permissionEnv(t)
	tc := &terminalClient{a: a}

	outputCalls := 0
	cap.terminalOutputReply = func(acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
		outputCalls++
		if outputCalls == 1 {
			return acpsdk.TerminalOutputResponse{Output: "hello\n"}, nil
		}
		return acpsdk.TerminalOutputResponse{
			Output:     "hello\n",
			ExitStatus: &acpsdk.TerminalExitStatus{ExitCode: acpsdk.Ptr(3)},
		}, nil
	}
	cap.waitTerminalReply = func(acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
		return acpsdk.WaitForTerminalExitResponse{ExitCode: acpsdk.Ptr(3)}, nil
	}

	output, exitCode, err := tc.RunTerminal(context.Background(), sess.sessionID, "echo hi", nil, "/tmp")
	require.NoError(t, err)
	require.Equal(t, "hello\n", output)
	require.Equal(t, 3, exitCode)

	cap.mu.Lock()
	require.Equal(t, []string{"create", "output", "output", "wait", "release"}, cap.terminalRequests)
	cap.mu.Unlock()
}

func TestTerminalClientTimeoutKillsAndReleases(t *testing.T) {
	a, sess, _, cap, _ := permissionEnv(t)
	tc := &terminalClient{a: a}

	old := clientFileTimeout
	clientFileTimeout = 300 * time.Millisecond
	defer func() { clientFileTimeout = old }()

	// The terminal never exits: the run must time out, kill, and release.
	cap.terminalOutputReply = func(acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
		return acpsdk.TerminalOutputResponse{Output: "still running"}, nil
	}

	_, _, err := tc.RunTerminal(context.Background(), sess.sessionID, "sleep 10", nil, "/tmp")
	require.Error(t, err)

	cap.mu.Lock()
	defer cap.mu.Unlock()
	require.Contains(t, cap.terminalRequests, "kill")
	require.Contains(t, cap.terminalRequests, "release")
	// Release must come after the kill.
	require.True(t, indexOf(cap.terminalRequests, "kill") < indexOf(cap.terminalRequests, "release"))
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// stubTerminalRunner is a config.TerminalRunner for tool tests.
type stubTerminalRunner struct{}

func (stubTerminalRunner) RunTerminal(_ context.Context, _ string, command string, _ []string, _ string) (string, int, error) {
	return "ran " + command, 0, nil
}

type errTerminalRunner struct{}

func (errTerminalRunner) RunTerminal(context.Context, string, string, []string, string) (string, int, error) {
	return "", -1, errors.New("client terminal gone")
}

func TestTerminalTool(t *testing.T) {
	perms := permission.NewPermissionService(t.TempDir(), true, nil)
	ctx := context.WithValue(context.Background(), tools.SessionIDContextKey, "s1")

	run := func(tool fantasy.AgentTool, command string) fantasy.ToolResponse {
		t.Helper()
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "t1", Name: tools.TerminalToolName, Input: `{"command":"` + command + `"}`})
		require.NoError(t, err)
		return resp
	}

	t.Run("runs through the client runner", func(t *testing.T) {
		tool := tools.NewTerminalTool(stubTerminalRunner{}, perms, t.TempDir())
		resp := run(tool, "ls")
		require.False(t, resp.IsError)
		require.Contains(t, resp.Content, "ran ls")
		require.Contains(t, resp.Content, "exit code 0")
	})

	t.Run("no runner falls back to bash guidance", func(t *testing.T) {
		tool := tools.NewTerminalTool(nil, perms, t.TempDir())
		resp := run(tool, "ls")
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "bash")
	})

	t.Run("runner error suggests bash", func(t *testing.T) {
		tool := tools.NewTerminalTool(errTerminalRunner{}, perms, t.TempDir())
		resp := run(tool, "ls")
		require.True(t, resp.IsError)
		require.True(t, strings.Contains(resp.Content, "bash"))
	})
}

var _ = config.TerminalRunner(stubTerminalRunner{})
var _ = permission.PermissionRequest{}
