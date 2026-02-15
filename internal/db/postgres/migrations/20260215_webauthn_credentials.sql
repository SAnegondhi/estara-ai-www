-- Migration: 20260215_webauthn_credentials
-- Description: Adds webauthn_credentials table for passkey (FIDO2) authentication
-- Author: Claude
-- Date: 2026-02-15
-- ADR: ADR-081

CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BYTEA UNIQUE NOT NULL,
    public_key BYTEA NOT NULL,
    attestation_type TEXT NOT NULL DEFAULT 'none',
    transport TEXT[] NOT NULL DEFAULT '{}',
    flags_value INTEGER NOT NULL DEFAULT 0,
    sign_count BIGINT NOT NULL DEFAULT 0,
    aaguid BYTEA,
    clone_warning BOOLEAN NOT NULL DEFAULT false,
    friendly_name TEXT NOT NULL DEFAULT 'Passkey',
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "lastUsedAt" TIMESTAMP(3),
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_webauthn_cred_user ON webauthn_credentials("userId");
CREATE INDEX IF NOT EXISTS idx_webauthn_cred_id ON webauthn_credentials(credential_id);
