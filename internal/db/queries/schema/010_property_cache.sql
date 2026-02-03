-- Property Cache Schema
-- ADR-061: Size-based FIFO cache for individual property reads
-- Replaces TTL-based caching with size-limited cache to control resource usage

CREATE TABLE IF NOT EXISTS property_cache (
    id SERIAL PRIMARY KEY,
    cache_key TEXT UNIQUE NOT NULL,           -- "property_read:{provider}:{propertyID}"
    provider TEXT NOT NULL,                    -- "hasdata", "brightdata", etc.
    property_id TEXT NOT NULL,                 -- External property ID from provider
    content JSONB NOT NULL,                    -- Full property data (providers.Property)
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

-- Comment on table purpose
COMMENT ON TABLE property_cache IS 'Size-based FIFO cache for individual property details (ADR-061)';
COMMENT ON COLUMN property_cache.cache_key IS 'Unique cache key in format property_read:{provider}:{propertyID}';
COMMENT ON COLUMN property_cache.content IS 'Full property data as JSONB (providers.Property struct)';
COMMENT ON COLUMN property_cache.access_count IS 'Number of times this cache entry has been read';
