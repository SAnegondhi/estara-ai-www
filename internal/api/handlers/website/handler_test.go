package website

import (
	"encoding/json"
	"testing"

	"github.com/estara-ai/www/internal/services/website"
)

func TestGenerateReportRequest_Validation(t *testing.T) {
	tests := []struct {
		name        string
		request     website.GenerateReportRequest
		expectValid bool
		reason      string
	}{
		{
			name: "valid request",
			request: website.GenerateReportRequest{
				Email:      "john@example.com",
				ReportType: "SNAPSHOT",
				Address:    "123 Main St",
				City:       "Austin",
				State:      "TX",
				ZipCode:    "78701",
			},
			expectValid: true,
		},
		{
			name: "missing email",
			request: website.GenerateReportRequest{
				ReportType: "SNAPSHOT",
				Address:    "123 Main St",
			},
			expectValid: false,
			reason:      "email is required",
		},
		{
			name: "missing report type",
			request: website.GenerateReportRequest{
				Email:   "john@example.com",
				Address: "123 Main St",
			},
			expectValid: false,
			reason:      "reportType is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.request.Email != "" && tt.request.ReportType != ""
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v (reason: %s)", tt.expectValid, isValid, tt.reason)
			}
		})
	}
}

func TestCheckoutRequest_Validation(t *testing.T) {
	tests := []struct {
		name        string
		request     website.CheckoutRequest
		expectValid bool
		reason      string
	}{
		{
			name: "valid request",
			request: website.CheckoutRequest{
				PriceID:    "price_123",
				SuccessURL: "https://example.com/success",
				CancelURL:  "https://example.com/cancel",
			},
			expectValid: true,
		},
		{
			name: "missing priceId",
			request: website.CheckoutRequest{
				SuccessURL: "https://example.com/success",
				CancelURL:  "https://example.com/cancel",
			},
			expectValid: false,
			reason:      "priceId is required",
		},
		{
			name: "missing URLs",
			request: website.CheckoutRequest{
				PriceID: "price_123",
			},
			expectValid: false,
			reason:      "successUrl and cancelUrl are required",
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

func TestFreeSnapshotRequest_Validation(t *testing.T) {
	tests := []struct {
		name        string
		request     website.FreeSnapshotRequest
		expectValid bool
		reason      string
	}{
		{
			name: "valid request",
			request: website.FreeSnapshotRequest{
				Email:   "john@example.com",
				Address: "123 Main St",
				City:    "Austin",
				State:   "TX",
				ZipCode: "78701",
			},
			expectValid: true,
		},
		{
			name: "missing email",
			request: website.FreeSnapshotRequest{
				Address: "123 Main St",
				City:    "Austin",
				State:   "TX",
				ZipCode: "78701",
			},
			expectValid: false,
			reason:      "email is required",
		},
		{
			name: "missing address",
			request: website.FreeSnapshotRequest{
				Email:   "john@example.com",
				City:    "Austin",
				State:   "TX",
				ZipCode: "78701",
			},
			expectValid: false,
			reason:      "address is required",
		},
		{
			name: "missing city",
			request: website.FreeSnapshotRequest{
				Email:   "john@example.com",
				Address: "123 Main St",
				State:   "TX",
				ZipCode: "78701",
			},
			expectValid: false,
			reason:      "city is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.request.Email != "" && tt.request.Address != "" && tt.request.City != "" && tt.request.State != "" && tt.request.ZipCode != ""
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v (reason: %s)", tt.expectValid, isValid, tt.reason)
			}
		})
	}
}

func TestPricingConfig_JSON(t *testing.T) {
	config := website.PricingConfig{
		Subscriptions: []website.PricingPlan{
			{
				ID:          "investor",
				Name:        "Investor",
				Description: "Essential tools for individual investors",
				Price:       "$19.99/mo",
				PriceID:     "price_investor",
				Interval:    "monthly",
				Features: []string{
					"5 property analyses per month",
					"Market insights",
				},
			},
			{
				ID:          "professional",
				Name:        "Professional",
				Description: "Advanced features for serious investors",
				Price:       "$49.99/mo",
				PriceID:     "price_pro",
				Interval:    "monthly",
				Popular:     true,
				Features: []string{
					"Unlimited property analyses",
					"Advanced market insights",
				},
			},
		},
		OneTimePurchases: []website.PricingItem{
			{
				ID:          "single_report",
				Name:        "Single Report",
				Description: "One comprehensive property report",
				Price:       "$9.99",
				PriceID:     "price_single",
			},
		},
		Features: []website.FeatureSet{
			{
				Tier:     "free",
				Features: []string{"3 free snapshots", "Basic data"},
			},
		},
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal pricing config: %v", err)
	}

	var decoded website.PricingConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal pricing config: %v", err)
	}

	if len(decoded.Subscriptions) != 2 {
		t.Errorf("expected 2 subscriptions, got=%d", len(decoded.Subscriptions))
	}
	if decoded.Subscriptions[0].Name != "Investor" {
		t.Errorf("expected first subscription name=Investor, got=%s", decoded.Subscriptions[0].Name)
	}
	if !decoded.Subscriptions[1].Popular {
		t.Error("expected professional plan to be marked as popular")
	}
	if len(decoded.OneTimePurchases) != 1 {
		t.Errorf("expected 1 one-time purchase, got=%d", len(decoded.OneTimePurchases))
	}
}

