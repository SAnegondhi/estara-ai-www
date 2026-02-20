-- Migration: 20260221_data_source_versions
-- Description: Creates data_source_versions table for event-driven refresh (ADR-087 Phase 7)
-- Author: Claude
-- Date: 2026-02-21
--
-- ADR-087: Market Intelligence Event-Driven Refresh
-- Tracks version numbers for each data source to detect staleness

CREATE TABLE IF NOT EXISTS data_source_versions (
    source TEXT PRIMARY KEY,              -- 'zhvi', 'zori', 'redfin', 'fred', 'census', 'ai_context'
    version TEXT NOT NULL,                -- Version identifier (date or version number)
    last_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),    -- When this version was imported
    import_job_id TEXT,                   -- Reference to import job (if applicable)
    metadata JSONB DEFAULT '{}'::JSONB,   -- Additional import metadata (file size, record count, etc.)

    -- Indexes
    CONSTRAINT valid_source CHECK (source IN ('zhvi', 'zori', 'redfin', 'fred', 'census', 'ai_context'))
);

CREATE INDEX IF NOT EXISTS idx_data_source_updated ON data_source_versions(last_updated DESC);

-- Populate with initial versions (current data state)
-- ZHVI/ZORI: February 2026 (last Zillow import)
-- Redfin: 2026-02-15 (last Redfin import from ADR-075)
-- FRED: January 2026 (last FRED import)
-- Census: 2025-Q4 (quarterly update)
-- AI Context: Today (web search context)

INSERT INTO data_source_versions (source, version, last_updated, metadata) VALUES
('zhvi', '2026-02', NOW(), '{"description": "Zillow Home Values - February 2026"}'::JSONB),
('zori', '2026-02', NOW(), '{"description": "Zillow Rents - February 2026"}'::JSONB),
('redfin', '2026-02-15', NOW(), '{"description": "Redfin Market Data - Weekly Update"}'::JSONB),
('fred', '2026-01', NOW(), '{"description": "FRED Economic Data - January 2026"}'::JSONB),
('census', '2025-Q4', NOW(), '{"description": "Census/BLS Demographics - Q4 2025"}'::JSONB),
('ai_context', '2026-02-21', NOW(), '{"description": "AI Market Context - Current"}'::JSONB)
ON CONFLICT (source) DO NOTHING;
