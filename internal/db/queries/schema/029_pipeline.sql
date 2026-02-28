-- ADR-101: Pipeline schema for sqlc validation

CREATE TABLE IF NOT EXISTS pipeline_deals (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          TEXT NOT NULL,
    name             TEXT NOT NULL,
    source           TEXT NOT NULL DEFAULT 'broker',
    status           TEXT NOT NULL DEFAULT 'under_review',
    notes            TEXT,
    property_count   INTEGER NOT NULL DEFAULT 0,
    memo_count       INTEGER NOT NULL DEFAULT 0,
    portfolio_excluded BOOLEAN NOT NULL DEFAULT FALSE,
    last_activity_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS pipeline_properties (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_deal_id  UUID NOT NULL REFERENCES pipeline_deals(id) ON DELETE CASCADE,
    address           TEXT NOT NULL,
    city              TEXT,
    state             TEXT,
    zip               TEXT,
    property_type     TEXT,
    beds              NUMERIC,
    baths             NUMERIC,
    sqft              INTEGER,
    year_built        INTEGER,
    units             INTEGER,
    asking_price      NUMERIC,
    target_price      NUMERIC,
    down_payment_pct  NUMERIC,
    financing_type    TEXT,
    interest_rate     NUMERIC,
    broker_rent       NUMERIC,
    system_rent       NUMERIC,
    current_occupancy NUMERIC,
    expense_overrides JSONB,
    source_type       TEXT NOT NULL DEFAULT 'manual',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
