-- Migration: 20260304_pipeline_property_completeness
-- Description: ADR-107 Phase 1 — property completeness validation field
-- Date: 2026-03-03

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS property_completeness TEXT NOT NULL DEFAULT 'incomplete';
-- Values: 'incomplete' | 'sufficient' | 'complete'
-- Computed by Go on every create/update, not client-side
