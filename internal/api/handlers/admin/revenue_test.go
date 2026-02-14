package admin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTierMRR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tier     string
		expected float64
	}{
		{"ANNUAL_ACCESS", 99.99},
		{"PROFESSIONAL_ALLOCATOR", 149.99},
		{"AAPI_INVESTOR", 79.99},
		{"API_INVESTOR", 79.99},
		{"AAPI_ALLOCATOR", 199.99},
		{"API_ALLOCATOR", 199.99},
		{"INVESTOR", 29.99},
		{"PROFESSIONAL", 49.99},
		{"FREE", 0},
		{"", 0},
		{"UNKNOWN_TIER", 0},
	}

	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			got := tierMRR(tt.tier)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestRevenueSummaryResponse_JSON(t *testing.T) {
	t.Parallel()

	resp := RevenueSummaryResponse{
		MRR:          12345.67,
		ARR:          148148.04,
		ARPU:         49.99,
		LTV:          599.88,
		ChurnRate:    3.5,
		GrowthRate:   8.2,
		ActiveSubs:   247,
		NewThisMonth: 15,
		ChurnedMonth: 8,
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded RevenueSummaryResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, resp.MRR, decoded.MRR)
	assert.Equal(t, resp.ARR, decoded.ARR)
	assert.Equal(t, resp.ARPU, decoded.ARPU)
	assert.Equal(t, resp.ActiveSubs, decoded.ActiveSubs)
	assert.Equal(t, resp.ChurnRate, decoded.ChurnRate)
}

func TestRevenueTrendPoint_JSON(t *testing.T) {
	t.Parallel()

	point := RevenueTrendPoint{
		Month:     "2026-01",
		MRR:       10000.0,
		NewSubs:   25,
		Churned:   3,
		NetGrowth: 22,
	}

	data, err := json.Marshal(point)
	require.NoError(t, err)

	var decoded RevenueTrendPoint
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, point.Month, decoded.Month)
	assert.Equal(t, point.MRR, decoded.MRR)
	assert.Equal(t, point.NetGrowth, decoded.NetGrowth)
}

func TestRevenueByTierItem_JSON(t *testing.T) {
	t.Parallel()

	item := RevenueByTierItem{
		Tier:       "ANNUAL_ACCESS",
		Count:      50,
		MRR:        4999.50,
		Percentage: 40.5,
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"tier":"ANNUAL_ACCESS"`)
	assert.Contains(t, string(data), `"count":50`)
}

func TestAtRiskCustomerResponse_JSON(t *testing.T) {
	t.Parallel()

	daysSince := 15
	lastActive := "2026-01-30T10:00:00Z"

	resp := AtRiskCustomerResponse{
		UserID:          "user-123",
		Email:           "test@example.com",
		Tier:            "ANNUAL_ACCESS",
		RiskLevel:       "HIGH",
		RiskScore:       75,
		Reason:          "No login for 15 days",
		LastActive:      &lastActive,
		DaysSinceActive: &daysSince,
		MRR:             99.99,
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded AtRiskCustomerResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "HIGH", decoded.RiskLevel)
	assert.NotNil(t, decoded.DaysSinceActive)
	assert.Equal(t, 15, *decoded.DaysSinceActive)
}

func TestAtRiskCustomerResponse_JSON_NilOptionals(t *testing.T) {
	t.Parallel()

	resp := AtRiskCustomerResponse{
		UserID:    "user-456",
		Email:     "inactive@example.com",
		Tier:      "FREE",
		RiskLevel: "CRITICAL",
		RiskScore: 90,
		Reason:    "Dispute filed",
		MRR:       0,
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	// omitempty should omit nil optional fields
	assert.NotContains(t, string(data), `"lastActive"`)
	assert.NotContains(t, string(data), `"daysSinceActive"`)
}

func TestChargebackRateResponse_JSON(t *testing.T) {
	t.Parallel()

	resp := ChargebackRateResponse{
		Rate:                0.45,
		DisputeCount:        3,
		TransactionCount:    670,
		VisaThreshold:       0.90,
		MastercardThreshold: 1.00,
		Status:              "OK",
		Period:              "90 days",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded ChargebackRateResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "OK", decoded.Status)
	assert.Equal(t, 0.90, decoded.VisaThreshold)
	assert.Equal(t, 1.00, decoded.MastercardThreshold)
}

func TestExportCSV(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()

	headers := []string{"Name", "Value"}
	rows := [][]string{
		{"Alice", "100"},
		{"Bob", "200"},
	}

	exportCSV(rr, "test.csv", headers, rows)

	assert.Equal(t, "text/csv", rr.Header().Get("Content-Type"))
	assert.Contains(t, rr.Header().Get("Content-Disposition"), "test.csv")

	body := rr.Body.String()
	assert.Contains(t, body, "Name,Value")
	assert.Contains(t, body, "Alice,100")
	assert.Contains(t, body, "Bob,200")
}
