-- Admin Revenue Analytics Queries

-- name: GetTierDistribution :many
SELECT tier::text, COUNT(*) as cnt
FROM subscriptions
WHERE status IN ('ACTIVE', 'TRIALING')
GROUP BY tier;

-- name: GetSubscriptionChurnStats :one
SELECT
    COUNT(*) FILTER (WHERE status::text = 'CANCELED' AND "updatedAt" >= DATE_TRUNC('month', CURRENT_DATE)) as churned,
    COUNT(*) FILTER (WHERE "createdAt" >= DATE_TRUNC('month', CURRENT_DATE) AND status IN ('ACTIVE', 'TRIALING')) as new_this_month
FROM subscriptions;

-- name: GetMRRByMonth :many
WITH months AS (
    SELECT generate_series(
        DATE_TRUNC('month', CURRENT_DATE - INTERVAL '11 months'),
        DATE_TRUNC('month', CURRENT_DATE),
        '1 month'::interval
    ) AS month
)
SELECT
    m.month,
    COUNT(*) FILTER (WHERE s."createdAt" < m.month + INTERVAL '1 month'
        AND (s.status IN ('ACTIVE','TRIALING')
            OR (s.status::text = 'CANCELED' AND s."updatedAt" >= m.month + INTERVAL '1 month'))) as active_count,
    COUNT(*) FILTER (WHERE s."createdAt" >= m.month AND s."createdAt" < m.month + INTERVAL '1 month') as new_subs,
    COUNT(*) FILTER (WHERE s.status::text = 'CANCELED' AND s."updatedAt" >= m.month AND s."updatedAt" < m.month + INTERVAL '1 month') as churned
FROM months m
LEFT JOIN subscriptions s ON TRUE
GROUP BY m.month
ORDER BY m.month;

-- name: GetMRRByTier :many
SELECT tier::text, COUNT(*) as cnt
FROM subscriptions
WHERE status IN ('ACTIVE', 'TRIALING')
GROUP BY tier
ORDER BY cnt DESC;

-- name: GetAtRiskCustomers :many
SELECT
    u.id, u.email, s.tier::text, u."updatedAt", s.status::text,
    EXTRACT(DAY FROM NOW() - u."updatedAt")::int as days_inactive
FROM users u
JOIN subscriptions s ON u.id = s."userId"
WHERE s.status IN ('ACTIVE', 'TRIALING')
ORDER BY u."updatedAt" ASC
LIMIT 50;

-- name: GetRevenueLeakageRefunds :one
SELECT
    COUNT(*) FILTER (WHERE action = 'STRIPE_REFUND') as refund_count,
    COUNT(*) FILTER (WHERE action = 'DISPUTE_CREATED' OR action LIKE 'DISPUTE_%') as chargeback_count
FROM audit_logs
WHERE "createdAt" >= $1;

-- name: GetRevenueLeakageInvoices :one
SELECT COUNT(*) as count, COALESCE(SUM("amountDue"), 0)::bigint as total
FROM invoices
WHERE status IN ('UNCOLLECTIBLE', 'VOID')
AND "createdAt" >= $1;

-- name: GetDisputeCount :one
SELECT COUNT(*)
FROM audit_logs
WHERE action LIKE 'DISPUTE_%'
AND "createdAt" >= NOW() - INTERVAL '90 days';

-- name: GetPaidInvoiceCount :one
SELECT COUNT(*)
FROM invoices
WHERE status = 'PAID'
AND "createdAt" >= NOW() - INTERVAL '90 days';

-- name: CountPaidInvoicesAfterDate :one
SELECT COUNT(*) FROM invoices
WHERE "createdAt" >= $1 AND status = 'PAID';

-- name: GetRetentionCohorts :many
WITH cohorts AS (
    SELECT
        DATE_TRUNC('month', "createdAt") as cohort_month,
        COUNT(*) as signup_count,
        COUNT(*) FILTER (WHERE status IN ('ACTIVE', 'TRIALING')) as active_now
    FROM subscriptions
    WHERE "createdAt" >= DATE_TRUNC('month', CURRENT_DATE - INTERVAL '11 months')
    GROUP BY DATE_TRUNC('month', "createdAt")
    ORDER BY cohort_month
)
SELECT cohort_month, signup_count, active_now FROM cohorts;
