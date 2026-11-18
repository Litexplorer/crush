-- name: GetMessage :one
SELECT *
FROM messages
WHERE id = ? LIMIT 1;

-- name: ListMessagesBySession :many
SELECT *
FROM messages
WHERE session_id = ?
ORDER BY created_at ASC;

-- name: ListMessagesBySessionTail :many
-- Newest page of a session, newest first. seq is insertion order and never
-- ties, which created_at (one-second resolution) cannot promise.
SELECT *
FROM messages
WHERE session_id = sqlc.arg(session_id)
ORDER BY seq DESC
LIMIT sqlc.arg(page_size);

-- name: GetMessageSeq :one
-- Cursor lookup for paging: insertion order of one message.
SELECT seq
FROM messages
WHERE id = sqlc.arg(id)
LIMIT 1;

-- name: ListMessagesBySessionPage :many
-- Page immediately preceding the cursor, same ordering contract as
-- ListMessagesBySessionTail. The cursor row itself is never returned.
SELECT *
FROM messages
WHERE session_id = sqlc.arg(session_id)
  AND seq < sqlc.arg(cursor_seq)
ORDER BY seq DESC
LIMIT sqlc.arg(page_size);

-- name: ListMessagesBySessionFromSummary :many
-- Messages from the summary onward, which is all a compacted session sends.
-- created_at has one-second resolution, so a few messages preceding the
-- summary can come back too; the caller slices from the summary by ID.
SELECT m.*
FROM messages m
WHERE m.session_id = ?
  AND m.created_at >= (SELECT s.created_at FROM messages s WHERE s.id = ?)
ORDER BY m.created_at ASC;

-- name: CreateMessage :one
-- seq is assigned here rather than by a trigger so the RETURNING row already
-- carries it: it is the paging cursor clients get back.
INSERT INTO messages (
    id,
    session_id,
    role,
    parts,
    model,
    model_name,
    provider,
    is_summary_message,
    created_at,
    updated_at,
    seq
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, strftime('%s', 'now'), strftime('%s', 'now'),
    (SELECT IFNULL(MAX(seq), 0) + 1 FROM messages)
)
RETURNING *;

-- name: UpdateMessage :exec
UPDATE messages
SET
    parts = ?,
    model_name = ?,
    prism_model_id = ?,
    prism_model_name = ?,
    prism_hypercredit_savings = ?,
    prism_dollar_savings = ?,
    finished_at = ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;


-- name: DeleteMessage :exec
DELETE FROM messages
WHERE id = ?;

-- name: DeleteSessionMessages :exec
DELETE FROM messages
WHERE session_id = ?;

-- name: ListUserMessagesBySession :many
-- Backs prompt history, which steps back one entry at a time.
SELECT *
FROM messages
WHERE session_id = ? AND role = 'user'
ORDER BY created_at DESC
LIMIT 200;

-- name: ListFirstUserMessagesBySession :many
-- First user message of every session, backing the session-list subtitle.
-- created_at has one-second resolution, so a session can match several rows
-- within the same second; callers dedupe per session and keep the lowest id.
SELECT m.session_id, m.id, m.parts
FROM messages m
JOIN (
    SELECT session_id, MIN(created_at) AS first_at
    FROM messages
    WHERE role = 'user'
    GROUP BY session_id
) earliest ON earliest.session_id = m.session_id AND earliest.first_at = m.created_at
WHERE m.role = 'user';

-- name: ListAllUserMessages :many
-- Backs prompt history when no session is open. Needs
-- idx_messages_role_created_at to seek rather than scan the table.
-- Child sessions are excluded: a subagent's task prompt is a user
-- message too, but the user never typed it, so it has no place in the
-- prompt history.
SELECT m.*
FROM messages m
JOIN sessions s ON s.id = m.session_id
WHERE m.role = 'user' AND s.parent_session_id IS NULL
ORDER BY m.created_at DESC
LIMIT 200;

-- name: SearchMessages :many
SELECT m.id, m.session_id, m.role, m.parts, m.created_at, s.title AS session_title
FROM messages m
JOIN sessions s ON s.id = m.session_id
WHERE m.rowid IN (SELECT fts.rowid FROM messages_fts fts WHERE fts.parts MATCH ?)
ORDER BY m.created_at DESC
LIMIT ?;

-- name: GetLastAssistantMessageBySession :one
SELECT *
FROM messages
WHERE session_id = ? AND role = 'assistant' AND is_summary_message = 0
ORDER BY created_at DESC
LIMIT 1;
