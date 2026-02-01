-- Market Database Queries
-- These queries are validated against the schema at compile time by sqlc.
-- If column names don't match, sqlc will fail to generate code.

-- ============================================================================
-- CITY MARKET CACHE QUERIES
-- ============================================================================

-- name: GetCityMarketData :one
-- Get market data for a specific city, joined with metro for metro name
SELECT
    c.id,
    c.location_key,
    c.city,
    c.state,
    c.metro_region_id,
    COALESCE(m.metro_name, '') AS metro_name,
    c.median_home_price,
    c.median_price_per_sqft,
    c.median_list_price,
    c.homes_sold,
    c.inventory_count,
    c.months_of_supply,
    c.median_days_on_market,
    c.price_yoy_change,
    c.forecast_growth,
    c.median_rent,
    c.rent_yoy_change,
    c.rental_yield,
    c.cap_rate,
    c.price_to_rent_ratio,
    c.vacancy_rate,
    c.population,
    c.median_household_income,
    c.unemployment_rate,
    c.market_heat_index,
    c.market_temperature,
    c.affordability_index,
    c.data_sources,
    c.data_quality_score,
    c.data_date,
    c.last_updated,
    c.is_ai_estimated,
    c.ai_confidence_score
FROM city_market_cache c
LEFT JOIN metro_time_series m ON c.metro_region_id = m.metro_region_id
WHERE c.city ILIKE $1 AND c.state = $2
LIMIT 1;

-- name: GetCityMetroMapping :one
-- Get the metro mapping for a city (for fallback to metro-level data)
SELECT
    c.city,
    c.state,
    COALESCE(m.metro_name, '') AS metro_name,
    COALESCE(c.metro_region_id, 0) AS metro_region_id
FROM city_market_cache c
LEFT JOIN metro_time_series m ON c.metro_region_id = m.metro_region_id
WHERE c.city ILIKE $1 AND c.state = $2
LIMIT 1;

-- name: ListCitiesByState :many
-- List all cities in a state with their market data
SELECT
    c.city,
    c.state,
    c.median_home_price,
    c.median_rent,
    c.cap_rate,
    c.data_quality_score,
    c.is_ai_estimated
FROM city_market_cache c
WHERE c.state = $1
ORDER BY c.city
LIMIT $2 OFFSET $3;

-- name: GetCitiesNeedingRefresh :many
-- Get cities where cache has expired
SELECT
    c.id,
    c.city,
    c.state,
    c.ttl_expires_at,
    c.last_updated
FROM city_market_cache c
WHERE c.ttl_expires_at < NOW()
ORDER BY c.ttl_expires_at
LIMIT $1;

-- ============================================================================
-- METRO TIME SERIES QUERIES
-- ============================================================================

-- name: GetMetroByName :one
-- Get metro data by name
SELECT
    id,
    metro_region_id,
    metro_name,
    state_name,
    zhvi_data,
    zori_data,
    zhvf_data,
    sales_count_data,
    days_on_market_data,
    market_heat_data,
    created_at,
    updated_at
FROM metro_time_series
WHERE metro_name ILIKE $1
LIMIT 1;

-- name: GetMetroByRegionID :one
-- Get metro data by region ID
SELECT
    id,
    metro_region_id,
    metro_name,
    state_name,
    zhvi_data,
    zori_data,
    zhvf_data,
    sales_count_data,
    days_on_market_data,
    market_heat_data,
    created_at,
    updated_at
FROM metro_time_series
WHERE metro_region_id = $1
LIMIT 1;

-- name: SearchMetros :many
-- Search metros by name pattern
SELECT DISTINCT
    metro_region_id,
    metro_name,
    state_name
FROM metro_time_series
WHERE metro_name ILIKE $1
ORDER BY metro_name
LIMIT $2;

-- name: ListMetrosByState :many
-- List all metros in a state
SELECT
    metro_region_id,
    metro_name,
    state_name
FROM metro_time_series
WHERE state_name = $1
ORDER BY metro_name;

-- name: GetMetroCount :one
-- Get total count of metros
SELECT COUNT(*) FROM metro_time_series;

-- name: GetCityCount :one
-- Get total count of cached cities
SELECT COUNT(*) FROM city_market_cache;

-- name: GetCityCountByState :one
-- Get count of cached cities in a state
SELECT COUNT(*) FROM city_market_cache WHERE state = $1;
