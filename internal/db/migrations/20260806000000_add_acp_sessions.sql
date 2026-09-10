-- +goose Up
-- +goose StatementBegin
-- ACP session registry: maps ACP session IDs (which double as crush
-- session IDs) to the workspace cwd they were created for, so ACP
-- clients can list, load, and resume sessions across process restarts.
CREATE TABLE IF NOT EXISTS acp_sessions (
    session_id TEXT PRIMARY KEY,
    cwd TEXT NOT NULL,
    updated_at INTEGER NOT NULL  -- Unix timestamp in milliseconds
);

CREATE INDEX IF NOT EXISTS idx_acp_sessions_cwd ON acp_sessions (cwd);
CREATE INDEX IF NOT EXISTS idx_acp_sessions_updated_at ON acp_sessions (updated_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_acp_sessions_updated_at;
DROP INDEX IF EXISTS idx_acp_sessions_cwd;
DROP TABLE IF EXISTS acp_sessions;
-- +goose StatementEnd
