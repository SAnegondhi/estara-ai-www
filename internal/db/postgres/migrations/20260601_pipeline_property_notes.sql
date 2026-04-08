-- Migration: 20260601_pipeline_property_notes
-- Description: Adds notes/description field to pipeline_properties
-- Author: Claude
-- Date: 2026-03-03

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS notes TEXT;
