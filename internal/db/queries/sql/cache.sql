-- name: GetCacheByKey :one
SELECT * FROM analysis_cache
WHERE key = $1 AND "expiresAt" > NOW();

-- name: GetCacheByUserAndKey :one
SELECT * FROM analysis_cache
WHERE "userId" = $1 AND key = $2 AND "expiresAt" > NOW();

-- name: GetCacheByUserAndKeyNoExpiry :one
-- Used for investment plans which shouldn't expire like ephemeral cached data
SELECT * FROM analysis_cache
WHERE "userId" = $1 AND key = $2;

-- name: GetCacheByID :one
SELECT * FROM analysis_cache WHERE id = $1;

-- name: UpsertCache :one
INSERT INTO analysis_cache (
    id, key, "userId", location, feature, content,
    "metricsData", "narrativeData", metadata,
    "expiresAt", "createdAt", "lastAccessedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW()
)
ON CONFLICT (key) DO UPDATE SET
    content = EXCLUDED.content,
    feature = EXCLUDED.feature,
    "metricsData" = EXCLUDED."metricsData",
    "narrativeData" = EXCLUDED."narrativeData",
    metadata = EXCLUDED.metadata,
    "expiresAt" = EXCLUDED."expiresAt",
    "lastAccessedAt" = NOW()
RETURNING *;

-- name: UpdateCacheAccess :exec
UPDATE analysis_cache
SET "lastAccessedAt" = NOW(), "accessCount" = "accessCount" + 1
WHERE key = $1;

-- name: DeleteCacheByKey :exec
DELETE FROM analysis_cache WHERE key = $1;

-- name: DeleteCacheByUserAndKey :exec
DELETE FROM analysis_cache
WHERE "userId" = $1 AND key = $2;

-- name: DeleteCacheByUserID :exec
DELETE FROM analysis_cache WHERE "userId" = $1;

-- name: DeleteExpiredCache :execrows
DELETE FROM analysis_cache WHERE "expiresAt" < NOW();

-- name: ListCacheByUser :many
SELECT * FROM analysis_cache
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: CountCacheByUser :one
SELECT COUNT(*) FROM analysis_cache WHERE "userId" = $1;

-- name: ListCacheByFeature :many
SELECT * FROM analysis_cache
WHERE feature = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: CountAllCache :one
SELECT COUNT(*) FROM analysis_cache;

-- name: CountExpiredCache :one
SELECT COUNT(*) FROM analysis_cache WHERE "expiresAt" < NOW();

-- name: GetCacheStats :one
SELECT
    COUNT(*) as total_entries,
    COUNT(*) FILTER (WHERE "expiresAt" < NOW()) as expired_entries,
    COUNT(DISTINCT "userId") as unique_users,
    COUNT(DISTINCT feature) as feature_count
FROM analysis_cache;

-- name: DeleteAllExpiredCache :execrows
DELETE FROM analysis_cache WHERE "expiresAt" < NOW();

-- name: DeleteCacheByFeature :execrows
DELETE FROM analysis_cache WHERE feature = $1;

-- name: DeleteCacheOlderThan :execrows
DELETE FROM analysis_cache WHERE "createdAt" < $1;

-- name: UpsertAnalysisReport :one
-- ADR-074: Upsert analysis report with fullReport column for V2 markdown reports
INSERT INTO analysis_cache (
    id, key, "userId", location, feature, content, "fullReport",
    "metricsData", "narrativeData", metadata,
    "expiresAt", "createdAt", "lastAccessedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW()
)
ON CONFLICT (key) DO UPDATE SET
    "userId" = EXCLUDED."userId",
    content = EXCLUDED.content,
    "fullReport" = EXCLUDED."fullReport",
    feature = EXCLUDED.feature,
    "metricsData" = EXCLUDED."metricsData",
    "narrativeData" = EXCLUDED."narrativeData",
    metadata = EXCLUDED.metadata,
    "expiresAt" = EXCLUDED."expiresAt",
    "lastAccessedAt" = NOW()
RETURNING *;

-- name: ListCacheByLocation :many
SELECT * FROM analysis_cache
WHERE location = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: DeleteAllCacheRows :execrows
DELETE FROM analysis_cache;

-- name: DeleteCacheByUserIDRows :execrows
DELETE FROM analysis_cache WHERE "userId" = $1;

-- name: DeleteCacheByKeyRows :execrows
DELETE FROM analysis_cache WHERE key = $1;

-- name: DeleteCacheByUserAndKeyRows :execrows
DELETE FROM analysis_cache WHERE "userId" = $1 AND key = $2;

-- name: ListCacheByUserAndFeature :many
SELECT * FROM analysis_cache
WHERE "userId" = $1 AND feature = $2 AND "expiresAt" > NOW()
ORDER BY "lastAccessedAt" DESC
LIMIT $3 OFFSET $4;

-- name: DeleteCacheByIDAndUserID :exec
DELETE FROM analysis_cache WHERE id = $1 AND "userId" = $2;

-- name: GetCacheIDAndKeyByUserAndKey :one
SELECT id, key FROM analysis_cache WHERE key = $1 AND "userId" = $2;

-- name: GetCacheIDAndKeyByIDUserKeyLike :one
SELECT id, key FROM analysis_cache WHERE id = $1 AND "userId" = $2 AND key LIKE $3;

