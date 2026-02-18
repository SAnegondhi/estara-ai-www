-- Admin Dashboard Analytics Queries

-- name: CountActiveSubscriptions :one
SELECT COUNT(*) FROM subscriptions WHERE status IN ('ACTIVE', 'TRIALING');

-- name: CountVendorConfigs :one
SELECT COUNT(*) FROM vendor_configs;

-- name: CountCronJobConfigs :one
SELECT COUNT(*) FROM cron_job_configs;

-- name: GetCacheStatsAdmin :one
SELECT
    COUNT(*) as total_entries,
    COUNT(*) FILTER (WHERE "expiresAt" < NOW()) as expired_entries,
    COUNT(DISTINCT "userId") as unique_users,
    COUNT(DISTINCT feature) as cache_types
FROM analysis_cache;

-- name: GetAIUsageTotalCost :one
SELECT
    COALESCE(SUM("inputTokens"), 0)::bigint as total_input_tokens,
    COALESCE(SUM("outputTokens"), 0)::bigint as total_output_tokens,
    COALESCE(SUM("totalCost"), 0) as total_cost
FROM ai_usage;

-- name: DeleteAllCache :exec
DELETE FROM analysis_cache;

-- name: GetUserAnalytics :one
SELECT
    COUNT(*) as total,
    COUNT(*) FILTER (WHERE "updatedAt" > NOW() - INTERVAL '7 days') as active_this_week,
    COUNT(*) FILTER (WHERE "updatedAt" > NOW() - INTERVAL '30 days') as active_this_month,
    COUNT(*) FILTER (WHERE "createdAt" > NOW() - INTERVAL '7 days') as new_this_week,
    COUNT(*) FILTER (WHERE "createdAt" > NOW() - INTERVAL '30 days') as new_this_month
FROM users;

-- name: GetReportAnalytics :one
SELECT
    COUNT(*) as total_generated,
    COUNT(*) FILTER (WHERE "createdAt" > NOW() - INTERVAL '7 days') as this_week,
    COUNT(*) FILTER (WHERE "createdAt" > NOW() - INTERVAL '30 days') as this_month,
    COUNT(*) FILTER (WHERE status = 'FAILED' AND "createdAt" > NOW() - INTERVAL '7 days') as failed_this_week
FROM investor_reports;

-- name: GetAIRequestAnalytics :one
SELECT
    COUNT(*) as total_requests,
    COUNT(*) FILTER (WHERE "createdAt" >= CURRENT_DATE) as requests_today
FROM ai_usage;

-- name: GetSubscriptionRevenueSummary :one
SELECT
    COALESCE(SUM(CASE
        WHEN status IN ('ACTIVE', 'TRIALING') AND tier::text = 'INVESTOR' THEN 29.99
        WHEN status IN ('ACTIVE', 'TRIALING') AND tier::text = 'PROFESSIONAL' THEN 49.99
        WHEN status IN ('ACTIVE', 'TRIALING') AND tier::text = 'ANNUAL_ACCESS' THEN 99.99
        WHEN status IN ('ACTIVE', 'TRIALING') AND tier::text = 'PROFESSIONAL_ALLOCATOR' THEN 149.99
        WHEN status IN ('ACTIVE', 'TRIALING') AND tier::text = 'AAPI_INVESTOR' THEN 79.99
        WHEN status IN ('ACTIVE', 'TRIALING') AND tier::text = 'AAPI_ALLOCATOR' THEN 199.99
        ELSE 0
    END), 0) as mrr,
    COUNT(*) FILTER (WHERE status IN ('ACTIVE', 'TRIALING')) as active_subs,
    COUNT(*) FILTER (WHERE status IN ('ACTIVE', 'TRIALING') AND "createdAt" > NOW() - INTERVAL '30 days') as new_subs,
    COUNT(*) FILTER (WHERE status::text = 'CANCELED' AND "updatedAt" > NOW() - INTERVAL '30 days') as churned
FROM subscriptions;

-- name: GetAdminUserStats :one
SELECT
    COUNT(*) as total_users,
    COUNT(*) FILTER (WHERE role::text IN ('ADMIN','SUPER_ADMIN')) as admin_count,
    COUNT(*) FILTER (WHERE role::text = 'USER') as user_count,
    COUNT(*) FILTER (WHERE "subscriptionTier" = 'free' OR "subscriptionTier" IS NULL) as free_count,
    COUNT(*) FILTER (WHERE "subscriptionTier" NOT IN ('free') AND "subscriptionTier" IS NOT NULL) as pro_count,
    COUNT(*) FILTER (WHERE "updatedAt" > NOW() - INTERVAL '7 days') as active_last_week
FROM users;

-- name: CountSearchUsersByEmail :one
SELECT COUNT(*) FROM users WHERE LOWER(email) LIKE $1;

-- name: ListSystemAlertsFiltered :many
SELECT id, type, severity, title, description, "alertKey", metadata,
    "firstSeen", "lastSeen", "occurrenceCount", dismissed, "actionRequired",
    "expiresAt", "createdAt"
FROM system_alerts
WHERE (sqlc.narg('dismissed_filter')::boolean IS NULL OR dismissed = sqlc.narg('dismissed_filter'))
ORDER BY
    CASE severity WHEN 'critical' THEN 1 WHEN 'error' THEN 2 WHEN 'warning' THEN 3 ELSE 4 END,
    "createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ResetInvestorReportForRetry :execrows
UPDATE investor_reports
SET status = 'PENDING', "completedAt" = NULL
WHERE id = $1;

-- name: CountAuditLogsFiltered :one
SELECT COUNT(*) FROM audit_logs
WHERE (sqlc.narg('user_id')::text IS NULL OR "userId" = sqlc.narg('user_id'))
  AND (sqlc.narg('action_filter')::text IS NULL OR (event::text LIKE sqlc.narg('action_filter') || '%' OR action LIKE sqlc.narg('action_filter') || '%'))
  AND (sqlc.narg('resource_filter')::text IS NULL OR resource = sqlc.narg('resource_filter'));

-- name: ListAuditLogsFiltered :many
SELECT id, "userId", event::text, COALESCE(action, '') as action, COALESCE(resource, '') as resource,
       COALESCE(description, '') as description, success,
       metadata, "ipAddress", "userAgent", "createdAt"
FROM audit_logs
WHERE (sqlc.narg('user_id')::text IS NULL OR "userId" = sqlc.narg('user_id'))
  AND (sqlc.narg('action_filter')::text IS NULL OR (event::text LIKE sqlc.narg('action_filter') || '%' OR action LIKE sqlc.narg('action_filter') || '%'))
  AND (sqlc.narg('resource_filter')::text IS NULL OR resource = sqlc.narg('resource_filter'))
ORDER BY "createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListAdminActionsForUser :many
SELECT id, "adminId", "adminEmail", action::text, resource, "resourceId",
       details, "ipAddress", "userAgent", "createdAt"
FROM admin_audit_log
WHERE "resourceId" = sqlc.arg('resource_id')
ORDER BY "createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAdminActionsForUser :one
SELECT COUNT(*) FROM admin_audit_log WHERE "resourceId" = sqlc.arg('resource_id');

-- name: CountSystemAlertsFiltered :one
SELECT COUNT(*) FROM system_alerts
WHERE (sqlc.narg('dismissed_filter')::boolean IS NULL OR dismissed = sqlc.narg('dismissed_filter'));
