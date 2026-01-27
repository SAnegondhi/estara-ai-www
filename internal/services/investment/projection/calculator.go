package projection

import (
	"math"

	"github.com/estara-ai/www/internal/services/investment"
)

// DefaultConfig holds default projection parameters
type DefaultConfig struct {
	AppreciationRate float64 // Annual property appreciation rate
	RentGrowthRate   float64 // Annual rent growth rate
	ExpenseRatio     float64 // Operating expenses as % of rent
	VacancyRate      float64 // Expected vacancy rate
	LoanTermYears    int     // Mortgage term in years
}

// DefaultProjectionConfig returns sensible defaults
func DefaultProjectionConfig() DefaultConfig {
	return DefaultConfig{
		AppreciationRate: 0.03,  // 3% annual appreciation
		RentGrowthRate:   0.02,  // 2% annual rent growth
		ExpenseRatio:     0.35,  // 35% operating expenses
		VacancyRate:      0.05,  // 5% vacancy
		LoanTermYears:    30,    // 30-year mortgage
	}
}

// Calculator calculates portfolio growth projections
type Calculator struct {
	config DefaultConfig
}

// NewCalculator creates a new projection calculator
func NewCalculator(config *DefaultConfig) *Calculator {
	cfg := DefaultProjectionConfig()
	if config != nil {
		cfg = *config
	}
	return &Calculator{config: cfg}
}

// CalculateGrowth projects portfolio value over time
func (c *Calculator) CalculateGrowth(properties []investment.PropertyInPortfolio, years int) *investment.GrowthProjection {
	if len(properties) == 0 || years <= 0 {
		return &investment.GrowthProjection{
			Years:      years,
			YearlyData: []investment.YearlyProjection{},
		}
	}

	// Calculate initial totals
	initialValue := 0
	initialLoanBalance := 0
	initialEquity := 0
	initialAnnualCashFlow := 0

	for _, p := range properties {
		initialValue += p.Property.Price
		initialLoanBalance += p.LoanAmount
		initialEquity += p.DownPayment
		initialAnnualCashFlow += p.MonthlyCashFlow * 12
	}

	yearlyData := make([]investment.YearlyProjection, years)
	cumulativeCashFlow := 0
	totalAppreciation := 0

	currentValue := float64(initialValue)
	currentLoanBalance := float64(initialLoanBalance)
	currentAnnualCashFlow := float64(initialAnnualCashFlow)

	for year := 1; year <= years; year++ {
		// Apply appreciation to property value
		appreciation := currentValue * c.config.AppreciationRate
		currentValue += appreciation
		totalAppreciation += int(appreciation)

		// Calculate principal paydown (simplified - assumes level payment)
		// In reality, this would be more complex based on amortization schedule
		annualPrincipalPaydown := c.calculateAnnualPrincipalPaydown(
			float64(initialLoanBalance),
			0.07, // Assume 7% mortgage rate (should be passed in)
			c.config.LoanTermYears,
			year,
		)
		currentLoanBalance -= annualPrincipalPaydown

		// Calculate equity
		equity := currentValue - currentLoanBalance

		// Apply rent growth to cash flow
		currentAnnualCashFlow *= (1 + c.config.RentGrowthRate)
		cumulativeCashFlow += int(currentAnnualCashFlow)

		yearlyData[year-1] = investment.YearlyProjection{
			Year:               year,
			PortfolioValue:     int(currentValue),
			Equity:             int(equity),
			LoanBalance:        int(currentLoanBalance),
			AnnualCashFlow:     int(currentAnnualCashFlow),
			CumulativeCashFlow: cumulativeCashFlow,
			Appreciation:       int(appreciation),
		}
	}

	// Calculate CAGR
	finalValue := yearlyData[years-1].PortfolioValue
	cagr := c.calculateCAGR(float64(initialValue), float64(finalValue), years)

	return &investment.GrowthProjection{
		Years:             years,
		YearlyData:        yearlyData,
		FinalValue:        finalValue,
		FinalEquity:       yearlyData[years-1].Equity,
		FinalCashFlow:     yearlyData[years-1].AnnualCashFlow,
		TotalAppreciation: totalAppreciation,
		TotalCashFlow:     cumulativeCashFlow,
		CAGR:              cagr,
	}
}