-- name: ListInvestmentPlanHistory :many
SELECT
    id, key, "userId", location, metadata, "metricsData", "investorProfile", "lastAccessedAt"
FROM analysis_cache
WHERE "userId" = $1
    AND feature = 'investment_planning'
    AND "supersededBy" IS NULL
    AND (sqlc.narg('search')::text IS NULL OR sqlc.narg('search') = '' OR location ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY "lastAccessedAt" DESC
LIMIT sqlc.arg('lim') OFFSET sqlc.arg('off');

-- name: CountInvestmentPlanHistory :one
SELECT COUNT(*)
FROM analysis_cache
WHERE "userId" = $1
    AND feature = 'investment_planning'
    AND "supersededBy" IS NULL
    AND (sqlc.narg('search')::text IS NULL OR sqlc.narg('search') = '' OR location ILIKE '%' || sqlc.narg('search') || '%');

-- name: ListMarketAnalysisHistory :many
SELECT
    id, key, location, content, "metricsData", "narrativeData", "lastAccessedAt", "fullReport"
FROM analysis_cache
WHERE "userId" = $1
    AND feature IN ('dual_agent_market_analysis', 'market_analysis_v2')
    AND "supersededBy" IS NULL
    AND (sqlc.narg('search')::text IS NULL OR sqlc.narg('search') = '' OR location ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY "lastAccessedAt" DESC
LIMIT sqlc.arg('lim') OFFSET sqlc.arg('off');

-- name: CountMarketAnalysisHistory :one
SELECT COUNT(*)
FROM analysis_cache
WHERE "userId" = $1
    AND feature IN ('dual_agent_market_analysis', 'market_analysis_v2')
    AND "supersededBy" IS NULL
    AND (sqlc.narg('search')::text IS NULL OR sqlc.narg('search') = '' OR location ILIKE '%' || sqlc.narg('search') || '%');

-- name: GetLatestMarketAnalysisByLocation :one
SELECT content, "metricsData", "narrativeData", "lastAccessedAt"
FROM analysis_cache
WHERE "userId" = $1
    AND feature = 'dual_agent_market_analysis'
    AND location ILIKE $2
    AND "supersededBy" IS NULL
ORDER BY "lastAccessedAt" DESC
LIMIT 1;

-- name: CountCacheByUserFeatureActive :one
SELECT COUNT(*) FROM analysis_cache
WHERE "userId" = $1 AND feature = $2
    AND "supersededBy" IS NULL AND "expiresAt" > NOW();

-- name: ListCacheByUserFeatureActive :many
SELECT id, key, location, content, "lastAccessedAt"
FROM analysis_cache
WHERE "userId" = $1 AND feature = $2
    AND "supersededBy" IS NULL AND "expiresAt" > NOW()
ORDER BY "lastAccessedAt" DESC
LIMIT $3 OFFSET $4;

-- name: UpdateCacheAccessByUserAndKey :exec
UPDATE analysis_cache
SET "lastAccessedAt" = NOW(), "accessCount" = "accessCount" + 1
WHERE "userId" = $1 AND key = $2;

-- name: GetActiveTrendCacheContent :one
SELECT content FROM analysis_cache
WHERE "userId" = $1 AND key = $2
    AND feature = 'market_trends'
    AND "supersededBy" IS NULL
    AND "expiresAt" > NOW();

-- name: DeleteAiResponseCacheByUserAndKey :exec
DELETE FROM analysis_cache WHERE "userId" = $1 AND key = $2;

-- name: DeleteAiResponseCacheByUserAndFeature :exec
DELETE FROM analysis_cache WHERE "userId" = $1 AND feature = $2;

-- Market Trends queries (ADR-083 Phase R2)

-- name: ListTrendsHistory :many
SELECT id, key, location, content, "lastAccessedAt"
FROM analysis_cache
WHERE "userId" = $1
    AND feature = 'market_trends'
    AND "supersededBy" IS NULL
    AND "expiresAt" > NOW()
ORDER BY "lastAccessedAt" DESC
LIMIT $2 OFFSET $3;

-- name: CountTrendsHistory :one
SELECT COUNT(*)
FROM analysis_cache
WHERE "userId" = $1
    AND feature = 'market_trends'
    AND "supersededBy" IS NULL
    AND "expiresAt" > NOW();

-- name: UpsertTrendCache :exec
INSERT INTO analysis_cache (
    id, key, "userId", location, feature, content, "fullReport",
    "expiresAt", "createdAt", "lastAccessedAt", metadata
)
VALUES ($1, $2, $3, $4, 'market_trends', $5, $6, $7, NOW(), NOW(), '{}')
ON CONFLICT (key) DO UPDATE
SET content = EXCLUDED.content,
    "fullReport" = EXCLUDED."fullReport",
    "expiresAt" = EXCLUDED."expiresAt",
    "lastAccessedAt" = NOW();

-- name: UpdateTrendLastAccessed :exec
UPDATE analysis_cache
SET "lastAccessedAt" = NOW()
WHERE key = $1 AND feature = 'market_trends';

-- name: UpdateTrendAccessTracking :exec
UPDATE analysis_cache
SET "lastAccessedAt" = NOW(), "accessCount" = "accessCount" + 1
WHERE "userId" = $1 AND key = $2;
