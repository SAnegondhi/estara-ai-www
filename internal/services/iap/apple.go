package iap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/config"
)

const (
	appleProductionURL = "https://buy.itunes.apple.com/verifyReceipt"
	appleSandboxURL    = "https://sandbox.itunes.apple.com/verifyReceipt"
)

// Apple receipt validation status codes
const (
	AppleStatusOK                      = 0
	AppleStatusUnreadableJSON          = 21000
	AppleStatusMalformedReceipt        = 21002
	AppleStatusReceiptAuthFailed       = 21003
	AppleStatusSharedSecretMismatch    = 21004
	AppleStatusServerUnavailable       = 21005
	AppleStatusReceiptValidForSandbox  = 21007
	AppleStatusReceiptValidForProd     = 21008
	AppleStatusInternalError           = 21009
	AppleStatusSubscriptionExpired     = 21010
)

var (
	ErrInvalidReceipt       = errors.New("invalid receipt")
	ErrReceiptExpired       = errors.New("receipt expired")
	ErrServerUnavailable    = errors.New("apple server unavailable")
	ErrAuthenticationFailed = errors.New("authentication failed")
)

// AppleService handles Apple IAP receipt validation
type AppleService struct {
	sharedSecret string
	httpClient   *http.Client
	logger       *slog.Logger
}

// NewAppleService creates a new Apple IAP service
func NewAppleService(cfg *config.Config) *AppleService {
	return &AppleService{
		sharedSecret: cfg.IAP.AppleSharedSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: slog.Default().With("component", "apple_iap_service"),
	}
}

// AppleReceiptRequest is the request body for Apple's verifyReceipt endpoint
type appleReceiptRequest struct {
	ReceiptData            string `json:"receipt-data"`
	Password               string `json:"password"`
	ExcludeOldTransactions bool   `json:"exclude-old-transactions,omitempty"`
}

// appleReceiptResponse is the response from Apple's verifyReceipt endpoint
type appleReceiptResponse struct {
	Status             int                    `json:"status"`
	Environment        string                 `json:"environment"`
	Receipt            *appleReceipt          `json:"receipt"`
	LatestReceiptInfo  []appleInAppPurchase   `json:"latest_receipt_info"`
	LatestReceipt      string                 `json:"latest_receipt"`
	PendingRenewalInfo []applePendingRenewal  `json:"pending_renewal_info"`
	IsRetryable        bool                   `json:"is-retryable"`
}

type appleReceipt struct {
	BundleID             string               `json:"bundle_id"`
	ApplicationVersion   string               `json:"application_version"`
	ReceiptType          string               `json:"receipt_type"`
	InApp                []appleInAppPurchase `json:"in_app"`
	OriginalPurchaseDate string               `json:"original_purchase_date_ms"`
}

type appleInAppPurchase struct {
	Quantity                  string `json:"quantity"`
	ProductID                 string `json:"product_id"`
	TransactionID             string `json:"transaction_id"`
	OriginalTransactionID     string `json:"original_transaction_id"`
	PurchaseDateMS            string `json:"purchase_date_ms"`
	OriginalPurchaseDateMS    string `json:"original_purchase_date_ms"`
	ExpiresDateMS             string `json:"expires_date_ms"`
	IsTrialPeriod             string `json:"is_trial_period"`
	IsInIntroOfferPeriod      string `json:"is_in_intro_offer_period"`
	CancellationDateMS        string `json:"cancellation_date_ms,omitempty"`
	CancellationReason        string `json:"cancellation_reason,omitempty"`
	WebOrderLineItemID        string `json:"web_order_line_item_id"`
	SubscriptionGroupID       string `json:"subscription_group_identifier"`
	InAppOwnershipType        string `json:"in_app_ownership_type"`
	PromotionalOfferID        string `json:"promotional_offer_id,omitempty"`
}

type applePendingRenewal struct {
	ProductID                string `json:"product_id"`
	AutoRenewProductID       string `json:"auto_renew_product_id"`
	OriginalTransactionID    string `json:"original_transaction_id"`
	AutoRenewStatus          string `json:"auto_renew_status"`
	ExpirationIntent         string `json:"expiration_intent,omitempty"`
	IsInBillingRetryPeriod   string `json:"is_in_billing_retry_period,omitempty"`
	GracePeriodExpiresDateMS string `json:"grace_period_expires_date_ms,omitempty"`
	PriceConsentStatus       string `json:"price_consent_status,omitempty"`
}

