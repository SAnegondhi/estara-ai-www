package projection

import (
	"fmt"
	"math"
	"strings"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/expenses"
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
		// No assumptions - use CalculateGrowthWithMarketData for transparent assumptions
	}
}

// CalculateGrowthWithMarketData projects portfolio value using location-specific market data
// and returns transparent assumptions for each rate used
func (c *Calculator) CalculateGrowthWithMarketData(
	properties []investment.PropertyInPortfolio,
	years int,
	marketQuality []investment.LocationMarketAnalysis,
	mortgageRate float64,
) *investment.GrowthProjection {
	if len(properties) == 0 || years <= 0 {
		return &investment.GrowthProjection{
			Years:      years,
			YearlyData: []investment.YearlyProjection{},
		}
	}

	// Build market data lookup
	marketLookup := make(map[string]*investment.LocationMarketAnalysis, len(marketQuality))
	for i := range marketQuality {
		key := strings.ToLower(marketQuality[i].Location)
		marketLookup[key] = &marketQuality[i]
	}

	// Calculate per-property appreciation and rent growth rates
	type propertyRates struct {
		appreciationRate float64
		rentGrowthRate   float64
		location         string
	}
	rates := make([]propertyRates, len(properties))
	hasMarketData := false

	for i, p := range properties {
		location := strings.ToLower(fmt.Sprintf("%s, %s", p.Property.City, p.Property.State))
		rates[i].location = location

		// Get appreciation rate
		if marketData, ok := marketLookup[location]; ok && marketData.PriceGrowth5Y != nil {
			// Convert 5-year growth to annual CAGR
			totalGrowth := *marketData.PriceGrowth5Y / 100
			if totalGrowth > -1 {
				rates[i].appreciationRate = (math.Pow(1+totalGrowth, 0.2) - 1)
				hasMarketData = true
			} else {
				rates[i].appreciationRate = c.config.AppreciationRate
			}
		} else {
			rates[i].appreciationRate = c.config.AppreciationRate
		}

		// Get rent growth rate
		if marketData, ok := marketLookup[location]; ok && marketData.RentGrowth5Y != nil {
			totalGrowth := *marketData.RentGrowth5Y / 100
			if totalGrowth > -1 {
				rates[i].rentGrowthRate = (math.Pow(1+totalGrowth, 0.2) - 1)
				hasMarketData = true
			} else {
				rates[i].rentGrowthRate = c.config.RentGrowthRate
			}
		} else {
			rates[i].rentGrowthRate = c.config.RentGrowthRate
		}
	}

	// Calculate weighted average rates for assumptions display
	totalValue := 0
	weightedAppreciation := 0.0
	weightedRentGrowth := 0.0
	for i, p := range properties {
		totalValue += p.Property.Price
		weightedAppreciation += float64(p.Property.Price) * rates[i].appreciationRate
		weightedRentGrowth += float64(p.Property.Price) * rates[i].rentGrowthRate
	}
	avgAppreciationRate := weightedAppreciation / float64(totalValue)
	avgRentGrowthRate := weightedRentGrowth / float64(totalValue)

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

	// Track per-property current values
	propertyValues := make([]float64, len(properties))
	propertyRents := make([]float64, len(properties))
	for i, p := range properties {
		propertyValues[i] = float64(p.Property.Price)
		propertyRents[i] = float64(p.Property.EstimatedRent * 12) // Annual rent
	}

	yearlyData := make([]investment.YearlyProjection, years)
	cumulativeCashFlow := 0
	totalAppreciation := 0
	currentLoanBalance := float64(initialLoanBalance)

	effectiveMortgageRate := mortgageRate
	if effectiveMortgageRate == 0 {
		effectiveMortgageRate = 7.0 // Default
	}

	for year := 1; year <= years; year++ {
		yearAppreciation := 0.0
		currentValue := 0.0

		// Apply per-property appreciation
		for i := range properties {
			appreciation := propertyValues[i] * rates[i].appreciationRate
			propertyValues[i] += appreciation
			yearAppreciation += appreciation
			currentValue += propertyValues[i]
		}
		totalAppreciation += int(yearAppreciation)

		// Calculate principal paydown
		annualPrincipalPaydown := c.calculateAnnualPrincipalPaydown(
			float64(initialLoanBalance),
			effectiveMortgageRate/100,
			c.config.LoanTermYears,
			year,
		)
		currentLoanBalance -= annualPrincipalPaydown

		// Calculate equity
		equity := currentValue - currentLoanBalance

		// Apply per-property rent growth and calculate cash flow
		annualCashFlow := 0.0
		for i, p := range properties {
			propertyRents[i] *= (1 + rates[i].rentGrowthRate)
			// Simplified: monthly cash flow grows proportionally
			monthlyCashFlow := float64(p.MonthlyCashFlow) * math.Pow(1+rates[i].rentGrowthRate, float64(year))
			annualCashFlow += monthlyCashFlow * 12
		}
		cumulativeCashFlow += int(annualCashFlow)

		yearlyData[year-1] = investment.YearlyProjection{
			Year:               year,
			PortfolioValue:     int(currentValue),
			Equity:             int(equity),
			LoanBalance:        int(currentLoanBalance),
			AnnualCashFlow:     int(annualCashFlow),
			CumulativeCashFlow: cumulativeCashFlow,
			Appreciation:       int(yearAppreciation),
		}
	}

	// Calculate CAGR
	finalValue := yearlyData[years-1].PortfolioValue
	cagr := c.calculateCAGR(float64(initialValue), float64(finalValue), years)

	// Build assumptions
	assumptions := c.buildGrowthAssumptions(
		properties, avgAppreciationRate, avgRentGrowthRate,
		effectiveMortgageRate, hasMarketData,
	)

	return &investment.GrowthProjection{
		Years:             years,
		YearlyData:        yearlyData,
		FinalValue:        finalValue,
		FinalEquity:       yearlyData[years-1].Equity,
		FinalCashFlow:     yearlyData[years-1].AnnualCashFlow,
		TotalAppreciation: totalAppreciation,
		TotalCashFlow:     cumulativeCashFlow,
		CAGR:              cagr,
		Assumptions:       assumptions,
	}
}

