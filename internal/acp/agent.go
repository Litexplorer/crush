// Package acp implements the ACP (Agent Client Protocol) agent side for
// crush. It consumes the transport-agnostic internal/backend layer just
// like the HTTP server layer does; capabilities are added incrementally
// per the ACP support plan (P0 handshake, P1 sessions and prompts, ...).
package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/agent"
	mcptools "github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/version"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

// Agent implements the ACP Agent interface. Methods are filled in phase
// by phase per the ACP support plan.
type Agent struct {
	conn    *acpsdk.AgentSideConnection
	backend *backend.Backend

	// dataDir is the data directory whose database backs sessions. It
	// is passed in by the caller (cmd/acp) so session history can be
	// listed, loaded, and resumed even before any workspace exists.
	dataDir string

	// clientID identifies this agent's single ACP client to the
	// backend; it is generated once per connection.
	clientID string

	mu                 sync.Mutex
	sessions           map[acpsdk.SessionId]*session
	clientCapabilities acpsdk.ClientCapabilities

	// workspaceMCPs records the MCP server names registered per
	// workspace from client-declared session requests (US-015), so they
	// can be removed when the last session on the workspace closes.
	workspaceMCPs map[string][]string
}

// session holds per-session state: the backend workspace backing it,
// the crush session ID created in that workspace, plus mode and config
// options (setSessionMode / setSessionConfigOption). modeCancel cancels
// the session's permission watcher when the mode changes or the session
// closes.
type session struct {
	workspace      *backend.Workspace
	sessionID      string
	mode           acpsdk.SessionModeId
	modeCancel     context.CancelFunc
	questionCancel context.CancelFunc
	configOptions  []acpsdk.SessionConfigOption
}

var _ acpsdk.Agent = (*Agent)(nil)
var _ acpsdk.AgentLoader = (*Agent)(nil)

// NewAgent returns an Agent bound to the given backend, with no active
// sessions. dataDir is the data directory whose database holds session
// history; it may be empty when only live-session operations are used.
func NewAgent(b *backend.Backend, dataDir string) *Agent {
	return &Agent{
		backend:       b,
		dataDir:       dataDir,
		clientID:      uuid.New().String(),
		sessions:      make(map[acpsdk.SessionId]*session),
		workspaceMCPs: make(map[string][]string),
	}
}

// ACP session modes map onto crush's permission model:
//
//	modePlan   "plan"   -> read-only: every permission request for the
//	                       session is denied.
//	modeNormal "normal" -> the default: permission requests are
//	                       forwarded to the ACP client for approval.
//	modeYolo   "yolo"   -> skip permission checks; every request is
//	                       granted.
const (
	modePlan   acpsdk.SessionModeId = "plan"
	modeNormal acpsdk.SessionModeId = "normal"
	modeYolo   acpsdk.SessionModeId = "yolo"
)

// availableModes is advertised in initialize and returned with session
// responses so clients can offer a mode picker.
var availableModes = []acpsdk.SessionMode{
	{Id: modePlan, Name: "Plan", Description: acpsdk.Ptr("Read-only: permission requests are denied")},
	{Id: modeNormal, Name: "Normal", Description: acpsdk.Ptr("Default: permission requests are sent to the client for approval")},
	{Id: modeYolo, Name: "YOLO", Description: acpsdk.Ptr("Skip permission checks")},
}

// session config option IDs: the whitelist enforced by
// SetSessionConfigOption.
const (
	configOptionMode  acpsdk.SessionConfigId = "mode"
	configOptionModel acpsdk.SessionConfigId = "model"
)

func defaultMode() acpsdk.SessionModeId { return modeNormal }

func isValidMode(m acpsdk.SessionModeId) bool {
	switch m {
	case modePlan, modeNormal, modeYolo:
		return true
	}
	return false
}

func modeState(m acpsdk.SessionModeId) *acpsdk.SessionModeState {
	return &acpsdk.SessionModeState{AvailableModes: availableModes, CurrentModeId: m}
}

// registryDataDir resolves the data directory whose database holds the
// given cwd's sessions. It mirrors the backend's own resolution (a
// config.Init call with the request cwd), so registry reads and
// workspace writes always target the same SQLite file. When cwd is
// empty it falls back to a live session's data directory, then to the
// data directory passed to NewAgent.
func (a *Agent) registryDataDir(ctx context.Context, cwd string) string {
	if cwd != "" {
		if store, err := config.Init(cwd, "", false); err == nil {
			if dd := store.Config().Options.DataDirectory; dd != "" {
				return dd
			}
		}
	}
	return a.resolvedDataDir()
}

// resolvedDataDir returns the data directory backing session history:
// the one passed to NewAgent, or, failing that, the data directory of
// any live session's workspace (used by tests that let the backend
// resolve it).
func (a *Agent) resolvedDataDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dataDir != "" {
		return a.dataDir
	}
	for _, s := range a.sessions {
		if s.workspace != nil && s.workspace.Cfg != nil {
			if dd := s.workspace.Cfg.Config().Options.DataDirectory; dd != "" {
				return dd
			}
		}
	}
	return ""
}

// Attach binds the agent to its connection. The connection is created
// with the agent itself as its peer, so this must be called immediately
// after acpsdk.NewAgentSideConnection.
func (a *Agent) Attach(conn *acpsdk.AgentSideConnection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.conn = conn
}

// connection returns the attached agent-side connection, if any.
func (a *Agent) connection() *acpsdk.AgentSideConnection {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn
}

// fsCapabilities returns the attached connection and the file system
// capabilities the client advertised during initialize.
func (a *Agent) fsCapabilities() (*acpsdk.AgentSideConnection, acpsdk.FileSystemCapabilities) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn, a.clientCapabilities.Fs
}

func (a *Agent) setClientCapabilities(c acpsdk.ClientCapabilities) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clientCapabilities = c
}

// authMethodID is the single authentication method the agent offers.
// Authentication itself is out-of-band, like the reference client: the
// user runs `crush login` in a terminal and the provider credentials
// land in the shared config, which the agent picks up on the next run.
// authenticate just validates the requested method ID.
const authMethodID = "crush-login"

// Authenticate accepts the out-of-band auth method advertised in
// initialize. Unknown methods are rejected with InvalidParams.
func (a *Agent) Authenticate(ctx context.Context, params acpsdk.AuthenticateRequest) (acpsdk.AuthenticateResponse, error) {
	if params.MethodId != authMethodID {
		return acpsdk.AuthenticateResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": fmt.Sprintf("unknown auth method: %s", params.MethodId),
		})
	}
	return acpsdk.AuthenticateResponse{}, nil
}

func (a *Agent) Initialize(ctx context.Context, params acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	if params.ProtocolVersion < acpsdk.ProtocolVersionNumber {
		return acpsdk.InitializeResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": fmt.Sprintf("unsupported protocol version %d", params.ProtocolVersion),
		})
	}

	a.setClientCapabilities(params.ClientCapabilities)

	v := params.ProtocolVersion
	if v > acpsdk.ProtocolVersionNumber {
		v = acpsdk.ProtocolVersionNumber
	}

	return acpsdk.InitializeResponse{
		ProtocolVersion: v,
		AgentCapabilities: acpsdk.AgentCapabilities{
			SessionCapabilities: acpsdk.SessionCapabilities{
				Close:  &acpsdk.SessionCloseCapabilities{},
				List:   &acpsdk.SessionListCapabilities{},
				Resume: &acpsdk.SessionResumeCapabilities{},
			},
			LoadSession: true,
			Auth: acpsdk.AgentAuthCapabilities{
				Logout: &acpsdk.LogoutCapabilities{},
			},
			// MCP servers declared by the client in session requests are
			// registered into crush's own MCP pipeline (US-015). The
			// MCP-over-ACP transport is not offered: SDK v0.13.5 has no
			// mcp/message dispatch.
			McpCapabilities: acpsdk.McpCapabilities{Http: true, Sse: true},
		},
		AgentInfo: &acpsdk.Implementation{
			Name:    "Crush",
			Version: version.Version,
		},
		AuthMethods: []acpsdk.AuthMethod{
			{Agent: &acpsdk.AuthMethodAgent{
				Id:          authMethodID,
				Name:        "Login with crush",
				Description: acpsdk.Ptr("Run `crush login` in your terminal to authenticate with a provider"),
			}},
		},
	}, nil
}

