-- Market Database Schema
-- This schema must match the actual market database tables.
-- Used by sqlc to generate type-safe Go code.
--
-- IMPORTANT: If you get sqlc errors, compare this schema with the actual database:
--   psql "$MARKET_DATABASE_URL" -c "SELECT column_name, data_type FROM information_schema.columns WHERE table_name = 'table_name';"

-- Metro time series data (Zillow ZHVI, ZORI, forecasts)
CREATE TABLE metro_time_series (
    id SERIAL PRIMARY KEY,
    metro_region_id INTEGER NOT NULL UNIQUE,
    metro_name VARCHAR(255) NOT NULL,
    state_name VARCHAR(50),
    zhvi_data JSONB,              -- {"2024-01": 341000, "2024-02": 343500, ...}
    zori_data JSONB,              -- {"2024-01": 2100, "2024-02": 2150, ...}
    zhvf_data JSONB,              -- ZHVI Forecast data (NOT zhvi_forecast_data!)
    sales_count_data JSONB,
    days_on_market_data JSONB,
    market_heat_data JSONB,
    affordability_data JSONB,
    redfin_data JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX metro_time_series_metro_name_idx ON metro_time_series(metro_name);
CREATE INDEX metro_time_series_metro_region_id_idx ON metro_time_series(metro_region_id);
CREATE INDEX metro_time_series_state_name_idx ON metro_time_series(state_name);

-- City market cache (aggregated market data per city)
CREATE TABLE city_market_cache (
    id SERIAL PRIMARY KEY,
    location_key VARCHAR(255) NOT NULL UNIQUE,
    city VARCHAR(100) NOT NULL,
    state VARCHAR(2) NOT NULL,                  -- NOTE: Column is 'state', NOT 'state_code'
    metro_region_id INTEGER REFERENCES metro_time_series(metro_region_id) ON UPDATE CASCADE ON DELETE SET NULL,

    -- Price metrics
    median_home_price NUMERIC(12,2),            -- NOTE: NOT 'median_home_value'
    median_price_per_sqft NUMERIC(8,2),
    median_list_price NUMERIC(12,2),

    -- Sales metrics
    homes_sold INTEGER,
    inventory_count INTEGER,
    months_of_supply NUMERIC(5,2),
    median_days_on_market INTEGER,

    -- Growth metrics
    price_yoy_change NUMERIC(5,2),              -- NOTE: NOT 'yoy_change'
    forecast_growth NUMERIC(5,2),

    -- Rental metrics
    median_rent NUMERIC(10,2),
    rent_yoy_change NUMERIC(5,2),
    rental_yield NUMERIC(5,2),
    cap_rate NUMERIC(5,2),
    price_to_rent_ratio NUMERIC(6,2),
    vacancy_rate NUMERIC(5,2),

    -- HUD Fair Market Rent
    hud_fmr_0br NUMERIC(10,2),
    hud_fmr_1br NUMERIC(10,2),
    hud_fmr_2br NUMERIC(10,2),
    hud_fmr_3br NUMERIC(10,2),
    hud_fmr_4br NUMERIC(10,2),

    -- Demographics
    population INTEGER,
    population_growth_rate NUMERIC(5,2),
    median_household_income NUMERIC(12,2),
    unemployment_rate NUMERIC(5,2),
    employment_growth_rate NUMERIC(5,2),

    -- Market indicators
    market_heat_index NUMERIC(5,2),
    market_temperature VARCHAR(20),
    affordability_index NUMERIC(5,2),
    affordability_burden VARCHAR(20),
    price_to_income_ratio NUMERIC(5,2),

    -- Metadata
    data_sources JSONB NOT NULL,
    data_quality_score INTEGER NOT NULL,
    data_date VARCHAR(20) NOT NULL,
    last_updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,  -- NOTE: NOT 'updated_at'
    ttl_expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- AI estimation fields
    is_ai_estimated BOOLEAN NOT NULL DEFAULT false,
    ai_confidence_score INTEGER,
    ai_estimation_details JSONB,
    ai_estimated_at TIMESTAMPTZ
);

CREATE INDEX city_market_cache_city_state_idx ON city_market_cache(city, state);
CREATE INDEX city_market_cache_state_idx ON city_market_cache(state);
CREATE INDEX city_market_cache_metro_region_id_idx ON city_market_cache(metro_region_id);
CREATE INDEX city_market_cache_last_updated_idx ON city_market_cache(last_updated);
CREATE INDEX city_market_cache_data_quality_score_idx ON city_market_cache(data_quality_score);
CREATE INDEX city_market_cache_is_ai_estimated_idx ON city_market_cache(is_ai_estimated);
CREATE INDEX city_market_cache_ttl_expires_at_idx ON city_market_cache(ttl_expires_at);

-- ============================================================================
-- CITY TIME SERIES (ADR-075: city-level ZHVI/ZORI time-series)
-- ============================================================================
CREATE TABLE city_time_series (
    id SERIAL PRIMARY KEY,
    city_region_id INTEGER NOT NULL UNIQUE,
    city_name VARCHAR(255) NOT NULL,
    state_name VARCHAR(50),
    metro_region_id INTEGER,
    zhvi_data JSONB,       -- {"2024-01": 341000, ...}
    zori_data JSONB,
    redfin_data JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX city_time_series_city_name_idx ON city_time_series(city_name);
CREATE INDEX city_time_series_state_name_idx ON city_time_series(state_name);
CREATE INDEX city_time_series_metro_region_id_idx ON city_time_series(metro_region_id);

-- ============================================================================
-- STATE TIME SERIES (ADR-075: state-level ZHVI/Redfin time-series)
-- ============================================================================
CREATE TABLE state_time_series (
    id SERIAL PRIMARY KEY,
    state_region_id INTEGER NOT NULL UNIQUE,
    state_code VARCHAR(2) NOT NULL UNIQUE,
    state_name VARCHAR(50) NOT NULL,
    zhvi_data JSONB,       -- {"2024-01": 341000, ...}
    zori_data JSONB,
    zhvf_data JSONB,
    sales_count_data JSONB,
    days_on_market_data JSONB,
    affordability_data JSONB,
    redfin_data JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX state_time_series_state_name_idx ON state_time_series(state_name);

-- ============================================================================
-- ZIP TIME SERIES (ADR-075: zip-level ZHVI/ZORI time-series)
-- ============================================================================
CREATE TABLE zip_time_series (
    id SERIAL PRIMARY KEY,
    zip_code VARCHAR(10) NOT NULL UNIQUE,
    city VARCHAR(100),
    state VARCHAR(2),
    county VARCHAR(100),
    metro_region_id INTEGER,
    zhvi_data JSONB,
    zori_data JSONB,
    redfin_data JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX zip_time_series_state_idx ON zip_time_series(state);
CREATE INDEX zip_time_series_metro_region_id_idx ON zip_time_series(metro_region_id);

-- ============================================================================
-- COUNTY TIME SERIES (ADR-075: county-level Redfin data)
-- ============================================================================
CREATE TABLE county_time_series (
    id SERIAL PRIMARY KEY,
    county_region_id INTEGER NOT NULL UNIQUE,
    county_fips VARCHAR(5) UNIQUE,
    county_name VARCHAR(100) NOT NULL,
    state_code VARCHAR(2) NOT NULL,
    state_name VARCHAR(50),
    metro_region_id INTEGER,
    zhvi_data JSONB,
    zori_data JSONB,
    zhvf_data JSONB,
    sales_count_data JSONB,
    days_on_market_data JSONB,
    redfin_data JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX county_time_series_state_code_idx ON county_time_series(state_code);
CREATE INDEX county_time_series_metro_region_id_idx ON county_time_series(metro_region_id);
