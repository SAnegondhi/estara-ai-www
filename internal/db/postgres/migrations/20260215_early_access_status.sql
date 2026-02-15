-- Migration: 20260215_early_access_status
-- Description: Add status column to early_access for tracking PENDING/INVITED/REVOKED
-- Author: Claude
-- Date: 2026-02-15

ALTER TABLE early_access ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'PENDING';

-- Backfill: existing invited entries → INVITED
UPDATE early_access SET status = 'INVITED' WHERE invited = true AND status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_early_access_status ON early_access(status);
