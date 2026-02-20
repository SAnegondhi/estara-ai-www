-- Analysis Cache Queries
-- Table: analysis_cache (schema 002_cache.sql)
-- These queries supplement existing queries in cache.sql

-- name: GetAnalysisContext :one
SELECT content, "metricsData", "narrativeData", "lastAccessedAt"
FROM analysis_cache
WHERE "userId" = $1
  AND feature = 'dual_agent_market_analysis'
  AND location ILIKE $2
  AND "supersededBy" IS NULL
ORDER BY "lastAccessedAt" DESC
LIMIT 1;

-- name: GetInvestmentPlanCacheByID :one
SELECT id, key
FROM analysis_cache
WHERE id = $1 AND "userId" = $2 AND key LIKE 'investment_plan_%';

-- name: DeleteAnalysisCacheByUserAndKey :exec
DELETE FROM analysis_cache
WHERE "userId" = $1 AND key = $2;

-- name: GetAnalysisCacheByKey :one
SELECT content, "metricsData", "narrativeData", "fullReport", "lastAccessedAt"
FROM analysis_cache
WHERE key = $1
  AND "supersededBy" IS NULL
ORDER BY "lastAccessedAt" DESC
LIMIT 1;

-- name: BatchGetAnalysisCacheByKeys :many
-- Batch fetch cache entries for multiple keys (to avoid N+1 queries)
-- Only fetch fields needed for preview generation (not full metricsData/narrativeData)
SELECT key, content, "fullReport"
FROM analysis_cache
WHERE key = ANY(sqlc.arg('keys')::text[])
  AND "supersededBy" IS NULL;
