-- Migration: 20260220_migrate_analysis_cache_to_reports
-- Description: Migrate existing analysis_cache entries to market_analysis_reports (ADR-087)
-- Author: Claude Sonnet 4.5
-- Date: 2026-02-20

-- Migrate existing market analysis entries from analysis_cache to market_analysis_reports
INSERT INTO market_analysis_reports (
    id,
    location,
    location_normalized,
    cache_key,
    status,
    generated_at,
    accessed_count,
    last_accessed_at,
    created_at,
    updated_at
)
SELECT
    id,
    location,
    LOWER(REPLACE(REPLACE(location, ' ', '_'), ',', '')) as location_normalized,
    key as cache_key,
    'completed' as status,
    "createdAt" as generated_at,
    "accessCount" as accessed_count,
    "lastAccessedAt" as last_accessed_at,
    "createdAt" as created_at,
    "lastAccessedAt" as updated_at
FROM analysis_cache
WHERE feature IN ('market_analysis_v2', 'market_analysis', 'dual_agent_market_analysis')
  AND "supersededBy" IS NULL
  AND location IS NOT NULL
  AND location != ''
  AND id NOT IN (SELECT id FROM market_analysis_reports WHERE id IN (SELECT id FROM analysis_cache))
ON CONFLICT (cache_key) DO NOTHING;

-- Create user access records for each migrated report
-- Since we don't know exactly when each user accessed each report,
-- we'll create an access record for the userId that created it
INSERT INTO user_market_analysis_access (
    id,
    user_id,
    report_id,
    accessed_at
)
SELECT
    gen_random_uuid()::text,
    ac."userId",
    mar.id,
    ac."lastAccessedAt"
FROM analysis_cache ac
JOIN market_analysis_reports mar ON ac.key = mar.cache_key
WHERE ac.feature IN ('market_analysis_v2', 'market_analysis', 'dual_agent_market_analysis')
  AND ac."supersededBy" IS NULL
  AND ac.location IS NOT NULL
  AND ac.location != ''
ON CONFLICT (user_id, report_id) DO NOTHING;
