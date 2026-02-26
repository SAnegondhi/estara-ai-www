-- Market Database Schema — idempotent initialisation
-- Applied automatically on startup by RunMarketSchema() in pool.go.
-- Uses CREATE TABLE IF NOT EXISTS so it is safe to run on every boot.
--
-- SOURCE OF TRUTH: internal/db/marketqueries/schema/market.sql
-- Keep in sync when adding new tables or columns.

CREATE TABLE IF NOT EXISTS metro_time_series (
    id SERIAL PRIMARY KEY,
    metro_region_id INTEGER NOT NULL UNIQUE,
    metro_name VARCHAR(255) NOT NULL,
    state_name VARCHAR(50),
    zhvi_data JSONB,
    zori_data JSONB,
    zhvf_data JSONB,
    sales_count_data JSONB,
    days_on_market_data JSONB,
    market_heat_data JSONB,
    affordability_data JSONB,
    redfin_data JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS metro_time_series_metro_name_idx ON metro_time_series(metro_name);
CREATE INDEX IF NOT EXISTS metro_time_series_metro_region_id_idx ON metro_time_series(metro_region_id);
CREATE INDEX IF NOT EXISTS metro_time_series_state_name_idx ON metro_time_series(state_name);

CREATE TABLE IF NOT EXISTS city_market_cache (
    id SERIAL PRIMARY KEY,
    location_key VARCHAR(255) NOT NULL UNIQUE,
    city VARCHAR(100) NOT NULL,
    state VARCHAR(2) NOT NULL,
    metro_region_id INTEGER REFERENCES metro_time_series(metro_region_id) ON UPDATE CASCADE ON DELETE SET NULL,
    median_home_price NUMERIC(12,2),
    median_price_per_sqft NUMERIC(8,2),
    median_list_price NUMERIC(12,2),
    homes_sold INTEGER,
    inventory_count INTEGER,
    months_of_supply NUMERIC(5,2),
    median_days_on_market INTEGER,
    price_yoy_change NUMERIC(5,2),
    forecast_growth NUMERIC(5,2),
    median_rent NUMERIC(10,2),
    rent_yoy_change NUMERIC(5,2),
    rental_yield NUMERIC(5,2),
    cap_rate NUMERIC(5,2),
    price_to_rent_ratio NUMERIC(6,2),
    vacancy_rate NUMERIC(5,2),
    hud_fmr_0br NUMERIC(10,2),
    hud_fmr_1br NUMERIC(10,2),
    hud_fmr_2br NUMERIC(10,2),
    hud_fmr_3br NUMERIC(10,2),
    hud_fmr_4br NUMERIC(10,2),
    population INTEGER,
    population_growth_rate NUMERIC(5,2),
    median_household_income NUMERIC(12,2),
    unemployment_rate NUMERIC(5,2),
    employment_growth_rate NUMERIC(5,2),
    market_heat_index NUMERIC(5,2),
    market_temperature VARCHAR(20),
    affordability_index NUMERIC(5,2),
    affordability_burden VARCHAR(20),
    price_to_income_ratio NUMERIC(5,2),
    data_sources JSONB NOT NULL,
    data_quality_score INTEGER NOT NULL,
    data_date VARCHAR(20) NOT NULL,
    last_updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ttl_expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_ai_estimated BOOLEAN NOT NULL DEFAULT false,
    ai_confidence_score INTEGER,
    ai_estimation_details JSONB,
    ai_estimated_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS city_market_cache_city_state_idx ON city_market_cache(city, state);
CREATE INDEX IF NOT EXISTS city_market_cache_state_idx ON city_market_cache(state);
CREATE INDEX IF NOT EXISTS city_market_cache_metro_region_id_idx ON city_market_cache(metro_region_id);
CREATE INDEX IF NOT EXISTS city_market_cache_last_updated_idx ON city_market_cache(last_updated);
CREATE INDEX IF NOT EXISTS city_market_cache_data_quality_score_idx ON city_market_cache(data_quality_score);
CREATE INDEX IF NOT EXISTS city_market_cache_is_ai_estimated_idx ON city_market_cache(is_ai_estimated);
CREATE INDEX IF NOT EXISTS city_market_cache_ttl_expires_at_idx ON city_market_cache(ttl_expires_at);

CREATE TABLE IF NOT EXISTS city_time_series (
    id SERIAL PRIMARY KEY,
    city_region_id INTEGER NOT NULL UNIQUE,
    city_name VARCHAR(255) NOT NULL,
    state_name VARCHAR(50),
    metro_region_id INTEGER,
    zhvi_data JSONB,
    zori_data JSONB,
    redfin_data JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS city_time_series_city_name_idx ON city_time_series(city_name);
CREATE INDEX IF NOT EXISTS city_time_series_state_name_idx ON city_time_series(state_name);
CREATE INDEX IF NOT EXISTS city_time_series_metro_region_id_idx ON city_time_series(metro_region_id);

CREATE TABLE IF NOT EXISTS state_time_series (
    id SERIAL PRIMARY KEY,
    state_region_id INTEGER NOT NULL UNIQUE,
    state_code VARCHAR(2) NOT NULL UNIQUE,
    state_name VARCHAR(50) NOT NULL,
    zhvi_data JSONB,
    zori_data JSONB,
    zhvf_data JSONB,
    sales_count_data JSONB,
    days_on_market_data JSONB,
    affordability_data JSONB,
    redfin_data JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS state_time_series_state_name_idx ON state_time_series(state_name);

CREATE TABLE IF NOT EXISTS zip_time_series (
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

CREATE INDEX IF NOT EXISTS zip_time_series_state_idx ON zip_time_series(state);
CREATE INDEX IF NOT EXISTS zip_time_series_metro_region_id_idx ON zip_time_series(metro_region_id);

CREATE TABLE IF NOT EXISTS county_time_series (
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

CREATE INDEX IF NOT EXISTS county_time_series_state_code_idx ON county_time_series(state_code);
CREATE INDEX IF NOT EXISTS county_time_series_metro_region_id_idx ON county_time_series(metro_region_id);

CREATE TABLE IF NOT EXISTS city_states (
    id TEXT PRIMARY KEY,
    external_id TEXT NOT NULL,
    city TEXT NOT NULL,
    city_ascii TEXT NOT NULL,
    city_lower TEXT NOT NULL,
    state_id TEXT NOT NULL,
    state_name TEXT NOT NULL,
    county_fips TEXT NOT NULL,
    county_name TEXT NOT NULL,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    population INTEGER NOT NULL DEFAULT 0,
    density DOUBLE PRECISION NOT NULL DEFAULT 0,
    source TEXT,
    military BOOLEAN NOT NULL DEFAULT false,
    incorporated BOOLEAN NOT NULL DEFAULT true,
    timezone TEXT,
    ranking INTEGER NOT NULL DEFAULT 0,
    zips_raw TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS city_states_city_lower_idx ON city_states(city_lower);
CREATE INDEX IF NOT EXISTS city_states_state_id_idx ON city_states(state_id);
CREATE INDEX IF NOT EXISTS city_states_population_idx ON city_states(population DESC);
CREATE INDEX IF NOT EXISTS city_states_city_lower_state_id_idx ON city_states(city_lower, state_id);
