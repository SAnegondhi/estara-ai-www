package pdf

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// InvestmentPlanPDFRequest is the payload for /api/report/investment-plan.
type InvestmentPlanPDFRequest struct {
	Portfolio      InvestmentPortfolioPDF `json:"portfolio"`
	SearchCriteria SearchCriteriaPDF      `json:"searchCriteria"`
	SearchResults  *SearchResultsPDF      `json:"searchResults,omitempty"`
	Projections    *InvestmentProjections `json:"projections,omitempty"`
	User           *PDFUser               `json:"user,omitempty"`

	// Enhanced portfolio data for comprehensive PDF export
	ScenarioProjections     *ScenarioProjectionsPDF    `json:"scenarioProjections,omitempty"`
	ReinvestmentAnalysis    *ReinvestmentAnalysisPDF    `json:"reinvestmentAnalysis,omitempty"`
	ExistingPortfolio       *ExistingPortfolioPDF       `json:"existingPortfolio,omitempty"`
	CombinedMetrics         *CombinedMetricsPDF         `json:"combinedMetrics,omitempty"`
	DiversificationAnalysis *DiversificationPDF         `json:"diversificationAnalysis,omitempty"`
	RiskAnalysis            *RiskAnalysisPDF            `json:"riskAnalysis,omitempty"`
	Insights                []string                    `json:"insights,omitempty"`
	Criteria                *ScenarioCriteriaPDF        `json:"criteria,omitempty"`
	MarketComparison        []MarketComparisonPDF       `json:"marketComparison,omitempty"`
	AllocationRationale     map[string]string           `json:"allocationRationale,omitempty"`
}

type PDFUser struct {
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Email     string `json:"email,omitempty"`
}

type SearchCriteriaPDF struct {
	Locations     []string `json:"locations,omitempty"`
	Strategy      string   `json:"strategy,omitempty"`
	RiskTolerance string   `json:"riskTolerance,omitempty"`
	Budget        float64  `json:"budget,omitempty"`
	DownPayment   float64  `json:"downPaymentPct,omitempty"`
}

type SearchResultsPDF struct {
	TotalPropertiesFound int `json:"totalPropertiesFound,omitempty"`
}

type InvestmentPortfolioPDF struct {
	SelectedProperties []PortfolioPropertyPDF `json:"selectedProperties"`
	Metrics            PortfolioMetricsPDF    `json:"metrics"`
	Allocations        []AllocationPDF        `json:"allocations,omitempty"`
	Recommendations    []string               `json:"recommendations,omitempty"`
	MarketAnalysis     []MarketQualityPDF     `json:"marketAnalysis,omitempty"`
	MarketFiltering    *MarketFilteringPDF    `json:"marketFiltering,omitempty"`
	CapitalUtilization *CapitalUtilizationPDF `json:"capitalUtilization,omitempty"`
	Concentration      *ConcentrationPDF      `json:"concentration,omitempty"`
}

type PortfolioPropertyPDF struct {
	Address          string  `json:"address"`
	City             string  `json:"city"`
	State            string  `json:"state"`
	ZipCode          string  `json:"zipCode,omitempty"`
	Price            float64 `json:"price,omitempty"`
	EstimatedRent    float64 `json:"estimatedRent,omitempty"`
	InvestmentAmount float64 `json:"investmentAmount,omitempty"`
	CashOnCashReturn float64 `json:"cashOnCashReturn,omitempty"`
	CapRate          float64 `json:"capRate,omitempty"`
	Recommendation   string  `json:"recommendation,omitempty"`
	RiskLevel        string  `json:"riskLevel,omitempty"`
	PropertyType     string  `json:"propertyType,omitempty"`
	Beds             int     `json:"bedrooms,omitempty"`
	Baths            float64 `json:"bathrooms,omitempty"`
	Sqft             int     `json:"sqft,omitempty"`
	DaysOnMarket     int     `json:"daysOnMarket,omitempty"`
}

type PortfolioMetricsPDF struct {
	TotalInvestment float64 `json:"totalInvestment,omitempty"`
	AnnualCashFlow  float64 `json:"annualCashFlow,omitempty"`
	TotalValue      float64 `json:"totalValue,omitempty"`
	TotalDebt       float64 `json:"totalDebt,omitempty"`
	AvgCapRate      float64 `json:"avgCapRate,omitempty"`
	AvgCashOnCash   float64 `json:"avgCashOnCash,omitempty"`
	PortfolioDSCR   float64 `json:"portfolioDscr,omitempty"`
	PropertyCount   int     `json:"propertyCount,omitempty"`
	LocationCount   int     `json:"locationCount,omitempty"`
}

type AllocationPDF struct {
	Location          string  `json:"location"`
	InvestmentAmount  float64 `json:"investmentAmount"`
	AllocationPercent float64 `json:"allocationPercentage"`
}

type MarketQualityPDF struct {
	Location            string   `json:"location"`
	MarketQualityScore  int      `json:"marketQualityScore"`
	MarketQualityRating string   `json:"marketQualityRating"`
	PriceGrowth5Y       *float64 `json:"priceGrowth5Y,omitempty"`
	PriceVolatility     *float64 `json:"priceVolatility,omitempty"`
}

type MarketFilteringPDF struct {
	TotalMarketsSearched int `json:"totalMarketsSearched"`
	MarketsPassed        int `json:"marketsPassed"`
	MarketsFailed        int `json:"marketsFailed"`
	FailedMarkets        []struct {
		Location       string   `json:"location"`
		FailedCriteria []string `json:"failedCriteria"`
	} `json:"failedMarkets,omitempty"`
}

type CapitalUtilizationPDF struct {
	InitialCapital        float64 `json:"initialCapital"`
	InvestedCapital       float64 `json:"investedCapital"`
	RemainingCapital      float64 `json:"remainingCapital"`
	UtilizationPercent    float64 `json:"utilizationPercent"`
	SearchIterations      int     `json:"searchIterations,omitempty"`
	PriceRangeAdjustments int     `json:"priceRangeAdjustments,omitempty"`
}

type ConcentrationPDF struct {
	HHIMarket          float64 `json:"hhiMarket"`
	HHISubmarket       float64 `json:"hhiSubmarket"`
	HHIType            float64 `json:"hhiType"`
	GeoCluster         float64 `json:"geoCluster"`
	ConcentrationIndex float64 `json:"concentrationIndex"`
}

// --- Enhanced PDF types for comprehensive portfolio export ---

type ScenarioCriteriaPDF struct {
	Locations            []string                 `json:"locations"`
	Strategy             string                   `json:"strategy"`
	AvailableCapital     int                      `json:"availableCapital"`
	FinancialAssumptions *FinancialAssumptionsPDF `json:"financialAssumptions,omitempty"`
	ReinvestEnabled      bool                     `json:"reinvestEnabled"`
	ReinvestmentRate     float64                  `json:"reinvestmentRate"`
	ProjectionYears      int                      `json:"projectionYears"`
	YearlyBudgets        []YearlyBudgetPDF        `json:"yearlyBudgets,omitempty"`
	RiskTolerance        string                   `json:"riskTolerance,omitempty"`
	IncludeSuburbs       bool                     `json:"includeSuburbs,omitempty"`
}

type FinancialAssumptionsPDF struct {
	MortgageRate       float64 `json:"mortgageRate"`
	DownPaymentPercent float64 `json:"downPaymentPercent"`
	OperatingExpenses  float64 `json:"operatingExpenses"`
}

type YearlyBudgetPDF struct {
	Year   int `json:"year"`
	Budget int `json:"budget"`
}

