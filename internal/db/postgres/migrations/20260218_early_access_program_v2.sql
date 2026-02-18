-- Migration: 20260218_early_access_program_v2
-- Description: Add primary_markets, key_investment_decisions, current_analytical_approach to early_access_requests
-- ADR-085

ALTER TABLE early_access_requests
    ADD COLUMN IF NOT EXISTS primary_markets              TEXT,
    ADD COLUMN IF NOT EXISTS key_investment_decisions     TEXT,
    ADD COLUMN IF NOT EXISTS current_analytical_approach  TEXT;
