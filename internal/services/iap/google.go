package iap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/androidpublisher/v3"
	"google.golang.org/api/option"

	"github.com/estara-ai/www/internal/config"
)

// Google Play purchase states
const (
	GooglePurchaseStatePending    = 0
	GooglePurchaseStatePurchased  = 1
	GooglePurchaseStateDeferred   = 2
	GooglePurchaseStateCanceled   = 3

	GooglePaymentStatePending       = 0
	GooglePaymentStateReceived      = 1
	GooglePaymentStateFree          = 2
	GooglePaymentStatePendingDeferred = 3

	GoogleCancelReasonUser     = 0
	GoogleCancelReasonSystem   = 1
	GoogleCancelReasonReplaced = 2
	GoogleCancelReasonDeveloper = 3
)

var (
	ErrGoogleServiceUnavailable = errors.New("google play service unavailable")
	ErrGoogleInvalidPurchase    = errors.New("invalid purchase")
	ErrGooglePurchaseNotFound   = errors.New("purchase not found")
)

// GoogleService handles Google Play IAP receipt validation
type GoogleService struct {
	packageName string
	client      *androidpublisher.Service
	logger      *slog.Logger
}

// NewGoogleService creates a new Google Play IAP service
func NewGoogleService(ctx context.Context, cfg *config.Config) (*GoogleService, error) {
	logger := slog.Default().With("component", "google_iap_service")

	packageName := cfg.IAP.GooglePlayPackageName
	if packageName == "" {
		packageName = "com.estara.ai"
	}

	serviceAccountJSON := cfg.IAP.GoogleServiceAccountJSON
	if serviceAccountJSON == "" {
		logger.Warn("GOOGLE_SERVICE_ACCOUNT_JSON not configured, Google Play IAP disabled")
		return &GoogleService{
			packageName: packageName,
			logger:      logger,
		}, nil
	}

	creds, err := google.CredentialsFromJSON(ctx, []byte(serviceAccountJSON), androidpublisher.AndroidpublisherScope)
	if err != nil {
		logger.Warn("failed to parse Google service account credentials", "error", err)
		return &GoogleService{
			packageName: packageName,
			logger:      logger,
		}, nil
	}

	client, err := androidpublisher.NewService(ctx, option.WithTokenSource(creds.TokenSource))
	if err != nil {
		logger.Warn("failed to create Google Play client", "error", err)
		return &GoogleService{
			packageName: packageName,
			logger:      logger,
		}, nil
	}

	return &GoogleService{
		packageName: packageName,
		client:      client,
		logger:      logger,
	}, nil
}

// ValidateReceipt validates a Google Play purchase and returns subscription info
func (s *GoogleService) ValidateReceipt(ctx context.Context, req *GoogleReceiptValidationRequest) (*GoogleReceiptValidationResponse, error) {
	if s.client == nil {
		return nil, ErrGoogleServiceUnavailable
	}

	packageName := req.PackageName
	if packageName == "" {
		packageName = s.packageName
	}

	// Get subscription purchase details from Google Play
	purchase, err := s.client.Purchases.Subscriptions.Get(
		packageName,
		req.ProductID,
		req.PurchaseToken,
	).Context(ctx).Do()

	if err != nil {
		s.logger.Error("failed to get subscription from Google Play",
			"error", err,
			"product_id", req.ProductID,
		)
		return nil, fmt.Errorf("failed to validate purchase: %w", err)
	}

	return s.parsePurchaseResponse(purchase, req.ProductID)
}

func (s *GoogleService) parsePurchaseResponse(purchase *androidpublisher.SubscriptionPurchase, productID string) (*GoogleReceiptValidationResponse, error) {
	// Get payment state safely (handle nil pointer)
	var paymentState int64
	if purchase.PaymentState != nil {
		paymentState = *purchase.PaymentState
	}

	result := &GoogleReceiptValidationResponse{
		ProductID:    productID,
		OrderID:      purchase.OrderId,
		AutoRenewing: purchase.AutoRenewing,
		PaymentState: int(paymentState),
	}

	// Parse purchase time
	if purchase.StartTimeMillis > 0 {
		t := time.UnixMilli(purchase.StartTimeMillis)
		result.PurchaseDate = &t
	}

	// Parse expiry time
	if purchase.ExpiryTimeMillis > 0 {
		t := time.UnixMilli(purchase.ExpiryTimeMillis)
		result.ExpiresDate = &t

		// Check if subscription is active
		if time.Now().Before(t) {
			result.Valid = true
			result.SubscriptionStatus = IAPStatusActive
		} else {
			result.Valid = false
			result.SubscriptionStatus = IAPStatusExpired
		}
	}

	// Check for cancellation
	if purchase.CancelReason > 0 {
		result.CancelReason = int(purchase.CancelReason)
		result.SubscriptionStatus = IAPStatusCanceled
		result.Valid = false
	}

	// Check payment state
	switch paymentState {
	case GooglePaymentStatePending, GooglePaymentStatePendingDeferred:
		result.SubscriptionStatus = IAPStatusPending
		result.Valid = false
	case GooglePaymentStateFree:
		result.SubscriptionStatus = IAPStatusTrialing
		result.Valid = true
	}

	// Check user cancellation
	if purchase.UserCancellationTimeMillis > 0 {
		result.SubscriptionStatus = IAPStatusCanceled
		// Still valid until expiry
		if result.ExpiresDate != nil && time.Now().Before(*result.ExpiresDate) {
			result.Valid = true
		}
	}

	// Map product to tier
	result.Tier = GetTierFromProductID(productID)

	return result, nil
}

// AcknowledgePurchase acknowledges a Google Play purchase
func (s *GoogleService) AcknowledgePurchase(ctx context.Context, productID, purchaseToken string) error {
	if s.client == nil {
		return ErrGoogleServiceUnavailable
	}

	err := s.client.Purchases.Subscriptions.Acknowledge(
		s.packageName,
		productID,
		purchaseToken,
		&androidpublisher.SubscriptionPurchasesAcknowledgeRequest{},
	).Context(ctx).Do()

	if err != nil {
		return fmt.Errorf("failed to acknowledge purchase: %w", err)
	}

	return nil
}

// VoidPurchase voids a Google Play purchase (for testing)
func (s *GoogleService) VoidPurchase(ctx context.Context, productID, purchaseToken string) error {
	if s.client == nil {
		return ErrGoogleServiceUnavailable
	}

	err := s.client.Purchases.Subscriptions.Revoke(
		s.packageName,
		productID,
		purchaseToken,
	).Context(ctx).Do()

	if err != nil {
		return fmt.Errorf("failed to void purchase: %w", err)
	}

	return nil
}

// DeferPurchase defers a Google Play subscription's billing
func (s *GoogleService) DeferPurchase(ctx context.Context, productID, purchaseToken string, deferUntil time.Time) error {
	if s.client == nil {
		return ErrGoogleServiceUnavailable
	}

	_, err := s.client.Purchases.Subscriptions.Defer(
		s.packageName,
		productID,
		purchaseToken,
		&androidpublisher.SubscriptionPurchasesDeferRequest{
			DeferralInfo: &androidpublisher.SubscriptionDeferralInfo{
				ExpectedExpiryTimeMillis: deferUntil.UnixMilli(),
				DesiredExpiryTimeMillis:  deferUntil.UnixMilli(),
			},
		},
	).Context(ctx).Do()

	if err != nil {
		return fmt.Errorf("failed to defer purchase: %w", err)
	}

	return nil
}

// unused but kept for oauth2 import
var _ = oauth2.NoContext
var _ = json.Marshal
