-- ADR-088 Phase 0: Scenario Planning v2.1 Schema
-- Creates foundation tables for efficient frontier, Monte Carlo simulation, and market history

-- ============================================================================
-- Market History Table
-- Stores 20-year historical data for stress test calibration
-- ============================================================================
CREATE TABLE IF NOT EXISTS market_history (
    market_id TEXT PRIMARY KEY,                   -- "Austin, TX"
    historical_peak_drop DOUBLE PRECISION,        -- Worst 12-month ZHVI return in 20yr window
    last_updated TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_market_history_updated ON market_history(last_updated);

COMMENT ON TABLE market_history IS 'Historical market data for stress test calibration (ADR-088)';
COMMENT ON COLUMN market_history.historical_peak_drop IS 'Worst 12-month return from 20-year ZHVI data';

-- ============================================================================
-- Frontier Points Table
-- Stores efficient frontier configurations (3-5 Pareto-optimal portfolios)
-- ============================================================================
CREATE TABLE IF NOT EXISTS frontier_points (
    id TEXT PRIMARY KEY,
    scenario_id TEXT NOT NULL,                    -- References parent scenario (not FK to avoid circular dependency)
    config_index INT NOT NULL,                    -- 0-4 for up to 5 frontier points
    property_ids TEXT[] NOT NULL,                 -- Array of property IDs in this config
    expected_return DOUBLE PRECISION NOT NULL,
    portfolio_volatility DOUBLE PRECISION NOT NULL, -- Markowitz analytical volatility
    sharpe_score DOUBLE PRECISION NOT NULL,
    concentration_index DOUBLE PRECISION NOT NULL,
    stress_test_equity BIGINT NOT NULL,           -- Final equity in stress scenario
    properties JSONB NOT NULL,                    -- Full property details + context-aware theses
    simulation_results JSONB NOT NULL,            -- Monte Carlo output (1,000 paths)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(scenario_id, config_index)
);

CREATE INDEX IF NOT EXISTS idx_frontier_scenario ON frontier_points(scenario_id);
CREATE INDEX IF NOT EXISTS idx_frontier_sharpe ON frontier_points(sharpe_score DESC);

COMMENT ON TABLE frontier_points IS 'Efficient frontier configurations for scenario planning (ADR-088)';
COMMENT ON COLUMN frontier_points.properties IS 'PropertyInPortfolio[] with context-aware theses';
COMMENT ON COLUMN frontier_points.simulation_results IS 'SimulationResults with 1,000 Monte Carlo paths';

-- ============================================================================
-- Simulation Paths Table
-- Stores individual Monte Carlo paths for replay/analysis
-- ============================================================================
CREATE TABLE IF NOT EXISTS simulation_paths (
    id TEXT PRIMARY KEY,
    frontier_point_id TEXT NOT NULL REFERENCES frontier_points(id) ON DELETE CASCADE,
    path_index INT NOT NULL,                      -- 0-999 for 1,000 paths
    yearly_outcomes JSONB NOT NULL,               -- YearOutcome[] for each year
    track_b_fired_year INT NOT NULL DEFAULT 0,    -- Year Track B triggered (0 = never)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(frontier_point_id, path_index)
);

CREATE INDEX IF NOT EXISTS idx_paths_frontier ON simulation_paths(frontier_point_id);
CREATE INDEX IF NOT EXISTS idx_paths_track_b ON simulation_paths(track_b_fired_year) WHERE track_b_fired_year > 0;

COMMENT ON TABLE simulation_paths IS 'Individual Monte Carlo paths for detailed analysis (ADR-088)';
COMMENT ON COLUMN simulation_paths.yearly_outcomes IS 'Array of YearOutcome (portfolio value, equity, cash flow, net worth)';

-- ============================================================================
-- Update Analysis Cache Schema
-- Add v2-specific fields to existing analysis_cache table
-- ============================================================================
DO $$
BEGIN
    -- Add frontier JSONB array (denormalized for speed)
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'analysis_cache' AND column_name = 'frontier'
    ) THEN
        ALTER TABLE analysis_cache ADD COLUMN frontier JSONB;
        COMMENT ON COLUMN analysis_cache.frontier IS 'FrontierPoint[] array (ADR-088 v2)';
    END IF;

    -- Add Monte Carlo seed for reproducibility
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'analysis_cache' AND column_name = 'monte_carlo_seed'
    ) THEN
        ALTER TABLE analysis_cache ADD COLUMN monte_carlo_seed BIGINT;
        COMMENT ON COLUMN analysis_cache.monte_carlo_seed IS 'Random seed for reproducible MC simulation (ADR-088)';
    END IF;

    -- Add portfolio context hash for Phase 2b cache
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'analysis_cache' AND column_name = 'portfolio_context_hash'
    ) THEN
        ALTER TABLE analysis_cache ADD COLUMN portfolio_context_hash TEXT;
        COMMENT ON COLUMN analysis_cache.portfolio_context_hash IS 'SHA-256 hash of portfolio properties for Phase 2b cache (ADR-088)';
    END IF;

    -- Add decision verdict
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'analysis_cache' AND column_name = 'decision_verdict'
    ) THEN
        ALTER TABLE analysis_cache ADD COLUMN decision_verdict JSONB;
        COMMENT ON COLUMN analysis_cache.decision_verdict IS 'DecisionVerdict object (PROCEED/PAUSE/REVISE) (ADR-088)';
    END IF;
END $$;

-- ============================================================================
-- Create indexes for v2 queries
-- ============================================================================
CREATE INDEX IF NOT EXISTS idx_analysis_cache_monte_carlo_seed
    ON analysis_cache(monte_carlo_seed) WHERE monte_carlo_seed IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_analysis_cache_portfolio_context
    ON analysis_cache(portfolio_context_hash) WHERE portfolio_context_hash IS NOT NULL;

-- ============================================================================
-- Migration Complete
-- ============================================================================
-- This migration is idempotent (safe to run multiple times)
-- Tables: market_history, frontier_points, simulation_paths
-- Updated: analysis_cache (added 4 columns)
