-- Migration: 20260218_early_access_program
-- Description: Add early access program tables and extend V2SubscriptionTier enum
-- Author: Claude
-- Date: 2026-02-18

-- 1. Extend V2SubscriptionTier enum with EARLY_ACCESS value
ALTER TYPE "V2SubscriptionTier" ADD VALUE IF NOT EXISTS 'EARLY_ACCESS';

-- 2. Add early_access_status column to users table
ALTER TABLE users ADD COLUMN IF NOT EXISTS early_access_status TEXT;
-- Values: NULL (not an early access user) | 'active' | 'suspended' | 'terminated'

-- 3. Create early_access_requests table
CREATE TABLE IF NOT EXISTS early_access_requests (
    id             TEXT PRIMARY KEY,
    email          TEXT NOT NULL UNIQUE,
    first_name     TEXT NOT NULL,
    last_name      TEXT NOT NULL,
    company        TEXT,
    use_case       TEXT NOT NULL,
    portfolio_size TEXT,              -- '1-5' | '6-20' | '21-100' | '100+'
    linkedin_url   TEXT,
    status         TEXT NOT NULL DEFAULT 'pending', -- pending | approved | rejected
    user_id        TEXT REFERENCES users(id),       -- populated on approval
    admin_notes    TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at    TIMESTAMPTZ,
    reviewed_by    TEXT                             -- admin user ID
);

CREATE INDEX IF NOT EXISTS idx_ear_status  ON early_access_requests(status);
CREATE INDEX IF NOT EXISTS idx_ear_email   ON early_access_requests(email);
CREATE INDEX IF NOT EXISTS idx_ear_created ON early_access_requests(created_at DESC);

-- 4. Create password_setup_tokens table
CREATE TABLE IF NOT EXISTS password_setup_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,  -- 72 hours from creation
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pst_token ON password_setup_tokens(token);
CREATE INDEX IF NOT EXISTS idx_pst_user  ON password_setup_tokens(user_id);
