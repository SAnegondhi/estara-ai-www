-- Migration: 20260214_whitelisted_emails
-- Description: Adds whitelisted_emails table for beta access control
-- Author: Claude
-- Date: 2026-02-14
-- Note: This table already exists from the Prisma migration (www_v1).
--       The CREATE TABLE IF NOT EXISTS is a no-op, kept for documentation.

DO $$ BEGIN
    CREATE TYPE "WhitelistType" AS ENUM ('EMAIL', 'DOMAIN');
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS whitelisted_emails (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    name TEXT,
    type "WhitelistType" NOT NULL DEFAULT 'EMAIL',
    "addedBy" TEXT,
    reason TEXT,
    active BOOLEAN NOT NULL DEFAULT true,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_whitelisted_emails_email ON whitelisted_emails(email);
CREATE INDEX IF NOT EXISTS idx_whitelisted_emails_active ON whitelisted_emails(active);
CREATE INDEX IF NOT EXISTS idx_whitelisted_emails_type ON whitelisted_emails(type);
