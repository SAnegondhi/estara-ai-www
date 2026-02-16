package webhooks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/config"
	db "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/iap"
)

// Apple Server Notification Types (App Store Server Notifications V2)
const (
	AppleNotificationTypeConsumptionRequest     = "CONSUMPTION_REQUEST"
	AppleNotificationTypeDidChangeRenewalPref   = "DID_CHANGE_RENEWAL_PREF"
	AppleNotificationTypeDidChangeRenewalStatus = "DID_CHANGE_RENEWAL_STATUS"
	AppleNotificationTypeDidFailToRenew         = "DID_FAIL_TO_RENEW"
	AppleNotificationTypeDidRenew               = "DID_RENEW"
	AppleNotificationTypeExpired                = "EXPIRED"
	AppleNotificationTypeGracePeriodExpired     = "GRACE_PERIOD_EXPIRED"
	AppleNotificationTypeOfferRedeemed          = "OFFER_REDEEMED"
	AppleNotificationTypePriceIncrease          = "PRICE_INCREASE"
	AppleNotificationTypeRefund                 = "REFUND"
	AppleNotificationTypeRefundDeclined         = "REFUND_DECLINED"
	AppleNotificationTypeRenewalExtended        = "RENEWAL_EXTENDED"
	AppleNotificationTypeRevoke                 = "REVOKE"
	AppleNotificationTypeSubscribed             = "SUBSCRIBED"
	AppleNotificationTypeTestNotification       = "TEST"
)

// Apple Notification Subtypes
const (
	AppleSubtypeInitialBuy        = "INITIAL_BUY"
	AppleSubtypeResubscribe       = "RESUBSCRIBE"
	AppleSubtypeDowngrade         = "DOWNGRADE"
	AppleSubtypeUpgrade           = "UPGRADE"
	AppleSubtypeAutoRenewEnabled  = "AUTO_RENEW_ENABLED"
	AppleSubtypeAutoRenewDisabled = "AUTO_RENEW_DISABLED"
	AppleSubtypeVoluntary         = "VOLUNTARY"
	AppleSubtypeBillingRetry      = "BILLING_RETRY"
	AppleSubtypePriceIncrease     = "PRICE_INCREASE"
	AppleSubtypeGracePeriod       = "GRACE_PERIOD"
	AppleSubtypeBillingRecovery   = "BILLING_RECOVERY"
	AppleSubtypePending           = "PENDING"
	AppleSubtypeAccepted          = "ACCEPTED"
)

// AppleNotificationPayload represents Apple App Store Server Notification V2
type AppleNotificationPayload struct {
	NotificationType string `json:"notificationType"`
	Subtype          string `json:"subtype,omitempty"`
	NotificationUUID string `json:"notificationUUID"`
	Data             struct {
		AppAppleID            int64  `json:"appAppleId"`
		BundleID              string `json:"bundleId"`
		BundleVersion         string `json:"bundleVersion"`
		Environment           string `json:"environment"`
		SignedTransactionInfo string `json:"signedTransactionInfo"`
		SignedRenewalInfo     string `json:"signedRenewalInfo"`
	} `json:"data"`
	Version   string `json:"version"`
	SignedDate int64  `json:"signedDate"`
}

// AppleTransactionInfo represents decoded transaction info
type AppleTransactionInfo struct {
	TransactionID               string `json:"transactionId"`
	OriginalTransactionID       string `json:"originalTransactionId"`
	WebOrderLineItemID          string `json:"webOrderLineItemId"`
	BundleID                    string `json:"bundleId"`
	ProductID                   string `json:"productId"`
	SubscriptionGroupIdentifier string `json:"subscriptionGroupIdentifier"`
	PurchaseDate                int64  `json:"purchaseDate"`
	OriginalPurchaseDate        int64  `json:"originalPurchaseDate"`
	ExpiresDate                 int64  `json:"expiresDate"`
	Quantity                    int    `json:"quantity"`
	Type                        string `json:"type"`
	InAppOwnershipType          string `json:"inAppOwnershipType"`
	SignedDate                  int64  `json:"signedDate"`
	Environment                 string `json:"environment"`
	StorefrontID                string `json:"storefrontId"`
	Storefront                  string `json:"storefront"`
	TransactionReason           string `json:"transactionReason"`
	RevocationDate              int64  `json:"revocationDate,omitempty"`
	RevocationReason            int    `json:"revocationReason,omitempty"`
	IsUpgraded                  bool   `json:"isUpgraded,omitempty"`
	OfferType                   int    `json:"offerType,omitempty"`
	OfferIdentifier             string `json:"offerIdentifier,omitempty"`
}