// buildGrowthAssumptions creates transparent assumptions for growth projections
func (c *Calculator) buildGrowthAssumptions(
	properties []investment.PropertyInPortfolio,
	avgAppreciationRate float64,
	avgRentGrowthRate float64,
	mortgageRate float64,
	hasMarketData bool,
) *investment.ProjectionAssumptions {
	assumptions := &investment.ProjectionAssumptions{
		// Growth rates
		AppreciationRate: avgAppreciationRate * 100, // Convert to percentage
		RentGrowthRate:   avgRentGrowthRate * 100,

		// Financing (for context)
		MortgageRate:   mortgageRate,
		LoanTermYears:  c.config.LoanTermYears,
		DownPaymentPct: 0.20, // Standard assumption

		// Data quality
		DataQualityNotes: []string{},
	}

	// Set sources
	if hasMarketData {
		assumptions.AppreciationSource = "Market Data 5Y CAGR (portfolio-weighted)"
		assumptions.RentGrowthSource = "Market Data 5Y CAGR (portfolio-weighted)"
		assumptions.OverallConfidence = 85
	} else {
		assumptions.AppreciationSource = fmt.Sprintf("Default (%.1f%%)", c.config.AppreciationRate*100)
		assumptions.RentGrowthSource = fmt.Sprintf("Default (%.1f%%)", c.config.RentGrowthRate*100)
		assumptions.OverallConfidence = 60
		assumptions.DataQualityNotes = append(assumptions.DataQualityNotes,
			"Using default growth rates (market data unavailable)")
	}

	// Mortgage rate source
	if mortgageRate != 7.0 {
		assumptions.MortgageSource = "FRED 30Y Fixed"
	} else {
		assumptions.MortgageSource = "Default (7%)"
	}

	// Calculate expense ratio from config
	assumptions.ExpenseRatio = c.config.ExpenseRatio * 100
	assumptions.ExpenseSource = fmt.Sprintf("Default (%.0f%%)", c.config.ExpenseRatio*100)

	// Acquisition assumptions (from portfolio)
	if len(properties) > 0 {
		totalPrice := 0
		totalRent := 0
		for _, p := range properties {
			totalPrice += p.Property.Price
			totalRent += p.Property.EstimatedRent
		}
		assumptions.AcquisitionPrice = totalPrice / len(properties)
		assumptions.AcquisitionRent = totalRent / len(properties)
		assumptions.AcquisitionPriceSource = "Portfolio Average"
		assumptions.AcquisitionRentSource = "Portfolio Average"

		// Most common location
		locationCounts := make(map[string]int)
		for _, p := range properties {
			loc := fmt.Sprintf("%s, %s", p.Property.City, p.Property.State)
			locationCounts[loc]++
		}
		var mostCommon string
		maxCount := 0
		for loc, count := range locationCounts {
			if count > maxCount {
				maxCount = count
				mostCommon = loc
			}
		}
		assumptions.AcquisitionLocation = mostCommon
	}

	return assumptions
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
	// ADR-063: Use portfolio-wide CoC (total cash flow / total down payment)
	// This ensures consistency with projection calculations
	if metrics.TotalDownPayment > 0 {
		metrics.AvgCashOnCash = (float64(metrics.AnnualCashFlow) / float64(metrics.TotalDownPayment)) * 100
	}
	metrics.AvgDSCR = totalDSCR / count

	// Calculate Expected Annual Return = NOI / Cash Invested (before debt service)
	// This differs from CoC which is (NOI - Debt Service) / Cash Invested
	if metrics.TotalDownPayment > 0 {
		metrics.ExpectedAnnualReturn = (totalNOI / float64(metrics.TotalDownPayment)) * 100
	}

	// Calculate portfolio-level DSCR
	if totalDebtService > 0 {
		metrics.PortfolioDSCR = totalNOI / totalDebtService
	}

	// Calculate leverage ratio
	if metrics.TotalInvestment > 0 {
		metrics.LeverageRatio = float64(metrics.TotalLoanAmount) / float64(metrics.TotalInvestment)
	}

	// === Additional metrics for client compatibility ===

	// DebtServiceCoverage - alias for PortfolioDSCR
	metrics.DebtServiceCoverage = metrics.PortfolioDSCR

	// LocationCount - count unique locations
	locationSet := make(map[string]bool)
	for _, p := range properties {
		if p.Property.City != "" && p.Property.State != "" {
			locationSet[p.Property.City+", "+p.Property.State] = true
		}
	}
	metrics.LocationCount = len(locationSet)

	// DiversificationScore - 20 points per location, max 100
	metrics.DiversificationScore = math.Min(100, float64(metrics.LocationCount)*20)

	// SharpeRatio - simplified calculation
	// Sharpe = (Return - Risk-free rate) / Volatility
	// Using avg CoC as return, 4% as risk-free, estimate volatility from property spread
	riskFreeRate := 4.0
	if count > 1 {
		// Calculate standard deviation of CoC returns
		variance := 0.0
		for _, p := range properties {
			diff := p.CashOnCash - metrics.AvgCashOnCash
			variance += diff * diff
		}
		volatility := math.Sqrt(variance / count)
		if volatility > 0 {
			metrics.SharpeRatio = (metrics.AvgCashOnCash - riskFreeRate) / volatility
		} else {
			// No volatility = perfect Sharpe (capped at 3)
			metrics.SharpeRatio = math.Min(3.0, (metrics.AvgCashOnCash-riskFreeRate)/1.0)
		}
	} else {
		// Single property - use simplified calculation
		metrics.SharpeRatio = (metrics.AvgCashOnCash - riskFreeRate) / 10.0 // Assume 10% volatility
	}

	// TotalROI - 5-year projected return
	// Assumes 3% annual appreciation + cumulative cash flow
	if metrics.TotalDownPayment > 0 {
		fiveYearAppreciation := float64(metrics.TotalInvestment) * (math.Pow(1.03, 5) - 1)
		fiveYearCashFlow := float64(metrics.AnnualCashFlow) * 5
		totalReturn := fiveYearAppreciation + fiveYearCashFlow
		metrics.TotalROI = (totalReturn / float64(metrics.TotalDownPayment)) * 100
	}

	// FiveYearProjection - pre-computed yearly values for client display
	metrics.FiveYearProjection = c.calculateFiveYearProjection(properties)

	return metrics
}

