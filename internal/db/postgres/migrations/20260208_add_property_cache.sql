-- Migration: 20260208_add_property_cache
-- Description: Adds property_cache table for L2 caching of individual property reads
--              Part of ADR-061 (Property Cache Strategy Optimization)
-- Author: Claude
-- Date: 2026-02-08

-- Size-based FIFO cache for individual property details
-- Replaces TTL-based caching with size-limited cache to control resource usage
CREATE TABLE IF NOT EXISTS property_cache (
    id SERIAL PRIMARY KEY,
    cache_key TEXT UNIQUE NOT NULL,
    provider TEXT NOT NULL,
    property_id TEXT NOT NULL,
    content JSONB NOT NULL,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_accessed_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    access_count INTEGER NOT NULL DEFAULT 0
);

-- Index for cache key lookups (primary access pattern)
CREATE INDEX IF NOT EXISTS idx_property_cache_key ON property_cache(cache_key);

-- Index for FIFO eviction (oldest first by created_at)
CREATE INDEX IF NOT EXISTS idx_property_cache_created ON property_cache(created_at);

-- Index for provider + property_id lookups
CREATE INDEX IF NOT EXISTS idx_property_cache_provider_property ON property_cache(provider, property_id);

COMMENT ON TABLE property_cache IS 'Size-based FIFO cache for individual property details (ADR-061)';
