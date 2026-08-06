// Package acp implements the ACP (Agent Client Protocol) agent side for
// crush. It consumes the transport-agnostic internal/backend layer just
// like the HTTP server layer does; capabilities are added incrementally
// per the ACP support plan (P0 handshake, P1 sessions and prompts, ...).
package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/proto"
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
}

// session holds per-session state: the backend workspace backing it,
// the crush session ID created in that workspace, plus mode and config
// options staged for P3 (setSessionMode / setSessionConfigOption).
type session struct {
	workspace     *backend.Workspace
	sessionID     string
	mode          acpsdk.SessionModeId
	configOptions []acpsdk.SessionConfigOption
}

var _ acpsdk.Agent = (*Agent)(nil)
var _ acpsdk.AgentLoader = (*Agent)(nil)

// NewAgent returns an Agent bound to the given backend, with no active
// sessions. dataDir is the data directory whose database holds session
// history; it may be empty when only live-session operations are used.
func NewAgent(b *backend.Backend, dataDir string) *Agent {
	return &Agent{
		backend:  b,
		dataDir:  dataDir,
		clientID: uuid.New().String(),
		sessions: make(map[acpsdk.SessionId]*session),
	}
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
	a.conn = conn
}

func (a *Agent) setClientCapabilities(c acpsdk.ClientCapabilities) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clientCapabilities = c
}

func (a *Agent) Authenticate(ctx context.Context, params acpsdk.AuthenticateRequest) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, acpsdk.NewMethodNotFound(acpsdk.AgentMethodAuthenticate)
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
		},
		AgentInfo: &acpsdk.Implementation{
			Name:    "Crush",
			Version: version.Version,
		},
	}, nil
}

func (a *Agent) Logout(ctx context.Context, params acpsdk.LogoutRequest) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, acpsdk.NewMethodNotFound(acpsdk.AgentMethodLogout)
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
	ws := sess.workspace
	sessionID := sess.sessionID
	a.mu.Unlock()

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

	sessionID := acpsdk.SessionId(crushSess.ID)
	a.mu.Lock()
	a.sessions[sessionID] = &session{workspace: ws, sessionID: crushSess.ID}
	a.mu.Unlock()

	return acpsdk.NewSessionResponse{SessionId: sessionID}, nil
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

	// Permission requests are auto-approved for this session for now;
	// client-side permission requests arrive in US-013.
	ws.Permissions.AutoApproveSession(sess.sessionID)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Subscribe before the run starts so no chunk is missed.
	events := ws.App.Messages.Subscribe(runCtx)
	readBytes := make(map[string]int)

	done := make(chan error, 1)
	go func() {
		_, err := ws.AgentCoordinator.Run(runCtx, sess.sessionID, text)
		done <- err
	}()

	stream := func(msg message.Message) {
		if msg.SessionID != sess.sessionID || msg.Role != message.Assistant || len(msg.Parts) == 0 {
			return
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
	if err := a.activateSession(ctx, params.Cwd, params.SessionId); err != nil {
		return acpsdk.LoadSessionResponse{}, err
	}
	return acpsdk.LoadSessionResponse{}, nil
}

// activateSession makes a persisted session live again: it validates
// the registry mapping and cwd, recreates the backing workspace,
// verifies the crush session still exists, and registers the session
// for subsequent prompts. Loading an already-live session is a no-op.
func (a *Agent) activateSession(ctx context.Context, cwd string, sessionID acpsdk.SessionId) error {
	a.mu.Lock()
	_, ok := a.sessions[sessionID]
	a.mu.Unlock()
	if ok {
		return nil
	}

	dataDir := a.registryDataDir(ctx, cwd)
	if dataDir == "" {
		return resourceNotFound(fmt.Sprintf("session not found: %s", sessionID))
	}
	rec, err := getRegistry(ctx, dataDir, string(sessionID))
	if err != nil {
		return acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	if rec == nil || filepath.Clean(rec.cwd) != filepath.Clean(cwd) {
		return resourceNotFound(fmt.Sprintf("session not found: %s", sessionID))
	}

	ws, _, err := a.backend.CreateWorkspace(proto.Workspace{
		Path:     cwd,
		ClientID: a.clientID,
		Version:  version.Version,
		Env:      os.Environ(),
	})
	if err != nil {
		return acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	if err := a.backend.AttachClient(ws.ID, a.clientID); err != nil {
		_ = a.backend.DeleteWorkspace(ws.ID, a.clientID)
		return acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	if _, err := ws.App.Sessions.Get(ctx, string(sessionID)); err != nil {
		// The registry row survived but the crush session is gone;
		// release the claim we just took.
		a.backend.DetachClient(ws.ID, a.clientID)
		return resourceNotFound(fmt.Sprintf("session not found: %s", sessionID))
	}

	a.mu.Lock()
	a.sessions[sessionID] = &session{workspace: ws, sessionID: string(sessionID)}
	a.mu.Unlock()
	return nil
}

// ResumeSession restores a persisted session like LoadSession. The ACP
// protocol has no prompt payload on resume; the client follows up with
// session/prompt, which streams through the standard prompt path with
// the full message history restored.
func (a *Agent) ResumeSession(ctx context.Context, params acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	if err := validateCwd(params.Cwd); err != nil {
		return acpsdk.ResumeSessionResponse{}, err
	}
	if err := a.activateSession(ctx, params.Cwd, params.SessionId); err != nil {
		return acpsdk.ResumeSessionResponse{}, err
	}
	return acpsdk.ResumeSessionResponse{}, nil
}

func (a *Agent) SetSessionConfigOption(ctx context.Context, params acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewMethodNotFound(acpsdk.AgentMethodSessionSetConfigOption)
}

func (a *Agent) SetSessionMode(ctx context.Context, params acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, acpsdk.NewMethodNotFound(acpsdk.AgentMethodSessionSetMode)
}
