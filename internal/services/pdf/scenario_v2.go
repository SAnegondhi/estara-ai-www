package pdf

// ADR-088 Phase 10: Scenario Planning v2.1 PDF Export
//
// Builds a multi-section "Efficient Frontier Decision Memo" PDF using the same
// DefaultTheme, typography, and layout helpers as the Investment Decision Memo
// (decision_memo.go) so both PDFs look like siblings from the same product.
//
// Sections:
//  1. Cover page
//  2. Decision verdict
//  3. Selected portfolio configuration summary
//  4. Property details table
//  5. Portfolio metrics & reinvestment plan
//  6. Probability fan chart (Monte Carlo)
//  7. Scenario comparison (Base / Upside / Stress)
//  8. Efficient frontier overview (all configs)
//  9. Monitoring signals
// 10. Disclaimers

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/phpdave11/gofpdf"

	"github.com/estara-ai/www/internal/services/investment"
)

// ScenarioV2PDFRequest is the input to ScenarioV2PDFBuilder.Build.
type ScenarioV2PDFRequest struct {
	// All frontier points — needed for the frontier scatter overview section.
	FrontierPoints []investment.FrontierPoint `json:"frontierPoints"`

	// SelectedConfigIndex picks which FrontierPoint to feature in the PDF (0-based).
	SelectedConfigIndex int `json:"selectedConfigIndex"`

	// Params supply cover page metadata (locations, budget, strategy).
	Params investment.InvestmentPlanningParams `json:"params"`

	// Profile supplies investor-level metadata (risk tolerance, strategy).
	Profile investment.InvestorProfile `json:"profile"`
}

// ScenarioV2PDFBuilder generates the Efficient Frontier Decision Memo PDF.
type ScenarioV2PDFBuilder struct {
	charts *QuickChartClient
	logger *slog.Logger
}

// NewScenarioV2PDFBuilder returns a new builder. Pass nil logger to use slog default.
func NewScenarioV2PDFBuilder(logger *slog.Logger) *ScenarioV2PDFBuilder {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScenarioV2PDFBuilder{
		charts: NewQuickChartClient(),
		logger: logger,
	}
}

