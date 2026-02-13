-- Migration: 20260212_rename_tiers_to_product_names
-- Description: Rename subscription tiers to match Stripe product names
--   INVESTOR -> ANNUAL_ACCESS (Annual Access $1,200/yr)
--   PROFESSIONAL -> PROFESSIONAL_ALLOCATOR (Professional Allocator $2,400/yr)
-- Author: Claude
-- Date: 2026-02-12

-- Add new ENUM values (PostgreSQL doesn't support renaming ENUM values)
-- Old values remain in the type but are unused.

ALTER TYPE "SubscriptionTier" ADD VALUE IF NOT EXISTS 'ANNUAL_ACCESS';
ALTER TYPE "SubscriptionTier" ADD VALUE IF NOT EXISTS 'PROFESSIONAL_ALLOCATOR';

ALTER TYPE "AccessTier" ADD VALUE IF NOT EXISTS 'ANNUAL_ACCESS';
ALTER TYPE "AccessTier" ADD VALUE IF NOT EXISTS 'PROFESSIONAL_ALLOCATOR';

ALTER TYPE "InvestorReportType" ADD VALUE IF NOT EXISTS 'ANNUAL_ACCESS';
ALTER TYPE "InvestorReportType" ADD VALUE IF NOT EXISTS 'PROFESSIONAL_ALLOCATOR';

-- Update users table (TEXT field - simple update)
UPDATE users SET "subscriptionTier" = 'annual_access' WHERE "subscriptionTier" = 'investor';
UPDATE users SET "subscriptionTier" = 'professional_allocator' WHERE "subscriptionTier" = 'professional';

-- Update subscriptions table (ENUM field - uses new values added above)
UPDATE subscriptions SET tier = 'ANNUAL_ACCESS' WHERE tier = 'INVESTOR';
UPDATE subscriptions SET tier = 'PROFESSIONAL_ALLOCATOR' WHERE tier = 'PROFESSIONAL';

-- Update billing_cycles table
UPDATE billing_cycles SET tier = 'ANNUAL_ACCESS' WHERE tier = 'INVESTOR';
UPDATE billing_cycles SET tier = 'PROFESSIONAL_ALLOCATOR' WHERE tier = 'PROFESSIONAL';

-- Update V2 evaluation quotas (TEXT field)
UPDATE v2_evaluation_quotas SET tier = 'V2_ANNUAL_ACCESS' WHERE tier = 'V2_PROFESSIONAL';
UPDATE v2_evaluation_quotas SET tier = 'V2_PROFESSIONAL_ALLOCATOR' WHERE tier = 'V2_ADVANCED';

-- Update investor_reports type
UPDATE investor_reports SET type = 'ANNUAL_ACCESS' WHERE type = 'INVESTOR';
UPDATE investor_reports SET type = 'PROFESSIONAL_ALLOCATOR' WHERE type = 'PROFESSIONAL';

-- Update insight_access tier
UPDATE insight_access SET tier = 'ANNUAL_ACCESS' WHERE tier = 'INVESTOR';
UPDATE insight_access SET tier = 'PROFESSIONAL_ALLOCATOR' WHERE tier = 'PROFESSIONAL';
