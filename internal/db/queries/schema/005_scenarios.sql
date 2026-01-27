-- Schema for scenarios and user preferences tables (matches Prisma schema)
-- These tables already exist in the database - this file is for sqlc code generation only

-- Scenarios table
CREATE TABLE IF NOT EXISTS scenarios (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    parameters JSONB NOT NULL,
    results JSONB,
    tags JSONB NOT NULL DEFAULT '[]',
    favorite BOOLEAN NOT NULL DEFAULT false,
    "hasAIParameters" BOOLEAN NOT NULL DEFAULT false,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "lastModified" TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scenario_user_modified ON scenarios("userId", "lastModified");
CREATE INDEX IF NOT EXISTS idx_scenario_user_favorite ON scenarios("userId", favorite);

-- User Analysis Preferences table
CREATE TABLE IF NOT EXISTS user_analysis_preferences (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "requestId" TEXT NOT NULL,
    hidden BOOLEAN NOT NULL DEFAULT false,
    favorited BOOLEAN NOT NULL DEFAULT false,
    notes TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    UNIQUE("userId", "requestId")
);

CREATE INDEX IF NOT EXISTS idx_user_analysis_pref_favorited ON user_analysis_preferences("userId", favorited);
CREATE INDEX IF NOT EXISTS idx_user_analysis_pref_request ON user_analysis_preferences("requestId");
