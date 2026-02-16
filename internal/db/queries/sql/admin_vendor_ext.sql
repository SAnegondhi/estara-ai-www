-- Admin Vendor Management Queries

-- name: ListVendorConfigsAdmin :many
SELECT id, name, "displayName", category::text, "billingModel"::text,
       "isActive", "healthCheckUrl", "lastHealthCheck", "healthStatus"::text,
       "monthlyCost", "isPrimary", notes, "createdAt", "updatedAt"
FROM vendor_configs
ORDER BY category, name;

-- name: GetVendorConfigAdmin :one
SELECT id, name, "displayName", category::text, "billingModel"::text,
       "isActive", "healthCheckUrl", "lastHealthCheck", "healthStatus"::text,
       "monthlyCost", "apiKeyEnvVar", "isPrimary", notes, "createdAt", "updatedAt"
FROM vendor_configs
WHERE id = $1;

-- name: UpdateVendorHealthCheck :exec
UPDATE vendor_configs
SET "healthStatus" = $2::text::"VendorHealthStatus", "lastHealthCheck" = NOW(), "updatedAt" = NOW()
WHERE id = $1;

-- name: ToggleVendorActive :exec
UPDATE vendor_configs
SET "isActive" = $2, "updatedAt" = NOW()
WHERE id = $1;

-- name: CreateVendorConfig :one
INSERT INTO vendor_configs (
    id, name, "displayName", category, "billingModel",
    "monthlyCost", "apiKeyEnvVar", "healthCheckUrl", "isPrimary", notes,
    "isActive", "createdAt", "updatedAt"
) VALUES ($1, $2, $3, $4::text::"VendorCategory", $5::text::"VendorBillingModel",
    $6, $7, $8, $9, $10,
    true, NOW(), NOW())
RETURNING id, name, "displayName", category::text, "billingModel"::text,
          "isActive", "healthCheckUrl", "lastHealthCheck", "healthStatus"::text,
          "monthlyCost", "isPrimary", notes, "createdAt", "updatedAt";

-- name: UpdateVendorConfig :one
UPDATE vendor_configs SET
    name = COALESCE(sqlc.narg('name')::text, name),
    "displayName" = COALESCE(sqlc.narg('display_name')::text, "displayName"),
    "monthlyCost" = COALESCE(sqlc.narg('monthly_cost')::numeric, "monthlyCost"),
    "apiKeyEnvVar" = COALESCE(sqlc.narg('api_key_env_var')::text, "apiKeyEnvVar"),
    "healthCheckUrl" = COALESCE(sqlc.narg('health_check_url')::text, "healthCheckUrl"),
    "isPrimary" = COALESCE(sqlc.narg('is_primary')::boolean, "isPrimary"),
    notes = COALESCE(sqlc.narg('notes')::text, notes),
    "updatedAt" = NOW()
WHERE id = $1
RETURNING id, name, "displayName", category::text, "billingModel"::text,
          "isActive", "healthCheckUrl", "lastHealthCheck", "healthStatus"::text,
          "monthlyCost", "isPrimary", notes, "createdAt", "updatedAt";

-- name: DeactivateVendorConfig :exec
UPDATE vendor_configs
SET "isActive" = false, "updatedAt" = NOW()
WHERE id = $1;

-- name: ListVendorSystemAlerts :many
SELECT id, type, severity, title, description, "alertKey", metadata,
    "firstSeen", "lastSeen", "occurrenceCount", dismissed, "actionRequired",
    "expiresAt", "createdAt"
FROM system_alerts
WHERE type LIKE 'vendor_%' AND dismissed = false
ORDER BY "createdAt" DESC
LIMIT 50;

-- name: GetVendorCostSummary :many
SELECT
    v.id as vendor_id,
    v.name as vendor_name,
    v.category::text,
    COALESCE(SUM(s."totalCost"), 0) as total_cost,
    COALESCE(SUM(s."totalRequests"), 0)::bigint as request_count,
    MAX(COALESCE(s."createdAt", NOW())) as recorded_at
FROM vendor_configs v
LEFT JOIN vendor_usage_summaries s ON v.name = s."vendorName"
    AND s.month = $1 AND s.year = $2
GROUP BY v.id, v.name, v.category::text
ORDER BY total_cost DESC;

-- name: ToggleVendorActiveRows :execrows
UPDATE vendor_configs
SET "isActive" = $2, "updatedAt" = NOW()
WHERE id = $1;

-- name: DismissSystemAlertRows :execrows
UPDATE system_alerts SET
    dismissed = true,
    "updatedAt" = NOW()
WHERE id = $1 AND dismissed = false;

-- name: GetWhitelistSummary :one
SELECT
    COUNT(*) as total,
    COUNT(*) FILTER (WHERE active = true) as active,
    COUNT(*) FILTER (WHERE type::text = 'EMAIL') as email_count,
    COUNT(*) FILTER (WHERE type::text = 'DOMAIN') as domain_count
FROM whitelisted_emails;
