-- Migration: 20260215_admin_extended_tables
-- Description: Create admin extended tables (vendor_contracts, terms_acceptances, admin_credits)
-- Author: Claude
-- Date: 2026-02-15

-- vendor_contracts — Contract metadata per vendor
CREATE TABLE IF NOT EXISTS vendor_contracts (
    id TEXT PRIMARY KEY,
    "vendorId" TEXT NOT NULL REFERENCES vendor_configs(id) ON DELETE CASCADE,
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

-- terms_acceptances — ToS acceptance log with IP/UA
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

-- admin_credits — Admin-granted credits/discounts
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
