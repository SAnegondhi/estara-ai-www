-- Property Finder Metrics tracking (for sqlc validation)

CREATE TABLE IF NOT EXISTS property_finder_metrics (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    session_id TEXT,
    provider_used TEXT,
    providers_attempted JSONB NOT NULL DEFAULT '[]',
    cache_hit BOOLEAN NOT NULL DEFAULT false,
    result_count INTEGER NOT NULL DEFAULT 0,
    search_time_ms INTEGER,
    total_time_ms INTEGER,
    location TEXT,
    search_type TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
