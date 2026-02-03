-- ADR-064: AI Scoring Cache
-- Caches AI property scoring results to avoid redundant Claude API calls
-- when properties are served from cache (per ADR-061)

CREATE TABLE IF NOT EXISTS ai_scoring_cache (
    id SERIAL PRIMARY KEY,
    cache_key TEXT UNIQUE NOT NULL,           -- "ai_score:{properties_hash}:{strategy}:{risk_tolerance}"
    properties_hash TEXT NOT NULL,             -- SHA256 hash of sorted property IDs
    strategy TEXT NOT NULL,                    -- Investment strategy
    risk_tolerance TEXT NOT NULL,              -- Investor risk tolerance
    scored_properties JSONB NOT NULL,          -- Array of ScoredProperty
    property_count INTEGER NOT NULL,           -- Number of properties scored
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP(3) NOT NULL           -- 24-hour TTL (matches property cache)
);

CREATE INDEX IF NOT EXISTS idx_ai_scoring_cache_key ON ai_scoring_cache(cache_key);
CREATE INDEX IF NOT EXISTS idx_ai_scoring_cache_expires ON ai_scoring_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_ai_scoring_cache_hash ON ai_scoring_cache(properties_hash);
