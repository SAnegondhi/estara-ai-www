package portfolio

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/pkg/httputil"
)

// ──────────────────────────────────────────────
// Request / Response types
// ──────────────────────────────────────────────

type scenarioRequest struct {
	Scenario  json.RawMessage   `json:"scenario"`
	Scenarios []json.RawMessage `json:"scenarios"`
}

// scenarioTypeHolder is used for two-phase unmarshal
type scenarioTypeHolder struct {
	Type string `json:"type"`
}

// PurchaseScenarioInput – add a new property to the portfolio
type PurchaseScenarioInput struct {
	Type               string `json:"type"`
	DownPaymentPercent *float64 `json:"downPaymentPercent,omitempty"`
	LoanTermYears      *int     `json:"loanTermYears,omitempty"`
	InterestRate       *float64 `json:"interestRate,omitempty"`
	Property           struct {
		PurchasePrice float64  `json:"purchasePrice"`
		CurrentValue  *float64 `json:"currentValue,omitempty"`
		MonthlyRent   *float64 `json:"monthlyRent,omitempty"`
		VacancyRate   *float64 `json:"vacancyRate,omitempty"`
		MortgageRate  *float64 `json:"mortgageRate,omitempty"`
		LoanTermYears *int     `json:"loanTermYears,omitempty"`
		PropertyType  *string  `json:"propertyType,omitempty"`
		City          *string  `json:"city,omitempty"`
		State         *string  `json:"state,omitempty"`
		Expenses      *struct {
			Maintenance float64 `json:"maintenance"`
			Tax         float64 `json:"tax"`
			Insurance   float64 `json:"insurance"`
			HOA         float64 `json:"hoa"`
			Other       float64 `json:"other"`
		} `json:"expenses,omitempty"`
	} `json:"property"`
}

// SaleScenarioInput – remove a property from the portfolio
type SaleScenarioInput struct {
	Type          string   `json:"type"`
	PropertyID    string   `json:"propertyId"`
	SalePrice     *float64 `json:"salePrice,omitempty"`
	SellingCosts  *float64 `json:"sellingCosts,omitempty"` // percentage (default 6)
}

// RefinanceScenarioInput – change loan terms
type RefinanceScenarioInput struct {
	Type            string   `json:"type"`
	PropertyID      string   `json:"propertyId"`
	NewRate         float64  `json:"newRate"`
	NewLoanTermYears *int    `json:"newLoanTermYears,omitempty"`
	CashOutAmount   *float64 `json:"cashOutAmount,omitempty"`
}

// MarketChangeScenarioInput – apply value/rent changes across the whole portfolio
type MarketChangeScenarioInput struct {
	Type               string   `json:"type"`
	ValueChangePercent float64  `json:"valueChangePercent"`
	RentChangePercent  *float64 `json:"rentChangePercent,omitempty"`
}

// ScenarioSummary is the before/after snapshot of portfolio-level metrics
type ScenarioSummary struct {
	TotalValue      float64 `json:"totalValue"`
	TotalEquity     float64 `json:"totalEquity"`
	TotalDebt       float64 `json:"totalDebt"`
	MonthlyCashFlow float64 `json:"monthlyCashFlow"`
	CapRate         float64 `json:"portfolioCapRate"`
	LTV             float64 `json:"ltv"`
	PropertyCount   int     `json:"propertyCount"`
}

type ScenarioChange struct {
	Absolute float64 `json:"absolute"`
	Percent  float64 `json:"percent"`
}

type ScenarioChanges struct {
	TotalValue      ScenarioChange `json:"totalValue"`
	TotalEquity     ScenarioChange `json:"totalEquity"`
	TotalDebt       ScenarioChange `json:"totalDebt"`
	MonthlyCashFlow ScenarioChange `json:"monthlyCashFlow"`
	CapRate         struct {
		Absolute float64 `json:"absolute"`
	} `json:"portfolioCapRate"`
	LTV struct {
		Absolute float64 `json:"absolute"`
	} `json:"ltv"`
	PropertyCount int `json:"propertyCount"`
}

type ScenarioResult struct {
	ScenarioType string          `json:"scenarioType"`
	Description  string          `json:"description"`
	Before       ScenarioSummary `json:"before"`
	After        ScenarioSummary `json:"after"`
	Changes      ScenarioChanges `json:"changes"`
	Insights     []string        `json:"insights"`
	CalculatedAt string          `json:"calculatedAt"`
}

// ──────────────────────────────────────────────
// HTTP handler
// ──────────────────────────────────────────────