// Logout clears the stored provider credentials, mirroring `crush
// logout`. It is connection-scoped: it clears credentials through each
// live workspace's config store; out-of-band `crush login` restores
// them.
func (a *Agent) Logout(ctx context.Context, params acpsdk.LogoutRequest) (acpsdk.LogoutResponse, error) {
	a.mu.Lock()
	stores := make(map[*config.ConfigStore]struct{})
	for _, s := range a.sessions {
		if s.workspace != nil && s.workspace.Cfg != nil {
			stores[s.workspace.Cfg] = struct{}{}
		}
	}
	a.mu.Unlock()

	for store := range stores {
		for _, key := range []string{
			"providers.hyper.api_key",
			"providers.hyper.oauth",
			"providers.copilot.api_key",
			"providers.copilot.oauth",
		} {
			_ = store.RemoveConfigField(config.ScopeGlobal, key)
		}
	}
	return acpsdk.LogoutResponse{}, nil
}

func (a *Agent) Cancel(ctx context.Context, params acpsdk.CancelNotification) error {
	a.mu.Lock()
	_, ok := a.sessions[params.SessionId]
	a.mu.Unlock()
	if !ok {
		return resourceNotFound(fmt.Sprintf("session not found: %s", params.SessionId))
	}
	// The SDK cancels the in-flight prompt context before calling this
	// method; the coordinator run observes it and returns. No further
	// crush-side bookkeeping is needed until per-session run tracking
	// lands.
	return nil
}

func (a *Agent) CloseSession(ctx context.Context, params acpsdk.CloseSessionRequest) (acpsdk.CloseSessionResponse, error) {
	a.mu.Lock()
	sess, ok := a.sessions[params.SessionId]
	if !ok {
		a.mu.Unlock()
		return acpsdk.CloseSessionResponse{}, resourceNotFound(fmt.Sprintf("session not found: %s", params.SessionId))
	}
	delete(a.sessions, params.SessionId)
	a.stopPermissionWatcherLocked(sess)
	a.stopQuestionWatcherLocked(sess)
	ws := sess.workspace
	sessionID := sess.sessionID
	lastOnWorkspace := true
	for _, remaining := range a.sessions {
		if remaining.workspace.ID == ws.ID {
			lastOnWorkspace = false
			break
		}
	}
	a.mu.Unlock()

	// Client-declared MCP servers (US-015) are removed once the last
	// session on the workspace closes, so nothing lingers.
	if lastOnWorkspace {
		a.unregisterMcpServers(ctx, ws)
	}

	// Treat close like session/cancel: cancel any in-flight prompt work
	// for this session, then release the workspace claim so it tears
	// down once the last session sharing it closes.
	if ws.AgentCoordinator != nil {
		ws.AgentCoordinator.Cancel(sessionID)
	}
	a.backend.DetachClient(ws.ID, a.clientID)
	return acpsdk.CloseSessionResponse{}, nil
}

// CloseAll releases every open session's workspace claim and empties
// the session map. It is called when the client disconnects so no
// workspace outlives the connection.
func (a *Agent) CloseAll() {
	a.mu.Lock()
	type ref struct {
		workspaceID string
		sessionID   string
		coordinator agent.Coordinator
	}
	refs := make([]ref, 0, len(a.sessions))
	for id, s := range a.sessions {
		refs = append(refs, ref{
			workspaceID: s.workspace.ID,
			sessionID:   s.sessionID,
			coordinator: s.workspace.AgentCoordinator,
		})
		a.stopPermissionWatcherLocked(s)
		a.stopQuestionWatcherLocked(s)
		delete(a.sessions, id)
	}
	a.mu.Unlock()

	for _, r := range refs {
		if r.coordinator != nil {
			r.coordinator.Cancel(r.sessionID)
		}
		a.backend.DetachClient(r.workspaceID, a.clientID)
	}
}

// listSessionsLimit caps each page of session/list results, matching
// the reference client's pagination size.
const listSessionsLimit = 100

// listEntry is a session/list result before serialization, carrying the
// numeric timestamp needed for cursor pagination.
type listEntry struct {
	sessionID acpsdk.SessionId
	cwd       string
	title     string
	updatedAt int64 // Unix ms; 0 when unknown
}

func (a *Agent) ListSessions(ctx context.Context, params acpsdk.ListSessionsRequest) (acpsdk.ListSessionsResponse, error) {
	// Sessions currently open on this connection first: their titles
	// come straight from the live session service.
	byID := make(map[acpsdk.SessionId]listEntry)
	a.mu.Lock()
	for id, s := range a.sessions {
		if params.Cwd != nil && filepath.Clean(*params.Cwd) != filepath.Clean(s.workspace.Path) {
			continue
		}
		entry := listEntry{sessionID: id, cwd: s.workspace.Path}
		if cs, err := s.workspace.App.Sessions.Get(ctx, s.sessionID); err == nil {
			entry.title = cs.Title
			entry.updatedAt = sessionTimestampMs(cs.UpdatedAt)
		}
		byID[id] = entry
	}
	a.mu.Unlock()

	// Then every persisted session from the registry, skipping any ID
	// already reported by the live map (the live entry is fresher).
	var cwdFilter string
	if params.Cwd != nil {
		cwdFilter = *params.Cwd
	}
	if dataDir := a.registryDataDir(ctx, cwdFilter); dataDir != "" {
		var cwd *string
		if params.Cwd != nil {
			c := filepath.Clean(*params.Cwd)
			cwd = &c
		}
		records, err := listRegistry(ctx, dataDir, cwd)
		if err != nil {
			return acpsdk.ListSessionsResponse{}, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
		}
		for _, r := range records {
			id := acpsdk.SessionId(r.sessionID)
			if _, ok := byID[id]; ok {
				continue
			}
			byID[id] = listEntry{sessionID: id, cwd: r.cwd, title: r.title, updatedAt: sessionTimestampMs(r.updatedAt)}
		}
	}

	entries := make([]listEntry, 0, len(byID))
	for _, e := range byID {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].updatedAt > entries[j].updatedAt })

	// Cursor-based pagination: the cursor is the updatedAt (ms) of the
	// last item of the previous page; only strictly older items follow.
	if params.Cursor != nil && *params.Cursor != "" {
		if ts, err := strconv.ParseInt(*params.Cursor, 10, 64); err == nil {
			kept := entries[:0]
			for _, e := range entries {
				if e.updatedAt < ts {
					kept = append(kept, e)
				}
			}
			entries = kept
		}
	}

	var nextCursor *string
	if len(entries) > listSessionsLimit {
		if last := entries[listSessionsLimit-1]; last.updatedAt > 0 {
			nextCursor = acpsdk.Ptr(strconv.FormatInt(last.updatedAt, 10))
		}
		entries = entries[:listSessionsLimit]
	}

	sessions := make([]acpsdk.SessionInfo, 0, len(entries))
	for _, e := range entries {
		info := acpsdk.SessionInfo{SessionId: e.sessionID, Cwd: e.cwd}
		if e.title != "" {
			info.Title = acpsdk.Ptr(e.title)
		}
		info.UpdatedAt = formatTimestamp(e.updatedAt)
		sessions = append(sessions, info)
	}
	return acpsdk.ListSessionsResponse{Sessions: sessions, NextCursor: nextCursor}, nil
}

