package investment

// InvestmentStrategy represents the investment approach
type InvestmentStrategy string

const (
	// StrategyCashFlow prioritizes monthly cash flow
	StrategyCashFlow InvestmentStrategy = "cash-flow"
	// StrategyAppreciation prioritizes property value growth
	StrategyAppreciation InvestmentStrategy = "appreciation"
	// StrategyBalanced balances cash flow and appreciation
	StrategyBalanced InvestmentStrategy = "balanced"
	// StrategyRiskAdjusted balances returns with volatility and stability
	StrategyRiskAdjusted InvestmentStrategy = "risk-adjusted"
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
	IncludeSuburbs    bool               `json:"includeSuburbs"`
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
	SelectedProperties []PropertyInPortfolio     `json:"selectedProperties"`
	Metrics            PortfolioMetrics          `json:"metrics"`
	GrowthProjection   GrowthProjection          `json:"growthProjection"`
	ExistingPortfolio  *ExistingPortfolioSummary `json:"existingPortfolio,omitempty"`
	CombinedMetrics    *CombinedPortfolioMetrics `json:"combinedMetrics,omitempty"`
	MultiYearPlan      *MultiYearProjection      `json:"multiYearPlan,omitempty"`
	MarketFilters      *MarketFilterSummary      `json:"marketFilters,omitempty"`
	MarketQuality      []LocationMarketAnalysis  `json:"marketQuality,omitempty"`
	Concentration      *ConcentrationMetrics     `json:"concentration,omitempty"`

	// www_v1 parity: Additional analysis fields
	Allocations             map[string]LocationAllocation `json:"allocations,omitempty"`
	AllocationRationale     map[string]string             `json:"allocationRationale,omitempty"`
	Recommendations         []string                      `json:"recommendations,omitempty"`
	DiversificationAnalysis *DiversificationAnalysis      `json:"diversificationAnalysis,omitempty"`
	RiskAnalysis            *RiskAnalysis                 `json:"riskAnalysis,omitempty"`

	// ADR-059: Selection mode fields
	Mode                    PlanningMode             `json:"mode,omitempty"`
	PropertyRecommendations []PropertyRecommendation `json:"propertyRecommendations,omitempty"`
	ReinvestmentAnalysis    *ReinvestmentAnalysis    `json:"reinvestmentAnalysis,omitempty"`

	// ADR-063: Unified Portfolio Projection Engine
	// User's original inputs stored for transparency
	UserAssumptions *UserFinancialAssumptions `json:"userAssumptions,omitempty"`
	// All 3 scenario projections computed by backend (single source of truth)
	ScenarioProjections *ScenarioProjections `json:"scenarioProjections,omitempty"`
}

// LocationAllocation represents allocation breakdown by location
type LocationAllocation struct {
	Percentage       float64 `json:"percentage"`
	InvestmentAmount int     `json:"investmentAmount"`
	PropertyCount    int     `json:"propertyCount"`
}

// DiversificationAnalysis contains portfolio diversification metrics
type DiversificationAnalysis struct {
	DiversificationScore float64             `json:"diversificationScore"`
	Correlations         []MarketCorrelation `json:"correlations,omitempty"`
	Opportunities        []string            `json:"opportunities,omitempty"`
	DataQualityNote      string              `json:"dataQualityNote,omitempty"`
}

// MarketCorrelation represents correlation between two markets
type MarketCorrelation struct {
	Market1          string  `json:"market1"`
	Market2          string  `json:"market2"`
	Correlation      float64 `json:"correlation"`
	PriceCorrelation float64 `json:"priceCorrelation,omitempty"` // ZHVI correlation
	RentCorrelation  float64 `json:"rentCorrelation,omitempty"`  // ZORI correlation
}

// RiskAnalysis contains portfolio risk metrics
type RiskAnalysis struct {
	PortfolioRisk    float64          `json:"portfolioRisk"`
	RiskDistribution RiskDistribution `json:"riskDistribution"`
	Warnings         []string         `json:"warnings,omitempty"`
}

// RiskDistribution shows risk breakdown by level
type RiskDistribution struct {
	Low    float64 `json:"low"`
	Medium float64 `json:"medium"`
	High   float64 `json:"high"`
}

// PropertyInPortfolio represents a property selected for the portfolio
type PropertyInPortfolio struct {
	Property        Property `json:"property"`
	DownPayment     int      `json:"downPayment"`
	LoanAmount      int      `json:"loanAmount"`
	MonthlyPayment  int      `json:"monthlyPayment"`
	MonthlyCashFlow int      `json:"monthlyCashFlow"`
	CapRate         float64  `json:"capRate"`
	CashOnCash      float64  `json:"cashOnCash"`
	DSCR            float64  `json:"dscr"`
	Score           float64  `json:"score"`

	// Operating expenses (calculated per property)
	Expenses *PropertyExpenses `json:"expenses,omitempty"`
}

