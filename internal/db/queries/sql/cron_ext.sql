-- Cron Job Extended Queries (Category B+C live operations)

-- name: ExpireIAPSubscriptions :exec
UPDATE subscriptions SET
    status = 'EXPIRED',
    "canceledAt" = NOW(),
    "updatedAt" = NOW()
WHERE status IN ('ACTIVE', 'TRIALING')
  AND "userId" IN (
    SELECT id FROM users
    WHERE "iapPlatform" IS NOT NULL
      AND "iapExpiresAt" IS NOT NULL
      AND "iapExpiresAt" < NOW()
  );

-- name: ExpireIAPSubscriptionsRows :execrows
UPDATE subscriptions SET
    status = 'EXPIRED',
    "canceledAt" = NOW(),
    "updatedAt" = NOW()
WHERE status IN ('ACTIVE', 'TRIALING')
  AND "userId" IN (
    SELECT id FROM users
    WHERE "iapPlatform" IS NOT NULL
      AND "iapExpiresAt" IS NOT NULL
      AND "iapExpiresAt" < NOW()
  );

-- name: GetUserWithSubscriptionEntitlements :one
-- Used by entitlements service to get user + subscription tier/status/trialEnd
SELECT u.id, u.email, s.tier::text as sub_tier, s.status::text as sub_status, s."trialEnd"
FROM users u
LEFT JOIN subscriptions s ON u.id = s."userId"
WHERE u.id = $1;

-- name: GetUserWithSubscription :one
SELECT u.id, u.email, u."firstName", u."lastName", u.role::text,
       u."subscriptionTier", u."stripeCustomerId",
       s.tier::text as sub_tier, s.status::text as sub_status,
       s."currentPeriodStart", s."currentPeriodEnd"
FROM users u
LEFT JOIN subscriptions s ON u.id = s."userId"
WHERE u.id = $1;

-- name: ListAuditLogFiltered :many
SELECT id, "userId", event::text, COALESCE(action, '') as action,
       COALESCE(resource, '') as resource, metadata, "ipAddress", "userAgent", "createdAt"
FROM audit_logs
WHERE (sqlc.narg('user_id')::text IS NULL OR "userId" = sqlc.narg('user_id'))
  AND (sqlc.narg('event_filter')::text IS NULL OR event::text LIKE sqlc.narg('event_filter') || '%')
  AND (sqlc.narg('resource_filter')::text IS NULL OR resource = sqlc.narg('resource_filter'))
ORDER BY "createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAuditLogFiltered :one
SELECT COUNT(*) FROM audit_logs
WHERE (sqlc.narg('user_id')::text IS NULL OR "userId" = sqlc.narg('user_id'))
  AND (sqlc.narg('event_filter')::text IS NULL OR event::text LIKE sqlc.narg('event_filter') || '%')
  AND (sqlc.narg('resource_filter')::text IS NULL OR resource = sqlc.narg('resource_filter'));

-- name: CountAuditLogsByUser :one
SELECT COUNT(*) FROM audit_logs WHERE "userId" = $1;

-- name: ListUserActivityAuditLogs :many
SELECT id, event::text, description, "ipAddress", "createdAt", success, action
FROM audit_logs
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListUserExportAuditLogs :many
SELECT id, event::text, description, "createdAt", "ipAddress"
FROM audit_logs
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2;

-- name: DeletePasswordResetTokensByUser :exec
DELETE FROM password_reset_tokens WHERE "userId" = $1;

-- name: CountRecentEmailVerificationCodesLastHour :one
SELECT COUNT(*) FROM email_verification_codes
WHERE email = $1 AND "createdAt" > NOW() - INTERVAL '1 hour';

-- name: UpdateUserProfileAdmin :exec
UPDATE users SET
    "firstName" = COALESCE(sqlc.narg('first_name')::text, "firstName"),
    "lastName" = COALESCE(sqlc.narg('last_name')::text, "lastName"),
    email = COALESCE(sqlc.narg('email')::text, email),
    role = COALESCE(sqlc.narg('role')::text::"UserRole", role),
    "subscriptionTier" = COALESCE(sqlc.narg('subscription_tier')::text, "subscriptionTier"),
    "updatedAt" = NOW()
WHERE id = $1;