// Build generates the PDF and returns the raw bytes.
//
// Layout mirrors the WorkspaceLayout screen — frontier overview first, then
// full property details for EVERY configuration, then deep-dive analytics
// (Monte Carlo, scenarios, verdict, reinvestment, signals) for the selected config.
func (b *ScenarioV2PDFBuilder) Build(ctx context.Context, req ScenarioV2PDFRequest) ([]byte, error) {
	if len(req.FrontierPoints) == 0 {
		return nil, fmt.Errorf("no frontier points provided")
	}

	// Resolve selected config
	selectedIdx := req.SelectedConfigIndex
	if selectedIdx < 0 || selectedIdx >= len(req.FrontierPoints) {
		selectedIdx = 0
	}
	selected := &req.FrontierPoints[selectedIdx]

	// Pre-render charts (non-fatal on failure — PDF still generated without them)
	charts, err := GenerateFrontierCharts(ctx, b.charts, req.FrontierPoints, selected)
	if err != nil {
		b.logger.Warn("frontier chart generation error (continuing without charts)", "error", err)
		charts = &ScenarioV2Charts{}
	}

	// ── PDF document setup ────────────────────────────────────────────────────
	pdf := NewPDF("P", "mm", "Letter")
	page := LetterPage
	theme := DefaultTheme

	markets := strings.Join(req.Params.Locations, " - ")
	if markets == "" {
		markets = "Multi-Market Portfolio"
	}

	// Section 1: Cover
	AddCoverPage(pdf, page, theme,
		"Frontier Analysis Report",
		fmt.Sprintf("%d configurations | %d properties analyzed",
			len(req.FrontierPoints), s2totalProperties(req.FrontierPoints)),
		sanitizeForPDF(markets),
		"Estara Insight | AI-Generated Analysis | Decision Support Only | Not Investment Advice | CONFIDENTIAL",
	)
	AddHeaderFooter(pdf, page, theme, "Frontier Analysis Report - Not Investment Advice")

	// Section 2: Efficient Frontier Overview (mirrors the Frontier tab on screen)
	pdf.AddPage()
	y := page.MarginTop
	y = b.sectionFrontierOverview(pdf, page, theme, req.FrontierPoints, selected, charts.FrontierPlot, y)

	// Section 3–N: Full configuration details for EVERY frontier point
	// (mirrors clicking each config in the Portfolio tab)
	for i := range req.FrontierPoints {
		pt := &req.FrontierPoints[i]
		y, _ = EnsureSpace(pdf, page, y, 50)
		y = b.sectionConfigDetail(pdf, page, theme, pt, selected, y)
	}

	// ── Selected configuration deep-dive analytics ────────────────────────────
	// These sections mirror the remaining tabs visible on screen for the selected config.

	// Decision Verdict (Verdict tab)
	y, _ = EnsureSpace(pdf, page, y, 50)
	y = b.sectionVerdict(pdf, page, theme, selected, y)

	// Portfolio Metrics & Reinvestment Plan (supplements Portfolio tab)
	y, _ = EnsureSpace(pdf, page, y, 50)
	y = b.sectionPortfolioMetrics(pdf, page, theme, selected, req.Params, req.Profile, y)

	// Monte Carlo Probability Fan (Monte Carlo tab)
	if charts.ProbabilityFan != "" {
		y, _ = EnsureSpace(pdf, page, y, 110)
		y = b.sectionProbabilityFan(pdf, page, theme, selected, charts.ProbabilityFan, y)
	} else if selected.SimulationResults != nil && selected.SimulationResults.Percentiles != nil {
		y, _ = EnsureSpace(pdf, page, y, 50)
		y = b.sectionProbabilityTable(pdf, page, theme, selected, y)
	}

	// Scenario Comparison — Base / Upside / Stress (Scenarios tab)
	if selected.Scenarios != nil {
		y, _ = EnsureSpace(pdf, page, y, 50)
		y = b.sectionScenarios(pdf, page, theme, selected, y)
	}

	// Monitoring Signals (Verdict tab sidebar)
	if selected.DecisionVerdict != nil && len(selected.DecisionVerdict.MonitoringSignals) > 0 {
		y, _ = EnsureSpace(pdf, page, y, 50)
		b.sectionMonitoringSignals(pdf, page, theme, selected.DecisionVerdict.MonitoringSignals, y)
	}

	// Disclaimers
	b.sectionDisclaimers(pdf, page, theme)

	// ── Output ────────────────────────────────────────────────────────────────
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdf output: %w", err)
	}
	return buf.Bytes(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Section renderers
// ─────────────────────────────────────────────────────────────────────────────

// sectionVerdict renders the PROCEED / PAUSE / REVISE decision block.
func (b *ScenarioV2PDFBuilder) sectionVerdict(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, pt *investment.FrontierPoint, y float64) float64 {
	y = AddSectionHeading(pdf, page, theme, "Decision Verdict", y)

	if pt.DecisionVerdict == nil {
		return AddParagraph(pdf, page, theme, "Decision verdict not available for this configuration.", y)
	}

	dv := pt.DecisionVerdict

	// Recommendation badge
	var badgeFill Color
	switch dv.Recommendation {
	case investment.DecisionProceed:
		badgeFill = theme.Accent
	case investment.DecisionPause:
		badgeFill = theme.Warning
	default: // REVISE
		badgeFill = Color{R: 220, G: 38, B: 38}
	}

	contentW := page.Width - page.MarginLeft - page.MarginRight
	pdf.SetFillColor(badgeFill.R, badgeFill.G, badgeFill.B)
	pdf.Rect(page.MarginLeft, y, contentW, 12, "F")
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont("Helvetica", "B", 11)
	label := "ANALYSIS OUTPUT: " + string(dv.Recommendation)
	labelW := pdf.GetStringWidth(label)
	pdf.Text((page.Width-labelW)/2, y+8.5, label)
	y += 16

	// Sub-label: not a recommendation
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
	subLabel := "This is a model output, not a professional investment recommendation. Consult a licensed advisor before acting."
	pdf.MultiCell(page.Width-page.MarginLeft-page.MarginRight, 4, subLabel, "", "C", false)
	pdf.SetXY(page.MarginLeft, pdf.GetY()+2)
	y = pdf.GetY()

	// Rationale
	y = AddParagraph(pdf, page, theme, sanitizeForPDF(dv.DecisionRationale), y)
	y += 3

	// Key conditions
	if len(dv.KeyConditions) > 0 {
		sanitized := make([]string, len(dv.KeyConditions))
		for i, c := range dv.KeyConditions {
			sanitized[i] = sanitizeForPDF(c)
		}
		pdf.SetFont("Helvetica", "B", 9)
		pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
		pdf.Text(page.MarginLeft, y, "Key Assumptions Underlying This Analysis:")
		y += 5
		y = AddBulletList(pdf, page, theme, sanitized, y)
	}

	return y + 2
}

// sectionConfigDetail renders a full configuration block: metrics + property table + theses.
// Shown for EVERY frontier point so the reader can compare all options side by side.
func (b *ScenarioV2PDFBuilder) sectionConfigDetail(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, pt *investment.FrontierPoint, selected *investment.FrontierPoint, y float64) float64 {
	label := fmt.Sprintf("Configuration %d", pt.ConfigIndex+1)
	if selected != nil && pt.ConfigIndex == selected.ConfigIndex {
		label += " (Selected)"
	}
	y = AddSectionHeading(pdf, page, theme, label, y)

	metrics := []MetricItem{
		{Label: "Properties in Portfolio", Value: fmt.Sprintf("%d", len(pt.Properties))},
		{Label: "Modeled Annual Return (Estimated)", Value: fmt.Sprintf("%.2f%%", pt.ExpectedReturn), Highlight: true},
		{Label: "Portfolio Volatility (Modeled)", Value: fmt.Sprintf("%.2f%%", pt.PortfolioVolatility)},
		{Label: "Sharpe Score (Risk-Adjusted, Modeled)", Value: fmt.Sprintf("%.3f", pt.SharpeScore), Highlight: true},
		{Label: "Concentration Index (HHI)", Value: fmt.Sprintf("%.3f", pt.ConcentrationIndex)},
		{Label: "Stress Test Equity - Year 10 (Estimated)", Value: "$" + formatNum(pt.StressTestEquity)},
		{Label: "Market Exposure", Value: s2marketExposure(pt.Properties)},
	}
	y = AddMetricsGrid(pdf, page, theme, metrics, y)
	y += 2

	// Property details
	y, _ = EnsureSpace(pdf, page, y, 20)
	y = b.sectionPropertyDetails(pdf, page, theme, pt, y)
	return y
}

// sectionPropertyDetails renders a compact summary table + per-property detail rows.
func (b *ScenarioV2PDFBuilder) sectionPropertyDetails(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, pt *investment.FrontierPoint, y float64) float64 {
	y = AddSectionHeading(pdf, page, theme, "Property Details", y)

	contentW := page.Width - page.MarginLeft - page.MarginRight
	addrW := 45.0
	colW := (contentW - addrW) / 6.0

	cols := []TableColumn{
		{Header: "Address", Width: addrW, Align: "L"},
		{Header: "Price", Width: colW, Align: "R"},
		{Header: "Est. Cap Rate", Width: colW, Align: "R"},
		{Header: "Est. CoC", Width: colW, Align: "R"},
		{Header: "Est. DSCR", Width: colW, Align: "R"},
		{Header: "Est. CF/mo", Width: colW, Align: "R"},
		{Header: "Score", Width: colW, Align: "R"},
	}

	rows := make([][]string, len(pt.Properties))
	for i, prop := range pt.Properties {
		addr := prop.Property.Address
		if len(addr) > 24 {
			addr = addr[:24] + "..."
		}
		scoreStr := fmt.Sprintf("%.0f", prop.Score)
		if prop.Score == 0 {
			scoreStr = "N/A"
		}
		rows[i] = []string{
			addr,
			"$" + formatNum(prop.Property.Price),
			fmt.Sprintf("%.2f%%", prop.CapRate),
			fmt.Sprintf("%.2f%%", prop.CashOnCash),
			fmt.Sprintf("%.2f", prop.DSCR),
			"$" + formatNum(prop.MonthlyCashFlow),
			scoreStr,
		}
	}

	y = AddTable(pdf, page, theme, cols, rows, y) + 4

	// Per-property thesis summaries (if any property has AI thesis data)
	for _, prop := range pt.Properties {
		if prop.Thesis == nil || prop.Thesis.InvestmentThesis == "" {
			continue
		}

		y, _ = EnsureSpace(pdf, page, y, 22)

		// Property header
		addr := prop.Property.Address + ", " + prop.Property.City + ", " + prop.Property.State
		if len(addr) > 80 {
			addr = addr[:80] + "..."
		}
		pdf.SetFont("Helvetica", "B", 8)
		pdf.SetTextColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
		pdf.Text(page.MarginLeft, y, addr)
		y += 5

		// Investment thesis
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
		pdf.SetXY(page.MarginLeft, y)
		pdf.MultiCell(page.Width-page.MarginLeft-page.MarginRight, 4, sanitizeForPDF(prop.Thesis.InvestmentThesis), "", "L", false)
		y = pdf.GetY() + 2

		// Key strengths (up to 2)
		if len(prop.Thesis.KeyStrengths) > 0 {
			pdf.SetFont("Helvetica", "I", 7.5)
			pdf.SetTextColor(theme.Accent.R, theme.Accent.G, theme.Accent.B)
			maxStrengths := min(2, len(prop.Thesis.KeyStrengths))
			for _, s := range prop.Thesis.KeyStrengths[:maxStrengths] {
				y, _ = EnsureSpace(pdf, page, y, 5)
				pdf.SetXY(page.MarginLeft+3, y)
				pdf.MultiCell(page.Width-page.MarginLeft-page.MarginRight-3, 3.5, "[+] "+s, "", "L", false)
				y = pdf.GetY()
			}
			y += 2
		}

		// CapEx alert (if any)
		if prop.Thesis.CapexAlert != nil && *prop.Thesis.CapexAlert != "" {
			y, _ = EnsureSpace(pdf, page, y, 7)
			pdf.SetFont("Helvetica", "I", 7.5)
			pdf.SetTextColor(theme.Warning.R, theme.Warning.G, theme.Warning.B)
			pdf.SetXY(page.MarginLeft+3, y)
			pdf.MultiCell(page.Width-page.MarginLeft-page.MarginRight-3, 3.5, "[!] "+*prop.Thesis.CapexAlert, "", "L", false)
			y = pdf.GetY() + 2
		}
	}

	return y + 2
}

// sectionPortfolioMetrics renders reinvestment plan and investor parameters.
func (b *ScenarioV2PDFBuilder) sectionPortfolioMetrics(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, pt *investment.FrontierPoint, params investment.InvestmentPlanningParams, profile investment.InvestorProfile, y float64) float64 {
	y = AddSectionHeading(pdf, page, theme, "Portfolio Metrics & Reinvestment Plan", y)

	// Investor parameters line — use profile fields (always populated) for strategy/risk
	strategy := string(profile.Strategy)
	if strategy == "" {
		strategy = string(params.Strategy)
	}
	riskTol := string(profile.RiskTolerance)
	if riskTol == "" {
		riskTol = string(params.RiskTolerance)
	}
	profileLine := fmt.Sprintf("Strategy: %s  |  Risk: %s  |  Budget: $%s  |  Down Payment: %.0f%%",
		strategy, riskTol, formatNum(params.Budget), params.DownPaymentPct*100)
	y = AddParagraph(pdf, page, theme, profileLine, y)

	if pt.ReinvestmentPlan == nil {
		return y
	}

	rp := pt.ReinvestmentPlan

	// Track A: yearly budget schedule
	if len(rp.TrackA.YearlyBudgets) > 0 {
		y += 4
		pdf.SetFont("Helvetica", "B", 9)
		pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
		pdf.Text(page.MarginLeft, y, "Track A - External Capital Schedule")
		y += 6

		contentW := page.Width - page.MarginLeft - page.MarginRight
		trackACols := []TableColumn{
			{Header: "Year", Width: 20, Align: "R"},
			{Header: "Budget", Width: 35, Align: "R"},
			{Header: "Target Locations", Width: contentW - 55, Align: "L"},
		}
		trackARows := make([][]string, len(rp.TrackA.YearlyBudgets))
		for i, yb := range rp.TrackA.YearlyBudgets {
			locs := strings.Join(yb.Locations, ", ")
			if locs == "" {
				locs = "--"
			}
			trackARows[i] = []string{
				fmt.Sprintf("%d", yb.Year),
				"$" + formatNum(yb.Budget),
				locs,
			}
		}
		y = AddTable(pdf, page, theme, trackACols, trackARows, y)
		y += 2
	}

	// Track B: summary metrics
	y += 2
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	pdf.Text(page.MarginLeft, y, "Track B - Internal Cash Flow Reinvestment")
	y += 6

	trackBMetrics := []MetricItem{
		{Label: "Trigger Threshold", Value: "$" + formatNum(rp.TrackB.Threshold)},
		{Label: "Median Fire Year", Value: s2yearLabel(rp.TrackB.MedianFiredYear)},
		{Label: "Fire Probability", Value: fmt.Sprintf("%.1f%%", rp.TrackB.FiredProbability*100)},
		{Label: "Expected Acquisitions", Value: fmt.Sprintf("%d", rp.TrackB.ExpectedAcquisitions)},
	}
	return AddMetricsGrid(pdf, page, theme, trackBMetrics, y)
}

// sectionProbabilityFan embeds the fan chart image + summary table.
func (b *ScenarioV2PDFBuilder) sectionProbabilityFan(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, pt *investment.FrontierPoint, chartBase64 string, y float64) float64 {
	y = AddSectionHeading(pdf, page, theme, "Monte Carlo Probability Fan - Net Worth Projection", y)

	imgW := page.Width - page.MarginLeft - page.MarginRight
	imgH := 90.0
	name := fmt.Sprintf("fan_%d", pt.ConfigIndex)
	if err := AddImageFromBase64(pdf, name, chartBase64, page.MarginLeft, y, imgW, imgH); err != nil {
		b.logger.Warn("failed to embed fan chart", "error", err)
		y = AddParagraph(pdf, page, theme, "[Probability fan chart unavailable]", y)
	} else {
		y += imgH + 4
	}

	// Percentile summary table below chart
	if pt.SimulationResults != nil && pt.SimulationResults.Percentiles != nil {
		y = b.sectionProbabilityTable(pdf, page, theme, pt, y)
	}

	return y
}

// sectionProbabilityTable renders P10/P25/P50/P75/P90 net worth for key years.
func (b *ScenarioV2PDFBuilder) sectionProbabilityTable(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, pt *investment.FrontierPoint, y float64) float64 {
	pct := pt.SimulationResults.Percentiles
	if pct == nil {
		return y
	}

	y, _ = EnsureSpace(pdf, page, y, 30)

	wantYears := map[int]bool{1: true, 3: true, 5: true, 7: true, 10: true}
	contentW := page.Width - page.MarginLeft - page.MarginRight
	yearW := 16.0
	colW := (contentW - yearW) / 5.0

	cols := []TableColumn{
		{Header: "Year", Width: yearW, Align: "L"},
		{Header: "P10 (Downside)", Width: colW, Align: "R"},
		{Header: "P25", Width: colW, Align: "R"},
		{Header: "Median (P50)", Width: colW, Align: "R"},
		{Header: "P75", Width: colW, Align: "R"},
		{Header: "P90 (Upside)", Width: colW, Align: "R"},
	}

	p10m := s2yearMap(pct.P10)
	p25m := s2yearMap(pct.P25)
	p50m := s2yearMap(pct.P50)
	p75m := s2yearMap(pct.P75)
	p90m := s2yearMap(pct.P90)

	var rows [][]string
	for yr := 1; yr <= 10; yr++ {
		if !wantYears[yr] {
			continue
		}
		rows = append(rows, []string{
			fmt.Sprintf("Year %d", yr),
			"$" + formatNum(p10m[yr]),
			"$" + formatNum(p25m[yr]),
			"$" + formatNum(p50m[yr]),
			"$" + formatNum(p75m[yr]),
			"$" + formatNum(p90m[yr]),
		})
	}

	return AddTable(pdf, page, theme, cols, rows, y) + 2
}

// sectionScenarios renders the Base / Upside / Stress year-by-year comparison.
func (b *ScenarioV2PDFBuilder) sectionScenarios(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, pt *investment.FrontierPoint, y float64) float64 {
	y = AddSectionHeading(pdf, page, theme, "Scenario Comparison - Base / Upside / Stress", y)

	ss := pt.Scenarios
	if ss == nil {
		return y
	}

	// Final net worth summary
	metrics := []MetricItem{
		{Label: "Base Case - Year 10 Modeled Net Worth", Value: "$" + formatNum(ss.Base.FinalNetWorth), Highlight: true},
		{Label: "Upside Case - Year 10 Modeled Net Worth", Value: "$" + formatNum(ss.Upside.FinalNetWorth)},
		{Label: "Stress Case - Year 10 Modeled Net Worth", Value: "$" + formatNum(ss.Stress.FinalNetWorth)},
	}
	y = AddMetricsGrid(pdf, page, theme, metrics, y)

	// Year-by-year table (years 1, 3, 5, 7, 10)
	wantYears := map[int]bool{1: true, 3: true, 5: true, 7: true, 10: true}
	baseM := s2yearMap(ss.Base.YearlyOutcomes)
	upM := s2yearMap(ss.Upside.YearlyOutcomes)
	stM := s2yearMap(ss.Stress.YearlyOutcomes)

	contentW := page.Width - page.MarginLeft - page.MarginRight
	yearW := 16.0
	colW := (contentW - yearW) / 3.0
	cols := []TableColumn{
		{Header: "Year", Width: yearW, Align: "L"},
		{Header: "Base Net Worth", Width: colW, Align: "R"},
		{Header: "Upside Net Worth", Width: colW, Align: "R"},
		{Header: "Stress Net Worth", Width: colW, Align: "R"},
	}

	var rows [][]string
	for yr := 1; yr <= 10; yr++ {
		if !wantYears[yr] {
			continue
		}
		rows = append(rows, []string{
			fmt.Sprintf("Year %d", yr),
			"$" + formatNum(baseM[yr]),
			"$" + formatNum(upM[yr]),
			"$" + formatNum(stM[yr]),
		})
	}

	return AddTable(pdf, page, theme, cols, rows, y) + 2
}

// sectionFrontierOverview renders scatter chart + comparison table for all configs.
func (b *ScenarioV2PDFBuilder) sectionFrontierOverview(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, allPoints []investment.FrontierPoint, selected *investment.FrontierPoint, scatterBase64 string, y float64) float64 {
	y = AddSectionHeading(pdf, page, theme, "Efficient Frontier Overview - All Configurations", y)

	if scatterBase64 != "" {
		imgW := page.Width - page.MarginLeft - page.MarginRight
		imgH := 85.0
		y, _ = EnsureSpace(pdf, page, y, imgH+5)
		if err := AddImageFromBase64(pdf, "frontier_scatter", scatterBase64, page.MarginLeft, y, imgW, imgH); err != nil {
			b.logger.Warn("failed to embed scatter chart", "error", err)
		} else {
			y += imgH + 4
		}
	}

	y, _ = EnsureSpace(pdf, page, y, 40)
	contentW := page.Width - page.MarginLeft - page.MarginRight
	firstW := 18.0
	colW := (contentW - firstW) / 6.0
	cols := []TableColumn{
		{Header: "Config", Width: firstW, Align: "L"},
		{Header: "Props", Width: colW, Align: "R"},
		{Header: "Mod. Return", Width: colW, Align: "R"},
		{Header: "Volatility", Width: colW, Align: "R"},
		{Header: "Sharpe", Width: colW, Align: "R"},
		{Header: "HHI", Width: colW, Align: "R"},
		{Header: "Est. Stress Eq.", Width: colW, Align: "R"},
	}

	rows := make([][]string, len(allPoints))
	for i, pt := range allPoints {
		label := fmt.Sprintf("C%d", pt.ConfigIndex+1)
		if selected != nil && pt.ConfigIndex == selected.ConfigIndex {
			label += "*"
		}
		rows[i] = []string{
			label,
			fmt.Sprintf("%d", len(pt.Properties)),
			fmt.Sprintf("%.2f%%", pt.ExpectedReturn),
			fmt.Sprintf("%.2f%%", pt.PortfolioVolatility),
			fmt.Sprintf("%.3f", pt.SharpeScore),
			fmt.Sprintf("%.3f", pt.ConcentrationIndex),
			"$" + formatNum(pt.StressTestEquity),
		}
	}

	return AddTable(pdf, page, theme, cols, rows, y) + 2
}

// sectionMonitoringSignals renders the monitoring indicator threshold table.
func (b *ScenarioV2PDFBuilder) sectionMonitoringSignals(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, signals []investment.MonitoringSignal, y float64) float64 {
	y = AddSectionHeading(pdf, page, theme, "Monitoring Signals", y)

	intro := "Track these leading indicators if you act on this portfolio configuration. " +
		"Watch level signals are informational. Warn level prompts assumption review. Alert level warrants re-analysis with a licensed advisor."
	y = AddParagraph(pdf, page, theme, intro, y)

	contentW := page.Width - page.MarginLeft - page.MarginRight
	indW := 48.0
	metricW := (contentW - indW - 18) / 4.0
	cols := []TableColumn{
		{Header: "Indicator", Width: indW, Align: "L"},
		{Header: "Current", Width: metricW, Align: "R"},
		{Header: "Watch", Width: metricW, Align: "R"},
		{Header: "Warn", Width: metricW, Align: "R"},
		{Header: "Alert", Width: metricW, Align: "R"},
		{Header: "Status", Width: 18, Align: "L"},
	}

	rows := make([][]string, len(signals))
	for i, s := range signals {
		rows[i] = []string{
			s.Indicator,
			fmt.Sprintf("%.2f", s.CurrentValue),
			fmt.Sprintf("%.2f", s.WatchLevel),
			fmt.Sprintf("%.2f", s.WarnLevel),
			fmt.Sprintf("%.2f", s.AlertLevel),
			s2signalStatus(s),
		}
	}
	y = AddTable(pdf, page, theme, cols, rows, y)

	// Justification notes (italic, small)
	pdf.SetFont("Helvetica", "I", 7)
	pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
	for _, s := range signals {
		if s.Justification == "" {
			continue
		}
		note := fmt.Sprintf("- %s: %s", s.Indicator, s.Justification)
		lines := pdf.SplitLines([]byte(note), contentW)
		for _, line := range lines {
			y, _ = EnsureSpace(pdf, page, y, 4)
			pdf.Text(page.MarginLeft, y, string(line))
			y += 3.5
		}
	}

	return y + 4
}

// sectionDisclaimers adds the standard disclaimer page.
func (b *ScenarioV2PDFBuilder) sectionDisclaimers(pdf *gofpdf.Fpdf, page PageConfig, theme Theme) {
	pdf.AddPage()
	y := page.MarginTop
	y = AddSectionHeading(pdf, page, theme, "Important Disclaimers", y)

	items := []string{
		"This report is generated by artificial intelligence and is provided for informational and decision-support purposes only. It does not constitute investment advice, a professional recommendation, or an offer to buy or sell any property or security.",
		"Estara AI is not a licensed real estate broker, investment advisor, financial planner, or broker-dealer. Nothing in this report should be construed as advice from a licensed professional. Always consult licensed real estate professionals, financial advisors, and legal counsel before making any investment decisions.",
		"All projections, forecasts, and simulations are illustrative estimates based on third-party market data, mathematical models, and user-provided assumptions. Actual results will differ, potentially materially, from any projected results shown.",
		"Property financial metrics (cap rate, cash-on-cash return, DSCR, monthly cash flow) are model estimates derived from third-party data sources. They are not verified appraisals, inspections, or audited figures. Actual metrics depend on local market conditions, property condition, tenant quality, vacancy rates, and management effectiveness.",
		"Monte Carlo simulations model statistical uncertainty using assumed return distributions. They cannot predict actual market conditions, economic shocks, interest rate changes, local regulatory changes, natural disasters, or property-specific factors. The range of outcomes shown does not capture all possible scenarios.",
		"This analysis does not address tax implications of any investment, including depreciation, capital gains, 1031 exchanges, passive activity rules, or state and local tax obligations. Consult a qualified tax professional before making any investment decision.",
		"Past performance of real estate markets does not guarantee future results. All investments carry risk, including the potential loss of some or all of the invested principal.",
		"This report was generated on " + time.Now().UTC().Format("January 2, 2006") + " and reflects data and assumptions current as of that date. Market conditions change. This report may become outdated and should not be relied upon indefinitely.",
		"Estara AI and its affiliates assume no liability for decisions made in reliance on this report. Use of this report is subject to Estara AI's Terms of Service.",
	}

	AddBulletList(pdf, page, theme, items, y)
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers — prefixed s2 to avoid collision with decision_memo helpers
// ─────────────────────────────────────────────────────────────────────────────

// s2totalProperties returns the total unique property count across all configs.
// Used for cover page subtitle.
func s2totalProperties(pts []investment.FrontierPoint) int {
	// Use the config with the most properties as the representative count.
	max := 0
	for _, pt := range pts {
		if len(pt.Properties) > max {
			max = len(pt.Properties)
		}
	}
	return max
}

// s2yearLabel returns "Never" for 0, else "Year N".
func s2yearLabel(year int) string {
	if year == 0 {
		return "Never"
	}
	return fmt.Sprintf("Year %d", year)
}

// s2marketExposure returns a "City, ST · City, ST" summary of unique markets.
func s2marketExposure(props []investment.PropertyInPortfolio) string {
	seen := map[string]bool{}
	var markets []string
	for _, p := range props {
		key := p.Property.City + ", " + p.Property.State
		if !seen[key] {
			seen[key] = true
			markets = append(markets, key)
		}
	}
	if len(markets) == 0 {
		return "--"
	}
	return strings.Join(markets, " - ")
}

// s2yearMap indexes YearOutcome net worth by year for fast lookup.
func s2yearMap(outcomes []investment.YearOutcome) map[int]int {
	m := make(map[int]int, len(outcomes))
	for _, o := range outcomes {
		m[o.Year] = o.NetWorth
	}
	return m
}

// s2signalStatus derives OK / WATCH / WARN / ALERT from current vs thresholds.
// Direction is inferred from threshold ordering:
//   - AlertLevel < WatchLevel → "lower is bad" (e.g. cap rate, HPI index, expected return)
//   - AlertLevel > WatchLevel → "higher is bad" (e.g. unemployment rate, vacancy)
func s2signalStatus(s investment.MonitoringSignal) string {
	if s.AlertLevel < s.WatchLevel {
		// Lower values are worse: ALERT when current drops below AlertLevel
		switch {
		case s.CurrentValue <= s.AlertLevel:
			return "ALERT"
		case s.CurrentValue <= s.WarnLevel:
			return "WARN"
		case s.CurrentValue <= s.WatchLevel:
			return "WATCH"
		default:
			return "OK"
		}
	}
	// Higher values are worse: ALERT when current rises above AlertLevel
	switch {
	case s.CurrentValue >= s.AlertLevel:
		return "ALERT"
	case s.CurrentValue >= s.WarnLevel:
		return "WARN"
	case s.CurrentValue >= s.WatchLevel:
		return "WATCH"
	default:
		return "OK"
	}
}
