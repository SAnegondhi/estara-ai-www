package investment

// InvestmentStrategy represents the investment approach
type InvestmentStrategy string

const (
	// StrategyCashFlow prioritizes monthly cash flow
	StrategyCashFlow InvestmentStrategy = "cash_flow"
	// StrategyAppreciation prioritizes property value growth
	StrategyAppreciation InvestmentStrategy = "appreciation"
	// StrategyBalanced balances cash flow and appreciation
	StrategyBalanced InvestmentStrategy = "balanced"
)

// RiskTolerance represents the investor's risk tolerance level
type RiskTolerance string

const (
	RiskConservative RiskTolerance = "conservative"
	RiskModerate     RiskTolerance = "moderate"
	RiskAggressive   RiskTolerance = "aggressive"
)

// Recommendation represents the AI's recommendation for a property
type Recommendation string

const (
	RecommendationStrongBuy Recommendation = "STRONG_BUY"
	RecommendationBuy       Recommendation = "BUY"
	RecommendationHold      Recommendation = "HOLD"
	RecommendationPass      Recommendation = "PASS"
)

// InvestmentPlanningParams holds the parameters for investment planning
type InvestmentPlanningParams struct {
	Locations         []string           `json:"locations"`
	Budget            int                `json:"budget"`
	DownPaymentPct    float64            `json:"downPaymentPct"`
	Strategy          InvestmentStrategy `json:"strategy"`
	RiskTolerance     RiskTolerance      `json:"riskTolerance"`
	MaxProperties     int                `json:"maxProperties"`
	YearlyBudgets     []YearlyBudget     `json:"yearlyBudgets,omitempty"`
	ExistingPortfolio *ExistingPortfolio `json:"existingPortfolio,omitempty"`
}

// YearlyBudget represents budget allocation for a specific year in multi-year planning
type YearlyBudget struct {
	Year      int      `json:"year"`
	Budget    int      `json:"budget"`
	Locations []string `json:"locations,omitempty"` // Optional override; defaults to main locations
}

// ExistingPortfolio represents the user's current real estate portfolio
type ExistingPortfolio struct {
	Properties []ExistingProperty `json:"properties"`
}

// ExistingProperty represents a property already owned by the investor
type ExistingProperty struct {
	ID              string  `json:"id"`
	Address         string  `json:"address"`
	City            string  `json:"city"`
	State           string  `json:"state"`
	CurrentValue    int     `json:"currentValue"`
	MonthlyRent     int     `json:"monthlyRent"`
	MortgageBalance int     `json:"mortgageBalance"`
	Equity          int     `json:"equity"`
	MonthlyCashFlow int     `json:"monthlyCashFlow"`
	CapRate         float64 `json:"capRate,omitempty"`
}

// InvestmentPlanningResult holds the complete result of investment planning
type InvestmentPlanningResult struct {
	SelectedProperties []PropertyInPortfolio       `json:"selectedProperties"`
	Metrics            PortfolioMetrics            `json:"metrics"`
	GrowthProjection   GrowthProjection            `json:"growthProjection"`
	ExistingPortfolio  *ExistingPortfolioSummary   `json:"existingPortfolio,omitempty"`
	CombinedMetrics    *CombinedPortfolioMetrics   `json:"combinedMetrics,omitempty"`
	MultiYearPlan      *MultiYearProjection        `json:"multiYearPlan,omitempty"`
}

// PropertyInPortfolio represents a property selected for the portfolio
type PropertyInPortfolio struct {
	Property       Property `json:"property"`
	DownPayment    int      `json:"downPayment"`
	LoanAmount     int      `json:"loanAmount"`
	MonthlyPayment int      `json:"monthlyPayment"`
	MonthlyCashFlow int     `json:"monthlyCashFlow"`
	CapRate        float64  `json:"capRate"`
	CashOnCash     float64  `json:"cashOnCash"`
	DSCR           float64  `json:"dscr"`
	Score          float64  `json:"score"`
}

