-- Migration: 20260304_pipeline_property_types
-- Description: ADR-107 Phase 2 — commercial property fields for expanded property types
-- Date: 2026-03-03

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS lease_type              TEXT,
    -- 'nnn' | 'modified_gross' | 'gross' | 'full_service'
    ADD COLUMN IF NOT EXISTS tenant_count            INTEGER,
    ADD COLUMN IF NOT EXISTS anchor_tenant           TEXT,
    ADD COLUMN IF NOT EXISTS weighted_avg_lease_yrs  NUMERIC,
    -- WALR: weighted average lease remaining in years
    ADD COLUMN IF NOT EXISTS commercial_sqft         INTEGER,
    -- For mixed_use: separate commercial building area (sqft = residential area)
    ADD COLUMN IF NOT EXISTS commercial_mix          JSONB;
    -- [{ tenantName, sqft, monthlyRent, leaseType, leaseExpiry }]
