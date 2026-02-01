package pdf

import (
	"context"
	"fmt"
	"sort"
)

// InvestmentCharts holds base64 chart images for investment PDFs.
type InvestmentCharts struct {
	EquityGrowth   string
	CashFlow       string
	PortfolioValue string
	ReturnMetrics  string
}

// InvestmentProjectionScenario represents a projection scenario for charts.
type InvestmentProjectionScenario struct {
	Scenario      string                     `json:"scenario"`
	YearlyMetrics []InvestmentYearlyMetrics  `json:"yearlyMetrics"`
}

// InvestmentYearlyMetrics holds aggregated yearly metrics used for charts.
type InvestmentYearlyMetrics struct {
	Year              int     `json:"year"`
	TotalPropertyValue float64 `json:"totalPropertyValue"`
	TotalLoanBalance  float64 `json:"totalLoanBalance"`
	TotalEquity       float64 `json:"totalEquity"`
	CashFlowAfterTax  float64 `json:"cashFlowAfterTax"`
	CashOnCashReturn  float64 `json:"cashOnCashReturn"`
	CapRate           float64 `json:"capRate"`
}

// GenerateInvestmentCharts generates investment planning charts using QuickChart.
func GenerateInvestmentCharts(ctx context.Context, client *QuickChartClient, scenarios []InvestmentProjectionScenario) (*InvestmentCharts, error) {
	if len(scenarios) == 0 {
		return nil, fmt.Errorf("no projections data provided")
	}

	sort.SliceStable(scenarios, func(i, j int) bool {
		order := map[string]int{"pessimistic": 0, "base": 1, "optimistic": 2}
		return order[scenarios[i].Scenario] < order[scenarios[j].Scenario]
	})

	base := scenarios[0]
	if len(base.YearlyMetrics) == 0 {
		return nil, fmt.Errorf("no yearly metrics for charts")
	}

	labels := make([]string, 0, len(base.YearlyMetrics))
	for _, metric := range base.YearlyMetrics {
		labels = append(labels, fmt.Sprintf("Year %d", metric.Year))
	}

	equityConfig := map[string]interface{}{
		"type": "bar",
		"data": map[string]interface{}{
			"labels":   labels,
			"datasets": buildScenarioDatasets(scenarios, func(m InvestmentYearlyMetrics) float64 { return m.TotalEquity }),
		},
		"options": chartDefaultOptions("Equity Growth"),
	}

	cashFlowConfig := map[string]interface{}{
		"type": "bar",
		"data": map[string]interface{}{
			"labels":   labels,
			"datasets": buildScenarioDatasets(scenarios, func(m InvestmentYearlyMetrics) float64 { return m.CashFlowAfterTax }),
		},
		"options": chartDefaultOptions("Annual Cash Flow"),
	}

	portfolioValueConfig := map[string]interface{}{
		"type": "bar",
		"data": map[string]interface{}{
			"labels": labels,
			"datasets": []map[string]interface{}{
				buildStackedDataset("Equity", scenarios, func(m InvestmentYearlyMetrics) float64 { return m.TotalEquity }, DefaultTheme.Primary),
				buildStackedDataset("Loan Balance", scenarios, func(m InvestmentYearlyMetrics) float64 { return m.TotalLoanBalance }, DefaultTheme.Muted),
			},
		},
		"options": chartStackedOptions("Portfolio Value"),
	}

	returnMetricsConfig := map[string]interface{}{
		"type": "bar",
		"data": map[string]interface{}{
			"labels": labels,
			"datasets": []map[string]interface{}{
				buildMetricDataset("Cash-on-Cash", scenarios, func(m InvestmentYearlyMetrics) float64 { return m.CashOnCashReturn }, DefaultTheme.Secondary),
				buildMetricDataset("Cap Rate", scenarios, func(m InvestmentYearlyMetrics) float64 { return m.CapRate }, DefaultTheme.Accent),
			},
		},
		"options": chartDefaultOptions("Return Metrics"),
	}

	if client == nil {
		client = NewQuickChartClient()
	}

	equityPNG, err := client.RenderPNG(ctx, equityConfig, 500, 300, "#FFFFFF")
	if err != nil {
		return nil, err
	}
	cashPNG, err := client.RenderPNG(ctx, cashFlowConfig, 500, 300, "#FFFFFF")
	if err != nil {
		return nil, err
	}
	valuePNG, err := client.RenderPNG(ctx, portfolioValueConfig, 500, 300, "#FFFFFF")
	if err != nil {
		return nil, err
	}
	returnPNG, err := client.RenderPNG(ctx, returnMetricsConfig, 500, 300, "#FFFFFF")
	if err != nil {
		return nil, err
	}

	return &InvestmentCharts{
		EquityGrowth:   EncodePNGBase64(equityPNG),
		CashFlow:       EncodePNGBase64(cashPNG),
		PortfolioValue: EncodePNGBase64(valuePNG),
		ReturnMetrics:  EncodePNGBase64(returnPNG),
	}, nil
}

