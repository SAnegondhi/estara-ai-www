-- Migration: 20260308_pipeline_extraction_issues
-- Description: Adds extraction_issues JSONB column for Pass 3 OM validation results.
-- Each row is an omExtractionIssue {field, tab, severity, omSays, message}.
-- Populated asynchronously after CreateDealFromOM; shown to user in the wizard
-- as per-tab amber warnings to build trust in extraction quality.

ALTER TABLE pipeline_properties
    ADD COLUMN IF NOT EXISTS extraction_issues JSONB;
