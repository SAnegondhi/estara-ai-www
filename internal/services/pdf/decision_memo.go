package pdf

import (
	"fmt"

	"github.com/phpdave11/gofpdf"

	"github.com/estara-ai/www/internal/services/memo"
)

// DecisionMemoBuilder generates PDF documents for Investment Decision Memos.
type DecisionMemoBuilder struct{}

// NewDecisionMemoBuilder creates a new builder.
func NewDecisionMemoBuilder() *DecisionMemoBuilder {
	return &DecisionMemoBuilder{}
}

// Build generates a multi-property Decision Memo PDF.
func (b *DecisionMemoBuilder) Build(memos []memo.MemoData) ([]byte, error) {
	if len(memos) == 0 {
		return nil, fmt.Errorf("no memos provided")
	}

	page := LetterPage
	theme := DefaultTheme

	pdfDoc := NewPDF("P", "mm", "Letter")

	// Cover page
	market := fmt.Sprintf("%s, %s", memos[0].Property.City, memos[0].Property.State)
	AddCoverPage(pdfDoc, page, theme, "Investment Decision Memo", fmt.Sprintf("%d Properties", len(memos)), market, "")
	AddHeaderFooter(pdfDoc, page, theme, "Decision Memo")

	// Generate pages per property
	for i, m := range memos {
		b.addPropertyPages(pdfDoc, page, theme, m, i+1, len(memos))
	}

	// Disclaimer page
	b.addDisclaimerPage(pdfDoc, page, theme)

	var buf []byte
	writer := &pdfBuffer{data: &buf}
	if err := pdfDoc.OutputAndClose(writer); err != nil {
		return nil, fmt.Errorf("failed to generate PDF: %w", err)
	}
	return buf, nil
}

