-- Queries for admin extended tables (vendor contracts, credits, cron tracking, terms)

-- =========================================================
-- Vendor Contracts
-- =========================================================

-- name: CreateVendorContract :one
INSERT INTO vendor_contracts (
    id, "vendorId", "startDate", "endDate", "autoRenew",
    "renewalTermMonths", "terminationNoticeDays", "pricingTiers",
    "usageLimits", "slaTerms", "legalRestrictions",
    "contactName", "contactEmail", notes, "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW()
) RETURNING *;

-- name: GetVendorContract :one
SELECT * FROM vendor_contracts
WHERE "vendorId" = $1
ORDER BY "createdAt" DESC
LIMIT 1;

-- name: GetVendorContractByID :one
SELECT * FROM vendor_contracts
WHERE id = $1;

-- name: UpdateVendorContract :one
UPDATE vendor_contracts SET
    "startDate" = $2,
    "endDate" = $3,
    "autoRenew" = $4,
    "renewalTermMonths" = $5,
    "terminationNoticeDays" = $6,
    "pricingTiers" = $7,
    "usageLimits" = $8,
    "slaTerms" = $9,
    "legalRestrictions" = $10,
    "contactName" = $11,
    "contactEmail" = $12,
    notes = $13,
    "updatedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: ListVendorContracts :many
SELECT * FROM vendor_contracts
ORDER BY "endDate" ASC NULLS LAST;

-- name: ListExpiringVendorContracts :many
SELECT * FROM vendor_contracts
WHERE "endDate" IS NOT NULL
  AND "endDate" < NOW() + ($1 || ' days')::INTERVAL
  AND "endDate" > NOW()
ORDER BY "endDate" ASC;

-- name: DeleteVendorContract :exec
DELETE FROM vendor_contracts WHERE id = $1;

-- =========================================================
-- Terms Acceptances
-- =========================================================

-- name: CreateTermsAcceptance :one
INSERT INTO terms_acceptances (
    id, "userId", version, "acceptedAt", "ipAddress", "userAgent"
) VALUES (
    $1, $2, $3, NOW(), $4, $5
) RETURNING *;

-- name: GetTermsAcceptancesByUser :many
SELECT * FROM terms_acceptances
WHERE "userId" = $1
ORDER BY "acceptedAt" DESC;

-- name: ListTermsAcceptances :many
SELECT * FROM terms_acceptances
ORDER BY "acceptedAt" DESC
LIMIT $1 OFFSET $2;

-- name: CountTermsAcceptancesByVersion :one
SELECT COUNT(*) FROM terms_acceptances
WHERE version = $1;

-- name: GetLatestTermsAcceptance :one
SELECT * FROM terms_acceptances
WHERE "userId" = $1
ORDER BY "acceptedAt" DESC
LIMIT 1;

-- =========================================================
-- Admin Credits
-- =========================================================

-- name: CreateAdminCredit :one
INSERT INTO admin_credits (
    id, "userId", amount, reason, "grantedBy", "expiresAt", "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, NOW()
) RETURNING *;

-- name: GetAdminCreditsByUser :many
SELECT * FROM admin_credits
WHERE "userId" = $1
ORDER BY "createdAt" DESC;

-- name: ListAdminCredits :many
SELECT * FROM admin_credits
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: ListUnusedAdminCredits :many
SELECT * FROM admin_credits
WHERE applied = false
  AND ("expiresAt" IS NULL OR "expiresAt" > NOW())
ORDER BY "createdAt" DESC;

-- name: ApplyAdminCredit :one
UPDATE admin_credits SET
    applied = true,
    "appliedAt" = NOW()
WHERE id = $1 AND applied = false
RETURNING *;

-- =========================================================
-- Cron Job Configs
-- =========================================================

-- name: CreateCronJobConfig :one
INSERT INTO cron_job_configs (
    id, name, description, schedule, endpoint,
    "isEnabled", "timeoutMs", "maxConsecutiveFailures",
    "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW()
) RETURNING *;

-- name: UpsertCronJobConfig :one
INSERT INTO cron_job_configs (
    id, name, description, schedule, endpoint,
    "isEnabled", "timeoutMs", "maxConsecutiveFailures",
    "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, true, $6, 3, NOW(), NOW()
)
ON CONFLICT (name)
DO UPDATE SET
    description = EXCLUDED.description,
    schedule = EXCLUDED.schedule,
    endpoint = EXCLUDED.endpoint,
    "timeoutMs" = EXCLUDED."timeoutMs",
    "updatedAt" = NOW()
RETURNING *;

-- name: GetCronJobConfigByName :one
SELECT * FROM cron_job_configs
WHERE name = $1;

-- name: GetCronJobConfigByID :one
SELECT * FROM cron_job_configs
WHERE id = $1;

-- name: ListCronJobConfigs :many
SELECT * FROM cron_job_configs
ORDER BY name;

-- name: ToggleCronJobConfig :one
UPDATE cron_job_configs SET
    "isEnabled" = $2,
    "updatedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateCronJobLastRun :exec
UPDATE cron_job_configs SET
    "lastRunAt" = $2,
    "lastRunStatus" = $3,
    "lastRunDurationMs" = $4,
    "totalRuns" = "totalRuns" + 1,
    "successfulRuns" = CASE WHEN $3 = 'SUCCESS' THEN "successfulRuns" + 1 ELSE "successfulRuns" END,
    "consecutiveFailures" = CASE WHEN $3 = 'SUCCESS' THEN 0 ELSE "consecutiveFailures" + 1 END,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: ResetCronJobFailures :exec
UPDATE cron_job_configs SET
    "consecutiveFailures" = 0,
    "updatedAt" = NOW()
WHERE id = $1;

-- =========================================================
-- Cron Job Runs
-- =========================================================

-- name: CreateCronJobRun :one
INSERT INTO cron_job_runs (
    id, "jobId", status, "startedAt", "createdAt"
) VALUES (
    $1, $2, 'RUNNING', NOW(), NOW()
) RETURNING *;

-- name: CompleteCronJobRun :one
UPDATE cron_job_runs SET
    status = $2,
    "completedAt" = NOW(),
    "durationMs" = $3,
    error = $4,
    output = $5
WHERE id = $1
RETURNING *;

-- name: GetCronJobRun :one
SELECT * FROM cron_job_runs
WHERE id = $1;

-- name: ListCronJobRuns :many
SELECT * FROM cron_job_runs
WHERE "jobId" = $1
ORDER BY "startedAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListRecentCronJobRuns :many
SELECT * FROM cron_job_runs
ORDER BY "startedAt" DESC
LIMIT $1;

-- name: CountCronJobRunsByStatus :one
SELECT
    COUNT(*) FILTER (WHERE status = 'SUCCESS') as success_count,
    COUNT(*) FILTER (WHERE status = 'FAILED') as failed_count,
    COUNT(*) FILTER (WHERE status = 'TIMEOUT') as timeout_count,
    COUNT(*) FILTER (WHERE status = 'RUNNING') as running_count
FROM cron_job_runs
WHERE "jobId" = $1;

-- name: DeleteOldCronJobRuns :exec
DELETE FROM cron_job_runs
WHERE "startedAt" < NOW() - ($1 || ' days')::INTERVAL;
