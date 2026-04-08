-- Migration: 20260306_pipeline_assumable_debt_rate
-- Description: Adds assumable_debt_rate to pipeline_properties for manually-entered assumable debt interest rate.

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS assumable_debt_rate NUMERIC;
