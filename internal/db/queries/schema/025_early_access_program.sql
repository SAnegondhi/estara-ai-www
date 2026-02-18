-- Schema for early access program tables (for sqlc validation)
-- ADR-085: Early Access Program

-- early_access_requests — stores applications from prospective users
CREATE TABLE IF NOT EXISTS early_access_requests (
    id             TEXT PRIMARY KEY,
    email          TEXT NOT NULL,
    first_name     TEXT NOT NULL,
    last_name      TEXT NOT NULL,
    company        TEXT,
    use_case       TEXT NOT NULL,
    portfolio_size TEXT,
    linkedin_url   TEXT,
    status         TEXT NOT NULL DEFAULT 'pending',
    user_id        TEXT,
    admin_notes    TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at    TIMESTAMPTZ,
    reviewed_by    TEXT
);

-- password_setup_tokens — one-time tokens for initial password setup
CREATE TABLE IF NOT EXISTS password_setup_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    token      TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