// CalculateMetrics computes portfolio-level metrics
func (c *Calculator) CalculateMetrics(properties []investment.PropertyInPortfolio) *investment.PortfolioMetrics {
	if len(properties) == 0 {
		return &investment.PortfolioMetrics{}
	}

	metrics := &investment.PortfolioMetrics{
		PropertyCount: len(properties),
	}

	totalCapRate := 0.0
	totalCashOnCash := 0.0
	totalDSCR := 0.0
	totalNOI := 0.0
	totalDebtService := 0.0

	for _, p := range properties {
		metrics.TotalInvestment += p.Property.Price
		metrics.TotalDownPayment += p.DownPayment
		metrics.TotalLoanAmount += p.LoanAmount
		metrics.MonthlyCashFlow += p.MonthlyCashFlow

		totalCapRate += p.CapRate
		totalCashOnCash += p.CashOnCash
		totalDSCR += p.DSCR

		// Calculate NOI and debt service for portfolio DSCR
		annualRent := p.Property.EstimatedRent * 12
		noi := float64(annualRent) * (1 - c.config.ExpenseRatio) * (1 - c.config.VacancyRate)
		totalNOI += noi
		totalDebtService += float64(p.MonthlyPayment * 12)
	}

	metrics.AnnualCashFlow = metrics.MonthlyCashFlow * 12
	metrics.ProjectedValue = metrics.TotalInvestment // Initial value
	metrics.TotalEquity = metrics.TotalDownPayment

	// Calculate averages
	count := float64(len(properties))
	metrics.AvgCapRate = totalCapRate / count
	metrics.AvgCashOnCash = totalCashOnCash / count
	metrics.AvgDSCR = totalDSCR / count

	// Calculate portfolio-level DSCR
	if totalDebtService > 0 {
		metrics.PortfolioDSCR = totalNOI / totalDebtService
	}

	// Calculate leverage ratio
	if metrics.TotalInvestment > 0 {
		metrics.LeverageRatio = float64(metrics.TotalLoanAmount) / float64(metrics.TotalInvestment)
	}

	return metrics
}

// CalculatePropertyMetrics calculates investment metrics for a single property
func (c *Calculator) CalculatePropertyMetrics(
	property investment.Property,
	downPaymentPct float64,
	mortgageRate float64,
) investment.PropertyInPortfolio {
	downPayment := int(float64(property.Price) * downPaymentPct)
	loanAmount := property.Price - downPayment

	// Calculate monthly mortgage payment
	monthlyPayment := c.calculateMonthlyPayment(float64(loanAmount), mortgageRate, c.config.LoanTermYears)

	// Calculate NOI
	annualRent := property.EstimatedRent * 12
	effectiveRent := float64(annualRent) * (1 - c.config.VacancyRate)
	operatingExpenses := effectiveRent * c.config.ExpenseRatio
	noi := effectiveRent - operatingExpenses

	// Calculate monthly cash flow
	annualDebtService := monthlyPayment * 12
	annualCashFlow := noi - annualDebtService
	monthlyCashFlow := int(annualCashFlow / 12)

	// Calculate cap rate
	capRate := 0.0
	if property.Price > 0 {
		capRate = (noi / float64(property.Price)) * 100
	}

	// Calculate cash on cash return
	cashOnCash := 0.0
	if downPayment > 0 {
		cashOnCash = (annualCashFlow / float64(downPayment)) * 100
	}

	// Calculate DSCR
	dscr := 0.0
	if annualDebtService > 0 {
		dscr = noi / annualDebtService
	}

	return investment.PropertyInPortfolio{
		Property:        property,
		DownPayment:     downPayment,
		LoanAmount:      loanAmount,
		MonthlyPayment:  int(monthlyPayment),
		MonthlyCashFlow: monthlyCashFlow,
		CapRate:         capRate,
		CashOnCash:      cashOnCash,
		DSCR:            dscr,
	}
}

// SummarizeExistingPortfolio creates a summary from existing properties
func (c *Calculator) SummarizeExistingPortfolio(portfolio *investment.ExistingPortfolio) *investment.ExistingPortfolioSummary {
	if portfolio == nil || len(portfolio.Properties) == 0 {
		return nil
	}

	summary := &investment.ExistingPortfolioSummary{
		PropertyCount: len(portfolio.Properties),
		Locations:     make([]string, 0),
	}

	locationSet := make(map[string]bool)
	totalCapRate := 0.0
	capRateCount := 0

	for _, p := range portfolio.Properties {
		summary.TotalValue += p.CurrentValue
		summary.TotalEquity += p.Equity
		summary.TotalDebt += p.MortgageBalance
		summary.MonthlyCashFlow += p.MonthlyCashFlow

		location := p.City + ", " + p.State
		if !locationSet[location] {
			locationSet[location] = true
			summary.Locations = append(summary.Locations, location)
		}

		if p.CapRate > 0 {
			totalCapRate += p.CapRate
			capRateCount++
		}
	}

	summary.AnnualCashFlow = summary.MonthlyCashFlow * 12

	if capRateCount > 0 {
		summary.AvgCapRate = totalCapRate / float64(capRateCount)
	}

	return summary
}

