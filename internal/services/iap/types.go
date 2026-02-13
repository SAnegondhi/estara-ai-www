package iap

import "time"

// Platform represents the mobile platform
type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

// SubscriptionStatus represents the status of an IAP subscription
type SubscriptionStatus string

const (
	IAPStatusActive    SubscriptionStatus = "ACTIVE"
	IAPStatusExpired   SubscriptionStatus = "EXPIRED"
	IAPStatusCanceled  SubscriptionStatus = "CANCELED"
	IAPStatusGracePd   SubscriptionStatus = "GRACE_PERIOD"
	IAPStatusOnHold    SubscriptionStatus = "ON_HOLD"
	IAPStatusPaused    SubscriptionStatus = "PAUSED"
	IAPStatusPending   SubscriptionStatus = "PENDING"
	IAPStatusTrialing  SubscriptionStatus = "TRIALING"
)

// ProductID maps to subscription tiers
type ProductID string

const (
	// iOS Product IDs
	ProductIOSAnnualAccessMonthly            ProductID = "com.estara.annual_access.monthly"
	ProductIOSAnnualAccessAnnual             ProductID = "com.estara.annual_access.annual"
	ProductIOSProfessionalAllocatorMonthly   ProductID = "com.estara.professional_allocator.monthly"
	ProductIOSProfessionalAllocatorAnnual    ProductID = "com.estara.professional_allocator.annual"

	// Android Product IDs
	ProductAndroidAnnualAccessMonthly            ProductID = "annual_access_monthly"
	ProductAndroidAnnualAccessAnnual             ProductID = "annual_access_annual"
	ProductAndroidProfessionalAllocatorMonthly   ProductID = "professional_allocator_monthly"
	ProductAndroidProfessionalAllocatorAnnual    ProductID = "professional_allocator_annual"
)

// SubscriptionTier represents the subscription tier
type SubscriptionTier string

const (
	TierFree                  SubscriptionTier = "FREE"
	TierAnnualAccess          SubscriptionTier = "ANNUAL_ACCESS"
	TierProfessionalAllocator SubscriptionTier = "PROFESSIONAL_ALLOCATOR"
)

// AppleReceiptValidationRequest is the request for iOS receipt validation
type AppleReceiptValidationRequest struct {
	ReceiptData        string `json:"receipt_data"`
	ExcludeOldTransactions bool   `json:"exclude_old_transactions,omitempty"`
}

// AppleReceiptValidationResponse is the response from Apple's receipt validation
type AppleReceiptValidationResponse struct {
	Valid             bool               `json:"valid"`
	Status            int                `json:"status"`
	ProductID         string             `json:"product_id,omitempty"`
	TransactionID     string             `json:"transaction_id,omitempty"`
	OriginalTransactionID string         `json:"original_transaction_id,omitempty"`
	PurchaseDate      *time.Time         `json:"purchase_date,omitempty"`
	ExpiresDate       *time.Time         `json:"expires_date,omitempty"`
	IsTrialPeriod     bool               `json:"is_trial_period"`
	InIntroOfferPeriod bool              `json:"in_intro_offer_period"`
	SubscriptionStatus SubscriptionStatus `json:"subscription_status"`
	Tier              SubscriptionTier   `json:"tier,omitempty"`
	Environment       string             `json:"environment"`
	Error             string             `json:"error,omitempty"`
}

// GoogleReceiptValidationRequest is the request for Android receipt validation
type GoogleReceiptValidationRequest struct {
	PackageName   string `json:"package_name"`
	ProductID     string `json:"product_id"`
	PurchaseToken string `json:"purchase_token"`
}

// GoogleReceiptValidationResponse is the response from Google Play validation
type GoogleReceiptValidationResponse struct {
	Valid              bool               `json:"valid"`
	ProductID          string             `json:"product_id,omitempty"`
	OrderID            string             `json:"order_id,omitempty"`
	PurchaseDate       *time.Time         `json:"purchase_date,omitempty"`
	ExpiresDate        *time.Time         `json:"expires_date,omitempty"`
	AutoRenewing       bool               `json:"auto_renewing"`
	PaymentState       int                `json:"payment_state"`
	CancelReason       int                `json:"cancel_reason,omitempty"`
	SubscriptionStatus SubscriptionStatus `json:"subscription_status"`
	Tier               SubscriptionTier   `json:"tier,omitempty"`
	Error              string             `json:"error,omitempty"`
}

// SyncEntitlementsRequest is the request to sync IAP entitlements
type SyncEntitlementsRequest struct {
	Platform    Platform `json:"platform"`
	ProductID   string   `json:"product_id"`
	ReceiptData string   `json:"receipt_data,omitempty"`    // For iOS
	PurchaseToken string `json:"purchase_token,omitempty"` // For Android
	TransactionID string `json:"transaction_id,omitempty"`
}

// SyncEntitlementsResponse is the response after syncing entitlements
type SyncEntitlementsResponse struct {
	Success    bool             `json:"success"`
	Tier       SubscriptionTier `json:"tier"`
	ExpiresAt  *time.Time       `json:"expires_at,omitempty"`
	Features   []string         `json:"features"`
	Message    string           `json:"message,omitempty"`
}

