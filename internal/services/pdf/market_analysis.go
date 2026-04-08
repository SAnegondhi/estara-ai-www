package pdf

import (
	"bytes"
	"context"
	"fmt"
	"math"
)

// MarketAnalysisPDFData represents normalized market analysis data for PDF.
type MarketAnalysisPDFData struct {
	Location   string                 `json:"location"`
	Metrics    map[string]interface{} `json:"metrics,omitempty"`
	Narrative  map[string]interface{} `json:"narrative,omitempty"`
	FullReport string                 `json:"fullReport,omitempty"`
	DataPoints map[string]interface{} `json:"dataPoints,omitempty"`
	// ADR-100: structured enrichment from aggregator + trends service
	StructuredMetrics *MarketAnalysisMetrics    `json:"structuredMetrics,omitempty"`
	TimeSeries        *MarketAnalysisTimeSeries `json:"timeSeries,omitempty"`
}

// MarketAnalysisMetrics holds typed market metrics for PDF rendering (ADR-100).
type MarketAnalysisMetrics struct {
	MedianHomePrice      int     `json:"medianHomePrice"`
	MedianRent           int     `json:"medianRent"`
	CapRate              float64 `json:"capRate"`
	MortgageRate30       float64 `json:"mortgageRate30"`
	GrossYield           float64 `json:"grossYield"`
	PriceToRentRatio     float64 `json:"priceToRentRatio"`
	PriceChangeYoY       float64 `json:"priceChangeYoY"`
	RentChangeYoY        float64 `json:"rentChangeYoY"`
	VacancyRate          float64 `json:"vacancyRate"`
	UnemploymentRate     float64 `json:"unemploymentRate"`
	EmploymentGrowthRate float64 `json:"employmentGrowthRate"`
	PopulationGrowthRate float64 `json:"populationGrowthRate"`
	DaysOnMarket         int     `json:"daysOnMarket"`
	DataDate             string  `json:"dataDate"`
}

// MarketAnalysisTimeSeriesPoint is a single chart data point (ADR-100).
type MarketAnalysisTimeSeriesPoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// MarketAnalysisTimeSeries holds historical time series for chart rendering (ADR-100).
type MarketAnalysisTimeSeries struct {
	HomeValues    []MarketAnalysisTimeSeriesPoint `json:"homeValues"`
	RentValues    []MarketAnalysisTimeSeriesPoint `json:"rentValues"`
	MortgageRates []MarketAnalysisTimeSeriesPoint `json:"mortgageRates,omitempty"`
}

