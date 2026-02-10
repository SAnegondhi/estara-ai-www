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

-- ============================================================================
-- AUTOCOMPLETE QUERIES (ADR-073: Market Intelligence)
-- ============================================================================

-- name: GetCitySnapshot :one
-- Get ALL fields from city_market_cache for a city (ADR-074: DATA_PAYLOAD builder)
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
    c.hud_fmr_0br,
    c.hud_fmr_1br,
    c.hud_fmr_2br,
    c.hud_fmr_3br,
    c.hud_fmr_4br,
    c.population,
    c.population_growth_rate,
    c.median_household_income,
    c.unemployment_rate,
    c.employment_growth_rate,
    c.market_heat_index,
    c.market_temperature,
    c.affordability_index,
    c.affordability_burden,
    c.price_to_income_ratio,
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

-- name: GetNationalTimeSeries :one
-- Get national aggregate ZHVI/ZORI from metro_time_series (metro_region_id=0)
-- Used for national benchmark comparison in DATA_PAYLOAD (ADR-074)
SELECT
    metro_region_id,
    metro_name,
    zhvi_data,
    zori_data,
    zhvf_data,
    updated_at
FROM metro_time_series
WHERE metro_region_id = 0
LIMIT 1;

-- name: SearchCities :many
-- Search cities by name for autocomplete
SELECT
    c.city,
    c.state,
    COALESCE(c.population, 0) AS population,
    COALESCE(m.metro_name, '') AS metro_name,
    CASE WHEN c.median_home_price IS NOT NULL THEN true ELSE false END AS has_market_data,
    CASE WHEN c.median_rent IS NOT NULL THEN true ELSE false END AS has_rental_data
FROM city_market_cache c
LEFT JOIN metro_time_series m ON c.metro_region_id = m.metro_region_id
WHERE c.city ILIKE $1
ORDER BY COALESCE(c.population, 0) DESC
LIMIT $2;

-- ============================================================================
-- METRO TIME SERIES UPSERT QUERIES (ADR-075: Market Data Import)
-- ============================================================================

-- name: UpsertMetroZHVI :exec
-- Upsert metro ZHVI time-series data
INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, zhvi_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    metro_name = EXCLUDED.metro_name,
    state_name = EXCLUDED.state_name,
    zhvi_data = EXCLUDED.zhvi_data,
    updated_at = NOW();

-- name: UpsertMetroZORI :exec
-- Upsert metro ZORI time-series data
INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, zori_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    metro_name = EXCLUDED.metro_name,
    state_name = EXCLUDED.state_name,
    zori_data = EXCLUDED.zori_data,
    updated_at = NOW();

-- name: UpsertMetroZHVF :exec
-- Upsert metro ZHVI forecast data
INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, zhvf_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    metro_name = EXCLUDED.metro_name,
    state_name = EXCLUDED.state_name,
    zhvf_data = EXCLUDED.zhvf_data,
    updated_at = NOW();

-- name: UpsertMetroMetrics :exec
-- Upsert metro sales/DOM/heat/affordability metrics
INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, sales_count_data, days_on_market_data, market_heat_data, affordability_data, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    sales_count_data = COALESCE(EXCLUDED.sales_count_data, metro_time_series.sales_count_data),
    days_on_market_data = COALESCE(EXCLUDED.days_on_market_data, metro_time_series.days_on_market_data),
    market_heat_data = COALESCE(EXCLUDED.market_heat_data, metro_time_series.market_heat_data),
    affordability_data = COALESCE(EXCLUDED.affordability_data, metro_time_series.affordability_data),
    updated_at = NOW();

-- name: UpsertMetroRedfin :exec
-- Upsert metro Redfin data
INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, redfin_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW();

-- name: GetAllMetroLookup :many
-- Get all metros for city→metro linking during import
SELECT metro_region_id, metro_name
FROM metro_time_series
ORDER BY metro_name;

-- ============================================================================
-- CITY TIME SERIES UPSERT QUERIES (ADR-075)
-- ============================================================================