// PropertyExpenses holds calculated operating expenses for a property
type PropertyExpenses struct {
	PropertyTax      float64 `json:"propertyTax"`      // Annual property tax
	Insurance        float64 `json:"insurance"`        // Annual insurance
	Maintenance      float64 `json:"maintenance"`      // Annual maintenance
	VacancyAllowance float64 `json:"vacancyAllowance"` // Annual vacancy reserve
	PropertyMgmt     float64 `json:"propertyMgmt"`     // Annual property management
	TotalAnnual      float64 `json:"totalAnnual"`      // Total annual expenses
	NOI              float64 `json:"noi"`              // Net Operating Income

	// Rates used (for transparency in projections)
	PropertyTaxRate  float64 `json:"propertyTaxRate"`  // % of home value
	InsuranceRate    float64 `json:"insuranceRate"`    // % of home value
	MaintenanceRate  float64 `json:"maintenanceRate"`  // % of home value
	VacancyRate      float64 `json:"vacancyRate"`      // % of rent
	PropertyMgmtRate float64 `json:"propertyMgmtRate"` // % of rent
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
	Latitude      float64 `json:"latitude,omitempty"`
	Longitude     float64 `json:"longitude,omitempty"`
}

// ScoredProperty represents a property with AI-generated scores
type ScoredProperty struct {
	Property            Property       `json:"property"`
	OverallScore        float64        `json:"overallScore"`     // 0-100
	BuyabilityScore     float64        `json:"buyabilityScore"`  // 0-100
	RentabilityScore    float64        `json:"rentabilityScore"` // 0-100
	ROIScore            float64        `json:"roiScore"`         // 0-100
	PortfolioFit        float64        `json:"portfolioFit"`     // 0-100
	Recommendation      Recommendation `json:"recommendation"`
	Rationale           string         `json:"rationale"`
	MarketQualityScore  float64        `json:"marketQualityScore,omitempty"`
	MarketQualityRating string         `json:"marketQualityRating,omitempty"`
}

// PortfolioMetrics holds aggregate metrics for the portfolio
type PortfolioMetrics struct {
	PropertyCount        int     `json:"propertyCount"`
	TotalInvestment      int     `json:"totalInvestment"`
	TotalDownPayment     int     `json:"totalDownPayment"`
	TotalLoanAmount      int     `json:"totalLoanAmount"`
	ProjectedValue       int     `json:"projectedValue"`
	AnnualCashFlow       int     `json:"annualCashFlow"`
	MonthlyCashFlow      int     `json:"monthlyCashFlow"`
	AvgCapRate           float64 `json:"avgCapRate"`
	AvgCashOnCash        float64 `json:"avgCashOnCash"`
	ExpectedAnnualReturn float64 `json:"expectedAnnualReturn"` // Total return: CoC + appreciation + equity buildup
	AvgDSCR              float64 `json:"avgDscr"`
	PortfolioDSCR        float64 `json:"portfolioDscr"`
	TotalEquity          int     `json:"totalEquity"`
	LeverageRatio        float64 `json:"leverageRatio"`

	// Additional metrics for client compatibility
	DebtServiceCoverage  float64 `json:"debtServiceCoverage"`  // Alias for PortfolioDSCR
	SharpeRatio          float64 `json:"sharpeRatio"`          // Risk-adjusted return
	DiversificationScore float64 `json:"diversificationScore"` // 0-100 score
	TotalROI             float64 `json:"totalROI"`             // 5-year total return %
	LocationCount        int     `json:"locationCount"`        // Number of unique locations

	// 5-year projection for portfolio view (derived from GrowthProjection)
	FiveYearProjection *FiveYearProjectionData `json:"fiveYearProjection,omitempty"`

	// Projection assumptions for transparency (appreciation rate, rent growth, etc.)
	ProjectionAssumptions *ProjectionAssumptions `json:"projectionAssumptions,omitempty"`
}

// FiveYearProjectionData holds simplified 5-year projection for client display
type FiveYearProjectionData struct {
	Year1 YearProjectionData `json:"year1"`
	Year2 YearProjectionData `json:"year2"`
	Year3 YearProjectionData `json:"year3"`
	Year4 YearProjectionData `json:"year4"`
	Year5 YearProjectionData `json:"year5"`
}

// YearProjectionData holds value and cash flow for a single year
type YearProjectionData struct {
	Value    int `json:"value"`
	CashFlow int `json:"cashFlow"`
}

