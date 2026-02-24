-- V2 Evaluations, Quotas, and Decision Records (for sqlc validation)
-- Note: These tables are managed by Prisma/legacy migrations, schema is for sqlc only

-- Enum types (must match database)
DO $$ BEGIN
    CREATE TYPE "V2SubscriptionTier" AS ENUM (
        'V2_PROFESSIONAL',
        'V2_ADVANCED',
        'V2_PRIVATE',
        'V2_ANNUAL_ACCESS',
        'V2_PROFESSIONAL_ALLOCATOR',
        'EARLY_ACCESS'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE "V2EvaluationStatus" AS ENUM (
        'DRAFT',
        'COMPLETED',
        'EXPORTED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS v2_evaluation_quotas (
    id TEXT PRIMARY KEY,
    user_id TEXT UNIQUE NOT NULL,
    tier "V2SubscriptionTier" NOT NULL,
    annual_limit INTEGER NOT NULL,
    used_this_period INTEGER NOT NULL DEFAULT 0,
    period_start_date TIMESTAMP(3) NOT NULL,
    period_end_date TIMESTAMP(3) NOT NULL,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(3) NOT NULL
);

CREATE TABLE IF NOT EXISTS v2_evaluations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    property_id TEXT NOT NULL,
    property_address TEXT NOT NULL,
    property_city TEXT NOT NULL,
    property_state TEXT NOT NULL,
    property_zip TEXT,
    property_details JSONB,
    property_snapshot JSONB,
    purchase_price DOUBLE PRECISION NOT NULL,
    down_payment_pct DOUBLE PRECISION NOT NULL,
    interest_rate DOUBLE PRECISION NOT NULL,
    loan_term_years INTEGER NOT NULL,
    monthly_rent DOUBLE PRECISION NOT NULL,
    vacancy_rate_pct DOUBLE PRECISION NOT NULL,
    maintenance_cost DOUBLE PRECISION NOT NULL,
    property_tax DOUBLE PRECISION NOT NULL,
    insurance DOUBLE PRECISION NOT NULL,
    hoa_fees DOUBLE PRECISION,
    appreciation_rate DOUBLE PRECISION NOT NULL,
    scenarios JSONB,
    sensitivity_data JSONB,
    chat_session_id TEXT,
    discovery_session_id TEXT,
    status "V2EvaluationStatus" NOT NULL DEFAULT 'DRAFT',
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(3) NOT NULL
);

CREATE TABLE IF NOT EXISTS v2_decision_records (
    id TEXT PRIMARY KEY,
    evaluation_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memo_content JSONB NOT NULL,
    pdf_url TEXT,
    exported_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
