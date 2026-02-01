// Package analysis provides financial engineering services for investment planning.
// Part of ADR-059: Investment Planning Enhancement - Selection Mode
package analysis

import (
	"log/slog"
	"math"

	"github.com/estara-ai/www/internal/services/investment"
)

// ReinvestmentModeler models cash flow reinvestment scenarios
type ReinvestmentModeler struct {
	logger *slog.Logger
}

// NewReinvestmentModeler creates a new reinvestment modeler
func NewReinvestmentModeler(logger *slog.Logger) *ReinvestmentModeler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReinvestmentModeler{
		logger: logger.With("component", "reinvestment_modeler"),
	}
}

// ReinvestmentParams holds parameters for reinvestment modeling
type ReinvestmentParams struct {
	// Portfolio properties to model
	Properties []investment.PropertyInPortfolio

	// Reinvestment settings
	ReinvestmentRate float64 // 0-100 (percentage)
	ProjectionYears  int     // 1-10 years

	// Financial assumptions
	MortgageRate       float64 // Annual rate (e.g., 7.0 for 7%)
	DownPaymentPct     float64 // e.g., 0.20 for 20%
	AppreciationRate   float64 // Annual rate (e.g., 3.0 for 3%)
	RentGrowthRate     float64 // Annual rate (e.g., 2.0 for 2%)
	OperatingExpenses  float64 // As % of rent (e.g., 0.50 for 50%)
	MinDownPayment     int     // Minimum down payment for new acquisition
	AvgPropertyPrice   int     // Average price for acquisition simulation
	AvgPropertyRent    int     // Average rent for acquisition simulation
}

// DefaultReinvestmentParams returns default parameters
func DefaultReinvestmentParams() ReinvestmentParams {
	return ReinvestmentParams{
		ReinvestmentRate:  100,  // Reinvest all surplus
		ProjectionYears:   5,
		MortgageRate:      7.0,
		DownPaymentPct:    0.20,
		AppreciationRate:  3.0,
		RentGrowthRate:    2.0,
		OperatingExpenses: 0.50,
		MinDownPayment:    50000,
		AvgPropertyPrice:  300000,
		AvgPropertyRent:   2000,
	}
}

// ModelReinvestment projects portfolio growth with and without reinvestment
func (m *ReinvestmentModeler) ModelReinvestment(
	params ReinvestmentParams,
) (*investment.ReinvestmentAnalysis, error) {
	m.logger.Info("modeling reinvestment scenarios",
		"propertyCount", len(params.Properties),
		"reinvestmentRate", params.ReinvestmentRate,
		"projectionYears", params.ProjectionYears,
	)

	// Apply defaults if not set
	params = m.applyDefaults(params)

	// Calculate base scenario (no reinvestment)
	baseScenario := m.projectWithoutReinvestment(params)

	// Calculate reinvest scenario
	reinvestScenario := m.projectWithReinvestment(params)

	// Calculate cumulative differences
	cumulativeDiff := m.calculateCumulativeDiff(baseScenario, reinvestScenario, params.ProjectionYears)

	// Calculate compounded returns
	compoundedReturns := m.calculateCompoundedReturns(baseScenario, reinvestScenario)

	analysis := &investment.ReinvestmentAnalysis{
		Enabled:           params.ReinvestmentRate > 0,
		ReinvestmentRate:  params.ReinvestmentRate,
		ProjectionYears:   params.ProjectionYears,
		BaseScenario:      baseScenario,
		ReinvestScenario:  reinvestScenario,
		CumulativeDiff:    cumulativeDiff,
		CompoundedReturns: compoundedReturns,
	}

	return analysis, nil
}