// GrowthProjection holds portfolio growth projections over time
type GrowthProjection struct {
	Years             int                    `json:"years"`
	YearlyData        []YearlyProjection     `json:"yearlyData"`
	FinalValue        int                    `json:"finalValue"`
	FinalEquity       int                    `json:"finalEquity"`
	FinalCashFlow     int                    `json:"finalCashFlow"`
	TotalAppreciation int                    `json:"totalAppreciation"`
	TotalCashFlow     int                    `json:"totalCashFlow"`
	CAGR              float64                `json:"cagr"`                   // Compound Annual Growth Rate
	Assumptions       *ProjectionAssumptions `json:"assumptions,omitempty"` // All assumptions used with sources
}

// YearlyProjection holds projected values for a specific year
type YearlyProjection struct {
	Year               int `json:"year"`
	PortfolioValue     int `json:"portfolioValue"`
	Equity             int `json:"equity"`
	LoanBalance        int `json:"loanBalance"`
	AnnualCashFlow     int `json:"annualCashFlow"`
	CumulativeCashFlow int `json:"cumulativeCashFlow"`
	Appreciation       int `json:"appreciation"`
	CashBalance        int `json:"cashBalance"` // Uninvested reinvestment cash held for future acquisitions
	// Detailed income/expense breakdown for reinvestment analysis
	GrossRentalIncome  int `json:"grossRentalIncome"`
	OperatingExpenses  int `json:"operatingExpenses"`
	NetOperatingIncome int `json:"netOperatingIncome"`
	DebtService        int `json:"debtService"`
	PropertyCount          int `json:"propertyCount"`
	PropertiesAcquired     int `json:"propertiesAcquired"`
	CapExReserve           int `json:"capExReserve"`
	CumulativeCapExReserve int `json:"cumulativeCapExReserve"`
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
	ExistingPropertyCount  int `json:"existingPropertyCount"`
	ExistingTotalValue     int `json:"existingTotalValue"`
	ExistingAnnualCashFlow int `json:"existingAnnualCashFlow"`

	// After (existing + new)
	CombinedPropertyCount  int `json:"combinedPropertyCount"`
	CombinedTotalValue     int `json:"combinedTotalValue"`
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
	PropertyCount     int `json:"propertyCount"`
	TotalInvestment   int `json:"totalInvestment"`
	ProjectedCashFlow int `json:"projectedCashFlow"`
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
	SelectedProperties      []PropertyInPortfolio         `json:"selectedProperties"`
	Metrics                 PortfolioMetrics              `json:"metrics"`
	ScoredProperties        []ScoredProperty              `json:"scoredProperties"`
	Concentration           *ConcentrationMetrics         `json:"concentration,omitempty"`
	MarketFilters           *MarketFilterSummary          `json:"marketFilters,omitempty"`
	MarketQuality           []LocationMarketAnalysis      `json:"marketQuality,omitempty"`
	Allocations             map[string]LocationAllocation `json:"allocations,omitempty"`
	AllocationRationale     map[string]string             `json:"allocationRationale,omitempty"`
	Recommendations         []string                      `json:"recommendations,omitempty"`
	DiversificationAnalysis *DiversificationAnalysis      `json:"diversificationAnalysis,omitempty"`
	RiskAnalysis            *RiskAnalysis                 `json:"riskAnalysis,omitempty"`
}

// MarketFilterCriteria defines kill-criteria thresholds for market screening.
type MarketFilterCriteria struct {
	MinPriceGrowth5Y    float64 `json:"minPriceGrowth5Y"`
	MaxPriceVolatility  float64 `json:"maxPriceVolatility"`
	MaxUnemploymentRate float64 `json:"maxUnemploymentRate"`
	MinEmploymentGrowth float64 `json:"minEmploymentGrowth"`
	MinPopulationGrowth float64 `json:"minPopulationGrowth"`
	MaxVacancyRate      float64 `json:"maxVacancyRate"`
	MaxPriceToIncome    float64 `json:"maxPriceToIncome"`
	MinCapRate          float64 `json:"minCapRate"`
}

// MarketFilterResult captures the outcome of filtering for a single location.
type MarketFilterResult struct {
	Location       string   `json:"location"`
	Passed         bool     `json:"passed"`
	FailedCriteria []string `json:"failedCriteria"`
}

// MarketFilterSummary summarizes market filter results across locations.
type MarketFilterSummary struct {
	Passed       []MarketFilterResult `json:"passed"`
	Failed       []MarketFilterResult `json:"failed"`
	TotalMarkets int                  `json:"totalMarkets"`
	PassRate     float64              `json:"passRate"`
}

// MarketScoreBreakdown captures component scores for market quality.
type MarketScoreBreakdown struct {
	Appreciation  MarketScoreCategory `json:"appreciation"`
	DemandHeat    MarketScoreCategory `json:"demandHeat"`
	Employment    MarketScoreCategory `json:"employment"`
	Demographics  MarketScoreCategory `json:"demographics"`
	Affordability MarketScoreCategory `json:"affordability"`
}

