-- Whitelist Management Queries

-- name: ListWhitelistedEmails :many
SELECT id, email, name, type::text, "addedBy", reason, active, "createdAt", "updatedAt"
FROM whitelisted_emails
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: ListActiveWhitelistedEmails :many
SELECT id, email, name, type::text, "addedBy", reason, active, "createdAt", "updatedAt"
FROM whitelisted_emails
WHERE active = true
ORDER BY "createdAt" DESC;

-- name: GetWhitelistedEmailByID :one
SELECT id, email, name, type::text, "addedBy", reason, active, "createdAt", "updatedAt"
FROM whitelisted_emails WHERE id = $1;

-- name: GetWhitelistedEmailByEmail :one
SELECT id, email, name, type::text, "addedBy", reason, active, "createdAt", "updatedAt"
FROM whitelisted_emails WHERE email = $1;

-- name: CreateWhitelistedEmail :one
INSERT INTO whitelisted_emails (
    id, email, name, type, "addedBy", reason, active, "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4::"WhitelistType", $5, $6, true, NOW(), NOW()
) RETURNING id, email, name, type::text, "addedBy", reason, active, "createdAt", "updatedAt";

-- name: UpdateWhitelistedEmail :one
UPDATE whitelisted_emails SET
    name = COALESCE($2, name),
    reason = COALESCE($3, reason),
    "updatedAt" = NOW()
WHERE id = $1
RETURNING id, email, name, type::text, "addedBy", reason, active, "createdAt", "updatedAt";

-- name: ToggleWhitelistedEmail :one
UPDATE whitelisted_emails SET
    active = $2,
    "updatedAt" = NOW()
WHERE id = $1
RETURNING id, email, name, type::text, "addedBy", reason, active, "createdAt", "updatedAt";

-- name: DeleteWhitelistedEmail :exec
DELETE FROM whitelisted_emails WHERE id = $1;

-- name: CountWhitelistedEmails :one
SELECT COUNT(*) FROM whitelisted_emails;

-- name: CountActiveWhitelistedEmails :one
SELECT COUNT(*) FROM whitelisted_emails WHERE active = true;

-- name: SearchWhitelistedEmails :many
SELECT id, email, name, type::text, "addedBy", reason, active, "createdAt", "updatedAt"
FROM whitelisted_emails
WHERE LOWER(email) LIKE $1 OR LOWER(COALESCE(name, '')) LIKE $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: CountSearchWhitelistedEmails :one
SELECT COUNT(*) FROM whitelisted_emails
WHERE LOWER(email) LIKE $1 OR LOWER(COALESCE(name, '')) LIKE $1;
