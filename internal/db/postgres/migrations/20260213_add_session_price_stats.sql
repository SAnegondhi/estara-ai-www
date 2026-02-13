-- Migration: 20260213_add_session_price_stats
-- Description: Adds median_price and mode_price columns to discovery_sessions
-- Author: Claude
-- Date: 2026-02-13

ALTER TABLE discovery_sessions ADD COLUMN IF NOT EXISTS median_price INTEGER;
ALTER TABLE discovery_sessions ADD COLUMN IF NOT EXISTS mode_price INTEGER;

-- Backfill existing sessions from discovery_session_properties
UPDATE discovery_sessions ds SET
    median_price = stats.median_price,
    mode_price = stats.mode_price
FROM (
    SELECT
        "discoverySessionId",
        percentile_cont(0.5) WITHIN GROUP (ORDER BY price)::int AS median_price,
        mode() WITHIN GROUP (ORDER BY price) AS mode_price
    FROM discovery_session_properties
    GROUP BY "discoverySessionId"
) stats
WHERE ds.id = stats."discoverySessionId"
  AND ds.median_price IS NULL;