// applyDefaults fills in default values for missing parameters
func (m *ReinvestmentModeler) applyDefaults(params ReinvestmentParams) ReinvestmentParams {
	defaults := DefaultReinvestmentParams()

	if params.ProjectionYears == 0 {
		params.ProjectionYears = defaults.ProjectionYears
	}
	if params.MortgageRate == 0 {
		params.MortgageRate = defaults.MortgageRate
	}
	if params.DownPaymentPct == 0 {
		params.DownPaymentPct = defaults.DownPaymentPct
	}
	if params.AppreciationRate == 0 {
		params.AppreciationRate = defaults.AppreciationRate
	}
	if params.RentGrowthRate == 0 {
		params.RentGrowthRate = defaults.RentGrowthRate
	}
	if params.OperatingExpenses == 0 {
		params.OperatingExpenses = defaults.OperatingExpenses
	}
	if params.MinDownPayment == 0 {
		params.MinDownPayment = defaults.MinDownPayment
	}
	if params.AvgPropertyPrice == 0 {
		// Calculate from portfolio
		if len(params.Properties) > 0 {
			total := 0
			for _, p := range params.Properties {
				total += p.Property.Price
			}
			params.AvgPropertyPrice = total / len(params.Properties)
		} else {
			params.AvgPropertyPrice = defaults.AvgPropertyPrice
		}
	}
	if params.AvgPropertyRent == 0 {
		// Calculate from portfolio
		if len(params.Properties) > 0 {
			total := 0
			for _, p := range params.Properties {
				total += p.Property.EstimatedRent
			}
			params.AvgPropertyRent = total / len(params.Properties)
		} else {
			params.AvgPropertyRent = defaults.AvgPropertyRent
		}
	}

	return params
}

// projectWithoutReinvestment models portfolio without reinvesting cash flows
func (m *ReinvestmentModeler) projectWithoutReinvestment(
	params ReinvestmentParams,
) []investment.YearlyProjection {
	projections := make([]investment.YearlyProjection, params.ProjectionYears)

	// Calculate initial portfolio metrics
	initialValue := m.calculatePortfolioValue(params.Properties)
	initialLoanBalance := m.calculateTotalLoanBalance(params.Properties)
	initialAnnualCashFlow := m.calculateAnnualCashFlow(params.Properties, params)

	cumulativeCashFlow := 0

	for year := 1; year <= params.ProjectionYears; year++ {
		// Apply appreciation
		yearValue := int(float64(initialValue) * math.Pow(1+params.AppreciationRate/100, float64(year)))

		// Calculate loan paydown (simplified - assume constant amortization factor)
		yearsPaid := float64(year)
		paydownFactor := yearsPaid / 30.0 // 30-year mortgage
		loanBalance := int(float64(initialLoanBalance) * (1 - paydownFactor*0.02)) // ~2% principal per year early on

		// Calculate equity
		equity := yearValue - loanBalance

		// Calculate cash flow with rent growth
		rentGrowthFactor := math.Pow(1+params.RentGrowthRate/100, float64(year))
		annualCashFlow := int(float64(initialAnnualCashFlow) * rentGrowthFactor)

		cumulativeCashFlow += annualCashFlow

		// Calculate appreciation for the year
		prevYearValue := initialValue
		if year > 1 {
			prevYearValue = int(float64(initialValue) * math.Pow(1+params.AppreciationRate/100, float64(year-1)))
		}
		yearAppreciation := yearValue - prevYearValue

		projections[year-1] = investment.YearlyProjection{
			Year:               year,
			PortfolioValue:     yearValue,
			Equity:             equity,
			LoanBalance:        loanBalance,
			AnnualCashFlow:     annualCashFlow,
			CumulativeCashFlow: cumulativeCashFlow,
			Appreciation:       yearAppreciation,
		}
	}

	return projections
}

