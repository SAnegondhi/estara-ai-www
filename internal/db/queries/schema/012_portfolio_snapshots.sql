-- Portfolio Schema
-- V2 Portfolio Properties and Snapshots

-- V2 Portfolio Properties table (required for sqlc validation)
-- Note: This table is managed by Prisma migrations, this schema is for sqlc only
CREATE TABLE IF NOT EXISTS v2_portfolio_properties (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    city TEXT NOT NULL,
    state TEXT NOT NULL,
    zip_code TEXT NOT NULL,
    property_type TEXT,
    bedrooms INTEGER,
    bathrooms DOUBLE PRECISION,
    sqft INTEGER,
    year_built INTEGER,
    purchase_price DOUBLE PRECISION NOT NULL,
    purchase_date TIMESTAMP NOT NULL,
    current_value DOUBLE PRECISION,
    last_valued_at TIMESTAMP,
    monthly_rent DOUBLE PRECISION,
    vacancy_rate DOUBLE PRECISION,
    expenses JSONB,
    mortgage_balance DOUBLE PRECISION,
    mortgage_rate DOUBLE PRECISION,
    mortgage_payment DOUBLE PRECISION,
    loan_term_years INTEGER,
    lat DOUBLE PRECISION,
    lng DOUBLE PRECISION,
    acquisition_type TEXT NOT NULL DEFAULT 'purchase',
    expense_frequency TEXT NOT NULL DEFAULT 'monthly',
    revenue_frequency TEXT NOT NULL DEFAULT 'monthly',
    sale_date TIMESTAMP,
    sale_price DOUBLE PRECISION,
    status TEXT NOT NULL DEFAULT 'active',
    property_status VARCHAR NOT NULL DEFAULT 'rented',
    last_confirmed_at TIMESTAMP,
    notes TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL
);

-- V2 Portfolio Snapshots - Historical portfolio metrics for trend tracking
CREATE TABLE IF NOT EXISTS v2_portfolio_snapshots (
    id VARCHAR(255) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Snapshot timing
    snapshot_date TIMESTAMP NOT NULL,

    -- Core metrics
    total_value DOUBLE PRECISION NOT NULL DEFAULT 0,
    total_equity DOUBLE PRECISION NOT NULL DEFAULT 0,
    total_debt DOUBLE PRECISION NOT NULL DEFAULT 0,
    monthly_cash_flow DOUBLE PRECISION NOT NULL DEFAULT 0,
    portfolio_cap_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    property_count INTEGER NOT NULL DEFAULT 0,

    -- Full metrics JSON for flexibility and future additions
    metrics_json JSONB,

    -- Timestamps
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_user_id ON v2_portfolio_snapshots(user_id);
CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_date ON v2_portfolio_snapshots(snapshot_date DESC);
CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_user_date ON v2_portfolio_snapshots(user_id, snapshot_date DESC);

-- Unique constraint: one snapshot per user per date
CREATE UNIQUE INDEX IF NOT EXISTS idx_portfolio_snapshots_unique ON v2_portfolio_snapshots(user_id, snapshot_date);

-- V2 Baseline Changes - Track when baseline values change over time
-- Used for rent increases, expense changes, etc. that only affect future months
CREATE TABLE IF NOT EXISTS v2_baseline_changes (
    id VARCHAR(255) PRIMARY KEY,
    property_id VARCHAR(255) NOT NULL REFERENCES v2_portfolio_properties(id) ON DELETE CASCADE,

    -- Which field is changing (e.g., 'monthlyRent', 'expenses.insurance', 'vacancyRate')
    field VARCHAR(50) NOT NULL,

    -- When the new value takes effect (first day of the month)
    effective_date TIMESTAMP NOT NULL,

    -- The new baseline value from this date forward
    new_value DOUBLE PRECISION NOT NULL,

    -- Previous value (for reference/audit)
    previous_value DOUBLE PRECISION,

    -- Optional note (e.g., "Lease renewal", "Insurance premium increase")
    note TEXT,

    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for baseline changes
CREATE INDEX IF NOT EXISTS idx_baseline_changes_property_id ON v2_baseline_changes(property_id);
CREATE INDEX IF NOT EXISTS idx_baseline_changes_effective_date ON v2_baseline_changes(effective_date);
CREATE UNIQUE INDEX IF NOT EXISTS idx_baseline_changes_unique ON v2_baseline_changes(property_id, field, effective_date);

-- V2 Portfolio Adjustments - Track monthly variances from baseline performance
-- Only stores deviations from expected (e.g., vacancy gap, extra expense)
CREATE TABLE IF NOT EXISTS v2_portfolio_adjustments (
    id VARCHAR(255) PRIMARY KEY,
    property_id VARCHAR(255) NOT NULL REFERENCES v2_portfolio_properties(id) ON DELETE CASCADE,

    -- Which month this adjustment applies to (first day of the month)
    month TIMESTAMP NOT NULL,

    -- Type of adjustment: 'rent', 'expense', 'mortgage'
    type VARCHAR(20) NOT NULL,

    -- Amount of variance (positive = more than baseline, negative = less than baseline)
    amount DOUBLE PRECISION NOT NULL,

    -- Optional note explaining the adjustment
    note TEXT,

    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for adjustments
CREATE INDEX IF NOT EXISTS idx_adjustments_property_id ON v2_portfolio_adjustments(property_id);
CREATE INDEX IF NOT EXISTS idx_adjustments_month ON v2_portfolio_adjustments(month);
CREATE UNIQUE INDEX IF NOT EXISTS idx_adjustments_unique ON v2_portfolio_adjustments(property_id, month, type);
