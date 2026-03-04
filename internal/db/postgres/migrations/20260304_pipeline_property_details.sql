-- Migration: 20260304_pipeline_property_details
-- Description: ADR-108 — physical detail columns for type-aware OM extraction
-- Date: 2026-03-04

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS year_renovated  INTEGER,
    ADD COLUMN IF NOT EXISTS stories         INTEGER,
    ADD COLUMN IF NOT EXISTS zoning          TEXT,
    ADD COLUMN IF NOT EXISTS construction    TEXT,
    ADD COLUMN IF NOT EXISTS parking_spaces  INTEGER;
