-- ADR-112: New Deal Wizard — additional property fields
-- Adds fields present in the OM review form but missing from manual entry.

ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS description TEXT;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS parking TEXT;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS broker_noi NUMERIC;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS broker_noi_stabilized NUMERIC;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS gross_potential_rent NUMERIC;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS effective_gross_income NUMERIC;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS vacancy_pct NUMERIC;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS vacancy_label TEXT;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS expense_items JSONB;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS om_date TEXT;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS broker_contact JSONB;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS latitude NUMERIC;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS longitude NUMERIC;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS broker_5yr_irr NUMERIC;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS broker_yr1_coc NUMERIC;
ALTER TABLE pipeline_properties ADD COLUMN IF NOT EXISTS loan_term_years INTEGER;