// calculateFiveYearProjection computes 5-year portfolio value and cash flow projections
func (c *Calculator) calculateFiveYearProjection(properties []investment.PropertyInPortfolio) *investment.FiveYearProjectionData {
	if len(properties) == 0 {
		return nil
	}

	// Calculate initial totals
	initialValue := 0
	annualCashFlow := 0
	for _, p := range properties {
		initialValue += p.Property.Price
		annualCashFlow += p.MonthlyCashFlow * 12
	}

	projection := &investment.FiveYearProjectionData{}
	currentValue := float64(initialValue)
	currentCashFlow := float64(annualCashFlow)

	for year := 1; year <= 5; year++ {
		// Apply 3% appreciation
		currentValue *= 1.03
		// Apply 2% rent growth to cash flow
		currentCashFlow *= 1.02

		// BUG FIX: CashFlow should be annual (currentCashFlow), not cumulative
		// The UI displays this as "Annual Cash Flow" per year
		yearData := investment.YearProjectionData{
			Value:    int(currentValue),
			CashFlow: int(currentCashFlow),
		}

		switch year {
		case 1:
			projection.Year1 = yearData
		case 2:
			projection.Year2 = yearData
		case 3:
			projection.Year3 = yearData
		case 4:
			projection.Year4 = yearData
		case 5:
			projection.Year5 = yearData
		}
	}

	return projection
}

