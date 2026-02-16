-- Admin Accounting Queries

-- name: GetMonthlyRevenue :many
SELECT
    DATE_TRUNC('month', "createdAt") as month,
    COALESCE(SUM("amountPaid"), 0)::bigint as total_paid,
    COALESCE(SUM("taxAmount"), 0)::bigint as total_tax
FROM invoices
WHERE status = 'PAID'
AND "createdAt" >= DATE_TRUNC('month', CURRENT_DATE - ($1 || ' months')::interval)
GROUP BY DATE_TRUNC('month', "createdAt")
ORDER BY month DESC;

-- name: ListActiveSubscriptionsWithUsers :many
SELECT
    s.id, u.email, s.tier::text,
    s."currentPeriodStart", s."currentPeriodEnd"
FROM subscriptions s
JOIN users u ON s."userId" = u.id
WHERE s.status::text = 'ACTIVE'
AND s."currentPeriodStart" IS NOT NULL
AND s."currentPeriodEnd" IS NOT NULL
ORDER BY s."currentPeriodEnd" DESC;

-- name: ListActiveVendorConfigs :many
SELECT id, name, category::text, COALESCE("monthlyCost", 0) as monthly_cost
FROM vendor_configs
WHERE "isActive" = true
ORDER BY category::text, name;

-- name: ListVendorContractsWithConfigs :many
SELECT vc.id, vc.name, vc.category::text, COALESCE(c.notes, '') as notes
FROM vendor_configs vc
JOIN vendor_contracts c ON vc.id = c."vendorId"
WHERE vc."isActive" = true AND c."endDate" > NOW();

-- name: GetQuarterlyExpenses :many
SELECT
    DATE_TRUNC('month', "createdAt") as month,
    COALESCE(SUM("amountPaid"), 0)::bigint as total
FROM invoices
WHERE status = 'PAID'
AND "createdAt" >= DATE_TRUNC('month', CURRENT_DATE - INTERVAL '5 months')
GROUP BY DATE_TRUNC('month', "createdAt")
ORDER BY month;

-- name: GetTotalActiveVendorCost :one
SELECT COALESCE(SUM("monthlyCost"), 0) as total
FROM vendor_configs
WHERE "isActive" = true;

-- name: ListActiveVendorNames :many
SELECT name, category::text
FROM vendor_configs
WHERE "isActive" = true
ORDER BY category::text, name;

-- name: GetActiveVendorContractCount :one
SELECT COUNT(*)
FROM vendor_configs vc
JOIN vendor_contracts c ON vc.id = c."vendorId"
WHERE vc."isActive" = true AND c."endDate" > NOW();

-- name: GetTaxSummary :many
SELECT
    COALESCE(u.state, 'Unknown') as state,
    COALESCE(SUM(i."taxAmount"), 0)::bigint as total_tax,
    COUNT(*) as invoice_count,
    COALESCE(SUM(i.total), 0)::bigint as total_revenue
FROM invoices i
LEFT JOIN users u ON i."userId" = u.id
WHERE i.status = 'PAID'
GROUP BY u.state
ORDER BY total_tax DESC;

-- name: GetTermsLogWithUsers :many
SELECT t.id, t."userId", u.email, t.version, t."acceptedAt", t."ipAddress"
FROM terms_acceptances t
LEFT JOIN users u ON t."userId" = u.id
ORDER BY t."acceptedAt" DESC
LIMIT $1 OFFSET $2;

-- name: GetTermsAcceptancesByVersion :many
SELECT version, COUNT(*) as cnt
FROM terms_acceptances
GROUP BY version
ORDER BY version DESC;

-- name: CountTermsAcceptances :one
SELECT COUNT(*) FROM terms_acceptances;