// sessionTimestampMs normalizes a crush session timestamp (seconds or
// milliseconds, both occur in the schema) to milliseconds.
func sessionTimestampMs(ts int64) int64 {
	if ts <= 0 {
		return 0
	}
	if ts > 1_000_000_000_000 {
		return ts
	}
	return ts * 1000
}

// formatTimestamp renders a Unix-ms timestamp as an ISO 8601 string,
// or nil when the timestamp is unknown.
func formatTimestamp(ms int64) *string {
	if ms <= 0 {
		return nil
	}
	return acpsdk.Ptr(time.UnixMilli(ms).UTC().Format(time.RFC3339))
}

func (a *Agent) NewSession(ctx context.Context, params acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	if err := validateCwd(params.Cwd); err != nil {
		return acpsdk.NewSessionResponse{}, err
	}

	ws, _, err := a.backend.CreateWorkspace(proto.Workspace{
		Path:     params.Cwd,
		ClientID: a.clientID,
		Version:  version.Version,
		Env:      os.Environ(),
	})
	if err != nil {
		return acpsdk.NewSessionResponse{}, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}

	// Convert the creation hold into a stream claim so the workspace
	// stays alive for the lifetime of the session. On failure, release
	// the hold we just created.
	if err := a.backend.AttachClient(ws.ID, a.clientID); err != nil {
		_ = a.backend.DeleteWorkspace(ws.ID, a.clientID)
		return acpsdk.NewSessionResponse{}, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}

	crushSess, err := ws.App.Sessions.Create(ctx, agent.DefaultSessionName)
	if err != nil {
		a.backend.DetachClient(ws.ID, a.clientID)
		return acpsdk.NewSessionResponse{}, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}

	// The ACP session ID is the crush session ID: messages, history,
	// and load/resume all key off it. The registry persists the cwd
	// mapping so the session survives process restarts. On registry
	// failure the session is not usable across connections, so fail the
	// request and release the workspace claim.
	dd := ""
	if ws.Cfg != nil {
		dd = ws.Cfg.Config().Options.DataDirectory
	}
	if err := upsertRegistry(ctx, dd, crushSess.ID, params.Cwd); err != nil {
		a.backend.DetachClient(ws.ID, a.clientID)
		return acpsdk.NewSessionResponse{}, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}

	// Route file reads and writes through the ACP client (US-014)
	// when it advertises file system support; tools fall back to the
	// local file system otherwise.
	if ws.Cfg != nil {
		ws.Cfg.Overrides().FileClient = &fileClient{a: a}
		// Client-declared MCP servers join the workspace's MCP pipeline
		// (US-015); a client terminal runner exposes the terminal tool
		// (US-016).
		a.registerMcpServers(ctx, ws, params.McpServers)
		if a.clientSupportsTerminal() {
			ws.Cfg.Overrides().TerminalRunner = &terminalClient{a: a}
		}
	}

	sessionID := acpsdk.SessionId(crushSess.ID)
	a.mu.Lock()
	sess := &session{workspace: ws, sessionID: crushSess.ID, mode: defaultMode()}
	sess.configOptions = a.sessionConfigOptionsLocked(sess)
	a.startPermissionWatcherLocked(sess, sess.mode)
	a.startQuestionWatcherLocked(sess)
	a.sessions[sessionID] = sess
	opts := sess.configOptions
	mode := sess.mode
	a.mu.Unlock()

	return acpsdk.NewSessionResponse{
		SessionId:     sessionID,
		ConfigOptions: opts,
		Modes:         modeState(mode),
	}, nil
}

// validateCwd checks that cwd is an absolute path to an existing
// directory, returning a JSON-RPC error otherwise.
func validateCwd(cwd string) error {
	if cwd == "" {
		return acpsdk.NewInvalidParams(map[string]any{"error": "cwd is required"})
	}
	if !filepath.IsAbs(cwd) {
		return acpsdk.NewInvalidParams(map[string]any{"error": "cwd must be an absolute path"})
	}
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return resourceNotFound(fmt.Sprintf("directory not found: %s", cwd))
	}
	return nil
}

