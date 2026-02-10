-- Market Pulse Queries
-- Public-facing queries for the sign-in page market pulse display.
-- Returns high-quality cities and counts for the live market ticker.

-- name: GetMarketPulseTickerCities :many
-- Returns high-quality cities for the market pulse ticker display
SELECT city, state, median_home_price, price_yoy_change, median_rent, cap_rate
FROM city_market_cache
WHERE data_quality_score >= 70
  AND median_home_price > 0
  AND price_yoy_change IS NOT NULL
ORDER BY data_quality_score DESC, median_home_price DESC
LIMIT 8;

-- name: GetMarketPulseCounts :one
-- Returns total counts of tracked cities and metros
SELECT
  (SELECT COUNT(*) FROM city_market_cache) AS city_count,
  (SELECT COUNT(DISTINCT metro_region_id) FROM metro_time_series) AS metro_count;

-- name: GetSparklineMetros :many
-- Returns metros that have ZHVI data (for daily-rotating sparkline selection).
-- Orders by metro_name for deterministic rotation.
SELECT metro_name, state_name, zhvi_data
FROM metro_time_series
WHERE zhvi_data IS NOT NULL
ORDER BY metro_name;
