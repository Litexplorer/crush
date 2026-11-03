package backend

import (
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
)

// GrantPermission grants, denies, or persistently grants a permission
// request. The returned bool reports whether this call resolved the
// pending request (true) or found it already resolved by a previous
// caller (false). A false return is not an error.
func (b *Backend) GrantPermission(workspaceID string, req proto.PermissionGrant) (bool, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return false, err
	}

	perm := permission.PermissionRequest{
		ID:          req.Permission.ID,
		SessionID:   req.Permission.SessionID,
		ToolCallID:  req.Permission.ToolCallID,
		ToolName:    req.Permission.ToolName,
		Description: req.Permission.Description,
		Action:      req.Permission.Action,
		Params:      req.Permission.Params,
		Path:        req.Permission.Path,
		Danger:      req.Permission.Danger,
	}

	switch req.Action {
	case proto.PermissionAllow:
		return ws.Permissions.Grant(perm), nil
	case proto.PermissionAllowForSession:
		return ws.Permissions.GrantPersistent(perm), nil
	case proto.PermissionDeny:
		return ws.Permissions.Deny(perm), nil
	default:
		return false, ErrInvalidPermissionAction
	}
}

// PermissionMode reports the permission mode a workspace currently enforces.
func (b *Backend) PermissionMode(workspaceID string) (permission.PermissionMode, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return permission.PermissionModeNormal, err
	}
	return ws.Permissions.PermissionMode(), nil
}

// SetSkipPermissions maps the legacy "skip permission prompts" toggle onto
// the permission modes that replaced it: on selects yolo, off returns to
// normal. A workspace already in sysadmin keeps sysadmin when the toggle is
// turned on: sysadmin is the stronger mode, and demoting it to yolo would
// re-arm the exec-time block list the caller never asked to get back.
func (b *Backend) SetSkipPermissions(workspaceID string, skip bool) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	if skip {
		if ws.Permissions.PermissionMode() != permission.PermissionModeSysadmin {
			ws.Permissions.SetPermissionMode(permission.PermissionModeYolo)
		}
		return nil
	}
	ws.Permissions.SetPermissionMode(permission.PermissionModeNormal)
	return nil
}

// SetPermissionMode sets the permission mode for a workspace.
//
// Only the permission service is updated; the config override is left
// holding the startup flag on purpose. The override is a seed that
// app.New reads once at construction, and config.Overrides() hands
// back a bare pointer with no locking, so writing it here would race
// the readers on the CreateWorkspace path for no benefit. Leaving the
// service as the single writable source of truth is what keeps the
// enforced mode and the mode reported to clients from drifting apart.
func (b *Backend) SetPermissionMode(workspaceID string, mode permission.PermissionMode) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	ws.Permissions.SetPermissionMode(mode)
	return nil
}
