-- Migration: 20260220_market_analysis_reports
-- Description: ADR-087 - Market Intelligence shared cache and history tracking
-- Author: Claude (ADR-087)
-- Date: 2026-02-20

-- Market analysis reports table (shared across all users)
CREATE TABLE IF NOT EXISTS market_analysis_reports (
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

    -- Metadata for display
    data_freshness_date TIMESTAMPTZ,
    report_size_bytes INT
);

CREATE INDEX IF NOT EXISTS idx_market_reports_location ON market_analysis_reports(location_normalized);
CREATE INDEX IF NOT EXISTS idx_market_reports_generated ON market_analysis_reports(generated_at DESC);
CREATE INDEX IF NOT EXISTS idx_market_reports_cache_key ON market_analysis_reports(cache_key);
CREATE INDEX IF NOT EXISTS idx_market_reports_status ON market_analysis_reports(status);

-- User access tracking (personalized "Recent Analyses" view)
CREATE TABLE IF NOT EXISTS user_market_analysis_access (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    report_id TEXT NOT NULL REFERENCES market_analysis_reports(id) ON DELETE CASCADE,
    accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(user_id, report_id)
);

CREATE INDEX IF NOT EXISTS idx_user_access_user_time ON user_market_analysis_access(user_id, accessed_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_access_report ON user_market_analysis_access(report_id);
