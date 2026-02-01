package ai

import (
	"net/http/httptest"
	"testing"
)

func TestParseBoundedIntQuery(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "http://example.com/api?limit=20&page=2", nil)
	if got := parseBoundedIntQuery(req, "limit", 50, 1, 100); got != 20 {
		t.Fatalf("limit got %d, want 20", got)
	}
	if got := parseBoundedIntQuery(req, "page", 1, 1, 10_000); got != 2 {
		t.Fatalf("page got %d, want 2", got)
	}

	req = httptest.NewRequest("GET", "http://example.com/api?limit=notanint&page=-5", nil)
	if got := parseBoundedIntQuery(req, "limit", 50, 1, 100); got != 50 {
		t.Fatalf("limit got %d, want default 50", got)
	}
	if got := parseBoundedIntQuery(req, "page", 1, 1, 10_000); got != 1 {
		t.Fatalf("page got %d, want min 1", got)
	}

	req = httptest.NewRequest("GET", "http://example.com/api?limit=999", nil)
	if got := parseBoundedIntQuery(req, "limit", 50, 1, 100); got != 100 {
		t.Fatalf("limit got %d, want max 100", got)
	}
}

func TestParseKeyForLocation(t *testing.T) {
	t.Parallel()

	got := parseKeyForLocation("investment_plan_chicago_il_dp20_ir725_oe50_balanced")
	if got != "Chicago, IL" {
		t.Fatalf("got %q, want %q", got, "Chicago, IL")
	}
}

func TestParseFinancialAssumptionsFromKey(t *testing.T) {
	t.Parallel()

	got := parseFinancialAssumptionsFromKey("investment_plan_chicago_il_dp20_ir725_oe50_balanced")
	if got == nil {
		t.Fatalf("expected assumptions map, got nil")
	}
	if got["downPaymentPercent"] != 20 {
		t.Fatalf("downPaymentPercent got %#v, want 20", got["downPaymentPercent"])
	}
	if got["operatingExpenses"] != 50 {
		t.Fatalf("operatingExpenses got %#v, want 50", got["operatingExpenses"])
	}
	rate, ok := got["mortgageRate"].(float64)
	if !ok {
		t.Fatalf("mortgageRate type got %T, want float64", got["mortgageRate"])
	}
	if rate != 7.25 {
		t.Fatalf("mortgageRate got %v, want 7.25", rate)
	}
}

func TestMapMetricsForHistory(t *testing.T) {
	t.Parallel()

	input := map[string]interface{}{
		"propertyCount":       5.0,
		"totalDownPayment":    123456.0,
		"expectedAnnualReturn": 9.5,
		"avgCashOnCash":       6.2,
		"annualCashFlow":      7890.0,
		"projectedValue":      999999.0,
		"totalLoanAmount":     555555.0,
	}

	out := mapMetricsForHistory(input)
	if out == nil {
		t.Fatalf("expected out, got nil")
	}
	if out["propertyCount"].(int) != 5 {
		t.Fatalf("propertyCount got %#v, want 5", out["propertyCount"])
	}
	if out["totalInvestment"].(int) != 123456 {
		t.Fatalf("totalInvestment got %#v, want 123456", out["totalInvestment"])
	}
	if out["projectedReturn"].(float64) != 9.5 {
		t.Fatalf("projectedReturn got %#v, want 9.5", out["projectedReturn"])
	}
	if out["cashOnCashReturn"].(float64) != 6.2 {
		t.Fatalf("cashOnCashReturn got %#v, want 6.2", out["cashOnCashReturn"])
	}
	if out["annualCashFlow"].(int) != 7890 {
		t.Fatalf("annualCashFlow got %#v, want 7890", out["annualCashFlow"])
	}
	if out["totalValue"].(int) != 999999 {
		t.Fatalf("totalValue got %#v, want 999999", out["totalValue"])
	}
	if out["totalDebt"].(int) != 555555 {
		t.Fatalf("totalDebt got %#v, want 555555", out["totalDebt"])
	}
}

