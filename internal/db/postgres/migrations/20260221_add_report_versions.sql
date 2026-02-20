-- Migration: 20260221_add_report_versions
-- Description: Adds data source version columns to market_analysis_reports (ADR-087 Phase 7)
-- Author: Claude
-- Date: 2026-02-21
--
-- ADR-087: Market Intelligence Event-Driven Refresh
-- Captures snapshot of data versions used when report was generated

-- Add version columns to market_analysis_reports
ALTER TABLE market_analysis_reports
ADD COLUMN IF NOT EXISTS zhvi_version TEXT,
ADD COLUMN IF NOT EXISTS zori_version TEXT,
ADD COLUMN IF NOT EXISTS redfin_version TEXT,
ADD COLUMN IF NOT EXISTS fred_version TEXT,
ADD COLUMN IF NOT EXISTS census_version TEXT,
ADD COLUMN IF NOT EXISTS ai_context_version TEXT;

-- Add index for staleness detection (find reports with outdated versions)
CREATE INDEX IF NOT EXISTS idx_reports_zhvi_version ON market_analysis_reports(zhvi_version);
CREATE INDEX IF NOT EXISTS idx_reports_zori_version ON market_analysis_reports(zori_version);
CREATE INDEX IF NOT EXISTS idx_reports_redfin_version ON market_analysis_reports(redfin_version);

-- Backfill existing reports with current versions (assume all reports used current data)
UPDATE market_analysis_reports
SET zhvi_version = '2026-02',
    zori_version = '2026-02',
    redfin_version = '2026-02-15',
    fred_version = '2026-01',
    census_version = '2025-Q4',
    ai_context_version = COALESCE(ai_context_version, '2026-02-21')
WHERE zhvi_version IS NULL;
