-- Compliance Queries
-- Column names match actual database (camelCase from Prisma)

-- name: ListUserConsents :many
SELECT * FROM user_consents
ORDER BY "timestamp" DESC
LIMIT $1 OFFSET $2;

-- name: ListUserConsentsByType :many
SELECT * FROM user_consents
WHERE "consentType"::text = $1
ORDER BY "timestamp" DESC
LIMIT $2 OFFSET $3;

-- name: GetUserConsent :one
SELECT * FROM user_consents
WHERE "userId" = $1 AND "consentType"::text = $2 AND granted = true;

-- name: CreateUserConsent :one
INSERT INTO user_consents (
    id, "userId", "consentType", version, granted, "ipAddress", "userAgent"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: RevokeUserConsent :exec
UPDATE user_consents SET granted = false
WHERE "userId" = $1 AND "consentType"::text = $2 AND granted = true;

-- name: GetConsentSummary :many
SELECT
    "consentType"::text as consent_type,
    COUNT(*) FILTER (WHERE granted = true) as active_count,
    COUNT(*) FILTER (WHERE granted = false) as revoked_count,
    COUNT(*) as total_count
FROM user_consents
GROUP BY "consentType"
ORDER BY "consentType";

-- name: CountUserConsents :one
SELECT COUNT(*) FROM user_consents;

-- name: CountUserConsentsByType :one
SELECT COUNT(*) FROM user_consents WHERE "consentType"::text = $1;
