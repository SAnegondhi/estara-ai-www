-- name: GetCacheByKey :one
SELECT * FROM analysis_cache
WHERE key = $1 AND "expiresAt" > NOW();

-- name: GetCacheByUserAndKey :one
SELECT * FROM analysis_cache
WHERE "userId" = $1 AND key = $2 AND "expiresAt" > NOW();

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

-- name: ListCacheByLocation :many
SELECT * FROM analysis_cache
WHERE location = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;
