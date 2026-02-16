-- Evaluation Chat Sessions and Messages Queries

-- name: CountEvaluationChatSessionsByUser :one
SELECT COUNT(*) FROM evaluation_chat_sessions
WHERE user_id = $1;

-- name: ListEvaluationChatSessionsWithStats :many
SELECT
    s.id, s.user_id, s.property_ids, s.cached_property_ids,
    s.investor_profile, s.portfolio_snapshot,
    s.created_at, s.updated_at,
    (SELECT COUNT(*) FROM evaluation_chat_messages m WHERE m.session_id = s.id) AS message_count,
    (SELECT m.created_at FROM evaluation_chat_messages m WHERE m.session_id = s.id ORDER BY m.created_at DESC LIMIT 1) AS last_message_at
FROM evaluation_chat_sessions s
WHERE s.user_id = $1
ORDER BY s.updated_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListEvaluationChatSessionsExtended :many
SELECT
    s.id, s.user_id, s.property_ids, s.cached_property_ids, s.investor_profile, s.portfolio_snapshot,
    s.created_at, s.updated_at,
    (SELECT COUNT(*) FROM evaluation_chat_messages WHERE session_id = s.id) as message_count,
    (SELECT content FROM evaluation_chat_messages WHERE session_id = s.id ORDER BY created_at DESC LIMIT 1) as last_message_content,
    (SELECT role FROM evaluation_chat_messages WHERE session_id = s.id ORDER BY created_at DESC LIMIT 1) as last_message_role,
    (SELECT created_at FROM evaluation_chat_messages WHERE session_id = s.id ORDER BY created_at DESC LIMIT 1) as last_message_at,
    (SELECT content FROM evaluation_chat_messages WHERE session_id = s.id AND role = 'user' ORDER BY created_at ASC LIMIT 1) as first_user_question
FROM evaluation_chat_sessions s
WHERE s.user_id = $1
ORDER BY s.updated_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetEvaluationChatSession :one
SELECT id, user_id, property_ids, cached_property_ids,
       investor_profile, portfolio_snapshot, created_at, updated_at
FROM evaluation_chat_sessions
WHERE id = $1 AND user_id = $2;

-- name: ListEvaluationChatMessages :many
SELECT id, session_id, role, content, parsed_blocks, token_usage, created_at
FROM evaluation_chat_messages
WHERE session_id = $1
ORDER BY created_at ASC;

-- name: CreateEvaluationChatSession :one
INSERT INTO evaluation_chat_sessions (
    id, user_id, property_ids, cached_property_ids,
    investor_profile, portfolio_snapshot, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
RETURNING *;

-- name: CreateEvaluationChatMessage :one
INSERT INTO evaluation_chat_messages (
    id, session_id, role, content, parsed_blocks, token_usage, created_at
) VALUES ($1, $2, $3, $4, $5, $6, NOW())
RETURNING *;

-- name: UpdateEvaluationChatSessionTimestamp :exec
UPDATE evaluation_chat_sessions
SET updated_at = NOW()
WHERE id = $1;

-- name: DeleteEvaluationChatSession :execrows
DELETE FROM evaluation_chat_sessions WHERE id = $1 AND user_id = $2;

-- name: ValidateEvaluationChatSessionOwnership :one
SELECT EXISTS(SELECT 1 FROM evaluation_chat_sessions WHERE id = $1 AND user_id = $2) AS exists;

-- name: GetSessionHistory :many
SELECT id, session_id, role, content, parsed_blocks, token_usage, created_at
FROM evaluation_chat_messages
WHERE session_id = $1
ORDER BY created_at ASC;