// Property represents a real estate property
type Property struct {
	ID            string  `json:"id"`
	Address       string  `json:"address"`
	City          string  `json:"city"`
	State         string  `json:"state"`
	ZipCode       string  `json:"zipCode,omitempty"`
	Price         int     `json:"price"`
	Beds          int     `json:"beds,omitempty"`
	Baths         float64 `json:"baths,omitempty"`
	Sqft          int     `json:"sqft,omitempty"`
	EstimatedRent int     `json:"estimatedRent,omitempty"`
	YearBuilt     int     `json:"yearBuilt,omitempty"`
	PropertyType  string  `json:"propertyType,omitempty"`
	ListingURL    string  `json:"listingUrl,omitempty"`
	ImageURL      string  `json:"imageUrl,omitempty"`
	DaysOnMarket  int     `json:"daysOnMarket,omitempty"`
	Provider      string  `json:"provider,omitempty"`
}

// ScoredProperty represents a property with AI-generated scores
type ScoredProperty struct {
	Property         Property       `json:"property"`
	OverallScore     float64        `json:"overallScore"`     // 0-100
	BuyabilityScore  float64        `json:"buyabilityScore"`  // 0-100
	RentabilityScore float64        `json:"rentabilityScore"` // 0-100
	ROIScore         float64        `json:"roiScore"`         // 0-100
	PortfolioFit     float64        `json:"portfolioFit"`     // 0-100
	Recommendation   Recommendation `json:"recommendation"`
	Rationale        string         `json:"rationale"`
}

// PortfolioMetrics holds aggregate metrics for the portfolio
type PortfolioMetrics struct {
	PropertyCount       int     `json:"propertyCount"`
	TotalInvestment     int     `json:"totalInvestment"`
	TotalDownPayment    int     `json:"totalDownPayment"`
	TotalLoanAmount     int     `json:"totalLoanAmount"`
	ProjectedValue      int     `json:"projectedValue"`
	AnnualCashFlow      int     `json:"annualCashFlow"`
	MonthlyCashFlow     int     `json:"monthlyCashFlow"`
	AvgCapRate          float64 `json:"avgCapRate"`
	AvgCashOnCash       float64 `json:"avgCashOnCash"`
	AvgDSCR             float64 `json:"avgDscr"`
	PortfolioDSCR       float64 `json:"portfolioDscr"`
	TotalEquity         int     `json:"totalEquity"`
	LeverageRatio       float64 `json:"leverageRatio"`
}

// GrowthProjection holds portfolio growth projections over time
type GrowthProjection struct {
	Years           int                 `json:"years"`
	YearlyData      []YearlyProjection  `json:"yearlyData"`
	FinalValue      int                 `json:"finalValue"`
	FinalEquity     int                 `json:"finalEquity"`
	FinalCashFlow   int                 `json:"finalCashFlow"`
	TotalAppreciation int               `json:"totalAppreciation"`
	TotalCashFlow   int                 `json:"totalCashFlow"`
	CAGR            float64             `json:"cagr"` // Compound Annual Growth Rate
}

// YearlyProjection holds projected values for a specific year
type YearlyProjection struct {
	Year           int     `json:"year"`
	PortfolioValue int     `json:"portfolioValue"`
	Equity         int     `json:"equity"`
	LoanBalance    int     `json:"loanBalance"`
	AnnualCashFlow int     `json:"annualCashFlow"`
	CumulativeCashFlow int `json:"cumulativeCashFlow"`
	Appreciation   int     `json:"appreciation"`
}

// ExistingPortfolioSummary summarizes the user's existing portfolio
type ExistingPortfolioSummary struct {
	PropertyCount   int      `json:"propertyCount"`
	TotalValue      int      `json:"totalValue"`
	TotalEquity     int      `json:"totalEquity"`
	TotalDebt       int      `json:"totalDebt"`
	AnnualCashFlow  int      `json:"annualCashFlow"`
	MonthlyCashFlow int      `json:"monthlyCashFlow"`
	Locations       []string `json:"locations"`
	AvgCapRate      float64  `json:"avgCapRate"`
}