func (b *DecisionMemoBuilder) addPropertyPages(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme, m memo.MemoData, num, total int) {
	pdfDoc.AddPage()
	y := page.MarginTop

	// Property header
	pdfDoc.SetTextColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
	pdfDoc.SetFont("Helvetica", "B", 14)
	pdfDoc.Text(page.MarginLeft, y, fmt.Sprintf("Property %d of %d", num, total))
	y += 7

	pdfDoc.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	pdfDoc.SetFont("Helvetica", "B", 12)
	pdfDoc.Text(page.MarginLeft, y, m.PropertyAddress)
	y += 5

	pdfDoc.SetFont("Helvetica", "", 9)
	pdfDoc.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
	pdfDoc.Text(page.MarginLeft, y, m.PropertySubtitle)
	y += 8

	// Status row
	y = b.addStatusRow(pdfDoc, page, theme, m, y)

	// Executive Summary
	if m.ExecutiveSummary != "" {
		y = AddSectionHeading(pdfDoc, page, theme, "Executive Summary", y)
		y = AddParagraph(pdfDoc, page, theme, m.ExecutiveSummary, y)
	}

	// Key Financials
	y = AddSectionHeading(pdfDoc, page, theme, "Key Financials", y)
	y = b.addFinancialsGrid(pdfDoc, page, theme, m.KeyFinancials, y)

	// Underwriting Assumptions
	if len(m.Assumptions) > 0 {
		y = AddSectionHeading(pdfDoc, page, theme, "Underwriting Assumptions", y)
		y = b.addAssumptionsTable(pdfDoc, page, theme, m.Assumptions, y)
	}

	// Cash Flow Projections
	if len(m.CashFlowProjections) > 0 {
		y, _ = EnsureSpace(pdfDoc, page, y, 60)
		y = AddSectionHeading(pdfDoc, page, theme, "10-Year Cash Flow Projection", y)
		y = b.addCashFlowTable(pdfDoc, page, theme, m.CashFlowProjections, y)
	}

	// Scenario Stress Testing
	if len(m.Scenarios) > 0 {
		y, _ = EnsureSpace(pdfDoc, page, y, 40)
		y = AddSectionHeading(pdfDoc, page, theme, "Scenario Stress Testing", y)
		y = b.addScenarioTable(pdfDoc, page, theme, m.Scenarios, y)
	}

	// Risk Assessment
	if len(m.RiskFactors) > 0 {
		y, _ = EnsureSpace(pdfDoc, page, y, 40)
		y = AddSectionHeading(pdfDoc, page, theme, "Risk Assessment", y)
		y = b.addRiskTable(pdfDoc, page, theme, m.RiskFactors, y)
		if m.KeyUncertainty != "" {
			y = AddParagraph(pdfDoc, page, theme, "Key Uncertainty: "+m.KeyUncertainty, y)
		}
	}

	// Comparables
	if m.Comparables != nil && m.Comparables.Sales != nil {
		y, _ = EnsureSpace(pdfDoc, page, y, 40)
		y = AddSectionHeading(pdfDoc, page, theme, "Comparable Sales", y)
		y = b.addCompsTable(pdfDoc, page, theme, m.Comparables, y)
	} else {
		y, _ = EnsureSpace(pdfDoc, page, y, 20)
		y = AddSectionHeading(pdfDoc, page, theme, "Comparable Sales", y)
		y = AddParagraph(pdfDoc, page, theme, "Comparable sales data is not yet available for this property. This section will be populated when a geo-spatial data provider is configured.", y)
	}

	// Portfolio Impact
	if m.PortfolioImpact != nil {
		y, _ = EnsureSpace(pdfDoc, page, y, 40)
		y = AddSectionHeading(pdfDoc, page, theme, "Portfolio Impact", y)
		y = b.addPortfolioImpactTable(pdfDoc, page, theme, m.PortfolioImpact, y)
	}

	// Decision Context
	if m.DecisionContext.PrimaryQuestion != "" {
		y, _ = EnsureSpace(pdfDoc, page, y, 40)
		y = AddSectionHeading(pdfDoc, page, theme, "Decision Context", y)
		y = AddParagraph(pdfDoc, page, theme, "Primary Question: "+m.DecisionContext.PrimaryQuestion, y)
		if m.DecisionContext.Tradeoffs != "" {
			y = AddParagraph(pdfDoc, page, theme, "Trade-offs: "+m.DecisionContext.Tradeoffs, y)
		}
		if m.DecisionContext.PipelineAlternatives != "" {
			_ = AddParagraph(pdfDoc, page, theme, "Pipeline: "+m.DecisionContext.PipelineAlternatives, y)
		}
	}
}

func (b *DecisionMemoBuilder) addStatusRow(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme, m memo.MemoData, y float64) float64 {
	contentWidth := page.Width - page.MarginLeft - page.MarginRight
	cellWidth := contentWidth / 6
	cellHeight := 12.0

	// Background
	pdfDoc.SetFillColor(theme.Light.R, theme.Light.G, theme.Light.B)
	pdfDoc.Rect(page.MarginLeft, y, contentWidth, cellHeight, "F")

	items := []struct {
		Label string
		Value string
	}{
		{"Status", m.Status},
		{"Risk", m.RiskProfile},
		{"Strategy Fit", fmt.Sprintf("%d%%", m.StrategyFit)},
		{"Confidence", fmt.Sprintf("%d%%", m.Confidence)},
		{"Capital Role", m.CapitalRole},
		{"Rank", m.RelativeRank},
	}

	x := page.MarginLeft
	for _, item := range items {
		pdfDoc.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
		pdfDoc.SetFont("Helvetica", "", 6)
		pdfDoc.Text(x+2, y+4, item.Label)

		pdfDoc.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
		pdfDoc.SetFont("Helvetica", "B", 8)
		pdfDoc.Text(x+2, y+9, item.Value)

		x += cellWidth
	}

	return y + cellHeight + 4
}

