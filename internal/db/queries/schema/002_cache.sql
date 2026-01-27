-- Schema for analysis_cache table (matches actual Neon database)
-- ADR-041: Consolidated cache table

CREATE TABLE IF NOT EXISTS analysis_cache (
    id TEXT PRIMARY KEY,
    key TEXT UNIQUE NOT NULL,
    "userId" TEXT NOT NULL,
    location TEXT NOT NULL,
    feature TEXT NOT NULL DEFAULT 'dual_agent_market_analysis',
    prompt TEXT,
    "promptHash" TEXT,
    complexity JSONB,
    "investorProfile" JSONB,
    "marketData" JSONB,
    content TEXT NOT NULL,
    "fullReport" TEXT,
    "metricsData" JSONB,
    "narrativeData" JSONB,
    metadata JSONB NOT NULL DEFAULT '{}',
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "expiresAt" TIMESTAMP(3) NOT NULL,
    "lastAccessedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "accessCount" INTEGER NOT NULL DEFAULT 0,
    "supersededBy" TEXT,
    "supersededAt" TIMESTAMP(3),
    "cacheHits" INTEGER NOT NULL DEFAULT 0,
    "generationCost" NUMERIC(10,6) NOT NULL DEFAULT 0,
    "savingsGenerated" NUMERIC(10,6) NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_analysis_cache_user_created ON analysis_cache("userId", "createdAt");
CREATE INDEX IF NOT EXISTS idx_analysis_cache_user_location ON analysis_cache("userId", location);
CREATE INDEX IF NOT EXISTS idx_analysis_cache_location ON analysis_cache(location);
CREATE INDEX IF NOT EXISTS idx_analysis_cache_feature ON analysis_cache(feature);
CREATE INDEX IF NOT EXISTS idx_analysis_cache_expires ON analysis_cache("expiresAt");