func chartDefaultOptions(title string) map[string]interface{} {
	return map[string]interface{}{
		"plugins": map[string]interface{}{
			"legend": map[string]interface{}{
				"position": "bottom",
				"labels": map[string]interface{}{
					"boxWidth": 12,
					"font": map[string]interface{}{
						"size": 9,
					},
				},
			},
			"title": map[string]interface{}{
				"display": false,
				"text":    title,
			},
		},
	}
}

func chartStackedOptions(title string) map[string]interface{} {
	options := chartDefaultOptions(title)
	options["scales"] = map[string]interface{}{
		"x": map[string]interface{}{"stacked": true},
		"y": map[string]interface{}{"stacked": true},
	}
	return options
}

func buildScenarioDatasets(scenarios []InvestmentProjectionScenario, getter func(InvestmentYearlyMetrics) float64) []map[string]interface{} {
	datasets := make([]map[string]interface{}, 0, len(scenarios))
	for _, scenario := range scenarios {
		color := scenarioColor(scenario.Scenario)
		datasets = append(datasets, map[string]interface{}{
			"label":           scenarioLabel(scenario.Scenario),
			"data":            extractMetric(scenario.YearlyMetrics, getter),
			"backgroundColor": fmt.Sprintf("rgb(%d,%d,%d)", color.R, color.G, color.B),
		})
	}
	return datasets
}

func buildStackedDataset(label string, scenarios []InvestmentProjectionScenario, getter func(InvestmentYearlyMetrics) float64, color Color) map[string]interface{} {
	base := scenarios[0]
	return map[string]interface{}{
		"label":           label,
		"data":            extractMetric(base.YearlyMetrics, getter),
		"backgroundColor": fmt.Sprintf("rgb(%d,%d,%d)", color.R, color.G, color.B),
		"stack":           "value",
	}
}

func buildMetricDataset(label string, scenarios []InvestmentProjectionScenario, getter func(InvestmentYearlyMetrics) float64, color Color) map[string]interface{} {
	base := scenarios[0]
	return map[string]interface{}{
		"label":           label,
		"data":            extractMetric(base.YearlyMetrics, getter),
		"backgroundColor": fmt.Sprintf("rgb(%d,%d,%d)", color.R, color.G, color.B),
	}
}

func extractMetric(metrics []InvestmentYearlyMetrics, getter func(InvestmentYearlyMetrics) float64) []float64 {
	values := make([]float64, 0, len(metrics))
	for _, metric := range metrics {
		values = append(values, getter(metric))
	}
	return values
}

func scenarioColor(scenario string) Color {
	switch scenario {
	case "pessimistic":
		return Color{R: 239, G: 68, B: 68}
	case "optimistic":
		return Color{R: 16, G: 185, B: 129}
	default:
		return Color{R: 59, G: 130, B: 246}
	}
}

func scenarioLabel(scenario string) string {
	switch scenario {
	case "pessimistic":
		return "Pessimistic"
	case "optimistic":
		return "Optimistic"
	default:
		return "Base Case"
	}
}
