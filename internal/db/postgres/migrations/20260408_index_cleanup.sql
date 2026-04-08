-- Migration: 20260603_index_cleanup
-- Description: Remove duplicate indexes and add missing FK indexes
-- Date: 2026-03-06
--
-- BACKGROUND
-- 27 duplicate index groups were found where a plain btree index shadowed an
-- existing UNIQUE constraint index on the same column(s). The UNIQUE index
-- already satisfies all lookup queries, so the plain duplicate is pure overhead
-- on every INSERT/UPDATE — consuming space and write amplification with no
-- query benefit.
--
-- Additionally 6 foreign-key columns had no index, risking sequential scans
-- on cascading deletes and any join/filter on those columns.
--
-- CHANGES
--   DROP  27 redundant plain indexes (UNIQUE constraint index kept for each)
--   DROP   3 extra duplicates on whitelisted_emails (keeps one UNIQUE per column)
--   ADD    6 missing FK indexes

-- =============================================================================
-- SECTION 1: Drop duplicate plain indexes
-- In each case the UNIQUE constraint index (ending in _key or idx_*_email) is
-- kept; the redundant plain index is removed.
-- =============================================================================

-- property_cache (UNIQUE: property_cache_cache_key_key)
DROP INDEX IF EXISTS idx_property_cache_key;

-- ai_scoring_cache (UNIQUE: ai_scoring_cache_cache_key_key)
DROP INDEX IF EXISTS idx_ai_scoring_cache_key;

-- cached_properties (UNIQUE: cached_properties_listing_id_key)
DROP INDEX IF EXISTS cached_properties_listing_id_idx;

-- early_access (UNIQUE: early_access_email_key)
DROP INDEX IF EXISTS early_access_email_idx;

-- early_access_requests (UNIQUE: early_access_requests_email_key)
DROP INDEX IF EXISTS idx_ear_email;

-- insight_access (UNIQUE: insight_access_stripeSubId_key)
DROP INDEX IF EXISTS "insight_access_stripeSubId_idx";

-- market_analysis_reports (UNIQUE: market_analysis_reports_cache_key_key)
DROP INDEX IF EXISTS idx_market_reports_cache_key;

-- password_reset_tokens (UNIQUE: password_reset_tokens_token_key)
DROP INDEX IF EXISTS password_reset_tokens_token_idx;

-- password_setup_tokens (UNIQUE: password_setup_tokens_token_key)
DROP INDEX IF EXISTS idx_pst_token;

-- receipts invoiceId (UNIQUE: receipts_invoiceId_key)
DROP INDEX IF EXISTS "receipts_invoiceId_idx";

-- receipts receiptNumber (UNIQUE: receipts_receiptNumber_key)
DROP INDEX IF EXISTS "receipts_receiptNumber_idx";

-- studio_jobs (UNIQUE: studio_jobs_jobId_key)
DROP INDEX IF EXISTS "studio_jobs_jobId_idx";

-- studio_report_requests (UNIQUE: studio_report_requests_studioOrderId_key)
DROP INDEX IF EXISTS "studio_report_requests_studioOrderId_idx";

-- system_alerts (UNIQUE: system_alerts_alertKey_key)
DROP INDEX IF EXISTS "system_alerts_alertKey_idx";

-- guest_sessions (UNIQUE: guest_sessions_sessionId_key)
DROP INDEX IF EXISTS "guest_sessions_sessionId_idx";

-- user_credit_wallets (UNIQUE: user_credit_wallets_userId_key)
DROP INDEX IF EXISTS "user_credit_wallets_userId_idx";

-- invoices (UNIQUE: invoices_stripeInvoiceId_key)
DROP INDEX IF EXISTS "invoices_stripeInvoiceId_idx";

-- user_market_locations (UNIQUE: user_market_locations_userId_key)
DROP INDEX IF EXISTS "user_market_locations_userId_idx";

-- webauthn_credentials (UNIQUE: webauthn_credentials_credential_id_key)
DROP INDEX IF EXISTS idx_webauthn_cred_id;

-- frontier_runs: partial index is redundant — the UNIQUE index already covers
-- all non-null lookups and enforces uniqueness (frontier_runs_share_token_key kept)
DROP INDEX IF EXISTS idx_frontier_runs_token;

-- =============================================================================
-- SECTION 2: whitelisted_emails — triple duplicates
-- Keep: idx_whitelisted_emails_email (UNIQUE), idx_whitelisted_emails_active,
--       idx_whitelisted_emails_type
-- Drop: the redundant second UNIQUE and the plain duplicates
-- =============================================================================

-- email column: three indexes — keep idx_whitelisted_emails_email (UNIQUE)
DROP INDEX IF EXISTS whitelisted_emails_email_key;   -- redundant second UNIQUE
DROP INDEX IF EXISTS whitelisted_emails_email_idx;   -- plain duplicate

-- active column: keep idx_whitelisted_emails_active
DROP INDEX IF EXISTS whitelisted_emails_active_idx;

-- type column: keep idx_whitelisted_emails_type
DROP INDEX IF EXISTS whitelisted_emails_type_idx;

-- =============================================================================
-- SECTION 3: admin_sessions — exact duplicates with different naming
-- Keep the idx_* variants (Go codebase convention), drop the camelCase Prisma ones
-- =============================================================================

-- expiresAt
DROP INDEX IF EXISTS "admin_sessions_expiresAt_idx";

-- userId + revokedAt composite
DROP INDEX IF EXISTS "admin_sessions_userId_revokedAt_idx";

-- =============================================================================
-- SECTION 4: cron_job_runs — near-duplicate with different sort order
-- Keep idx_cron_job_runs_job (DESC on startedAt — better for latest-runs queries)
-- Drop the ASC variant
-- Keep idx_cron_job_runs_status, drop plain duplicate
-- =============================================================================

DROP INDEX IF EXISTS "cron_job_runs_cronJobId_startedAt_idx";
DROP INDEX IF EXISTS cron_job_runs_status_idx;

-- =============================================================================
-- SECTION 5: Add missing FK indexes
-- These columns are FK references with no index — risk of seqscans on joins
-- and cascading deletes.
-- =============================================================================

CREATE INDEX IF NOT EXISTS idx_credit_ledger_entries_vendor_usage_id
    ON credit_ledger_entries ("vendorUsageId");

CREATE INDEX IF NOT EXISTS idx_early_access_requests_user_id
    ON early_access_requests (user_id);

CREATE INDEX IF NOT EXISTS idx_properties_user_id
    ON properties ("userId");

CREATE INDEX IF NOT EXISTS idx_reports_property_id
    ON reports ("propertyId");

CREATE INDEX IF NOT EXISTS idx_reports_user_id
    ON reports ("userId");

-- Self-referential FK on vendor_usage_records (parent → child lookups)
CREATE INDEX IF NOT EXISTS idx_vendor_usage_records_parent_request_id
    ON vendor_usage_records ("parentRequestId");
