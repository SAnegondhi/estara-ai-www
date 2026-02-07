-- System Cache Queries
-- For global data like FRED economic rates (not user-specific)

-- name: GetSystemCache :one
SELECT * FROM system_cache
WHERE key = $1 AND expires_at > NOW();

-- name: GetSystemCacheIgnoreExpiry :one
-- Used when we want stale data as fallback
SELECT * FROM system_cache
WHERE key = $1;

-- name: UpsertSystemCache :one
INSERT INTO system_cache (key, value, expires_at, created_at, updated_at)
VALUES ($1, $2, $3, NOW(), NOW())
ON CONFLICT (key) DO UPDATE SET
    value = EXCLUDED.value,
    expires_at = EXCLUDED.expires_at,
    updated_at = NOW()
RETURNING *;

-- name: DeleteSystemCache :exec
DELETE FROM system_cache WHERE key = $1;

-- name: DeleteExpiredSystemCache :execrows
DELETE FROM system_cache WHERE expires_at < NOW();

-- name: GetSystemCacheStats :one
SELECT
    COUNT(*) as total_entries,
    COUNT(*) FILTER (WHERE expires_at > NOW()) as valid_entries,
    COUNT(*) FILTER (WHERE expires_at <= NOW()) as expired_entries
FROM system_cache;
