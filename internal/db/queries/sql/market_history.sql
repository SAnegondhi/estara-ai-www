-- ADR-088 Phase 1: Market History Queries
-- CRUD operations for market historical data

-- name: GetMarketHistory :one
SELECT market_id, historical_peak_drop, last_updated, created_at
FROM market_history
WHERE market_id = $1;

-- name: ListMarketHistory :many
SELECT market_id, historical_peak_drop, last_updated, created_at
FROM market_history
ORDER BY market_id;

-- name: UpsertMarketHistory :exec
INSERT INTO market_history (market_id, historical_peak_drop, last_updated, created_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (market_id)
DO UPDATE SET
    historical_peak_drop = EXCLUDED.historical_peak_drop,
    last_updated = EXCLUDED.last_updated;

-- name: ListStaleMarketHistory :many
-- Get markets that haven't been updated in 7+ days
SELECT market_id, historical_peak_drop, last_updated, created_at
FROM market_history
WHERE last_updated < NOW() - INTERVAL '7 days'
ORDER BY last_updated ASC;

-- name: CountMarketHistory :one
SELECT COUNT(*) FROM market_history;

-- name: DeleteMarketHistory :exec
DELETE FROM market_history WHERE market_id = $1;
