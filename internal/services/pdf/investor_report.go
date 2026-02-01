package pdf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// InvestorReportPDFInput aggregates data for investor report PDF generation.
type InvestorReportPDFInput struct {
	ReportType      string
	PropertyAddress string
	Email           string
	MetricsData     json.RawMessage
	NarrativeData   json.RawMessage
	FullReport      *string
	GeneratedAt     time.Time
}

// BuildInvestorReportPDF renders an investor report PDF.
func BuildInvestorReportPDF(input InvestorReportPDFInput) ([]byte, error) {
	pdf := NewPDF("P", "mm", "Letter")
	page := LetterPage
	theme := DefaultTheme

	title := "Investor Report"
	if input.ReportType != "" {
		title = fmt.Sprintf("%s Report", titleCase(input.ReportType))
	}

	location := input.PropertyAddress
	if location == "" {
		location = "Investor Report"
	}

	AddCoverPage(pdf, page, theme, title, "Investment Analysis", location, input.Email)
	AddHeaderFooter(pdf, page, theme, "Investor Report")

	metrics := map[string]interface{}{}
	narrative := map[string]interface{}{}
	_ = json.Unmarshal(input.MetricsData, &metrics)
	_ = json.Unmarshal(input.NarrativeData, &narrative)

	pdf.AddPage()
	y := page.MarginTop
	y = AddSectionHeading(pdf, page, theme, "Executive Summary", y)
	if summary := getNarrativeField(narrative, "executiveSummary"); summary != "" {
		y = AddParagraph(pdf, page, theme, summary, y)
	} else if input.FullReport != nil {
		y = AddParagraph(pdf, page, theme, *input.FullReport, y)
	}

	y = AddSectionHeading(pdf, page, theme, "Key Metrics", y+2)
	metricsGrid := buildInvestorMetrics(metrics)
	y = AddMetricsGrid(pdf, page, theme, metricsGrid, y)

	if analysis := getNarrativeField(narrative, "marketAnalysis"); analysis != "" {
		y = AddSectionHeading(pdf, page, theme, "Market Analysis", y+4)
		y = AddParagraph(pdf, page, theme, analysis, y)
	}
	if strategy := getNarrativeField(narrative, "investmentStrategy"); strategy != "" {
		y = AddSectionHeading(pdf, page, theme, "Investment Strategy", y+4)
		y = AddParagraph(pdf, page, theme, strategy, y)
	}
	if risks := getNarrativeField(narrative, "riskAnalysis"); risks != "" {
		y = AddSectionHeading(pdf, page, theme, "Risk Analysis", y+4)
		y = AddParagraph(pdf, page, theme, risks, y)
	}
	if recommendations := getNarrativeList(narrative, "recommendations"); len(recommendations) > 0 {
		y = AddSectionHeading(pdf, page, theme, "Recommendations", y+4)
		y = AddBulletList(pdf, page, theme, recommendations, y)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildInvestorMetrics(metrics map[string]interface{}) []MetricItem {
	items := []MetricItem{}
	add := func(label, key, suffix string, highlight bool) {
		if val, ok := metrics[key]; ok {
			items = append(items, MetricItem{
				Label:     label,
				Value:     formatValue(val, suffix),
				Highlight: highlight,
			})
		}
	}
	add("Median Home Price", "medianHomePrice", "$", true)
	add("Median Rent", "medianRent", "$", false)
	add("Cap Rate", "capRate", "%", true)
	add("Cash-on-Cash", "cashOnCashReturn", "%", false)
	add("Annual Cash Flow", "annualCashFlow", "$", false)
	add("Price-to-Rent", "priceToRentRatio", "x", false)
	return items
}
