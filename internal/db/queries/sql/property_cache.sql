-- Property Cache Queries
-- ADR-061: Size-based FIFO cache for individual property reads

-- name: GetPropertyFromCache :one
-- Retrieves a single property from cache by its cache key
SELECT * FROM property_cache WHERE cache_key = $1;

-- name: GetPropertyByProviderAndID :one
-- Retrieves a property by provider and property ID
SELECT * FROM property_cache WHERE provider = $1 AND property_id = $2;

-- name: UpsertPropertyCache :exec
-- Inserts or updates a property in the cache
INSERT INTO property_cache (cache_key, provider, property_id, content, created_at, last_accessed_at, access_count)
VALUES ($1, $2, $3, $4, NOW(), NOW(), 0)
ON CONFLICT (cache_key) DO UPDATE SET
    content = EXCLUDED.content,
    last_accessed_at = NOW(),
    access_count = property_cache.access_count + 1;

-- name: CountPropertyCache :one
-- Returns the total number of entries in the property cache
SELECT COUNT(*)::bigint FROM property_cache;

-- name: DeleteOldestPropertyCache :execrows
-- Deletes the oldest N entries from the cache (FIFO eviction)
DELETE FROM property_cache
WHERE id IN (
    SELECT id FROM property_cache
    ORDER BY created_at ASC
    LIMIT $1
);

-- name: UpdatePropertyCacheAccess :exec
-- Updates access timestamp and increments access count
UPDATE property_cache
SET last_accessed_at = NOW(), access_count = access_count + 1
WHERE cache_key = $1;

-- name: DeletePropertyFromCache :exec
-- Deletes a specific property from the cache
DELETE FROM property_cache WHERE cache_key = $1;

-- name: DeletePropertyCacheByProvider :execrows
-- Deletes all cache entries for a specific provider
DELETE FROM property_cache WHERE provider = $1;

-- name: GetPropertyCacheStats :one
-- Returns cache statistics for monitoring
SELECT
    COUNT(*)::bigint as total_entries,
    MIN(created_at) as oldest_entry,
    MAX(created_at) as newest_entry,
    SUM(access_count)::bigint as total_accesses
FROM property_cache;
