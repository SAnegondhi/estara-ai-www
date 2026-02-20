-- Schema: User Activity Tracking (ADR-087 Phase 5)
-- Track user interactions with locations for intelligent preloading

CREATE TABLE user_location_activity (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    location TEXT NOT NULL,
    activity_type TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE user_location_preferences (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    location TEXT NOT NULL,
    view_count INT NOT NULL DEFAULT 1,
    last_viewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, location)
);
