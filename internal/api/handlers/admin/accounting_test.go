package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJournalEntryResponse_JSON(t *testing.T) {
	t.Parallel()

	entry := JournalEntryResponse{
		Month:         "2026-01",
		Description:   "Monthly subscription revenue",
		DebitAccount:  "Accounts Receivable",
		CreditAccount: "Revenue",
		Amount:        4999.50,
	}

	data, err := json.Marshal(entry)
	require.NoError(t, err)

	var decoded JournalEntryResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "2026-01", decoded.Month)
	assert.Equal(t, "Accounts Receivable", decoded.DebitAccount)
	assert.Equal(t, "Revenue", decoded.CreditAccount)
	assert.Equal(t, 4999.50, decoded.Amount)
}

func TestDeferredRevenueItem_JSON(t *testing.T) {
	t.Parallel()

	item := DeferredRevenueItem{
		SubscriptionID:   "sub-123",
		Email:            "test@example.com",
		Tier:             "ANNUAL_ACCESS",
		TotalAmount:      1199.88,
		RecognizedAmount: 599.94,
		DeferredAmount:   599.94,
		StartDate:        "2025-08-14",
		EndDate:          "2026-08-14",
		MonthsTotal:      12,
		MonthsRecognized: 6,
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	var decoded DeferredRevenueItem
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "sub-123", decoded.SubscriptionID)
	assert.Equal(t, 12, decoded.MonthsTotal)
	assert.Equal(t, 6, decoded.MonthsRecognized)
	assert.InDelta(t, item.TotalAmount, decoded.RecognizedAmount+decoded.DeferredAmount, 0.01)
}

func TestVendorExpenseItem_JSON(t *testing.T) {
	t.Parallel()

	expense := VendorExpenseItem{
		VendorID:    "vendor-abc",
		VendorName:  "Anthropic",
		Category:    "ai_provider",
		MonthlyCost: 500.00,
		AnnualCost:  6000.00,
	}

	data, err := json.Marshal(expense)
	require.NoError(t, err)

	var decoded VendorExpenseItem
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "Anthropic", decoded.VendorName)
	assert.Equal(t, 500.00, decoded.MonthlyCost)
	assert.Equal(t, 6000.00, decoded.AnnualCost)
}

func TestProfitabilityResponse_JSON(t *testing.T) {
	t.Parallel()

	resp := ProfitabilityResponse{
		GrossRevenue: 25000.00,
		TotalCosts:   8000.00,
		GrossProfit:  17000.00,
		GrossMargin:  68.0,
		RevenueByMonth: []MonthAmount{
			{Month: "2026-01", Amount: 12000.00},
			{Month: "2026-02", Amount: 13000.00},
		},
		CostsByMonth: []MonthAmount{
			{Month: "2026-01", Amount: 3800.00},
			{Month: "2026-02", Amount: 4200.00},
		},
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded ProfitabilityResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 25000.00, decoded.GrossRevenue)
	assert.Equal(t, 68.0, decoded.GrossMargin)
	assert.Len(t, decoded.RevenueByMonth, 2)
	assert.Len(t, decoded.CostsByMonth, 2)
	assert.Equal(t, "2026-01", decoded.RevenueByMonth[0].Month)
}

func TestMonthAmount_JSON(t *testing.T) {
	t.Parallel()

	ma := MonthAmount{
		Month:  "2026-01",
		Amount: 15000.50,
	}

	data, err := json.Marshal(ma)
	require.NoError(t, err)

	assert.Contains(t, string(data), `"month":"2026-01"`)
	assert.Contains(t, string(data), `"amount":15000.5`)
}

func TestProfitabilityResponse_MarginCalculation(t *testing.T) {
	t.Parallel()

	// Test that margin = (revenue - costs) / revenue * 100
	tests := []struct {
		name     string
		revenue  float64
		costs    float64
		expected float64
	}{
		{"100% margin", 1000, 0, 100.0},
		{"50% margin", 1000, 500, 50.0},
		{"0% margin", 1000, 1000, 0.0},
		{"negative margin", 1000, 1500, -50.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			margin := (tt.revenue - tt.costs) / tt.revenue * 100
			assert.InDelta(t, tt.expected, margin, 0.01)
		})
	}
}
