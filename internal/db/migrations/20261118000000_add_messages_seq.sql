-- +goose Up
-- +goose StatementBegin
-- Insertion order per message. created_at only has one-second resolution, so
-- paging on it alone can skip or repeat messages that share a second. seq is
-- monotonic and lets a cursor page without ties.
ALTER TABLE messages ADD COLUMN seq INTEGER;
UPDATE messages SET seq = rowid WHERE seq IS NULL;
CREATE INDEX IF NOT EXISTS idx_messages_session_seq ON messages (session_id, seq);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_messages_session_seq;
ALTER TABLE messages DROP COLUMN seq;
-- +goose StatementEnd
