-- Migration: 20251201_initial_schema
-- Description: Consolidated base schema for fresh database deployments.
--              All statements use IF NOT EXISTS / DO..EXCEPTION so this is
--              safe to run against existing databases too.
-- Date: 2025-12-01 (predates all incremental migrations)

-- ============================================================
-- 001: users
-- ============================================================

DO $$ BEGIN
    CREATE TYPE "UserRole" AS ENUM ('USER', 'ADMIN', 'SUPER_ADMIN');
EXCEPTION WHEN duplicate_object THEN null;
END $$;

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    email TEXT UNIQUE NOT NULL,
    "firstName" TEXT,
    "lastName" TEXT,
    "stripeCustomerId" TEXT UNIQUE,
    role "UserRole" NOT NULL DEFAULT 'USER',
    "hasDataTier" TEXT DEFAULT 'free',
    password TEXT,
    theme TEXT DEFAULT 'estara',
    "subscriptionTier" TEXT DEFAULT 'free',
    phone TEXT UNIQUE,
    "streetAddress" TEXT,
    city TEXT,
    state TEXT,
    "zipCode" TEXT,
    "investorProfile" JSONB,
    "iapPlatform" TEXT,
    "iapProductId" TEXT,
    "iapReceiptData" TEXT,
    "iapExpiresAt" TIMESTAMP(3),
    "iapLastValidated" TIMESTAMP(3),
    "appleOriginalTransactionId" TEXT UNIQUE,
    "appleEnvironment" TEXT,
    "suspendedAt" TIMESTAMP(3),
    "suspendedBy" TEXT,
    "suspendReason" TEXT,
    early_access_status TEXT
);

CREATE INDEX IF NOT EXISTS idx_user_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_user_stripe_customer ON users("stripeCustomerId");

-- ============================================================
-- 002: analysis_cache
-- ============================================================

