package pdf

import (
	"context"
	"fmt"

	"github.com/estara-ai/www/internal/services/investment"
)

// ScenarioV2Charts holds pre-rendered chart images for the frontier PDF.
type ScenarioV2Charts struct {
	ProbabilityFan string // base64 PNG — Monte Carlo fan (P10/P25/P50/P75/P90)
	FrontierPlot   string // base64 PNG — frontier scatter plot (all configs)
}

// GenerateFrontierCharts renders both PDF charts via QuickChart.io.
// All frontier points are needed for the scatter plot; the selected point
// supplies simulation results for the probability fan chart.
func GenerateFrontierCharts(
	ctx context.Context,
	client *QuickChartClient,
	allPoints []investment.FrontierPoint,
	selected *investment.FrontierPoint,
) (*ScenarioV2Charts, error) {
	if client == nil {
		client = NewQuickChartClient()
	}

	charts := &ScenarioV2Charts{}

	// ── Probability Fan ───────────────────────────────────────────────────────
	if selected != nil &&
		selected.SimulationResults != nil &&
		selected.SimulationResults.Percentiles != nil {

		fanPNG, err := renderProbabilityFan(ctx, client, selected.SimulationResults.Percentiles)
		if err == nil {
			charts.ProbabilityFan = EncodePNGBase64(fanPNG)
		}
		// Non-fatal: embed chart if available, skip if QuickChart fails
	}

	// ── Frontier Scatter Plot ─────────────────────────────────────────────────
	if len(allPoints) > 0 {
		scatterPNG, err := renderFrontierScatter(ctx, client, allPoints, selected)
		if err == nil {
			charts.FrontierPlot = EncodePNGBase64(scatterPNG)
		}
	}

	return charts, nil
}

// renderProbabilityFan builds a Chart.js line chart with filled bands for P10-P90.
func renderProbabilityFan(ctx context.Context, client *QuickChartClient, pct *investment.PercentileOutcomes) ([]byte, error) {
	// Build year labels from P50 (all percentiles share same years)
	labels := make([]string, len(pct.P50))
	for i, o := range pct.P50 {
		labels[i] = fmt.Sprintf("Yr %d", o.Year)
	}

	// Helper: extract net worth values scaled to $K for readability
	netWorths := func(outcomes []investment.YearOutcome) []float64 {
		vals := make([]float64, len(outcomes))
		for i, o := range outcomes {
			vals[i] = float64(o.NetWorth) / 1000
		}
		return vals
	}

	// Color palette (matches DefaultTheme)
	p90Color := "rgba(16,185,129,0.6)"   // Accent green
	p75Color := "rgba(59,130,246,0.4)"   // Secondary blue
	p50Color := "rgba(30,58,138,1)"      // Primary dark blue (solid)
	p25Color := "rgba(59,130,246,0.4)"   // Secondary blue
	p10Color := "rgba(239,68,68,0.6)"    // Red

	p90Fill := "rgba(16,185,129,0.1)"
	p75Fill := "rgba(59,130,246,0.12)"

	// Chart.js 3 datasets: P90 fills to P10, P75 fills to P25, P50 is solid
	datasets := []map[string]any{
		{
			"label":           "P90 (Best Case)",
			"data":            netWorths(pct.P90),
			"borderColor":     p90Color,
			"backgroundColor": p90Fill,
			"fill":            "+4", // fill toward P10 (index 4)
			"tension":         0.4,
			"pointRadius":     2,
			"borderWidth":     1.5,
			"borderDash":      []int{4, 4},
		},
		{
			"label":           "P75",
			"data":            netWorths(pct.P75),
			"borderColor":     p75Color,
			"backgroundColor": p75Fill,
			"fill":            "+2", // fill toward P25 (index 3)
			"tension":         0.4,
			"pointRadius":     2,
			"borderWidth":     1.5,
			"borderDash":      []int{2, 2},
		},
		{
			"label":           "Median (P50)",
			"data":            netWorths(pct.P50),
			"borderColor":     p50Color,
			"backgroundColor": "transparent",
			"fill":            false,
			"tension":         0.4,
			"pointRadius":     3,
			"borderWidth":     2.5,
		},
		{
			"label":           "P25",
			"data":            netWorths(pct.P25),
			"borderColor":     p25Color,
			"backgroundColor": "transparent",
			"fill":            false,
			"tension":         0.4,
			"pointRadius":     2,
			"borderWidth":     1.5,
			"borderDash":      []int{2, 2},
		},
		{
			"label":           "P10 (Downside)",
			"data":            netWorths(pct.P10),
			"borderColor":     p10Color,
			"backgroundColor": "transparent",
			"fill":            false,
			"tension":         0.4,
			"pointRadius":     2,
			"borderWidth":     1.5,
			"borderDash":      []int{4, 4},
		},
	}

	config := map[string]any{
		"type": "line",
		"data": map[string]any{
			"labels":   labels,
			"datasets": datasets,
		},
		"options": map[string]any{
			"responsive": false,
			"plugins": map[string]any{
				"title": map[string]any{
					"display": true,
					"text":    "10-Year Net Worth Projection ($K)",
					"color":   "#1e293b",
					"font":    map[string]any{"size": 14, "weight": "bold"},
				},
				"legend": map[string]any{
					"position": "bottom",
					"labels":   map[string]any{"font": map[string]any{"size": 10}},
				},
			},
			"scales": map[string]any{
				"x": map[string]any{
					"grid": map[string]any{"color": "rgba(0,0,0,0.05)"},
					"ticks": map[string]any{"font": map[string]any{"size": 10}},
				},
				"y": map[string]any{
					"title": map[string]any{
						"display": true,
						"text":    "Net Worth ($K)",
						"color":   "#64748b",
						"font":    map[string]any{"size": 11},
					},
					"grid": map[string]any{"color": "rgba(0,0,0,0.05)"},
					"ticks": map[string]any{"font": map[string]any{"size": 10}},
				},
			},
		},
	}

	return client.RenderPNG(ctx, config, 680, 340, "#FFFFFF")
}

