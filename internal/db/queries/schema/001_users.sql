-- Schema for users table (matches actual Neon database)
-- Table name is "users" (lowercase)
-- Column names are camelCase

-- UserRole enum (matches Prisma)
DO $$ BEGIN
    CREATE TYPE "UserRole" AS ENUM ('USER', 'ADMIN', 'SUPER_ADMIN');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    email TEXT UNIQUE NOT NULL,
    "firstName" TEXT,
    "lastName" TEXT,
    "stripeCustomerId" TEXT UNIQUE,
    role "UserRole" NOT NULL DEFAULT 'USER',
    "hasDataTier" TEXT DEFAULT 'free',
    password TEXT,
    theme TEXT DEFAULT 'estara',
    "subscriptionTier" TEXT DEFAULT 'free',
    phone TEXT UNIQUE,
    "streetAddress" TEXT,
    city TEXT,
    state TEXT,
    "zipCode" TEXT,
    "investorProfile" JSONB,
    "iapPlatform" TEXT,
    "iapProductId" TEXT,
    "iapReceiptData" TEXT,
    "iapExpiresAt" TIMESTAMP(3),
    "iapLastValidated" TIMESTAMP(3),
    "appleOriginalTransactionId" TEXT UNIQUE,
    "appleEnvironment" TEXT,
    "suspendedAt" TIMESTAMP(3),
    "suspendedBy" TEXT,
    "suspendReason" TEXT
);

CREATE INDEX IF NOT EXISTS idx_user_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_user_stripe_customer ON users("stripeCustomerId");
