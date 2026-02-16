-- Usage Tracking Queries (Investment Plan + Area Comparison)

-- name: GetInvestmentPlanUsage :one
SELECT id, "userId", month, year, "picksUsed", "lastPickAt", "createdAt", "updatedAt"
FROM investment_plan_usage
WHERE "userId" = $1 AND month = $2 AND year = $3;

-- name: UpsertInvestmentPlanUsage :one
INSERT INTO investment_plan_usage (
    id, "userId", month, year, "picksUsed", "lastPickAt", "createdAt", "updatedAt"
) VALUES ($1, $2, $3, $4, $5, NOW(), NOW(), NOW())
ON CONFLICT ("userId", month, year) DO UPDATE SET
    "picksUsed" = investment_plan_usage."picksUsed" + EXCLUDED."picksUsed",
    "lastPickAt" = NOW(),
    "updatedAt" = NOW()
RETURNING *;

-- name: GetAreaComparisonUsage :one
SELECT id, "userId", month, year, "comparisonsUsed", "lastComparisonAt", "createdAt", "updatedAt"
FROM area_comparison_usage
WHERE "userId" = $1 AND month = $2 AND year = $3;

-- name: UpsertAreaComparisonUsage :one
INSERT INTO area_comparison_usage (
    id, "userId", month, year, "comparisonsUsed", "lastComparisonAt", "createdAt", "updatedAt"
) VALUES ($1, $2, $3, $4, $5, NOW(), NOW(), NOW())
ON CONFLICT ("userId", month, year) DO UPDATE SET
    "comparisonsUsed" = area_comparison_usage."comparisonsUsed" + EXCLUDED."comparisonsUsed",
    "lastComparisonAt" = NOW(),
    "updatedAt" = NOW()
RETURNING *;