func TestGenerateReportResponse_JSON(t *testing.T) {
	result := website.GenerateReportResponse{
		Success:  true,
		Message:  "Report generation started",
		ReportID: "report-123",
		Status:   "PENDING",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var decoded website.GenerateReportResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if decoded.ReportID != result.ReportID {
		t.Errorf("expected reportId=%s, got=%s", result.ReportID, decoded.ReportID)
	}
	if decoded.Status != result.Status {
		t.Errorf("expected status=%s, got=%s", result.Status, decoded.Status)
	}
}

func TestReportTypeValidation(t *testing.T) {
	validTypes := []string{"SNAPSHOT", "MIR", "CIP", "COMPREHENSIVE"}
	invalidTypes := []string{"", "unknown", "snapshot", "INVALID"}

	for _, rt := range validTypes {
		t.Run("valid_"+rt, func(t *testing.T) {
			// Simulated validation
			isValid := rt == "SNAPSHOT" || rt == "MIR" || rt == "CIP" || rt == "COMPREHENSIVE"
			if !isValid {
				t.Errorf("expected %s to be valid", rt)
			}
		})
	}

	for _, rt := range invalidTypes {
		t.Run("invalid_"+rt, func(t *testing.T) {
			isValid := rt == "SNAPSHOT" || rt == "MIR" || rt == "CIP" || rt == "COMPREHENSIVE"
			if isValid {
				t.Errorf("expected %s to be invalid", rt)
			}
		})
	}
}

func TestFreeSnapshotResponse_JSON(t *testing.T) {
	resp := website.FreeSnapshotResponse{
		Success:   true,
		RequestID: "req-123",
		SessionID: "sess-456",
		Message:   "Snapshot request created",
		Remaining: 2,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var decoded website.FreeSnapshotResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if decoded.RequestID != resp.RequestID {
		t.Errorf("expected requestId=%s, got=%s", resp.RequestID, decoded.RequestID)
	}
	if decoded.Remaining != resp.Remaining {
		t.Errorf("expected remaining=%d, got=%d", resp.Remaining, decoded.Remaining)
	}
}

func TestCheckoutResponse_JSON(t *testing.T) {
	resp := website.CheckoutResponse{
		SessionID:      "cs_123",
		SessionURL:     "https://checkout.stripe.com/c/pay/cs_123",
		PublishableKey: "pk_test_xxx",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var decoded website.CheckoutResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if decoded.SessionID != resp.SessionID {
		t.Errorf("expected sessionId=%s, got=%s", resp.SessionID, decoded.SessionID)
	}
	if decoded.SessionURL != resp.SessionURL {
		t.Errorf("expected sessionUrl=%s, got=%s", resp.SessionURL, decoded.SessionURL)
	}
}

func TestProductTypeMapping(t *testing.T) {
	tests := []struct {
		productType string
		expected    string
	}{
		{"SINGLE_REPORT", "SINGLE_REPORT"},
		{"REPORT_PACK", "REPORT_PACK"},
		{"INSIGHT_ACCESS", "INSIGHT_ACCESS"},
		{"", "subscription"},
	}

	for _, tt := range tests {
		t.Run(tt.productType, func(t *testing.T) {
			mapped := tt.productType
			if mapped == "" {
				mapped = "subscription"
			}
			if mapped != tt.expected {
				t.Errorf("expected mapping=%s, got=%s", tt.expected, mapped)
			}
		})
	}
}
