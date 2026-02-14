-- Migration: 20260215_add_ai_scoring_cache
-- Description: Adds ai_scoring_cache table for caching AI property scoring results
-- (ADR-064). Matches sqlc schema at queries/schema/011_ai_scoring_cache.sql.
-- Author: Claude
-- Date: 2026-02-15

CREATE TABLE IF NOT EXISTS ai_scoring_cache (
    id SERIAL PRIMARY KEY,
    cache_key TEXT UNIQUE NOT NULL,
    properties_hash TEXT NOT NULL,
    strategy TEXT NOT NULL,
    risk_tolerance TEXT NOT NULL,
    scored_properties JSONB NOT NULL,
    property_count INTEGER NOT NULL,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ai_scoring_cache_key ON ai_scoring_cache(cache_key);
CREATE INDEX IF NOT EXISTS idx_ai_scoring_cache_expires ON ai_scoring_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_ai_scoring_cache_hash ON ai_scoring_cache(properties_hash);
