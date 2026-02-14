// Package memo provides Investment Decision Memo generation.
// A memo is a per-property analysis document with computed financials,
// AI-generated narratives, and market context.
package memo

import (
	"time"

	"github.com/estara-ai/www/internal/services/geospatial"
)

// MemoData is the complete data structure for one property's Decision Memo.
// It matches the 12-section layout from the marketing sample memo.
type MemoData struct {
	// Header
	PropertyAddress  string    `json:"propertyAddress"`
	PropertySubtitle string    `json:"propertySubtitle"` // "SFR · $425,000 · Built 2015 · 3/2 · 1,680 sqft"
	GeneratedAt      time.Time `json:"generatedAt"`
	Strategy         string    `json:"strategy"`

	// Status Row (6 items — AI-generated)
	Status       string `json:"status"`       // Qualified | Not Qualified
	RiskProfile  string `json:"riskProfile"`  // Low | Moderate | Elevated | High
	StrategyFit  int    `json:"strategyFit"`  // 0-100%
	Confidence   int    `json:"confidence"`   // 0-100%
	CapitalRole  string `json:"capitalRole"`  // Diversifier | Core | Value-Add | Opportunistic
	RelativeRank string `json:"relativeRank"` // "#2 of 7"

	// Executive Summary (AI-generated)
	ExecutiveSummary string `json:"executiveSummary"`

	// Key Financials (computed)
	KeyFinancials KeyFinancials `json:"keyFinancials"`

	// Underwriting Assumptions (computed — assumption vs market avg)
	Assumptions []AssumptionRow `json:"assumptions"`

	// 10-Year Cash Flow (computed)
	CashFlowProjections []YearCashFlow `json:"cashFlowProjections"`

	// Scenario Stress Testing (computed)
	Scenarios []ScenarioRow `json:"scenarios"`

	// Risk Assessment (AI-generated)
	RiskFactors    []RiskFactor `json:"riskFactors"`
	KeyUncertainty string       `json:"keyUncertainty"`

	// Market Context (from CitySnapshot + TimeSeries)
	MarketContext MarketContextData `json:"marketContext"`

	// Comparable Sales (from geo-spatial — nil until wired)
	Comparables *ComparablesData `json:"comparables,omitempty"`

	// Portfolio Impact (conditional — only if user has investment plan)
	PortfolioImpact *PortfolioImpactData `json:"portfolioImpact,omitempty"`

	// Decision Context (AI-generated)
	DecisionContext DecisionContext `json:"decisionContext"`

	// Original property data
	Property BatchPropertyInput `json:"property"`
}

// KeyFinancials holds the main financial metrics displayed in the memo.
type KeyFinancials struct {
	PurchasePrice    int     `json:"purchasePrice"`
	DownPayment      int     `json:"downPayment"`
	LoanAmount       int     `json:"loanAmount"`
	InterestRate     float64 `json:"interestRate"`
	MonthlyPayment   float64 `json:"monthlyPayment"`
	MonthlyRent      int     `json:"monthlyRent"`
	NOI              float64 `json:"noi"`              // Annual net operating income
	CapRate          float64 `json:"capRate"`           // %
	CashOnCashYr1    float64 `json:"cashOnCashYr1"`    // %
	DSCR             float64 `json:"dscr"`              // Debt Service Coverage Ratio
	IRR5Yr           float64 `json:"irr5Yr"`            // 5-year IRR %
	EquityMultiple10 float64 `json:"equityMultiple10"`  // 10-year equity multiple
	TotalExpenses    float64 `json:"totalExpenses"`     // Annual operating expenses
	ExpenseRatio     float64 `json:"expenseRatio"`      // Total expenses / Gross rent
}

// AssumptionRow compares an underwriting assumption against the market average.
type AssumptionRow struct {
	Parameter          string  `json:"parameter"`
	Assumption         string  `json:"assumption"`         // Value used in analysis
	MarketAvg          string  `json:"marketAvg"`          // Market average
	Variance           string  `json:"variance"`           // Difference
	VarianceDirection  string  `json:"varianceDirection"`  // "positive" | "negative" | "neutral"
}

// YearCashFlow holds projected cash flow for one year.
type YearCashFlow struct {
	Year             int     `json:"year"`
	GrossRent        float64 `json:"grossRent"`
	Expenses         float64 `json:"expenses"`
	NOI              float64 `json:"noi"`
	DebtService      float64 `json:"debtService"`
	NetCashFlow      float64 `json:"netCashFlow"`
	PropertyValue    float64 `json:"propertyValue"`
	Equity           float64 `json:"equity"`
	CumulativeCF     float64 `json:"cumulativeCF"`
}

