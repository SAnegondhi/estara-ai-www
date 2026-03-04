-- ADR-105: Pipeline OM data model — flexible OM storage + file persistence
-- Adds om_data JSONB (flexible extracted content), broker_cap_rate typed column,
-- and file storage columns (volume path preferred over BYTEA).

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS om_data         JSONB,     -- All extracted OM content (flexible-first)
    ADD COLUMN IF NOT EXISTS broker_cap_rate NUMERIC,   -- Typed cap rate from OM (as decimal, e.g. 0.065)
    ADD COLUMN IF NOT EXISTS om_file_path    TEXT,      -- Volume-relative path (preferred; set when volume configured)
    ADD COLUMN IF NOT EXISTS om_file_data    BYTEA,     -- Fallback storage if no volume configured
    ADD COLUMN IF NOT EXISTS om_file_name    TEXT,      -- Original filename (for display + Content-Disposition)
    ADD COLUMN IF NOT EXISTS om_file_type    TEXT;      -- MIME type (application/pdf, text/plain, etc.)
