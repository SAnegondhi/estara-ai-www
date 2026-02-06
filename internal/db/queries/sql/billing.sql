-- Subscription Queries

-- name: GetSubscriptionByUserID :one
SELECT * FROM subscriptions WHERE "userId" = $1;

-- name: GetSubscriptionByStripeID :one
SELECT * FROM subscriptions WHERE "stripeSubscriptionId" = $1;

-- name: CreateSubscription :one
INSERT INTO subscriptions (
    id, "userId", "stripeSubscriptionId", "stripePriceId", "stripeCustomerId",
    tier, status, "currentPeriodStart", "currentPeriodEnd",
    "trialStart", "trialEnd", "canceledAt", "cancelAtPeriodEnd", metadata,
    "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW()
) RETURNING *;

-- name: UpdateSubscriptionStatus :exec
UPDATE subscriptions SET
    status = $2,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: UpdateSubscriptionTier :exec
UPDATE subscriptions SET
    tier = $2,
    "stripePriceId" = $3,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: UpdateSubscriptionPeriod :exec
UPDATE subscriptions SET
    "currentPeriodStart" = $2,
    "currentPeriodEnd" = $3,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: CancelSubscription :exec
UPDATE subscriptions SET
    "canceledAt" = NOW(),
    "cancelAtPeriodEnd" = $2,
    status = $3,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: ListActiveSubscriptions :many
SELECT * FROM subscriptions
WHERE status IN ('ACTIVE', 'TRIALING')
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: ListSubscriptionsByTier :many
SELECT * FROM subscriptions
WHERE tier = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: CountSubscriptionsByStatus :one
SELECT
    COUNT(*) FILTER (WHERE status = 'ACTIVE') as active_count,
    COUNT(*) FILTER (WHERE status = 'TRIALING') as trialing_count,
    COUNT(*) FILTER (WHERE status = 'CANCELED') as canceled_count,
    COUNT(*) FILTER (WHERE status = 'FREE') as free_count
FROM subscriptions;

-- Invoice Queries

-- name: GetInvoiceByID :one
SELECT * FROM invoices WHERE id = $1;

-- name: GetInvoiceByStripeID :one
SELECT * FROM invoices WHERE "stripeInvoiceId" = $1;

-- name: CreateInvoice :one
INSERT INTO invoices (
    id, "stripeInvoiceId", "stripeCustomerId", "stripeSubscriptionId", "userId",
    "invoiceNumber", status, subtotal, "taxAmount", total, "amountPaid", "amountDue",
    currency, "dueDate", "paidAt", "periodStart", "periodEnd",
    "hostedInvoiceUrl", "invoicePdfUrl", description, "productType",
    "emailSentAt", "emailDelivered", "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
    $18, $19, $20, $21, $22, $23, NOW(), NOW()
) RETURNING *;

-- name: UpdateInvoiceStatus :exec
UPDATE invoices SET
    status = $2,
    "paidAt" = $3,
    "amountPaid" = $4,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: UpdateInvoiceStatusByStripeID :exec
UPDATE invoices SET
    status = $2,
    "hostedInvoiceUrl" = COALESCE($3, "hostedInvoiceUrl"),
    "invoicePdfUrl" = COALESCE($4, "invoicePdfUrl"),
    "emailSentAt" = $5,
    "emailDelivered" = COALESCE($6, "emailDelivered"),
    "updatedAt" = NOW()
WHERE "stripeInvoiceId" = $1;

-- name: UpdateInvoicePaid :exec
UPDATE invoices SET
    status = 'PAID',
    "paidAt" = $2,
    "amountPaid" = $3,
    "updatedAt" = NOW()
WHERE "stripeInvoiceId" = $1;

-- name: UpsertInvoice :one
INSERT INTO invoices (
    id, "stripeInvoiceId", "stripeCustomerId", "stripeSubscriptionId", "userId",
    "invoiceNumber", status, subtotal, "taxAmount", total, "amountPaid", "amountDue",
    currency, "dueDate", "paidAt", "periodStart", "periodEnd",
    "hostedInvoiceUrl", "invoicePdfUrl", description, "productType",
    "emailSentAt", "emailDelivered", "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
    $18, $19, $20, $21, $22, $23, NOW(), NOW()
)
ON CONFLICT ("stripeInvoiceId") DO UPDATE SET
    status = EXCLUDED.status,
    subtotal = EXCLUDED.subtotal,
    "taxAmount" = EXCLUDED."taxAmount",
    total = EXCLUDED.total,
    "amountPaid" = EXCLUDED."amountPaid",
    "amountDue" = EXCLUDED."amountDue",
    "hostedInvoiceUrl" = COALESCE(EXCLUDED."hostedInvoiceUrl", invoices."hostedInvoiceUrl"),
    "invoicePdfUrl" = COALESCE(EXCLUDED."invoicePdfUrl", invoices."invoicePdfUrl"),
    "updatedAt" = NOW()
RETURNING *;

-- name: ListUserInvoices :many
SELECT * FROM invoices
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListInvoicesByStatus :many
SELECT * FROM invoices
WHERE status = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- Receipt Queries