// MarketScoreCategory captures score and component values for a category.
type MarketScoreCategory struct {
	Score      float64            `json:"score"`
	MaxScore   float64            `json:"maxScore"`
	Components map[string]float64 `json:"components,omitempty"`
}

// MarketQualityScore represents a market quality rating.
type MarketQualityScore struct {
	Score     int                  `json:"score"`
	Rating    string               `json:"rating"`
	Breakdown MarketScoreBreakdown `json:"breakdown"`
}

// LocationMarketAnalysis captures per-location market quality details.
type LocationMarketAnalysis struct {
	Location             string               `json:"location"`
	MarketQualityScore   int                  `json:"marketQualityScore"`
	MarketQualityRating  string               `json:"marketQualityRating"`
	Breakdown            MarketScoreBreakdown `json:"breakdown"`
	PriceGrowth5Y        *float64             `json:"priceGrowth5Y,omitempty"`
	RentGrowth5Y         *float64             `json:"rentGrowth5Y,omitempty"`
	PriceVolatility      *float64             `json:"priceVolatility,omitempty"`
	UnemploymentRate     *float64             `json:"unemploymentRate,omitempty"`
	EmploymentGrowthRate *float64             `json:"employmentGrowthRate,omitempty"`
	MarketHeatIndex      *float64             `json:"marketHeatIndex,omitempty"`
	MedianDaysOnMarket   *float64             `json:"medianDaysOnMarket,omitempty"`
	AffordabilityIndex   *float64             `json:"affordabilityIndex,omitempty"`
	VacancyRate          *float64             `json:"vacancyRate,omitempty"`
	PopulationGrowth     *float64             `json:"populationGrowth,omitempty"`
}

// ConcentrationMetrics captures portfolio concentration measures.
type ConcentrationMetrics struct {
	HHIMarket          float64 `json:"hhiMarket"`
	HHISubmarket       float64 `json:"hhiSubmarket"`
	HHIType            float64 `json:"hhiType"`
	GeoCluster         float64 `json:"geoCluster"`
	ConcentrationIndex float64 `json:"concentrationIndex"`
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
	MarketFilters     *MarketFilterSummary      `json:"marketFilters,omitempty"`
	MarketQuality     []LocationMarketAnalysis  `json:"marketQuality,omitempty"`
}

// InvestorProfile represents the investor's profile for AI evaluation
type InvestorProfile struct {
	RiskTolerance     RiskTolerance      `json:"riskTolerance"`
	InvestmentHorizon string             `json:"investmentHorizon"`
	AvailableCapital  int                `json:"availableCapital"`
	Strategy          InvestmentStrategy `json:"strategy"`
}

// ============================================================================
// Selection Mode Types (ADR-059)
// ============================================================================

// PlanningMode indicates how the investment plan was created
type PlanningMode string

const (
	// PlanningModeSearch indicates properties were found via search
	PlanningModeSearch PlanningMode = "search"
	// PlanningModeSelection indicates properties were pre-selected by user
	PlanningModeSelection PlanningMode = "selection"
)

// SelectedProperty represents a property pre-selected by the user from Discovery
type SelectedProperty struct {
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
	CapRate       float64 `json:"capRate,omitempty"`
	YearBuilt     int     `json:"yearBuilt,omitempty"`
	PropertyType  string  `json:"propertyType,omitempty"`
	DaysOnMarket  int     `json:"daysOnMarket,omitempty"`
	ImageURL      string  `json:"imageUrl,omitempty"`
	ListingURL    string  `json:"listingUrl,omitempty"`
}

// RecommendationAction represents the action to take on a property
type RecommendationAction string

const (
	// RecommendationActionKeep indicates the property should be held
	RecommendationActionKeep RecommendationAction = "KEEP"
	// RecommendationActionDivest indicates the property should be sold
	RecommendationActionDivest RecommendationAction = "DIVEST"
	// RecommendationActionAcquire indicates the property should be purchased
	RecommendationActionAcquire RecommendationAction = "ACQUIRE"
)

// PropertyRecommendation represents a keep/divest/acquire recommendation
type PropertyRecommendation struct {
	Property   PropertyInPortfolio  `json:"property"`
	Action     RecommendationAction `json:"action"`
	Confidence float64              `json:"confidence"` // 0-100
	Reasoning  string               `json:"reasoning"`

	// Scoring breakdown
	Scores PropertyRecommendationScores `json:"scores"`

	// Projected metrics
	Metrics PropertyRecommendationMetrics `json:"metrics"`

	// For DIVEST recommendations
	DivestReason         string `json:"divestReason,omitempty"`
	EstimatedSaleProceeds int    `json:"estimatedSaleProceeds,omitempty"`
	BetterUseOfCapital   string `json:"betterUseOfCapital,omitempty"`

	// For ACQUIRE recommendations
	AcquisitionPriority   int `json:"acquisitionPriority,omitempty"` // 1 = highest
	RequiredDownPayment   int `json:"requiredDownPayment,omitempty"`
}

