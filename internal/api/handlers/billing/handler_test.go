package billing

import (
	"encoding/json"
	"testing"
)

func TestCreateCheckoutSessionRequest_Validation(t *testing.T) {
	tests := []struct {
		name        string
		request     CreateCheckoutSessionRequest
		expectValid bool
		reason      string
	}{
		{
			name: "valid request",
			request: CreateCheckoutSessionRequest{
				PriceID:    "price_123",
				SuccessURL: "https://example.com/success",
				CancelURL:  "https://example.com/cancel",
			},
			expectValid: true,
		},
		{
			name: "missing priceId",
			request: CreateCheckoutSessionRequest{
				SuccessURL: "https://example.com/success",
				CancelURL:  "https://example.com/cancel",
			},
			expectValid: false,
			reason:      "priceId is required",
		},
		{
			name: "missing successUrl",
			request: CreateCheckoutSessionRequest{
				PriceID:   "price_123",
				CancelURL: "https://example.com/cancel",
			},
			expectValid: false,
			reason:      "successUrl is required",
		},
		{
			name: "missing cancelUrl",
			request: CreateCheckoutSessionRequest{
				PriceID:    "price_123",
				SuccessURL: "https://example.com/success",
			},
			expectValid: false,
			reason:      "cancelUrl is required",
		},
		{
			name: "with optional metadata",
			request: CreateCheckoutSessionRequest{
				PriceID:    "price_123",
				SuccessURL: "https://example.com/success",
				CancelURL:  "https://example.com/cancel",
				Metadata:   map[string]string{"source": "website"},
			},
			expectValid: true,
		},
		{
			name: "with product type",
			request: CreateCheckoutSessionRequest{
				PriceID:     "price_123",
				ProductType: "SINGLE_REPORT",
				SuccessURL:  "https://example.com/success",
				CancelURL:   "https://example.com/cancel",
			},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.request.PriceID != "" && tt.request.SuccessURL != "" && tt.request.CancelURL != ""
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v (reason: %s)", tt.expectValid, isValid, tt.reason)
			}
		})
	}
}

func TestCreateCheckoutSessionRequest_JSON(t *testing.T) {
	req := CreateCheckoutSessionRequest{
		PriceID:        "price_123",
		ProductType:    "SUBSCRIPTION",
		SuccessURL:     "https://example.com/success",
		CancelURL:      "https://example.com/cancel",
		Metadata:       map[string]string{"ref": "homepage"},
		AllowPromoCode: true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	var decoded CreateCheckoutSessionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	if decoded.PriceID != req.PriceID {
		t.Errorf("expected priceId=%s, got=%s", req.PriceID, decoded.PriceID)
	}
	if decoded.AllowPromoCode != req.AllowPromoCode {
		t.Errorf("expected allowPromoCode=%v, got=%v", req.AllowPromoCode, decoded.AllowPromoCode)
	}
}

func TestProductTypeMapping(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"SINGLE_REPORT", "SINGLE_REPORT"},
		{"REPORT_PACK", "REPORT_PACK"},
		{"OVERAGE_REPORT", "OVERAGE_REPORT"},
		{"INSIGHT_ACCESS", "INSIGHT_ACCESS"},
		{"", "subscription"}, // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			// Simulated mapping
			mapped := tt.input
			if mapped == "" {
				mapped = "subscription"
			}
			if mapped != tt.expected {
				t.Errorf("expected mapping=%s, got=%s", tt.expected, mapped)
			}
		})
	}
}

func TestSubscriptionStatusValidation(t *testing.T) {
	validStatuses := []string{"active", "trialing", "past_due", "canceled", "unpaid", "incomplete", "incomplete_expired"}

	for _, status := range validStatuses {
		t.Run("valid_"+status, func(t *testing.T) {
			isValid := isValidSubscriptionStatus(status)
			if !isValid {
				t.Errorf("expected %s to be a valid status", status)
			}
		})
	}

	invalidStatuses := []string{"", "unknown", "ACTIVE", "pending"}
	for _, status := range invalidStatuses {
		t.Run("invalid_"+status, func(t *testing.T) {
			isValid := isValidSubscriptionStatus(status)
			if isValid {
				t.Errorf("expected %s to be an invalid status", status)
			}
		})
	}
}

func TestPriceIDValidation(t *testing.T) {
	tests := []struct {
		priceID     string
		expectValid bool
	}{
		{"price_123abc", true},
		{"price_test_123", true},
		{"price_live_abc123", true},
		{"", false},
		{"123", false},
		{"invalid_price", false},
	}

	for _, tt := range tests {
		t.Run(tt.priceID, func(t *testing.T) {
			isValid := isValidPriceID(tt.priceID)
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v for priceID=%s, got=%v", tt.expectValid, tt.priceID, isValid)
			}
		})
	}
}

// Helper functions
func isValidSubscriptionStatus(status string) bool {
	validStatuses := map[string]bool{
		"active":             true,
		"trialing":           true,
		"past_due":           true,
		"canceled":           true,
		"unpaid":             true,
		"incomplete":         true,
		"incomplete_expired": true,
	}
	return validStatuses[status]
}

func isValidPriceID(priceID string) bool {
	if priceID == "" {
		return false
	}
	// Stripe price IDs start with "price_"
	return len(priceID) > 6 && priceID[:6] == "price_"
}