// ScenarioRow represents one stress-test scenario.
type ScenarioRow struct {
	Name           string  `json:"name"`           // Base | Conservative | Downside
	IRR            float64 `json:"irr"`            // %
	CashOnCash     float64 `json:"cashOnCash"`     // %
	EquityMultiple float64 `json:"equityMultiple"`
	DSCR           float64 `json:"dscr"`
	RiskLevel      string  `json:"riskLevel"`      // Low | Moderate | High
}

// RiskFactor describes a single identified risk.
type RiskFactor struct {
	Factor   string `json:"factor"`
	Severity string `json:"severity"` // Low | Medium | High
	Detail   string `json:"detail"`
}

// MarketContextData holds market data for trend charts and context.
type MarketContextData struct {
	PriceTrend   []TimePoint `json:"priceTrend"`
	RentTrend    []TimePoint `json:"rentTrend"`
	SubjectPrice int         `json:"subjectPrice"`
	SubjectRent  int         `json:"subjectRent"`
	MedianPrice  int         `json:"medianPrice"`
	MedianRent   int         `json:"medianRent"`
	PriceCAGR    float64     `json:"priceCAGR"` // 5yr
	RentCAGR     float64     `json:"rentCAGR"`  // 5yr
	City         string      `json:"city"`
	State        string      `json:"state"`
}

// TimePoint is a single data point for trend charts.
type TimePoint struct {
	Date  string  `json:"date"` // "2024-Q1" or "2024-01"
	Value float64 `json:"value"`
}

// ComparablesData holds comparable sales/rentals from the geo-spatial service.
type ComparablesData struct {
	Sales   *geospatial.ComparableSalesResponse   `json:"sales,omitempty"`
	Rentals *geospatial.ComparableRentalsResponse  `json:"rentals,omitempty"`
}

// PortfolioImpactData shows how acquiring this property would change portfolio metrics.
type PortfolioImpactData struct {
	Rows                  []PortfolioImpactRow `json:"rows"`
	DiversificationNote   string               `json:"diversificationNote,omitempty"`
	ConcentrationWarning  string               `json:"concentrationWarning,omitempty"`
}

// PortfolioImpactRow shows one dimension of portfolio impact.
type PortfolioImpactRow struct {
	Dimension       string `json:"dimension"`       // e.g. "SFR Allocation"
	Current         string `json:"current"`
	PostAcquisition string `json:"postAcquisition"`
	Threshold       string `json:"threshold"`
	IsWarning       bool   `json:"isWarning"`
}

// DecisionContext provides the AI-generated decision framing.
type DecisionContext struct {
	PrimaryQuestion      string `json:"primaryQuestion"`
	Tradeoffs            string `json:"tradeoffs"`
	PipelineAlternatives string `json:"pipelineAlternatives"`
}

// BatchPropertyInput represents property data from the client for memo generation.
// Matches the discover handler's BatchPropertyInput.
type BatchPropertyInput struct {
	ID            string  `json:"id"`
	Address       string  `json:"address"`
	City          string  `json:"city"`
	State         string  `json:"state"`
	ZipCode       string  `json:"zipCode,omitempty"`
	Price         int     `json:"price"`
	Beds          int     `json:"beds,omitempty"`
	Baths         float64 `json:"baths,omitempty"`
	Sqft          int     `json:"sqft,omitempty"`
	YearBuilt     int     `json:"yearBuilt,omitempty"`
	EstimatedRent int     `json:"estimatedRent,omitempty"`
	PropertyType  string  `json:"propertyType,omitempty"`
	Latitude      float64 `json:"latitude,omitempty"`
	Longitude     float64 `json:"longitude,omitempty"`
}

// GenerateOptions controls memo generation behaviour.
type GenerateOptions struct {
	Strategy     string `json:"strategy"`     // e.g. "cash_flow", "appreciation", "balanced"
	UserID       string `json:"userId"`
	ForceRefresh bool   `json:"forceRefresh"`
}

// AIPropertyOutput is the structured AI response for a single property.
type AIPropertyOutput struct {
	Index               int            `json:"index"`
	Status              string         `json:"status"`
	RiskProfile         string         `json:"riskProfile"`
	StrategyFit         int            `json:"strategyFit"`
	Confidence          int            `json:"confidence"`
	CapitalRole         string         `json:"capitalRole"`
	ExecutiveSummary    string         `json:"executiveSummary"`
	RiskFactors         []RiskFactor   `json:"riskFactors"`
	KeyUncertainty      string         `json:"keyUncertainty"`
	DecisionContext     DecisionContext `json:"decisionContext"`
}

// AIBatchOutput is the structured response from the AI for a batch of properties.
type AIBatchOutput struct {
	Properties []AIPropertyOutput `json:"properties"`
}