func (a *Agent) Prompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	a.mu.Lock()
	sess, ok := a.sessions[params.SessionId]
	a.mu.Unlock()
	if !ok {
		return acpsdk.PromptResponse{}, resourceNotFound(fmt.Sprintf("session not found: %s", params.SessionId))
	}

	text := promptText(params.Prompt)
	if strings.TrimSpace(text) == "" {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": "prompt contains no text"})
	}

	ws := sess.workspace
	if ws.AgentCoordinator == nil {
		return acpsdk.PromptResponse{}, acpsdk.NewInternalError(map[string]any{"error": "agent not initialized"})
	}

	// Permission requests are resolved by the session's mode watcher
	// (see applyMode): plan denies them, normal and yolo grant them.

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Subscribe before the run starts so no chunk is missed.
	events := ws.App.Messages.Subscribe(runCtx)
	readBytes := make(map[string]int)
	// readThinking tracks how much of each assistant message's reasoning
	// content has been relayed (US-020). Like the text part, the
	// thinking part grows monotonically, so a byte cursor suffices.
	readThinking := make(map[string]int)
	// Tool call state per prompt (US-021). IDs are provider-issued and
	// unique within a session, so a flat set per stage is enough:
	// started once announced, inputComplete once the provider finished
	// streaming the call's input, settled once its result was relayed.
	startedToolCalls := make(map[string]bool)
	inputCompleteToolCalls := make(map[string]bool)
	settledToolCalls := make(map[string]bool)

	done := make(chan error, 1)
	go func() {
		_, err := ws.AgentCoordinator.Run(runCtx, sess.sessionID, text)
		done <- err
	}()

	stream := func(msg message.Message) {
		if msg.SessionID != sess.sessionID || len(msg.Parts) == 0 {
			return
		}

		// Tool messages carry the outcome of a previously announced
		// tool call; settle the card with the result (US-021).
		if msg.Role == message.Tool {
			for _, tr := range msg.ToolResults() {
				if !startedToolCalls[tr.ToolCallID] || settledToolCalls[tr.ToolCallID] {
					// Result for an unknown or already-settled call
					// (e.g. replayed history, duplicate results): drop.
					continue
				}
				status := acpsdk.ToolCallStatusCompleted
				if tr.IsError {
					status = acpsdk.ToolCallStatusFailed
				}
				opts := []acpsdk.ToolCallUpdateOpt{acpsdk.WithUpdateStatus(status)}
				if tr.Content != "" {
					opts = append(opts,
						acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock(tr.Content))}),
						acpsdk.WithUpdateRawOutput(parseJSONValue(tr.Content)),
					)
				}
				if err := a.conn.SessionUpdate(runCtx, acpsdk.SessionNotification{
					SessionId: params.SessionId,
					Update:    acpsdk.UpdateToolCall(acpsdk.ToolCallId(tr.ToolCallID), opts...),
				}); err != nil {
					// The client went away; tear the run down.
					cancel()
					return
				}
				settledToolCalls[tr.ToolCallID] = true
			}
			return
		}
		if msg.Role != message.Assistant {
			return
		}

		// Reasoning deltas (US-020): the thinking part grows ahead of
		// the text part and is relayed on its own channel.
		if thinking := msg.ReasoningContent().Thinking; len(thinking) > readThinking[msg.ID] {
			if err := a.conn.SessionUpdate(runCtx, acpsdk.SessionNotification{
				SessionId: params.SessionId,
				Update:    acpsdk.UpdateAgentThoughtText(thinking[readThinking[msg.ID]:]),
			}); err != nil {
				// The client went away; tear the run down.
				cancel()
				return
			}
			readThinking[msg.ID] = len(thinking)
		}

		// Tool calls (US-021): announce new calls as they appear,
		// mark input-complete once the provider stops streaming the
		// call's input, and let the result settle the card later.
		for _, tc := range msg.ToolCalls() {
			if !startedToolCalls[tc.ID] {
				status := acpsdk.ToolCallStatusPending
				if tc.Finished {
					status = acpsdk.ToolCallStatusInProgress
				}
				opts := []acpsdk.ToolCallStartOpt{
					acpsdk.WithStartKind(toolCallKind(tc.Name)),
					acpsdk.WithStartStatus(status),
				}
				if tc.Input != "" {
					opts = append(opts, acpsdk.WithStartRawInput(parseJSONValue(tc.Input)))
				}
				if err := a.conn.SessionUpdate(runCtx, acpsdk.SessionNotification{
					SessionId: params.SessionId,
					Update:    acpsdk.StartToolCall(acpsdk.ToolCallId(tc.ID), tc.Name, opts...),
				}); err != nil {
					// The client went away; tear the run down.
					cancel()
					return
				}
				startedToolCalls[tc.ID] = true
			}
			if tc.Finished && !inputCompleteToolCalls[tc.ID] {
				opts := []acpsdk.ToolCallUpdateOpt{
					acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
				}
				if tc.Input != "" {
					opts = append(opts, acpsdk.WithUpdateRawInput(parseJSONValue(tc.Input)))
				}
				if err := a.conn.SessionUpdate(runCtx, acpsdk.SessionNotification{
					SessionId: params.SessionId,
					Update:    acpsdk.UpdateToolCall(acpsdk.ToolCallId(tc.ID), opts...),
				}); err != nil {
					// The client went away; tear the run down.
					cancel()
					return
				}
				inputCompleteToolCalls[tc.ID] = true
			}
		}

		content := msg.Content().String()
		read := readBytes[msg.ID]
		if len(content) <= read {
			return
		}
		part := content[read:]
		if read == 0 {
			part = strings.TrimLeft(part, " \t")
		}
		if strings.TrimSpace(part) == "" {
			readBytes[msg.ID] = len(content)
			return
		}
		if err := a.conn.SessionUpdate(runCtx, acpsdk.SessionNotification{
			SessionId: params.SessionId,
			Update:    acpsdk.UpdateAgentMessageText(part),
		}); err != nil {
			// The client went away; tear the run down.
			cancel()
			return
		}
		readBytes[msg.ID] = len(content)
	}

	for {
		select {
		case err := <-done:
			// The run's terminal message update is published before
			// Run returns, so drain the channel to catch the final
			// chunk.
			for {
				select {
				case event := <-events:
					stream(event.Payload)
				default:
					if err != nil {
						return acpsdk.PromptResponse{}, err
					}
					// The turn is over; push the session's context
					// usage so the client panel stays current
					// (US-024). Best-effort: failures must not
					// change the prompt result.
					a.pushSessionUsage(ctx, params.SessionId, sess)
					return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
				}
			}
		case event := <-events:
			stream(event.Payload)
		case <-runCtx.Done():
			return acpsdk.PromptResponse{}, runCtx.Err()
		}
	}
}

// promptText concatenates the text content blocks of a prompt.
func promptText(blocks []acpsdk.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Text != nil {
			sb.WriteString(b.Text.Text)
		}
	}
	return sb.String()
}

// LoadSession implements acpsdk.AgentLoader. It restores a persisted
// ACP session: the registry maps the session ID to its cwd, the
// backing workspace is (re)created there, and the crush session's
// message history is picked up by the next prompt automatically.
func (a *Agent) LoadSession(ctx context.Context, params acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	if err := validateCwd(params.Cwd); err != nil {
		return acpsdk.LoadSessionResponse{}, err
	}
	sess, err := a.activateSession(ctx, params.Cwd, params.SessionId, params.McpServers)
	if err != nil {
		return acpsdk.LoadSessionResponse{}, err
	}
	a.mu.Lock()
	opts := sess.configOptions
	mode := sess.mode
	a.mu.Unlock()
	return acpsdk.LoadSessionResponse{ConfigOptions: opts, Modes: modeState(mode)}, nil
}

