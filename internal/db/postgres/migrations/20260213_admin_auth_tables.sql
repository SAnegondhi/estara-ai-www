-- Migration: 20260213_admin_auth_tables
-- Description: Create admin_sessions and admin_two_factor tables for admin authentication
-- Author: Claude
-- Date: 2026-02-13

-- admin_sessions table — tracks active admin login sessions
CREATE TABLE IF NOT EXISTS admin_sessions (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "tokenHash" TEXT UNIQUE NOT NULL,
    "ipAddress" TEXT NOT NULL,
    "userAgent" TEXT NOT NULL,
    "deviceName" TEXT,
    location TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "lastActive" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "expiresAt" TIMESTAMP(3) NOT NULL,
    "revokedAt" TIMESTAMP(3)
);

CREATE INDEX IF NOT EXISTS idx_admin_session_user_revoked ON admin_sessions("userId", "revokedAt");
CREATE INDEX IF NOT EXISTS idx_admin_session_expires ON admin_sessions("expiresAt");

-- admin_two_factor table — TOTP secrets and backup codes
CREATE TABLE IF NOT EXISTS admin_two_factor (
    id TEXT PRIMARY KEY,
    "userId" TEXT UNIQUE NOT NULL,
    secret TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT false,
    "backupCodes" TEXT[] NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL
);
