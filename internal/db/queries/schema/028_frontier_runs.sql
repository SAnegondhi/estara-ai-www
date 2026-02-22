-- ADR-088 Phase 13: Saved Analyses + Share Tokens

CREATE TABLE IF NOT EXISTS frontier_runs (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    locations       TEXT[] NOT NULL,
    criteria        JSONB NOT NULL,
    frontier_points JSONB NOT NULL,
    property_count  INT NOT NULL DEFAULT 0,
    strategy        TEXT NOT NULL DEFAULT 'balanced',
    best_sharpe     FLOAT NOT NULL DEFAULT 0,
    share_token     TEXT UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accessed_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_frontier_runs_user  ON frontier_runs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_frontier_runs_token ON frontier_runs(share_token)
    WHERE share_token IS NOT NULL;
