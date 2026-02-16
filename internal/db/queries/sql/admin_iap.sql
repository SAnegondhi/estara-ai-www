-- Admin IAP (In-App Purchase) Management Queries

-- name: GetIAPStats :one
SELECT
    COUNT(*) FILTER (WHERE "iapPlatform" IS NOT NULL) as total_iap,
    COUNT(*) FILTER (WHERE "iapPlatform" IS NOT NULL AND "iapExpiresAt" > NOW()) as active_iap,
    COUNT(*) FILTER (WHERE "iapPlatform" IS NOT NULL AND "iapExpiresAt" > NOW() AND "iapExpiresAt" < NOW() + INTERVAL '7 days') as expiring_soon,
    COUNT(*) FILTER (WHERE "iapPlatform" IS NOT NULL AND "iapExpiresAt" <= NOW()) as expired,
    COUNT(*) FILTER (WHERE "iapPlatform" = 'apple') as apple_count,
    COUNT(*) FILTER (WHERE "iapPlatform" = 'google') as google_count
FROM users;

-- name: ListIAPAuditLogs :many
SELECT id, action, resource, metadata, "createdAt"
FROM audit_logs
WHERE action LIKE 'IAP_%' OR resource = 'iap'
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: CountIAPAuditLogs :one
SELECT COUNT(*) FROM audit_logs
WHERE action LIKE 'IAP_%' OR resource = 'iap';

-- name: GetUserIAPPlatform :one
SELECT "iapPlatform" FROM users WHERE id = $1;

-- name: DowngradeIAPUser :exec
UPDATE users SET
    "subscriptionTier" = 'free',
    "iapPlatform" = NULL,
    "iapExpiresAt" = NULL,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: CancelIAPSubscriptions :exec
UPDATE subscriptions SET
    status = 'CANCELED',
    tier = 'FREE',
    "updatedAt" = NOW()
WHERE "userId" = $1 AND status IN ('ACTIVE', 'TRIALING');

-- name: ListWebhookEvents :many
SELECT id, "eventType"::text, "eventData", "createdAt"
FROM billing_audit_logs
WHERE "eventType" IS NOT NULL
ORDER BY "createdAt" DESC
LIMIT $1;

-- name: ListInvestorReportsFiltered :many
SELECT r.id, r."userId", u.email, r.type::text as report_type, r.status::text,
       r."propertyAddress", r."createdAt", r."completedAt"
FROM investor_reports r
LEFT JOIN users u ON r."userId" = u.id
WHERE (sqlc.narg('status_filter')::text IS NULL OR r.status::text = sqlc.narg('status_filter'))
  AND (sqlc.narg('user_id')::text IS NULL OR r."userId" = sqlc.narg('user_id'))
ORDER BY r."createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountInvestorReportsFiltered :one
SELECT COUNT(*)
FROM investor_reports r
WHERE (sqlc.narg('status_filter')::text IS NULL OR r.status::text = sqlc.narg('status_filter'))
  AND (sqlc.narg('user_id')::text IS NULL OR r."userId" = sqlc.narg('user_id'));

-- name: UpdateInvestorReportStatusAdmin :exec
UPDATE investor_reports
SET status = $2::text::"InvestorReportStatus",
    "completedAt" = CASE WHEN $2 IN ('COMPLETE', 'FAILED') THEN NOW() ELSE "completedAt" END
WHERE id = $1;
