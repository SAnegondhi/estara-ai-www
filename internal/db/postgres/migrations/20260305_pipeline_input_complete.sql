-- Migration: 20260305_pipeline_input_complete
-- ADR-107 (revised): Add deal-level input_complete flag.
-- Deals with input_complete = false are "saved but not ready for analysis".
-- Only input_complete = true deals can trigger decision memo generation.

ALTER TABLE pipeline_deals
    ADD COLUMN IF NOT EXISTS input_complete BOOLEAN NOT NULL DEFAULT false;
