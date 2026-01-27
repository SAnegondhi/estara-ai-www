-- Reports and Entitlements Queries

-- InsightAccess Queries

-- name: GetInsightAccessByID :one
SELECT * FROM insight_access WHERE id = $1;

-- name: GetInsightAccessByUserID :one
SELECT * FROM insight_access WHERE "userId" = $1 AND status = 'ACTIVE';

-- name: GetInsightAccessByStripeSubID :one
SELECT * FROM insight_access WHERE "stripeSubId" = $1;

-- name: CreateInsightAccess :one
INSERT INTO insight_access (
    id, "userId", tier, "billingFrequency", "reportsPerPeriod", "reportsPerYear",
    "reportsUsed", "rolloverReports", "periodStartDate", "periodEndDate",
    "startDate", "endDate", "stripeSubId", "stripePriceId", status,
    "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, NOW(), NOW()
) RETURNING *;

-- name: UpdateInsightAccess :one
UPDATE insight_access SET
    tier = COALESCE($2, tier),
    "billingFrequency" = COALESCE($3, "billingFrequency"),
    "reportsPerPeriod" = COALESCE($4, "reportsPerPeriod"),
    "reportsUsed" = COALESCE($5, "reportsUsed"),
    "rolloverReports" = COALESCE($6, "rolloverReports"),
    "lastRolloverDate" = COALESCE($7, "lastRolloverDate"),
    "periodStartDate" = COALESCE($8, "periodStartDate"),
    "periodEndDate" = COALESCE($9, "periodEndDate"),
    status = COALESCE($10, status),
    "lastReportGeneratedAt" = COALESCE($11, "lastReportGeneratedAt"),
    "consumptionHistory" = COALESCE($12, "consumptionHistory"),
    "updatedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: IncrementInsightAccessUsage :one
UPDATE insight_access SET
    "reportsUsed" = "reportsUsed" + 1,
    "lastReportGeneratedAt" = NOW(),
    "updatedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: ListExpiringInsightAccess :many
SELECT * FROM insight_access
WHERE status = 'ACTIVE' AND "periodEndDate" <= $1
ORDER BY "periodEndDate" ASC;

-- name: CancelInsightAccess :one
UPDATE insight_access SET
    status = 'CANCELLED',
    "updatedAt" = NOW()
WHERE id = $1
RETURNING *;

-- ReportPack Queries

-- name: GetReportPackByID :one
SELECT * FROM report_packs WHERE id = $1;

-- name: GetReportPacksByUserID :many
SELECT * FROM report_packs
WHERE "userId" = $1 AND "usedReports" < "totalReports"
ORDER BY "purchasedAt" ASC;

-- name: GetAllReportPacksByUserID :many
SELECT * FROM report_packs
WHERE "userId" = $1
ORDER BY "purchasedAt" DESC;

-- name: CreateReportPack :one
INSERT INTO report_packs (
    id, "userId", "totalReports", "usedReports", "purchasedAt",
    "stripePaymentId", "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, 0, NOW(), $4, NOW(), NOW()
) RETURNING *;

-- name: IncrementReportPackUsage :one
UPDATE report_packs SET
    "usedReports" = "usedReports" + 1,
    "lastUsedAt" = NOW(),
    "updatedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateReportPackConsumptionHistory :exec
UPDATE report_packs SET
    "consumptionHistory" = $2,
    "updatedAt" = NOW()
WHERE id = $1;

-- InvestorReport Queries

-- name: GetInvestorReportByID :one
SELECT * FROM investor_reports WHERE id = $1;

-- name: GetInvestorReportByIDAndUser :one
SELECT * FROM investor_reports WHERE id = $1 AND "userId" = $2;

-- name: GetInvestorReportByCacheKey :one
SELECT * FROM investor_reports
WHERE "cacheKey" = $1 AND status = 'COMPLETE';

-- name: GetInvestorReportByCriteriaHash :one
SELECT * FROM investor_reports
WHERE "criteriaHash" = $1 AND status = 'COMPLETE' AND "supersededAt" IS NULL
ORDER BY "createdAt" DESC
LIMIT 1;

-- name: CreateInvestorReport :one
INSERT INTO investor_reports (
    id, "userId", email, "propertyId", "propertyAddress", type, status,
    "sourceType", "sourcePackId", "sourceAccessId", "stripePaymentId",
    "investmentCriteria", "cacheKey", "criteriaHash", "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW()
) RETURNING *;

