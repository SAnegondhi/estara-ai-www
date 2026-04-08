-- Migration: 20260307_pipeline_progressive_fields
-- Description: ADR-113 progressive disclosure fields — other income, value-add, amenities, market context
-- Author: Claude
-- Date: 2026-03-07

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS other_income_items         JSONB,
    ADD COLUMN IF NOT EXISTS renovation_cost            NUMERIC,
    ADD COLUMN IF NOT EXISTS claimed_renovation_noi_uplift NUMERIC,
    ADD COLUMN IF NOT EXISTS value_add_data             JSONB,
    ADD COLUMN IF NOT EXISTS building_amenities         TEXT[],
    ADD COLUMN IF NOT EXISTS market_overview_text       TEXT;
