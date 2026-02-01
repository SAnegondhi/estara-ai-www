-- Discovery Sessions - Groups property searches with associated activities
-- Created: 2026-01-30

-- Discovery Sessions table
CREATE TABLE IF NOT EXISTS discovery_sessions (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Search context
    "searchCriteria" JSONB NOT NULL,
    location TEXT NOT NULL,
    "propertyCount" INTEGER NOT NULL DEFAULT 0,
    "cachedPropertyIds" TEXT[] NOT NULL DEFAULT '{}',

    -- User metadata
    name TEXT,
    notes TEXT,

    -- Status
    status TEXT NOT NULL DEFAULT 'ACTIVE',  -- ACTIVE, ARCHIVED

    -- Timestamps
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "lastAccessedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "archivedAt" TIMESTAMP(3),  -- When auto-archived (30 days)
    "expiresAt" TIMESTAMP(3),   -- When auto-deleted (180 days from creation)

    -- Metrics
    "chatSessionCount" INTEGER NOT NULL DEFAULT 0,
    "evaluationCount" INTEGER NOT NULL DEFAULT 0
);

-- Indexes for discovery_sessions
CREATE INDEX IF NOT EXISTS idx_discovery_session_user_created ON discovery_sessions("userId", "createdAt" DESC);
CREATE INDEX IF NOT EXISTS idx_discovery_session_user_status ON discovery_sessions("userId", status);
CREATE INDEX IF NOT EXISTS idx_discovery_session_status ON discovery_sessions(status);
CREATE INDEX IF NOT EXISTS idx_discovery_session_expires ON discovery_sessions("expiresAt");
CREATE INDEX IF NOT EXISTS idx_discovery_session_archived ON discovery_sessions("archivedAt") WHERE "archivedAt" IS NOT NULL;

-- Activity links table - Links AI Chat sessions and evaluations to discovery sessions
CREATE TABLE IF NOT EXISTS discovery_session_activities (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "activityType" TEXT NOT NULL,  -- 'CHAT_SESSION', 'EVALUATION'
    "activityId" TEXT NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE("discoverySessionId", "activityType", "activityId")
);

-- Indexes for discovery_session_activities
CREATE INDEX IF NOT EXISTS idx_activity_session ON discovery_session_activities("discoverySessionId");
CREATE INDEX IF NOT EXISTS idx_activity_type ON discovery_session_activities("activityType");

-- Discovery Session Properties - Stores full property data separately from session metadata
-- Properties have their own lifecycle and can be cleaned up independently
CREATE TABLE IF NOT EXISTS discovery_session_properties (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "listingId" TEXT NOT NULL,

    -- Property details
    address TEXT NOT NULL,
    city TEXT NOT NULL,
    state TEXT NOT NULL,
    "zipCode" TEXT,
    price INTEGER NOT NULL,
    "estimatedRent" INTEGER,
    "capRateMin" NUMERIC(5,2),
    "capRateMax" NUMERIC(5,2),
    beds INTEGER NOT NULL DEFAULT 0,
    baths NUMERIC(3,1) NOT NULL DEFAULT 0,
    sqft INTEGER,
    "yearBuilt" INTEGER,
    "propertyType" TEXT,
    "listingDate" TEXT,
    "daysOnMarket" INTEGER,
    "imageUrl" TEXT,
    "listingSearchUrl" TEXT,
    "googleSearchUrl" TEXT,
    latitude NUMERIC(10,7),
    longitude NUMERIC(10,7),

    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE("discoverySessionId", "listingId")
);

-- Indexes for discovery_session_properties
CREATE INDEX IF NOT EXISTS idx_session_properties_session ON discovery_session_properties("discoverySessionId");
CREATE INDEX IF NOT EXISTS idx_session_properties_listing ON discovery_session_properties("listingId");

-- Discovery Session Evaluations - Stores evaluation results as part of the discovery session
-- Each evaluation batch is a snapshot of metrics at evaluation time
CREATE TABLE IF NOT EXISTS discovery_session_evaluations (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "propertyId" TEXT NOT NULL,  -- References listingId from discovery_session_properties

    -- Property info snapshot (at evaluation time)
    address TEXT NOT NULL,
    price INTEGER NOT NULL,
    "estimatedRent" INTEGER,

    -- Scenario results (JSONB for flexibility)
    scenarios JSONB NOT NULL,  -- Contains conservative, base, optimistic metrics

    -- Derived values
    recommendation TEXT,  -- strong_buy, buy, hold, pass
    "riskLevel" TEXT,     -- low, medium, high
    score INTEGER,        -- 0-100

    -- Status
    status TEXT NOT NULL DEFAULT 'COMPLETED',  -- COMPLETED, FAILED

    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE("discoverySessionId", "propertyId")
);

-- Indexes for discovery_session_evaluations
CREATE INDEX IF NOT EXISTS idx_session_evaluations_session ON discovery_session_evaluations("discoverySessionId");
CREATE INDEX IF NOT EXISTS idx_session_evaluations_property ON discovery_session_evaluations("propertyId");