type ScenarioProjectionsPDF struct {
	Base        []ExpandedYearPDF         `json:"base"`
	Optimistic  []ExpandedYearPDF         `json:"optimistic,omitempty"`
	Pessimistic []ExpandedYearPDF         `json:"pessimistic,omitempty"`
	Assumptions *ProjectionAssumptionsPDF `json:"assumptions,omitempty"`
}

type ExpandedYearPDF struct {
	Year                   int     `json:"year"`
	PortfolioValue         int     `json:"value"`
	Equity                 int     `json:"equity"`
	LoanBalance            int     `json:"loanBalance"`
	AnnualCashFlow         int     `json:"cashFlow"`
	NetOperatingIncome     int     `json:"noi"`
	GrossRent              int     `json:"grossRent"`
	OperatingExpenses      int     `json:"operatingExpenses"`
	DebtService            int     `json:"debtService"`
	CapExReserve           int     `json:"capExReserve"`
	CumulativeCapExReserve int     `json:"cumulativeCapExReserve"`
	CashOnCash             float64 `json:"cashOnCash"`
	CapRate                float64 `json:"capRate"`
	EquityMultiple         float64 `json:"equityMultiple"`
	InterestExpense        int     `json:"interestExpense"`
	PrincipalPayment       int     `json:"principalPayment"`
	Depreciation           int     `json:"depreciation"`
	TaxableIncome          int     `json:"taxableIncome"`
	IncomeTaxes            int     `json:"incomeTaxes"`
	CashFlowAfterTax       int     `json:"cashFlowAfterTax"`
	CumulativeCashFlow     int     `json:"cumulativeCashFlow"`
	Appreciation           int     `json:"appreciation"`
}

type ProjectionAssumptionsPDF struct {
	MortgageRate       float64 `json:"mortgageRate"`
	MortgageSource     string  `json:"mortgageSource"`
	AppreciationRate   float64 `json:"appreciationRate"`
	AppreciationSource string  `json:"appreciationSource"`
	RentGrowthRate     float64 `json:"rentGrowthRate"`
	RentGrowthSource   string  `json:"rentGrowthSource"`
	ExpenseRatio       float64 `json:"expenseRatio"`
	ExpenseSource      string  `json:"expenseSource"`
	CapExReserveRate   float64 `json:"capExReserveRate"`
	CapExReserveSource string  `json:"capExReserveSource"`
	DownPaymentPct     float64 `json:"downPaymentPct"`
	LoanTermYears      int     `json:"loanTermYears"`
	ConfidenceLevel    string  `json:"confidenceLevel"`
}

type ReinvestmentAnalysisPDF struct {
	Enabled           bool                  `json:"enabled"`
	ReinvestmentRate  float64               `json:"reinvestmentRate"`
	ProjectionYears   int                   `json:"projectionYears"`
	BaseScenario      []YearlyProjectionPDF `json:"baseScenario"`
	ReinvestScenario  []YearlyProjectionPDF `json:"reinvestScenario"`
	CumulativeDiff    *CumulativeDiffPDF    `json:"cumulativeDifference,omitempty"`
	CompoundedReturns *CompoundedReturnsPDF `json:"compoundedReturns,omitempty"`
}

type YearlyProjectionPDF struct {
	Year                   int `json:"year"`
	PortfolioValue         int `json:"portfolioValue"`
	Equity                 int `json:"equity"`
	AnnualCashFlow         int `json:"annualCashFlow"`
	CumulativeCashFlow     int `json:"cumulativeCashFlow"`
	PropertyCount          int `json:"propertyCount"`
	PropertiesAcquired     int `json:"propertiesAcquired"`
	NetOperatingIncome     int `json:"netOperatingIncome"`
	CapExReserve           int `json:"capExReserve"`
	CumulativeCapExReserve int `json:"cumulativeCapExReserve"`
	CashBalance            int `json:"cashBalance"`
}

type CumulativeDiffPDF struct {
	ValueDiff    int `json:"valueDifference"`
	EquityDiff   int `json:"equityDifference"`
	CashFlowDiff int `json:"cashFlowDifference"`
}

type CompoundedReturnsPDF struct {
	TotalReinvested    int `json:"totalReinvested"`
	AdditionalValue    int `json:"additionalValue"`
	AdditionalCashFlow int `json:"additionalAnnualCashFlow"`
}

type ExistingPortfolioPDF struct {
	PropertyCount  int     `json:"propertyCount"`
	TotalValue     int     `json:"totalValue"`
	TotalEquity    int     `json:"totalEquity"`
	TotalDebt      int     `json:"totalDebt"`
	AnnualCashFlow int     `json:"annualCashFlow"`
	CashOnCash     float64 `json:"cashOnCash"`
	CapRate        float64 `json:"capRate"`
	LocationCount  int     `json:"locationCount"`
}

type CombinedMetricsPDF struct {
	BeforeValue      int     `json:"beforeValue"`
	BeforeCashFlow   int     `json:"beforeCashFlow"`
	AfterValue       int     `json:"afterValue"`
	AfterCashFlow    int     `json:"afterCashFlow"`
	ValueIncrease    float64 `json:"valueIncrease"`
	CashFlowIncrease float64 `json:"cashFlowIncrease"`
}

type DiversificationPDF struct {
	Score           int                  `json:"score"`
	Opportunities   []string             `json:"opportunities,omitempty"`
	Correlations    []CorrelationPDF     `json:"correlations,omitempty"`
	DataQualityNote string               `json:"dataQualityNote,omitempty"`
}

type CorrelationPDF struct {
	Market1          string  `json:"market1"`
	Market2          string  `json:"market2"`
	Correlation      float64 `json:"correlation"`
	PriceCorrelation float64 `json:"priceCorrelation"`
	RentCorrelation  float64 `json:"rentCorrelation"`
}

type MarketComparisonPDF struct {
	Location         string   `json:"location"`
	QualityScore     int      `json:"qualityScore"`
	QualityRating    string   `json:"qualityRating"`
	PriceGrowth5Y    *float64 `json:"priceGrowth5Y"`
	RentGrowth5Y     *float64 `json:"rentGrowth5Y"`
	EmploymentGrowth *float64 `json:"employmentGrowth"`
	UnemploymentRate *float64 `json:"unemploymentRate"`
	PopulationGrowth *float64 `json:"populationGrowth"`
	VacancyRate      *float64 `json:"vacancyRate"`
}

type RiskAnalysisPDF struct {
	LowRiskPct    float64 `json:"lowRiskPct"`
	MediumRiskPct float64 `json:"mediumRiskPct"`
	HighRiskPct   float64 `json:"highRiskPct"`
}

type InvestmentProjections struct {
	Base        *ProjectionScenarioPDF `json:"base,omitempty"`
	Optimistic  *ProjectionScenarioPDF `json:"optimistic,omitempty"`
	Pessimistic *ProjectionScenarioPDF `json:"pessimistic,omitempty"`
}

type ProjectionScenarioPDF struct {
	Scenario      string                   `json:"scenario"`
	YearlyMetrics []InvestmentYearlyMetrics `json:"yearlyMetrics"`
	Summary       map[string]interface{}   `json:"summary,omitempty"`
}

// BuildInvestmentPlanPDF renders the unified investment plan PDF.
func BuildInvestmentPlanPDF(ctx context.Context, req InvestmentPlanPDFRequest) ([]byte, error) {
	enhanced := req.ScenarioProjections != nil || req.Criteria != nil
	slog.Info("BuildInvestmentPlanPDF",
		"enhanced", enhanced,
		"hasCriteria", req.Criteria != nil,
		"hasScenarioProjections", req.ScenarioProjections != nil,
		"hasReinvestment", req.ReinvestmentAnalysis != nil,
		"hasExistingPortfolio", req.ExistingPortfolio != nil,
		"propertyCount", len(req.Portfolio.SelectedProperties),
		"insightCount", len(req.Insights),
	)
	if enhanced {
		return buildEnhancedPDF(ctx, req)
	}
	return buildSimplePDF(ctx, req)
}

