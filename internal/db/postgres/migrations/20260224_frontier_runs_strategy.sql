-- Migration: 20260224_frontier_runs_strategy
-- Description: ADR-089 — Add strategy + best_sharpe columns for dashboard filtering/display
-- Author: Claude
-- Date: 2026-02-24

ALTER TABLE frontier_runs ADD COLUMN IF NOT EXISTS strategy TEXT NOT NULL DEFAULT 'balanced';
ALTER TABLE frontier_runs ADD COLUMN IF NOT EXISTS best_sharpe FLOAT NOT NULL DEFAULT 0;
