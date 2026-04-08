-- ADR-112 Addendum: Type-specific property fields missing from initial wizard migration.
-- Adds columns for broker metrics (Tab 2) and type-specific physical fields (Tab 1).

-- Tab 2: broker-stated metrics (not derivable from other fields)
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS broker_grm             NUMERIC;       -- broker-stated GRM
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS broker_dscr            NUMERIC;       -- broker-stated DSCR
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS assumable_debt_balance NUMERIC;       -- assumable loan balance ($)
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS assumable_debt_term    NUMERIC;       -- remaining term (years, decimal OK)
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS cap_rate_pro_forma     NUMERIC;       -- pro-forma cap rate (decimal, e.g. 0.065)

-- Tab 1: multifamily
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS building_class         TEXT;          -- 'A' | 'B' | 'C' | 'D'

-- Tab 1: residential (SFH/condo/townhouse)
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS hoa_monthly            NUMERIC;       -- HOA fee ($/month)

-- Tab 1: industrial / warehouse
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS clear_height_ft        NUMERIC;       -- clear height in feet
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS loading_docks          INTEGER;       -- number of loading docks
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS office_pct             NUMERIC;       -- % of GLA that is office space (0-100)
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS sprinklered            TEXT;          -- 'yes' | 'no'

-- Tab 1: self-storage
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS total_storage_units    INTEGER;       -- total rentable units
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS climate_controlled_pct NUMERIC;       -- % of units that are climate-controlled (0-100)
