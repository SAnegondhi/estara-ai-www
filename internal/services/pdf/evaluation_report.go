package pdf

import (
	"bytes"
	"fmt"
	"time"
)

// EvaluationReportPDFRequest holds evaluation report generation data.
type EvaluationReportPDFRequest struct {
	ReportID    string              `json:"reportId"`
	Evaluations []EvaluationForPDF  `json:"evaluations"`
	GeneratedAt string              `json:"generatedAt,omitempty"`
	User        *PDFUser            `json:"user,omitempty"`
}

type EvaluationForPDF struct {
	ID         string                    `json:"id"`
	Property   EvaluationPropertyPDF     `json:"property"`
	Assumptions EvaluationAssumptionsPDF `json:"assumptions"`
	Scenarios  EvaluationScenariosPDF    `json:"scenarios"`
}

type EvaluationPropertyPDF struct {
	Address string  `json:"address"`
	City    string  `json:"city"`
	State   string  `json:"state"`
	ZipCode string  `json:"zipCode,omitempty"`
	Beds    int     `json:"beds,omitempty"`
	Baths   float64 `json:"baths,omitempty"`
	Sqft    int     `json:"sqft,omitempty"`
}

type EvaluationAssumptionsPDF struct {
	PurchasePrice   float64 `json:"purchasePrice"`
	DownPaymentPct  float64 `json:"downPaymentPct"`
	InterestRate    float64 `json:"interestRate"`
	LoanTermYears   int     `json:"loanTermYears"`
	MonthlyRent     float64 `json:"monthlyRent"`
	VacancyRatePct  float64 `json:"vacancyRatePct"`
	MaintenanceCost float64 `json:"maintenanceCost"`
	PropertyTax     float64 `json:"propertyTax"`
	Insurance       float64 `json:"insurance"`
	HoaFees         *float64 `json:"hoaFees,omitempty"`
	AppreciationRate float64 `json:"appreciationRate"`
}

type EvaluationScenariosPDF struct {
	Conservative EvaluationScenarioPDF `json:"conservative"`
	Base         EvaluationScenarioPDF `json:"base"`
	Optimistic   EvaluationScenarioPDF `json:"optimistic"`
}

type EvaluationScenarioPDF struct {
	Name        string                   `json:"name"`
	Assumptions ScenarioAssumptionsPDF   `json:"assumptions"`
	Metrics     ScenarioMetricsPDF       `json:"metrics"`
	YearlyProjections []ScenarioYearlyPDF `json:"yearlyProjections"`
}

type ScenarioAssumptionsPDF struct {
	AppreciationRate float64 `json:"appreciationRate"`
	VacancyRate      float64 `json:"vacancyRate"`
	RentGrowthRate   float64 `json:"rentGrowthRate"`
}

type ScenarioMetricsPDF struct {
	MonthlyPayment float64 `json:"monthlyPayment"`
	MonthlyCashFlow float64 `json:"monthlyCashFlow"`
	AnnualCashFlow  float64 `json:"annualCashFlow"`
	CashOnCash      float64 `json:"cashOnCash"`
	CapRate         float64 `json:"capRate"`
	TotalReturn5Yr  float64 `json:"totalReturn5Yr"`
	Irr5Yr          float64 `json:"irr5Yr"`
	BreakEvenMonths float64 `json:"breakEvenMonths"`
}

type ScenarioYearlyPDF struct {
	Year           int     `json:"year"`
	PropertyValue  float64 `json:"propertyValue"`
	Equity         float64 `json:"equity"`
	AnnualCashFlow float64 `json:"annualCashFlow"`
	CumulativeCashFlow float64 `json:"cumulativeCashFlow"`
	TotalReturn    float64 `json:"totalReturn"`
}

