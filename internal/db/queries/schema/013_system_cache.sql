-- System-wide cache for global data (FRED economic rates, etc.)
-- Unlike analysis_cache, this is not user-specific

CREATE TABLE IF NOT EXISTS system_cache (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for cleanup queries
CREATE INDEX IF NOT EXISTS idx_system_cache_expires_at ON system_cache(expires_at);

-- Comment for documentation
COMMENT ON TABLE system_cache IS 'System-wide cache for global data like FRED economic rates. Not user-specific.';
