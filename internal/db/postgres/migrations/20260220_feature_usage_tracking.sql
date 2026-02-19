-- Migration: 20260220_feature_usage_tracking
-- Description: Add feature usage event types to AuditEventType enum for Estara Insight feature tracking
-- Author: Claude Sonnet 4.5
-- Date: 2026-02-20
-- Purpose: Track all user feature usage for customer support and chargeback defense

-- Add new feature usage event types to existing enum
-- Note: ALTER TYPE ADD VALUE cannot run inside transaction blocks, but migrations run in individual transactions
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_DISCOVER_SEARCH';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_EVALUATION_VIEW';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_DECISION_MEMO';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_MARKET_ANALYSIS';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_MARKET_TRENDS';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_SCENARIO_CREATE';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_SCENARIO_UPDATE';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_SCENARIO_VIEW';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_PORTFOLIO_ADD';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_PDF_EXPORT';
ALTER TYPE "AuditEventType" ADD VALUE IF NOT EXISTS 'FEATURE_PASSWORD_CHANGE';

-- Create index for feature usage queries (performance optimization)
-- This index helps with feature usage analytics and user activity tracking
CREATE INDEX IF NOT EXISTS idx_audit_feature_usage
ON audit_logs(event, "userId", "createdAt")
WHERE event::text LIKE 'FEATURE_%';