// BuildEvaluationReportPDF generates the evaluation report PDF.
func BuildEvaluationReportPDF(req EvaluationReportPDFRequest) ([]byte, error) {
	pdf := NewPDF("P", "mm", "A4")
	page := A4Page
	theme := DefaultTheme

	coverName := ""
	if req.User != nil {
		if req.User.FirstName != "" || req.User.LastName != "" {
			coverName = fmt.Sprintf("%s %s", req.User.FirstName, req.User.LastName)
		}
	}
	AddCoverPage(pdf, page, theme, "Property Evaluation Report", "Scenario Analysis", "Portfolio", coverName)
	AddHeaderFooter(pdf, page, theme, "Evaluation Report")

	pdf.AddPage()
	y := page.MarginTop
	y = AddSectionHeading(pdf, page, theme, "Report Summary", y)
	generatedAt := req.GeneratedAt
	if generatedAt == "" {
		generatedAt = time.Now().Format(time.RFC3339)
	}
	summary := []MetricItem{
		{Label: "Report ID", Value: req.ReportID},
		{Label: "Evaluations", Value: fmt.Sprintf("%d", len(req.Evaluations))},
		{Label: "Generated At", Value: generatedAt},
	}
	y = AddMetricsGrid(pdf, page, theme, summary, y)

	for idx, eval := range req.Evaluations {
		pdf.AddPage()
		y = page.MarginTop
		title := fmt.Sprintf("Property %d", idx+1)
		y = AddSectionHeading(pdf, page, theme, title, y)

		property := eval.Property
		address := fmt.Sprintf("%s, %s %s", property.Address, property.City, property.State)
		y = AddParagraph(pdf, page, theme, address, y)

		props := []MetricItem{
			{Label: "Beds/Baths", Value: fmt.Sprintf("%d / %.1f", property.Beds, property.Baths)},
			{Label: "Sqft", Value: fmt.Sprintf("%d", property.Sqft)},
			{Label: "Purchase Price", Value: formatCurrency(eval.Assumptions.PurchasePrice)},
			{Label: "Monthly Rent", Value: formatCurrency(eval.Assumptions.MonthlyRent)},
		}
		y = AddMetricsGrid(pdf, page, theme, props, y)

		y = AddSectionHeading(pdf, page, theme, "Assumptions", y)
		assumptionCols := []TableColumn{
			{Header: "Metric", Width: 60},
			{Header: "Value", Width: 60},
			{Header: "Metric", Width: 60},
			{Header: "Value", Width: 60},
		}
		assumptionRows := [][]string{
			{"Down Payment", formatPercent(eval.Assumptions.DownPaymentPct), "Interest Rate", formatPercent(eval.Assumptions.InterestRate)},
			{"Loan Term (yrs)", fmt.Sprintf("%d", eval.Assumptions.LoanTermYears), "Vacancy Rate", formatPercent(eval.Assumptions.VacancyRatePct)},
			{"Maintenance", formatCurrency(eval.Assumptions.MaintenanceCost), "Property Tax", formatCurrency(eval.Assumptions.PropertyTax)},
			{"Insurance", formatCurrency(eval.Assumptions.Insurance), "HOA Fees", formatCurrency(ptrOrZero(eval.Assumptions.HoaFees))},
			{"Appreciation", formatPercent(eval.Assumptions.AppreciationRate), "", ""},
		}
		y = AddTable(pdf, page, theme, assumptionCols, assumptionRows, y)

		scenarioSections := []struct {
			Title string
			Data  EvaluationScenarioPDF
		}{
			{"Conservative Scenario", eval.Scenarios.Conservative},
			{"Base Scenario", eval.Scenarios.Base},
			{"Optimistic Scenario", eval.Scenarios.Optimistic},
		}

		for _, section := range scenarioSections {
			y = AddSectionHeading(pdf, page, theme, section.Title, y+4)
			metricCols := []TableColumn{
				{Header: "Metric", Width: 70},
				{Header: "Value", Width: 70},
				{Header: "Metric", Width: 70},
				{Header: "Value", Width: 70},
			}
			metricRows := [][]string{
				{"Monthly Payment", formatCurrency(section.Data.Metrics.MonthlyPayment), "Monthly Cash Flow", formatCurrency(section.Data.Metrics.MonthlyCashFlow)},
				{"Annual Cash Flow", formatCurrency(section.Data.Metrics.AnnualCashFlow), "Cash-on-Cash", formatPercent(section.Data.Metrics.CashOnCash)},
				{"Cap Rate", formatPercent(section.Data.Metrics.CapRate), "Total Return (5Y)", formatPercent(section.Data.Metrics.TotalReturn5Yr)},
				{"IRR (5Y)", formatPercent(section.Data.Metrics.Irr5Yr), "Break-even (mo)", fmt.Sprintf("%.0f", section.Data.Metrics.BreakEvenMonths)},
			}
			y = AddTable(pdf, page, theme, metricCols, metricRows, y)

			if len(section.Data.YearlyProjections) > 0 {
				y = AddSectionHeading(pdf, page, theme, "Yearly Projections", y+2)
				cols := []TableColumn{
					{Header: "Year", Width: 18},
					{Header: "Value", Width: 40},
					{Header: "Equity", Width: 40},
					{Header: "Cash Flow", Width: 40},
					{Header: "Total Return", Width: 40},
				}
				rows := make([][]string, 0, len(section.Data.YearlyProjections))
				for _, proj := range section.Data.YearlyProjections {
					rows = append(rows, []string{
						fmt.Sprintf("%d", proj.Year),
						formatCurrency(proj.PropertyValue),
						formatCurrency(proj.Equity),
						formatCurrency(proj.AnnualCashFlow),
						formatCurrency(proj.TotalReturn),
					})
				}
				y = AddTable(pdf, page, theme, cols, rows, y)
			}
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ptrOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