// activateSession makes a persisted session live again: it validates
// the registry mapping and cwd, recreates the backing workspace,
// verifies the crush session still exists, and registers the session
// for subsequent prompts. Loading an already-live session is a no-op.
func (a *Agent) activateSession(ctx context.Context, cwd string, sessionID acpsdk.SessionId, servers []acpsdk.McpServer) (*session, error) {
	a.mu.Lock()
	_, ok := a.sessions[sessionID]
	a.mu.Unlock()
	if ok {
		a.mu.Lock()
		sess := a.sessions[sessionID]
		a.mu.Unlock()
		return sess, nil
	}

	dataDir := a.registryDataDir(ctx, cwd)
	if dataDir == "" {
		return nil, resourceNotFound(fmt.Sprintf("session not found: %s", sessionID))
	}
	rec, err := getRegistry(ctx, dataDir, string(sessionID))
	if err != nil {
		return nil, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	if rec == nil || filepath.Clean(rec.cwd) != filepath.Clean(cwd) {
		return nil, resourceNotFound(fmt.Sprintf("session not found: %s", sessionID))
	}

	ws, _, err := a.backend.CreateWorkspace(proto.Workspace{
		Path:     cwd,
		ClientID: a.clientID,
		Version:  version.Version,
		Env:      os.Environ(),
	})
	if err != nil {
		return nil, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	if err := a.backend.AttachClient(ws.ID, a.clientID); err != nil {
		_ = a.backend.DeleteWorkspace(ws.ID, a.clientID)
		return nil, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	if _, err := ws.App.Sessions.Get(ctx, string(sessionID)); err != nil {
		// The registry row survived but the crush session is gone;
		// release the claim we just took.
		a.backend.DetachClient(ws.ID, a.clientID)
		return nil, resourceNotFound(fmt.Sprintf("session not found: %s", sessionID))
	}

	// Route file reads and writes through the ACP client (US-014),
	// same as NewSession, plus client-declared MCP servers (US-015)
	// and the client terminal runner (US-016).
	if ws.Cfg != nil {
		ws.Cfg.Overrides().FileClient = &fileClient{a: a}
		a.registerMcpServers(ctx, ws, servers)
		if a.clientSupportsTerminal() {
			ws.Cfg.Overrides().TerminalRunner = &terminalClient{a: a}
		}
	}

	a.mu.Lock()
	sess := &session{workspace: ws, sessionID: string(sessionID), mode: defaultMode()}
	sess.configOptions = a.sessionConfigOptionsLocked(sess)
	a.startPermissionWatcherLocked(sess, sess.mode)
	a.startQuestionWatcherLocked(sess)
	a.sessions[sessionID] = sess
	a.mu.Unlock()

	// The session panel opens on load/resume; send its current usage
	// right away so the client has data without waiting for a prompt
	// (US-024). Best-effort, same as the turn-end push.
	a.pushSessionUsage(ctx, sessionID, sess)
	return sess, nil
}

// ResumeSession restores a persisted session like LoadSession. The ACP
// protocol has no prompt payload on resume; the client follows up with
// session/prompt, which streams through the standard prompt path with
// the full message history restored.
func (a *Agent) ResumeSession(ctx context.Context, params acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	if err := validateCwd(params.Cwd); err != nil {
		return acpsdk.ResumeSessionResponse{}, err
	}
	sess, err := a.activateSession(ctx, params.Cwd, params.SessionId, params.McpServers)
	if err != nil {
		return acpsdk.ResumeSessionResponse{}, err
	}
	a.mu.Lock()
	opts := sess.configOptions
	mode := sess.mode
	a.mu.Unlock()
	return acpsdk.ResumeSessionResponse{ConfigOptions: opts, Modes: modeState(mode)}, nil
}

func (a *Agent) SetSessionConfigOption(ctx context.Context, params acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	var (
		configID  acpsdk.SessionConfigId
		value     string
		sessionID acpsdk.SessionId
	)
	switch {
	case params.ValueId != nil:
		configID = params.ValueId.ConfigId
		value = string(params.ValueId.Value)
		sessionID = params.ValueId.SessionId
	case params.Boolean != nil:
		configID = params.Boolean.ConfigId
		value = strconv.FormatBool(params.Boolean.Value)
		sessionID = params.Boolean.SessionId
	default:
		return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": "config option value is required"})
	}

	a.mu.Lock()
	sess, ok := a.sessions[sessionID]
	a.mu.Unlock()
	if !ok {
		return acpsdk.SetSessionConfigOptionResponse{}, resourceNotFound(fmt.Sprintf("session not found: %s", sessionID))
	}

	switch configID {
	case configOptionMode:
		if params.Boolean != nil {
			return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": "mode expects a select value"})
		}
		mode := acpsdk.SessionModeId(value)
		changed, err := a.applyMode(mode, sess)
		if err != nil {
			return acpsdk.SetSessionConfigOptionResponse{}, err
		}
		if changed {
			a.notifyMode(ctx, sessionID, mode)
		}
	case configOptionModel:
		if params.Boolean != nil {
			return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": "model expects a select value"})
		}
		if err := a.applyModel(ctx, sess, value); err != nil {
			return acpsdk.SetSessionConfigOptionResponse{}, err
		}
	default:
		return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": fmt.Sprintf("unknown config option: %s", configID),
		})
	}

	a.mu.Lock()
	opts := a.sessionConfigOptionsLocked(sess)
	sess.configOptions = opts
	a.mu.Unlock()

	a.notifyConfigOptions(ctx, sessionID, opts)
	return acpsdk.SetSessionConfigOptionResponse{ConfigOptions: opts}, nil
}

func (a *Agent) SetSessionMode(ctx context.Context, params acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, error) {
	a.mu.Lock()
	sess, ok := a.sessions[params.SessionId]
	a.mu.Unlock()
	if !ok {
		return acpsdk.SetSessionModeResponse{}, resourceNotFound(fmt.Sprintf("session not found: %s", params.SessionId))
	}
	changed, err := a.applyMode(params.ModeId, sess)
	if err != nil {
		return acpsdk.SetSessionModeResponse{}, err
	}
	if changed {
		a.notifyMode(ctx, params.SessionId, params.ModeId)
	}
	return acpsdk.SetSessionModeResponse{}, nil
}

// applyMode validates and applies a mode change for a live session. It
// swaps the session's permission watcher so the new mode's policy takes
// effect immediately. It reports whether the mode actually changed.
func (a *Agent) applyMode(mode acpsdk.SessionModeId, sess *session) (bool, error) {
	if !isValidMode(mode) {
		return false, acpsdk.NewInvalidParams(map[string]any{
			"error": fmt.Sprintf("unsupported mode: %s", mode),
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if sess.mode == mode {
		return false, nil
	}
	a.stopPermissionWatcherLocked(sess)
	sess.mode = mode
	a.startPermissionWatcherLocked(sess, mode)
	return true, nil
}

// startPermissionWatcherLocked installs the per-session permission
// handler (US-013): plan denies every request (read-only), yolo grants
// every request, and normal forwards the request to the ACP client for
// approval, denying on timeout, client error, or non-approval. Callers
// must hold a.mu.
func (a *Agent) startPermissionWatcherLocked(sess *session, mode acpsdk.SessionModeId) {
	ctx, cancel := context.WithCancel(context.Background())
	sess.modeCancel = cancel
	events := sess.workspace.Permissions.Subscribe(ctx)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				p := ev.Payload
				if p.SessionID != sess.sessionID {
					continue
				}
				switch mode {
				case modePlan:
					sess.workspace.Permissions.Deny(p)
				case modeYolo:
					sess.workspace.Permissions.Grant(p)
				default:
					a.forwardPermissionRequest(ctx, sess, p)
				}
			}
		}
	}()
}

// stopPermissionWatcherLocked cancels the session's permission watcher,
// if any. Callers must hold a.mu.
func (a *Agent) stopPermissionWatcherLocked(sess *session) {
	if sess.modeCancel != nil {
		sess.modeCancel()
		sess.modeCancel = nil
	}
}

// startQuestionWatcherLocked installs the per-session question handler
// (US-022): when the model calls the question tool, question.Service
// publishes the batch over pubsub and blocks; this watcher relays each
// question to the ACP client as a requestPermission and feeds the
// client's choice back through Answer, so Ask never hangs in ACP mode
// (where no TUI consumes question events). Callers must hold a.mu.
func (a *Agent) startQuestionWatcherLocked(sess *session) {
	ctx, cancel := context.WithCancel(context.Background())
	sess.questionCancel = cancel
	events := sess.workspace.App.Questions.Subscribe(ctx)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				req := ev.Payload
				if req.SessionID != sess.sessionID {
					continue
				}
				a.forwardQuestionRequest(ctx, sess, req)
			}
		}
	}()
}

// stopQuestionWatcherLocked cancels the session's question watcher, if
// any. Callers must hold a.mu.
func (a *Agent) stopQuestionWatcherLocked(sess *session) {
	if sess.questionCancel != nil {
		sess.questionCancel()
		sess.questionCancel = nil
	}
}

// questionRequestTimeout bounds how long the agent waits for the
// client to answer a question before cancelling it (US-022). Tests may
// shorten it.
var questionRequestTimeout = 5 * time.Minute

// forwardQuestionRequest relays a question batch to the ACP client
// (US-022). Each question becomes one requestPermission; answers are
// collected and delivered to the question service in one Answer call so
// the tool's Ask returns. A cancel, client error, or timeout cancels
// the pending question (the tool surfaces it as a cancellation and, for
// questions that cannot be expressed, the model can switch tactics).
// With no client attached (in-process use) the batch is auto-answered,
// matching the permission auto-grant behavior.
func (a *Agent) forwardQuestionRequest(ctx context.Context, sess *session, req question.Request) {
	conn := a.connection()
	if conn == nil {
		sess.workspace.App.Questions.Answer(autoAnswers(req.Questions))
		return
	}

	answers := make([]question.Answer, 0, len(req.Questions))
	for _, q := range req.Questions {
		if !canExpressQuestion(q) {
			// US-023: multi_choice / free_text cannot be carried by
			// requestPermission options. Cancel so the tool returns a
			// StopTurn error and the model can switch tactics; see
			// US-023 for richer degradation options.
			sess.workspace.App.Questions.Cancel()
			return
		}
		rctx, cancel := context.WithTimeout(ctx, questionRequestTimeout)
		resp, err := conn.RequestPermission(rctx, questionToPermission(req, q))
		cancel()
		if err != nil {
			sess.workspace.App.Questions.Cancel()
			return
		}
		if resp.Outcome.Cancelled != nil {
			sess.workspace.App.Questions.Cancel()
			return
		}
		if resp.Outcome.Selected == nil {
			sess.workspace.App.Questions.Cancel()
			return
		}
		answer, ok := permissionOutcomeToAnswer(q, resp.Outcome)
		if !ok {
			sess.workspace.App.Questions.Cancel()
			return
		}
		answers = append(answers, answer)
	}
	sess.workspace.App.Questions.Answer(answers)
}

