-- Password Setup Token Queries (ADR-085)

-- name: CreatePasswordSetupToken :one
INSERT INTO password_setup_tokens (
    id, user_id, token, expires_at, created_at
) VALUES (
    $1, $2, $3, $4, NOW()
) RETURNING *;

-- name: GetPasswordSetupToken :one
SELECT * FROM password_setup_tokens WHERE token = $1;

-- name: MarkPasswordSetupTokenUsed :exec
UPDATE password_setup_tokens SET used_at = NOW() WHERE id = $1;
