-- ADR-064: AI Scoring Cache Queries

-- name: GetAIScoringCache :one
-- Retrieves cached AI scoring if not expired
SELECT * FROM ai_scoring_cache
WHERE cache_key = $1 AND expires_at > NOW();

-- name: UpsertAIScoringCache :exec
-- Inserts or updates AI scoring cache entry
INSERT INTO ai_scoring_cache (cache_key, properties_hash, strategy, risk_tolerance, scored_properties, property_count, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW(), $7)
ON CONFLICT (cache_key) DO UPDATE SET
    scored_properties = EXCLUDED.scored_properties,
    property_count = EXCLUDED.property_count,
    created_at = NOW(),
    expires_at = EXCLUDED.expires_at;

-- name: DeleteExpiredAIScoringCache :execrows
-- Removes expired cache entries (called by cron job)
DELETE FROM ai_scoring_cache WHERE expires_at < NOW();

-- name: CountAIScoringCache :one
-- Returns count of valid (non-expired) cache entries
SELECT COUNT(*) FROM ai_scoring_cache WHERE expires_at > NOW();

-- name: GetAIScoringCacheByHash :one
-- Retrieves cached AI scoring by properties hash (for debugging)
SELECT * FROM ai_scoring_cache
WHERE properties_hash = $1 AND expires_at > NOW()
ORDER BY created_at DESC
LIMIT 1;
