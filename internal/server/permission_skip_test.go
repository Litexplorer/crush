package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// buildSkipController wires a controller around one workspace whose
// permission service is real, so the legacy skip toggle is exercised
// against the mode it maps onto rather than against a stub.
func buildSkipController(t *testing.T) (*controllerV1, string) {
	t.Helper()

	b := backend.New(context.Background(), nil, nil)
	wsID := uuid.New().String()
	a := &app.App{
		Permissions: permission.NewPermissionService(t.TempDir(), nil, nil),
	}
	backend.InsertWorkspaceForTest(b, &backend.Workspace{ID: wsID, Path: t.TempDir(), App: a})

	return &controllerV1{backend: b, server: &Server{backend: b}}, wsID
}

func skipRequest(t *testing.T, method, wsID, body string) *http.Request {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method,
		"/v1/workspaces/"+wsID+"/permissions/skip", reader)
	req.SetPathValue("id", wsID)
	return req
}

// readSkip calls the GET half and returns the decoded flag.
func readSkip(t *testing.T, c *controllerV1, wsID string) bool {
	t.Helper()

	rec := httptest.NewRecorder()
	c.handleGetWorkspacePermissionsSkip(rec, skipRequest(t, http.MethodGet, wsID, ""))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out proto.PermissionSkipRequest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Skip
}

// TestGetWorkspacePermissionsSkipReportsMode covers the read half: the flag
// mirrors the mode, so a client using the legacy endpoint sees what the
// server actually enforces.
func TestGetWorkspacePermissionsSkipReportsMode(t *testing.T) {
	t.Parallel()

	c, wsID := buildSkipController(t)
	require.False(t, readSkip(t, c, wsID), "a fresh workspace prompts for permissions")

	require.NoError(t, c.backend.SetPermissionMode(wsID, permission.PermissionModeYolo))
	require.True(t, readSkip(t, c, wsID), "yolo answers prompts itself, so skip is on")
}

// TestPostWorkspacePermissionsSkipTogglesMode covers the write half in both
// directions, including the mode the service ends up enforcing.
func TestPostWorkspacePermissionsSkipTogglesMode(t *testing.T) {
	t.Parallel()

	c, wsID := buildSkipController(t)

	rec := httptest.NewRecorder()
	c.handlePostWorkspacePermissionsSkip(rec, skipRequest(t, http.MethodPost, wsID, `{"skip":true}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	mode, err := c.backend.PermissionMode(wsID)
	require.NoError(t, err)
	require.Equal(t, permission.PermissionModeYolo, mode, "skip on must select yolo")
	require.True(t, readSkip(t, c, wsID))

	rec = httptest.NewRecorder()
	c.handlePostWorkspacePermissionsSkip(rec, skipRequest(t, http.MethodPost, wsID, `{"skip":false}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	mode, err = c.backend.PermissionMode(wsID)
	require.NoError(t, err)
	require.Equal(t, permission.PermissionModeNormal, mode, "skip off must return to normal")
	require.False(t, readSkip(t, c, wsID))
}

// TestPostWorkspacePermissionsSkipKeepsSysadmin pins the one asymmetric case:
// acknowledging the toggle must not demote a workspace that was put in
// sysadmin, because yolo would re-arm the exec-time block list.
func TestPostWorkspacePermissionsSkipKeepsSysadmin(t *testing.T) {
	t.Parallel()

	c, wsID := buildSkipController(t)
	require.NoError(t, c.backend.SetPermissionMode(wsID, permission.PermissionModeSysadmin))

	rec := httptest.NewRecorder()
	c.handlePostWorkspacePermissionsSkip(rec, skipRequest(t, http.MethodPost, wsID, `{"skip":true}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	mode, err := c.backend.PermissionMode(wsID)
	require.NoError(t, err)
	require.Equal(t, permission.PermissionModeSysadmin, mode, "sysadmin must survive the toggle")
}

// TestPostWorkspacePermissionsSkipRejectsBadBody keeps a malformed body a
// client error instead of a 500.
func TestPostWorkspacePermissionsSkipRejectsBadBody(t *testing.T) {
	t.Parallel()

	c, wsID := buildSkipController(t)

	rec := httptest.NewRecorder()
	c.handlePostWorkspacePermissionsSkip(rec, skipRequest(t, http.MethodPost, wsID, "{not json"))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
