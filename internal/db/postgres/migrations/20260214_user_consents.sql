-- Migration: 20260214_user_consents
-- Description: Ensures user_consents table exists for compliance/GDPR consent tracking
-- Author: Claude
-- Date: 2026-02-14
-- Updated: 2026-02-15 (fixed column names to match actual Prisma-created schema)

-- ConsentType enum
DO $$ BEGIN
    CREATE TYPE "ConsentType" AS ENUM (
        'MARKETING', 'ANALYTICS', 'COOKIES', 'DATA_SHARING', 'TERMS_OF_SERVICE', 'PRIVACY_POLICY'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

-- user_consents table (already created by Prisma, this is idempotent)
CREATE TABLE IF NOT EXISTS user_consents (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id),
    "consentType" "ConsentType" NOT NULL,
    version TEXT NOT NULL,
    granted BOOLEAN NOT NULL,
    "ipAddress" TEXT NOT NULL,
    "userAgent" TEXT NOT NULL,
    "timestamp" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_user_consents_type ON user_consents("consentType");
CREATE INDEX IF NOT EXISTS idx_user_consents_user ON user_consents("userId");