-- name: UpsertCityZHVI :exec
-- Upsert city ZHVI time-series data
INSERT INTO city_time_series (city_region_id, city_name, state_name, metro_region_id, zhvi_data, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (city_region_id) DO UPDATE SET
    city_name = EXCLUDED.city_name,
    state_name = EXCLUDED.state_name,
    metro_region_id = EXCLUDED.metro_region_id,
    zhvi_data = EXCLUDED.zhvi_data,
    updated_at = NOW();

-- name: UpsertCityZORI :exec
-- Upsert city ZORI time-series data
INSERT INTO city_time_series (city_region_id, city_name, state_name, metro_region_id, zori_data, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (city_region_id) DO UPDATE SET
    city_name = EXCLUDED.city_name,
    state_name = EXCLUDED.state_name,
    metro_region_id = EXCLUDED.metro_region_id,
    zori_data = EXCLUDED.zori_data,
    updated_at = NOW();

-- name: UpsertCityRedfin :exec
-- Upsert city Redfin data (keyed by city_name + state since Redfin has no region IDs)
INSERT INTO city_time_series (city_region_id, city_name, state_name, redfin_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (city_region_id) DO UPDATE SET
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW();

-- name: GetCityTimeSeriesCount :one
-- Get total count of city time-series entries
SELECT COUNT(*) FROM city_time_series;

-- ============================================================================
-- STATE TIME SERIES UPSERT QUERIES (ADR-075)
-- ============================================================================

-- name: UpsertStateZHVI :exec
-- Upsert state ZHVI time-series data
INSERT INTO state_time_series (state_region_id, state_code, state_name, zhvi_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (state_code) DO UPDATE SET
    state_name = EXCLUDED.state_name,
    zhvi_data = EXCLUDED.zhvi_data,
    updated_at = NOW();

-- name: UpsertStateRedfin :exec
-- Upsert state Redfin data
INSERT INTO state_time_series (state_region_id, state_code, state_name, redfin_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (state_code) DO UPDATE SET
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW();

-- name: GetStateTimeSeriesCount :one
-- Get total count of state time-series entries
SELECT COUNT(*) FROM state_time_series;

-- name: GetAllStateZHVI :many
-- Get all state ZHVI data for national aggregation
SELECT state_region_id, state_code, state_name, zhvi_data
FROM state_time_series
WHERE zhvi_data IS NOT NULL;

-- ============================================================================
-- ZIP TIME SERIES UPSERT QUERIES (ADR-075)
-- ============================================================================

-- name: UpsertZipZHVI :exec
-- Upsert zip ZHVI time-series data
INSERT INTO zip_time_series (zip_code, city, state, county, metro_region_id, zhvi_data, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (zip_code) DO UPDATE SET
    city = EXCLUDED.city,
    state = EXCLUDED.state,
    county = EXCLUDED.county,
    metro_region_id = EXCLUDED.metro_region_id,
    zhvi_data = EXCLUDED.zhvi_data,
    updated_at = NOW();

-- name: UpsertZipZORI :exec
-- Upsert zip ZORI time-series data
INSERT INTO zip_time_series (zip_code, city, state, zhvi_data, zori_data, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (zip_code) DO UPDATE SET
    zori_data = EXCLUDED.zori_data,
    updated_at = NOW();

-- name: UpsertZipRedfin :exec
-- Upsert zip Redfin data
INSERT INTO zip_time_series (zip_code, state, redfin_data, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (zip_code) DO UPDATE SET
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW();

-- name: GetZipTimeSeriesCount :one
-- Get total count of zip time-series entries
SELECT COUNT(*) FROM zip_time_series;

-- ============================================================================
-- COUNTY TIME SERIES UPSERT QUERIES (ADR-075)
-- ============================================================================

-- name: UpsertCountyRedfin :exec
-- Upsert county Redfin data (keyed by county_region_id since county_fips may be null)
INSERT INTO county_time_series (county_region_id, county_fips, county_name, state_code, metro_region_id, redfin_data, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (county_region_id) DO UPDATE SET
    county_fips = COALESCE(EXCLUDED.county_fips, county_time_series.county_fips),
    county_name = EXCLUDED.county_name,
    state_code = EXCLUDED.state_code,
    metro_region_id = EXCLUDED.metro_region_id,
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW();

-- name: GetCountyTimeSeriesCount :one
-- Get total count of county time-series entries
SELECT COUNT(*) FROM county_time_series;

-- ============================================================================
-- STATUS QUERIES (ADR-075: Market data freshness)
-- ============================================================================

-- name: GetMetroFreshness :one
-- Get most recent metro update timestamp
SELECT MAX(updated_at) AS latest_update FROM metro_time_series;

-- name: GetCityTimeSeriesFreshness :one
-- Get most recent city time-series update timestamp
SELECT MAX(updated_at) AS latest_update FROM city_time_series;

-- name: GetStateFreshness :one
-- Get most recent state update timestamp
SELECT MAX(updated_at) AS latest_update FROM state_time_series;

-- name: GetZipFreshness :one
-- Get most recent zip update timestamp
SELECT MAX(updated_at) AS latest_update FROM zip_time_series;

-- name: GetCountyFreshness :one
-- Get most recent county update timestamp
SELECT MAX(updated_at) AS latest_update FROM county_time_series;