// CalculateCombinedMetrics compares existing vs combined portfolio
func (c *Calculator) CalculateCombinedMetrics(
	existing *investment.ExistingPortfolioSummary,
	newMetrics *investment.PortfolioMetrics,
) *investment.CombinedPortfolioMetrics {
	if existing == nil {
		return nil
	}

	combined := &investment.CombinedPortfolioMetrics{
		// Existing metrics
		ExistingPropertyCount:  existing.PropertyCount,
		ExistingTotalValue:     existing.TotalValue,
		ExistingAnnualCashFlow: existing.AnnualCashFlow,

		// Combined metrics
		CombinedPropertyCount:  existing.PropertyCount + newMetrics.PropertyCount,
		CombinedTotalValue:     existing.TotalValue + newMetrics.TotalInvestment,
		CombinedAnnualCashFlow: existing.AnnualCashFlow + newMetrics.AnnualCashFlow,
	}

	// Calculate improvements
	combined.ValueIncrease = combined.CombinedTotalValue - combined.ExistingTotalValue
	combined.CashFlowIncrease = combined.CombinedAnnualCashFlow - combined.ExistingAnnualCashFlow

	// Calculate diversification improvement (simplified - based on location count)
	// In a real implementation, this would be more sophisticated
	combined.DiversificationImprovement = 0.0 // TODO: Implement location-based diversification scoring

	return combined
}

// Helper functions

// calculateMonthlyPayment calculates monthly mortgage payment using PMT formula
func (c *Calculator) calculateMonthlyPayment(principal float64, annualRate float64, years int) float64 {
	if annualRate == 0 {
		return principal / float64(years*12)
	}

	monthlyRate := annualRate / 12
	numPayments := float64(years * 12)

	// PMT = P * [r(1+r)^n] / [(1+r)^n - 1]
	payment := principal * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) /
		(math.Pow(1+monthlyRate, numPayments) - 1)

	return payment
}

// calculateAnnualPrincipalPaydown estimates principal paid in a given year
func (c *Calculator) calculateAnnualPrincipalPaydown(
	originalPrincipal float64,
	annualRate float64,
	termYears int,
	currentYear int,
) float64 {
	if annualRate == 0 {
		return originalPrincipal / float64(termYears)
	}

	monthlyRate := annualRate / 12
	monthlyPayment := c.calculateMonthlyPayment(originalPrincipal, annualRate, termYears)

	// Calculate remaining balance at start of year
	startMonth := (currentYear - 1) * 12
	remainingBalance := c.calculateRemainingBalance(originalPrincipal, monthlyRate, monthlyPayment, startMonth)

	// Calculate remaining balance at end of year
	endMonth := currentYear * 12
	endBalance := c.calculateRemainingBalance(originalPrincipal, monthlyRate, monthlyPayment, endMonth)

	return remainingBalance - endBalance
}

// calculateRemainingBalance calculates loan balance after n payments
func (c *Calculator) calculateRemainingBalance(
	principal float64,
	monthlyRate float64,
	monthlyPayment float64,
	numPayments int,
) float64 {
	if monthlyRate == 0 {
		return principal - (monthlyPayment * float64(numPayments))
	}

	// B = P*(1+r)^n - PMT*[(1+r)^n - 1]/r
	factor := math.Pow(1+monthlyRate, float64(numPayments))
	balance := principal*factor - monthlyPayment*(factor-1)/monthlyRate

	if balance < 0 {
		return 0
	}
	return balance
}

// calculateCAGR calculates Compound Annual Growth Rate
func (c *Calculator) calculateCAGR(initialValue, finalValue float64, years int) float64 {
	if initialValue <= 0 || years <= 0 {
		return 0
	}

	cagr := math.Pow(finalValue/initialValue, 1.0/float64(years)) - 1
	return cagr * 100 // Return as percentage
}