// CalculatePropertyMetrics calculates investment metrics for a single property
// Uses actual state-specific expense rates instead of flat defaults
func (c *Calculator) CalculatePropertyMetrics(
	property investment.Property,
	downPaymentPct float64,
	mortgageRate float64,
) investment.PropertyInPortfolio {
	return c.CalculatePropertyMetricsWithVacancy(property, downPaymentPct, mortgageRate, nil)
}

// CalculatePropertyMetricsWithVacancy calculates metrics with optional market-specific vacancy rate
func (c *Calculator) CalculatePropertyMetricsWithVacancy(
	property investment.Property,
	downPaymentPct float64,
	mortgageRate float64,
	marketVacancyRate *float64,
) investment.PropertyInPortfolio {
	downPayment := int(float64(property.Price) * downPaymentPct)
	loanAmount := property.Price - downPayment

	// Calculate monthly mortgage payment
	monthlyPayment := c.calculateMonthlyPayment(float64(loanAmount), mortgageRate, c.config.LoanTermYears)

	// Use expense calculator for actual state-specific rates
	expCalc := expenses.NewCalculator()
	expInput := expenses.PropertyInput{
		Price:         property.Price,
		State:         property.State,
		City:          property.City, // For market-class based management fees
		YearBuilt:     property.YearBuilt,
		EstimatedRent: property.EstimatedRent,
		PropertyType:  property.PropertyType,
	}
	if marketVacancyRate != nil {
		expInput.VacancyRateOverride = marketVacancyRate
	}

	opEx, err := expCalc.Calculate(expInput)
	if err != nil {
		// Fall back to legacy flat-rate calculation if expense calculator fails
		annualRent := float64(property.EstimatedRent * 12)
		vacancyRate := c.config.VacancyRate
		if marketVacancyRate != nil {
			vacancyRate = *marketVacancyRate / 100.0
		}
		effectiveRent := annualRent * (1 - vacancyRate)
		noi := effectiveRent * (1 - c.config.ExpenseRatio)

		capRate := 0.0
		if property.Price > 0 {
			capRate = (noi / float64(property.Price)) * 100
		}

		annualDebtService := monthlyPayment * 12
		annualCashFlow := noi - annualDebtService
		monthlyCashFlow := int(annualCashFlow / 12)

		cashOnCash := 0.0
		if downPayment > 0 {
			cashOnCash = (annualCashFlow / float64(downPayment)) * 100
		}

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
			Expenses:        nil, // No detailed expenses on fallback
		}
	}

	// Use calculated NOI from expense calculator
	noi := opEx.NOI

	// Calculate monthly cash flow
	annualDebtService := monthlyPayment * 12
	annualCashFlow := noi - annualDebtService
	monthlyCashFlow := int(annualCashFlow / 12)

	// Calculate cap rate (from expense calculator is more accurate)
	capRate := opEx.CapRate

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

	// Store calculated expenses for projection transparency
	propExpenses := &investment.PropertyExpenses{
		PropertyTax:      opEx.PropertyTax,
		Insurance:        opEx.Insurance,
		Maintenance:      opEx.Maintenance,
		VacancyAllowance: opEx.VacancyAllowance,
		PropertyMgmt:     opEx.PropertyMgmt,
		TotalAnnual:      opEx.TotalAnnual,
		NOI:              opEx.NOI,
		PropertyTaxRate:  opEx.PropertyTaxRate,
		InsuranceRate:    opEx.InsuranceRate,
		MaintenanceRate:  opEx.MaintenanceRate,
		VacancyRate:      opEx.VacancyRate,
		PropertyMgmtRate: opEx.PropertyMgmtRate,
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
		Expenses:        propExpenses,
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

// ============================================================================
// ADR-063: Unified Portfolio Projection Engine
// ============================================================================

// CalculateAllScenarios generates base, optimistic, and pessimistic projections
// This is the single source of truth - client should NOT recalculate
func (c *Calculator) CalculateAllScenarios(
	properties []investment.PropertyInPortfolio,
	years int,
	marketQuality []investment.LocationMarketAnalysis,
	userAssumptions *investment.UserFinancialAssumptions,
) *investment.ScenarioProjections {
	if len(properties) == 0 || years <= 0 {
		return nil
	}

	// Get mortgage rate from user assumptions or default
	mortgageRate := 7.0
	if userAssumptions != nil && userAssumptions.MortgageRate > 0 {
		mortgageRate = userAssumptions.MortgageRate
	}

	// Base scenario uses actual/market data (multiplier = 1.0)
	base := c.calculateExpandedProjection(properties, years, marketQuality, userAssumptions, mortgageRate, 1.0)

	// Optimistic: +15% growth rates
	optimistic := c.calculateExpandedProjection(properties, years, marketQuality, userAssumptions, mortgageRate, 1.15)

	// Pessimistic: -15% growth rates
	pessimistic := c.calculateExpandedProjection(properties, years, marketQuality, userAssumptions, mortgageRate, 0.85)

	// Build assumptions for transparency
	assumptions := c.buildScenarioAssumptions(properties, marketQuality, userAssumptions, mortgageRate)

	// Build summary for quick comparison
	summary := c.buildScenarioSummary(base, optimistic, pessimistic)

	return &investment.ScenarioProjections{
		Base:        base,
		Optimistic:  optimistic,
		Pessimistic: pessimistic,
		Assumptions: assumptions,
		Summary:     summary,
	}
}

// calculateExpandedProjection computes detailed year-by-year projections for a scenario
func (c *Calculator) calculateExpandedProjection(
	properties []investment.PropertyInPortfolio,
	years int,
	marketQuality []investment.LocationMarketAnalysis,
	userAssumptions *investment.UserFinancialAssumptions,
	mortgageRate float64,
	growthMultiplier float64,
) []investment.ExpandedYearProjection {
	if len(properties) == 0 {
		return nil
	}

	// Build market data lookup
	marketLookup := make(map[string]*investment.LocationMarketAnalysis, len(marketQuality))
	for i := range marketQuality {
		key := strings.ToLower(marketQuality[i].Location)
		marketLookup[key] = &marketQuality[i]
	}

	// Calculate per-property rates
	type propertyRates struct {
		appreciationRate float64
		rentGrowthRate   float64
	}
	rates := make([]propertyRates, len(properties))

	for i, p := range properties {
		location := strings.ToLower(fmt.Sprintf("%s, %s", p.Property.City, p.Property.State))

		// Get appreciation rate from market data or default
		if marketData, ok := marketLookup[location]; ok && marketData.PriceGrowth5Y != nil {
			totalGrowth := *marketData.PriceGrowth5Y / 100
			if totalGrowth > -1 {
				rates[i].appreciationRate = (math.Pow(1+totalGrowth, 0.2) - 1) * growthMultiplier
			} else {
				rates[i].appreciationRate = c.config.AppreciationRate * growthMultiplier
			}
		} else {
			rates[i].appreciationRate = c.config.AppreciationRate * growthMultiplier
		}

		// Get rent growth rate from market data or default
		if marketData, ok := marketLookup[location]; ok && marketData.RentGrowth5Y != nil {
			totalGrowth := *marketData.RentGrowth5Y / 100
			if totalGrowth > -1 {
				rates[i].rentGrowthRate = (math.Pow(1+totalGrowth, 0.2) - 1) * growthMultiplier
			} else {
				rates[i].rentGrowthRate = c.config.RentGrowthRate * growthMultiplier
			}
		} else {
			rates[i].rentGrowthRate = c.config.RentGrowthRate * growthMultiplier
		}
	}

	// Calculate initial totals
	initialValue := 0
	initialLoanBalance := 0
	initialDownPayment := 0
	initialAnnualRent := 0
	initialAnnualExpenses := 0
	initialAnnualDebtService := 0

	for _, p := range properties {
		initialValue += p.Property.Price
		initialLoanBalance += p.LoanAmount
		initialDownPayment += p.DownPayment
		initialAnnualRent += p.Property.EstimatedRent * 12
		initialAnnualDebtService += p.MonthlyPayment * 12

		// Calculate operating expenses from property data or estimate
		if p.Expenses != nil {
			initialAnnualExpenses += int(p.Expenses.TotalAnnual)
		} else {
			// Fallback: estimate expenses as percentage of rent
			initialAnnualExpenses += int(float64(p.Property.EstimatedRent*12) * c.config.ExpenseRatio)
		}
	}

	// Track per-property current values
	propertyValues := make([]float64, len(properties))
	propertyRents := make([]float64, len(properties))
	for i, p := range properties {
		propertyValues[i] = float64(p.Property.Price)
		propertyRents[i] = float64(p.Property.EstimatedRent * 12)
	}

	projections := make([]investment.ExpandedYearProjection, years)
	cumulativeCashFlow := 0
	currentLoanBalance := float64(initialLoanBalance)

	loanTermYears := c.config.LoanTermYears
	if userAssumptions != nil && userAssumptions.LoanTermYears > 0 {
		loanTermYears = userAssumptions.LoanTermYears
	}

	// Calculate initial annual cash flow from pre-calculated property values
	// This ensures Year 1 matches CalculateMetrics() exactly
	initialAnnualCashFlow := 0
	for _, p := range properties {
		initialAnnualCashFlow += p.MonthlyCashFlow * 12
	}
	// Initial NOI = cash flow + debt service (back-calculate for consistency)
	initialNOI := float64(initialAnnualCashFlow) + float64(initialAnnualDebtService)

	for year := 1; year <= years; year++ {
		yearAppreciation := 0.0
		currentValue := 0.0
		currentGrossRent := 0.0

		// Year 1: Use initial values (must match portfolio metrics exactly)
		// Year 2+: Apply appreciation and rent growth from previous year
		if year == 1 {
			// No growth for Year 1 - use initial values
			for i := range properties {
				currentValue += propertyValues[i]
				currentGrossRent += propertyRents[i]
			}
		} else {
			// Apply per-property appreciation and rent growth for Year 2+
			for i := range properties {
				appreciation := propertyValues[i] * rates[i].appreciationRate
				propertyValues[i] += appreciation
				yearAppreciation += appreciation
				currentValue += propertyValues[i]

				propertyRents[i] *= (1 + rates[i].rentGrowthRate)
				currentGrossRent += propertyRents[i]
			}
		}

		// Calculate principal paydown
		annualPrincipalPaydown := c.calculateAnnualPrincipalPaydown(
			float64(initialLoanBalance),
			mortgageRate/100,
			loanTermYears,
			year,
		)
		currentLoanBalance -= annualPrincipalPaydown
		if currentLoanBalance < 0 {
			currentLoanBalance = 0
		}

		// Calculate equity
		equity := currentValue - currentLoanBalance

		// Calculate NOI and cash flow
		var noi float64
		var cashFlow float64
		var currentExpenses float64
		debtService := float64(initialAnnualDebtService) // Constant for fixed-rate mortgage

		if year == 1 {
			// Year 1: Use pre-calculated values from properties to match CalculateMetrics()
			// This ensures Portfolio Overview and Projection table show identical Year 1 values
			cashFlow = float64(initialAnnualCashFlow)
			noi = initialNOI
			currentExpenses = float64(initialAnnualExpenses) // No growth in Year 1
		} else {
			// Year 2+: Calculate based on rent growth
			// Expenses grow proportionally with rent
			rentGrowthFactor := currentGrossRent / float64(initialAnnualRent)
			currentExpenses = float64(initialAnnualExpenses) * rentGrowthFactor

			// NOI = gross rent - operating expenses
			noi = currentGrossRent - currentExpenses

			// Cash flow = NOI - debt service
			cashFlow = noi - debtService
		}

		cumulativeCashFlow += int(cashFlow)

		// Calculate interest vs principal split
		// Interest = Debt Service - Principal Paydown
		interestExpense := debtService - annualPrincipalPaydown
		if interestExpense < 0 {
			interestExpense = 0
		}
		principalPayment := annualPrincipalPaydown

		// Calculate depreciation (27.5 year schedule for residential rental property)
		// Depreciation is based on building value (typically 80% of property value, excluding land)
		buildingValueRatio := 0.80
		depreciation := (currentValue * buildingValueRatio) / 27.5

		// Calculate taxable income
		// Taxable Income = NOI - Interest Expense - Depreciation
		taxableIncome := noi - interestExpense - depreciation

		// Calculate income taxes (default 25% combined federal + state rate)
		// Only pay taxes if taxable income is positive
		incomeTaxRate := 0.25
		incomeTaxes := 0.0
		if taxableIncome > 0 {
			incomeTaxes = taxableIncome * incomeTaxRate
		}

		// Calculate cash flow after tax
		cashFlowAfterTax := cashFlow - incomeTaxes

		// Calculate metrics
		cashOnCash := 0.0
		if initialDownPayment > 0 {
			cashOnCash = (cashFlow / float64(initialDownPayment)) * 100
		}

		capRate := 0.0
		if currentValue > 0 {
			capRate = (noi / currentValue) * 100
		}

		equityMultiple := 0.0
		if initialDownPayment > 0 {
			equityMultiple = equity / float64(initialDownPayment)
		}

		projections[year-1] = investment.ExpandedYearProjection{
			Year:               year,
			PortfolioValue:     int(currentValue),
			Equity:             int(equity),
			LoanBalance:        int(currentLoanBalance),
			AnnualCashFlow:     int(cashFlow),
			NetOperatingIncome: int(noi),
			GrossRent:          int(currentGrossRent),
			OperatingExpenses:  int(currentExpenses),
			DebtService:        int(debtService),
			InterestExpense:    int(interestExpense),
			PrincipalPayment:   int(principalPayment),
			Depreciation:       int(depreciation),
			TaxableIncome:      int(taxableIncome),
			IncomeTaxes:        int(incomeTaxes),
			CashFlowAfterTax:   int(cashFlowAfterTax),
			CashOnCash:         cashOnCash,
			CapRate:            capRate,
			EquityMultiple:     equityMultiple,
			CumulativeCashFlow: cumulativeCashFlow,
			Appreciation:       int(yearAppreciation),
		}
	}

	return projections
}

// buildScenarioAssumptions creates transparent assumptions for all scenarios
func (c *Calculator) buildScenarioAssumptions(
	properties []investment.PropertyInPortfolio,
	marketQuality []investment.LocationMarketAnalysis,
	userAssumptions *investment.UserFinancialAssumptions,
	mortgageRate float64,
) *investment.ProjectionAssumptions {
	// Build market data lookup
	marketLookup := make(map[string]*investment.LocationMarketAnalysis, len(marketQuality))
	for i := range marketQuality {
		key := strings.ToLower(marketQuality[i].Location)
		marketLookup[key] = &marketQuality[i]
	}

	// Calculate weighted average rates
	totalValue := 0
	weightedAppreciation := 0.0
	weightedRentGrowth := 0.0
	hasMarketData := false

	for _, p := range properties {
		location := strings.ToLower(fmt.Sprintf("%s, %s", p.Property.City, p.Property.State))
		totalValue += p.Property.Price

		if marketData, ok := marketLookup[location]; ok {
			if marketData.PriceGrowth5Y != nil {
				totalGrowth := *marketData.PriceGrowth5Y / 100
				if totalGrowth > -1 {
					rate := math.Pow(1+totalGrowth, 0.2) - 1
					weightedAppreciation += float64(p.Property.Price) * rate
					hasMarketData = true
				} else {
					weightedAppreciation += float64(p.Property.Price) * c.config.AppreciationRate
				}
			} else {
				weightedAppreciation += float64(p.Property.Price) * c.config.AppreciationRate
			}

			if marketData.RentGrowth5Y != nil {
				totalGrowth := *marketData.RentGrowth5Y / 100
				if totalGrowth > -1 {
					rate := math.Pow(1+totalGrowth, 0.2) - 1
					weightedRentGrowth += float64(p.Property.Price) * rate
					hasMarketData = true
				} else {
					weightedRentGrowth += float64(p.Property.Price) * c.config.RentGrowthRate
				}
			} else {
				weightedRentGrowth += float64(p.Property.Price) * c.config.RentGrowthRate
			}
		} else {
			weightedAppreciation += float64(p.Property.Price) * c.config.AppreciationRate
			weightedRentGrowth += float64(p.Property.Price) * c.config.RentGrowthRate
		}
	}

	avgAppreciationRate := 0.0
	avgRentGrowthRate := 0.0
	if totalValue > 0 {
		avgAppreciationRate = weightedAppreciation / float64(totalValue)
		avgRentGrowthRate = weightedRentGrowth / float64(totalValue)
	}

	// Get down payment from user assumptions or calculate from properties
	downPaymentPct := 0.20
	if userAssumptions != nil && userAssumptions.DownPaymentPercent > 0 {
		downPaymentPct = userAssumptions.DownPaymentPercent / 100
	} else if len(properties) > 0 && totalValue > 0 {
		totalDownPayment := 0
		for _, p := range properties {
			totalDownPayment += p.DownPayment
		}
		downPaymentPct = float64(totalDownPayment) / float64(totalValue)
	}

	loanTermYears := c.config.LoanTermYears
	if userAssumptions != nil && userAssumptions.LoanTermYears > 0 {
		loanTermYears = userAssumptions.LoanTermYears
	}

	assumptions := &investment.ProjectionAssumptions{
		// Growth rates
		AppreciationRate: avgAppreciationRate * 100,
		RentGrowthRate:   avgRentGrowthRate * 100,

		// Financing
		MortgageRate:   mortgageRate,
		DownPaymentPct: downPaymentPct,
		LoanTermYears:  loanTermYears,

		// Data quality
		DataQualityNotes: []string{},
	}

	// Set sources
	if hasMarketData {
		assumptions.AppreciationSource = "Market Data 5Y CAGR (portfolio-weighted)"
		assumptions.RentGrowthSource = "Market Data 5Y CAGR (portfolio-weighted)"
		assumptions.OverallConfidence = 85
	} else {
		assumptions.AppreciationSource = fmt.Sprintf("Default (%.1f%%)", c.config.AppreciationRate*100)
		assumptions.RentGrowthSource = fmt.Sprintf("Default (%.1f%%)", c.config.RentGrowthRate*100)
		assumptions.OverallConfidence = 60
		assumptions.DataQualityNotes = append(assumptions.DataQualityNotes,
			"Using default growth rates (market data unavailable)")
	}

	// Mortgage rate source
	if userAssumptions != nil && userAssumptions.MortgageRate > 0 {
		assumptions.MortgageSource = "User Input"
	} else if mortgageRate != 7.0 {
		assumptions.MortgageSource = "FRED 30Y Fixed"
	} else {
		assumptions.MortgageSource = "Default (7%)"
	}

	// Expense ratio
	assumptions.ExpenseRatio = c.config.ExpenseRatio * 100
	if len(properties) > 0 && properties[0].Expenses != nil {
		assumptions.ExpenseSource = "State-specific calculation"
	} else {
		assumptions.ExpenseSource = fmt.Sprintf("Default (%.0f%%)", c.config.ExpenseRatio*100)
	}

	return assumptions
}

// buildScenarioSummary creates a quick comparison across scenarios
func (c *Calculator) buildScenarioSummary(
	base, optimistic, pessimistic []investment.ExpandedYearProjection,
) *investment.ScenarioSummary {
	if len(base) == 0 {
		return nil
	}

	lastIdx := len(base) - 1

	// Calculate CAGR for each scenario
	initialValue := float64(base[0].PortfolioValue) / (1 + c.config.AppreciationRate)
	years := len(base)

	baseCAGR := c.calculateCAGR(initialValue, float64(base[lastIdx].PortfolioValue), years)
	optCAGR := c.calculateCAGR(initialValue, float64(optimistic[lastIdx].PortfolioValue), years)
	pessCAGR := c.calculateCAGR(initialValue, float64(pessimistic[lastIdx].PortfolioValue), years)

	// Calculate total cash flow for each scenario
	baseTotalCF := 0
	optTotalCF := 0
	pessTotalCF := 0
	for i := range base {
		baseTotalCF += base[i].AnnualCashFlow
		optTotalCF += optimistic[i].AnnualCashFlow
		pessTotalCF += pessimistic[i].AnnualCashFlow
	}

	return &investment.ScenarioSummary{
		// Base
		BaseFinalValue:    base[lastIdx].PortfolioValue,
		BaseFinalEquity:   base[lastIdx].Equity,
		BaseTotalCashFlow: baseTotalCF,
		BaseCAGR:          baseCAGR,

		// Optimistic
		OptimisticFinalValue:    optimistic[lastIdx].PortfolioValue,
		OptimisticFinalEquity:   optimistic[lastIdx].Equity,
		OptimisticTotalCashFlow: optTotalCF,
		OptimisticCAGR:          optCAGR,

		// Pessimistic
		PessimisticFinalValue:    pessimistic[lastIdx].PortfolioValue,
		PessimisticFinalEquity:   pessimistic[lastIdx].Equity,
		PessimisticTotalCashFlow: pessTotalCF,
		PessimisticCAGR:          pessCAGR,

		// Multipliers
		OptimisticMultiplier:  1.15,
		PessimisticMultiplier: 0.85,
	}
}