// SubscriptionPlan represents a mobile subscription plan
type SubscriptionPlan struct {
	ProductID   string `json:"product_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Tier        string `json:"tier"`
	Price       string `json:"price"`
	Currency    string `json:"currency"`
	Period      string `json:"period"` // "monthly" or "annual"
	Features    []string `json:"features"`
}

// SubscriptionPlansResponse is the response with available plans
type SubscriptionPlansResponse struct {
	IOSPlans     []SubscriptionPlan `json:"ios_plans"`
	AndroidPlans []SubscriptionPlan `json:"android_plans"`
}

// AppleServerNotification represents an Apple App Store Server Notification
type AppleServerNotification struct {
	NotificationType    string                 `json:"notification_type"`
	NotificationUUID    string                 `json:"notification_uuid"`
	Data                AppleNotificationData  `json:"data"`
	Version             string                 `json:"version"`
	SignedDate          int64                  `json:"signed_date"`
}

// AppleNotificationData contains the notification payload
type AppleNotificationData struct {
	AppAppleID            int64  `json:"app_apple_id"`
	BundleID              string `json:"bundle_id"`
	BundleVersion         string `json:"bundle_version"`
	Environment           string `json:"environment"`
	SignedTransactionInfo string `json:"signed_transaction_info"`
	SignedRenewalInfo     string `json:"signed_renewal_info"`
}

// AppleTransactionInfo represents decoded transaction info
type AppleTransactionInfo struct {
	TransactionID         string `json:"transaction_id"`
	OriginalTransactionID string `json:"original_transaction_id"`
	WebOrderLineItemID    string `json:"web_order_line_item_id"`
	BundleID              string `json:"bundle_id"`
	ProductID             string `json:"product_id"`
	SubscriptionGroupID   string `json:"subscription_group_identifier"`
	PurchaseDate          int64  `json:"purchase_date"`
	OriginalPurchaseDate  int64  `json:"original_purchase_date"`
	ExpiresDate           int64  `json:"expires_date"`
	Quantity              int    `json:"quantity"`
	Type                  string `json:"type"`
	InAppOwnershipType    string `json:"in_app_ownership_type"`
	SignedDate            int64  `json:"signed_date"`
	Environment           string `json:"environment"`
	TransactionReason     string `json:"transaction_reason"`
	Storefront            string `json:"storefront"`
	StorefrontID          string `json:"storefront_id"`
	RevocationDate        int64  `json:"revocation_date,omitempty"`
	RevocationReason      int    `json:"revocation_reason,omitempty"`
	IsUpgraded            bool   `json:"is_upgraded,omitempty"`
	OfferType             int    `json:"offer_type,omitempty"`
	OfferIdentifier       string `json:"offer_identifier,omitempty"`
}

// AppleRenewalInfo represents decoded renewal info
type AppleRenewalInfo struct {
	OriginalTransactionID     string `json:"original_transaction_id"`
	ExpirationIntent          int    `json:"expiration_intent,omitempty"`
	AutoRenewProductID        string `json:"auto_renew_product_id"`
	ProductID                 string `json:"product_id"`
	AutoRenewStatus           int    `json:"auto_renew_status"`
	IsInBillingRetryPeriod    bool   `json:"is_in_billing_retry_period,omitempty"`
	PriceIncreaseStatus       int    `json:"price_increase_status,omitempty"`
	GracePeriodExpiresDate    int64  `json:"grace_period_expires_date,omitempty"`
	OfferType                 int    `json:"offer_type,omitempty"`
	OfferIdentifier           string `json:"offer_identifier,omitempty"`
	SignedDate                int64  `json:"signed_date"`
	Environment               string `json:"environment"`
	RecentSubscriptionStartDate int64 `json:"recent_subscription_start_date,omitempty"`
	RenewalDate               int64  `json:"renewal_date,omitempty"`
}

// ProductToTierMapping maps product IDs to subscription tiers
var ProductToTierMapping = map[ProductID]SubscriptionTier{
	ProductIOSAnnualAccessMonthly:            TierAnnualAccess,
	ProductIOSAnnualAccessAnnual:             TierAnnualAccess,
	ProductIOSProfessionalAllocatorMonthly:   TierProfessionalAllocator,
	ProductIOSProfessionalAllocatorAnnual:    TierProfessionalAllocator,
	ProductAndroidAnnualAccessMonthly:        TierAnnualAccess,
	ProductAndroidAnnualAccessAnnual:         TierAnnualAccess,
	ProductAndroidProfessionalAllocatorMonthly: TierProfessionalAllocator,
	ProductAndroidProfessionalAllocatorAnnual:  TierProfessionalAllocator,
}

// GetTierFromProductID returns the subscription tier for a product ID
func GetTierFromProductID(productID string) SubscriptionTier {
	if tier, ok := ProductToTierMapping[ProductID(productID)]; ok {
		return tier
	}
	return TierFree
}

// GetFeaturesForTier returns the features available for a subscription tier
func GetFeaturesForTier(tier SubscriptionTier) []string {
	switch tier {
	case TierProfessionalAllocator:
		return []string{
			"unlimited_market_analysis",
			"unlimited_property_search",
			"advanced_investment_planning",
			"investor_reports",
			"pdf_export",
			"priority_support",
			"api_access",
		}
	case TierAnnualAccess:
		return []string{
			"market_analysis",
			"property_search",
			"investment_planning",
			"investor_reports",
			"pdf_export",
		}
	default:
		return []string{
			"limited_market_analysis",
			"limited_property_search",
		}
	}
}