// projectWithReinvestment models portfolio with cash flow reinvestment
func (m *ReinvestmentModeler) projectWithReinvestment(
	params ReinvestmentParams,
) []investment.YearlyProjection {
	projections := make([]investment.YearlyProjection, params.ProjectionYears)

	// Track evolving portfolio
	currentProperties := make([]investment.PropertyInPortfolio, len(params.Properties))
	copy(currentProperties, params.Properties)

	acquisitionPool := 0 // Accumulated cash for acquisition
	cumulativeCashFlow := 0
	propertiesAcquired := 0

	for year := 1; year <= params.ProjectionYears; year++ {
		// Calculate current portfolio value with appreciation
		portfolioValue := 0
		totalLoanBalance := 0

		for i := range currentProperties {
			// Apply appreciation to each property
			appreciationFactor := math.Pow(1+params.AppreciationRate/100, float64(year))
			currentProperties[i].Property.Price = int(float64(params.Properties[0].Property.Price) * appreciationFactor)
			portfolioValue += currentProperties[i].Property.Price

			// Calculate loan balance (simplified)
			yearsPaid := float64(year)
			paydownFactor := yearsPaid / 30.0
			currentProperties[i].LoanAmount = int(float64(currentProperties[i].LoanAmount) * (1 - paydownFactor*0.02))
			totalLoanBalance += currentProperties[i].LoanAmount
		}

		// Calculate annual cash flow with rent growth
		annualCashFlow := m.calculateAnnualCashFlowForYear(currentProperties, params, year)

		// Calculate reinvestment amount
		reinvestAmount := int(float64(annualCashFlow) * params.ReinvestmentRate / 100)
		acquisitionPool += reinvestAmount

		// Check if we can acquire a new property
		if acquisitionPool >= params.MinDownPayment {
			// Simulate acquisition
			newProperty := m.simulateAcquisition(params)
			currentProperties = append(currentProperties, newProperty)

			// Deduct from pool
			acquisitionPool -= int(float64(params.AvgPropertyPrice) * params.DownPaymentPct)
			propertiesAcquired++

			m.logger.Debug("simulated property acquisition",
				"year", year,
				"totalProperties", len(currentProperties),
			)
		}

		// Recalculate portfolio value after potential acquisition
		portfolioValue = 0
		totalLoanBalance = 0
		for _, p := range currentProperties {
			portfolioValue += p.Property.Price
			totalLoanBalance += p.LoanAmount
		}

		equity := portfolioValue - totalLoanBalance
		cumulativeCashFlow += annualCashFlow

		// Calculate appreciation for the year
		prevYearValue := m.calculatePortfolioValue(params.Properties)
		if year > 1 {
			prevYearValue = projections[year-2].PortfolioValue
		}
		yearAppreciation := portfolioValue - prevYearValue

		projections[year-1] = investment.YearlyProjection{
			Year:               year,
			PortfolioValue:     portfolioValue,
			Equity:             equity,
			LoanBalance:        totalLoanBalance,
			AnnualCashFlow:     annualCashFlow,
			CumulativeCashFlow: cumulativeCashFlow,
			Appreciation:       yearAppreciation,
		}
	}

	return projections
}

// simulateAcquisition creates a simulated property acquisition
func (m *ReinvestmentModeler) simulateAcquisition(params ReinvestmentParams) investment.PropertyInPortfolio {
	downPayment := int(float64(params.AvgPropertyPrice) * params.DownPaymentPct)
	loanAmount := params.AvgPropertyPrice - downPayment

	// Calculate monthly mortgage
	monthlyRate := params.MortgageRate / 100 / 12
	numPayments := 360.0
	monthlyMortgage := float64(loanAmount) * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) /
		(math.Pow(1+monthlyRate, numPayments) - 1)

	// Calculate monthly cash flow
	monthlyRent := params.AvgPropertyRent
	monthlyExpenses := int(float64(monthlyRent) * params.OperatingExpenses)
	monthlyCashFlow := monthlyRent - monthlyExpenses - int(monthlyMortgage)

	// Calculate cap rate
	annualNOI := float64(monthlyRent) * 12 * (1 - params.OperatingExpenses)
	capRate := (annualNOI / float64(params.AvgPropertyPrice)) * 100

	return investment.PropertyInPortfolio{
		Property: investment.Property{
			ID:            "simulated",
			Address:       "Simulated Acquisition",
			City:          "Various",
			State:         "US",
			Price:         params.AvgPropertyPrice,
			EstimatedRent: params.AvgPropertyRent,
		},
		DownPayment:     downPayment,
		LoanAmount:      loanAmount,
		MonthlyPayment:  int(monthlyMortgage),
		MonthlyCashFlow: monthlyCashFlow,
		CapRate:         capRate,
	}
}

