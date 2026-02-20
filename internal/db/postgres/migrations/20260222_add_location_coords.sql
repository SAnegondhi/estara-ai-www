-- Migration: 20260222_add_location_coords
-- Description: Add latitude/longitude coordinates to market analysis reports for proximity queries
-- Author: Claude
-- Date: 2026-02-22

-- Add latitude/longitude to market_analysis_reports
ALTER TABLE market_analysis_reports
ADD COLUMN IF NOT EXISTS latitude DOUBLE PRECISION,
ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION;

-- Create spatial index for proximity queries
CREATE INDEX IF NOT EXISTS idx_market_analysis_reports_coords
ON market_analysis_reports(latitude, longitude)
WHERE latitude IS NOT NULL AND longitude IS NOT NULL;

-- Note: Coordinates will be backfilled from city_states table in market DB
-- This will be handled by a one-time data migration script or during report generation
