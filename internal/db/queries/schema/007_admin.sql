-- Schema for admin and audit tables (matches Prisma schema)
-- These tables already exist in the database - this file is for sqlc code generation only

-- Enums (create if not exists pattern)
DO $$ BEGIN
    CREATE TYPE "AuditEventType" AS ENUM (
        'USER_SIGNUP', 'USER_SIGNIN', 'USER_SIGNOUT',
        'SUBSCRIPTION_CREATED', 'SUBSCRIPTION_UPDATED', 'SUBSCRIPTION_CANCELED',
        'PAYMENT_SUCCESS', 'PAYMENT_FAILED',
        'PROPERTY_CREATED', 'PROPERTY_UPDATED', 'PROPERTY_DELETED',
        'REPORT_GENERATED', 'REPORT_DOWNLOADED',
        'API_CALL', 'RATE_LIMIT_EXCEEDED', 'SECURITY_VIOLATION', 'DATA_EXPORT',
        'AUTH_SUCCESS', 'AUTH_FAILURE', 'AUTH_TOKEN_REFRESH', 'UNAUTHORIZED_ACCESS',
        'AI_REQUEST_STARTED', 'AI_REQUEST_COMPLETED', 'AI_REQUEST_FAILED', 'AI_REQUEST_BLOCKED',
        'CONTENT_VALIDATION_FAILED', 'PROMPT_INJECTION_DETECTED', 'PII_DETECTED', 'MALICIOUS_CONTENT_DETECTED',
        'SYSTEM_ERROR',
        'ADMIN_ACTION',
        -- Feature usage tracking (added 2026-02-20)
        'FEATURE_DISCOVER_SEARCH', 'FEATURE_EVALUATION_VIEW', 'FEATURE_DECISION_MEMO',
        'FEATURE_MARKET_ANALYSIS', 'FEATURE_MARKET_TRENDS',
        'FEATURE_SCENARIO_CREATE', 'FEATURE_SCENARIO_UPDATE', 'FEATURE_SCENARIO_VIEW',
        'FEATURE_PORTFOLIO_ADD', 'FEATURE_PDF_EXPORT', 'FEATURE_PASSWORD_CHANGE'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "AdminAction" AS ENUM (
        'USER_VIEW', 'USER_CREATE', 'USER_UPDATE', 'USER_DELETE',
        'USER_SUSPEND', 'USER_UNSUSPEND', 'USER_IMPERSONATE',
        'USER_DATA_EXPORT', 'USER_DATA_DELETE',
        'SUBSCRIPTION_VIEW', 'SUBSCRIPTION_OVERRIDE', 'SUBSCRIPTION_CANCEL', 'SUBSCRIPTION_REFUND',
        'WHITELIST_ADD', 'WHITELIST_UPDATE', 'WHITELIST_DELETE', 'WHITELIST_TOGGLE',
        'CACHE_INVALIDATE', 'CACHE_PRUNE',
        'MODEL_CREATE', 'MODEL_DELETE', 'MODEL_UPDATE',
        'QUOTA_UPDATE', 'ALERT_DISMISS'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "AuditActorType" AS ENUM (
        'ADMIN_USER',       -- Human admin via web UI
        'SYSTEM',           -- Automated system processes
        'CRON_JOB',         -- Scheduled cron jobs
        'API_AUTOMATION',   -- External API calls (future)
        'BACKGROUND_WORKER' -- Async worker processes
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

-- audit_logs table (system audit) — column names match actual Prisma-created DB schema
CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "userId" TEXT REFERENCES users(id),
    event "AuditEventType" NOT NULL,
    description TEXT,
    "ipAddress" TEXT,
    "userAgent" TEXT,
    metadata JSONB,
    success BOOLEAN NOT NULL DEFAULT true,
    error TEXT,
    action TEXT,
    "complianceFlags" JSONB,
    endpoint TEXT,
    "performanceMetrics" JSONB,
    "requestId" TEXT,
    resource TEXT,
    "resourceId" TEXT,
    "actorType" "AuditActorType",
    "adminId" TEXT,
    "adminEmail" TEXT,
    "securityContext" JSONB,
    "sessionId" TEXT,
    severity TEXT NOT NULL DEFAULT 'info'
);

CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_logs("createdAt");
CREATE INDEX IF NOT EXISTS idx_audit_event_type ON audit_logs(event);
CREATE INDEX IF NOT EXISTS idx_audit_severity ON audit_logs(severity);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs("userId");
CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_logs("sessionId");
CREATE INDEX IF NOT EXISTS idx_audit_request ON audit_logs("requestId");
CREATE INDEX IF NOT EXISTS idx_audit_endpoint ON audit_logs(endpoint);

-- admin_audit_log table (admin action tracking)
CREATE TABLE IF NOT EXISTS admin_audit_log (
    id TEXT PRIMARY KEY,
    "adminId" TEXT NOT NULL,
    "adminEmail" TEXT NOT NULL,
    "actorType" "AuditActorType" NOT NULL DEFAULT 'ADMIN_USER',
    action "AdminAction" NOT NULL,
    resource TEXT NOT NULL,
    "resourceId" TEXT,
    details JSONB NOT NULL,
    "ipAddress" TEXT NOT NULL,
    "userAgent" TEXT NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_admin_audit_admin_created ON admin_audit_log("adminId", "createdAt");
CREATE INDEX IF NOT EXISTS idx_admin_audit_actor_created ON admin_audit_log("actorType", "createdAt");
CREATE INDEX IF NOT EXISTS idx_admin_audit_action_created ON admin_audit_log(action, "createdAt");
CREATE INDEX IF NOT EXISTS idx_admin_audit_resource ON admin_audit_log(resource, "resourceId");
CREATE INDEX IF NOT EXISTS idx_admin_audit_created ON admin_audit_log("createdAt");

-- admin_sessions table
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

-- admin_two_factor table
CREATE TABLE IF NOT EXISTS admin_two_factor (
    id TEXT PRIMARY KEY,
    "userId" TEXT UNIQUE NOT NULL,
    secret TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT false,
    "backupCodes" TEXT[] NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL
);

-- system_alerts table
CREATE TABLE IF NOT EXISTS system_alerts (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    severity TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    "alertKey" TEXT UNIQUE NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    "firstSeen" TIMESTAMP(3) NOT NULL,
    "lastSeen" TIMESTAMP(3) NOT NULL,
    "occurrenceCount" INTEGER NOT NULL DEFAULT 1,
    dismissed BOOLEAN NOT NULL DEFAULT false,
    "actionRequired" BOOLEAN NOT NULL DEFAULT false,
    "expiresAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_system_alert_key ON system_alerts("alertKey");
CREATE INDEX IF NOT EXISTS idx_system_alert_type ON system_alerts(type);
CREATE INDEX IF NOT EXISTS idx_system_alert_severity ON system_alerts(severity);
CREATE INDEX IF NOT EXISTS idx_system_alert_dismissed ON system_alerts(dismissed);
CREATE INDEX IF NOT EXISTS idx_system_alert_action ON system_alerts("actionRequired");
CREATE INDEX IF NOT EXISTS idx_system_alert_expires ON system_alerts("expiresAt");
