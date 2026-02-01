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

	y = AddSectionHeading(pdf, page, theme, "Executive Summary", y)
	if summary := getNarrativeField(data.Narrative, "executiveSummary"); summary != "" {
		y = AddParagraph(pdf, page, theme, summary, y)
	} else if data.FullReport != "" {
		y = AddParagraph(pdf, page, theme, data.FullReport, y)
	}

	y = AddSectionHeading(pdf, page, theme, "Market Snapshot", y+2)
	metrics := buildMarketAnalysisMetrics(data)
	y = AddMetricsGrid(pdf, page, theme, metrics, y)

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

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