CREATE TABLE IF NOT EXISTS analysis_cache (
    id TEXT PRIMARY KEY,
    key TEXT UNIQUE NOT NULL,
    "userId" TEXT NOT NULL,
    location TEXT NOT NULL,
    feature TEXT NOT NULL DEFAULT 'dual_agent_market_analysis',
    prompt TEXT,
    "promptHash" TEXT,
    complexity JSONB,
    "investorProfile" JSONB,
    "marketData" JSONB,
    content TEXT NOT NULL,
    "fullReport" TEXT,
    "metricsData" JSONB,
    "narrativeData" JSONB,
    metadata JSONB NOT NULL DEFAULT '{}',
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "expiresAt" TIMESTAMP(3) NOT NULL,
    "lastAccessedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "accessCount" INTEGER NOT NULL DEFAULT 0,
    "supersededBy" TEXT,
    "supersededAt" TIMESTAMP(3),
    "cacheHits" INTEGER NOT NULL DEFAULT 0,
    "generationCost" NUMERIC(10,6) NOT NULL DEFAULT 0,
    "savingsGenerated" NUMERIC(10,6) NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_analysis_cache_user_created ON analysis_cache("userId", "createdAt");
CREATE INDEX IF NOT EXISTS idx_analysis_cache_user_location ON analysis_cache("userId", location);
CREATE INDEX IF NOT EXISTS idx_analysis_cache_location ON analysis_cache(location);
CREATE INDEX IF NOT EXISTS idx_analysis_cache_feature ON analysis_cache(feature);
CREATE INDEX IF NOT EXISTS idx_analysis_cache_expires ON analysis_cache("expiresAt");

-- ============================================================
-- 003: auth_extended
-- ============================================================

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id TEXT PRIMARY KEY,
    token TEXT UNIQUE NOT NULL,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    "expiresAt" TIMESTAMP(3) NOT NULL,
    used BOOLEAN NOT NULL DEFAULT false,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_password_reset_token ON password_reset_tokens(token);
CREATE INDEX IF NOT EXISTS idx_password_reset_user ON password_reset_tokens("userId");
CREATE INDEX IF NOT EXISTS idx_password_reset_expires ON password_reset_tokens("expiresAt");

CREATE TABLE IF NOT EXISTS silent_login_sessions (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    "jwtToken" TEXT NOT NULL,
    "clientUrl" TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "expiresAt" TIMESTAMP(3) NOT NULL,
    "lastAccessedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    active BOOLEAN NOT NULL DEFAULT true
);

CREATE INDEX IF NOT EXISTS idx_silent_login_user ON silent_login_sessions("userId");
CREATE INDEX IF NOT EXISTS idx_silent_login_expires ON silent_login_sessions("expiresAt");
CREATE INDEX IF NOT EXISTS idx_silent_login_active ON silent_login_sessions(active);

CREATE TABLE IF NOT EXISTS email_verification_codes (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    code TEXT NOT NULL,
    "expiresAt" TIMESTAMP(3) NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT false,
    attempts INTEGER NOT NULL DEFAULT 0,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(email, code)
);

CREATE INDEX IF NOT EXISTS idx_email_verification_email ON email_verification_codes(email);
CREATE INDEX IF NOT EXISTS idx_email_verification_expires ON email_verification_codes("expiresAt");

-- ============================================================
-- 004: billing
-- ============================================================

DO $$ BEGIN
    CREATE TYPE "SubscriptionTier" AS ENUM (
        'FREE', 'REPORT_USER', 'INVESTOR', 'PROFESSIONAL',
        'AAPI_INVESTOR', 'AAPI_ALLOCATOR', 'ENTERPRISE',
        'ANNUAL_ACCESS', 'PROFESSIONAL_ALLOCATOR'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "SubscriptionStatus" AS ENUM (
        'FREE', 'ACTIVE', 'TRIALING', 'PAST_DUE', 'CANCELED',
        'UNPAID', 'INCOMPLETE', 'INCOMPLETE_EXPIRED', 'PAUSED', 'EXPIRED'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "InvoiceStatus" AS ENUM (
        'DRAFT', 'OPEN', 'PAID', 'VOID', 'UNCOLLECTIBLE'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "InvoiceProductType" AS ENUM (
        'SUBSCRIPTION', 'SINGLE_REPORT', 'REPORT_PACK', 'OVERAGE', 'OTHER'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "BillingFrequency" AS ENUM (
        'MONTHLY', 'QUARTERLY', 'ANNUAL'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "BillingEventType" AS ENUM (
        'CHECKOUT_STARTED', 'CHECKOUT_COMPLETED', 'CHECKOUT_FAILED',
        'SUBSCRIPTION_CREATED', 'SUBSCRIPTION_UPDATED', 'SUBSCRIPTION_CANCELED',
        'SUBSCRIPTION_RENEWED', 'SUBSCRIPTION_PAUSED', 'SUBSCRIPTION_RESUMED',
        'PAYMENT_SUCCEEDED', 'PAYMENT_FAILED', 'PAYMENT_REFUNDED',
        'INVOICE_CREATED', 'INVOICE_PAID', 'INVOICE_FAILED',
        'DISPUTE_CREATED', 'DISPUTE_WON', 'DISPUTE_LOST',
        'TRIAL_STARTED', 'TRIAL_ENDED', 'TRIAL_CONVERTED',
        'IAP_PURCHASE', 'IAP_RENEWAL', 'IAP_CANCELLATION', 'IAP_REFUND'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "RenewalEmailType" AS ENUM (
        'RENEWAL_REMINDER_30', 'RENEWAL_REMINDER_7', 'RENEWAL_REMINDER_1',
        'RENEWAL_CONFIRMATION', 'CANCELLATION_CONFIRMATION'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

CREATE TABLE IF NOT EXISTS subscriptions (
    id TEXT PRIMARY KEY,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    "userId" TEXT UNIQUE NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    "stripeSubscriptionId" TEXT UNIQUE,
    "stripePriceId" TEXT,
    "stripeCustomerId" TEXT,
    tier "SubscriptionTier" NOT NULL DEFAULT 'FREE',
    status "SubscriptionStatus" NOT NULL DEFAULT 'FREE',
    "currentPeriodStart" TIMESTAMP(3),
    "currentPeriodEnd" TIMESTAMP(3),
    "trialStart" TIMESTAMP(3),
    "trialEnd" TIMESTAMP(3),
    "canceledAt" TIMESTAMP(3),
    "cancelAtPeriodEnd" BOOLEAN NOT NULL DEFAULT false,
    metadata JSONB
);

CREATE TABLE IF NOT EXISTS invoices (
    id TEXT PRIMARY KEY,
    "stripeInvoiceId" TEXT UNIQUE NOT NULL,
    "stripeCustomerId" TEXT NOT NULL,
    "stripeSubscriptionId" TEXT,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    "invoiceNumber" TEXT,
    status "InvoiceStatus" NOT NULL,
    subtotal INTEGER NOT NULL,
    "taxAmount" INTEGER NOT NULL DEFAULT 0,
    total INTEGER NOT NULL,
    "amountPaid" INTEGER NOT NULL DEFAULT 0,
    "amountDue" INTEGER NOT NULL,
    currency TEXT NOT NULL DEFAULT 'usd',
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "dueDate" TIMESTAMP(3),
    "paidAt" TIMESTAMP(3),
    "periodStart" TIMESTAMP(3),
    "periodEnd" TIMESTAMP(3),
    "hostedInvoiceUrl" TEXT,
    "invoicePdfUrl" TEXT,
    description TEXT,
    "productType" "InvoiceProductType" NOT NULL,
    "emailSentAt" TIMESTAMP(3),
    "emailDelivered" BOOLEAN NOT NULL DEFAULT false,
    "updatedAt" TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_invoice_user ON invoices("userId");
CREATE INDEX IF NOT EXISTS idx_invoice_stripe ON invoices("stripeInvoiceId");
CREATE INDEX IF NOT EXISTS idx_invoice_customer ON invoices("stripeCustomerId");
CREATE INDEX IF NOT EXISTS idx_invoice_status ON invoices(status);
CREATE INDEX IF NOT EXISTS idx_invoice_created ON invoices("createdAt");

CREATE TABLE IF NOT EXISTS receipts (
    id TEXT PRIMARY KEY,
    "invoiceId" TEXT UNIQUE NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    "stripeChargeId" TEXT UNIQUE,
    "stripePaymentIntentId" TEXT,
    "receiptNumber" TEXT UNIQUE NOT NULL,
    amount INTEGER NOT NULL,
    currency TEXT NOT NULL DEFAULT 'usd',
    "paymentMethod" TEXT,
    "cardBrand" TEXT,
    "cardLast4" TEXT,
    "paidAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "stripeReceiptUrl" TEXT,
    "emailSentAt" TIMESTAMP(3),
    "emailDelivered" BOOLEAN NOT NULL DEFAULT false,
    "productDescription" TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_receipt_user ON receipts("userId");
CREATE INDEX IF NOT EXISTS idx_receipt_invoice ON receipts("invoiceId");
CREATE INDEX IF NOT EXISTS idx_receipt_paid ON receipts("paidAt");
CREATE INDEX IF NOT EXISTS idx_receipt_number ON receipts("receiptNumber");

CREATE TABLE IF NOT EXISTS billing_audit_logs (
    id TEXT PRIMARY KEY,
    "userId" TEXT,
    "stripeCustomerId" TEXT,
    "stripeSubscriptionId" TEXT,
    "stripePaymentIntentId" TEXT,
    "stripeInvoiceId" TEXT,
    "appleOriginalTransactionId" TEXT,
    "appleTransactionId" TEXT,
    "appleProductId" TEXT,
    "appleEnvironment" TEXT,
    "eventType" "BillingEventType" NOT NULL,
    "eventData" JSONB,
    "ipAddress" TEXT,
    "userAgent" TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_billing_audit_user ON billing_audit_logs("userId");
CREATE INDEX IF NOT EXISTS idx_billing_audit_customer ON billing_audit_logs("stripeCustomerId");
CREATE INDEX IF NOT EXISTS idx_billing_audit_sub ON billing_audit_logs("stripeSubscriptionId");
CREATE INDEX IF NOT EXISTS idx_billing_audit_apple ON billing_audit_logs("appleOriginalTransactionId");
CREATE INDEX IF NOT EXISTS idx_billing_audit_event ON billing_audit_logs("eventType");
CREATE INDEX IF NOT EXISTS idx_billing_audit_created ON billing_audit_logs("createdAt");

CREATE TABLE IF NOT EXISTS billing_cycles (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    tier "SubscriptionTier" NOT NULL,
    frequency "BillingFrequency" NOT NULL,
    "creditsGranted" DECIMAL(10, 6) NOT NULL,
    "startDate" TIMESTAMP(3) NOT NULL,
    "endDate" TIMESTAMP(3) NOT NULL,
    processed BOOLEAN NOT NULL DEFAULT false,
    "processedAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_billing_cycle_user ON billing_cycles("userId", "startDate");
CREATE INDEX IF NOT EXISTS idx_billing_cycle_end ON billing_cycles("endDate");
CREATE INDEX IF NOT EXISTS idx_billing_cycle_processed ON billing_cycles(processed);

CREATE TABLE IF NOT EXISTS checkout_evidence (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "stripePaymentIntentId" TEXT UNIQUE,
    "stripeSubscriptionId" TEXT,
    "termsAcceptedAt" TIMESTAMP(3) NOT NULL,
    "termsVersion" TEXT NOT NULL,
    "refundPolicyAccepted" BOOLEAN NOT NULL DEFAULT true,
    "chargebackPolicyAccepted" BOOLEAN NOT NULL DEFAULT true,
    "ipAddress" TEXT NOT NULL,
    "userAgent" TEXT NOT NULL,
    "deviceFingerprint" TEXT,
    "billingEmail" TEXT NOT NULL,
    "billingName" TEXT,
    "billingAddress" JSONB,
    "cardLast4" TEXT,
    "cardBrand" TEXT,
    amount DECIMAL(10, 2) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'usd',
    "productDescription" TEXT NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_checkout_evidence_user ON checkout_evidence("userId");
CREATE INDEX IF NOT EXISTS idx_checkout_evidence_sub ON checkout_evidence("stripeSubscriptionId");
CREATE INDEX IF NOT EXISTS idx_checkout_evidence_created ON checkout_evidence("createdAt");

CREATE TABLE IF NOT EXISTS renewal_notifications (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "subscriptionId" TEXT NOT NULL,
    "emailType" "RenewalEmailType" NOT NULL,
    "sentAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "emailContent" TEXT NOT NULL,
    "recipientEmail" TEXT NOT NULL,
    "renewalDate" TIMESTAMP(3) NOT NULL,
    "renewalAmount" DECIMAL(10, 2) NOT NULL,
    "sendgridMessageId" TEXT,
    delivered BOOLEAN NOT NULL DEFAULT false,
    "deliveredAt" TIMESTAMP(3),
    opened BOOLEAN NOT NULL DEFAULT false,
    "openedAt" TIMESTAMP(3)
);

CREATE INDEX IF NOT EXISTS idx_renewal_notif_user ON renewal_notifications("userId", "sentAt");
CREATE INDEX IF NOT EXISTS idx_renewal_notif_sub ON renewal_notifications("subscriptionId");
CREATE INDEX IF NOT EXISTS idx_renewal_notif_date ON renewal_notifications("renewalDate");

-- ============================================================
-- 005: scenarios
-- ============================================================

CREATE TABLE IF NOT EXISTS scenarios (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    parameters JSONB NOT NULL,
    results JSONB,
    tags JSONB NOT NULL DEFAULT '[]',
    favorite BOOLEAN NOT NULL DEFAULT false,
    "hasAIParameters" BOOLEAN NOT NULL DEFAULT false,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "lastModified" TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scenario_user_modified ON scenarios("userId", "lastModified");
CREATE INDEX IF NOT EXISTS idx_scenario_user_favorite ON scenarios("userId", favorite);

CREATE TABLE IF NOT EXISTS user_analysis_preferences (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "requestId" TEXT NOT NULL,
    hidden BOOLEAN NOT NULL DEFAULT false,
    favorited BOOLEAN NOT NULL DEFAULT false,
    notes TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    UNIQUE("userId", "requestId")
);

CREATE INDEX IF NOT EXISTS idx_user_analysis_pref_favorited ON user_analysis_preferences("userId", favorited);
CREATE INDEX IF NOT EXISTS idx_user_analysis_pref_request ON user_analysis_preferences("requestId");

-- ============================================================
-- 006: reports / entitlements
-- ============================================================

DO $$ BEGIN
    CREATE TYPE "AccessTier" AS ENUM (
        'INVESTOR', 'PROFESSIONAL', 'AAPI_INVESTOR', 'AAPI_ALLOCATOR',
        'ANNUAL_ACCESS', 'PROFESSIONAL_ALLOCATOR'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "AccessStatus" AS ENUM ('ACTIVE', 'CANCELLED', 'EXPIRED', 'PAUSED');
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "InvestorReportType" AS ENUM (
        'SNAPSHOT', 'INVESTOR', 'PROFESSIONAL',
        'ANNUAL_ACCESS', 'PROFESSIONAL_ALLOCATOR'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "InvestorReportStatus" AS ENUM (
        'PENDING', 'GENERATING', 'COMPLETE', 'FAILED', 'SUPERSEDED'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "ReportSourceType" AS ENUM (
        'SINGLE_PURCHASE', 'REPORT_PACK', 'ANNUAL_SUBSCRIPTION', 'FREE_SNAPSHOT', 'OVERAGE'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "SnapshotStatus" AS ENUM (
        'PENDING', 'GENERATING', 'COMPLETE', 'FAILED', 'CONVERTED'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

CREATE TABLE IF NOT EXISTS insight_access (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tier "AccessTier" NOT NULL,
    "billingFrequency" "BillingFrequency" NOT NULL DEFAULT 'ANNUAL',
    "reportsPerPeriod" INTEGER NOT NULL,
    "reportsPerYear" INTEGER,
    "reportsUsed" INTEGER NOT NULL DEFAULT 0,
    "rolloverReports" INTEGER NOT NULL DEFAULT 0,
    "lastRolloverDate" TIMESTAMP(3),
    "periodStartDate" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "periodEndDate" TIMESTAMP(3) NOT NULL,
    "startDate" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "endDate" TIMESTAMP(3) NOT NULL,
    "stripeSubId" TEXT UNIQUE,
    "stripePriceId" TEXT,
    status "AccessStatus" NOT NULL DEFAULT 'ACTIVE',
    "lastReportGeneratedAt" TIMESTAMP(3),
    "consumptionHistory" JSONB,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_access_user ON insight_access("userId");
CREATE INDEX IF NOT EXISTS idx_insight_access_status ON insight_access(status);
CREATE INDEX IF NOT EXISTS idx_insight_access_end_date ON insight_access("endDate");
CREATE INDEX IF NOT EXISTS idx_insight_access_period_end ON insight_access("periodEndDate");
CREATE INDEX IF NOT EXISTS idx_insight_access_stripe ON insight_access("stripeSubId");

CREATE TABLE IF NOT EXISTS report_packs (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    "totalReports" INTEGER NOT NULL DEFAULT 5,
    "usedReports" INTEGER NOT NULL DEFAULT 0,
    "purchasedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "stripePaymentId" TEXT,
    "lastUsedAt" TIMESTAMP(3),
    "consumptionHistory" JSONB,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_report_pack_user ON report_packs("userId");
CREATE INDEX IF NOT EXISTS idx_report_pack_stripe ON report_packs("stripePaymentId");

CREATE TABLE IF NOT EXISTS investor_reports (
    id TEXT PRIMARY KEY,
    "userId" TEXT REFERENCES users(id) ON DELETE CASCADE,
    email TEXT,
    "propertyId" TEXT,
    "propertyAddress" TEXT,
    type "InvestorReportType" NOT NULL DEFAULT 'INVESTOR',
    status "InvestorReportStatus" NOT NULL DEFAULT 'PENDING',
    "sourceType" "ReportSourceType" NOT NULL,
    "sourcePackId" TEXT REFERENCES report_packs(id),
    "sourceAccessId" TEXT REFERENCES insight_access(id),
    "stripePaymentId" TEXT,
    "investmentCriteria" JSONB,
    "metricsData" JSONB,
    "narrativeData" JSONB,
    "fullReport" TEXT,
    "pdfUrl" TEXT,
    "generationCost" DECIMAL(10, 6) NOT NULL DEFAULT 0,
    "generationTimeMs" INTEGER,
    "allocationDecrementedAt" TIMESTAMP(3),
    "allocationSnapshot" JSONB,
    "cacheKey" TEXT UNIQUE,
    "criteriaHash" TEXT,
    "cachedFromId" TEXT,
    "supersededById" TEXT,
    "supersededAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "completedAt" TIMESTAMP(3),
    "expiresAt" TIMESTAMP(3)
);

CREATE INDEX IF NOT EXISTS idx_investor_report_user ON investor_reports("userId");
CREATE INDEX IF NOT EXISTS idx_investor_report_email ON investor_reports(email);
CREATE INDEX IF NOT EXISTS idx_investor_report_status ON investor_reports(status);
CREATE INDEX IF NOT EXISTS idx_investor_report_type ON investor_reports(type);
CREATE INDEX IF NOT EXISTS idx_investor_report_created ON investor_reports("createdAt");
CREATE INDEX IF NOT EXISTS idx_investor_report_cache ON investor_reports("cacheKey");
CREATE INDEX IF NOT EXISTS idx_investor_report_criteria ON investor_reports("criteriaHash");

CREATE TABLE IF NOT EXISTS snapshot_requests (
    id TEXT PRIMARY KEY,
    "sessionId" TEXT NOT NULL,
    "userId" TEXT REFERENCES users(id),
    email TEXT,
    criteria JSONB NOT NULL,
    "criteriaHash" TEXT,
    location TEXT,
    status "SnapshotStatus" NOT NULL DEFAULT 'PENDING',
    "snapshotNumber" INTEGER NOT NULL DEFAULT 1,
    "propertiesFound" INTEGER NOT NULL DEFAULT 0,
    properties JSONB,
    "resultId" TEXT,
    "cacheKey" TEXT,
    "cachedAt" TIMESTAMP(3),
    "cacheExpiresAt" TIMESTAMP(3),
    "ipAddress" TEXT,
    "userAgent" TEXT,
    source TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "completedAt" TIMESTAMP(3),
    "convertedAt" TIMESTAMP(3)
);

CREATE INDEX IF NOT EXISTS idx_snapshot_session ON snapshot_requests("sessionId");
CREATE INDEX IF NOT EXISTS idx_snapshot_user ON snapshot_requests("userId");
CREATE INDEX IF NOT EXISTS idx_snapshot_email ON snapshot_requests(email);
CREATE INDEX IF NOT EXISTS idx_snapshot_email_status ON snapshot_requests(email, status);
CREATE INDEX IF NOT EXISTS idx_snapshot_status ON snapshot_requests(status);
CREATE INDEX IF NOT EXISTS idx_snapshot_created ON snapshot_requests("createdAt");
CREATE INDEX IF NOT EXISTS idx_snapshot_cache ON snapshot_requests("cacheKey");
CREATE INDEX IF NOT EXISTS idx_snapshot_criteria ON snapshot_requests("criteriaHash");

-- ============================================================
-- 007: admin / audit
-- ============================================================

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
        'SYSTEM_ERROR'
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
        'MODEL_CREATE', 'MODEL_UPDATE', 'MODEL_DELETE',
        'QUOTA_UPDATE', 'ALERT_DISMISS'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

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

CREATE TABLE IF NOT EXISTS admin_audit_log (
    id TEXT PRIMARY KEY,
    "adminId" TEXT NOT NULL,
    "adminEmail" TEXT NOT NULL,
    action "AdminAction" NOT NULL,
    resource TEXT NOT NULL,
    "resourceId" TEXT,
    details JSONB NOT NULL,
    "ipAddress" TEXT NOT NULL,
    "userAgent" TEXT NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_admin_audit_admin_created ON admin_audit_log("adminId", "createdAt");
CREATE INDEX IF NOT EXISTS idx_admin_audit_action_created ON admin_audit_log(action, "createdAt");
CREATE INDEX IF NOT EXISTS idx_admin_audit_resource ON admin_audit_log(resource, "resourceId");
CREATE INDEX IF NOT EXISTS idx_admin_audit_created ON admin_audit_log("createdAt");

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

CREATE TABLE IF NOT EXISTS admin_two_factor (
    id TEXT PRIMARY KEY,
    "userId" TEXT UNIQUE NOT NULL,
    secret TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT false,
    "backupCodes" TEXT[] NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL
);

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

-- ============================================================
-- 008: website
-- ============================================================

CREATE TABLE IF NOT EXISTS contact_submissions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT NOT NULL,
    company TEXT,
    phone TEXT,
    subject TEXT,
    message TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'GENERAL',
    "categoryConfidence" DOUBLE PRECISION,
    "categoryReasoning" TEXT,
    source TEXT,
    "inquiryType" TEXT,
    status TEXT NOT NULL DEFAULT 'NEW',
    "assignedTo" TEXT,
    notes TEXT,
    "firstResponseAt" TIMESTAMP(3),
    "resolvedAt" TIMESTAMP(3),
    "responseCount" INTEGER NOT NULL DEFAULT 0,
    "ipAddress" TEXT,
    "userAgent" TEXT,
    "notificationSent" BOOLEAN NOT NULL DEFAULT false,
    "confirmationSent" BOOLEAN NOT NULL DEFAULT false,
    "sendgridMessageId" TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_contact_email ON contact_submissions(email);
CREATE INDEX IF NOT EXISTS idx_contact_status ON contact_submissions(status);
CREATE INDEX IF NOT EXISTS idx_contact_created ON contact_submissions("createdAt");

CREATE TABLE IF NOT EXISTS early_access (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    "requestedAt" TIMESTAMP(3),
    source TEXT,
    metadata JSONB,
    contacted BOOLEAN NOT NULL DEFAULT false,
    invited BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'PENDING',
    notes TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_early_access_email ON early_access(email);
CREATE INDEX IF NOT EXISTS idx_early_access_created ON early_access("createdAt");
CREATE INDEX IF NOT EXISTS idx_early_access_invited ON early_access(invited);

CREATE TABLE IF NOT EXISTS guest_sessions (
    id TEXT PRIMARY KEY,
    token TEXT UNIQUE NOT NULL,
    "ipAddress" TEXT,
    "userAgent" TEXT,
    "deviceFingerprint" TEXT,
    "expiresAt" TIMESTAMP(3) NOT NULL,
    "snapshotsUsed" INTEGER NOT NULL DEFAULT 0,
    "lastActivityAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_guest_session_token ON guest_sessions(token);
CREATE INDEX IF NOT EXISTS idx_guest_session_expires ON guest_sessions("expiresAt");
CREATE INDEX IF NOT EXISTS idx_guest_session_created ON guest_sessions("createdAt");

-- ============================================================
-- 009: discovery sessions (required before 20260131 migration)
-- ============================================================

CREATE TABLE IF NOT EXISTS discovery_sessions (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    "searchCriteria" JSONB NOT NULL,
    location TEXT NOT NULL,
    "propertyCount" INTEGER NOT NULL DEFAULT 0,
    "cachedPropertyIds" TEXT[] NOT NULL DEFAULT '{}',
    name TEXT,
    notes TEXT,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "lastAccessedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "archivedAt" TIMESTAMP(3),
    "expiresAt" TIMESTAMP(3),
    "chatSessionCount" INTEGER NOT NULL DEFAULT 0,
    "evaluationCount" INTEGER NOT NULL DEFAULT 0,
    median_price INTEGER,
    mode_price INTEGER
);

CREATE INDEX IF NOT EXISTS idx_discovery_session_user_created ON discovery_sessions("userId", "createdAt" DESC);
CREATE INDEX IF NOT EXISTS idx_discovery_session_user_status ON discovery_sessions("userId", status);
CREATE INDEX IF NOT EXISTS idx_discovery_session_status ON discovery_sessions(status);
CREATE INDEX IF NOT EXISTS idx_discovery_session_expires ON discovery_sessions("expiresAt");
CREATE INDEX IF NOT EXISTS idx_discovery_session_archived ON discovery_sessions("archivedAt") WHERE "archivedAt" IS NOT NULL;

CREATE TABLE IF NOT EXISTS discovery_session_activities (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "activityType" TEXT NOT NULL,
    "activityId" TEXT NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE("discoverySessionId", "activityType", "activityId")
);

CREATE INDEX IF NOT EXISTS idx_activity_session ON discovery_session_activities("discoverySessionId");
CREATE INDEX IF NOT EXISTS idx_activity_type ON discovery_session_activities("activityType");

CREATE TABLE IF NOT EXISTS discovery_session_properties (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "listingId" TEXT NOT NULL,
    address TEXT NOT NULL,
    city TEXT NOT NULL,
    state TEXT NOT NULL,
    "zipCode" TEXT,
    price INTEGER NOT NULL,
    "estimatedRent" INTEGER,
    "capRateMin" NUMERIC(5,2),
    "capRateMax" NUMERIC(5,2),
    beds INTEGER NOT NULL DEFAULT 0,
    baths NUMERIC(3,1) NOT NULL DEFAULT 0,
    sqft INTEGER,
    "yearBuilt" INTEGER,
    "propertyType" TEXT,
    "listingDate" TEXT,
    "daysOnMarket" INTEGER,
    "imageUrl" TEXT,
    "listingSearchUrl" TEXT,
    "googleSearchUrl" TEXT,
    latitude NUMERIC(10,7),
    longitude NUMERIC(10,7),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE("discoverySessionId", "listingId")
);

CREATE INDEX IF NOT EXISTS idx_session_properties_session ON discovery_session_properties("discoverySessionId");
CREATE INDEX IF NOT EXISTS idx_session_properties_listing ON discovery_session_properties("listingId");

CREATE TABLE IF NOT EXISTS discovery_session_evaluations (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "propertyId" TEXT NOT NULL,
    address TEXT NOT NULL,
    price INTEGER NOT NULL,
    "estimatedRent" INTEGER,
    scenarios JSONB NOT NULL,
    recommendation TEXT,
    "riskLevel" TEXT,
    score INTEGER,
    status TEXT NOT NULL DEFAULT 'COMPLETED',
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE("discoverySessionId", "propertyId")
);

CREATE INDEX IF NOT EXISTS idx_session_evaluations_session ON discovery_session_evaluations("discoverySessionId");
CREATE INDEX IF NOT EXISTS idx_session_evaluations_property ON discovery_session_evaluations("propertyId");

-- ============================================================
-- 010: property_cache
-- ============================================================

CREATE TABLE IF NOT EXISTS property_cache (
    id SERIAL PRIMARY KEY,
    cache_key TEXT UNIQUE NOT NULL,
    provider TEXT NOT NULL,
    property_id TEXT NOT NULL,
    content JSONB NOT NULL,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_accessed_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    access_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_property_cache_key ON property_cache(cache_key);
CREATE INDEX IF NOT EXISTS idx_property_cache_created ON property_cache(created_at);
CREATE INDEX IF NOT EXISTS idx_property_cache_provider_property ON property_cache(provider, property_id);

-- ============================================================
-- 011: ai_scoring_cache
-- ============================================================

CREATE TABLE IF NOT EXISTS ai_scoring_cache (
    id SERIAL PRIMARY KEY,
    cache_key TEXT UNIQUE NOT NULL,
    properties_hash TEXT NOT NULL,
    strategy TEXT NOT NULL,
    risk_tolerance TEXT NOT NULL,
    scored_properties JSONB NOT NULL,
    property_count INTEGER NOT NULL,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP(3) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ai_scoring_cache_key ON ai_scoring_cache(cache_key);
CREATE INDEX IF NOT EXISTS idx_ai_scoring_cache_expires ON ai_scoring_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_ai_scoring_cache_hash ON ai_scoring_cache(properties_hash);

-- ============================================================
-- 012: portfolio
-- ============================================================

CREATE TABLE IF NOT EXISTS v2_portfolio_properties (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    city TEXT NOT NULL,
    state TEXT NOT NULL,
    zip_code TEXT NOT NULL,
    property_type TEXT,
    bedrooms INTEGER,
    bathrooms DOUBLE PRECISION,
    sqft INTEGER,
    year_built INTEGER,
    purchase_price DOUBLE PRECISION NOT NULL,
    purchase_date TIMESTAMP NOT NULL,
    current_value DOUBLE PRECISION,
    last_valued_at TIMESTAMP,
    monthly_rent DOUBLE PRECISION,
    vacancy_rate DOUBLE PRECISION,
    expenses JSONB,
    mortgage_balance DOUBLE PRECISION,
    mortgage_rate DOUBLE PRECISION,
    mortgage_payment DOUBLE PRECISION,
    loan_term_years INTEGER,
    lat DOUBLE PRECISION,
    lng DOUBLE PRECISION,
    acquisition_type TEXT NOT NULL DEFAULT 'purchase',
    expense_frequency TEXT NOT NULL DEFAULT 'monthly',
    revenue_frequency TEXT NOT NULL DEFAULT 'monthly',
    sale_date TIMESTAMP,
    sale_price DOUBLE PRECISION,
    status TEXT NOT NULL DEFAULT 'active',
    property_status VARCHAR NOT NULL DEFAULT 'rented',
    last_confirmed_at TIMESTAMP,
    notes TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS v2_portfolio_snapshots (
    id VARCHAR(255) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    snapshot_date TIMESTAMP NOT NULL,
    total_value DOUBLE PRECISION NOT NULL DEFAULT 0,
    total_equity DOUBLE PRECISION NOT NULL DEFAULT 0,
    total_debt DOUBLE PRECISION NOT NULL DEFAULT 0,
    monthly_cash_flow DOUBLE PRECISION NOT NULL DEFAULT 0,
    portfolio_cap_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    property_count INTEGER NOT NULL DEFAULT 0,
    metrics_json JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_user_id ON v2_portfolio_snapshots(user_id);
CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_date ON v2_portfolio_snapshots(snapshot_date DESC);
CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_user_date ON v2_portfolio_snapshots(user_id, snapshot_date DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_portfolio_snapshots_unique ON v2_portfolio_snapshots(user_id, snapshot_date);

CREATE TABLE IF NOT EXISTS v2_baseline_changes (
    id VARCHAR(255) PRIMARY KEY,
    property_id VARCHAR(255) NOT NULL REFERENCES v2_portfolio_properties(id) ON DELETE CASCADE,
    field VARCHAR(50) NOT NULL,
    effective_date TIMESTAMP NOT NULL,
    new_value DOUBLE PRECISION NOT NULL,
    previous_value DOUBLE PRECISION,
    note TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_baseline_changes_property_id ON v2_baseline_changes(property_id);
CREATE INDEX IF NOT EXISTS idx_baseline_changes_effective_date ON v2_baseline_changes(effective_date);
CREATE UNIQUE INDEX IF NOT EXISTS idx_baseline_changes_unique ON v2_baseline_changes(property_id, field, effective_date);

CREATE TABLE IF NOT EXISTS v2_portfolio_adjustments (
    id VARCHAR(255) PRIMARY KEY,
    property_id VARCHAR(255) NOT NULL REFERENCES v2_portfolio_properties(id) ON DELETE CASCADE,
    month TIMESTAMP NOT NULL,
    type VARCHAR(20) NOT NULL,
    amount DOUBLE PRECISION NOT NULL,
    note TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_adjustments_property_id ON v2_portfolio_adjustments(property_id);
CREATE INDEX IF NOT EXISTS idx_adjustments_month ON v2_portfolio_adjustments(month);
CREATE UNIQUE INDEX IF NOT EXISTS idx_adjustments_unique ON v2_portfolio_adjustments(property_id, month, type);

-- ============================================================
-- 013: system_cache
-- ============================================================

CREATE TABLE IF NOT EXISTS system_cache (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_system_cache_expires_at ON system_cache(expires_at);

-- ============================================================
-- 014: admin_extended (vendors, contracts, cron)
-- ============================================================

DO $$ BEGIN
    CREATE TYPE "VendorCategory" AS ENUM (
        'AI_PROVIDER', 'DATA_PROVIDER', 'INFRASTRUCTURE',
        'PAYMENT', 'EMAIL', 'MONITORING'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "VendorBillingModel" AS ENUM (
        'PAYGO', 'MONTHLY', 'ANNUAL', 'FREE_TIER', 'USAGE_BASED'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE "VendorHealthStatus" AS ENUM (
        'OPERATIONAL', 'DEGRADED', 'DOWN', 'UNKNOWN', 'MAINTENANCE'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

CREATE TABLE IF NOT EXISTS vendor_configs (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    "displayName" TEXT NOT NULL,
    category "VendorCategory" NOT NULL,
    "billingModel" "VendorBillingModel" NOT NULL,
    "billingCycleDay" INTEGER,
    "paymentDueDate" TIMESTAMP(3),
    "monthlyCost" NUMERIC(10, 2),
    "apiKeyEnvVar" TEXT,
    "apiKeyExpiry" TIMESTAMP(3),
    "adminKeyEnvVar" TEXT,
    "costApiEndpoint" TEXT,
    "usageApiEndpoint" TEXT,
    "costApiBaseUrl" TEXT,
    "healthCheckUrl" TEXT,
    "lastHealthCheck" TIMESTAMP(3),
    "healthStatus" "VendorHealthStatus" NOT NULL DEFAULT 'UNKNOWN',
    "errorRateThreshold" DOUBLE PRECISION NOT NULL DEFAULT 0.05,
    "errorRateCurrent" DOUBLE PRECISION NOT NULL DEFAULT 0,
    "errorRateUpdatedAt" TIMESTAMP(3),
    "currentBalance" NUMERIC(10, 2),
    "balanceAlertThreshold" NUMERIC(10, 2),
    "lastBalanceCheck" TIMESTAMP(3),
    "totalRequests" INTEGER NOT NULL DEFAULT 0,
    "totalCost" NUMERIC(10, 2) NOT NULL DEFAULT 0,
    "isActive" BOOLEAN NOT NULL DEFAULT true,
    "isPrimary" BOOLEAN NOT NULL DEFAULT false,
    notes TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_vendor_config_category ON vendor_configs(category);
CREATE INDEX IF NOT EXISTS idx_vendor_config_active ON vendor_configs("isActive");

CREATE TABLE IF NOT EXISTS vendor_contracts (
    id TEXT PRIMARY KEY,
    "vendorId" TEXT NOT NULL REFERENCES vendor_configs(id) ON DELETE CASCADE,
    "startDate" TIMESTAMP(3) NOT NULL,
    "endDate" TIMESTAMP(3),
    "autoRenew" BOOLEAN NOT NULL DEFAULT false,
    "renewalTermMonths" INTEGER,
    "terminationNoticeDays" INTEGER,
    "pricingTiers" JSONB,
    "usageLimits" JSONB,
    "slaTerms" JSONB,
    "legalRestrictions" TEXT,
    "contactName" TEXT,
    "contactEmail" TEXT,
    notes TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_vendor_contracts_vendor ON vendor_contracts("vendorId");
CREATE INDEX IF NOT EXISTS idx_vendor_contracts_end_date ON vendor_contracts("endDate");

CREATE TABLE IF NOT EXISTS terms_acceptances (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    version TEXT NOT NULL,
    "acceptedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "ipAddress" TEXT,
    "userAgent" TEXT,
    UNIQUE("userId", version)
);

CREATE INDEX IF NOT EXISTS idx_terms_acceptances_user ON terms_acceptances("userId");
CREATE INDEX IF NOT EXISTS idx_terms_acceptances_version ON terms_acceptances(version);

CREATE TABLE IF NOT EXISTS admin_credits (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount NUMERIC(10, 2) NOT NULL,
    reason TEXT NOT NULL,
    "grantedBy" TEXT NOT NULL,
    applied BOOLEAN NOT NULL DEFAULT false,
    "appliedAt" TIMESTAMP(3),
    "expiresAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_admin_credits_user ON admin_credits("userId");
CREATE INDEX IF NOT EXISTS idx_admin_credits_expires ON admin_credits("expiresAt");

DO $$ BEGIN
    CREATE TYPE "CronJobStatus" AS ENUM (
        'SUCCESS', 'FAILED', 'TIMEOUT', 'RUNNING', 'SKIPPED'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

CREATE TABLE IF NOT EXISTS cron_job_configs (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    description TEXT NOT NULL,
    schedule TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    "isRequired" BOOLEAN NOT NULL DEFAULT true,
    "isConfigured" BOOLEAN NOT NULL DEFAULT false,
    "isEnabled" BOOLEAN NOT NULL DEFAULT true,
    "lastRun" TIMESTAMP(3),
    "lastRunStatus" "CronJobStatus",
    "lastRunDuration" INTEGER,
    "lastRunError" TEXT,
    "consecutiveFailures" INTEGER NOT NULL DEFAULT 0,
    "alertOnFailure" BOOLEAN NOT NULL DEFAULT true,
    "maxFailures" INTEGER NOT NULL DEFAULT 3,
    "timeoutMs" INTEGER NOT NULL DEFAULT 60000,
    "totalRuns" INTEGER NOT NULL DEFAULT 0,
    "successfulRuns" INTEGER NOT NULL DEFAULT 0,
    "failedRuns" INTEGER NOT NULL DEFAULT 0,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3)
);

CREATE TABLE IF NOT EXISTS cron_job_runs (
    id TEXT PRIMARY KEY,
    "cronJobId" TEXT NOT NULL REFERENCES cron_job_configs(id) ON DELETE CASCADE,
    status "CronJobStatus" NOT NULL,
    duration INTEGER,
    error TEXT,
    output JSONB,
    "triggeredBy" TEXT,
    "startedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "completedAt" TIMESTAMP(3)
);

CREATE INDEX IF NOT EXISTS idx_cron_job_runs_job ON cron_job_runs("cronJobId", "startedAt" DESC);
CREATE INDEX IF NOT EXISTS idx_cron_job_runs_status ON cron_job_runs(status);

-- ============================================================
-- 016: user_consents
-- ============================================================

DO $$ BEGIN
    CREATE TYPE "ConsentType" AS ENUM (
        'MARKETING', 'ANALYTICS', 'COOKIES', 'DATA_SHARING', 'TERMS_OF_SERVICE', 'PRIVACY_POLICY'
    );
EXCEPTION WHEN duplicate_object THEN null;
END $$;

CREATE TABLE IF NOT EXISTS user_consents (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id),
    "consentType" "ConsentType" NOT NULL,
    version TEXT NOT NULL,
    granted BOOLEAN NOT NULL,
    "ipAddress" TEXT NOT NULL,
    "userAgent" TEXT NOT NULL,
    "timestamp" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- 017: whitelisted_emails
-- ============================================================

DO $$ BEGIN
    CREATE TYPE "WhitelistType" AS ENUM ('EMAIL', 'DOMAIN');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS whitelisted_emails (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    name TEXT,
    type "WhitelistType" NOT NULL DEFAULT 'EMAIL',
    "addedBy" TEXT,
    reason TEXT,
    active BOOLEAN NOT NULL DEFAULT true,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_whitelisted_emails_email ON whitelisted_emails(email);
CREATE INDEX IF NOT EXISTS idx_whitelisted_emails_active ON whitelisted_emails(active);
CREATE INDEX IF NOT EXISTS idx_whitelisted_emails_type ON whitelisted_emails(type);

-- ============================================================
-- 018: webauthn_credentials
-- ============================================================

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

-- ============================================================
-- 019: ai_usage
-- ============================================================

CREATE TABLE IF NOT EXISTS ai_usage (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "sessionId" TEXT NOT NULL,
    model TEXT NOT NULL,
    "inputTokens" INTEGER NOT NULL,
    "outputTokens" INTEGER NOT NULL,
    "cacheCreationTokens" INTEGER NOT NULL DEFAULT 0,
    "cacheReadTokens" INTEGER NOT NULL DEFAULT 0,
    "totalTokens" INTEGER NOT NULL,
    "baseInputCost" NUMERIC(10,6) NOT NULL,
    "cacheCreationCost" NUMERIC(10,6) NOT NULL DEFAULT 0,
    "cacheReadCost" NUMERIC(10,6) NOT NULL DEFAULT 0,
    "outputCost" NUMERIC(10,6) NOT NULL,
    "totalCost" NUMERIC(10,6) NOT NULL,
    "requestType" TEXT NOT NULL,
    feature TEXT NOT NULL,
    location TEXT,
    "requestId" TEXT NOT NULL,
    "userAgent" TEXT,
    "ipAddress" TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    "actualResponseTime" INTEGER,
    "agentType" TEXT,
    "agentVersion" TEXT,
    "apiLatency" INTEGER,
    "cacheHit" BOOLEAN NOT NULL DEFAULT false,
    "contentFiltered" BOOLEAN NOT NULL DEFAULT false,
    "errorCode" TEXT,
    "errorMessage" TEXT,
    "errorType" TEXT,
    "fallbackModel" TEXT,
    "fallbackUsed" BOOLEAN NOT NULL DEFAULT false,
    "processingTime" INTEGER,
    "queueTime" INTEGER,
    "responseComplexity" TEXT,
    "responseLength" INTEGER,
    "retryCount" INTEGER NOT NULL DEFAULT 0,
    "securityProcessed" BOOLEAN NOT NULL DEFAULT true,
    "userRating" INTEGER,
    "validationScore" NUMERIC
);

-- ============================================================
-- 020: evaluation_chat
-- ============================================================

CREATE TABLE IF NOT EXISTS evaluation_chat_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    property_ids TEXT[] DEFAULT '{}',
    cached_property_ids TEXT[] DEFAULT '{}',
    investor_profile JSONB,
    portfolio_snapshot JSONB,
    discovery_session_id TEXT,      -- ADR-098: direct FK to discovery session
    pipeline_deal_id UUID,          -- ADR-101: pipeline deal context
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(3) NOT NULL
);

CREATE TABLE IF NOT EXISTS evaluation_chat_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    parsed_blocks JSONB,
    token_usage JSONB,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- 021: v2_evaluations
-- ============================================================

DO $$ BEGIN
    CREATE TYPE "V2SubscriptionTier" AS ENUM (
        'V2_PROFESSIONAL', 'V2_ADVANCED', 'V2_PRIVATE',
        'V2_ANNUAL_ACCESS', 'V2_PROFESSIONAL_ALLOCATOR', 'EARLY_ACCESS'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE "V2EvaluationStatus" AS ENUM ('DRAFT', 'COMPLETED', 'EXPORTED');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS v2_evaluation_quotas (
    id TEXT PRIMARY KEY,
    user_id TEXT UNIQUE NOT NULL,
    tier "V2SubscriptionTier" NOT NULL,
    annual_limit INTEGER NOT NULL,
    used_this_period INTEGER NOT NULL DEFAULT 0,
    period_start_date TIMESTAMP(3) NOT NULL,
    period_end_date TIMESTAMP(3) NOT NULL,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(3) NOT NULL
);

CREATE TABLE IF NOT EXISTS v2_evaluations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    property_id TEXT NOT NULL,
    property_address TEXT NOT NULL,
    property_city TEXT NOT NULL,
    property_state TEXT NOT NULL,
    property_zip TEXT,
    property_details JSONB,
    purchase_price DOUBLE PRECISION NOT NULL,
    down_payment_pct DOUBLE PRECISION NOT NULL,
    interest_rate DOUBLE PRECISION NOT NULL,
    loan_term_years INTEGER NOT NULL,
    monthly_rent DOUBLE PRECISION NOT NULL,
    vacancy_rate_pct DOUBLE PRECISION NOT NULL,
    maintenance_cost DOUBLE PRECISION NOT NULL,
    property_tax DOUBLE PRECISION NOT NULL,
    insurance DOUBLE PRECISION NOT NULL,
    hoa_fees DOUBLE PRECISION,
    appreciation_rate DOUBLE PRECISION NOT NULL,
    scenarios JSONB,
    sensitivity_data JSONB,
    status "V2EvaluationStatus" NOT NULL DEFAULT 'DRAFT',
    chat_session_id TEXT,           -- ADR-098: direct FK to chat session
    discovery_session_id TEXT,      -- ADR-098: direct FK to discovery session
    market_context JSONB,           -- ADR-098: market conditions at time of evaluation
    property_snapshot JSONB,        -- ADR-093: full enriched property data at evaluation time
    pipeline_property_id UUID,      -- ADR-101: pipeline property context
    pipeline_deal_id UUID,          -- ADR-101: pipeline deal context
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(3) NOT NULL
);

CREATE TABLE IF NOT EXISTS v2_decision_records (
    id TEXT PRIMARY KEY,
    evaluation_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memo_content JSONB NOT NULL,
    pdf_url TEXT,
    exported_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- 022: analysis_jobs
-- ============================================================

DO $$ BEGIN
    CREATE TYPE "AnalysisJobType" AS ENUM (
        'MARKET_ANALYSIS', 'INVESTMENT_PLANNING', 'INVESTOR_REPORT', 'EVALUATION_CHAT'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE "AnalysisStatus" AS ENUM (
        'QUEUED', 'AGENT1_RUNNING', 'AGENT1_COMPLETE',
        'AGENT2_RUNNING', 'AGENT2_COMPLETE',
        'STORING', 'COMPLETED', 'FAILED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS analysis_jobs (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "jobType" "AnalysisJobType" NOT NULL DEFAULT 'MARKET_ANALYSIS',
    status "AnalysisStatus" NOT NULL DEFAULT 'QUEUED',
    progress INTEGER NOT NULL DEFAULT 0,
    location TEXT NOT NULL,
    criteria JSONB,
    "metricsData" JSONB,
    "metricsError" TEXT,
    "narrativeData" JSONB,
    "narrativeError" TEXT,
    "reportId" TEXT,
    "queuedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "startedAt" TIMESTAMP(3),
    "completedAt" TIMESTAMP(3),
    "dismissedAt" TIMESTAMP(3),
    error TEXT,
    "retryCount" INTEGER NOT NULL DEFAULT 0
);

-- ============================================================
-- 023: usage_tables
-- ============================================================

CREATE TABLE IF NOT EXISTS investment_plan_usage (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    month INTEGER NOT NULL,
    year INTEGER NOT NULL,
    "picksUsed" INTEGER NOT NULL DEFAULT 0,
    "lastPickAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    UNIQUE("userId", month, year)
);

CREATE TABLE IF NOT EXISTS area_comparison_usage (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    month INTEGER NOT NULL,
    year INTEGER NOT NULL,
    "comparisonsUsed" INTEGER NOT NULL DEFAULT 0,
    "lastComparisonAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    UNIQUE("userId", month, year)
);

CREATE TABLE IF NOT EXISTS vendor_usage_summaries (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "vendorName" TEXT NOT NULL,
    month INTEGER NOT NULL,
    year INTEGER NOT NULL,
    "totalRequests" INTEGER NOT NULL DEFAULT 0,
    "totalCost" NUMERIC(10,6) NOT NULL,
    "monthlyLimit" INTEGER,
    "limitExceeded" BOOLEAN NOT NULL DEFAULT false,
    breakdown JSONB,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL,
    UNIQUE("userId", "vendorName", month, year)
);

-- ============================================================
-- 024: cached_properties
-- ============================================================

CREATE TABLE IF NOT EXISTS cached_properties (
    id TEXT PRIMARY KEY,
    listing_id TEXT UNIQUE NOT NULL,
    provider TEXT NOT NULL,
    address TEXT NOT NULL,
    city TEXT NOT NULL,
    state TEXT NOT NULL,
    zip_code TEXT,
    price INTEGER NOT NULL,
    beds INTEGER,
    baths DOUBLE PRECISION,
    sqft INTEGER,
    estimated_rent INTEGER,
    cap_rate DOUBLE PRECISION,
    listing_url TEXT,
    image_url TEXT,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- 025: early_access_program
-- ============================================================

CREATE TABLE IF NOT EXISTS early_access_requests (
    id                          TEXT PRIMARY KEY,
    email                       TEXT NOT NULL,
    first_name                  TEXT NOT NULL,
    last_name                   TEXT NOT NULL,
    company                     TEXT,
    use_case                    TEXT NOT NULL,
    portfolio_size              TEXT,
    linkedin_url                TEXT,
    primary_markets             TEXT,
    key_investment_decisions    TEXT,
    current_analytical_approach TEXT,
    status                      TEXT NOT NULL DEFAULT 'pending',
    user_id                     TEXT,
    admin_notes                 TEXT,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at                 TIMESTAMPTZ,
    reviewed_by                 TEXT
);

CREATE TABLE IF NOT EXISTS password_setup_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    token      TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- 029: pipeline (ADR-101/ADR-104)
-- ============================================================

CREATE TABLE IF NOT EXISTS pipeline_deals (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          TEXT NOT NULL,
    name             TEXT NOT NULL,
    source           TEXT NOT NULL DEFAULT 'broker',
    -- source: broker | off-market | syndication | jv | auction | direct | other
    status           TEXT NOT NULL DEFAULT 'under_review',
    -- status: under_review | memo_generated | passed | proceeding | closed
    notes            TEXT,
    property_count   INTEGER NOT NULL DEFAULT 0,
    memo_count       INTEGER NOT NULL DEFAULT 0,
    portfolio_excluded BOOLEAN NOT NULL DEFAULT FALSE,
    last_activity_at TIMESTAMPTZ,
    closed_outcome   TEXT,          -- ADR-104: acquired | rejected | other | NULL
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pipeline_deals_user_id        ON pipeline_deals(user_id);
CREATE INDEX IF NOT EXISTS idx_pipeline_deals_status         ON pipeline_deals(status);
CREATE INDEX IF NOT EXISTS idx_pipeline_deals_user_status    ON pipeline_deals(user_id, status);

CREATE TABLE IF NOT EXISTS pipeline_properties (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_deal_id  UUID NOT NULL REFERENCES pipeline_deals(id) ON DELETE CASCADE,
    address           TEXT NOT NULL,
    city              TEXT,
    state             TEXT,
    zip               TEXT,
    property_type     TEXT,
    -- property_type: sfh | multifamily | condo | townhouse | commercial | nnn | other
    beds              NUMERIC,
    baths             NUMERIC,
    sqft              INTEGER,
    year_built        INTEGER,
    units             INTEGER,
    asking_price      NUMERIC,
    target_price      NUMERIC,
    down_payment_pct  NUMERIC,
    financing_type    TEXT,
    -- financing_type: conventional | cash | other
    interest_rate     NUMERIC,
    broker_rent       NUMERIC,
    system_rent       NUMERIC,
    current_occupancy NUMERIC,
    expense_overrides JSONB,
    unit_mix          JSONB,     -- per-unit-type breakdown for MF/condo (ADR-102)
    lot_sqft          INTEGER,   -- land parcel area in sq ft (separate from building sqft)
    building_count    INTEGER,   -- number of structures on the parcel
    source_type       TEXT NOT NULL DEFAULT 'manual',
    -- source_type: manual | address_lookup | document_upload | discover_duplicate
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pipeline_properties_deal_id ON pipeline_properties(pipeline_deal_id);
