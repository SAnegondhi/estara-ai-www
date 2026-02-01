package pdf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MarketTrendsExportRequest mirrors /api/market-trends/export payload.
type MarketTrendsExportRequest struct {
	Format string            `json:"format,omitempty"`
	Data   MarketTrendsData  `json:"data"`
}

type MarketTrendsData struct {
	Metro            string                 `json:"metro"`
	CanonicalName    string                 `json:"canonicalName"`
	CustomerEmail    string                 `json:"customerEmail,omitempty"`
	DataAvailability map[string]interface{} `json:"dataAvailability,omitempty"`
	CalculatedMetrics map[string]interface{} `json:"calculatedMetrics,omitempty"`
	Comparisons      map[string]interface{} `json:"comparisons,omitempty"`
	AISynthesis      string                 `json:"aiSynthesis,omitempty"`
	TimeSeries       *MarketTrendsTimeSeries `json:"timeSeries,omitempty"`
}

type MarketTrendsTimeSeries struct {
	Zhvi []TimeSeriesPoint `json:"zhvi,omitempty"`
	Zori []TimeSeriesPoint `json:"zori,omitempty"`
}

type TimeSeriesPoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// BuildMarketTrendsPDF renders a market trends PDF.
func BuildMarketTrendsPDF(ctx context.Context, req MarketTrendsExportRequest) ([]byte, error) {
	pdf := NewPDF("P", "mm", "A4")
	page := A4Page
	theme := DefaultTheme

	title := "Market Trends Report"
	location := req.Data.CanonicalName
	if location == "" {
		location = req.Data.Metro
	}
	if location == "" {
		location = "Market Trends"
	}

	AddCoverPage(pdf, page, theme, title, "Price & Rent Trends", location, req.Data.CustomerEmail)
	AddHeaderFooter(pdf, page, theme, "Market Trends")

	pdf.AddPage()
	y := page.MarginTop
	y = AddSectionHeading(pdf, page, theme, "Key Metrics", y)

	metrics := buildMarketTrendsMetrics(req.Data.CalculatedMetrics)
	y = AddMetricsGrid(pdf, page, theme, metrics, y)

	if req.Data.TimeSeries != nil {
		charts, err := buildMarketTrendsCharts(ctx, req.Data.TimeSeries)
		if err == nil {
			y = AddSectionHeading(pdf, page, theme, "Trend Charts", y+4)
			chartWidth := (page.Width - page.MarginLeft - page.MarginRight - 10) / 2
			chartHeight := 60.0
			_ = AddImageFromBase64(pdf, "price_trend", charts[0], page.MarginLeft, y+4, chartWidth, chartHeight)
			_ = AddImageFromBase64(pdf, "rent_trend", charts[1], page.MarginLeft+chartWidth+10, y+4, chartWidth, chartHeight)
			y += chartHeight + 12
		}
	}

	if req.Data.Comparisons != nil {
		y = AddSectionHeading(pdf, page, theme, "Comparison Markets", y+4)
		rows := buildComparisonRows(req.Data.Comparisons)
		if len(rows) > 0 {
			cols := []TableColumn{
				{Header: "Market", Width: 70},
				{Header: "Price Growth", Width: 30},
				{Header: "Rent Growth", Width: 30},
				{Header: "Cap Rate", Width: 25},
				{Header: "Affordability", Width: 25},
			}
			y = AddTable(pdf, page, theme, cols, rows, y)
		}
	}

	if req.Data.AISynthesis != "" {
		y = AddSectionHeading(pdf, page, theme, "AI Synthesis", y+4)
		y = AddParagraph(pdf, page, theme, req.Data.AISynthesis, y)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildMarketTrendsMetrics(metrics map[string]interface{}) []MetricItem {
	if metrics == nil {
		return []MetricItem{}
	}
	values := make([]MetricItem, 0, 10)
	addMetric := func(label string, key string, suffix string) {
		if val, ok := metrics[key]; ok {
			values = append(values, MetricItem{Label: label, Value: formatMetricValue(val, suffix)})
		}
	}
	addMetric("Price Growth (5Y)", "priceGrowth5Y", "%")
	addMetric("Rent Growth (5Y)", "rentGrowth5Y", "%")
	addMetric("Median Price", "medianHomePrice", "$")
	addMetric("Median Rent", "medianRent", "$")
	addMetric("Cap Rate", "capRate", "%")
	addMetric("Price-to-Rent", "priceToRentRatio", "x")
	addMetric("Days on Market", "medianDaysOnMarket", "")
	addMetric("Vacancy Rate", "vacancyRate", "%")
	addMetric("Population Growth", "populationGrowth", "%")
	addMetric("Employment Growth", "employmentGrowthRate", "%")
	return values
}

func formatMetricValue(val interface{}, suffix string) string {
	switch v := val.(type) {
	case float64:
		if suffix == "$" {
			return fmt.Sprintf("$%.0f", v)
		}
		if suffix == "%" {
			return fmt.Sprintf("%.1f%%", v)
		}
		if suffix == "x" {
			return fmt.Sprintf("%.1fx", v)
		}
		return fmt.Sprintf("%.2f", v)
	case int:
		if suffix == "$" {
			return fmt.Sprintf("$%d", v)
		}
		return fmt.Sprintf("%d", v)
	default:
		return "N/A"
	}
}

func buildComparisonRows(comparisons map[string]interface{}) [][]string {
	rows := [][]string{}
	if comparisons == nil {
		return rows
	}

	if list, ok := comparisons["metros"].([]interface{}); ok {
		for _, item := range list {
			obj, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			rows = append(rows, []string{
				toString(obj["canonicalName"]),
				formatMetricValue(obj["priceGrowth5Y"], "%"),
				formatMetricValue(obj["rentGrowth5Y"], "%"),
				formatMetricValue(obj["capRate"], "%"),
				formatMetricValue(obj["priceToIncomeRatio"], "x"),
			})
		}
	}
	return rows
}

func buildMarketTrendsCharts(ctx context.Context, series *MarketTrendsTimeSeries) ([]string, error) {
	client := NewQuickChartClient()
	priceConfig := chartConfigFromSeries("Median Home Price", series.Zhvi, DefaultTheme.Primary)
	rentConfig := chartConfigFromSeries("Median Rent", series.Zori, DefaultTheme.Secondary)

	pricePNG, err := client.RenderPNG(ctx, priceConfig, 600, 300, "#FFFFFF")
	if err != nil {
		return nil, err
	}
	rentPNG, err := client.RenderPNG(ctx, rentConfig, 600, 300, "#FFFFFF")
	if err != nil {
		return nil, err
	}
	return []string{EncodePNGBase64(pricePNG), EncodePNGBase64(rentPNG)}, nil
}

func chartConfigFromSeries(title string, series []TimeSeriesPoint, color Color) map[string]interface{} {
	labels := make([]string, 0, len(series))
	values := make([]float64, 0, len(series))
	for _, point := range series {
		if point.Date == "" {
			continue
		}
		labels = append(labels, formatSeriesDate(point.Date))
		values = append(values, point.Value)
	}

	return map[string]interface{}{
		"type": "line",
		"data": map[string]interface{}{
			"labels": labels,
			"datasets": []map[string]interface{}{
				{
					"label":           title,
					"data":            values,
					"borderColor":     fmt.Sprintf("rgb(%d,%d,%d)", color.R, color.G, color.B),
					"backgroundColor": "rgba(0,0,0,0)",
					"borderWidth":     2,
					"pointRadius":     2,
				},
			},
		},
		"options": map[string]interface{}{
			"plugins": map[string]interface{}{
				"legend": map[string]interface{}{"display": false},
			},
			"scales": map[string]interface{}{
				"x": map[string]interface{}{
					"ticks": map[string]interface{}{"maxTicksLimit": 8},
				},
			},
		},
	}
}

func formatSeriesDate(value string) string {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Format("Jan 2006")
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.Format("Jan 2006")
	}
	return strings.TrimSpace(value)
}

func toString(val interface{}) string {
	switch v := val.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		data, _ := json.Marshal(val)
		return string(data)
	}
}
