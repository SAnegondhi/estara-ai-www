-- Migration: 20260207_add_system_cache
-- Description: Adds system_cache table for L2 caching of economic data (FRED, BLS, Census)
--              Part of ADR-068 (Centralized Economic Data Service) and ADR-069 (Economic Data Integration)
-- Author: Claude
-- Date: 2026-02-07

-- System-wide cache for global data (FRED economic rates, BLS employment, Census demographics)
-- Unlike analysis_cache, this is not user-specific
-- Used by: FRED service, BLS service, Census service for L2 caching
CREATE TABLE IF NOT EXISTS system_cache (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for cleanup queries (expired entry removal)
CREATE INDEX IF NOT EXISTS idx_system_cache_expires_at ON system_cache(expires_at);

-- Comment for documentation
COMMENT ON TABLE system_cache IS 'System-wide cache for global data like FRED economic rates, BLS employment data, Census demographics. Not user-specific. Part of ADR-068/069.';