// calculateCumulativeDiff calculates the difference between scenarios at key years
func (m *ReinvestmentModeler) calculateCumulativeDiff(
	base, reinvest []investment.YearlyProjection,
	years int,
) investment.ReinvestmentDiff {
	diff := investment.ReinvestmentDiff{}

	// Year 5 comparison
	if years >= 5 && len(base) >= 5 && len(reinvest) >= 5 {
		diff.Year5 = investment.ReinvestmentDiffPoint{
			Value:    reinvest[4].PortfolioValue - base[4].PortfolioValue,
			CashFlow: reinvest[4].AnnualCashFlow - base[4].AnnualCashFlow,
			Equity:   reinvest[4].Equity - base[4].Equity,
		}
	}

	// Year 10 comparison
	if years >= 10 && len(base) >= 10 && len(reinvest) >= 10 {
		diff.Year10 = investment.ReinvestmentDiffPoint{
			Value:    reinvest[9].PortfolioValue - base[9].PortfolioValue,
			CashFlow: reinvest[9].AnnualCashFlow - base[9].AnnualCashFlow,
			Equity:   reinvest[9].Equity - base[9].Equity,
		}
	} else if years > 0 && len(base) > 0 && len(reinvest) > 0 {
		// Use final year if less than 10 years
		lastIdx := len(base) - 1
		diff.Year10 = investment.ReinvestmentDiffPoint{
			Value:    reinvest[lastIdx].PortfolioValue - base[lastIdx].PortfolioValue,
			CashFlow: reinvest[lastIdx].AnnualCashFlow - base[lastIdx].AnnualCashFlow,
			Equity:   reinvest[lastIdx].Equity - base[lastIdx].Equity,
		}
	}

	return diff
}

// calculateCompoundedReturns calculates the total additional returns from reinvestment
func (m *ReinvestmentModeler) calculateCompoundedReturns(
	base, reinvest []investment.YearlyProjection,
) investment.CompoundedReturns {
	if len(reinvest) == 0 || len(base) == 0 {
		return investment.CompoundedReturns{}
	}

	lastIdx := len(reinvest) - 1
	baseLastIdx := len(base) - 1

	// Calculate total reinvested (sum of cash flows that were reinvested vs withdrawn)
	// In base scenario, cash flows are kept; in reinvest scenario, they're deployed
	baseCumulativeCashFlow := base[baseLastIdx].CumulativeCashFlow
	totalReinvested := baseCumulativeCashFlow // Amount that was reinvested instead of kept

	// Additional value from reinvestment
	additionalValue := reinvest[lastIdx].PortfolioValue - base[baseLastIdx].PortfolioValue

	// Additional annual cash flow from acquired properties
	additionalCashFlow := reinvest[lastIdx].AnnualCashFlow - base[baseLastIdx].AnnualCashFlow

	return investment.CompoundedReturns{
		TotalReinvested:         totalReinvested,
		AdditionalPropertyValue: additionalValue,
		AdditionalCashFlow:      additionalCashFlow,
	}
}

// Helper methods

func (m *ReinvestmentModeler) calculatePortfolioValue(properties []investment.PropertyInPortfolio) int {
	total := 0
	for _, p := range properties {
		total += p.Property.Price
	}
	return total
}

func (m *ReinvestmentModeler) calculateTotalLoanBalance(properties []investment.PropertyInPortfolio) int {
	total := 0
	for _, p := range properties {
		total += p.LoanAmount
	}
	return total
}

func (m *ReinvestmentModeler) calculateAnnualCashFlow(
	properties []investment.PropertyInPortfolio,
	params ReinvestmentParams,
) int {
	total := 0
	for _, p := range properties {
		total += p.MonthlyCashFlow * 12
	}
	return total
}

func (m *ReinvestmentModeler) calculateAnnualCashFlowForYear(
	properties []investment.PropertyInPortfolio,
	params ReinvestmentParams,
	year int,
) int {
	// Apply rent growth
	rentGrowthFactor := math.Pow(1+params.RentGrowthRate/100, float64(year))

	total := 0
	for _, p := range properties {
		// Adjusted cash flow with rent growth (expenses and mortgage stay same)
		adjustedRent := int(float64(p.Property.EstimatedRent) * rentGrowthFactor)
		expenses := int(float64(adjustedRent) * params.OperatingExpenses)
		cashFlow := adjustedRent - expenses - p.MonthlyPayment
		total += cashFlow * 12
	}

	return total
}
