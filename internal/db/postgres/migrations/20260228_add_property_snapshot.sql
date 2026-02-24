-- Migration: 20260228_add_property_snapshot
-- Description: Adds property_snapshot JSONB to v2_evaluations for full enriched property persistence.
--
-- Design decision (documented in ADR-093):
--   Complete record storage vs. just IDs (speed vs. memory trade-off):
--   - IDs only: minimal storage, requires L0/L1/L2 cache to be alive for re-runs
--   - Full records: ~2-5KB per evaluation, durable independent of cache lifetime
--   Current approach: L0/L1/L2 cache (Redis + ai_scoring_cache + discovery_session_properties)
--   stores full records. property_snapshot provides DB-durable fallback.
--   Backfill: written lazily from discovery_session_properties on first ListEvaluations call.
--
-- Author: Claude
-- Date: 2026-02-28

ALTER TABLE v2_evaluations ADD COLUMN IF NOT EXISTS property_snapshot JSONB;

COMMENT ON COLUMN v2_evaluations.property_snapshot IS
    'Full enriched property data at evaluation time: estimatedRent, capRateMin/Max, '
    'yearBuilt, imageUrl, listingSearchUrl, googleSearchUrl, latitude, longitude. '
    'Populated lazily from discovery_session_properties on first frontier use. '
    'Provides DB-durable fallback independent of L0/L1/L2 cache lifetime. '
    'See ADR-093 Property Persistence section for design decision rationale.';