// ValidateReceipt validates an Apple receipt and returns subscription info
func (s *AppleService) ValidateReceipt(ctx context.Context, receiptData string) (*AppleReceiptValidationResponse, error) {
	// Try production first
	resp, err := s.verifyReceipt(ctx, receiptData, appleProductionURL)
	if err != nil {
		return nil, err
	}

	// If receipt is for sandbox, retry with sandbox URL
	if resp.Status == AppleStatusReceiptValidForSandbox {
		s.logger.Debug("receipt is for sandbox environment, retrying")
		resp, err = s.verifyReceipt(ctx, receiptData, appleSandboxURL)
		if err != nil {
			return nil, err
		}
	}

	return s.parseReceiptResponse(resp)
}

func (s *AppleService) verifyReceipt(ctx context.Context, receiptData string, url string) (*appleReceiptResponse, error) {
	reqBody := appleReceiptRequest{
		ReceiptData:            receiptData,
		Password:               s.sharedSecret,
		ExcludeOldTransactions: true,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var appleResp appleReceiptResponse
	if err := json.Unmarshal(body, &appleResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &appleResp, nil
}

func (s *AppleService) parseReceiptResponse(resp *appleReceiptResponse) (*AppleReceiptValidationResponse, error) {
	result := &AppleReceiptValidationResponse{
		Status:      resp.Status,
		Environment: resp.Environment,
	}

	switch resp.Status {
	case AppleStatusOK:
		result.Valid = true
	case AppleStatusSubscriptionExpired:
		result.Valid = false
		result.SubscriptionStatus = IAPStatusExpired
		result.Error = "subscription expired"
	case AppleStatusReceiptAuthFailed, AppleStatusSharedSecretMismatch:
		return nil, ErrAuthenticationFailed
	case AppleStatusServerUnavailable:
		return nil, ErrServerUnavailable
	case AppleStatusUnreadableJSON, AppleStatusMalformedReceipt:
		return nil, ErrInvalidReceipt
	default:
		result.Valid = false
		result.Error = fmt.Sprintf("unknown status code: %d", resp.Status)
	}

	// Find the latest active subscription
	if len(resp.LatestReceiptInfo) > 0 {
		latest := s.findLatestSubscription(resp.LatestReceiptInfo)
		if latest != nil {
			result.ProductID = latest.ProductID
			result.TransactionID = latest.TransactionID
			result.OriginalTransactionID = latest.OriginalTransactionID
			result.IsTrialPeriod = latest.IsTrialPeriod == "true"
			result.InIntroOfferPeriod = latest.IsInIntroOfferPeriod == "true"

			// Parse dates
			if purchaseDate := parseAppleTimestamp(latest.PurchaseDateMS); purchaseDate != nil {
				result.PurchaseDate = purchaseDate
			}
			if expiresDate := parseAppleTimestamp(latest.ExpiresDateMS); expiresDate != nil {
				result.ExpiresDate = expiresDate

				// Check if expired
				if time.Now().After(*expiresDate) {
					result.SubscriptionStatus = IAPStatusExpired
					result.Valid = false
				} else {
					result.SubscriptionStatus = IAPStatusActive
				}
			}

			// Check for cancellation
			if latest.CancellationDateMS != "" {
				result.SubscriptionStatus = IAPStatusCanceled
				result.Valid = false
			}

			// Map product to tier
			result.Tier = GetTierFromProductID(latest.ProductID)
		}
	}

	// Check pending renewal info for grace period
	for _, renewal := range resp.PendingRenewalInfo {
		if renewal.IsInBillingRetryPeriod == "1" {
			result.SubscriptionStatus = IAPStatusGracePd
		}
	}

	return result, nil
}

func (s *AppleService) findLatestSubscription(purchases []appleInAppPurchase) *appleInAppPurchase {
	if len(purchases) == 0 {
		return nil
	}

	var latest *appleInAppPurchase
	var latestExpiry int64

	for i := range purchases {
		purchase := &purchases[i]
		expiryMS := parseTimestampMS(purchase.ExpiresDateMS)
		if expiryMS > latestExpiry {
			latestExpiry = expiryMS
			latest = purchase
		}
	}

	return latest
}

// parseAppleTimestamp converts Apple's millisecond timestamp string to time.Time
func parseAppleTimestamp(ms string) *time.Time {
	msInt := parseTimestampMS(ms)
	if msInt == 0 {
		return nil
	}
	t := time.UnixMilli(msInt)
	return &t
}

func parseTimestampMS(ms string) int64 {
	var result int64
	fmt.Sscanf(ms, "%d", &result)
	return result
}
