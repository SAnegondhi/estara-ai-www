-- Migration: 20260402_pipeline_unit_mix
-- Description: Add unit_mix, lot_sqft, building_count to pipeline_properties.
--
-- A multifamily or condo complex has multiple unit types (2bd, 3bd, studios,
-- commercial) each with distinct beds/baths/sqft/rent. The previous schema
-- stored only scalar values, losing the per-unit-type breakdown entirely.
--
-- unit_mix JSONB stores an array of:
--   { type, count, beds, baths, sqftPerUnit,
--     rentCurrent, rentProForma, rentMarket }
--
-- lot_sqft is the land parcel area (not building area — that is still sqft).
-- building_count is the number of structures on the parcel.

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS unit_mix       JSONB,
    ADD COLUMN IF NOT EXISTS lot_sqft       INTEGER,
    ADD COLUMN IF NOT EXISTS building_count INTEGER;