// CalculateScenarios handles POST /api/v2/portfolio/scenarios
func (h *Handler) CalculateScenarios(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	var req scenarioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	isSingle := len(req.Scenario) > 0 && string(req.Scenario) != "null"
	isCompare := len(req.Scenarios) > 0

	if !isSingle && !isCompare {
		httputil.BadRequest(w, `request must include either "scenario" or "scenarios" field`)
		return
	}

	if isCompare {
		if len(req.Scenarios) > 5 {
			httputil.BadRequest(w, "maximum 5 scenarios can be compared at once")
			return
		}
	}

	// Fetch user's current portfolio
	properties, err := h.getPropertiesByUserID(ctx, user.UserID)
	if err != nil {
		h.logger.Error("failed to fetch portfolio for scenario", "error", err, "user_id", user.UserID)
		httputil.InternalError(w, fmt.Errorf("failed to load portfolio"))
		return
	}

	if isSingle {
		result, err := h.runScenario(properties, req.Scenario)
		if err != nil {
			httputil.BadRequest(w, err.Error())
			return
		}
		httputil.JSON(w, http.StatusOK, map[string]any{
			"success": true,
			"result":  result,
		})
		return
	}

	// Compare multiple scenarios
	results := make([]ScenarioResult, 0, len(req.Scenarios))
	for _, raw := range req.Scenarios {
		r, err := h.runScenario(properties, raw)
		if err != nil {
			httputil.BadRequest(w, err.Error())
			return
		}
		results = append(results, *r)
	}
	httputil.JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"results": results,
	})
}

// ──────────────────────────────────────────────
// Dispatch
// ──────────────────────────────────────────────

func (h *Handler) runScenario(properties []PortfolioProperty, raw json.RawMessage) (*ScenarioResult, error) {
	var th scenarioTypeHolder
	if err := json.Unmarshal(raw, &th); err != nil {
		return nil, fmt.Errorf("invalid scenario: missing type")
	}
	switch th.Type {
	case "purchase":
		var inp PurchaseScenarioInput
		if err := json.Unmarshal(raw, &inp); err != nil {
			return nil, fmt.Errorf("invalid purchase scenario")
		}
		return calcPurchase(properties, inp)
	case "sale":
		var inp SaleScenarioInput
		if err := json.Unmarshal(raw, &inp); err != nil {
			return nil, fmt.Errorf("invalid sale scenario")
		}
		return calcSale(properties, inp)
	case "refinance":
		var inp RefinanceScenarioInput
		if err := json.Unmarshal(raw, &inp); err != nil {
			return nil, fmt.Errorf("invalid refinance scenario")
		}
		return calcRefinance(properties, inp)
	case "market_change":
		var inp MarketChangeScenarioInput
		if err := json.Unmarshal(raw, &inp); err != nil {
			return nil, fmt.Errorf("invalid market_change scenario")
		}
		return calcMarketChange(properties, inp)
	default:
		return nil, fmt.Errorf("unknown scenario type: %s", th.Type)
	}
}

// ──────────────────────────────────────────────
// Metric helpers
// ──────────────────────────────────────────────

func portfolioSummary(properties []PortfolioProperty) ScenarioSummary {
	s := ScenarioSummary{PropertyCount: len(properties)}
	var totalCapRateNumer, totalCapRateDenom float64
	for _, p := range properties {
		s.TotalValue += p.CurrentValue
		s.TotalEquity += p.Equity
		s.TotalDebt += p.MortgageBalance
		s.MonthlyCashFlow += p.MonthlyCashFlow
		if p.CurrentValue > 0 {
			annualNOI := (p.MonthlyRent - p.MonthlyExpenses) * 12
			totalCapRateNumer += annualNOI
			totalCapRateDenom += p.CurrentValue
		}
	}
	if totalCapRateDenom > 0 {
		s.CapRate = (totalCapRateNumer / totalCapRateDenom) * 100
	}
	if s.TotalValue > 0 {
		s.LTV = (s.TotalDebt / s.TotalValue) * 100
	}
	return s
}

func change(before, after float64) ScenarioChange {
	abs := after - before
	var pct float64
	if before != 0 {
		pct = (abs / math.Abs(before)) * 100
	}
	return ScenarioChange{Absolute: abs, Percent: pct}
}

func buildChanges(before, after ScenarioSummary) ScenarioChanges {
	c := ScenarioChanges{
		TotalValue:      change(before.TotalValue, after.TotalValue),
		TotalEquity:     change(before.TotalEquity, after.TotalEquity),
		TotalDebt:       change(before.TotalDebt, after.TotalDebt),
		MonthlyCashFlow: change(before.MonthlyCashFlow, after.MonthlyCashFlow),
		PropertyCount:   after.PropertyCount - before.PropertyCount,
	}
	c.CapRate.Absolute = after.CapRate - before.CapRate
	c.LTV.Absolute = after.LTV - before.LTV
	return c
}

