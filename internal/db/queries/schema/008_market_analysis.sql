-- Schema: Market Analysis Reports (ADR-087)
-- Shared cache and history tracking for Market Intelligence

CREATE TABLE market_analysis_reports (
    id TEXT PRIMARY KEY,
    location TEXT NOT NULL,
    location_normalized TEXT NOT NULL,
    cache_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('completed', 'pending', 'failed')),
    error_message TEXT,
    generated_at TIMESTAMPTZ,
    accessed_count INT NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    data_freshness_date TIMESTAMPTZ,
    report_size_bytes INT,

    -- ADR-087 Phase 7: Data source versions (event-driven refresh)
    zhvi_version TEXT,
    zori_version TEXT,
    redfin_version TEXT,
    fred_version TEXT,
    census_version TEXT,
    ai_context_version TEXT,

    -- ADR-087 Phase 5: Location coordinates for proximity-based preloading
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION
);

CREATE TABLE user_market_analysis_access (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    report_id TEXT NOT NULL REFERENCES market_analysis_reports(id) ON DELETE CASCADE,
    accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, report_id)
);