-- name: GetReceiptByID :one
SELECT * FROM receipts WHERE id = $1;

-- name: GetReceiptByNumber :one
SELECT * FROM receipts WHERE "receiptNumber" = $1;

-- name: GetReceiptByInvoiceID :one
SELECT * FROM receipts WHERE "invoiceId" = $1;

-- name: CreateReceipt :one
INSERT INTO receipts (
    id, "invoiceId", "userId", "stripeChargeId", "stripePaymentIntentId",
    "receiptNumber", amount, currency, "paymentMethod", "cardBrand", "cardLast4",
    "paidAt", "stripeReceiptUrl", "emailSentAt", "emailDelivered", "productDescription",
    "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, NOW()
) RETURNING *;

-- name: ListUserReceipts :many
SELECT * FROM receipts
WHERE "userId" = $1
ORDER BY "paidAt" DESC
LIMIT $2 OFFSET $3;

-- Billing Audit Log Queries

-- name: CreateBillingAuditLog :one
INSERT INTO billing_audit_logs (
    id, "userId", "stripeCustomerId", "stripeSubscriptionId",
    "stripePaymentIntentId", "stripeInvoiceId",
    "appleOriginalTransactionId", "appleTransactionId", "appleProductId", "appleEnvironment",
    "eventType", "eventData", "ipAddress", "userAgent", "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW()
) RETURNING *;

-- name: ListBillingAuditLogsByUser :many
SELECT * FROM billing_audit_logs
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListBillingAuditLogsByEventType :many
SELECT * FROM billing_audit_logs
WHERE "eventType" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListBillingAuditLogsBySubscription :many
SELECT * FROM billing_audit_logs
WHERE "stripeSubscriptionId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListBillingAuditLogsDateRange :many
SELECT * FROM billing_audit_logs
WHERE "createdAt" >= $1 AND "createdAt" <= $2
ORDER BY "createdAt" DESC
LIMIT $3 OFFSET $4;

-- Billing Cycle Queries

-- name: GetBillingCycleByID :one
SELECT * FROM billing_cycles WHERE id = $1;

-- name: CreateBillingCycle :one
INSERT INTO billing_cycles (
    id, "userId", tier, frequency, "creditsGranted",
    "startDate", "endDate", processed, "processedAt", "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, false, NULL, NOW()
) RETURNING *;

-- name: MarkBillingCycleProcessed :exec
UPDATE billing_cycles SET
    processed = true,
    "processedAt" = NOW()
WHERE id = $1;

-- name: ListUnprocessedBillingCycles :many
SELECT * FROM billing_cycles
WHERE processed = false AND "endDate" <= NOW()
ORDER BY "endDate" ASC
LIMIT $1;

-- name: ListUserBillingCycles :many
SELECT * FROM billing_cycles
WHERE "userId" = $1
ORDER BY "startDate" DESC
LIMIT $2 OFFSET $3;

-- Checkout Evidence Queries

-- name: GetCheckoutEvidenceByPaymentIntent :one
SELECT * FROM checkout_evidence WHERE "stripePaymentIntentId" = $1;

-- name: GetCheckoutEvidenceBySubscription :one
SELECT * FROM checkout_evidence WHERE "stripeSubscriptionId" = $1;

-- name: CreateCheckoutEvidence :one
INSERT INTO checkout_evidence (
    id, "userId", "stripePaymentIntentId", "stripeSubscriptionId",
    "termsAcceptedAt", "termsVersion", "refundPolicyAccepted", "chargebackPolicyAccepted",
    "ipAddress", "userAgent", "deviceFingerprint",
    "billingEmail", "billingName", "billingAddress", "cardLast4", "cardBrand",
    amount, currency, "productDescription", "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, NOW()
) RETURNING *;

-- name: ListUserCheckoutEvidence :many
SELECT * FROM checkout_evidence
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- Renewal Notification Queries

-- name: CreateRenewalNotification :one
INSERT INTO renewal_notifications (
    id, "userId", "subscriptionId", "emailType", "sentAt",
    "emailContent", "recipientEmail", "renewalDate", "renewalAmount",
    "sendgridMessageId", delivered, "deliveredAt", opened, "openedAt"
) VALUES (
    $1, $2, $3, $4, NOW(), $5, $6, $7, $8, $9, $10, $11, false, NULL
) RETURNING *;

-- name: UpdateRenewalNotificationDelivered :exec
UPDATE renewal_notifications SET
    delivered = true,
    "deliveredAt" = NOW()
WHERE id = $1;

-- name: UpdateRenewalNotificationOpened :exec
UPDATE renewal_notifications SET
    opened = true,
    "openedAt" = NOW()
WHERE id = $1;

-- name: ListUserRenewalNotifications :many
SELECT * FROM renewal_notifications
WHERE "userId" = $1
ORDER BY "sentAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListRenewalNotificationsBySubscription :many
SELECT * FROM renewal_notifications
WHERE "subscriptionId" = $1
ORDER BY "sentAt" DESC;

-- name: GetRecentRenewalNotification :one
SELECT * FROM renewal_notifications
WHERE "userId" = $1 AND "emailType" = $2
ORDER BY "sentAt" DESC
LIMIT 1;
