-- +goose Up
-- +goose StatementBegin
-- Prompt cache accounting, accumulated over every request a session made.
--
-- prompt_tokens keeps its old meaning (the context size of the latest
-- request), so the cache numbers live in their own columns: a hit rate
-- needs running sums, not a snapshot.
--
-- uncached_input_tokens is the miss side: everything the provider had to
-- read fresh. Naming it explicitly keeps the rate computable without
-- depending on how each provider splits prompt_tokens.
ALTER TABLE sessions ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0);
ALTER TABLE sessions ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0);
ALTER TABLE sessions ADD COLUMN uncached_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (uncached_input_tokens >= 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN uncached_input_tokens;
ALTER TABLE sessions DROP COLUMN cache_write_tokens;
ALTER TABLE sessions DROP COLUMN cache_read_tokens;
-- +goose StatementEnd