// AppleRenewalInfo represents decoded renewal info
type AppleRenewalInfo struct {
	AutoRenewProductID          string `json:"autoRenewProductId"`
	AutoRenewStatus             int    `json:"autoRenewStatus"`
	Environment                 string `json:"environment"`
	ExpirationIntent            int    `json:"expirationIntent,omitempty"`
	GracePeriodExpiresDate      int64  `json:"gracePeriodExpiresDate,omitempty"`
	IsInBillingRetryPeriod      bool   `json:"isInBillingRetryPeriod,omitempty"`
	OfferIdentifier             string `json:"offerIdentifier,omitempty"`
	OfferType                   int    `json:"offerType,omitempty"`
	OriginalTransactionID       string `json:"originalTransactionId"`
	PriceIncreaseStatus         int    `json:"priceIncreaseStatus,omitempty"`
	ProductID                   string `json:"productId"`
	RecentSubscriptionStartDate int64  `json:"recentSubscriptionStartDate,omitempty"`
	RenewalDate                 int64  `json:"renewalDate,omitempty"`
	SignedDate                  int64  `json:"signedDate"`
}

// AppleHandler handles Apple App Store webhooks
type AppleHandler struct {
	store  *db.Store
	cfg    *config.Config
	logger *slog.Logger
}

// NewAppleHandler creates a new Apple webhook handler
func NewAppleHandler(store *db.Store, cfg *config.Config) *AppleHandler {
	return &AppleHandler{
		store:  store,
		cfg:    cfg,
		logger: slog.Default().With("component", "apple_webhook"),
	}
}

