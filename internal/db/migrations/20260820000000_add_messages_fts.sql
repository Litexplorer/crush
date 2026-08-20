-- +goose Up
-- +goose StatementBegin
-- FTS5 index over messages.parts for keyword search. Uses the external
-- content table mode (content='messages', content_rowid='rowid') so the
-- index does not duplicate message storage; triggers keep it in sync.
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    parts,
    content='messages',
    content_rowid='rowid',
    tokenize='unicode61'
);

-- Keep the FTS index in sync with the messages table.
CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, parts) VALUES (new.rowid, new.parts);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, parts) VALUES ('delete', old.rowid, old.parts);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_au AFTER UPDATE OF parts ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, parts) VALUES ('delete', old.rowid, old.parts);
    INSERT INTO messages_fts(rowid, parts) VALUES (new.rowid, new.parts);
END;

-- Backfill the index with pre-existing messages.
INSERT INTO messages_fts(messages_fts) VALUES ('rebuild');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS messages_fts_au;
DROP TRIGGER IF EXISTS messages_fts_ad;
DROP TRIGGER IF EXISTS messages_fts_ai;
DROP TABLE IF EXISTS messages_fts;
-- +goose StatementEnd
