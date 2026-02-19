-- Migration: 20260219_audit_actor_type
-- Description: Adds actorType field to admin_audit_log for distinguishing human admins from system processes
-- Author: Claude Sonnet 4.5
-- Date: 2026-02-19
-- Related: ADR-086 Enhanced Admin Audit Logging

-- Create actorType enum
DO $$ BEGIN
    CREATE TYPE "AuditActorType" AS ENUM (
        'ADMIN_USER',       -- Human admin via web UI
        'SYSTEM',           -- Automated system processes
        'CRON_JOB',         -- Scheduled cron jobs
        'API_AUTOMATION',   -- External API calls (future)
        'BACKGROUND_WORKER' -- Async worker processes
    );
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

-- Add actorType column to admin_audit_log (defaults to ADMIN_USER for backward compatibility)
ALTER TABLE admin_audit_log
ADD COLUMN IF NOT EXISTS "actorType" "AuditActorType" NOT NULL DEFAULT 'ADMIN_USER';

-- Create index for filtering by actor type
CREATE INDEX IF NOT EXISTS idx_admin_audit_actor_created
ON admin_audit_log("actorType", "createdAt");
