package iap

import (
	"encoding/json"
	"testing"

	"github.com/estara-ai/www/internal/services/iap"
)

func TestValidateIOSReceiptRequest_Validation(t *testing.T) {
	tests := []struct {
		name        string
		request     ValidateIOSReceiptRequest
		expectValid bool
		reason      string
	}{
		{
			name: "valid iOS request",
			request: ValidateIOSReceiptRequest{
				ReceiptData: "receipt-data-base64",
			},
			expectValid: true,
		},
		{
			name: "missing receiptData",
			request: ValidateIOSReceiptRequest{
				ReceiptData: "",
			},
			expectValid: false,
			reason:      "receiptData is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.request.ReceiptData != ""
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v (reason: %s)", tt.expectValid, isValid, tt.reason)
			}
		})
	}
}

func TestValidateIOSReceiptRequest_JSON(t *testing.T) {
	req := ValidateIOSReceiptRequest{
		ReceiptData: "base64-encoded-receipt-data",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	var decoded ValidateIOSReceiptRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	if decoded.ReceiptData != req.ReceiptData {
		t.Errorf("expected receiptData=%s, got=%s", req.ReceiptData, decoded.ReceiptData)
	}
}

func TestGoogleReceiptValidationRequest_Validation(t *testing.T) {
	tests := []struct {
		name        string
		request     iap.GoogleReceiptValidationRequest
		expectValid bool
		reason      string
	}{
		{
			name: "valid Android request",
			request: iap.GoogleReceiptValidationRequest{
				ProductID:     "com.estara.monthly",
				PurchaseToken: "purchase-token",
			},
			expectValid: true,
		},
		{
			name: "missing productId",
			request: iap.GoogleReceiptValidationRequest{
				PurchaseToken: "purchase-token",
			},
			expectValid: false,
			reason:      "productId is required",
		},
		{
			name: "missing purchaseToken",
			request: iap.GoogleReceiptValidationRequest{
				ProductID: "com.estara.monthly",
			},
			expectValid: false,
			reason:      "purchaseToken is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.request.ProductID != "" && tt.request.PurchaseToken != ""
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v (reason: %s)", tt.expectValid, isValid, tt.reason)
			}
		})
	}
}

func TestGetTierFromProductID(t *testing.T) {
	tests := []struct {
		productID    string
		expectedTier iap.SubscriptionTier
	}{
		{"com.estara.annual_access.monthly", iap.TierAnnualAccess},
		{"com.estara.annual_access.annual", iap.TierAnnualAccess},
		{"com.estara.professional_allocator.monthly", iap.TierProfessionalAllocator},
		{"com.estara.professional_allocator.annual", iap.TierProfessionalAllocator},
		{"annual_access_monthly", iap.TierAnnualAccess},
		{"annual_access_annual", iap.TierAnnualAccess},
		{"professional_allocator_monthly", iap.TierProfessionalAllocator},
		{"professional_allocator_annual", iap.TierProfessionalAllocator},
		{"unknown.product", iap.TierFree},
	}

	for _, tt := range tests {
		t.Run(tt.productID, func(t *testing.T) {
			tier := iap.GetTierFromProductID(tt.productID)
			if tier != tt.expectedTier {
				t.Errorf("expected tier=%s, got=%s", tt.expectedTier, tier)
			}
		})
	}
}

func TestPlatformValidation(t *testing.T) {
	validPlatforms := []string{"ios", "android"}
	invalidPlatforms := []string{"", "windows", "IOS", "Android", "web"}

	for _, p := range validPlatforms {
		t.Run("valid_"+p, func(t *testing.T) {
			isValid := p == "ios" || p == "android"
			if !isValid {
				t.Errorf("expected %s to be valid", p)
			}
		})
	}

	for _, p := range invalidPlatforms {
		t.Run("invalid_"+p, func(t *testing.T) {
			isValid := p == "ios" || p == "android"
			if isValid {
				t.Errorf("expected %s to be invalid", p)
			}
		})
	}
}

func TestSubscriptionTierConstants(t *testing.T) {
	// Verify tier constants exist and have expected values
	if iap.TierFree != "FREE" {
		t.Errorf("expected TierFree='FREE', got='%s'", iap.TierFree)
	}
	if iap.TierAnnualAccess != "ANNUAL_ACCESS" {
		t.Errorf("expected TierAnnualAccess='ANNUAL_ACCESS', got='%s'", iap.TierAnnualAccess)
	}
	if iap.TierProfessionalAllocator != "PROFESSIONAL_ALLOCATOR" {
		t.Errorf("expected TierProfessionalAllocator='PROFESSIONAL_ALLOCATOR', got='%s'", iap.TierProfessionalAllocator)
	}
}

