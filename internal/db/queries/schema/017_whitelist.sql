-- Schema for whitelisted_emails table (beta access control)
-- This table was created by Prisma (www_v1) and uses camelCase columns + WhitelistType enum.
-- Migration 20260214_whitelisted_emails.sql is a no-op since the table already exists.

-- Enum type (created by Prisma)
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
