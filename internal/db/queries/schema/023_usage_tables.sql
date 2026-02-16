-- Usage tracking tables (for sqlc validation)
-- Note: These tables are managed by Prisma/legacy migrations, schema is for sqlc only

CREATE TABLE IF NOT EXISTS investment_plan_usage (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    month INTEGER NOT NULL,
    year INTEGER NOT NULL,
    "picksUsed" INTEGER NOT NULL DEFAULT 0,
    "lastPickAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    UNIQUE("userId", month, year)
);

CREATE TABLE IF NOT EXISTS area_comparison_usage (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    month INTEGER NOT NULL,
    year INTEGER NOT NULL,
    "comparisonsUsed" INTEGER NOT NULL DEFAULT 0,
    "lastComparisonAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    UNIQUE("userId", month, year)
);

CREATE TABLE IF NOT EXISTS vendor_usage_summaries (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "vendorName" TEXT NOT NULL,
    month INTEGER NOT NULL,
    year INTEGER NOT NULL,
    "totalRequests" INTEGER NOT NULL DEFAULT 0,
    "totalCost" NUMERIC(10,6) NOT NULL,
    "monthlyLimit" INTEGER,
    "limitExceeded" BOOLEAN NOT NULL DEFAULT false,
    breakdown JSONB,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    UNIQUE("userId", "vendorName", month, year)
);
