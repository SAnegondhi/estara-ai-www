-- Migration: 20260131_add_discovery_session_evaluations
-- Description: Adds discovery_session_evaluations table to store evaluation results
--              as part of discovery sessions for later retrieval
-- Author: Claude
-- Date: 2026-01-31

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

-- Indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_session_evaluations_session ON discovery_session_evaluations("discoverySessionId");
CREATE INDEX IF NOT EXISTS idx_session_evaluations_property ON discovery_session_evaluations("propertyId");
