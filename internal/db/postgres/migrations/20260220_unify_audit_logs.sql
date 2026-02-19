-- Migration: 20260220_unify_audit_logs
-- Description: Merge admin_audit_log into audit_logs for single unified audit trail
-- Author: Claude Sonnet 4.5
-- Date: 2026-02-20

-- Step 0: Add 'ADMIN_ACTION' to AuditEventType enum (for admin audit entries)
-- Note: ALTER TYPE ADD VALUE cannot run in a transaction, must check manually
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'ADMIN_ACTION';

-- Step 1: Add actorType column to audit_logs (if not exists)
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS "actorType" "AuditActorType" DEFAULT 'ADMIN_USER';

-- Step 2: Add adminId and adminEmail columns to audit_logs (for admin actions)
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS "adminId" TEXT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS "adminEmail" TEXT;

-- Step 2b: Add resourceId column to audit_logs (if not exists)
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS "resourceId" TEXT;

-- Step 3: Migrate data from admin_audit_log to audit_logs
INSERT INTO audit_logs (
    id, "createdAt", "userId", event, action, resource, "resourceId",
    metadata, "ipAddress", "userAgent", "actorType", "adminId", "adminEmail"
)
SELECT
    id,
    "createdAt",
    NULL as "userId",  -- Admin actions don't have userId
    'ADMIN_ACTION'::"AuditEventType" as event,  -- Use generic ADMIN_ACTION event type
    action::text as action,  -- Store actual admin action as text
    resource,
    "resourceId",
    details as metadata,  -- Map details to metadata
    "ipAddress",
    "userAgent",
    "actorType",
    "adminId",
    "adminEmail"
FROM admin_audit_log
WHERE id NOT IN (SELECT id FROM audit_logs)  -- Avoid duplicates
ON CONFLICT (id) DO NOTHING;

-- Step 4: Set actorType for existing user events (backward fill)
UPDATE audit_logs
SET "actorType" = 'ADMIN_USER'
WHERE "actorType" IS NULL AND "adminId" IS NOT NULL;

UPDATE audit_logs
SET "actorType" = 'SYSTEM'
WHERE "actorType" IS NULL AND event IN ('SYSTEM_ERROR', 'CACHE_OPERATION');

UPDATE audit_logs
SET "actorType" = 'ADMIN_USER'
WHERE "actorType" IS NULL;  -- Default everything else to ADMIN_USER

-- Step 5: Drop admin_audit_log table (after successful migration)
-- Commented out for safety - uncomment after verifying migration
-- DROP TABLE IF EXISTS admin_audit_log;

-- Step 6: Create index on actorType for filtering
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor_type ON audit_logs("actorType", "createdAt");
