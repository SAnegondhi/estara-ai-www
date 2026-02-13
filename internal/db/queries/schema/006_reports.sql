-- Schema for reports and entitlements tables (matches Prisma schema)
-- These tables already exist in the database - this file is for sqlc code generation only

-- Enums (create if not exists pattern)
DO $$ BEGIN
    CREATE TYPE "AccessTier" AS ENUM (
        'INVESTOR', 'PROFESSIONAL', 'AAPI_INVESTOR', 'AAPI_ALLOCATOR',
        'ANNUAL_ACCESS', 'PROFESSIONAL_ALLOCATOR'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "AccessStatus" AS ENUM (
        'ACTIVE', 'CANCELLED', 'EXPIRED', 'PAUSED'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "InvestorReportType" AS ENUM (
        'SNAPSHOT', 'INVESTOR', 'PROFESSIONAL',
        'ANNUAL_ACCESS', 'PROFESSIONAL_ALLOCATOR'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "InvestorReportStatus" AS ENUM (
        'PENDING', 'GENERATING', 'COMPLETE', 'FAILED', 'SUPERSEDED'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "ReportSourceType" AS ENUM (
        'SINGLE_PURCHASE', 'REPORT_PACK', 'ANNUAL_SUBSCRIPTION', 'FREE_SNAPSHOT', 'OVERAGE'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "SnapshotStatus" AS ENUM (
        'PENDING', 'GENERATING', 'COMPLETE', 'FAILED', 'CONVERTED'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

-- InsightAccess table (annual subscriptions)
CREATE TABLE IF NOT EXISTS insight_access (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tier "AccessTier" NOT NULL,
    "billingFrequency" "BillingFrequency" NOT NULL DEFAULT 'ANNUAL',
    "reportsPerPeriod" INTEGER NOT NULL,
    "reportsPerYear" INTEGER,
    "reportsUsed" INTEGER NOT NULL DEFAULT 0,
    "rolloverReports" INTEGER NOT NULL DEFAULT 0,
    "lastRolloverDate" TIMESTAMP(3),
    "periodStartDate" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "periodEndDate" TIMESTAMP(3) NOT NULL,
    "startDate" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "endDate" TIMESTAMP(3) NOT NULL,
    "stripeSubId" TEXT UNIQUE,
    "stripePriceId" TEXT,
    status "AccessStatus" NOT NULL DEFAULT 'ACTIVE',
    "lastReportGeneratedAt" TIMESTAMP(3),
    "consumptionHistory" JSONB,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_access_user ON insight_access("userId");
CREATE INDEX IF NOT EXISTS idx_insight_access_status ON insight_access(status);
CREATE INDEX IF NOT EXISTS idx_insight_access_end_date ON insight_access("endDate");
CREATE INDEX IF NOT EXISTS idx_insight_access_period_end ON insight_access("periodEndDate");
CREATE INDEX IF NOT EXISTS idx_insight_access_stripe ON insight_access("stripeSubId");

-- ReportPack table (purchased bundles)
CREATE TABLE IF NOT EXISTS report_packs (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    "totalReports" INTEGER NOT NULL DEFAULT 5,
    "usedReports" INTEGER NOT NULL DEFAULT 0,
    "purchasedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "stripePaymentId" TEXT,
    "lastUsedAt" TIMESTAMP(3),
    "consumptionHistory" JSONB,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_report_pack_user ON report_packs("userId");
CREATE INDEX IF NOT EXISTS idx_report_pack_stripe ON report_packs("stripePaymentId");

-- InvestorReport table
CREATE TABLE IF NOT EXISTS investor_reports (
    id TEXT PRIMARY KEY,
    "userId" TEXT REFERENCES users(id) ON DELETE CASCADE,
    email TEXT,
    "propertyId" TEXT,
    "propertyAddress" TEXT,
    type "InvestorReportType" NOT NULL DEFAULT 'INVESTOR',
    status "InvestorReportStatus" NOT NULL DEFAULT 'PENDING',
    "sourceType" "ReportSourceType" NOT NULL,
    "sourcePackId" TEXT REFERENCES report_packs(id),
    "sourceAccessId" TEXT REFERENCES insight_access(id),
    "stripePaymentId" TEXT,
    "investmentCriteria" JSONB,
    "metricsData" JSONB,
    "narrativeData" JSONB,
    "fullReport" TEXT,
    "pdfUrl" TEXT,
    "generationCost" DECIMAL(10, 6) NOT NULL DEFAULT 0,
    "generationTimeMs" INTEGER,
    "allocationDecrementedAt" TIMESTAMP(3),
    "allocationSnapshot" JSONB,
    "cacheKey" TEXT UNIQUE,
    "criteriaHash" TEXT,
    "cachedFromId" TEXT,
    "supersededById" TEXT,
    "supersededAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "completedAt" TIMESTAMP(3),
    "expiresAt" TIMESTAMP(3)
);

CREATE INDEX IF NOT EXISTS idx_investor_report_user ON investor_reports("userId");
CREATE INDEX IF NOT EXISTS idx_investor_report_email ON investor_reports(email);
CREATE INDEX IF NOT EXISTS idx_investor_report_status ON investor_reports(status);
CREATE INDEX IF NOT EXISTS idx_investor_report_type ON investor_reports(type);
CREATE INDEX IF NOT EXISTS idx_investor_report_created ON investor_reports("createdAt");
CREATE INDEX IF NOT EXISTS idx_investor_report_cache ON investor_reports("cacheKey");
CREATE INDEX IF NOT EXISTS idx_investor_report_criteria ON investor_reports("criteriaHash");

-- SnapshotRequest table (free snapshots / lead generation)
CREATE TABLE IF NOT EXISTS snapshot_requests (
    id TEXT PRIMARY KEY,
    "sessionId" TEXT NOT NULL,
    "userId" TEXT REFERENCES users(id),
    email TEXT,
    criteria JSONB NOT NULL,
    "criteriaHash" TEXT,
    location TEXT,
    status "SnapshotStatus" NOT NULL DEFAULT 'PENDING',
    "snapshotNumber" INTEGER NOT NULL DEFAULT 1,
    "propertiesFound" INTEGER NOT NULL DEFAULT 0,
    properties JSONB,
    "resultId" TEXT,
    "cacheKey" TEXT,
    "cachedAt" TIMESTAMP(3),
    "cacheExpiresAt" TIMESTAMP(3),
    "ipAddress" TEXT,
    "userAgent" TEXT,
    source TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "completedAt" TIMESTAMP(3),
    "convertedAt" TIMESTAMP(3)
);

CREATE INDEX IF NOT EXISTS idx_snapshot_session ON snapshot_requests("sessionId");
CREATE INDEX IF NOT EXISTS idx_snapshot_user ON snapshot_requests("userId");
CREATE INDEX IF NOT EXISTS idx_snapshot_email ON snapshot_requests(email);
CREATE INDEX IF NOT EXISTS idx_snapshot_email_status ON snapshot_requests(email, status);
CREATE INDEX IF NOT EXISTS idx_snapshot_status ON snapshot_requests(status);
CREATE INDEX IF NOT EXISTS idx_snapshot_created ON snapshot_requests("createdAt");
CREATE INDEX IF NOT EXISTS idx_snapshot_cache ON snapshot_requests("cacheKey");
CREATE INDEX IF NOT EXISTS idx_snapshot_criteria ON snapshot_requests("criteriaHash");
