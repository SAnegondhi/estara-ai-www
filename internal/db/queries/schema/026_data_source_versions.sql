-- Schema: Data Source Versions (ADR-087 Phase 7)
-- Tracks version numbers for event-driven refresh of market analysis reports

CREATE TABLE data_source_versions (
    source TEXT PRIMARY KEY CHECK (source IN ('zhvi', 'zori', 'redfin', 'fred', 'census', 'ai_context')),
    version TEXT NOT NULL,
    last_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    import_job_id TEXT,
    metadata JSONB DEFAULT '{}'::JSONB
);
