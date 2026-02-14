-- Schema for admin extended tables (vendor contracts, credits, cron tracking, terms)
-- These tables support the admin frontend application

-- =========================================================
-- VendorConfig table (already exists in database from www_v1)
-- Included here for sqlc reference and FK constraints
-- =========================================================

DO $$ BEGIN
    CREATE TYPE "VendorCategory" AS ENUM (
        'AI_PROVIDER', 'DATA_PROVIDER', 'INFRASTRUCTURE',
        'PAYMENT', 'EMAIL', 'MONITORING'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "VendorBillingModel" AS ENUM (
        'PAYGO', 'MONTHLY', 'ANNUAL', 'FREE_TIER', 'USAGE_BASED'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "VendorHealthStatus" AS ENUM (
        'OPERATIONAL', 'DEGRADED', 'DOWN', 'UNKNOWN', 'MAINTENANCE'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

CREATE TABLE IF NOT EXISTS "VendorConfig" (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    "displayName" TEXT NOT NULL,
    category "VendorCategory" NOT NULL,
    "billingModel" "VendorBillingModel" NOT NULL,
    "monthlyCost" NUMERIC(10, 2),
    "apiKeyEnvVar" TEXT,
    "healthCheckUrl" TEXT,
    "healthUrl" TEXT,
    "lastHealthCheck" TIMESTAMP(3),
    "healthStatus" "VendorHealthStatus" NOT NULL DEFAULT 'UNKNOWN',
    "lastChecked" TIMESTAMP(3),
    status TEXT,
    "currentBalance" NUMERIC(10, 2),
    "balanceAlertThreshold" NUMERIC(10, 2),
    "totalRequests" INTEGER NOT NULL DEFAULT 0,
    "totalCost" NUMERIC(10, 2) NOT NULL DEFAULT 0,
    "isActive" BOOLEAN NOT NULL DEFAULT true,
    "isPrimary" BOOLEAN NOT NULL DEFAULT false,
    enabled BOOLEAN NOT NULL DEFAULT true,
    notes TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_vendor_config_category ON "VendorConfig"(category);
CREATE INDEX IF NOT EXISTS idx_vendor_config_active ON "VendorConfig"("isActive");
CREATE INDEX IF NOT EXISTS idx_vendor_config_enabled ON "VendorConfig"(enabled);

-- =========================================================
-- vendor_contracts — Contract metadata per vendor
-- =========================================================

CREATE TABLE IF NOT EXISTS vendor_contracts (
    id TEXT PRIMARY KEY,
    "vendorId" TEXT NOT NULL REFERENCES "VendorConfig"(id) ON DELETE CASCADE,
    "startDate" TIMESTAMP(3) NOT NULL,
    "endDate" TIMESTAMP(3),
    "autoRenew" BOOLEAN NOT NULL DEFAULT false,
    "renewalTermMonths" INTEGER,
    "terminationNoticeDays" INTEGER,
    "pricingTiers" JSONB,
    "usageLimits" JSONB,
    "slaTerms" JSONB,
    "legalRestrictions" TEXT,
    "contactName" TEXT,
    "contactEmail" TEXT,
    notes TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_vendor_contracts_vendor ON vendor_contracts("vendorId");
CREATE INDEX IF NOT EXISTS idx_vendor_contracts_end_date ON vendor_contracts("endDate");

-- =========================================================
-- terms_acceptances — ToS acceptance log with IP/UA
-- =========================================================

CREATE TABLE IF NOT EXISTS terms_acceptances (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    version TEXT NOT NULL,
    "acceptedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "ipAddress" TEXT,
    "userAgent" TEXT,
    UNIQUE("userId", version)
);

CREATE INDEX IF NOT EXISTS idx_terms_acceptances_user ON terms_acceptances("userId");
CREATE INDEX IF NOT EXISTS idx_terms_acceptances_version ON terms_acceptances(version);

-- =========================================================
-- admin_credits — Admin-granted credits/discounts
-- =========================================================

CREATE TABLE IF NOT EXISTS admin_credits (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount NUMERIC(10, 2) NOT NULL,
    reason TEXT NOT NULL,
    "grantedBy" TEXT NOT NULL,
    applied BOOLEAN NOT NULL DEFAULT false,
    "appliedAt" TIMESTAMP(3),
    "expiresAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_admin_credits_user ON admin_credits("userId");
CREATE INDEX IF NOT EXISTS idx_admin_credits_expires ON admin_credits("expiresAt");

-- =========================================================
-- cron_job_configs — Cron job registry with state
-- =========================================================

CREATE TABLE IF NOT EXISTS cron_job_configs (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    description TEXT,
    schedule TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    "isEnabled" BOOLEAN NOT NULL DEFAULT true,
    "timeoutMs" INTEGER NOT NULL DEFAULT 300000,
    "maxConsecutiveFailures" INTEGER NOT NULL DEFAULT 3,
    "consecutiveFailures" INTEGER NOT NULL DEFAULT 0,
    "totalRuns" INTEGER NOT NULL DEFAULT 0,
    "successfulRuns" INTEGER NOT NULL DEFAULT 0,
    "lastRunAt" TIMESTAMP(3),
    "lastRunStatus" TEXT,
    "lastRunDurationMs" INTEGER,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- =========================================================
-- cron_job_runs — Cron execution history
-- =========================================================

CREATE TABLE IF NOT EXISTS cron_job_runs (
    id TEXT PRIMARY KEY,
    "jobId" TEXT NOT NULL REFERENCES cron_job_configs(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'RUNNING',
    "startedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "completedAt" TIMESTAMP(3),
    "durationMs" INTEGER,
    error TEXT,
    output JSONB,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_cron_job_runs_job ON cron_job_runs("jobId", "startedAt" DESC);
CREATE INDEX IF NOT EXISTS idx_cron_job_runs_status ON cron_job_runs(status);
