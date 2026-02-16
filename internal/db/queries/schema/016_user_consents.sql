-- User Consents table for compliance/GDPR tracking
-- Schema matches actual database (Prisma-created, camelCase columns)

DO $$ BEGIN
    CREATE TYPE "ConsentType" AS ENUM (
        'MARKETING', 'ANALYTICS', 'COOKIES', 'DATA_SHARING', 'TERMS_OF_SERVICE', 'PRIVACY_POLICY'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

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
