-- Schema for website-related tables (matches Prisma schema)
-- These tables already exist in the database - this file is for sqlc code generation only

-- contact_submissions table
CREATE TABLE IF NOT EXISTS contact_submissions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT NOT NULL,
    company TEXT,
    phone TEXT,
    subject TEXT,
    message TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'GENERAL',
    "categoryConfidence" DOUBLE PRECISION,
    "categoryReasoning" TEXT,
    source TEXT,
    "inquiryType" TEXT,
    status TEXT NOT NULL DEFAULT 'NEW',
    "assignedTo" TEXT,
    notes TEXT,
    "firstResponseAt" TIMESTAMP(3),
    "resolvedAt" TIMESTAMP(3),
    "responseCount" INTEGER NOT NULL DEFAULT 0,
    "ipAddress" TEXT,
    "userAgent" TEXT,
    "notificationSent" BOOLEAN NOT NULL DEFAULT false,
    "confirmationSent" BOOLEAN NOT NULL DEFAULT false,
    "sendgridMessageId" TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_contact_email ON contact_submissions(email);
CREATE INDEX IF NOT EXISTS idx_contact_status ON contact_submissions(status);
CREATE INDEX IF NOT EXISTS idx_contact_created ON contact_submissions("createdAt");

-- early_access table (waitlist)
CREATE TABLE IF NOT EXISTS early_access (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    "requestedAt" TIMESTAMP(3),
    source TEXT,
    metadata JSONB,
    contacted BOOLEAN NOT NULL DEFAULT false,
    invited BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'PENDING',
    notes TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_early_access_email ON early_access(email);
CREATE INDEX IF NOT EXISTS idx_early_access_created ON early_access("createdAt");
CREATE INDEX IF NOT EXISTS idx_early_access_invited ON early_access(invited);

-- guest_sessions table (unauthenticated tracking)
CREATE TABLE IF NOT EXISTS guest_sessions (
    id TEXT PRIMARY KEY,
    token TEXT UNIQUE NOT NULL,
    "ipAddress" TEXT,
    "userAgent" TEXT,
    "deviceFingerprint" TEXT,
    "expiresAt" TIMESTAMP(3) NOT NULL,
    "snapshotsUsed" INTEGER NOT NULL DEFAULT 0,
    "lastActivityAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_guest_session_token ON guest_sessions(token);
CREATE INDEX IF NOT EXISTS idx_guest_session_expires ON guest_sessions("expiresAt");
CREATE INDEX IF NOT EXISTS idx_guest_session_created ON guest_sessions("createdAt");
