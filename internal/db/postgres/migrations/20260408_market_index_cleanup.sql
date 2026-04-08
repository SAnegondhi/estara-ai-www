-- Migration: 20260604_market_index_cleanup
-- Description: Remove duplicate and superseded indexes from market database
-- Date: 2026-03-06
--
-- BACKGROUND
-- Audit of the Railway Market DB found two categories of redundant indexes:
--
-- 1. EXACT DUPLICATES (plain btree shadowing UNIQUE constraint on same column)
--    metro_time_series and state_time_series each have a plain index identical
--    to their UNIQUE constraint index — pure write overhead.
--
-- 2. SINGLE-COLUMN INDEXES SUPERSEDED BY COMPOSITES
--    PostgreSQL can use a composite index (a, b) to satisfy queries that
--    filter on just (a) — the leading column. Single-column indexes on the
--    same leading column are therefore redundant when a composite exists.
--
--    Dropped:                             Already covered by:
--    city_states_city_lower_idx           city_states_city_lower_state_id_idx (city_lower, state_id)
--    city_states_county_fips_idx          city_states_county_fips_city_lower_idx (county_fips, city_lower)
--    city_states_state_id_idx             city_states_state_id_city_lower_idx (state_id, city_lower)
--    location_counties_state_id_idx       location_counties_state_id_name_lower_idx (state_id, name_lower)
--    metro_city_mapping_metro_region_id_idx  metro_city_mapping_metro_region_id_city_name_state_code_key (UNIQUE)
--    location_zip_codes_zip_idx           location_zip_codes_zip_city_state_id_key (UNIQUE, zip leading)
--
-- NET SAVINGS: ~2.3 MB of index space, 8 fewer indexes to maintain on writes.
-- No FK indexes needed — market DB has no FK relationships.
--
-- NOTE: This migration runs against the MARKET database (MARKET_DATABASE_URL).
-- The migration runner in migrate.go runs against the MAIN database only.
-- Apply this manually or via a dedicated market migration runner.

-- =============================================================================
-- SECTION 1: Exact duplicates (plain index = UNIQUE constraint, same column)
-- =============================================================================

-- metro_time_series: keep UNIQUE metro_time_series_metro_region_id_key
DROP INDEX IF EXISTS metro_time_series_metro_region_id_idx;

-- state_time_series: keep UNIQUE state_time_series_state_code_key
DROP INDEX IF EXISTS state_time_series_state_code_idx;

-- =============================================================================
-- SECTION 2: Single-column indexes superseded by composite leading-column
-- =============================================================================

-- city_states: (city_lower) superseded by (city_lower, state_id)
DROP INDEX IF EXISTS city_states_city_lower_idx;

-- city_states: (county_fips) superseded by (county_fips, city_lower)
DROP INDEX IF EXISTS city_states_county_fips_idx;

-- city_states: (state_id) superseded by (state_id, city_lower)
DROP INDEX IF EXISTS city_states_state_id_idx;

-- location_counties: (state_id) superseded by (state_id, name_lower)
DROP INDEX IF EXISTS location_counties_state_id_idx;

-- metro_city_mapping: (metro_region_id) superseded by UNIQUE (metro_region_id, city_name, state_code)
DROP INDEX IF EXISTS metro_city_mapping_metro_region_id_idx;

-- location_zip_codes: (zip) superseded by UNIQUE (zip, city_state_id)
DROP INDEX IF EXISTS location_zip_codes_zip_idx;
