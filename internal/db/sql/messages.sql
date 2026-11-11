-- name: GetMessage :one
SELECT *
FROM messages
WHERE id = ? LIMIT 1;

-- name: ListMessagesBySession :many
SELECT *
FROM messages
WHERE session_id = ?
ORDER BY created_at ASC;

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
INSERT INTO messages (
    id,
    session_id,
    role,
    parts,
    model,
    provider,
    is_summary_message,
    created_at,
    updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, strftime('%s', 'now'), strftime('%s', 'now')
)
RETURNING *;

-- name: UpdateMessage :exec
UPDATE messages
SET
    parts = ?,
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
