package pdf

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// InvestmentPlanPDFRequest is the payload for /api/report/investment-plan.
type InvestmentPlanPDFRequest struct {
	Portfolio      InvestmentPortfolioPDF `json:"portfolio"`
	SearchCriteria SearchCriteriaPDF      `json:"searchCriteria"`
	SearchResults  *SearchResultsPDF      `json:"searchResults,omitempty"`
	Projections    *InvestmentProjections `json:"projections,omitempty"`
	User           *PDFUser               `json:"user,omitempty"`
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

type InvestmentProjections struct {
	Base        *ProjectionScenarioPDF `json:"base,omitempty"`
	Optimistic  *ProjectionScenarioPDF `json:"optimistic,omitempty"`
	Pessimistic *ProjectionScenarioPDF `json:"pessimistic,omitempty"`
}

type ProjectionScenarioPDF struct {
	Scenario      string                    `json:"scenario"`
	YearlyMetrics []InvestmentYearlyMetrics `json:"yearlyMetrics"`
	Summary       map[string]interface{}    `json:"summary,omitempty"`
}

// BuildInvestmentPlanPDF renders the unified investment plan PDF.
func BuildInvestmentPlanPDF(ctx context.Context, req InvestmentPlanPDFRequest) ([]byte, error) {
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

	y = AddSectionHeading(pdf, page, theme, "Selected Properties", y+4)
	propCols := []TableColumn{
		{Header: "Address", Width: 62},
		{Header: "Price", Width: 22},
		{Header: "Rent", Width: 20},
		{Header: "CoC", Width: 16},
		{Header: "Cap", Width: 16},
		{Header: "Rec", Width: 18},
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
			prop.Recommendation,
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
			{Header: "Score", Width: 20},
			{Header: "Rating", Width: 30},
			{Header: "5Y Growth", Width: 24},
			{Header: "Volatility", Width: 24},
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
	parts := strings.Fields(strings.ReplaceAll(value, "-", " "))
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
	return fmt.Sprintf("$%.0f", val)
}

func formatPercent(val float64) string {
	if val == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", val)
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
