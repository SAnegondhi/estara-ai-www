-- Migration: 20260306_pipeline_investment_highlights
-- Description: Adds investment_highlights JSONB column to pipeline_properties
--              for manually-transcribed broker OM highlight bullets.
-- Author: Claude
-- Date: 2026-03-06

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS investment_highlights JSONB;