// CombinedPortfolioMetrics shows before/after comparison
type CombinedPortfolioMetrics struct {
	// Before (existing)
	ExistingPropertyCount int `json:"existingPropertyCount"`
	ExistingTotalValue    int `json:"existingTotalValue"`
	ExistingAnnualCashFlow int `json:"existingAnnualCashFlow"`

	// After (existing + new)
	CombinedPropertyCount int `json:"combinedPropertyCount"`
	CombinedTotalValue    int `json:"combinedTotalValue"`
	CombinedAnnualCashFlow int `json:"combinedAnnualCashFlow"`

	// Improvement
	ValueIncrease              int     `json:"valueIncrease"`
	CashFlowIncrease           int     `json:"cashFlowIncrease"`
	DiversificationImprovement float64 `json:"diversificationImprovement"`
}

// MultiYearProjection holds the complete multi-year acquisition plan
type MultiYearProjection struct {
	Years             []YearlyAcquisitionPlan `json:"years"`
	CumulativeMetrics CumulativeMetrics       `json:"cumulativeMetrics"`
	GrowthChart       []GrowthChartPoint      `json:"growthChart"`
}

// YearlyAcquisitionPlan holds the plan for a single year
type YearlyAcquisitionPlan struct {
	Year             int                   `json:"year"`
	Budget           int                   `json:"budget"`
	AllocatedCapital int                   `json:"allocatedCapital"`
	Properties       []PropertyInPortfolio `json:"properties"`
	Metrics          YearlyPlanMetrics     `json:"metrics"`
}

// YearlyPlanMetrics holds metrics for a year's acquisitions
type YearlyPlanMetrics struct {
	PropertyCount      int `json:"propertyCount"`
	TotalInvestment    int `json:"totalInvestment"`
	ProjectedCashFlow  int `json:"projectedCashFlow"`
}

// CumulativeMetrics holds totals across all years
type CumulativeMetrics struct {
	TotalPropertyCount int `json:"totalPropertyCount"`
	TotalInvestment    int `json:"totalInvestment"`
	ProjectedValue     int `json:"projectedValue"`
}

// GrowthChartPoint represents a data point for growth visualization
type GrowthChartPoint struct {
	Year           int `json:"year"`
	PortfolioValue int `json:"portfolioValue"`
	CashFlow       int `json:"cashFlow"`
	Equity         int `json:"equity"`
}

// OptimizationRequest holds parameters for portfolio optimization
type OptimizationRequest struct {
	Properties        []Property         `json:"properties"`
	Budget            int                `json:"budget"`
	DownPaymentPct    float64            `json:"downPaymentPct"`
	Strategy          InvestmentStrategy `json:"strategy"`
	RiskTolerance     RiskTolerance      `json:"riskTolerance"`
	MaxProperties     int                `json:"maxProperties"`
	ExistingPortfolio *ExistingPortfolio `json:"existingPortfolio,omitempty"`
	MortgageRate      float64            `json:"mortgageRate"`
}

// OptimizationResult holds the result of portfolio optimization
type OptimizationResult struct {
	SelectedProperties []PropertyInPortfolio `json:"selectedProperties"`
	Metrics            PortfolioMetrics      `json:"metrics"`
	ScoredProperties   []ScoredProperty      `json:"scoredProperties"`
}

// MultiYearRequest holds parameters for multi-year optimization
type MultiYearRequest struct {
	Properties        []Property         `json:"properties"`
	YearlyBudgets     []YearlyBudget     `json:"yearlyBudgets"`
	DownPaymentPct    float64            `json:"downPaymentPct"`
	Strategy          InvestmentStrategy `json:"strategy"`
	RiskTolerance     RiskTolerance      `json:"riskTolerance"`
	ExistingPortfolio *ExistingPortfolio `json:"existingPortfolio,omitempty"`
	MortgageRate      float64            `json:"mortgageRate"`
}

// MultiYearResult holds the result of multi-year optimization
type MultiYearResult struct {
	MultiYearPlan     *MultiYearProjection      `json:"multiYearPlan"`
	ExistingPortfolio *ExistingPortfolioSummary `json:"existingPortfolio,omitempty"`
	CombinedMetrics   *CombinedPortfolioMetrics `json:"combinedMetrics,omitempty"`
}

// InvestorProfile represents the investor's profile for AI evaluation
type InvestorProfile struct {
	RiskTolerance     RiskTolerance      `json:"riskTolerance"`
	InvestmentHorizon string             `json:"investmentHorizon"`
	AvailableCapital  int                `json:"availableCapital"`
	Strategy          InvestmentStrategy `json:"strategy"`
}