// buildSimplePDF is the original simplified PDF layout (backwards compatible).
func buildSimplePDF(ctx context.Context, req InvestmentPlanPDFRequest) ([]byte, error) {
	pdf := NewPDF("P", "mm", "A4")
	page := A4Page
	theme := DefaultTheme

	locationLabel := strings.Join(req.SearchCriteria.Locations, ", ")
	if locationLabel == "" {
		locationLabel = "Investment Plan"
	}

	userLabel := ""
	if req.User != nil {
		if req.User.FirstName != "" || req.User.LastName != "" {
			userLabel = strings.TrimSpace(req.User.FirstName + " " + req.User.LastName)
		} else if req.User.Email != "" {
			userLabel = req.User.Email
		}
	}

	AddCoverPage(pdf, page, theme, "Investment Planning Report", "Portfolio & Projections", locationLabel, userLabel)
	AddHeaderFooter(pdf, page, theme, "Investment Plan")

	pdf.AddPage()
	y := page.MarginTop
	y = AddSectionHeading(pdf, page, theme, "Executive Summary", y)

	summary := []MetricItem{
		{Label: "Strategy", Value: titleCase(req.SearchCriteria.Strategy), Highlight: true},
		{Label: "Risk Tolerance", Value: titleCase(req.SearchCriteria.RiskTolerance)},
		{Label: "Properties Selected", Value: fmt.Sprintf("%d", len(req.Portfolio.SelectedProperties))},
		{Label: "Total Investment", Value: formatCurrency(req.Portfolio.Metrics.TotalInvestment)},
		{Label: "Annual Cash Flow", Value: formatCurrency(req.Portfolio.Metrics.AnnualCashFlow)},
		{Label: "Portfolio Value", Value: formatCurrency(req.Portfolio.Metrics.TotalValue)},
	}
	y = AddMetricsGrid(pdf, page, theme, summary, y)

	if req.Portfolio.Metrics.PortfolioDSCR > 0 {
		y = AddParagraph(pdf, page, theme, fmt.Sprintf("Portfolio DSCR: %.2f", req.Portfolio.Metrics.PortfolioDSCR), y)
	}

	y = AddSectionHeading(pdf, page, theme, "Selected Properties", y)
	propCols := []TableColumn{
		{Header: "Address", Width: 62},
		{Header: "Price", Width: 22, Align: "R"},
		{Header: "Rent", Width: 20, Align: "R"},
		{Header: "CoC", Width: 16, Align: "R"},
		{Header: "Cap", Width: 16, Align: "R"},
	}
	propRows := make([][]string, 0, len(req.Portfolio.SelectedProperties))
	for _, prop := range req.Portfolio.SelectedProperties {
		address := strings.TrimSpace(fmt.Sprintf("%s, %s %s", prop.Address, prop.City, prop.State))
		propRows = append(propRows, []string{
			address,
			formatCurrency(prop.Price),
			formatCurrency(prop.EstimatedRent),
			formatPercent(prop.CashOnCashReturn),
			formatPercent(prop.CapRate),
		})
	}
	y = AddTable(pdf, page, theme, propCols, propRows, y)

	if req.Portfolio.MarketFiltering != nil {
		y = AddSectionHeading(pdf, page, theme, "Market Filters", y)
		filterSummary := fmt.Sprintf(
			"Markets searched: %d | Passed: %d | Failed: %d",
			req.Portfolio.MarketFiltering.TotalMarketsSearched,
			req.Portfolio.MarketFiltering.MarketsPassed,
			req.Portfolio.MarketFiltering.MarketsFailed,
		)
		y = AddParagraph(pdf, page, theme, filterSummary, y)
		if len(req.Portfolio.MarketFiltering.FailedMarkets) > 0 {
			items := make([]string, 0, len(req.Portfolio.MarketFiltering.FailedMarkets))
			for _, failed := range req.Portfolio.MarketFiltering.FailedMarkets {
				items = append(items, fmt.Sprintf("%s: %s", failed.Location, strings.Join(failed.FailedCriteria, "; ")))
			}
			y = AddBulletList(pdf, page, theme, items, y)
		}
	}

	if len(req.Portfolio.MarketAnalysis) > 0 {
		y = AddSectionHeading(pdf, page, theme, "Market Quality", y)
		cols := []TableColumn{
			{Header: "Location", Width: 70},
			{Header: "Score", Width: 20, Align: "R"},
			{Header: "Rating", Width: 30},
			{Header: "5Y Growth", Width: 24, Align: "R"},
			{Header: "Volatility", Width: 24, Align: "R"},
		}
		rows := make([][]string, 0, len(req.Portfolio.MarketAnalysis))
		for _, analysis := range req.Portfolio.MarketAnalysis {
			rows = append(rows, []string{
				analysis.Location,
				fmt.Sprintf("%d", analysis.MarketQualityScore),
				titleCase(analysis.MarketQualityRating),
				formatPercentPtr(analysis.PriceGrowth5Y),
				formatPercentPtr(analysis.PriceVolatility),
			})
		}
		y = AddTable(pdf, page, theme, cols, rows, y)
	}

	if req.Portfolio.Concentration != nil {
		y = AddSectionHeading(pdf, page, theme, "Diversification & Concentration", y)
		metrics := []MetricItem{
			{Label: "HHI Market", Value: formatDecimal(req.Portfolio.Concentration.HHIMarket)},
			{Label: "HHI Submarket", Value: formatDecimal(req.Portfolio.Concentration.HHISubmarket)},
			{Label: "HHI Type", Value: formatDecimal(req.Portfolio.Concentration.HHIType)},
			{Label: "Geo Cluster", Value: formatDecimal(req.Portfolio.Concentration.GeoCluster)},
			{Label: "Concentration Index", Value: formatDecimal(req.Portfolio.Concentration.ConcentrationIndex), Highlight: true},
		}
		y = AddMetricsGrid(pdf, page, theme, metrics, y)
	}

	if req.Portfolio.CapitalUtilization != nil {
		y = AddSectionHeading(pdf, page, theme, "Capital Utilization", y)
		metrics := []MetricItem{
			{Label: "Initial Capital", Value: formatCurrency(req.Portfolio.CapitalUtilization.InitialCapital)},
			{Label: "Invested Capital", Value: formatCurrency(req.Portfolio.CapitalUtilization.InvestedCapital), Highlight: true},
			{Label: "Remaining Capital", Value: formatCurrency(req.Portfolio.CapitalUtilization.RemainingCapital)},
			{Label: "Utilization", Value: formatPercent(req.Portfolio.CapitalUtilization.UtilizationPercent)},
		}
		y = AddMetricsGrid(pdf, page, theme, metrics, y)
	}

	if req.Projections != nil {
		scenarios := projectionScenarios(req.Projections)
		if len(scenarios) > 0 {
			chartClient := NewQuickChartClient()
			charts, err := GenerateInvestmentCharts(ctx, chartClient, scenarios)
			if err == nil && charts != nil {
				pdf.AddPage()
				y = page.MarginTop
				y = AddSectionHeading(pdf, page, theme, "Projection Charts", y)
				chartWidth := (page.Width - page.MarginLeft - page.MarginRight - 10) / 2
				chartHeight := 55.0

				_ = AddImageFromBase64(pdf, "equity_growth", charts.EquityGrowth, page.MarginLeft, y+4, chartWidth, chartHeight)
				_ = AddImageFromBase64(pdf, "cash_flow", charts.CashFlow, page.MarginLeft+chartWidth+10, y+4, chartWidth, chartHeight)
				y += chartHeight + 12
				_ = AddImageFromBase64(pdf, "portfolio_value", charts.PortfolioValue, page.MarginLeft, y, chartWidth, chartHeight)
				_ = AddImageFromBase64(pdf, "return_metrics", charts.ReturnMetrics, page.MarginLeft+chartWidth+10, y, chartWidth, chartHeight)
			}
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sanitizeForPDF replaces common Unicode characters with CP1252-safe equivalents
// and strips characters that cannot be represented in the PDF's built-in fonts.
func sanitizeForPDF(s string) string {
	s = strings.ReplaceAll(s, "\u2014", "-")  // em-dash
	s = strings.ReplaceAll(s, "\u2013", "-")  // en-dash
	s = strings.ReplaceAll(s, "\u2018", "'")  // left single quote
	s = strings.ReplaceAll(s, "\u2019", "'")  // right single quote
	s = strings.ReplaceAll(s, "\u201c", "\"") // left double quote
	s = strings.ReplaceAll(s, "\u201d", "\"") // right double quote
	var buf strings.Builder
	for _, r := range s {
		if r <= 0xFF {
			buf.WriteRune(r)
		}
	}
	return strings.TrimSpace(buf.String())
}

// buildEnhancedPDF renders the comprehensive portfolio PDF with all sections.
// Content flows continuously; page breaks occur only when tables/charts won't fit.
func buildEnhancedPDF(ctx context.Context, req InvestmentPlanPDFRequest) ([]byte, error) {
	pdf := NewPDF("P", "mm", "A4")
	page := A4Page
	theme := DefaultTheme

	// Sanitize user-provided text
	for i, insight := range req.Insights {
		req.Insights[i] = sanitizeForPDF(insight)
	}

	locationLabel := ""
	if req.Criteria != nil && len(req.Criteria.Locations) > 0 {
		locationLabel = strings.Join(req.Criteria.Locations, ", ")
	} else if len(req.SearchCriteria.Locations) > 0 {
		locationLabel = strings.Join(req.SearchCriteria.Locations, ", ")
	} else {
		locationLabel = "Scenario Portfolio"
	}

	userLabel := ""
	if req.User != nil {
		if req.User.FirstName != "" || req.User.LastName != "" {
			userLabel = strings.TrimSpace(req.User.FirstName + " " + req.User.LastName)
		} else if req.User.Email != "" {
			userLabel = req.User.Email
		}
	}

	// 1. Cover Page
	AddCoverPage(pdf, page, theme, "Investment Scenario Report", "Comprehensive Analysis", locationLabel, userLabel)
	AddHeaderFooter(pdf, page, theme, "Scenario Portfolio")

	// 2. Scenario Parameters — all user-set criteria
	pdf.AddPage()
	y := page.MarginTop
	if req.Criteria != nil {
		y = AddSectionHeading(pdf, page, theme, "Scenario Parameters", y)
		params := []MetricItem{
			{Label: "Strategy", Value: titleCase(req.Criteria.Strategy), Highlight: true},
			{Label: "Available Capital", Value: formatCurrencyInt(req.Criteria.AvailableCapital)},
			{Label: "Properties Selected", Value: fmt.Sprintf("%d", len(req.Portfolio.SelectedProperties))},
			{Label: "Locations", Value: strings.Join(req.Criteria.Locations, ", ")},
		}
		if req.Criteria.RiskTolerance != "" {
			params = append(params, MetricItem{Label: "Risk Tolerance", Value: titleCase(req.Criteria.RiskTolerance)})
		}
		if req.Criteria.IncludeSuburbs {
			params = append(params, MetricItem{Label: "Include Suburbs", Value: "Yes"})
		}
		if req.Criteria.FinancialAssumptions != nil {
			fa := req.Criteria.FinancialAssumptions
			params = append(params,
				MetricItem{Label: "Mortgage Rate", Value: formatPercent(fa.MortgageRate)},
				MetricItem{Label: "Down Payment", Value: formatPercentSmart(fa.DownPaymentPercent)},
			)
			if fa.OperatingExpenses > 0 {
				params = append(params, MetricItem{Label: "Operating Expenses", Value: formatPercentSmart(fa.OperatingExpenses)})
			}
		}
		if req.Criteria.ReinvestEnabled {
			params = append(params,
				MetricItem{Label: "Reinvestment", Value: fmt.Sprintf("%.0f%% of cash flow", req.Criteria.ReinvestmentRate)},
				MetricItem{Label: "Projection Years", Value: fmt.Sprintf("%d years", req.Criteria.ProjectionYears)},
			)
		}
		y = AddMetricsGrid(pdf, page, theme, params, y)

		// Yearly budgets
		if len(req.Criteria.YearlyBudgets) > 0 {
			y += 2
			pdf.SetFont("Helvetica", "B", 9)
			pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
			pdf.Text(page.MarginLeft, y, "Yearly Budgets:")
			y += 5
			budgetCols := []TableColumn{
				{Header: "Year", Width: 30},
				{Header: "Budget", Width: 50, Align: "R"},
			}
			budgetRows := make([][]string, 0, len(req.Criteria.YearlyBudgets))
			for _, yb := range req.Criteria.YearlyBudgets {
				budgetRows = append(budgetRows, []string{
					fmt.Sprintf("Year %d", yb.Year),
					formatCurrencyInt(yb.Budget),
				})
			}
			y = AddTable(pdf, page, theme, budgetCols, budgetRows, y)
		}
	}

	// 3. Existing Portfolio
	if req.ExistingPortfolio != nil && req.ExistingPortfolio.PropertyCount > 0 {
		y = AddSectionHeading(pdf, page, theme, "Your Existing Portfolio", y)
		metrics := []MetricItem{
			{Label: "Properties", Value: fmt.Sprintf("%d", req.ExistingPortfolio.PropertyCount)},
			{Label: "Total Value", Value: formatCurrencyInt(req.ExistingPortfolio.TotalValue)},
			{Label: "Total Equity", Value: formatCurrencyInt(req.ExistingPortfolio.TotalEquity), Highlight: true},
			{Label: "Total Debt", Value: formatCurrencyInt(req.ExistingPortfolio.TotalDebt)},
			{Label: "Annual Cash Flow", Value: formatCurrencyInt(req.ExistingPortfolio.AnnualCashFlow)},
			{Label: "Cash-on-Cash", Value: formatPercent(req.ExistingPortfolio.CashOnCash)},
			{Label: "Cap Rate", Value: formatPercent(req.ExistingPortfolio.CapRate)},
			{Label: "Markets", Value: fmt.Sprintf("%d", req.ExistingPortfolio.LocationCount)},
		}
		y = AddMetricsGrid(pdf, page, theme, metrics, y)
	}

	// 4. Portfolio Expansion Impact
	if req.CombinedMetrics != nil {
		y = AddComparisonTable(pdf, page, theme, "Portfolio Expansion Impact",
			[]string{"Metric", "Before", "After", "Change"},
			[][]string{
				{"Portfolio Value", formatCurrencyInt(req.CombinedMetrics.BeforeValue), formatCurrencyInt(req.CombinedMetrics.AfterValue), fmt.Sprintf("+%.1f%%", req.CombinedMetrics.ValueIncrease)},
				{"Annual Cash Flow", formatCurrencyInt(req.CombinedMetrics.BeforeCashFlow), formatCurrencyInt(req.CombinedMetrics.AfterCashFlow), fmt.Sprintf("+%.1f%%", req.CombinedMetrics.CashFlowIncrease)},
			}, y)
	}

	// 5. Portfolio Overview (Year 1 metrics)
	y = AddSectionHeading(pdf, page, theme, "Portfolio Overview - Year 1", y)
	overview := []MetricItem{
		{Label: "Total Investment", Value: formatCurrency(req.Portfolio.Metrics.TotalInvestment), Highlight: true},
		{Label: "Annual Cash Flow", Value: formatCurrency(req.Portfolio.Metrics.AnnualCashFlow)},
		{Label: "Cash-on-Cash", Value: formatPercent(req.Portfolio.Metrics.AvgCashOnCash)},
		{Label: "Avg Cap Rate", Value: formatPercent(req.Portfolio.Metrics.AvgCapRate)},
		{Label: "Properties", Value: fmt.Sprintf("%d", req.Portfolio.Metrics.PropertyCount)},
		{Label: "Locations", Value: fmt.Sprintf("%d", req.Portfolio.Metrics.LocationCount)},
	}
	y = AddMetricsGrid(pdf, page, theme, overview, y)

	// 6. Growth Projection Table (base scenario)
	if req.ScenarioProjections != nil && len(req.ScenarioProjections.Base) > 0 {
		y = AddSectionHeading(pdf, page, theme, fmt.Sprintf("%d-Year Growth Projection (Base)", len(req.ScenarioProjections.Base)), y)
		projHeaders := []string{"Year", "Value", "NOI", "CoC", "Mortgage", "CapEx Rsv"}
		projRows := make([][]string, 0, len(req.ScenarioProjections.Base))
		for _, yr := range req.ScenarioProjections.Base {
			projRows = append(projRows, []string{
				fmt.Sprintf("Year %d", yr.Year),
				formatCurrencyInt(yr.PortfolioValue),
				formatCurrencyInt(yr.NetOperatingIncome),
				formatPercent(yr.CashOnCash),
				formatCurrencyInt(yr.LoanBalance),
				formatCurrencyInt(yr.CumulativeCapExReserve),
			})
		}
		y = AddComparisonTable(pdf, page, theme, "", projHeaders, projRows, y)
	}

	// 6b. Detailed Projection Table (income, expenses, returns)
	if req.ScenarioProjections != nil && len(req.ScenarioProjections.Base) > 0 {
		y = AddSectionHeading(pdf, page, theme, "Detailed Financial Projections", y)
		detailHeaders := []string{"Year", "Gross Rent", "OpEx", "Debt Svc", "Cash Flow", "Equity", "Eq Multiple"}
		detailRows := make([][]string, 0, len(req.ScenarioProjections.Base))
		for _, yr := range req.ScenarioProjections.Base {
			detailRows = append(detailRows, []string{
				fmt.Sprintf("Year %d", yr.Year),
				formatCurrencyInt(yr.GrossRent),
				formatCurrencyInt(yr.OperatingExpenses),
				formatCurrencyInt(yr.DebtService),
				formatCurrencyInt(yr.AnnualCashFlow),
				formatCurrencyInt(yr.Equity),
				fmt.Sprintf("%.2fx", yr.EquityMultiple),
			})
		}
		y = AddComparisonTable(pdf, page, theme, "", detailHeaders, detailRows, y)
	}

	// 7. Reinvestment Analysis
	if req.ReinvestmentAnalysis != nil && req.ReinvestmentAnalysis.Enabled {
		y = AddSectionHeading(pdf, page, theme, "Cash Flow Reinvestment Analysis", y)

		if req.ReinvestmentAnalysis.CompoundedReturns != nil {
			cr := req.ReinvestmentAnalysis.CompoundedReturns
			reinvestSummary := []MetricItem{
				{Label: "Total Reinvested", Value: formatCurrencyInt(cr.TotalReinvested)},
				{Label: "Additional Value", Value: formatCurrencyInt(cr.AdditionalValue), Highlight: true},
				{Label: "Additional Cash Flow/yr", Value: formatCurrencyInt(cr.AdditionalCashFlow)},
				{Label: "Reinvestment Rate", Value: fmt.Sprintf("%.0f%%", req.ReinvestmentAnalysis.ReinvestmentRate)},
			}
			y = AddMetricsGrid(pdf, page, theme, reinvestSummary, y)
		}

		// Comparison table
		if len(req.ReinvestmentAnalysis.BaseScenario) > 0 && len(req.ReinvestmentAnalysis.ReinvestScenario) > 0 {
			compHeaders := []string{"Year", "Keep Cash", "Reinvest", "Benefit"}
			compRows := make([][]string, 0, len(req.ReinvestmentAnalysis.ReinvestScenario))
			for i, reinYr := range req.ReinvestmentAnalysis.ReinvestScenario {
				baseVal := 0
				if i < len(req.ReinvestmentAnalysis.BaseScenario) {
					baseVal = req.ReinvestmentAnalysis.BaseScenario[i].PortfolioValue
				}
				diff := reinYr.PortfolioValue - baseVal
				compRows = append(compRows, []string{
					fmt.Sprintf("Year %d", reinYr.Year),
					formatCurrencyInt(baseVal),
					formatCurrencyInt(reinYr.PortfolioValue),
					formatCurrencyInt(diff),
				})
			}
			y = AddComparisonTable(pdf, page, theme, "", compHeaders, compRows, y)

			// Reinvestment chart — ensure it fits on one page
			chartClient := NewQuickChartClient()
			reinvestChart, err := GenerateReinvestmentChart(ctx, chartClient,
				req.ReinvestmentAnalysis.BaseScenario,
				req.ReinvestmentAnalysis.ReinvestScenario)
			if err == nil && reinvestChart != "" {
				chartHeight := 50.0
				titleSpace := 10.0
				y, _ = EnsureSpace(pdf, page, y, chartHeight+titleSpace+4)
				// Chart title
				pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
				pdf.SetFont("Helvetica", "B", 10)
				pdf.Text(page.MarginLeft, y+3, "Reinvestment Impact Over Time")
				pdf.SetFont("Helvetica", "", 8)
				pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
				pdf.Text(page.MarginLeft, y+8, "Portfolio value: Keep Cash vs Reinvest scenario")
				y += titleSpace
				chartWidth := page.Width - page.MarginLeft - page.MarginRight
				_ = AddImageFromBase64(pdf, "reinvest_chart", reinvestChart, page.MarginLeft, y+2, chartWidth, chartHeight)
				y += chartHeight + 6
			}
		}
	}

	// 8. Investment Allocation
	if len(req.Portfolio.Allocations) > 0 {
		y = AddSectionHeading(pdf, page, theme, "Investment Allocation", y)
		allocCols := []TableColumn{
			{Header: "Location", Width: 60},
			{Header: "Amount", Width: 45, Align: "R"},
			{Header: "Allocation", Width: 35, Align: "R"},
		}
		allocRows := make([][]string, 0, len(req.Portfolio.Allocations))
		for _, alloc := range req.Portfolio.Allocations {
			allocRows = append(allocRows, []string{
				alloc.Location,
				formatCurrency(alloc.InvestmentAmount),
				formatPercent(alloc.AllocationPercent),
			})
		}
		y = AddTable(pdf, page, theme, allocCols, allocRows, y)
	}

	// 8b. Market Comparison (rows = markets, columns = metrics; omit sparse columns)
	if len(req.MarketComparison) >= 2 {
		y = AddSectionHeading(pdf, page, theme, "Market Comparison", y)

		fmtOpt := func(v *float64) string {
			if v == nil {
				return "-"
			}
			return fmt.Sprintf("%.1f%%", *v)
		}

		// Define all possible metric columns with a data-presence check
		type metricCol struct {
			header string
			width  float64
			has    func(MarketComparisonPDF) bool
			value  func(MarketComparisonPDF) string
		}
		allMetrics := []metricCol{
			{"Quality", 18, func(MarketComparisonPDF) bool { return true }, func(m MarketComparisonPDF) string { return fmt.Sprintf("%d/100", m.QualityScore) }},
			{"5Y Price", 18, func(m MarketComparisonPDF) bool { return m.PriceGrowth5Y != nil }, func(m MarketComparisonPDF) string { return fmtOpt(m.PriceGrowth5Y) }},
			{"5Y Rent", 18, func(m MarketComparisonPDF) bool { return m.RentGrowth5Y != nil }, func(m MarketComparisonPDF) string { return fmtOpt(m.RentGrowth5Y) }},
			{"Emp.", 18, func(m MarketComparisonPDF) bool { return m.EmploymentGrowth != nil }, func(m MarketComparisonPDF) string { return fmtOpt(m.EmploymentGrowth) }},
			{"Unemp.", 18, func(m MarketComparisonPDF) bool { return m.UnemploymentRate != nil }, func(m MarketComparisonPDF) string { return fmtOpt(m.UnemploymentRate) }},
			{"Pop.", 18, func(m MarketComparisonPDF) bool { return m.PopulationGrowth != nil }, func(m MarketComparisonPDF) string { return fmtOpt(m.PopulationGrowth) }},
			{"Vacancy", 18, func(m MarketComparisonPDF) bool { return m.VacancyRate != nil }, func(m MarketComparisonPDF) string { return fmtOpt(m.VacancyRate) }},
		}

		// Keep columns where at least 10% of markets have data
		n := len(req.MarketComparison)
		threshold := (n + 9) / 10 // ceil(n * 0.1), minimum 1
		var visibleMetrics []metricCol
		for _, mc := range allMetrics {
			count := 0
			for _, m := range req.MarketComparison {
				if mc.has(m) {
					count++
				}
			}
			if count >= threshold {
				visibleMetrics = append(visibleMetrics, mc)
			}
		}

		compCols := []TableColumn{{Header: "Market", Width: 30}}
		for _, mc := range visibleMetrics {
			compCols = append(compCols, TableColumn{Header: mc.header, Width: mc.width, Align: "R"})
		}

		compRows := make([][]string, 0, n)
		for _, m := range req.MarketComparison {
			city := m.Location
			if idx := strings.Index(city, ","); idx > 0 {
				city = city[:idx]
			}
			row := []string{city}
			for _, mc := range visibleMetrics {
				row = append(row, mc.value(m))
			}
			compRows = append(compRows, row)
		}
		y = AddTable(pdf, page, theme, compCols, compRows, y)
	}

	// 8c. Correlation Matrix
	if req.DiversificationAnalysis != nil && len(req.DiversificationAnalysis.Correlations) > 0 {
		y = AddSectionHeading(pdf, page, theme, "Market Correlation Matrix", y)
		y = AddParagraph(pdf, page, theme, "Lower correlation = better diversification. Below 30% is strong; above 50% is weak.", y)

		corrCols := []TableColumn{
			{Header: "Markets", Width: 55},
			{Header: "Price", Width: 25, Align: "R"},
			{Header: "Rent", Width: 25, Align: "R"},
			{Header: "Overall", Width: 25, Align: "R"},
			{Header: "Benefit", Width: 30, Align: "R"},
		}
		corrRows := make([][]string, 0, len(req.DiversificationAnalysis.Correlations))
		for _, c := range req.DiversificationAnalysis.Correlations {
			m1 := c.Market1
			m2 := c.Market2
			if idx := strings.Index(m1, ","); idx > 0 {
				m1 = m1[:idx]
			}
			if idx := strings.Index(m2, ","); idx > 0 {
				m2 = m2[:idx]
			}
			absCorr := c.Correlation
			if absCorr < 0 {
				absCorr = -absCorr
			}
			benefit := "High"
			if absCorr < 0.3 {
				benefit = "Strong"
			} else if absCorr < 0.5 {
				benefit = "Moderate"
			} else {
				benefit = "Low"
			}
			corrRows = append(corrRows, []string{
				fmt.Sprintf("%s ↔ %s", m1, m2),
				fmt.Sprintf("%.0f%%", c.PriceCorrelation*100),
				fmt.Sprintf("%.0f%%", c.RentCorrelation*100),
				fmt.Sprintf("%.0f%%", c.Correlation*100),
				benefit,
			})
		}
		y = AddTable(pdf, page, theme, corrCols, corrRows, y)

		if req.DiversificationAnalysis.DataQualityNote != "" {
			y = AddParagraph(pdf, page, theme, "Note: "+req.DiversificationAnalysis.DataQualityNote, y)
		}
	}

	// 8d. Allocation Rationale
	if len(req.AllocationRationale) > 0 {
		y = AddSectionHeading(pdf, page, theme, "Allocation Rationale", y)
		for loc, rationale := range req.AllocationRationale {
			y = AddParagraph(pdf, page, theme, fmt.Sprintf("%s: %s", loc, rationale), y)
		}
	}

	// 9. Diversification & Risk
	hasDiversification := req.DiversificationAnalysis != nil
	hasRisk := req.RiskAnalysis != nil
	if hasDiversification || hasRisk {
		y = AddSectionHeading(pdf, page, theme, "Diversification & Risk", y)

		if hasDiversification {
			y = AddParagraph(pdf, page, theme, fmt.Sprintf("Diversification Score: %d/100", req.DiversificationAnalysis.Score), y)
			if len(req.DiversificationAnalysis.Opportunities) > 0 {
				y = AddBulletList(pdf, page, theme, req.DiversificationAnalysis.Opportunities, y)
			}
		}
		if hasRisk {
			riskItems := []MetricItem{
				{Label: "Low Risk", Value: formatPercent(req.RiskAnalysis.LowRiskPct)},
				{Label: "Medium Risk", Value: formatPercent(req.RiskAnalysis.MediumRiskPct)},
				{Label: "High Risk", Value: formatPercent(req.RiskAnalysis.HighRiskPct)},
			}
			y = AddMetricsGrid(pdf, page, theme, riskItems, y)
		}
	}

	// 10. Selected Properties
	y = AddSectionHeading(pdf, page, theme, "Selected Properties", y)
	propCols := []TableColumn{
		{Header: "Address", Width: 70},
		{Header: "Price", Width: 28, Align: "R"},
		{Header: "Rent", Width: 24, Align: "R"},
		{Header: "Cap Rate", Width: 22, Align: "R"},
		{Header: "CoC", Width: 22, Align: "R"},
	}
	propRows := make([][]string, 0, len(req.Portfolio.SelectedProperties))
	for _, prop := range req.Portfolio.SelectedProperties {
		address := prop.Address
		if prop.City != "" {
			address = fmt.Sprintf("%s, %s", prop.Address, prop.City)
		}
		propRows = append(propRows, []string{
			address,
			formatCurrency(prop.Price),
			formatCurrency(prop.EstimatedRent),
			formatPercent(prop.CapRate),
			formatPercent(prop.CashOnCashReturn),
		})
	}
	y = AddTable(pdf, page, theme, propCols, propRows, y)

	// 11. Portfolio Insights
	if len(req.Insights) > 0 {
		y = AddSectionHeading(pdf, page, theme, "Portfolio Insights", y)
		y = AddBulletList(pdf, page, theme, req.Insights, y)
	}

	// 12. Projection Charts — ensure charts don't split across pages
	if req.ScenarioProjections != nil && len(req.ScenarioProjections.Base) > 0 {
		scenarios := expandedToChartScenarios(req.ScenarioProjections)
		if len(scenarios) > 0 {
			chartClient := NewQuickChartClient()
			charts, err := GenerateInvestmentCharts(ctx, chartClient, scenarios)
			if err == nil && charts != nil {
				chartWidth := (page.Width - page.MarginLeft - page.MarginRight - 10) / 2
				chartHeight := 55.0
				totalChartsHeight := chartHeight*2 + 28 // 2 rows + title + gap

				y, _ = EnsureSpace(pdf, page, y, totalChartsHeight)
				y = AddSectionHeading(pdf, page, theme, "Scenario Projection Charts", y)

				// Row 1: Equity Growth + Cash Flow
				pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
				pdf.SetFont("Helvetica", "B", 9)
				pdf.Text(page.MarginLeft, y, "Equity Growth")
				pdf.Text(page.MarginLeft+chartWidth+10, y, "Annual Cash Flow")
				pdf.SetFont("Helvetica", "", 7)
				pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
				pdf.Text(page.MarginLeft, y+4, "Total equity over projection period")
				pdf.Text(page.MarginLeft+chartWidth+10, y+4, "Net cash flow after all expenses")
				y += 6

				_ = AddImageFromBase64(pdf, "eq_equity", charts.EquityGrowth, page.MarginLeft, y, chartWidth, chartHeight)
				_ = AddImageFromBase64(pdf, "eq_cashflow", charts.CashFlow, page.MarginLeft+chartWidth+10, y, chartWidth, chartHeight)
				y += chartHeight + 8

				// Row 2: Portfolio Value + Return Metrics
				pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
				pdf.SetFont("Helvetica", "B", 9)
				pdf.Text(page.MarginLeft, y, "Portfolio Value")
				pdf.Text(page.MarginLeft+chartWidth+10, y, "Return Metrics")
				pdf.SetFont("Helvetica", "", 7)
				pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
				pdf.Text(page.MarginLeft, y+4, "Total property market value")
				pdf.Text(page.MarginLeft+chartWidth+10, y+4, "Cash-on-Cash and Cap Rate trends")
				y += 6

				_ = AddImageFromBase64(pdf, "eq_value", charts.PortfolioValue, page.MarginLeft, y, chartWidth, chartHeight)
				_ = AddImageFromBase64(pdf, "eq_returns", charts.ReturnMetrics, page.MarginLeft+chartWidth+10, y, chartWidth, chartHeight)
				y += chartHeight + 6
			}
		}
	}

	// 13. Projection Summary (base scenario final metrics)
	if req.ScenarioProjections != nil && len(req.ScenarioProjections.Base) > 0 {
		baseSummary := computeScenarioSummary(req.ScenarioProjections.Base)
		initialInvestment := req.Portfolio.Metrics.TotalInvestment
		totalROI := 0.0
		annualizedROI := 0.0
		nYears := len(req.ScenarioProjections.Base)
		if initialInvestment > 0 {
			totalROI = float64(baseSummary.FinalEquity+baseSummary.TotalCashFlow-int(initialInvestment)) / initialInvestment * 100
			if nYears > 0 {
				annualizedROI = totalROI / float64(nYears)
			}
		}
		y = AddSectionHeading(pdf, page, theme, "Projection Summary (Base Scenario)", y)
		summaryMetrics := []MetricItem{
			{Label: "Final Portfolio Value", Value: formatCurrencyInt(baseSummary.FinalValue), Highlight: true},
			{Label: "Final Equity", Value: formatCurrencyInt(baseSummary.FinalEquity)},
			{Label: "Total Cash Flow", Value: formatCurrencyInt(baseSummary.TotalCashFlow)},
			{Label: "Total ROI", Value: fmt.Sprintf("%.1f%%", totalROI)},
			{Label: "Annualized ROI", Value: fmt.Sprintf("%.1f%%/yr", annualizedROI)},
			{Label: "Avg Cash-on-Cash", Value: formatPercent(baseSummary.AvgCoC)},
		}
		y = AddMetricsGrid(pdf, page, theme, summaryMetrics, y)
	}

	// 14. Scenario Comparison
	if req.ScenarioProjections != nil && len(req.ScenarioProjections.Base) > 0 &&
		(len(req.ScenarioProjections.Optimistic) > 0 || len(req.ScenarioProjections.Pessimistic) > 0) {

		y = AddSectionHeading(pdf, page, theme, "Scenario Comparison", y)

		baseSummary := computeScenarioSummary(req.ScenarioProjections.Base)
		optSummary := computeScenarioSummary(req.ScenarioProjections.Optimistic)
		pessSummary := computeScenarioSummary(req.ScenarioProjections.Pessimistic)

		compHeaders := []string{"Metric", "Pessimistic", "Base", "Optimistic", "Range"}
		compRows := [][]string{
			{"Final Value", formatCurrencyInt(pessSummary.FinalValue), formatCurrencyInt(baseSummary.FinalValue), formatCurrencyInt(optSummary.FinalValue), formatCurrencyInt(optSummary.FinalValue - pessSummary.FinalValue)},
			{"Final Equity", formatCurrencyInt(pessSummary.FinalEquity), formatCurrencyInt(baseSummary.FinalEquity), formatCurrencyInt(optSummary.FinalEquity), formatCurrencyInt(optSummary.FinalEquity - pessSummary.FinalEquity)},
			{"Total Cash Flow", formatCurrencyInt(pessSummary.TotalCashFlow), formatCurrencyInt(baseSummary.TotalCashFlow), formatCurrencyInt(optSummary.TotalCashFlow), formatCurrencyInt(optSummary.TotalCashFlow - pessSummary.TotalCashFlow)},
			{"Avg CoC", formatPercent(pessSummary.AvgCoC), formatPercent(baseSummary.AvgCoC), formatPercent(optSummary.AvgCoC), formatPercent(optSummary.AvgCoC - pessSummary.AvgCoC)},
			{"Appreciation", formatCurrencyInt(pessSummary.Appreciation), formatCurrencyInt(baseSummary.Appreciation), formatCurrencyInt(optSummary.Appreciation), formatCurrencyInt(optSummary.Appreciation - pessSummary.Appreciation)},
			{"Equity Growth", formatCurrencyInt(pessSummary.EquityGrowth), formatCurrencyInt(baseSummary.EquityGrowth), formatCurrencyInt(optSummary.EquityGrowth), formatCurrencyInt(optSummary.EquityGrowth - pessSummary.EquityGrowth)},
		}
		y = AddComparisonTable(pdf, page, theme, "", compHeaders, compRows, y)

		// Risk Assessment
		if len(req.ScenarioProjections.Optimistic) > 0 && len(req.ScenarioProjections.Pessimistic) > 0 {
			downside := baseSummary.FinalEquity - pessSummary.FinalEquity
			upside := optSummary.FinalEquity - baseSummary.FinalEquity
			y = AddSectionHeading(pdf, page, theme, "Risk Assessment", y)
			riskMetrics := []MetricItem{
				{Label: "Downside Risk (Equity)", Value: fmt.Sprintf("-%s", formatCurrencyInt(downside))},
				{Label: "Upside Potential (Equity)", Value: fmt.Sprintf("+%s", formatCurrencyInt(upside)), Highlight: true},
			}
			y = AddMetricsGrid(pdf, page, theme, riskMetrics, y)
		}
	}

	// 15. Assumptions
	if req.ScenarioProjections != nil && req.ScenarioProjections.Assumptions != nil {
		y = AddSectionHeading(pdf, page, theme, "Projection Assumptions", y)
		a := req.ScenarioProjections.Assumptions
		assumptions := []MetricItem{
			{Label: "Mortgage Rate", Value: fmt.Sprintf("%.2f%% (%s)", a.MortgageRate, sanitizeForPDF(a.MortgageSource))},
			{Label: "Down Payment", Value: formatPercentSmart(a.DownPaymentPct)},
			{Label: "Loan Term", Value: fmt.Sprintf("%d years", a.LoanTermYears)},
			{Label: "Appreciation", Value: fmt.Sprintf("%.2f%% (%s)", a.AppreciationRate, sanitizeForPDF(a.AppreciationSource))},
			{Label: "Rent Growth", Value: fmt.Sprintf("%.2f%% (%s)", a.RentGrowthRate, sanitizeForPDF(a.RentGrowthSource))},
			{Label: "Expense Ratio", Value: fmt.Sprintf("%.1f%% (%s)", smartPercent(a.ExpenseRatio), sanitizeForPDF(a.ExpenseSource))},
		}
		if a.CapExReserveRate > 0 {
			assumptions = append(assumptions, MetricItem{Label: "CapEx Reserve", Value: fmt.Sprintf("%.1f%% (%s)", smartPercent(a.CapExReserveRate), sanitizeForPDF(a.CapExReserveSource))})
		}
		if a.ConfidenceLevel != "" {
			assumptions = append(assumptions, MetricItem{Label: "Data Confidence", Value: a.ConfidenceLevel})
		}
		y = AddMetricsGrid(pdf, page, theme, assumptions, y)
	}

	// 16. Compliance Footer
	y, _ = EnsureSpace(pdf, page, y, 30)
	pdf.SetDrawColor(theme.Warning.R, theme.Warning.G, theme.Warning.B)
	pdf.SetLineWidth(0.3)
	pdf.Line(page.MarginLeft, y, page.Width-page.MarginRight, y)
	y += 4
	pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
	pdf.SetFont("Helvetica", "B", 7)
	pdf.Text(page.MarginLeft, y, "IMPORTANT DISCLAIMER")
	y += 4
	pdf.SetFont("Helvetica", "", 7)
	disclaimer := "This report is for informational purposes only. It is NOT investment advice, NOT a recommendation, and NOT an offer to buy or sell any property. All projections are estimates based on historical data and assumptions that may not reflect future performance. All investment decisions remain solely your responsibility. Estara AI does not guarantee any results."
	lines := pdf.SplitLines([]byte(disclaimer), page.Width-page.MarginLeft-page.MarginRight)
	for _, line := range lines {
		pdf.Text(page.MarginLeft, y, string(line))
		y += 3.2
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// expandedToChartScenarios converts ScenarioProjectionsPDF to chart-compatible scenarios.
func expandedToChartScenarios(sp *ScenarioProjectionsPDF) []InvestmentProjectionScenario {
	if sp == nil {
		return nil
	}

	convert := func(years []ExpandedYearPDF) []InvestmentYearlyMetrics {
		metrics := make([]InvestmentYearlyMetrics, 0, len(years))
		for _, yr := range years {
			metrics = append(metrics, InvestmentYearlyMetrics{
				Year:                   yr.Year,
				TotalPropertyValue:     float64(yr.PortfolioValue),
				TotalLoanBalance:       float64(yr.LoanBalance),
				TotalEquity:            float64(yr.Equity),
				CashFlowAfterTax:       float64(yr.CashFlowAfterTax),
				CashOnCashReturn:       yr.CashOnCash,
				CapRate:                yr.CapRate,
				CapExReserve:           float64(yr.CapExReserve),
				CumulativeCapExReserve: float64(yr.CumulativeCapExReserve),
			})
		}
		return metrics
	}

	var scenarios []InvestmentProjectionScenario
	if len(sp.Pessimistic) > 0 {
		scenarios = append(scenarios, InvestmentProjectionScenario{Scenario: "pessimistic", YearlyMetrics: convert(sp.Pessimistic)})
	}
	if len(sp.Base) > 0 {
		scenarios = append(scenarios, InvestmentProjectionScenario{Scenario: "base", YearlyMetrics: convert(sp.Base)})
	}
	if len(sp.Optimistic) > 0 {
		scenarios = append(scenarios, InvestmentProjectionScenario{Scenario: "optimistic", YearlyMetrics: convert(sp.Optimistic)})
	}
	return scenarios
}

// scenarioSummaryData holds computed summary metrics for one projection scenario.
type scenarioSummaryData struct {
	FinalValue    int
	FinalEquity   int
	TotalCashFlow int
	AvgCoC        float64
	Appreciation  int
	EquityGrowth  int
}

// computeScenarioSummary derives summary metrics from yearly projection data.
func computeScenarioSummary(years []ExpandedYearPDF) scenarioSummaryData {
	if len(years) == 0 {
		return scenarioSummaryData{}
	}
	last := years[len(years)-1]
	first := years[0]

	totalCashFlow := 0
	totalCoC := 0.0
	for _, yr := range years {
		totalCashFlow += yr.AnnualCashFlow
		totalCoC += yr.CashOnCash
	}

	return scenarioSummaryData{
		FinalValue:    last.PortfolioValue,
		FinalEquity:   last.Equity,
		TotalCashFlow: totalCashFlow,
		AvgCoC:        totalCoC / float64(len(years)),
		Appreciation:  last.PortfolioValue - first.PortfolioValue,
		EquityGrowth:  last.Equity - first.Equity,
	}
}

// addCommas inserts thousand separators into a non-negative integer string.
func addCommas(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var buf []byte
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, byte(ch))
	}
	return string(buf)
}

// formatCurrencyInt formats an integer as currency with comma separators.
func formatCurrencyInt(val int) string {
	if val == 0 {
		return "$0"
	}
	if val < 0 {
		return "($" + addCommas(int64(-val)) + ")"
	}
	return "$" + addCommas(int64(val))
}

func projectionScenarios(proj *InvestmentProjections) []InvestmentProjectionScenario {
	if proj == nil {
		return nil
	}
	var scenarios []InvestmentProjectionScenario
	if proj.Pessimistic != nil {
		scenarios = append(scenarios, InvestmentProjectionScenario{
			Scenario:      scenarioName(proj.Pessimistic.Scenario, "pessimistic"),
			YearlyMetrics: proj.Pessimistic.YearlyMetrics,
		})
	}
	if proj.Base != nil {
		scenarios = append(scenarios, InvestmentProjectionScenario{
			Scenario:      scenarioName(proj.Base.Scenario, "base"),
			YearlyMetrics: proj.Base.YearlyMetrics,
		})
	}
	if proj.Optimistic != nil {
		scenarios = append(scenarios, InvestmentProjectionScenario{
			Scenario:      scenarioName(proj.Optimistic.Scenario, "optimistic"),
			YearlyMetrics: proj.Optimistic.YearlyMetrics,
		})
	}
	return scenarios
}

func scenarioName(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return strings.ToLower(value)
}

func titleCase(value string) string {
	if value == "" {
		return ""
	}
	s := strings.ReplaceAll(strings.ReplaceAll(value, "-", " "), "_", " ")
	parts := strings.Fields(s)
	for i, part := range parts {
		lower := strings.ToLower(part)
		if len(lower) == 0 {
			continue
		}
		if len(lower) == 1 {
			parts[i] = strings.ToUpper(lower)
			continue
		}
		parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(parts, " ")
}

func formatCurrency(val float64) string {
	if val == 0 {
		return "$0"
	}
	if val < 0 {
		return "($" + addCommas(int64(-val)) + ")"
	}
	return "$" + addCommas(int64(val))
}

func formatPercent(val float64) string {
	if val == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", val)
}

// smartPercent converts a value to display percentage.
// Values <= 1 are treated as fractions (0.20 → 20.0), others used as-is.
func smartPercent(val float64) float64 {
	if val > 0 && val <= 1 {
		return val * 100
	}
	return val
}

// formatPercentSmart formats a value as percentage, auto-detecting fractions.
// Values <= 1 are multiplied by 100 (e.g., 0.20 → "20.0%").
func formatPercentSmart(val float64) string {
	return fmt.Sprintf("%.1f%%", smartPercent(val))
}

func formatPercentPtr(val *float64) string {
	if val == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.1f%%", *val)
}

func formatDecimal(val float64) string {
	if val == 0 {
		return "0.00"
	}
	return fmt.Sprintf("%.2f", val)
}