func monthlyPayment(principal, annualRatePct float64, termYears int) float64 {
	if termYears <= 0 {
		return 0
	}
	r := annualRatePct / 100 / 12
	n := float64(termYears * 12)
	if r == 0 {
		return principal / n
	}
	return principal * (r * math.Pow(1+r, n)) / (math.Pow(1+r, n) - 1)
}

func enrichMetrics(p *PortfolioProperty) {
	p.Equity = p.CurrentValue - p.MortgageBalance
	p.MonthlyCashFlow = p.MonthlyRent - p.MortgagePayment - p.MonthlyExpenses
	if p.CurrentValue > 0 {
		annualNOI := (p.MonthlyRent - p.MonthlyExpenses) * 12
		p.CapRate = (annualNOI / p.CurrentValue) * 100
	}
}

// ──────────────────────────────────────────────
// Scenario calculators
// ──────────────────────────────────────────────

const (
	defaultDownPaymentPct  = 20.0
	defaultLoanTermYears   = 30
	defaultInterestRate    = 6.5
	defaultSellingCostsPct = 6.0
)

func calcPurchase(properties []PortfolioProperty, inp PurchaseScenarioInput) (*ScenarioResult, error) {
	if inp.Property.PurchasePrice <= 0 {
		return nil, fmt.Errorf("purchasePrice is required for purchase scenario")
	}

	before := portfolioSummary(properties)

	downPct := defaultDownPaymentPct
	if inp.DownPaymentPercent != nil {
		downPct = *inp.DownPaymentPercent
	}
	termYears := defaultLoanTermYears
	if inp.LoanTermYears != nil {
		termYears = *inp.LoanTermYears
	}
	rate := defaultInterestRate
	if inp.InterestRate != nil {
		rate = *inp.InterestRate
	}

	pp := inp.Property.PurchasePrice
	downPayment := pp * (downPct / 100)
	loanAmt := pp - downPayment

	cv := pp
	if inp.Property.CurrentValue != nil {
		cv = *inp.Property.CurrentValue
	}

	var grossRent float64
	if inp.Property.MonthlyRent != nil {
		grossRent = *inp.Property.MonthlyRent
	}

	vacancyPct := 5.0 // default 5%
	if inp.Property.VacancyRate != nil {
		vacancyPct = *inp.Property.VacancyRate
	}
	effectiveRent := grossRent * (1 - vacancyPct/100)

	var totalExpenses float64
	if inp.Property.Expenses != nil {
		e := inp.Property.Expenses
		totalExpenses = e.Maintenance + e.Tax + e.Insurance + e.HOA + e.Other
	}

	pmt := monthlyPayment(loanAmt, rate, termYears)

	newProp := PortfolioProperty{
		ID:              "simulated-purchase",
		Address:         "New Property",
		City:            derefStringVal(inp.Property.City, "Unknown"),
		State:           derefStringVal(inp.Property.State, "Unknown"),
		PurchasePrice:   pp,
		CurrentValue:    cv,
		MonthlyRent:     effectiveRent,
		MonthlyExpenses: totalExpenses,
		MortgageBalance: loanAmt,
		MortgageRate:    rate,
		MortgagePayment: pmt,
	}
	enrichMetrics(&newProp)

	after := portfolioSummary(append(properties, newProp))

	// Insights
	var insights []string
	cashFlow := effectiveRent - pmt - totalExpenses
	if grossRent > 0 {
		if cashFlow > 0 {
			insights = append(insights, fmt.Sprintf("Net cash flow $%s/mo after %.0f%% vacancy, expenses & debt service", fmtInt(int(cashFlow)), vacancyPct))
		} else {
			insights = append(insights, fmt.Sprintf("Negative cash flow of $%s/mo — rent may not cover expenses & mortgage", fmtInt(int(-cashFlow))))
		}
		// Cash-on-cash return
		if downPayment > 0 {
			annualCashFlow := cashFlow * 12
			coc := (annualCashFlow / downPayment) * 100
			insights = append(insights, fmt.Sprintf("Cash-on-cash return: %.1f%% (annual cash flow ÷ down payment)", coc))
		}
		// DSCR
		annualNOI := (effectiveRent - totalExpenses) * 12
		annualDebtService := pmt * 12
		if annualDebtService > 0 {
			dscr := annualNOI / annualDebtService
			if dscr >= 1.25 {
				insights = append(insights, fmt.Sprintf("DSCR %.2f — healthy (lenders typically require ≥1.25)", dscr))
			} else if dscr >= 1.0 {
				insights = append(insights, fmt.Sprintf("DSCR %.2f — marginal (below lender threshold of 1.25)", dscr))
			} else {
				insights = append(insights, fmt.Sprintf("DSCR %.2f — NOI does not cover debt service (risk of negative cash flow)", dscr))
			}
		}
	}
	if after.LTV > 75 {
		insights = append(insights, fmt.Sprintf("Portfolio LTV would increase to %.1f%% (high leverage)", after.LTV))
	}

	description := fmt.Sprintf("Purchase property at $%s with %.0f%% down", fmtInt(int(pp)), downPct)
	return &ScenarioResult{
		ScenarioType: "purchase",
		Description:  description,
		Before:       before,
		After:        after,
		Changes:      buildChanges(before, after),
		Insights:     insights,
		CalculatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func calcSale(properties []PortfolioProperty, inp SaleScenarioInput) (*ScenarioResult, error) {
	if inp.PropertyID == "" {
		return nil, fmt.Errorf("propertyId is required for sale scenario")
	}

	var prop *PortfolioProperty
	for i := range properties {
		if properties[i].ID == inp.PropertyID {
			prop = &properties[i]
			break
		}
	}
	if prop == nil {
		return nil, fmt.Errorf("property not found in portfolio")
	}

	before := portfolioSummary(properties)

	salePrice := prop.CurrentValue
	if salePrice == 0 {
		salePrice = prop.PurchasePrice
	}
	if inp.SalePrice != nil {
		salePrice = *inp.SalePrice
	}

	sellingCostsPct := defaultSellingCostsPct
	if inp.SellingCosts != nil {
		sellingCostsPct = *inp.SellingCosts
	}
	sellingCosts := salePrice * (sellingCostsPct / 100)
	netProceeds := salePrice - sellingCosts - prop.MortgageBalance

	remaining := make([]PortfolioProperty, 0, len(properties)-1)
	for _, p := range properties {
		if p.ID != inp.PropertyID {
			remaining = append(remaining, p)
		}
	}
	after := portfolioSummary(remaining)

	var insights []string
	if netProceeds > 0 {
		insights = append(insights, fmt.Sprintf("Sale would generate net proceeds of $%s", fmtInt(int(netProceeds))))
	} else {
		insights = append(insights, fmt.Sprintf("Sale would result in a shortfall of $%s", fmtInt(int(-netProceeds))))
	}
	gain := salePrice - prop.PurchasePrice
	if gain > 0 {
		gainPct := (gain / prop.PurchasePrice) * 100
		insights = append(insights, fmt.Sprintf("Gain on sale: $%s (%.1f%%)", fmtInt(int(gain)), gainPct))
	} else if gain < 0 {
		insights = append(insights, fmt.Sprintf("Loss on sale: $%s", fmtInt(int(-gain))))
	}

	description := fmt.Sprintf("Sell %s for $%s", prop.Address, fmtInt(int(salePrice)))
	return &ScenarioResult{
		ScenarioType: "sale",
		Description:  description,
		Before:       before,
		After:        after,
		Changes:      buildChanges(before, after),
		Insights:     insights,
		CalculatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func calcRefinance(properties []PortfolioProperty, inp RefinanceScenarioInput) (*ScenarioResult, error) {
	if inp.PropertyID == "" {
		return nil, fmt.Errorf("propertyId is required for refinance scenario")
	}
	if inp.NewRate <= 0 {
		return nil, fmt.Errorf("newRate is required for refinance scenario")
	}

	var propIdx int = -1
	for i := range properties {
		if properties[i].ID == inp.PropertyID {
			propIdx = i
			break
		}
	}
	if propIdx == -1 {
		return nil, fmt.Errorf("property not found in portfolio")
	}

	prop := properties[propIdx]
	if prop.MortgageBalance <= 0 {
		return nil, fmt.Errorf("property has no mortgage to refinance")
	}

	before := portfolioSummary(properties)

	cashOut := 0.0
	if inp.CashOutAmount != nil {
		cashOut = *inp.CashOutAmount
	}
	newBalance := prop.MortgageBalance + cashOut

	newTerm := defaultLoanTermYears
	if inp.NewLoanTermYears != nil {
		newTerm = *inp.NewLoanTermYears
	}

	oldPmt := monthlyPayment(prop.MortgageBalance, prop.MortgageRate, 30)
	newPmt := monthlyPayment(newBalance, inp.NewRate, newTerm)

	modified := prop
	modified.MortgageBalance = newBalance
	modified.MortgageRate = inp.NewRate
	modified.MortgagePayment = newPmt
	enrichMetrics(&modified)

	modifiedProps := make([]PortfolioProperty, len(properties))
	copy(modifiedProps, properties)
	modifiedProps[propIdx] = modified
	after := portfolioSummary(modifiedProps)

	var insights []string
	diff := oldPmt - newPmt
	if diff > 0 {
		insights = append(insights, fmt.Sprintf("Monthly payment would decrease by $%s", fmtInt(int(diff))))
		insights = append(insights, fmt.Sprintf("Annual savings: $%s", fmtInt(int(diff*12))))
	} else if diff < 0 {
		insights = append(insights, fmt.Sprintf("Monthly payment would increase by $%s", fmtInt(int(-diff))))
	}
	if cashOut > 0 {
		insights = append(insights, fmt.Sprintf("Cash-out amount: $%s", fmtInt(int(cashOut))))
	}
	rateDiff := prop.MortgageRate - inp.NewRate
	if rateDiff > 0 {
		insights = append(insights, fmt.Sprintf("Interest rate reduction: %.2f%%", rateDiff))
	}

	description := fmt.Sprintf("Refinance %s at %.2f%%", prop.Address, inp.NewRate)
	if cashOut > 0 {
		description += fmt.Sprintf(" with $%s cash-out", fmtInt(int(cashOut)))
	}
	return &ScenarioResult{
		ScenarioType: "refinance",
		Description:  description,
		Before:       before,
		After:        after,
		Changes:      buildChanges(before, after),
		Insights:     insights,
		CalculatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func calcMarketChange(properties []PortfolioProperty, inp MarketChangeScenarioInput) (*ScenarioResult, error) {
	before := portfolioSummary(properties)

	rentChgPct := 0.0
	if inp.RentChangePercent != nil {
		rentChgPct = *inp.RentChangePercent
	}

	modified := make([]PortfolioProperty, len(properties))
	for i, p := range properties {
		cv := p.CurrentValue
		if cv == 0 {
			cv = p.PurchasePrice
		}
		p.CurrentValue = math.Round(cv * (1 + inp.ValueChangePercent/100))
		if p.MonthlyRent > 0 {
			p.MonthlyRent = math.Round(p.MonthlyRent * (1 + rentChgPct/100))
		}
		enrichMetrics(&p)
		modified[i] = p
	}
	after := portfolioSummary(modified)

	var insights []string
	if inp.ValueChangePercent < 0 {
		insights = append(insights, fmt.Sprintf("Portfolio value would decrease by %.0f%%", math.Abs(inp.ValueChangePercent)))
		if after.LTV > 80 {
			insights = append(insights, fmt.Sprintf("Warning: LTV would exceed 80%% (%.1f%%)", after.LTV))
		}
	} else if inp.ValueChangePercent > 0 {
		insights = append(insights, fmt.Sprintf("Portfolio value would increase by %.0f%%", inp.ValueChangePercent))
	}
	if rentChgPct > 0 {
		insights = append(insights, fmt.Sprintf("Rents would increase by %.0f%%", rentChgPct))
	} else if rentChgPct < 0 {
		insights = append(insights, fmt.Sprintf("Rents would decrease by %.0f%%", math.Abs(rentChgPct)))
	}

	parts := []string{}
	if inp.ValueChangePercent != 0 {
		sign := "+"
		if inp.ValueChangePercent < 0 {
			sign = ""
		}
		parts = append(parts, fmt.Sprintf("%s%.0f%% property values", sign, inp.ValueChangePercent))
	}
	if rentChgPct != 0 {
		sign := "+"
		if rentChgPct < 0 {
			sign = ""
		}
		parts = append(parts, fmt.Sprintf("%s%.0f%% rents", sign, rentChgPct))
	}
	description := "Market change"
	if len(parts) > 0 {
		description += ": " + strings.Join(parts, ", ")
	}

	return &ScenarioResult{
		ScenarioType: "market_change",
		Description:  description,
		Before:       before,
		After:        after,
		Changes:      buildChanges(before, after),
		Insights:     insights,
		CalculatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// ──────────────────────────────────────────────
// Local helpers
// ──────────────────────────────────────────────

func fmtInt(n int) string {
	if n < 0 {
		return "-" + fmtInt(-n)
	}
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func derefStringVal(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}