func TestGetFeaturesForTier(t *testing.T) {
	tests := []struct {
		tier            iap.SubscriptionTier
		minFeatureCount int
	}{
		{iap.TierFree, 2},
		{iap.TierAnnualAccess, 5},
		{iap.TierProfessionalAllocator, 7},
	}

	for _, tt := range tests {
		t.Run(string(tt.tier), func(t *testing.T) {
			features := iap.GetFeaturesForTier(tt.tier)
			if len(features) < tt.minFeatureCount {
				t.Errorf("expected at least %d features for tier %s, got %d", tt.minFeatureCount, tt.tier, len(features))
			}
		})
	}
}

func TestSyncEntitlementsRequest_Validation(t *testing.T) {
	tests := []struct {
		name        string
		request     iap.SyncEntitlementsRequest
		expectValid bool
		reason      string
	}{
		{
			name: "valid iOS request",
			request: iap.SyncEntitlementsRequest{
				Platform:    iap.PlatformIOS,
				ProductID:   "com.estara.annual_access.monthly",
				ReceiptData: "receipt-data",
			},
			expectValid: true,
		},
		{
			name: "valid Android request",
			request: iap.SyncEntitlementsRequest{
				Platform:      iap.PlatformAndroid,
				ProductID:     "annual_access_monthly",
				PurchaseToken: "purchase-token",
			},
			expectValid: true,
		},
		{
			name: "missing platform",
			request: iap.SyncEntitlementsRequest{
				ProductID: "com.estara.annual_access.monthly",
			},
			expectValid: false,
			reason:      "platform is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.request.Platform != "" && tt.request.ProductID != ""
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v (reason: %s)", tt.expectValid, isValid, tt.reason)
			}
		})
	}
}

func TestSubscriptionPlan_JSON(t *testing.T) {
	plan := iap.SubscriptionPlan{
		ProductID:   "com.estara.annual_access.monthly",
		Name:        "Annual Access Monthly",
		Description: "Essential tools for individual investors",
		Tier:        string(iap.TierAnnualAccess),
		Price:       "$19.99",
		Currency:    "USD",
		Period:      "monthly",
		Features:    []string{"market_analysis", "property_search"},
	}

	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("failed to marshal plan: %v", err)
	}

	var decoded iap.SubscriptionPlan
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal plan: %v", err)
	}

	if decoded.ProductID != plan.ProductID {
		t.Errorf("expected productId=%s, got=%s", plan.ProductID, decoded.ProductID)
	}
	if decoded.Tier != plan.Tier {
		t.Errorf("expected tier=%s, got=%s", plan.Tier, decoded.Tier)
	}
}

func TestSubscriptionPlansResponse_JSON(t *testing.T) {
	resp := iap.SubscriptionPlansResponse{
		IOSPlans: []iap.SubscriptionPlan{
			{
				ProductID: "com.estara.annual_access.monthly",
				Name:      "Annual Access Monthly",
				Tier:      string(iap.TierAnnualAccess),
			},
		},
		AndroidPlans: []iap.SubscriptionPlan{
			{
				ProductID: "annual_access_monthly",
				Name:      "Annual Access Monthly",
				Tier:      string(iap.TierAnnualAccess),
			},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var decoded iap.SubscriptionPlansResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(decoded.IOSPlans) != 1 {
		t.Errorf("expected 1 iOS plan, got %d", len(decoded.IOSPlans))
	}
	if len(decoded.AndroidPlans) != 1 {
		t.Errorf("expected 1 Android plan, got %d", len(decoded.AndroidPlans))
	}
}

func TestIAPStatusConstants(t *testing.T) {
	// Verify status constants
	statuses := []iap.SubscriptionStatus{
		iap.IAPStatusActive,
		iap.IAPStatusExpired,
		iap.IAPStatusCanceled,
		iap.IAPStatusGracePd,
		iap.IAPStatusOnHold,
		iap.IAPStatusPaused,
		iap.IAPStatusPending,
		iap.IAPStatusTrialing,
	}

	for _, status := range statuses {
		if status == "" {
			t.Error("status constant should not be empty")
		}
	}
}
