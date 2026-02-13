-- Schema for billing tables (matches Prisma schema)
-- These tables already exist in the database - this file is for sqlc code generation only

-- Enums (create if not exists pattern)
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

-- Subscriptions table
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

-- Invoices table
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

-- Receipts table
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

-- Billing Audit Logs table
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

-- Billing Cycles table
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

-- Checkout Evidence table
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

-- Renewal Notifications table
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
