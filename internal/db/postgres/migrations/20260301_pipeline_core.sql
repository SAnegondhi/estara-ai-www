-- ADR-101: Investment Pipeline — Core schema
-- pipeline_deals: top-level deal entity (one per broker proposal, off-market pitch, etc.)
-- pipeline_properties: individual properties within a deal
-- Extensions to v2_evaluations and evaluation_chat_sessions for source tracking

CREATE TABLE IF NOT EXISTS pipeline_deals (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          TEXT NOT NULL,
    name             TEXT NOT NULL,
    source           TEXT NOT NULL DEFAULT 'broker',
    -- source values: broker | off-market | syndication | jv | auction | direct | other
    status           TEXT NOT NULL DEFAULT 'under_review',
    -- status values: under_review | memo_generated | passed | proceeding | closed
    notes            TEXT,
    property_count   INTEGER NOT NULL DEFAULT 0,
    memo_count       INTEGER NOT NULL DEFAULT 0,
    portfolio_excluded BOOLEAN NOT NULL DEFAULT FALSE,
    last_activity_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pipeline_deals_user_id ON pipeline_deals(user_id);
CREATE INDEX IF NOT EXISTS idx_pipeline_deals_status  ON pipeline_deals(status);
CREATE INDEX IF NOT EXISTS idx_pipeline_deals_user_status ON pipeline_deals(user_id, status);

CREATE TABLE IF NOT EXISTS pipeline_properties (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_deal_id  UUID NOT NULL REFERENCES pipeline_deals(id) ON DELETE CASCADE,
    address           TEXT NOT NULL,
    city              TEXT,
    state             TEXT,
    zip               TEXT,
    property_type     TEXT,
    -- property_type: sfh | multifamily | condo | townhouse | commercial | nnn | other
    beds              NUMERIC,
    baths             NUMERIC,
    sqft              INTEGER,
    year_built        INTEGER,
    units             INTEGER,
    asking_price      NUMERIC,
    target_price      NUMERIC,
    down_payment_pct  NUMERIC,
    financing_type    TEXT,
    -- financing_type: conventional | cash | other
    interest_rate     NUMERIC,
    broker_rent       NUMERIC,
    system_rent       NUMERIC,
    current_occupancy NUMERIC,
    expense_overrides JSONB,
    -- expense_overrides keys: vacancy_rate, property_tax_rate, insurance_rate,
    --   maintenance_rate, management_fee
    source_type       TEXT NOT NULL DEFAULT 'manual',
    -- source_type: manual | address_lookup | document_upload | discover_duplicate
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pipeline_properties_deal_id ON pipeline_properties(pipeline_deal_id);

-- Extend v2_evaluations to link to pipeline (both deal and specific property)
ALTER TABLE v2_evaluations
    ADD COLUMN IF NOT EXISTS pipeline_property_id UUID,
    ADD COLUMN IF NOT EXISTS pipeline_deal_id UUID;

-- Extend evaluation_chat_sessions to link to pipeline deal
ALTER TABLE evaluation_chat_sessions
    ADD COLUMN IF NOT EXISTS pipeline_deal_id UUID;