// BuildMarketAnalysisPDF renders a market analysis PDF report.
func BuildMarketAnalysisPDF(ctx context.Context, data MarketAnalysisPDFData) ([]byte, error) {
	pdf := NewPDF("P", "mm", "A4")
	page := A4Page
	theme := DefaultTheme

	location := data.Location
	if location == "" {
		location = "Market Analysis"
	}

	AddCoverPage(pdf, page, theme, "Market Analysis Report", "AI Market Insights", location, "")
	AddHeaderFooter(pdf, page, theme, "Market Analysis")

	pdf.AddPage()
	y := page.MarginTop

	// ADR-100: Render structured KPI strip when enrichment data is available
	if data.StructuredMetrics != nil {
		y = AddSectionHeading(pdf, page, theme, "Market Snapshot", y)
		kpis := buildStructuredMetricsGrid(data.StructuredMetrics)
		y = AddMetricsGrid(pdf, page, theme, kpis, y)
		y += 4
	}

	// ADR-100: Render time series charts when historical data is available
	if data.TimeSeries != nil {
		charts, _ := buildTimeSeriesCharts(ctx, data.TimeSeries, location)
		if len(charts) > 0 {
			y = AddSectionHeading(pdf, page, theme, "Historical Trends (5 Years)", y+2)
			fullWidth := page.Width - page.MarginLeft - page.MarginRight
			halfWidth := (fullWidth - 10) / 2
			chartHeight := 60.0
			y, _ = EnsureSpace(pdf, page, y+4, chartHeight+12)
			// Home values (left) + Rent values (right) — side by side
			if len(charts) > 0 {
				_ = AddImageFromBase64(pdf, "home_values", charts[0], page.MarginLeft, y, halfWidth, chartHeight)
			}
			if len(charts) > 1 {
				_ = AddImageFromBase64(pdf, "rent_values", charts[1], page.MarginLeft+halfWidth+10, y, halfWidth, chartHeight)
			}
			y += chartHeight + 8
			// Mortgage rate chart — full width (rate spike is the most compelling signal)
			if len(charts) > 2 {
				y, _ = EnsureSpace(pdf, page, y, chartHeight+16)
				_ = AddImageFromBase64(pdf, "mortgage_rates", charts[2], page.MarginLeft, y, fullWidth, chartHeight+8)
				y += chartHeight + 16
			}
		}
	}

	if data.FullReport != "" {
		// V2 path: the markdown report IS the full content — render it directly
		y = RenderMarkdown(pdf, page, theme, data.FullReport, y)
	} else {
		// V1 path: structured sections from dual-agent pipeline
		y = AddSectionHeading(pdf, page, theme, "Executive Summary", y)
		if summary := getNarrativeField(data.Narrative, "executiveSummary"); summary != "" {
			y = AddParagraph(pdf, page, theme, summary, y)
		}

		// Only show legacy metrics grid if no structured metrics were rendered above
		if data.StructuredMetrics == nil {
			y = AddSectionHeading(pdf, page, theme, "Market Metrics", y+2)
			metrics := buildMarketAnalysisMetrics(data)
			y = AddMetricsGrid(pdf, page, theme, metrics, y)
		}

		// Legacy charts only when no time series data
		if data.TimeSeries == nil {
			charts, _ := buildMarketAnalysisCharts(ctx, data)
			if len(charts) > 0 {
				y = AddSectionHeading(pdf, page, theme, "Market Charts", y+2)
				chartWidth := (page.Width - page.MarginLeft - page.MarginRight - 10) / 2
				chartHeight := 55.0
				if len(charts) > 0 {
					_ = AddImageFromBase64(pdf, "price_trends", charts[0], page.MarginLeft, y+4, chartWidth, chartHeight)
				}
				if len(charts) > 1 {
					_ = AddImageFromBase64(pdf, "affordability", charts[1], page.MarginLeft+chartWidth+10, y+4, chartWidth, chartHeight)
				}
				y += chartHeight + 12
				if len(charts) > 2 {
					_ = AddImageFromBase64(pdf, "supply_demand", charts[2], page.MarginLeft, y, chartWidth, chartHeight)
				}
				if len(charts) > 3 {
					_ = AddImageFromBase64(pdf, "waterfall", charts[3], page.MarginLeft+chartWidth+10, y, chartWidth, chartHeight)
				}
				y += chartHeight + 8
			}
		}

		sections := []struct {
			Title string
			Key   string
		}{
			{"Market Conditions", "marketConditions"},
			{"Investment Strategy", "investmentStrategy"},
			{"Risk Analysis", "riskAnalysis"},
			{"Recommendations", "recommendations"},
		}

		for _, section := range sections {
			if text := getNarrativeField(data.Narrative, section.Key); text != "" {
				y = AddSectionHeading(pdf, page, theme, section.Title, y+4)
				y = AddParagraph(pdf, page, theme, text, y)
			}
		}

		if insights := getNarrativeList(data.Narrative, "keyInsights"); len(insights) > 0 {
			y = AddSectionHeading(pdf, page, theme, "Key Insights", y+4)
			y = AddBulletList(pdf, page, theme, insights, y)
		}
		if actions := getNarrativeList(data.Narrative, "actionItems"); len(actions) > 0 {
			y = AddSectionHeading(pdf, page, theme, "Action Items", y+4)
			y = AddBulletList(pdf, page, theme, actions, y)
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildStructuredMetricsGrid builds a MetricItem slice from typed ADR-100 enrichment data.
func buildStructuredMetricsGrid(m *MarketAnalysisMetrics) []MetricItem {
	items := []MetricItem{}
	if m.MedianHomePrice > 0 {
		items = append(items, MetricItem{Label: "Median Home Price", Value: fmt.Sprintf("$%s", formatInt(m.MedianHomePrice))})
	}
	if m.MedianRent > 0 {
		// Label source to prevent mismatch confusion with AI-reported ZORI figures
		items = append(items, MetricItem{Label: "Median Rent/mo (ZORI)", Value: fmt.Sprintf("$%s", formatInt(m.MedianRent))})
	}
	// Cap rate is intentionally omitted: the V2 analysis prompt states there are no
	// observed cap rates in the data — showing a derived cap rate here conflicts with
	// the AI report's gross yield figure and confuses readers (critique fix #3).
	if m.GrossYield > 0 {
		items = append(items, MetricItem{Label: "Gross Yield", Value: fmt.Sprintf("%.1f%%", m.GrossYield)})
	}
	if m.PriceToRentRatio > 0 {
		items = append(items, MetricItem{Label: "Price-to-Rent", Value: fmt.Sprintf("%.0fx", m.PriceToRentRatio)})
	}
	if m.MortgageRate30 > 0 {
		items = append(items, MetricItem{Label: "30Y Rate", Value: fmt.Sprintf("%.2f%%", m.MortgageRate30)})
	}
	if m.PriceChangeYoY != 0 {
		items = append(items, MetricItem{Label: "Price YoY", Value: fmt.Sprintf("%+.1f%%", m.PriceChangeYoY)})
	}
	if m.RentChangeYoY != 0 {
		items = append(items, MetricItem{Label: "Rent YoY", Value: fmt.Sprintf("%+.1f%%", m.RentChangeYoY)})
	}
	if m.VacancyRate > 0 {
		// Vacancy rate comes from FRED national rental vacancy (RRVRUSQ156N), not city-specific.
		// Label it clearly so it doesn't contradict AI report notes about missing local vacancy data.
		items = append(items, MetricItem{Label: "Rental Vacancy (National)", Value: fmt.Sprintf("%.1f%%", m.VacancyRate)})
	}
	if m.UnemploymentRate > 0 {
		items = append(items, MetricItem{Label: "Unemployment", Value: fmt.Sprintf("%.1f%%", m.UnemploymentRate)})
	}
	if m.DaysOnMarket > 0 {
		items = append(items, MetricItem{Label: "Days on Market", Value: fmt.Sprintf("%d", m.DaysOnMarket)})
	}
	return items
}

// formatInt formats an integer with comma-separated thousands.
func formatInt(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	result := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result += ","
		}
		result += string(c)
	}
	return result
}

// buildTimeSeriesCharts generates QuickChart line chart PNGs from historical time series (ADR-100).
func buildTimeSeriesCharts(ctx context.Context, ts *MarketAnalysisTimeSeries, location string) ([]string, error) {
	client := NewQuickChartClient()
	charts := []string{}

	lineColor := "#1e3a8a"
	rentColor := "#10b981"
	rateColor := "#f59e0b"

	buildLineChart := func(points []MarketAnalysisTimeSeriesPoint, label, color string, prefix string) string {
		if len(points) < 2 {
			return ""
		}
		labels := make([]string, len(points))
		values := make([]float64, len(points))
		for i, p := range points {
			if len(p.Date) >= 7 {
				labels[i] = p.Date[:7] // YYYY-MM
			} else {
				labels[i] = p.Date
			}
			values[i] = p.Value
		}
		// Show only every 12th label for readability
		sparseLabels := make([]string, len(labels))
		for i, l := range labels {
			if i%12 == 0 || i == len(labels)-1 {
				sparseLabels[i] = l
			}
		}
		callbackStr := ""
		if prefix == "$" {
			callbackStr = `function(value) { return '$' + (value >= 1000 ? (value/1000).toFixed(0) + 'k' : value); }`
		} else if prefix == "%" {
			callbackStr = `function(value) { return value.toFixed(2) + '%'; }`
		}
		config := map[string]interface{}{
			"type": "line",
			"data": map[string]interface{}{
				"labels": sparseLabels,
				"datasets": []map[string]interface{}{
					{
						"label":           label,
						"data":            values,
						"borderColor":     color,
						"backgroundColor": color + "20",
						"fill":            true,
						"tension":         0.3,
						"pointRadius":     0,
						"borderWidth":     2,
					},
				},
			},
			"options": map[string]interface{}{
				"plugins": map[string]interface{}{
					"legend": map[string]interface{}{"display": true},
				},
				"scales": map[string]interface{}{
					"x": map[string]interface{}{
						"ticks": map[string]interface{}{"maxRotation": 45},
					},
					"y": map[string]interface{}{
						"ticks": map[string]interface{}{"callback": callbackStr},
					},
				},
			},
		}
		png, err := client.RenderPNG(ctx, config, 600, 350, "#FFFFFF")
		if err != nil {
			return ""
		}
		return EncodePNGBase64(png)
	}

	if img := buildLineChart(ts.HomeValues, "Median Home Value — "+location, lineColor, "$"); img != "" {
		charts = append(charts, img)
	}
	if img := buildLineChart(ts.RentValues, "Median Rent — "+location, rentColor, "$"); img != "" {
		charts = append(charts, img)
	}
	if img := buildLineChart(ts.MortgageRates, "30Y Mortgage Rate", rateColor, "%"); img != "" {
		charts = append(charts, img)
	}

	return charts, nil
}

func buildMarketAnalysisMetrics(data MarketAnalysisPDFData) []MetricItem {
	metrics := []MetricItem{}
	add := func(label, key, suffix string) {
		if val := getMetricValue(data, key); val != nil {
			metrics = append(metrics, MetricItem{
				Label: label,
				Value: formatValue(val, suffix),
			})
		}
	}
	add("Median Home Price", "medianHomePrice", "$")
	add("Average Rent", "averageRent", "$")
	add("Cap Rate", "capRate", "%")
	add("Vacancy Rate", "vacancyRate", "%")
	add("Price-to-Income", "priceToIncomeRatio", "x")
	add("Days on Market", "daysOnMarket", "")
	add("5Y Price Growth", "zhvi5YGrowth", "%")
	add("5Y Rent Growth", "zori5YGrowth", "%")
	return metrics
}

func buildMarketAnalysisCharts(ctx context.Context, data MarketAnalysisPDFData) ([]string, error) {
	client := NewQuickChartClient()
	charts := []string{}

	priceGrowth := toFloat(getMetricValue(data, "zhvi5YGrowth"))
	rentGrowth := toFloat(getMetricValue(data, "zori5YGrowth"))
	if priceGrowth != nil || rentGrowth != nil {
		config := map[string]interface{}{
			"type": "bar",
			"data": map[string]interface{}{
				"labels": []string{"Price Growth 5Y", "Rent Growth 5Y"},
				"datasets": []map[string]interface{}{
					{
						"label":           "Growth (%)",
						"data":            []float64{valueOrZero(priceGrowth), valueOrZero(rentGrowth)},
						"backgroundColor": []string{"#3B82F6", "#10B981"},
					},
				},
			},
		}
		png, err := client.RenderPNG(ctx, config, 500, 300, "#FFFFFF")
		if err == nil {
			charts = append(charts, EncodePNGBase64(png))
		}
	}

	pti := toFloat(getMetricValue(data, "priceToIncomeRatio"))
	if pti != nil {
		config := map[string]interface{}{
			"type": "doughnut",
			"data": map[string]interface{}{
				"labels": []string{"Price-to-Income"},
				"datasets": []map[string]interface{}{
					{
						"data":            []float64{*pti, math.Max(0, 10-*pti)},
						"backgroundColor": []string{"#F59E0B", "#E2E8F0"},
					},
				},
			},
		}
		png, err := client.RenderPNG(ctx, config, 500, 300, "#FFFFFF")
		if err == nil {
			charts = append(charts, EncodePNGBase64(png))
		}
	}

	monthsSupply := toFloat(getMetricValue(data, "monthsOfSupply"))
	if monthsSupply != nil {
		config := map[string]interface{}{
			"type": "bar",
			"data": map[string]interface{}{
				"labels": []string{"Months of Supply"},
				"datasets": []map[string]interface{}{
					{
						"label":           "Supply",
						"data":            []float64{*monthsSupply},
						"backgroundColor": "#64748B",
					},
				},
			},
		}
		png, err := client.RenderPNG(ctx, config, 500, 300, "#FFFFFF")
		if err == nil {
			charts = append(charts, EncodePNGBase64(png))
		}
	}

	rent := toFloat(getMetricValue(data, "averageRent"))
	price := toFloat(getMetricValue(data, "medianHomePrice"))
	if rent != nil && price != nil {
		mortgage := estimateMortgage(*price)
		expenses := *rent * 0.25
		cashFlow := *rent - expenses - mortgage
		config := map[string]interface{}{
			"type": "bar",
			"data": map[string]interface{}{
				"labels": []string{"Gross Rent", "Expenses", "Mortgage", "Net Cash Flow"},
				"datasets": []map[string]interface{}{
					{
						"label":           "Monthly Cash Flow",
						"data":            []float64{*rent, expenses, mortgage, cashFlow},
						"backgroundColor": []string{"#3B82F6", "#F59E0B", "#EF4444", "#10B981"},
					},
				},
			},
		}
		png, err := client.RenderPNG(ctx, config, 600, 300, "#FFFFFF")
		if err == nil {
			charts = append(charts, EncodePNGBase64(png))
		}
	}

	return charts, nil
}

func getNarrativeField(narrative map[string]interface{}, key string) string {
	if narrative == nil {
		return ""
	}
	if val, ok := narrative[key]; ok {
		return fmt.Sprintf("%v", val)
	}
	return ""
}

func getNarrativeList(narrative map[string]interface{}, key string) []string {
	if narrative == nil {
		return nil
	}
	raw, ok := narrative[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []interface{}:
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, fmt.Sprintf("%v", item))
		}
		return items
	case []string:
		return v
	default:
		return nil
	}
}