// PropertyRecommendationScores holds the scoring breakdown for a recommendation
type PropertyRecommendationScores struct {
	PerformanceScore   float64 `json:"performanceScore"`   // 0-40 (40% weight)
	MarketQualityScore float64 `json:"marketQualityScore"` // 0-30 (30% weight)
	PortfolioFitScore  float64 `json:"portfolioFitScore"`  // 0-20 (20% weight)
	OpportunityCost    float64 `json:"opportunityCost"`    // 0-10 (10% weight)
	TotalScore         float64 `json:"totalScore"`         // 0-100
}

// PropertyRecommendationMetrics holds projected metrics for a recommendation
type PropertyRecommendationMetrics struct {
	ProjectedAnnualCashFlow int     `json:"projectedAnnualCashFlow"`
	ProjectedCapRate        float64 `json:"projectedCapRate"`
	ProjectedROI            float64 `json:"projectedROI"`
	ProjectedAppreciation   float64 `json:"projectedAppreciation"` // 5Y estimate
}

// ReinvestmentAnalysis holds the comparison of base vs reinvest scenarios
type ReinvestmentAnalysis struct {
	Enabled           bool               `json:"enabled"`
	ReinvestmentRate  float64            `json:"reinvestmentRate"` // 0-100
	ProjectionYears   int                `json:"projectionYears"`
	BaseScenario      []YearlyProjection `json:"baseScenario"`
	ReinvestScenario  []YearlyProjection `json:"reinvestScenario"`
	CumulativeDiff    ReinvestmentDiff   `json:"cumulativeDifference"`
	CompoundedReturns CompoundedReturns  `json:"compoundedReturns"`
	Assumptions       *ProjectionAssumptions `json:"assumptions,omitempty"` // All assumptions used with sources
}

// ReinvestmentDiff holds the cumulative difference at key years
type ReinvestmentDiff struct {
	Year5  ReinvestmentDiffPoint `json:"year5"`
	Year10 ReinvestmentDiffPoint `json:"year10"`
}

// ReinvestmentDiffPoint holds the difference for a specific year
type ReinvestmentDiffPoint struct {
	Value    int `json:"value"`
	CashFlow int `json:"cashFlow"`
	Equity   int `json:"equity"`
}

// CompoundedReturns holds the total compounded returns from reinvestment
type CompoundedReturns struct {
	TotalReinvested         int `json:"totalReinvested"`
	AdditionalPropertyValue int `json:"additionalPropertyValue"`
	AdditionalCashFlow      int `json:"additionalCashFlow"`
}

// ProjectionAssumptions captures all assumptions used in projections for transparency
type ProjectionAssumptions struct {
	// Financing assumptions
	MortgageRate   float64 `json:"mortgageRate"`   // Annual interest rate (%)
	MortgageSource string  `json:"mortgageSource"` // "FRED 30Y Fixed" or "Default (7%)"
	DownPaymentPct float64 `json:"downPaymentPct"` // Down payment percentage (e.g., 0.20 for 20%)
	LoanTermYears  int     `json:"loanTermYears"`  // Loan term in years (e.g., 30)

	// Growth rate assumptions
	AppreciationRate   float64 `json:"appreciationRate"`   // Annual property appreciation (%)
	AppreciationSource string  `json:"appreciationSource"` // "Market Data 5Y CAGR" or "Default (3%)"
	RentGrowthRate     float64 `json:"rentGrowthRate"`     // Annual rent growth (%)
	RentGrowthSource   string  `json:"rentGrowthSource"`   // "Market Data 5Y CAGR" or "Default (2%)"

	// Expense assumptions (for acquisitions)
	ExpenseRatio     float64                     `json:"expenseRatio"`                // Total expenses as % of rent
	ExpenseSource    string                      `json:"expenseSource"`               // "State-specific calculation" or "Default (50%)"
	ExpenseBreakdown *ProjectionExpenseBreakdown `json:"expenseBreakdown,omitempty"`

	// Capital expenditure reserve assumptions
	CapExReserveRate   float64              `json:"capExReserveRate"`             // Age-adjusted annual rate (% of value)
	CapExReserveSource string               `json:"capExReserveSource"`           // "Component lifecycle annualization"
	CapExComponents    []CapExComponentDetail `json:"capExComponents,omitempty"`   // Per-component breakdown

	// Acquisition assumptions
	AcquisitionPrice       int    `json:"acquisitionPrice"`       // Price used for simulated acquisitions
	AcquisitionPriceSource string `json:"acquisitionPriceSource"` // "Market Data Median" or "Portfolio Average"
	AcquisitionRent        int    `json:"acquisitionRent"`        // Monthly rent used for simulated acquisitions
	AcquisitionRentSource  string `json:"acquisitionRentSource"`  // "Market Data Median" or "Portfolio Average"
	AcquisitionLocation    string `json:"acquisitionLocation"`    // Target location for acquisitions

	// Data quality indicators
	OverallConfidence float64  `json:"overallConfidence"` // 0-100 confidence score
	DataQualityNotes  []string `json:"dataQualityNotes"`  // List of data quality warnings/notes
}

