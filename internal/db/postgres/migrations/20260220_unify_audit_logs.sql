-- Migration: 20260220_unify_audit_logs
-- Description: Merge admin_audit_log into audit_logs for single unified audit trail
-- Author: Claude Sonnet 4.5
-- Date: 2026-02-20

-- Step 1: Add actorType column to audit_logs (if not exists)
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'audit_logs' AND column_name = 'actorType') THEN
        ALTER TABLE audit_logs ADD COLUMN "actorType" "AuditActorType" DEFAULT 'ADMIN_USER';
    END IF;
END $$;

-- Step 2: Add adminId and adminEmail columns to audit_logs (for admin actions)
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'audit_logs' AND column_name = 'adminId') THEN
        ALTER TABLE audit_logs ADD COLUMN "adminId" TEXT;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'audit_logs' AND column_name = 'adminEmail') THEN
        ALTER TABLE audit_logs ADD COLUMN "adminEmail" TEXT;
    END IF;
END $$;

-- Step 2b: Add resourceId column to audit_logs (if not exists)
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'audit_logs' AND column_name = 'resourceId') THEN
        ALTER TABLE audit_logs ADD COLUMN "resourceId" TEXT;
    END IF;
END $$;

-- Step 3: Migrate data from admin_audit_log to audit_logs
INSERT INTO audit_logs (
    id, "createdAt", "userId", event, action, resource, "resourceId",
    metadata, "ipAddress", "userAgent", "actorType", "adminId", "adminEmail"
)
SELECT
    id,
    "createdAt",
    NULL as "userId",  -- Admin actions don't have userId
    action::text as event,  -- Map action to event
    action::text as action,  -- Also populate action field
    resource,
    "resourceId",
    details as metadata,  -- Map details to metadata
    "ipAddress",
    "userAgent",
    "actorType",
    "adminId",
    "adminEmail"
FROM admin_audit_log
WHERE id NOT IN (SELECT id FROM audit_logs);  -- Avoid duplicates

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