-- name: UpdateInvestorReportStatus :one
UPDATE investor_reports SET
    status = $2,
    "completedAt" = CASE WHEN $2 = 'COMPLETE' THEN NOW() ELSE "completedAt" END
WHERE id = $1
RETURNING *;

-- name: UpdateInvestorReportData :one
UPDATE investor_reports SET
    "metricsData" = COALESCE($2, "metricsData"),
    "narrativeData" = COALESCE($3, "narrativeData"),
    "fullReport" = COALESCE($4, "fullReport"),
    "pdfUrl" = COALESCE($5, "pdfUrl"),
    "generationCost" = COALESCE($6, "generationCost"),
    "generationTimeMs" = COALESCE($7, "generationTimeMs"),
    status = COALESCE($8, status),
    "completedAt" = CASE WHEN $8 = 'COMPLETE' THEN NOW() ELSE "completedAt" END
WHERE id = $1
RETURNING *;

-- name: UpdateInvestorReportAllocation :exec
UPDATE investor_reports SET
    "allocationDecrementedAt" = NOW(),
    "allocationSnapshot" = $2
WHERE id = $1;

-- name: SupersedeInvestorReport :exec
UPDATE investor_reports SET
    "supersededById" = $2,
    "supersededAt" = NOW()
WHERE id = $1;

-- name: ListUserInvestorReports :many
SELECT * FROM investor_reports
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListUserInvestorReportsByStatus :many
SELECT * FROM investor_reports
WHERE "userId" = $1 AND status = $2
ORDER BY "createdAt" DESC
LIMIT $3 OFFSET $4;

-- name: CountUserInvestorReports :one
SELECT COUNT(*) FROM investor_reports WHERE "userId" = $1;

-- name: GetPendingInvestorReports :many
SELECT * FROM investor_reports
WHERE status = 'PENDING' OR status = 'GENERATING'
ORDER BY "createdAt" ASC
LIMIT $1;

-- SnapshotRequest Queries

-- name: GetSnapshotRequestByID :one
SELECT * FROM snapshot_requests WHERE id = $1;

-- name: GetSnapshotRequestBySessionID :one
SELECT * FROM snapshot_requests
WHERE "sessionId" = $1
ORDER BY "createdAt" DESC
LIMIT 1;

-- name: CountSnapshotsByEmail :one
SELECT COUNT(*) FROM snapshot_requests
WHERE email = $1 AND status IN ('COMPLETE', 'GENERATING', 'PENDING');

-- name: GetLatestSnapshotByEmail :one
SELECT * FROM snapshot_requests
WHERE email = $1
ORDER BY "createdAt" DESC
LIMIT 1;

-- name: GetSnapshotByCacheKey :one
SELECT * FROM snapshot_requests
WHERE "cacheKey" = $1 AND status = 'COMPLETE' AND ("cacheExpiresAt" IS NULL OR "cacheExpiresAt" > NOW());

-- name: CreateSnapshotRequest :one
INSERT INTO snapshot_requests (
    id, "sessionId", "userId", email, criteria, "criteriaHash", location,
    status, "snapshotNumber", "ipAddress", "userAgent", source, "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW()
) RETURNING *;

-- name: UpdateSnapshotRequestStatus :one
UPDATE snapshot_requests SET
    status = $2,
    "completedAt" = CASE WHEN $2 = 'COMPLETE' THEN NOW() ELSE "completedAt" END
WHERE id = $1
RETURNING *;

-- name: UpdateSnapshotRequestResults :one
UPDATE snapshot_requests SET
    status = 'COMPLETE',
    "propertiesFound" = $2,
    properties = $3,
    "resultId" = $4,
    "cacheKey" = $5,
    "cachedAt" = NOW(),
    "cacheExpiresAt" = $6,
    "completedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: MarkSnapshotConverted :one
UPDATE snapshot_requests SET
    status = 'CONVERTED',
    "convertedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: ListUserSnapshots :many
SELECT * FROM snapshot_requests
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: DeleteExpiredSnapshots :exec
DELETE FROM snapshot_requests
WHERE "cacheExpiresAt" IS NOT NULL AND "cacheExpiresAt" < NOW();