// ProjectionExpenseBreakdown provides detailed expense rate breakdown for transparency
type ProjectionExpenseBreakdown struct {
	PropertyTaxRate  float64 `json:"propertyTaxRate"`  // % of home value
	PropertyTaxState string  `json:"propertyTaxState"` // State used for tax rate
	InsuranceRate    float64 `json:"insuranceRate"`    // % of home value
	InsuranceState   string  `json:"insuranceState"`   // State used for insurance rate
	MaintenanceRate  float64 `json:"maintenanceRate"`  // % of home value
	MaintenanceAge   string  `json:"maintenanceAge"`   // Age category: "new", "modern", "established", "older", "historic"
	VacancyRate      float64 `json:"vacancyRate"`      // % of rent
	VacancySource    string  `json:"vacancySource"`    // "FRED Local" or "National Average"
	ManagementRate   float64 `json:"managementRate"`   // % of rent
	MarketClass      string  `json:"marketClass"`      // "A", "B", or "C"
}

// CapExComponentDetail provides per-component CapEx reserve detail for assumptions display
type CapExComponentDetail struct {
	Name             string `json:"name"`
	LifespanYears    int    `json:"lifespanYears"`
	EstRemainingLife int    `json:"estRemainingLife"`
	AnnualReserve    int    `json:"annualReserve"`
	RiskLevel        string `json:"riskLevel"` // "low", "medium", "high"
}

// DivestAnalysisResult holds the result of analyzing a property for divest
type DivestAnalysisResult struct {
	Property           Property                     `json:"property"`
	Action             RecommendationAction         `json:"action"`
	Confidence         float64                      `json:"confidence"`
	Reasoning          string                       `json:"reasoning"`
	Scores             PropertyRecommendationScores `json:"scores"`
	EstimatedProceeds  int                          `json:"estimatedProceeds,omitempty"`
	BetterAlternatives []Property                   `json:"betterAlternatives,omitempty"`
}

// ============================================================================
// ADR-063: Unified Portfolio Projection Engine
// ============================================================================

// UserFinancialAssumptions stores the user's input values for transparency
// These are what the user entered in the form, separate from what was actually
// used in calculations (which may be overridden by state-specific data)
type UserFinancialAssumptions struct {
	MortgageRate       float64 `json:"mortgageRate"`       // User's entered mortgage rate (%)
	DownPaymentPercent float64 `json:"downPaymentPercent"` // User's entered down payment (%)
	OperatingExpenses  float64 `json:"operatingExpenses"`  // User's entered op expenses (%)
	LoanTermYears      int     `json:"loanTermYears"`      // Loan term (default 30)
	TargetCashFlow     int     `json:"targetCashFlow,omitempty"` // User's target monthly cash flow
}

// ExpandedYearProjection has all metrics needed for detailed display
// This replaces the minimal YearProjectionData structure
type ExpandedYearProjection struct {
	Year               int     `json:"year"`
	PortfolioValue     int     `json:"value"`             // Total property value
	Equity             int     `json:"equity"`            // Value - loan balance
	LoanBalance        int     `json:"loanBalance"`       // Remaining mortgage
	AnnualCashFlow     int     `json:"cashFlow"`          // Annual cash flow after expenses (before tax)
	NetOperatingIncome int     `json:"noi"`               // NOI (rent - operating expenses)
	GrossRent              int     `json:"grossRent"`              // Total annual rent before vacancy
	VacancyLoss            int     `json:"vacancyLoss"`            // Vacancy portion of operating expenses
	EffectiveGrossIncome   int     `json:"effectiveGrossIncome"`   // GrossRent - VacancyLoss
	OperatingExpenses      int     `json:"operatingExpenses"`      // Total operating expenses
	DebtService        int     `json:"debtService"`       // Annual mortgage payments
	InterestExpense    int     `json:"interestExpense"`   // Interest portion of debt service (deductible)
	PrincipalPayment   int     `json:"principalPayment"`  // Principal portion of debt service
	Depreciation       int     `json:"depreciation"`      // Annual depreciation (value/27.5 for residential)
	TaxableIncome      int     `json:"taxableIncome"`     // NOI - interest - depreciation
	IncomeTaxes        int     `json:"incomeTaxes"`       // Taxable income × tax rate (if positive)
	CashFlowAfterTax   int     `json:"cashFlowAfterTax"`  // Cash flow - income taxes
	CashOnCash         float64 `json:"cashOnCash"`        // Cash flow / down payment (%)
	CapRate            float64 `json:"capRate"`           // NOI / value (%)
	EquityMultiple     float64 `json:"equityMultiple"`    // Current equity / initial investment
	CumulativeCashFlow     int     `json:"cumulativeCashFlow"`     // Total cash flow to date
	Appreciation           int     `json:"appreciation"`           // Value gain this year
	CapExReserve           int     `json:"capExReserve"`           // Annual CapEx reserve
	CumulativeCapExReserve int     `json:"cumulativeCapExReserve"` // Cumulative CapEx reserves
}