// renderFrontierScatter builds a scatter chart of all frontier configs
// with the selected config highlighted.
func renderFrontierScatter(
	ctx context.Context,
	client *QuickChartClient,
	allPoints []investment.FrontierPoint,
	selected *investment.FrontierPoint,
) ([]byte, error) {
	// Scatter colors: cycle through secondary palette
	configColors := []string{
		"rgba(30,58,138,0.85)",   // Primary dark blue
		"rgba(59,130,246,0.85)",  // Secondary blue
		"rgba(16,185,129,0.85)",  // Accent green
		"rgba(245,158,11,0.85)",  // Warning amber
		"rgba(139,92,246,0.85)",  // Purple
	}

	// Build one dataset per config so we can set individual colors
	datasets := make([]map[string]any, 0, len(allPoints))
	selectedConfigIndex := -1
	if selected != nil {
		selectedConfigIndex = selected.ConfigIndex
	}

	for i, pt := range allPoints {
		color := configColors[i%len(configColors)]
		radius := 8
		borderWidth := 1
		label := fmt.Sprintf("Config %d (%.1f%% ret / %.1f%% vol)", pt.ConfigIndex+1, pt.ExpectedReturn, pt.PortfolioVolatility)
		if pt.ConfigIndex == selectedConfigIndex {
			radius = 14
			borderWidth = 3
			label += " ✓"
		}
		datasets = append(datasets, map[string]any{
			"label":           label,
			"data":            []map[string]any{{"x": roundTwo(pt.PortfolioVolatility), "y": roundTwo(pt.ExpectedReturn)}},
			"backgroundColor": color,
			"borderColor":     "white",
			"borderWidth":     borderWidth,
			"pointRadius":     radius,
		})
	}

	config := map[string]any{
		"type": "scatter",
		"data": map[string]any{
			"datasets": datasets,
		},
		"options": map[string]any{
			"responsive": false,
			"plugins": map[string]any{
				"title": map[string]any{
					"display": true,
					"text":    "Efficient Frontier — Return vs. Volatility",
					"color":   "#1e293b",
					"font":    map[string]any{"size": 14, "weight": "bold"},
				},
				"legend": map[string]any{
					"position": "bottom",
					"labels":   map[string]any{"font": map[string]any{"size": 9}},
				},
			},
			"scales": map[string]any{
				"x": map[string]any{
					"title": map[string]any{
						"display": true,
						"text":    "Portfolio Volatility (%)",
						"color":   "#64748b",
						"font":    map[string]any{"size": 11},
					},
					"grid": map[string]any{"color": "rgba(0,0,0,0.05)"},
					"ticks": map[string]any{"font": map[string]any{"size": 10}},
				},
				"y": map[string]any{
					"title": map[string]any{
						"display": true,
						"text":    "Expected Return (%)",
						"color":   "#64748b",
						"font":    map[string]any{"size": 11},
					},
					"grid": map[string]any{"color": "rgba(0,0,0,0.05)"},
					"ticks": map[string]any{"font": map[string]any{"size": 10}},
				},
			},
		},
	}

	return client.RenderPNG(ctx, config, 680, 360, "#FFFFFF")
}

// roundTwo rounds a float64 to 2 decimal places.
func roundTwo(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
