-- ADR-088 Phase 1: Market History Schema
-- Stores 20-year historical market data for stress test calibration

CREATE TABLE IF NOT EXISTS market_history (
    market_id TEXT PRIMARY KEY,
    historical_peak_drop DOUBLE PRECISION,
    last_updated TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_market_history_updated ON market_history(last_updated);
