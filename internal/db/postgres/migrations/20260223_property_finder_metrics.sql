-- Migration: 20260223_property_finder_metrics
-- Description: Adds property_finder_metrics table for tracking search analytics
-- Author: Claude
-- Date: 2026-02-23

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

CREATE INDEX IF NOT EXISTS idx_property_finder_metrics_user_id ON property_finder_metrics(user_id);
CREATE INDEX IF NOT EXISTS idx_property_finder_metrics_created_at ON property_finder_metrics(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_property_finder_metrics_provider ON property_finder_metrics(provider_used);
