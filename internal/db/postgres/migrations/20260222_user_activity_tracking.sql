-- Migration: 20260222_user_activity_tracking
-- Description: Track user interactions with locations for intelligent preloading
-- Author: Claude
-- Date: 2026-02-22

-- Track all user location interactions (searches, views, generations)
CREATE TABLE IF NOT EXISTS user_location_activity (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    location TEXT NOT NULL,           -- "San Francisco, CA"
    activity_type TEXT NOT NULL,      -- "search", "view_analysis", "view_trends", "generate"
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_activity_user_created
ON user_location_activity(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_activity_location
ON user_location_activity(location);

-- Aggregate view counts per location per user
CREATE TABLE IF NOT EXISTS user_location_preferences (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    location TEXT NOT NULL,
    view_count INT NOT NULL DEFAULT 1,
    last_viewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, location)
);

CREATE INDEX IF NOT EXISTS idx_user_prefs_user_views
ON user_location_preferences(user_id, view_count DESC, last_viewed_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_prefs_location
ON user_location_preferences(location);