// HandleWebhook processes Apple App Store Server Notifications
// POST /api/webhooks/apple
func (h *AppleHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse the signed payload
	var signedPayload struct {
		SignedPayload string `json:"signedPayload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&signedPayload); err != nil {
		h.logger.Error("failed to decode webhook payload", "error", err)
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	// TODO: Verify JWT signature using Apple's public key
	// For now, decode without verification (should be added for production)
	payload, err := h.decodePayload(signedPayload.SignedPayload)
	if err != nil {
		h.logger.Error("failed to decode signed payload", "error", err)
		http.Error(w, "invalid signed payload", http.StatusBadRequest)
		return
	}

	h.logger.Info("received Apple webhook",
		"notification_type", payload.NotificationType,
		"subtype", payload.Subtype,
		"notification_uuid", payload.NotificationUUID,
		"environment", payload.Data.Environment,
	)

	// Record audit log for all notifications
	q := h.store.Q()
	eventData, _ := json.Marshal(payload)
	_, _ = q.CreateBillingAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID: uuid.New().String(),
		AppleEnvironment: pgtype.Text{
			String: payload.Data.Environment,
			Valid:  true,
		},
		EventType: "APPLE_WEBHOOK_" + payload.NotificationType,
		EventData: eventData,
	})

	// Handle different notification types
	switch payload.NotificationType {
	case AppleNotificationTypeSubscribed:
		h.handleSubscribed(ctx, payload)
	case AppleNotificationTypeDidRenew:
		h.handleDidRenew(ctx, payload)
	case AppleNotificationTypeDidFailToRenew:
		h.handleDidFailToRenew(ctx, payload)
	case AppleNotificationTypeExpired:
		h.handleExpired(ctx, payload)
	case AppleNotificationTypeDidChangeRenewalStatus:
		h.handleDidChangeRenewalStatus(ctx, payload)
	case AppleNotificationTypeRefund:
		h.handleRefund(ctx, payload)
	case AppleNotificationTypeRevoke:
		h.handleRevoke(ctx, payload)
	case AppleNotificationTypeGracePeriodExpired:
		h.handleGracePeriodExpired(ctx, payload)
	case AppleNotificationTypeTestNotification:
		h.logger.Info("received test notification from Apple")
	default:
		h.logger.Info("unhandled notification type", "type", payload.NotificationType)
	}

	// Always return 200 to acknowledge receipt
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ===============================
// JWT Decoding Helpers
// ===============================

func (h *AppleHandler) decodePayload(signedPayload string) (*AppleNotificationPayload, error) {
	// Try to parse directly (for testing with plain JSON)
	var payload AppleNotificationPayload
	if err := json.Unmarshal([]byte(signedPayload), &payload); err == nil {
		return &payload, nil
	}

	// Decode JWS (JSON Web Signature) — three base64url-encoded parts separated by '.'
	// TODO: Verify signature with Apple's public key for production
	decoded, err := decodeJWTPayload(signedPayload)
	if err != nil {
		h.logger.Warn("failed to decode JWS payload", "error", err)
		return &payload, nil
	}

	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal decoded payload: %w", err)
	}

	return &payload, nil
}

func (h *AppleHandler) decodeTransactionInfo(signedTransactionInfo string) (*AppleTransactionInfo, error) {
	if signedTransactionInfo == "" {
		return nil, fmt.Errorf("empty signedTransactionInfo")
	}

	decoded, err := decodeJWTPayload(signedTransactionInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to decode transaction JWT: %w", err)
	}

	var txn AppleTransactionInfo
	if err := json.Unmarshal(decoded, &txn); err != nil {
		return nil, fmt.Errorf("failed to unmarshal transaction info: %w", err)
	}

	return &txn, nil
}

func (h *AppleHandler) decodeRenewalInfo(signedRenewalInfo string) (*AppleRenewalInfo, error) {
	if signedRenewalInfo == "" {
		return nil, fmt.Errorf("empty signedRenewalInfo")
	}

	decoded, err := decodeJWTPayload(signedRenewalInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to decode renewal JWT: %w", err)
	}

	var renewal AppleRenewalInfo
	if err := json.Unmarshal(decoded, &renewal); err != nil {
		return nil, fmt.Errorf("failed to unmarshal renewal info: %w", err)
	}

	return &renewal, nil
}

// decodeJWTPayload extracts and base64url-decodes the payload (middle part) from a JWS token.
func decodeJWTPayload(jws string) ([]byte, error) {
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWS format: expected 3 parts, got %d", len(parts))
	}

	// Decode the payload (second part) using base64url
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to base64url decode payload: %w", err)
	}

	return decoded, nil
}

// ===============================
// Subscription Lookup Helper
// ===============================

func (h *AppleHandler) findSubscription(ctx context.Context, q *queries.Queries, originalTransactionID string) (*queries.Subscription, error) {
	sub, err := q.GetSubscriptionByAppleTransactionID(ctx, pgtype.Text{
		String: originalTransactionID,
		Valid:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("subscription not found for Apple transaction %s: %w", originalTransactionID, err)
	}
	return &sub, nil
}

// ===============================
// Notification Handlers
// ===============================

func (h *AppleHandler) handleSubscribed(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling subscription event",
		"subtype", payload.Subtype,
		"notification_uuid", payload.NotificationUUID,
	)

	txn, err := h.decodeTransactionInfo(payload.Data.SignedTransactionInfo)
	if err != nil {
		h.logger.Error("failed to decode transaction info for SUBSCRIBED", "error", err)
		return
	}

	q := h.store.Q()

	// Try to find existing subscription linked to this Apple transaction
	sub, err := h.findSubscription(ctx, q, txn.OriginalTransactionID)
	if err != nil {
		// No subscription linked yet — IAP purchase may not be synced to a user yet.
		// The client app syncs the transaction after purchase; the user link happens via
		// the IAP service ValidateAndSync flow. Log and wait for client sync.
		h.logger.Info("no subscription found for SUBSCRIBED event — awaiting client sync",
			"original_transaction_id", txn.OriginalTransactionID,
			"product_id", txn.ProductID,
		)
		return
	}

	// Update subscription period and status
	expiresAt := millisecondsToTime(txn.ExpiresDate)
	purchaseDate := millisecondsToTime(txn.PurchaseDate)

	if err := q.UpdateSubscriptionPeriod(ctx, queries.UpdateSubscriptionPeriodParams{
		ID:                 sub.ID,
		CurrentPeriodStart: pgtype.Timestamp{Time: purchaseDate, Valid: true},
		CurrentPeriodEnd:   pgtype.Timestamp{Time: expiresAt, Valid: true},
	}); err != nil {
		h.logger.Error("failed to update subscription period", "error", err, "sub_id", sub.ID)
	}

	if err := q.UpdateSubscriptionStatus(ctx, queries.UpdateSubscriptionStatusParams{
		ID:     sub.ID,
		Status: "ACTIVE",
	}); err != nil {
		h.logger.Error("failed to update subscription status", "error", err, "sub_id", sub.ID)
	}

	h.logger.Info("subscription activated via Apple IAP",
		"sub_id", sub.ID,
		"product_id", txn.ProductID,
		"expires_at", expiresAt,
	)
}

func (h *AppleHandler) handleDidRenew(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling renewal event",
		"notification_uuid", payload.NotificationUUID,
	)

	txn, err := h.decodeTransactionInfo(payload.Data.SignedTransactionInfo)
	if err != nil {
		h.logger.Error("failed to decode transaction info for DID_RENEW", "error", err)
		return
	}

	q := h.store.Q()
	sub, err := h.findSubscription(ctx, q, txn.OriginalTransactionID)
	if err != nil {
		h.logger.Warn("no subscription found for DID_RENEW", "original_transaction_id", txn.OriginalTransactionID)
		return
	}

	// Extend subscription period
	expiresAt := millisecondsToTime(txn.ExpiresDate)
	purchaseDate := millisecondsToTime(txn.PurchaseDate)

	if err := q.UpdateSubscriptionPeriod(ctx, queries.UpdateSubscriptionPeriodParams{
		ID:                 sub.ID,
		CurrentPeriodStart: pgtype.Timestamp{Time: purchaseDate, Valid: true},
		CurrentPeriodEnd:   pgtype.Timestamp{Time: expiresAt, Valid: true},
	}); err != nil {
		h.logger.Error("failed to update subscription period for renewal", "error", err, "sub_id", sub.ID)
	}

	// Ensure status is ACTIVE (clear any past_due state from billing retry)
	if err := q.UpdateSubscriptionStatus(ctx, queries.UpdateSubscriptionStatusParams{
		ID:     sub.ID,
		Status: "ACTIVE",
	}); err != nil {
		h.logger.Error("failed to update subscription status for renewal", "error", err, "sub_id", sub.ID)
	}

	h.logger.Info("subscription renewed via Apple IAP",
		"sub_id", sub.ID,
		"new_expires_at", expiresAt,
	)
}

func (h *AppleHandler) handleDidFailToRenew(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling failed renewal event",
		"subtype", payload.Subtype,
		"notification_uuid", payload.NotificationUUID,
	)

	txn, err := h.decodeTransactionInfo(payload.Data.SignedTransactionInfo)
	if err != nil {
		h.logger.Error("failed to decode transaction info for DID_FAIL_TO_RENEW", "error", err)
		return
	}

	q := h.store.Q()
	sub, err := h.findSubscription(ctx, q, txn.OriginalTransactionID)
	if err != nil {
		h.logger.Warn("no subscription found for DID_FAIL_TO_RENEW", "original_transaction_id", txn.OriginalTransactionID)
		return
	}

	// During billing retry or grace period, keep user on a degraded status but don't cancel
	status := "PAST_DUE"
	if payload.Subtype == AppleSubtypeGracePeriod {
		status = "PAST_DUE" // Grace period — still active but payment pending
	}

	if err := q.UpdateSubscriptionStatus(ctx, queries.UpdateSubscriptionStatusParams{
		ID:     sub.ID,
		Status: status,
	}); err != nil {
		h.logger.Error("failed to update subscription to past_due", "error", err, "sub_id", sub.ID)
	}

	h.logger.Info("subscription payment failed — billing retry in progress",
		"sub_id", sub.ID,
		"subtype", payload.Subtype,
	)
}

func (h *AppleHandler) handleExpired(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling expiration event",
		"subtype", payload.Subtype,
		"notification_uuid", payload.NotificationUUID,
	)

	txn, err := h.decodeTransactionInfo(payload.Data.SignedTransactionInfo)
	if err != nil {
		h.logger.Error("failed to decode transaction info for EXPIRED", "error", err)
		return
	}

	q := h.store.Q()
	sub, err := h.findSubscription(ctx, q, txn.OriginalTransactionID)
	if err != nil {
		h.logger.Warn("no subscription found for EXPIRED", "original_transaction_id", txn.OriginalTransactionID)
		return
	}

	// Cancel subscription — user loses access
	if err := q.CancelSubscription(ctx, queries.CancelSubscriptionParams{
		ID:                sub.ID,
		CancelAtPeriodEnd: false, // Already expired
		Status:            "EXPIRED",
	}); err != nil {
		h.logger.Error("failed to expire subscription", "error", err, "sub_id", sub.ID)
	}

	h.logger.Info("subscription expired",
		"sub_id", sub.ID,
		"subtype", payload.Subtype,
	)
}

func (h *AppleHandler) handleDidChangeRenewalStatus(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling renewal status change",
		"subtype", payload.Subtype,
		"notification_uuid", payload.NotificationUUID,
	)

	renewal, err := h.decodeRenewalInfo(payload.Data.SignedRenewalInfo)
	if err != nil {
		h.logger.Error("failed to decode renewal info for DID_CHANGE_RENEWAL_STATUS", "error", err)
		return
	}

	q := h.store.Q()
	sub, err := h.findSubscription(ctx, q, renewal.OriginalTransactionID)
	if err != nil {
		h.logger.Warn("no subscription found for DID_CHANGE_RENEWAL_STATUS", "original_transaction_id", renewal.OriginalTransactionID)
		return
	}

	switch payload.Subtype {
	case AppleSubtypeAutoRenewDisabled:
		// User disabled auto-renewal — subscription stays active until period end
		if err := q.UpdateSubscriptionCancelAtPeriodEnd(ctx, queries.UpdateSubscriptionCancelAtPeriodEndParams{
			ID:                sub.ID,
			CancelAtPeriodEnd: true,
		}); err != nil {
			h.logger.Error("failed to set cancelAtPeriodEnd", "error", err, "sub_id", sub.ID)
		}
		h.logger.Info("auto-renewal disabled", "sub_id", sub.ID)

	case AppleSubtypeAutoRenewEnabled:
		// User re-enabled auto-renewal
		if err := q.UpdateSubscriptionCancelAtPeriodEnd(ctx, queries.UpdateSubscriptionCancelAtPeriodEndParams{
			ID:                sub.ID,
			CancelAtPeriodEnd: false,
		}); err != nil {
			h.logger.Error("failed to clear cancelAtPeriodEnd", "error", err, "sub_id", sub.ID)
		}
		h.logger.Info("auto-renewal re-enabled", "sub_id", sub.ID)
	}
}

func (h *AppleHandler) handleRefund(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling refund event",
		"notification_uuid", payload.NotificationUUID,
	)

	txn, err := h.decodeTransactionInfo(payload.Data.SignedTransactionInfo)
	if err != nil {
		h.logger.Error("failed to decode transaction info for REFUND", "error", err)
		return
	}

	q := h.store.Q()
	sub, err := h.findSubscription(ctx, q, txn.OriginalTransactionID)
	if err != nil {
		h.logger.Warn("no subscription found for REFUND", "original_transaction_id", txn.OriginalTransactionID)
		return
	}

	// Immediately revoke access on refund
	if err := q.CancelSubscription(ctx, queries.CancelSubscriptionParams{
		ID:                sub.ID,
		CancelAtPeriodEnd: false,
		Status:            "CANCELED",
	}); err != nil {
		h.logger.Error("failed to cancel subscription for refund", "error", err, "sub_id", sub.ID)
	}

	// Create system alert for admin attention
	alertKey := fmt.Sprintf("apple_refund_%s", txn.TransactionID)
	_, _ = q.UpsertSystemAlert(ctx, queries.UpsertSystemAlertParams{
		ID:             alertKey,
		Type:           "billing_error",
		Severity:       "warning",
		Title:          "Apple IAP Refund Processed",
		Description:    fmt.Sprintf("Refund for product %s (transaction %s)", txn.ProductID, txn.TransactionID),
		AlertKey:       alertKey,
		Metadata:       fmt.Sprintf(`{"transaction_id":"%s","original_transaction_id":"%s","product_id":"%s"}`, txn.TransactionID, txn.OriginalTransactionID, txn.ProductID),
		ActionRequired: false,
	})

	h.logger.Info("subscription canceled due to refund",
		"sub_id", sub.ID,
		"transaction_id", txn.TransactionID,
	)
}

func (h *AppleHandler) handleRevoke(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling revocation event",
		"notification_uuid", payload.NotificationUUID,
	)

	txn, err := h.decodeTransactionInfo(payload.Data.SignedTransactionInfo)
	if err != nil {
		h.logger.Error("failed to decode transaction info for REVOKE", "error", err)
		return
	}

	q := h.store.Q()
	sub, err := h.findSubscription(ctx, q, txn.OriginalTransactionID)
	if err != nil {
		h.logger.Warn("no subscription found for REVOKE", "original_transaction_id", txn.OriginalTransactionID)
		return
	}

	// Immediately revoke access (family sharing revoked, etc.)
	if err := q.CancelSubscription(ctx, queries.CancelSubscriptionParams{
		ID:                sub.ID,
		CancelAtPeriodEnd: false,
		Status:            "CANCELED",
	}); err != nil {
		h.logger.Error("failed to cancel subscription for revocation", "error", err, "sub_id", sub.ID)
	}

	h.logger.Info("subscription revoked",
		"sub_id", sub.ID,
		"revocation_reason", txn.RevocationReason,
	)
}

func (h *AppleHandler) handleGracePeriodExpired(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling grace period expiration",
		"notification_uuid", payload.NotificationUUID,
	)

	txn, err := h.decodeTransactionInfo(payload.Data.SignedTransactionInfo)
	if err != nil {
		h.logger.Error("failed to decode transaction info for GRACE_PERIOD_EXPIRED", "error", err)
		return
	}

	q := h.store.Q()
	sub, err := h.findSubscription(ctx, q, txn.OriginalTransactionID)
	if err != nil {
		h.logger.Warn("no subscription found for GRACE_PERIOD_EXPIRED", "original_transaction_id", txn.OriginalTransactionID)
		return
	}

	// Grace period ended without payment — expire the subscription
	if err := q.CancelSubscription(ctx, queries.CancelSubscriptionParams{
		ID:                sub.ID,
		CancelAtPeriodEnd: false,
		Status:            "EXPIRED",
	}); err != nil {
		h.logger.Error("failed to expire subscription after grace period", "error", err, "sub_id", sub.ID)
	}

	h.logger.Info("subscription expired after grace period",
		"sub_id", sub.ID,
	)
}

// ===============================
// Utility Functions
// ===============================

// GetTierFromProductID maps Apple product ID to subscription tier
func GetTierFromProductID(productID string) iap.SubscriptionTier {
	return iap.GetTierFromProductID(productID)
}

// millisecondsToTime converts milliseconds since epoch to time.Time
func millisecondsToTime(ms int64) time.Time {
	return time.UnixMilli(ms)
}