func getMetricValue(data MarketAnalysisPDFData, key string) interface{} {
	if data.DataPoints != nil {
		if val, ok := data.DataPoints[key]; ok {
			return val
		}
	}
	if data.Metrics != nil {
		if val, ok := data.Metrics[key]; ok {
			return val
		}
	}
	return nil
}

func formatValue(val interface{}, suffix string) string {
	switch v := val.(type) {
	case float64:
		return formatValueFloat(v, suffix)
	case int:
		return formatValueFloat(float64(v), suffix)
	case int64:
		return formatValueFloat(float64(v), suffix)
	case string:
		if v == "" {
			return "N/A"
		}
		return v
	default:
		return "N/A"
	}
}

func formatValueFloat(val float64, suffix string) string {
	if suffix == "$" {
		return fmt.Sprintf("$%.0f", val)
	}
	if suffix == "%" {
		return fmt.Sprintf("%.1f%%", val)
	}
	if suffix == "x" {
		return fmt.Sprintf("%.1fx", val)
	}
	if suffix == "" {
		return fmt.Sprintf("%.0f", val)
	}
	return fmt.Sprintf("%.2f%s", val, suffix)
}

func toFloat(val interface{}) *float64 {
	switch v := val.(type) {
	case float64:
		return &v
	case int:
		f := float64(v)
		return &f
	case int64:
		f := float64(v)
		return &f
	case string:
		if v == "" {
			return nil
		}
	}
	return nil
}

func valueOrZero(val *float64) float64 {
	if val == nil {
		return 0
	}
	return *val
}

func estimateMortgage(price float64) float64 {
	loan := price * 0.8
	rate := 0.07 / 12
	n := 360.0
	payment := loan * (rate * math.Pow(1+rate, n)) / (math.Pow(1+rate, n) - 1)
	if math.IsNaN(payment) || math.IsInf(payment, 0) {
		return 0
	}
	return payment
}