func (b *DecisionMemoBuilder) addFinancialsGrid(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme, kf memo.KeyFinancials, y float64) float64 {
	metrics := []MetricItem{
		{Label: "Purchase Price", Value: fmt.Sprintf("$%s", formatNum(kf.PurchasePrice))},
		{Label: "Down Payment (25%)", Value: fmt.Sprintf("$%s", formatNum(kf.DownPayment))},
		{Label: "Monthly Rent", Value: fmt.Sprintf("$%s/mo", formatNum(kf.MonthlyRent))},
		{Label: "Monthly Payment", Value: fmt.Sprintf("$%.0f/mo", kf.MonthlyPayment)},
		{Label: "Cap Rate", Value: fmt.Sprintf("%.2f%%", kf.CapRate), Highlight: true},
		{Label: "Cash-on-Cash (Yr 1)", Value: fmt.Sprintf("%.2f%%", kf.CashOnCashYr1), Highlight: true},
		{Label: "DSCR", Value: fmt.Sprintf("%.2f", kf.DSCR), Highlight: true},
		{Label: "5-Year IRR", Value: fmt.Sprintf("%.1f%%", kf.IRR5Yr)},
		{Label: "10-Year Equity Multiple", Value: fmt.Sprintf("%.2fx", kf.EquityMultiple10)},
		{Label: "NOI", Value: fmt.Sprintf("$%s", formatNum(int(kf.NOI)))},
	}
	return AddMetricsGrid(pdfDoc, page, theme, metrics, y)
}

func (b *DecisionMemoBuilder) addAssumptionsTable(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme, assumptions []memo.AssumptionRow, y float64) float64 {
	headers := []string{"Parameter", "Assumption", "Market Avg", "Variance"}
	rows := make([][]string, len(assumptions))
	for i, a := range assumptions {
		rows[i] = []string{a.Parameter, a.Assumption, a.MarketAvg, a.Variance}
	}
	return AddComparisonTable(pdfDoc, page, theme, "", headers, rows, y)
}

func (b *DecisionMemoBuilder) addCashFlowTable(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme, projections []memo.YearCashFlow, y float64) float64 {
	cols := []TableColumn{
		{Header: "Year", Width: 15},
		{Header: "Gross Rent", Width: 25, Align: "R"},
		{Header: "Expenses", Width: 25, Align: "R"},
		{Header: "NOI", Width: 22, Align: "R"},
		{Header: "Debt Service", Width: 25, Align: "R"},
		{Header: "Net CF", Width: 22, Align: "R"},
		{Header: "Property Value", Width: 28, Align: "R"},
		{Header: "Equity", Width: 25, Align: "R"},
	}

	// Only show select years to fit on page
	showYears := []int{0, 1, 2, 4, 9} // Yr 1, 2, 3, 5, 10
	rows := make([][]string, 0)
	for _, idx := range showYears {
		if idx < len(projections) {
			p := projections[idx]
			rows = append(rows, []string{
				fmt.Sprintf("%d", p.Year),
				fmt.Sprintf("$%s", formatNum(int(p.GrossRent))),
				fmt.Sprintf("$%s", formatNum(int(p.Expenses))),
				fmt.Sprintf("$%s", formatNum(int(p.NOI))),
				fmt.Sprintf("$%s", formatNum(int(p.DebtService))),
				fmt.Sprintf("$%s", formatNum(int(p.NetCashFlow))),
				fmt.Sprintf("$%s", formatNum(int(p.PropertyValue))),
				fmt.Sprintf("$%s", formatNum(int(p.Equity))),
			})
		}
	}

	return AddTable(pdfDoc, page, theme, cols, rows, y)
}

func (b *DecisionMemoBuilder) addScenarioTable(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme, scenarios []memo.ScenarioRow, y float64) float64 {
	headers := []string{"Scenario", "IRR", "Cash-on-Cash", "Equity Multiple", "DSCR", "Risk Level"}
	rows := make([][]string, len(scenarios))
	for i, s := range scenarios {
		rows[i] = []string{
			s.Name,
			fmt.Sprintf("%.1f%%", s.IRR),
			fmt.Sprintf("%.1f%%", s.CashOnCash),
			fmt.Sprintf("%.2fx", s.EquityMultiple),
			fmt.Sprintf("%.2f", s.DSCR),
			s.RiskLevel,
		}
	}
	return AddComparisonTable(pdfDoc, page, theme, "", headers, rows, y)
}

