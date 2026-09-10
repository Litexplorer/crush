// Registry for ACP sessions. ACP session IDs double as crush session
// IDs (see agent.go), so the only extra bookkeeping needed is which cwd
// each session belongs to: the sessions table already holds title and
// activity timestamps. The table lives in the data directory's SQLite
// database, so sessions survive process restarts and can be listed,
// loaded, and resumed by a later `crush acp` connection.
package acp

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/charmbracelet/crush/internal/db"
)

// acpSessionRecord is one registry row joined with the backing crush
// session's title and activity timestamp.
type acpSessionRecord struct {
	sessionID string
	cwd       string
	title     string
	updatedAt int64 // Unix timestamp; seconds or milliseconds (unit sniffed on use)
}

// openRegistry opens (or shares, via the per-data-dir connection pool)
// the database for the given data directory. Callers must pair this
// with db.Release.
func openRegistry(ctx context.Context, dataDir string) (*sql.DB, error) {
	if dataDir == "" {
		return nil, errors.New("data dir not resolved")
	}
	return db.Connect(ctx, dataDir, db.WithDataDirLock(true))
}

// upsertRegistry records (or refreshes) an ACP session's cwd mapping.
// The cwd is fixed at creation time and never updated.
func upsertRegistry(ctx context.Context, dataDir, sessionID, cwd string) error {
	conn, err := openRegistry(ctx, dataDir)
	if err != nil {
		return err
	}
	defer db.Release(dataDir)
	_, err = conn.ExecContext(ctx, `
		INSERT INTO acp_sessions (session_id, cwd, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET updated_at = excluded.updated_at`,
		sessionID, cwd, time.Now().UnixMilli())
	return err
}

// listRegistry returns every registered session, newest activity first,
// optionally filtered to one cwd.
func listRegistry(ctx context.Context, dataDir string, cwd *string) ([]acpSessionRecord, error) {
	conn, err := openRegistry(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Release(dataDir)

	query := `
		SELECT a.session_id, a.cwd, a.updated_at, COALESCE(s.title, '')
		FROM acp_sessions a
		LEFT JOIN sessions s ON s.id = a.session_id`
	args := []any{}
	if cwd != nil {
		query += ` WHERE a.cwd = ?`
		args = append(args, *cwd)
	}
	query += ` ORDER BY COALESCE(s.updated_at, a.updated_at) DESC`

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []acpSessionRecord{}
	for rows.Next() {
		var r acpSessionRecord
		if err := rows.Scan(&r.sessionID, &r.cwd, &r.updatedAt, &r.title); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// getRegistry returns the record for one session, or (nil, nil) when
// the session is not registered.
func getRegistry(ctx context.Context, dataDir, sessionID string) (*acpSessionRecord, error) {
	conn, err := openRegistry(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Release(dataDir)

	var r acpSessionRecord
	err = conn.QueryRowContext(ctx, `
		SELECT a.session_id, a.cwd, a.updated_at, COALESCE(s.title, '')
		FROM acp_sessions a
		LEFT JOIN sessions s ON s.id = a.session_id
		WHERE a.session_id = ?`, sessionID).Scan(&r.sessionID, &r.cwd, &r.updatedAt, &r.title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