// ScenarioProjections contains all 3 scenarios computed by backend
// This is the single source of truth - client should not recalculate
type ScenarioProjections struct {
	Base        []ExpandedYearProjection `json:"base"`
	Optimistic  []ExpandedYearProjection `json:"optimistic"`
	Pessimistic []ExpandedYearProjection `json:"pessimistic"`
	Assumptions *ProjectionAssumptions   `json:"assumptions"`
	Summary     *ScenarioSummary         `json:"summary,omitempty"`
}

// ScenarioSummary provides quick comparison across scenarios
type ScenarioSummary struct {
	// Base scenario final values
	BaseFinalValue    int     `json:"baseFinalValue"`
	BaseFinalEquity   int     `json:"baseFinalEquity"`
	BaseTotalCashFlow int     `json:"baseTotalCashFlow"`
	BaseCAGR          float64 `json:"baseCAGR"`

	// Optimistic scenario final values
	OptimisticFinalValue    int     `json:"optimisticFinalValue"`
	OptimisticFinalEquity   int     `json:"optimisticFinalEquity"`
	OptimisticTotalCashFlow int     `json:"optimisticTotalCashFlow"`
	OptimisticCAGR          float64 `json:"optimisticCAGR"`

	// Pessimistic scenario final values
	PessimisticFinalValue    int     `json:"pessimisticFinalValue"`
	PessimisticFinalEquity   int     `json:"pessimisticFinalEquity"`
	PessimisticTotalCashFlow int     `json:"pessimisticTotalCashFlow"`
	PessimisticCAGR          float64 `json:"pessimisticCAGR"`

	// Scenario multipliers used
	OptimisticMultiplier  float64 `json:"optimisticMultiplier"`  // e.g., 1.15 for +15%
	PessimisticMultiplier float64 `json:"pessimisticMultiplier"` // e.g., 0.85 for -15%
}

// ============================================================================
// ADR-088: Scenario Planning v2.1 Types (Phase 0)
// ============================================================================

// PropertyThesis holds AI-generated investment analysis for a property
type PropertyThesis struct {
	InvestmentThesis string   `json:"investmentThesis"` // 2-3 sentences summarizing opportunity
	KeyStrengths     []string `json:"keyStrengths"`     // Exactly 2 strengths
	KeyRisks         []string `json:"keyRisks"`         // Exactly 2 risks
	MarketContext    string   `json:"marketContext"`    // 1 sentence on market conditions
	CapexAlert       *string  `json:"capexAlert"`       // null or warning message if needed
}

// FrontierPoint represents one configuration on the efficient frontier
type FrontierPoint struct {
	ConfigIndex         int                     `json:"configIndex"`         // 0-4 for up to 5 points
	Properties          []PropertyInPortfolio   `json:"properties"`          // 5-8 properties in this config
	ExpectedReturn      float64                 `json:"expectedReturn"`      // Annual expected return (%)
	PortfolioVolatility float64                 `json:"portfolioVolatility"` // Markowitz analytical volatility
	SharpeScore         float64                 `json:"sharpeScore"`         // Risk-adjusted return score
	ConcentrationIndex  float64                 `json:"concentrationIndex"`  // Weighted concentration metric
	StressTestEquity    int                     `json:"stressTestEquity"`    // Final equity under stress scenario
	SimulationResults   *SimulationResults      `json:"simulationResults"`   // Monte Carlo outcomes
	ReinvestmentPlan    *DualTrackReinvestment  `json:"reinvestmentPlan"`    // Track A + Track B details
}