func (b *DecisionMemoBuilder) addRiskTable(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme, risks []memo.RiskFactor, y float64) float64 {
	headers := []string{"Risk Factor", "Severity", "Detail"}
	rows := make([][]string, len(risks))
	for i, r := range risks {
		rows[i] = []string{r.Factor, r.Severity, r.Detail}
	}
	return AddComparisonTable(pdfDoc, page, theme, "", headers, rows, y)
}

func (b *DecisionMemoBuilder) addCompsTable(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme, comps *memo.ComparablesData, y float64) float64 {
	if comps.Sales == nil || len(comps.Sales.Sales) == 0 {
		return AddParagraph(pdfDoc, page, theme, "No comparable sales data available.", y)
	}

	headers := []string{"Address", "Sale Price", "$/sqft", "Beds", "Sqft", "Distance"}
	rows := make([][]string, len(comps.Sales.Sales))
	for i, s := range comps.Sales.Sales {
		rows[i] = []string{
			s.Address,
			fmt.Sprintf("$%s", formatNum(s.SalePrice)),
			fmt.Sprintf("$%.0f", s.PricePerSqft),
			fmt.Sprintf("%d", s.Beds),
			fmt.Sprintf("%s", formatNum(s.Sqft)),
			fmt.Sprintf("%.1f mi", s.Distance),
		}
	}
	return AddComparisonTable(pdfDoc, page, theme, "", headers, rows, y)
}

func (b *DecisionMemoBuilder) addPortfolioImpactTable(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme, impact *memo.PortfolioImpactData, y float64) float64 {
	headers := []string{"Dimension", "Current", "Post-Acquisition", "Threshold"}
	rows := make([][]string, len(impact.Rows))
	for i, r := range impact.Rows {
		rows[i] = []string{r.Dimension, r.Current, r.PostAcquisition, r.Threshold}
	}
	y = AddComparisonTable(pdfDoc, page, theme, "", headers, rows, y)

	if impact.DiversificationNote != "" {
		y = AddParagraph(pdfDoc, page, theme, impact.DiversificationNote, y)
	}
	if impact.ConcentrationWarning != "" {
		pdfDoc.SetTextColor(theme.Warning.R, theme.Warning.G, theme.Warning.B)
		y = AddParagraph(pdfDoc, page, theme, "Warning: "+impact.ConcentrationWarning, y)
		pdfDoc.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	}
	return y
}

func (b *DecisionMemoBuilder) addDisclaimerPage(pdfDoc *gofpdf.Fpdf, page PageConfig, theme Theme) {
	pdfDoc.AddPage()
	y := page.MarginTop

	y = AddSectionHeading(pdfDoc, page, theme, "Important Disclaimers", y)
	disclaimers := []string{
		"This Investment Decision Memo is for informational purposes only and does not constitute investment advice, a recommendation, or an offer to buy or sell any property.",
		"All projections, estimates, and analyses are based on assumptions that may not materialize. Past performance does not guarantee future results.",
		"Real estate investments involve significant risks including loss of principal, illiquidity, and market volatility. Investors should conduct their own due diligence.",
		"The financial calculations assume standard financing terms and may not reflect your specific lending situation. Consult with a mortgage professional for actual terms.",
		"Market data is sourced from third-party providers and may contain inaccuracies. Estara AI does not guarantee the accuracy or completeness of any data presented.",
		"This document is generated by an AI system and should be reviewed by qualified professionals before making any investment decisions.",
	}
	_ = AddBulletList(pdfDoc, page, theme, disclaimers, y)
}

// formatNum adds comma separators to an integer.
func formatNum(n int) string {
	neg := ""
	if n < 0 {
		neg = "-"
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return neg + s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return neg + string(result)
}

// pdfBuffer implements io.WriteCloser for gofpdf.OutputAndClose.
type pdfBuffer struct {
	data *[]byte
}

func (b *pdfBuffer) Write(p []byte) (int, error) {
	*b.data = append(*b.data, p...)
	return len(p), nil
}

func (b *pdfBuffer) Close() error {
	return nil
}