// canExpressQuestion reports whether a question type can be carried by
// a requestPermission (US-022 / US-023): yes_no and single_choice map
// one-to-one onto permission options; multi_choice and free_text cannot.
func canExpressQuestion(q question.Question) bool {
	switch q.Type {
	case question.TypeYesNo, question.TypeSingleChoice:
		return true
	default:
		return false
	}
}

// autoAnswers produces default answers for a question batch when no
// client is attached (in-process use): yes_no answers yes, single_choice
// picks the first choice. multi_choice and free_text fall back to an
// empty answer so the tool still formats a response.
func autoAnswers(questions []question.Question) []question.Answer {
	answers := make([]question.Answer, 0, len(questions))
	for _, q := range questions {
		ans := question.Answer{QuestionID: q.ID}
		switch q.Type {
		case question.TypeYesNo:
			yes := true
			ans.Yes = &yes
		case question.TypeSingleChoice:
			if len(q.Choices) > 0 {
				ans.SelectedIDs = []string{q.Choices[0].ID}
			}
		}
		answers = append(answers, ans)
	}
	return answers
}

// questionToPermission maps one question onto a requestPermission the
// client can present as a question dialog: the question text becomes
// the tool call title, choices become options (yes_no becomes Yes/No,
// single_choice becomes one option per choice), and the full question
// structure rides along in rawInput for clients that want to render it.
func questionToPermission(req question.Request, q question.Question) acpsdk.RequestPermissionRequest {
	title := q.Text
	if len(req.Questions) > 1 && req.ConfirmTitle != "" {
		title = fmt.Sprintf("%s / %s", req.ConfirmTitle, q.Text)
	}

	options := make([]acpsdk.PermissionOption, 0, 2)
	switch q.Type {
	case question.TypeYesNo:
		options = append(options,
			acpsdk.PermissionOption{OptionId: "yes", Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Yes"},
			acpsdk.PermissionOption{OptionId: "no", Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "No"},
		)
	case question.TypeSingleChoice:
		for _, c := range q.Choices {
			options = append(options, acpsdk.PermissionOption{
				OptionId: acpsdk.PermissionOptionId(c.ID),
				Kind:     acpsdk.PermissionOptionKindAllowOnce,
				Name:     c.Label,
			})
		}
	}

	rawInput := map[string]any{
		"question_id":   q.ID,
		"question_type": string(q.Type),
		"question":      q.Text,
		"description":   q.Description,
		"label":         q.Label,
	}
	if len(req.Questions) > 1 {
		rawInput["confirm_title"] = req.ConfirmTitle
		rawInput["confirm_description"] = req.ConfirmDescription
	}

	return acpsdk.RequestPermissionRequest{
		SessionId: acpsdk.SessionId(req.SessionID),
		ToolCall: acpsdk.ToolCallUpdate{
			ToolCallId: acpsdk.ToolCallId(req.ToolCallID),
			Title:      acpsdk.Ptr(title),
			Kind:       acpsdk.Ptr(acpsdk.ToolKindOther),
			RawInput:   rawInput,
		},
		Options: options,
	}
}

// permissionOutcomeToAnswer maps the client's permission choice back to
// a question answer: yes_no reads the yes/no option, single_choice
// reports the chosen option ID. The bool reports whether the outcome
// produced a valid answer.
func permissionOutcomeToAnswer(q question.Question, out acpsdk.RequestPermissionOutcome) (question.Answer, bool) {
	if out.Selected == nil {
		return question.Answer{}, false
	}
	optionID := string(out.Selected.OptionId)
	ans := question.Answer{QuestionID: q.ID}
	switch q.Type {
	case question.TypeYesNo:
		yes := optionID == "yes"
		ans.Yes = &yes
	case question.TypeSingleChoice:
		ans.SelectedIDs = []string{optionID}
	default:
		return question.Answer{}, false
	}
	return ans, true
}

// permissionRequestTimeout bounds how long the agent waits for the
// client to answer a permission request before denying it. Tests may
// shorten it.
var permissionRequestTimeout = 5 * time.Minute

// forwardPermissionRequest asks the ACP client to approve a permission
// request (US-013). The client picks allow-once / allow-always /
// reject; anything but an explicit allow is denied, as are timeouts and
// client errors (safety first). With no client attached (in-process
// use) the request is granted, matching the pre-US-013 behavior.
func (a *Agent) forwardPermissionRequest(ctx context.Context, sess *session, p permission.PermissionRequest) {
	conn := a.connection()
	if conn == nil {
		sess.workspace.Permissions.Grant(p)
		return
	}

	req := acpsdk.RequestPermissionRequest{
		SessionId: acpsdk.SessionId(p.SessionID),
		ToolCall: acpsdk.ToolCallUpdate{
			ToolCallId: acpsdk.ToolCallId(p.ToolCallID),
			Title:      acpsdk.Ptr(permissionTitle(p)),
			Kind:       permissionToolKind(p.Action),
			RawInput:   p.Params,
		},
		Options: []acpsdk.PermissionOption{
			{OptionId: "allow-once", Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow once"},
			{OptionId: "allow-always", Kind: acpsdk.PermissionOptionKindAllowAlways, Name: "Always allow"},
			{OptionId: "reject", Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "Reject"},
		},
	}
	if p.Path != "" {
		req.ToolCall.Locations = []acpsdk.ToolCallLocation{{Path: p.Path}}
	}

	rctx, cancel := context.WithTimeout(ctx, permissionRequestTimeout)
	defer cancel()
	resp, err := conn.RequestPermission(rctx, req)
	if err != nil {
		// Timeout, client error, or disconnect: deny by default.
		sess.workspace.Permissions.Deny(p)
		return
	}
	switch {
	case resp.Outcome.Selected != nil && resp.Outcome.Selected.OptionId == "allow-once":
		sess.workspace.Permissions.Grant(p)
	case resp.Outcome.Selected != nil && resp.Outcome.Selected.OptionId == "allow-always":
		sess.workspace.Permissions.GrantPersistent(p)
	default:
		sess.workspace.Permissions.Deny(p)
	}
}

// permissionTitle builds the tool call title shown in the client's
// permission dialog.
func permissionTitle(p permission.PermissionRequest) string {
	if p.Description != "" {
		return p.Description
	}
	return fmt.Sprintf("%s (%s)", p.ToolName, p.Action)
}

// permissionToolKind maps a crush permission action to the ACP tool
// kind for the permission dialog.
func permissionToolKind(action string) *acpsdk.ToolKind {
	var k acpsdk.ToolKind
	switch action {
	case "read":
		k = acpsdk.ToolKindRead
	case "write":
		k = acpsdk.ToolKindEdit
	case "execute":
		k = acpsdk.ToolKindExecute
	default:
		k = acpsdk.ToolKindOther
	}
	return &k
}

// toolCallKind maps a crush tool name to the ACP tool kind so the
// client can pick an icon and UI treatment for the tool call card
// (US-021).
func toolCallKind(name string) acpsdk.ToolKind {
	switch name {
	case "view", "ls", "outline", "file_finder":
		return acpsdk.ToolKindRead
	case "edit", "write", "multiedit", "apply_patch":
		return acpsdk.ToolKindEdit
	case "bash", "terminal":
		return acpsdk.ToolKindExecute
	case "grep", "glob", "search", "sourcegraph", "web_search":
		return acpsdk.ToolKindSearch
	case "fetch", "web_fetch", "download", "agentic_fetch":
		return acpsdk.ToolKindFetch
	default:
		return acpsdk.ToolKindOther
	}
}

// parseJSONValue parses a tool input or output string as JSON so the
// client's tool call UI can show structured rawInput/rawOutput, falling
// back to the raw string when it is not valid JSON.
func parseJSONValue(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}

// fileClient routes file reads and writes to the ACP client when it
// advertises file system support (US-014). Tools reach it through the
// config.RuntimeOverrides.FileClient hook and fall back to local IO on
// any error.
type fileClient struct {
	a *Agent
}

// errClientUnavailable is returned when no client is attached or the
// client does not advertise the file system capability.
var errClientUnavailable = errors.New("acp client file system is unavailable")

// clientFileTimeout bounds how long a file or terminal operation waits
// for the client before the tool falls back to the local file system.
// Tests may shorten it.
var clientFileTimeout = 10 * time.Second

func (f *fileClient) ReadTextFile(ctx context.Context, sessionID, path string, line, limit *int) (string, error) {
	conn, fs := f.a.fsCapabilities()
	if conn == nil || !fs.ReadTextFile {
		return "", errClientUnavailable
	}
	rctx, cancel := context.WithTimeout(ctx, clientFileTimeout)
	defer cancel()
	resp, err := conn.ReadTextFile(rctx, acpsdk.ReadTextFileRequest{
		SessionId: acpsdk.SessionId(sessionID),
		Path:      path,
		Line:      line,
		Limit:     limit,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (f *fileClient) WriteTextFile(ctx context.Context, sessionID, path, content string) error {
	conn, fs := f.a.fsCapabilities()
	if conn == nil || !fs.WriteTextFile {
		return errClientUnavailable
	}
	rctx, cancel := context.WithTimeout(ctx, clientFileTimeout)
	defer cancel()
	_, err := conn.WriteTextFile(rctx, acpsdk.WriteTextFileRequest{
		SessionId: acpsdk.SessionId(sessionID),
		Path:      path,
		Content:   content,
	})
	return err
}

var _ config.FileClient = (*fileClient)(nil)

// clientSupportsTerminal reports whether the attached client advertises
// terminal support (US-016).
func (a *Agent) clientSupportsTerminal() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil && a.clientCapabilities.Terminal
}

// mcpServerToolWait bounds how long registerMcpServers waits for a
// server's tools to register before refreshing the agent's tool list.
// Tests may shorten it.
var mcpServerToolWait = 15 * time.Second

// registerMcpServers wires client-declared MCP servers into the
// workspace's MCP pipeline (US-015), mirroring the reference client's
// session-scoped server registration. stdio/http/sse servers are added
// in memory and started; the MCP-over-ACP transport is skipped because
// SDK v0.13.5 has no mcp/message dispatch. The agent's tool list is
// refreshed once the servers' tools are registered so the first prompt
// sees them.
func (a *Agent) registerMcpServers(ctx context.Context, ws *backend.Workspace, servers []acpsdk.McpServer) {
	if len(servers) == 0 || ws.Cfg == nil {
		return
	}
	var names []string
	for _, srv := range servers {
		cfg, name, ok := mcpServerConfig(srv)
		if !ok {
			continue
		}
		if name == "" {
			name = "acp-" + uuid.NewString()
		}
		ws.Cfg.SetMCPConfigInMemory(name, cfg)
		names = append(names, name)
	}
	if len(names) == 0 {
		return
	}
	mcptools.Reinitialize(ctx, ws.Cfg)

	// Tool registration happens asynchronously after the server connects;
	// wait for it so the tool list refresh below is complete.
	deadline := time.Now().Add(mcpServerToolWait)
	for pending := len(names); pending > 0 && time.Now().Before(deadline); {
		pending = 0
		registered := make(map[string]bool, len(names))
		for n := range mcptools.Tools() {
			registered[n] = true
		}
		for _, name := range names {
			if !registered[name] {
				pending++
			}
		}
		if pending > 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	_ = ws.UpdateAgentModel(ctx)

	a.mu.Lock()
	a.workspaceMCPs[ws.ID] = append(a.workspaceMCPs[ws.ID], names...)
	a.mu.Unlock()
}

// unregisterMcpServers removes the client-declared MCP servers recorded
// for a workspace and refreshes the agent's tools.
func (a *Agent) unregisterMcpServers(ctx context.Context, ws *backend.Workspace) {
	a.mu.Lock()
	names := a.workspaceMCPs[ws.ID]
	delete(a.workspaceMCPs, ws.ID)
	a.mu.Unlock()
	if len(names) == 0 || ws.Cfg == nil {
		return
	}
	for _, name := range names {
		ws.Cfg.RemoveMCPConfigInMemory(name)
	}
	mcptools.Reinitialize(ctx, ws.Cfg)
	_ = ws.UpdateAgentModel(ctx)
}

// mcpServerConfig translates an ACP McpServer into a crush MCP config
// and the server's display name. The MCP-over-ACP transport is
// unsupported (SDK v0.13.5 gap) and is reported as not ok, so the
// caller skips it.
func mcpServerConfig(srv acpsdk.McpServer) (config.MCPConfig, string, bool) {
	switch {
	case srv.Stdio != nil:
		env := make(map[string]string, len(srv.Stdio.Env))
		for _, e := range srv.Stdio.Env {
			env[e.Name] = e.Value
		}
		return config.MCPConfig{Type: config.MCPStdio, Command: srv.Stdio.Command, Args: srv.Stdio.Args, Env: env}, srv.Stdio.Name, true
	case srv.Http != nil:
		headers := make(map[string]string, len(srv.Http.Headers))
		for _, h := range srv.Http.Headers {
			headers[h.Name] = h.Value
		}
		return config.MCPConfig{Type: config.MCPHttp, URL: srv.Http.Url, Headers: headers}, srv.Http.Name, true
	case srv.Sse != nil:
		headers := make(map[string]string, len(srv.Sse.Headers))
		for _, h := range srv.Sse.Headers {
			headers[h.Name] = h.Value
		}
		return config.MCPConfig{Type: config.MCPSSE, URL: srv.Sse.Url, Headers: headers}, srv.Sse.Name, true
	default:
		// acp transport: unsupported.
		return config.MCPConfig{}, "", false
	}
}

// terminalClient runs commands in the ACP client's integrated terminal
// (US-016). It implements config.TerminalRunner; the terminal tool falls
// back to the local bash tool when the client is unavailable.
type terminalClient struct {
	a *Agent
}

var _ config.TerminalRunner = (*terminalClient)(nil)

// terminalPollInterval is how often the agent polls terminal output.
const terminalPollInterval = 100 * time.Millisecond

// RunTerminal drives the full client terminal lifecycle: create, poll
// output until exit (or timeout, killing the terminal), then release.
func (t *terminalClient) RunTerminal(ctx context.Context, sessionID, command string, args []string, cwd string) (string, int, error) {
	conn := t.a.connection()
	if conn == nil {
		return "", -1, errClientUnavailable
	}
	sid := acpsdk.SessionId(sessionID)
	rctx, cancel := context.WithTimeout(ctx, clientFileTimeout)
	defer cancel()

	createResp, err := conn.CreateTerminal(rctx, acpsdk.CreateTerminalRequest{
		SessionId: sid,
		Command:   command,
		Args:      args,
		Cwd:       &cwd,
	})
	if err != nil {
		return "", -1, err
	}
	terminalID := createResp.TerminalId

	output := ""
	released := false
	defer func() {
		if released {
			return
		}
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		_, _ = conn.ReleaseTerminal(releaseCtx, acpsdk.ReleaseTerminalRequest{SessionId: sid, TerminalId: terminalID})
	}()

	for {
		if rctx.Err() != nil {
			killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer killCancel()
			_, _ = conn.KillTerminal(killCtx, acpsdk.KillTerminalRequest{SessionId: sid, TerminalId: terminalID})
			return output, -1, rctx.Err()
		}
		outResp, err := conn.TerminalOutput(rctx, acpsdk.TerminalOutputRequest{SessionId: sid, TerminalId: terminalID})
		if err != nil {
			return output, -1, err
		}
		if outResp.Output != "" {
			output = outResp.Output
		}
		if outResp.ExitStatus != nil {
			// The terminal exited; get its exit code, then release.
			exitCode := 0
			if waitResp, werr := conn.WaitForTerminalExit(rctx, acpsdk.WaitForTerminalExitRequest{SessionId: sid, TerminalId: terminalID}); werr == nil && waitResp.ExitCode != nil {
				exitCode = *waitResp.ExitCode
			}
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer releaseCancel()
			_, _ = conn.ReleaseTerminal(releaseCtx, acpsdk.ReleaseTerminalRequest{SessionId: sid, TerminalId: terminalID})
			released = true
			return output, exitCode, nil
		}
		select {
		case <-rctx.Done():
		case <-time.After(terminalPollInterval):
		}
	}
}

// applyModel sets the workspace's preferred large model from a
// "provider/model" value and rebuilds the agent so the next prompt uses
// it. The provider must exist in the config; model IDs are not further
// validated because configured model lists can be partial.
func (a *Agent) applyModel(ctx context.Context, sess *session, value string) error {
	provider, model, ok := strings.Cut(value, "/")
	if !ok || provider == "" || model == "" {
		return acpsdk.NewInvalidParams(map[string]any{
			"error": fmt.Sprintf("invalid model value: %q (want provider/model)", value),
		})
	}
	if sess.workspace == nil || sess.workspace.Cfg == nil {
		return acpsdk.NewInternalError(map[string]any{"error": "workspace config is unavailable"})
	}
	if _, ok := sess.workspace.Cfg.Config().Providers.Get(provider); !ok {
		return acpsdk.NewInvalidParams(map[string]any{
			"error": fmt.Sprintf("unknown provider: %s", provider),
		})
	}
	if err := sess.workspace.Cfg.UpdatePreferredModel(config.ScopeWorkspace, config.SelectedModelTypeLarge, config.SelectedModel{Provider: provider, Model: model}); err != nil {
		return acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	if err := sess.workspace.UpdateAgentModel(ctx); err != nil {
		return acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	return nil
}

// sessionConfigOptionsLocked builds the config options advertised for a
// session from its current mode and workspace configuration. Callers
// must hold a.mu.
func (a *Agent) sessionConfigOptionsLocked(sess *session) []acpsdk.SessionConfigOption {
	mode := sess.mode
	if mode == "" {
		mode = defaultMode()
	}
	modeOpts := make(acpsdk.SessionConfigSelectOptionsUngrouped, 0, len(availableModes))
	for _, m := range availableModes {
		modeOpts = append(modeOpts, acpsdk.SessionConfigSelectOption{
			Name:  m.Name,
			Value: acpsdk.SessionConfigValueId(m.Id),
		})
	}
	category := acpsdk.SessionConfigOptionCategoryMode
	opts := []acpsdk.SessionConfigOption{
		{
			Select: &acpsdk.SessionConfigOptionSelect{
				Category:     &category,
				CurrentValue: acpsdk.SessionConfigValueId(mode),
				Id:           configOptionMode,
				Name:         "Mode",
				Options:      acpsdk.SessionConfigSelectOptions{Ungrouped: &modeOpts},
				Type:         "select",
			},
		},
	}

	if sess.workspace == nil || sess.workspace.Cfg == nil {
		return opts
	}
	cfg := sess.workspace.Cfg.Config()
	current := ""
	if m, ok := cfg.Models[config.SelectedModelTypeLarge]; ok && m.Provider != "" {
		current = m.Provider + "/" + m.Model
	}
	var modelOpts acpsdk.SessionConfigSelectOptionsUngrouped
	for providerID, p := range cfg.Providers.Seq2() {
		label := p.Name
		if label == "" {
			label = providerID
		}
		for _, m := range p.Models {
			modelOpts = append(modelOpts, acpsdk.SessionConfigSelectOption{
				Name:  label + " · " + m.ID,
				Value: acpsdk.SessionConfigValueId(providerID + "/" + m.ID),
			})
		}
	}
	modelCategory := acpsdk.SessionConfigOptionCategoryModel
	opts = append(opts, acpsdk.SessionConfigOption{
		Select: &acpsdk.SessionConfigOptionSelect{
			Category:     &modelCategory,
			CurrentValue: acpsdk.SessionConfigValueId(current),
			Id:           configOptionModel,
			Name:         "Model",
			Options:      acpsdk.SessionConfigSelectOptions{Ungrouped: &modelOpts},
			Type:         "select",
		},
	})
	return opts
}

// notifyMode pushes a current_mode_update notification to the client.
func (a *Agent) notifyMode(ctx context.Context, sid acpsdk.SessionId, mode acpsdk.SessionModeId) {
	if a.conn == nil {
		return
	}
	_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: sid,
		Update: acpsdk.SessionUpdate{
			CurrentModeUpdate: &acpsdk.SessionCurrentModeUpdate{CurrentModeId: mode},
		},
	})
}

// notifyConfigOptions pushes a config_option_update notification to the
// client.
func (a *Agent) notifyConfigOptions(ctx context.Context, sid acpsdk.SessionId, opts []acpsdk.SessionConfigOption) {
	if a.conn == nil {
		return
	}
	_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: sid,
		Update: acpsdk.SessionUpdate{
			ConfigOptionUpdate: &acpsdk.SessionConfigOptionUpdate{ConfigOptions: opts},
		},
	})
}

// notifyUsage pushes a usage_update notification carrying the session's
// context-window usage and cumulative cost to the client (US-024). The
// update is best-effort: an absent connection or a client that has
// gone away must never affect the caller.
func (a *Agent) notifyUsage(ctx context.Context, sid acpsdk.SessionId, used, size int, cost *acpsdk.Cost) {
	if a.conn == nil {
		return
	}
	_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: sid,
		Update: acpsdk.SessionUpdate{
			UsageUpdate: &acpsdk.SessionUsageUpdate{
				Size: size,
				Used: used,
				Cost: cost,
			},
		},
	})
}

// pushSessionUsage reads the session's current token usage and the
// workspace's large-model context window and notifies the client
// (US-024). Best-effort: a missing workspace config, an unknown
// session, or a transport error are all ignored so the caller's result
// is unaffected.
func (a *Agent) pushSessionUsage(ctx context.Context, sid acpsdk.SessionId, sess *session) {
	if sess == nil || sess.workspace == nil || sess.workspace.Cfg == nil {
		return
	}
	largeModel := sess.workspace.Cfg.Config().LargeModel()
	if largeModel == nil {
		return
	}
	crushSess, err := sess.workspace.App.Sessions.Get(ctx, sess.sessionID)
	if err != nil {
		return
	}
	a.notifyUsage(ctx, sid,
		int(crushSess.CompletionTokens+crushSess.PromptTokens),
		int(largeModel.ContextWindow),
		&acpsdk.Cost{Amount: crushSess.Cost, Currency: "USD"})
}
