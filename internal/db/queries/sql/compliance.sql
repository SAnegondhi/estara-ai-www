-- Compliance Queries

-- name: ListUserConsents :many
SELECT * FROM user_consents
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListUserConsentsByType :many
SELECT * FROM user_consents
WHERE consent_type = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetUserConsent :one
SELECT * FROM user_consents
WHERE user_id = $1 AND consent_type = $2 AND revoked_at IS NULL;

-- name: CreateUserConsent :one
INSERT INTO user_consents (
    user_id, consent_type, version, ip_address, user_agent
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: RevokeUserConsent :exec
UPDATE user_consents SET revoked_at = NOW()
WHERE user_id = $1 AND consent_type = $2 AND revoked_at IS NULL;

-- name: GetConsentSummary :many
SELECT
    consent_type,
    COUNT(*) FILTER (WHERE revoked_at IS NULL) as active_count,
    COUNT(*) FILTER (WHERE revoked_at IS NOT NULL) as revoked_count,
    COUNT(*) as total_count
FROM user_consents
GROUP BY consent_type
ORDER BY consent_type;

-- name: CountUserConsents :one
SELECT COUNT(*) FROM user_consents;

-- name: CountUserConsentsByType :one
SELECT COUNT(*) FROM user_consents WHERE consent_type = $1;
