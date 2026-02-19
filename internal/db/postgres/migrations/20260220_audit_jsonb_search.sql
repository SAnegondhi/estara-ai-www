-- Migration: 20260220_audit_jsonb_search
-- Description: Add GIN index for JSONB full-text search on admin_audit_log.details
-- Author: Claude Sonnet 4.5
-- Date: 2026-02-20

-- Create GIN index on details JSONB column
-- Using jsonb_path_ops for faster queries with @> operator (containment)
CREATE INDEX IF NOT EXISTS idx_admin_audit_details_gin
ON admin_audit_log USING gin(details jsonb_path_ops);

-- Note: jsonb_path_ops optimizes for @> (containment) queries
-- Example: WHERE details @> '{"reason": "Duplicate"}'::jsonb
-- This is faster but only supports @> operator
-- For broader operators (?, ?&, ?|), use default jsonb_ops instead
