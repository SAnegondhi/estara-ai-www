-- Schema for Metro Time Series data (Zillow ZHVI/ZORI data)
-- This is in the separate market database (MARKET_DATABASE_URL)

CREATE TABLE IF NOT EXISTS metro_timeseries (
    id SERIAL PRIMARY KEY,
    region_id INTEGER NOT NULL,
    region_name TEXT NOT NULL,
    region_type TEXT NOT NULL,  -- 'msa', 'city', 'zip'
    state_code TEXT,
    metro TEXT,
    date DATE NOT NULL,
    zhvi NUMERIC(12, 2),        -- Zillow Home Value Index
    zori NUMERIC(10, 2),        -- Zillow Observed Rent Index
    zhvi_forecast NUMERIC(12, 2),
    zori_forecast NUMERIC(10, 2),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_metro_ts_region_date
ON metro_timeseries(region_id, date);

CREATE INDEX IF NOT EXISTS idx_metro_ts_region_name
ON metro_timeseries(region_name, region_type);

CREATE INDEX IF NOT EXISTS idx_metro_ts_state
ON metro_timeseries(state_code);

CREATE INDEX IF NOT EXISTS idx_metro_ts_date
ON metro_timeseries(date);

-- City to metro mapping table
CREATE TABLE IF NOT EXISTS city_metro_mapping (
    id SERIAL PRIMARY KEY,
    city TEXT NOT NULL,
    state_code TEXT NOT NULL,
    metro_name TEXT NOT NULL,
    metro_region_id INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_city_metro_city_state
ON city_metro_mapping(city, state_code);

CREATE INDEX IF NOT EXISTS idx_city_metro_metro
ON city_metro_mapping(metro_name);