// SimulationResults holds Monte Carlo simulation outcomes
type SimulationResults struct {
	Paths           []MonteCarloPath    `json:"paths"`           // All 1,000 paths (full detail)
	Percentiles     *PercentileOutcomes `json:"percentiles"`     // P10, P25, P50, P75, P90, P95
	BaseProjection  []YearOutcome       `json:"baseProjection"`  // P50 path (median)
	UpsideCase      []YearOutcome       `json:"upsideCase"`      // P75 MC (lazy-loaded)
	StressTest      []YearOutcome       `json:"stressTest"`      // Deterministic stress scenario
	MonteCarloSeed  int64               `json:"monteCarloSeed"`  // Random seed for reproducibility
}

// MonteCarloPath represents a single simulation path (1 of 1,000)
type MonteCarloPath struct {
	PathIndex       int           `json:"pathIndex"`       // 0-999
	YearlyOutcomes  []YearOutcome `json:"yearlyOutcomes"`  // Year-by-year results
	TrackBFiredYear int           `json:"trackBFiredYear"` // Year Track B triggered (0 = never)
	FinalNetWorth   int           `json:"finalNetWorth"`   // Year 10 net worth
}

// YearOutcome represents portfolio state for a single year in a simulation path
type YearOutcome struct {
	Year                int `json:"year"`                // 1-10
	PortfolioValue      int `json:"portfolioValue"`      // Total property value
	Equity              int `json:"equity"`              // Portfolio value - loan balance
	CumulativeCashFlow  int `json:"cumulativeCashFlow"`  // Cumulative cash flow to date
	NetWorth            int `json:"netWorth"`            // Equity + cumulative cash flow
}

// PercentileOutcomes holds percentile distributions across all MC paths
type PercentileOutcomes struct {
	P10 []YearOutcome `json:"p10"` // 10th percentile (downside case)
	P25 []YearOutcome `json:"p25"` // 25th percentile
	P50 []YearOutcome `json:"p50"` // 50th percentile (median, Base scenario)
	P75 []YearOutcome `json:"p75"` // 75th percentile (upside)
	P90 []YearOutcome `json:"p90"` // 90th percentile
	P95 []YearOutcome `json:"p95"` // 95th percentile (best case)
}

// DecisionVerdict holds AI-generated recommendation for a scenario
type DecisionVerdict struct {
	Recommendation    DecisionRecommendation `json:"recommendation"`    // PROCEED, PAUSE, or REVISE
	DecisionRationale string                 `json:"decisionRationale"` // 2-3 paragraphs explaining recommendation
	KeyConditions     []string               `json:"keyConditions"`     // 3-5 conditions for thesis to hold
	MonitoringSignals []MonitoringSignal     `json:"monitoringSignals"` // 3 leading indicators to track
	GeneratedAt       string                 `json:"generatedAt"`       // Timestamp (ISO 8601)
}

// DecisionRecommendation represents the AI's verdict
type DecisionRecommendation string

const (
	DecisionProceed DecisionRecommendation = "PROCEED" // Portfolio meets goals, execute plan
	DecisionPause   DecisionRecommendation = "PAUSE"   // Uncertainty too high, wait for clarity
	DecisionRevise  DecisionRecommendation = "REVISE"  // Adjust parameters and re-run
)

// MonitoringSignal represents a leading indicator to track over time
type MonitoringSignal struct {
	Indicator     string  `json:"indicator"`     // e.g., "Austin Metro Unemployment Rate"
	Description   string  `json:"description"`   // Why this metric matters
	CurrentValue  float64 `json:"currentValue"`  // Current value of indicator
	WatchLevel    float64 `json:"watchLevel"`    // Informational threshold (no action)
	WarnLevel     float64 `json:"warnLevel"`     // Review assumptions threshold
	AlertLevel    float64 `json:"alertLevel"`    // Trigger re-analysis threshold
	Justification string  `json:"justification"` // Why these thresholds matter
}

// DualTrackReinvestment holds reinvestment plan details
type DualTrackReinvestment struct {
	TrackA TrackAReinvestment `json:"trackA"` // External capital (user-declared budgets)
	TrackB TrackBReinvestment `json:"trackB"` // Internal cash flow (threshold-triggered)
}

// TrackAReinvestment holds external capital reinvestment schedule
type TrackAReinvestment struct {
	YearlyBudgets []YearlyBudget `json:"yearlyBudgets"` // User-declared budgets by year
	TotalCapital  int            `json:"totalCapital"`  // Sum of all yearly budgets
}

// TrackBReinvestment holds internal cash flow reinvestment details
type TrackBReinvestment struct {
	Threshold         int     `json:"threshold"`         // Cash flow threshold to trigger acquisition (e.g., $50K)
	MedianFiredYear   int     `json:"medianFiredYear"`   // Median year Track B fires across MC paths (0 = never)
	FiredProbability  float64 `json:"firedProbability"`  // % of MC paths where Track B fired
	ExpectedAcquisitions int  `json:"expectedAcquisitions"` // Expected # of Track B properties
}
