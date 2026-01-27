package webhooks

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
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
	db     *postgres.DB
	cfg    *config.Config
	logger *slog.Logger
}

// NewAppleHandler creates a new Apple webhook handler
func NewAppleHandler(db *postgres.DB, cfg *config.Config) *AppleHandler {
	return &AppleHandler{
		db:     db,
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
	q := queries.New(h.db.Main)
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

func (h *AppleHandler) decodePayload(signedPayload string) (*AppleNotificationPayload, error) {
	// TODO: Properly decode and verify JWT
	// For now, this is a simplified implementation
	// In production, use github.com/golang-jwt/jwt/v5 to verify with Apple's public key

	// The signedPayload is a JWS (JSON Web Signature)
	// Split by '.' and decode the payload (middle part)
	// For now, return a mock payload if we can't decode
	var payload AppleNotificationPayload

	// Try to parse directly (for testing)
	if err := json.Unmarshal([]byte(signedPayload), &payload); err == nil {
		return &payload, nil
	}

	// If it's a real JWS, we need proper JWT handling
	// This is a placeholder - production code should verify the signature
	h.logger.Warn("JWT verification not implemented, processing without verification")

	return &payload, nil
}

func (h *AppleHandler) handleSubscribed(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling subscription event",
		"subtype", payload.Subtype,
		"notification_uuid", payload.NotificationUUID,
	)

	// TODO: Decode signedTransactionInfo to get transaction details
	// Then sync entitlements for the user
}

func (h *AppleHandler) handleDidRenew(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling renewal event",
		"notification_uuid", payload.NotificationUUID,
	)

	// TODO: Update subscription expiry date
}

func (h *AppleHandler) handleDidFailToRenew(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling failed renewal event",
		"subtype", payload.Subtype,
		"notification_uuid", payload.NotificationUUID,
	)

	// TODO: Handle billing retry or grace period
}

func (h *AppleHandler) handleExpired(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling expiration event",
		"subtype", payload.Subtype,
		"notification_uuid", payload.NotificationUUID,
	)

	// TODO: Downgrade user to free tier
}

func (h *AppleHandler) handleDidChangeRenewalStatus(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling renewal status change",
		"subtype", payload.Subtype,
		"notification_uuid", payload.NotificationUUID,
	)

	// Subtype AUTO_RENEW_DISABLED means user cancelled (but still active until period end)
	// Subtype AUTO_RENEW_ENABLED means user re-enabled auto-renew
}

func (h *AppleHandler) handleRefund(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling refund event",
		"notification_uuid", payload.NotificationUUID,
	)

	// TODO: Revoke user entitlements immediately
}

func (h *AppleHandler) handleRevoke(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling revocation event",
		"notification_uuid", payload.NotificationUUID,
	)

	// TODO: Revoke user entitlements (family sharing revoked, etc.)
}

func (h *AppleHandler) handleGracePeriodExpired(ctx context.Context, payload *AppleNotificationPayload) {
	h.logger.Info("handling grace period expiration",
		"notification_uuid", payload.NotificationUUID,
	)

	// TODO: Downgrade user after grace period
}

// GetTierFromProductID maps Apple product ID to subscription tier
func GetTierFromProductID(productID string) iap.SubscriptionTier {
	return iap.GetTierFromProductID(productID)
}

// Helper to convert milliseconds to time.Time
func millisecondsToTime(ms int64) time.Time {
	return time.UnixMilli(ms)
}
