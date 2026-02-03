// Package analysis provides financial engineering services for investment planning.
// Part of ADR-059: Investment Planning Enhancement - Selection Mode
package analysis

import (
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/expenses"
)

// ReinvestmentModeler models cash flow reinvestment scenarios
type ReinvestmentModeler struct {
	logger      *slog.Logger
	expenseCalc *expenses.Calculator
}

// NewReinvestmentModeler creates a new reinvestment modeler
func NewReinvestmentModeler(logger *slog.Logger) *ReinvestmentModeler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReinvestmentModeler{
		logger:      logger.With("component", "reinvestment_modeler"),
		expenseCalc: expenses.NewCalculator(),
	}
}

// AcquisitionMarketData holds market-specific data for simulated acquisitions
type AcquisitionMarketData struct {
	TargetCity      string   // City for acquisitions (from portfolio or user-specified)
	TargetState     string   // State for acquisitions
	MedianHomePrice *int     // Market median price (from Market Data)
	MedianRent      *int     // Market median rent (from Market Data)
	VacancyRate     *float64 // Market vacancy rate (from FRED)
	MortgageRate    *float64 // Live mortgage rate (from FRED)
}

// ReinvestmentParams holds parameters for reinvestment modeling
type ReinvestmentParams struct {
	// Portfolio properties to model
	Properties []investment.PropertyInPortfolio

	// Market data for location-specific appreciation rates
	MarketQuality []investment.LocationMarketAnalysis

	// Multi-year investment budgets (user's planned yearly investments)
	YearlyBudgets []investment.YearlyBudget

	// Market data for simulated acquisitions (optional - uses portfolio averages if nil)
	AcquisitionMarket *AcquisitionMarketData

	// Reinvestment settings
	ReinvestmentRate float64 // 0-100 (percentage)
	ProjectionYears  int     // 1-10 years

	// Financial assumptions
	MortgageRate       float64 // Annual rate (e.g., 7.0 for 7%)
	DownPaymentPct     float64 // e.g., 0.20 for 20%
	AppreciationRate   float64 // Annual rate (e.g., 3.0 for 3%) - fallback if no market data
	RentGrowthRate     float64 // Annual rate (e.g., 2.0 for 2%) - fallback if no market data
	OperatingExpenses  float64 // As % of rent (e.g., 0.50 for 50%) - fallback if expense calc fails
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

	// Build assumptions for transparency
	assumptions := m.buildAssumptions(params)

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
		Assumptions:       assumptions,
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

// buildAssumptions creates a transparent summary of all assumptions used in projections
func (m *ReinvestmentModeler) buildAssumptions(params ReinvestmentParams) *investment.ProjectionAssumptions {
	assumptions := &investment.ProjectionAssumptions{
		// Financing
		MortgageRate:   params.MortgageRate,
		DownPaymentPct: params.DownPaymentPct,
		LoanTermYears:  30, // Fixed for now

		// Growth rates (defaults, will be updated if market data available)
		AppreciationRate: params.AppreciationRate,
		RentGrowthRate:   params.RentGrowthRate,

		// Acquisitions
		AcquisitionPrice: params.AvgPropertyPrice,
		AcquisitionRent:  params.AvgPropertyRent,

		// Data quality
		DataQualityNotes: []string{},
	}

	// Determine sources
	defaults := DefaultReinvestmentParams()

	// Mortgage rate source
	if params.AcquisitionMarket != nil && params.AcquisitionMarket.MortgageRate != nil {
		assumptions.MortgageSource = "FRED 30Y Fixed"
	} else if params.MortgageRate == defaults.MortgageRate {
		assumptions.MortgageSource = fmt.Sprintf("Default (%.1f%%)", defaults.MortgageRate)
	} else {
		assumptions.MortgageSource = "User-specified"
	}

	// Appreciation source - check if we have market data
	marketLookup := m.buildMarketDataLookup(params.MarketQuality)
	if len(params.Properties) > 0 {
		// Get the most common location for reporting
		locationCounts := make(map[string]int)
		for _, p := range params.Properties {
			loc := m.getPropertyLocation(p)
			locationCounts[loc]++
		}
		var mostCommonLocation string
		maxCount := 0
		for loc, count := range locationCounts {
			if count > maxCount {
				maxCount = count
				mostCommonLocation = loc
			}
		}

		if marketData, ok := marketLookup[mostCommonLocation]; ok && marketData.PriceGrowth5Y != nil {
			// We have market data - calculate weighted average for display
			avgRate := m.calculateWeightedAverageAppreciation(params.Properties, marketLookup, params.AppreciationRate)
			assumptions.AppreciationRate = avgRate
			assumptions.AppreciationSource = "Market Data 5Y CAGR (portfolio-weighted)"
		} else {
			assumptions.AppreciationSource = fmt.Sprintf("Default (%.1f%%)", defaults.AppreciationRate)
			assumptions.DataQualityNotes = append(assumptions.DataQualityNotes,
				"Appreciation rate using historical average (market data unavailable)")
		}

		// Same for rent growth
		if marketData, ok := marketLookup[mostCommonLocation]; ok && marketData.RentGrowth5Y != nil {
			assumptions.RentGrowthSource = "Market Data 5Y CAGR (portfolio-weighted)"
		} else {
			assumptions.RentGrowthSource = fmt.Sprintf("Default (%.1f%%)", defaults.RentGrowthRate)
			assumptions.DataQualityNotes = append(assumptions.DataQualityNotes,
				"Rent growth rate using historical average (market data unavailable)")
		}
	} else {
		assumptions.AppreciationSource = fmt.Sprintf("Default (%.1f%%)", defaults.AppreciationRate)
		assumptions.RentGrowthSource = fmt.Sprintf("Default (%.1f%%)", defaults.RentGrowthRate)
	}

	// Acquisition price/rent source
	if params.AcquisitionMarket != nil && params.AcquisitionMarket.MedianHomePrice != nil {
		assumptions.AcquisitionPrice = *params.AcquisitionMarket.MedianHomePrice
		assumptions.AcquisitionPriceSource = "Market Data Median"
	} else if len(params.Properties) > 0 {
		assumptions.AcquisitionPriceSource = "Portfolio Average"
	} else {
		assumptions.AcquisitionPriceSource = fmt.Sprintf("Default ($%d)", defaults.AvgPropertyPrice)
		assumptions.DataQualityNotes = append(assumptions.DataQualityNotes,
			"Acquisition price using default (no portfolio or market data)")
	}

	if params.AcquisitionMarket != nil && params.AcquisitionMarket.MedianRent != nil {
		assumptions.AcquisitionRent = *params.AcquisitionMarket.MedianRent
		assumptions.AcquisitionRentSource = "Market Data Median"
	} else if len(params.Properties) > 0 {
		assumptions.AcquisitionRentSource = "Portfolio Average"
	} else {
		assumptions.AcquisitionRentSource = fmt.Sprintf("Default ($%d/mo)", defaults.AvgPropertyRent)
		assumptions.DataQualityNotes = append(assumptions.DataQualityNotes,
			"Acquisition rent using default (no portfolio or market data)")
	}

	// Acquisition location
	if params.AcquisitionMarket != nil && params.AcquisitionMarket.TargetCity != "" {
		assumptions.AcquisitionLocation = fmt.Sprintf("%s, %s",
			params.AcquisitionMarket.TargetCity,
			params.AcquisitionMarket.TargetState)
	} else if len(params.Properties) > 0 {
		// Use most common location from portfolio
		locationCounts := make(map[string]int)
		for _, p := range params.Properties {
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
		assumptions.AcquisitionLocation = mostCommon + " (portfolio-based)"
	} else {
		assumptions.AcquisitionLocation = "Various (no location data)"
	}

	// Calculate expense breakdown for acquisitions
	expenseBreakdown := m.buildExpenseBreakdown(params)
	if expenseBreakdown != nil {
		assumptions.ExpenseBreakdown = expenseBreakdown
		assumptions.ExpenseRatio = expenseBreakdown.VacancyRate + expenseBreakdown.ManagementRate +
			((expenseBreakdown.PropertyTaxRate + expenseBreakdown.InsuranceRate + expenseBreakdown.MaintenanceRate) *
				float64(params.AvgPropertyPrice) / float64(params.AvgPropertyRent*12) * 100)
		assumptions.ExpenseSource = "State-specific calculation"
	} else {
		assumptions.ExpenseRatio = params.OperatingExpenses * 100
		assumptions.ExpenseSource = fmt.Sprintf("Default (%.0f%%)", params.OperatingExpenses*100)
		assumptions.DataQualityNotes = append(assumptions.DataQualityNotes,
			"Operating expenses using simplified estimate (detailed calculation unavailable)")
	}

	// Calculate confidence score (0-100)
	assumptions.OverallConfidence = m.calculateConfidenceScore(assumptions)

	return assumptions
}

// buildExpenseBreakdown creates detailed expense breakdown for acquisitions
func (m *ReinvestmentModeler) buildExpenseBreakdown(params ReinvestmentParams) *investment.ProjectionExpenseBreakdown {
	// Determine target state for expense calculation
	var targetState, targetCity string

	if params.AcquisitionMarket != nil && params.AcquisitionMarket.TargetState != "" {
		targetState = params.AcquisitionMarket.TargetState
		targetCity = params.AcquisitionMarket.TargetCity
	} else if len(params.Properties) > 0 {
		// Use most common state from portfolio
		stateCounts := make(map[string]int)
		cityCounts := make(map[string]string) // state -> city
		for _, p := range params.Properties {
			stateCounts[p.Property.State]++
			cityCounts[p.Property.State] = p.Property.City
		}
		maxCount := 0
		for state, count := range stateCounts {
			if count > maxCount {
				maxCount = count
				targetState = state
				targetCity = cityCounts[state]
			}
		}
	}

	if targetState == "" {
		return nil // Can't calculate without state
	}

	// Use expense calculator
	input := expenses.PropertyInput{
		Price:         params.AvgPropertyPrice,
		State:         targetState,
		City:          targetCity,
		YearBuilt:     time.Now().Year() - 10, // Assume 10-year-old property for acquisitions
		EstimatedRent: params.AvgPropertyRent,
	}

	// Get vacancy rate from market data if available
	if params.AcquisitionMarket != nil && params.AcquisitionMarket.VacancyRate != nil {
		input.VacancyRateOverride = params.AcquisitionMarket.VacancyRate
	}

	result, err := m.expenseCalc.Calculate(input)
	if err != nil {
		m.logger.Warn("expense calculation failed, using defaults",
			"error", err,
			"state", targetState,
		)
		return nil
	}

	// Get market class info for display
	marketClass, _, _ := m.expenseCalc.GetMarketClassInfo(targetCity)

	// Determine age category
	ageCategory := "established" // 10-year-old property
	propertyAge := time.Now().Year() - input.YearBuilt
	switch {
	case propertyAge <= 5:
		ageCategory = "new"
	case propertyAge <= 15:
		ageCategory = "modern"
	case propertyAge <= 30:
		ageCategory = "established"
	case propertyAge <= 50:
		ageCategory = "older"
	default:
		ageCategory = "historic"
	}

	// Determine vacancy source
	vacancySource := "National Average"
	if params.AcquisitionMarket != nil && params.AcquisitionMarket.VacancyRate != nil {
		vacancySource = "FRED Local"
	}

	return &investment.ProjectionExpenseBreakdown{
		PropertyTaxRate:  result.PropertyTaxRate,
		PropertyTaxState: targetState,
		InsuranceRate:    result.InsuranceRate,
		InsuranceState:   targetState,
		MaintenanceRate:  result.MaintenanceRate,
		MaintenanceAge:   ageCategory,
		VacancyRate:      result.VacancyRate,
		VacancySource:    vacancySource,
		ManagementRate:   result.PropertyMgmtRate,
		MarketClass:      string(marketClass),
	}
}

// calculateConfidenceScore calculates an overall confidence score (0-100) based on data quality
func (m *ReinvestmentModeler) calculateConfidenceScore(assumptions *investment.ProjectionAssumptions) float64 {
	score := 100.0

	// Deduct for each data quality note (indicates fallback/default used)
	deductionPerNote := 15.0
	score -= float64(len(assumptions.DataQualityNotes)) * deductionPerNote

	// Bonus for market-based sources
	if strings.Contains(assumptions.AppreciationSource, "Market Data") {
		score += 5
	}
	if strings.Contains(assumptions.AcquisitionPriceSource, "Market Data") {
		score += 5
	}
	if strings.Contains(assumptions.MortgageSource, "FRED") {
		score += 5
	}
	if assumptions.ExpenseBreakdown != nil {
		score += 10 // Detailed expense calculation available
	}

	// Clamp to 0-100
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return score
}

// buildMarketDataLookup creates a map for quick location-based market data lookup
func (m *ReinvestmentModeler) buildMarketDataLookup(marketQuality []investment.LocationMarketAnalysis) map[string]*investment.LocationMarketAnalysis {
	lookup := make(map[string]*investment.LocationMarketAnalysis, len(marketQuality))
	for i := range marketQuality {
		// Store by location string (e.g., "Austin, TX")
		lookup[strings.ToLower(marketQuality[i].Location)] = &marketQuality[i]
	}
	return lookup
}

// getPropertyLocation returns a normalized location key for a property
func (m *ReinvestmentModeler) getPropertyLocation(prop investment.PropertyInPortfolio) string {
	return strings.ToLower(fmt.Sprintf("%s, %s", prop.Property.City, prop.Property.State))
}

// getAppreciationRateForProperty returns the annual appreciation rate for a property
// based on its location's 5-year price growth, converted to annual rate
func (m *ReinvestmentModeler) getAppreciationRateForProperty(
	prop investment.PropertyInPortfolio,
	marketLookup map[string]*investment.LocationMarketAnalysis,
	defaultRate float64,
) float64 {
	location := m.getPropertyLocation(prop)
	if marketData, ok := marketLookup[location]; ok && marketData.PriceGrowth5Y != nil {
		// Convert 5-year total growth to annual rate using CAGR formula
		// If 5Y growth is 25%, annual rate = (1.25)^(1/5) - 1 = ~4.56%
		totalGrowth := *marketData.PriceGrowth5Y / 100 // Convert from percent to decimal
		if totalGrowth > -1 {                          // Ensure valid for math
			annualRate := (math.Pow(1+totalGrowth, 0.2) - 1) * 100 // Convert back to percent
			m.logger.Debug("using market-based appreciation rate",
				"location", location,
				"priceGrowth5Y", *marketData.PriceGrowth5Y,
				"annualRate", annualRate,
			)
			return annualRate
		}
	}
	m.logger.Debug("using default appreciation rate",
		"location", location,
		"defaultRate", defaultRate,
	)
	return defaultRate
}

// getRentGrowthRateForProperty returns the annual rent growth rate for a property
// based on its location's 5-year rent growth, converted to annual rate
func (m *ReinvestmentModeler) getRentGrowthRateForProperty(
	prop investment.PropertyInPortfolio,
	marketLookup map[string]*investment.LocationMarketAnalysis,
	defaultRate float64,
) float64 {
	location := m.getPropertyLocation(prop)
	if marketData, ok := marketLookup[location]; ok && marketData.RentGrowth5Y != nil {
		// Convert 5-year total growth to annual rate using CAGR formula
		totalGrowth := *marketData.RentGrowth5Y / 100 // Convert from percent to decimal
		if totalGrowth > -1 {                         // Ensure valid for math
			annualRate := (math.Pow(1+totalGrowth, 0.2) - 1) * 100 // Convert back to percent
			m.logger.Debug("using market-based rent growth rate",
				"location", location,
				"rentGrowth5Y", *marketData.RentGrowth5Y,
				"annualRate", annualRate,
			)
			return annualRate
		}
	}
	m.logger.Debug("using default rent growth rate",
		"location", location,
		"defaultRate", defaultRate,
	)
	return defaultRate
}

// calculateWeightedAverageAppreciation calculates portfolio-weighted average appreciation rate
func (m *ReinvestmentModeler) calculateWeightedAverageAppreciation(
	properties []investment.PropertyInPortfolio,
	marketLookup map[string]*investment.LocationMarketAnalysis,
	defaultRate float64,
) float64 {
	if len(properties) == 0 {
		return defaultRate
	}

	totalValue := 0
	weightedSum := 0.0

	for _, p := range properties {
		rate := m.getAppreciationRateForProperty(p, marketLookup, defaultRate)
		totalValue += p.Property.Price
		weightedSum += float64(p.Property.Price) * rate
	}

	if totalValue == 0 {
		return defaultRate
	}

	return weightedSum / float64(totalValue)
}

// projectWithoutReinvestment models portfolio WITH yearly budget investments
// but WITHOUT reinvesting cash flows (user keeps cash flow as income)
func (m *ReinvestmentModeler) projectWithoutReinvestment(
	params ReinvestmentParams,
) []investment.YearlyProjection {
	projections := make([]investment.YearlyProjection, params.ProjectionYears)

	// Build market data lookup for location-specific rates
	marketLookup := m.buildMarketDataLookup(params.MarketQuality)

	// Build yearly budget lookup (year -> budget amount)
	yearlyBudgetMap := make(map[int]int)
	for _, yb := range params.YearlyBudgets {
		yearlyBudgetMap[yb.Year] = yb.Budget
	}

	// Track evolving portfolio (properties can be added each year from yearly budgets)
	currentProperties := make([]investment.PropertyInPortfolio, len(params.Properties))
	copy(currentProperties, params.Properties)

	// Store original property data and per-property rates
	propertyDataList := make([]propertyProjectionData, len(params.Properties))
	for i, p := range params.Properties {
		propertyDataList[i] = propertyProjectionData{
			originalPrice:    p.Property.Price,
			originalLoan:     p.LoanAmount,
			appreciationRate: m.getAppreciationRateForProperty(p, marketLookup, params.AppreciationRate),
			rentGrowthRate:   m.getRentGrowthRateForProperty(p, marketLookup, params.RentGrowthRate),
			originalRent:     p.Property.EstimatedRent,
			monthlyPayment:   p.MonthlyPayment,
			acquisitionYear:  0, // Year 0 = initial portfolio
		}
	}

	// Calculate weighted average appreciation for simulated acquisitions
	avgAppreciationRate := m.calculateWeightedAverageAppreciation(params.Properties, marketLookup, params.AppreciationRate)

	cumulativeCashFlow := 0
	prevYearValue := m.calculatePortfolioValue(params.Properties)

	for year := 1; year <= params.ProjectionYears; year++ {
		// Check if there's a yearly budget for this year (Year 2+)
		// Note: Year 1 properties are already in params.Properties
		if yearBudget, ok := yearlyBudgetMap[year]; ok && year > 1 && yearBudget >= params.MinDownPayment {
			// Simulate acquisitions from yearly budget (not from cash flow reinvestment)
			acquisitionPool := yearBudget
			for acquisitionPool >= params.MinDownPayment {
				newProperty := m.simulateAcquisitionWithRate(params, avgAppreciationRate)
				currentProperties = append(currentProperties, newProperty)

				propertyDataList = append(propertyDataList, propertyProjectionData{
					originalPrice:    newProperty.Property.Price,
					originalLoan:     newProperty.LoanAmount,
					appreciationRate: avgAppreciationRate,
					rentGrowthRate:   params.RentGrowthRate,
					originalRent:     newProperty.Property.EstimatedRent,
					monthlyPayment:   newProperty.MonthlyPayment,
					acquisitionYear:  year,
				})

				acquisitionPool -= int(float64(params.AvgPropertyPrice) * params.DownPaymentPct)
				m.logger.Debug("base scenario: simulated acquisition from yearly budget",
					"year", year,
					"yearBudget", yearBudget,
					"totalProperties", len(currentProperties),
				)
			}
		}

		// Calculate current portfolio value with per-property appreciation
		yearValue := 0
		loanBalance := 0
		annualCashFlow := 0

		for i := range currentProperties {
			if i < len(propertyDataList) {
				pd := propertyDataList[i]
				yearsOwned := year - pd.acquisitionYear
				if yearsOwned < 1 {
					yearsOwned = 1
				}

				// Apply property-specific appreciation rate
				appreciationFactor := math.Pow(1+pd.appreciationRate/100, float64(yearsOwned))
				propertyValue := int(float64(pd.originalPrice) * appreciationFactor)
				yearValue += propertyValue

				// Calculate loan paydown
				paydownFactor := float64(yearsOwned) / 30.0
				propLoanBalance := int(float64(pd.originalLoan) * (1 - paydownFactor*0.02))
				loanBalance += propLoanBalance

				// Calculate cash flow with property-specific rent growth
				rentGrowthFactor := math.Pow(1+pd.rentGrowthRate/100, float64(yearsOwned))
				adjustedRent := int(float64(pd.originalRent) * rentGrowthFactor)
				expenses := int(float64(adjustedRent) * params.OperatingExpenses)
				propCashFlow := (adjustedRent - expenses - pd.monthlyPayment) * 12
				annualCashFlow += propCashFlow
			}
		}

		// Calculate equity
		equity := yearValue - loanBalance

		cumulativeCashFlow += annualCashFlow

		// Calculate appreciation for the year
		yearAppreciation := yearValue - prevYearValue
		prevYearValue = yearValue

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

// projectWithReinvestment models portfolio with yearly budgets AND cash flow reinvestment
func (m *ReinvestmentModeler) projectWithReinvestment(
	params ReinvestmentParams,
) []investment.YearlyProjection {
	projections := make([]investment.YearlyProjection, params.ProjectionYears)

	// Build market data lookup for location-specific rates
	marketLookup := m.buildMarketDataLookup(params.MarketQuality)

	// Build yearly budget lookup (year -> budget amount)
	yearlyBudgetMap := make(map[int]int)
	for _, yb := range params.YearlyBudgets {
		yearlyBudgetMap[yb.Year] = yb.Budget
	}

	// Track evolving portfolio
	currentProperties := make([]investment.PropertyInPortfolio, len(params.Properties))
	copy(currentProperties, params.Properties)

	acquisitionPool := 0 // Accumulated cash for acquisition (from reinvestment)
	cumulativeCashFlow := 0
	propertiesAcquired := 0

	// Store original property data and per-property rates
	propertyDataList := make([]propertyProjectionData, len(params.Properties))
	for i, p := range params.Properties {
		propertyDataList[i] = propertyProjectionData{
			originalPrice:    p.Property.Price,
			originalLoan:     p.LoanAmount,
			appreciationRate: m.getAppreciationRateForProperty(p, marketLookup, params.AppreciationRate),
			rentGrowthRate:   m.getRentGrowthRateForProperty(p, marketLookup, params.RentGrowthRate),
			originalRent:     p.Property.EstimatedRent,
			monthlyPayment:   p.MonthlyPayment,
			acquisitionYear:  0,
		}
	}

	// Calculate weighted average appreciation for simulated acquisitions
	avgAppreciationRate := m.calculateWeightedAverageAppreciation(params.Properties, marketLookup, params.AppreciationRate)

	prevYearValue := m.calculatePortfolioValue(params.Properties)

	for year := 1; year <= params.ProjectionYears; year++ {
		// Add yearly budget to acquisition pool (Year 2+ only, Year 1 already in params.Properties)
		if yearBudget, ok := yearlyBudgetMap[year]; ok && year > 1 {
			acquisitionPool += yearBudget
			m.logger.Debug("reinvest scenario: added yearly budget to acquisition pool",
				"year", year,
				"yearBudget", yearBudget,
				"acquisitionPool", acquisitionPool,
			)
		}

		// Calculate current portfolio value with per-property appreciation
		portfolioValue := 0
		totalLoanBalance := 0

		for i := range currentProperties {
			if i < len(propertyDataList) {
				pd := propertyDataList[i]
				yearsOwned := year - pd.acquisitionYear
				if yearsOwned < 1 {
					yearsOwned = 1
				}

				// Apply property-specific appreciation rate
				appreciationFactor := math.Pow(1+pd.appreciationRate/100, float64(yearsOwned))
				currentProperties[i].Property.Price = int(float64(pd.originalPrice) * appreciationFactor)

				// Calculate loan balance using original loan amount
				paydownFactor := float64(yearsOwned) / 30.0
				currentProperties[i].LoanAmount = int(float64(pd.originalLoan) * (1 - paydownFactor*0.02))
			}
			// For newly acquired properties not yet in propertyDataList, values are set by simulateAcquisition
			portfolioValue += currentProperties[i].Property.Price
			totalLoanBalance += currentProperties[i].LoanAmount
		}

		// Calculate annual cash flow with per-property rent growth
		annualCashFlow := m.calculateAnnualCashFlowForYearWithRates(currentProperties, propertyDataList, params, year)

		// Add reinvested cash flow to acquisition pool
		reinvestAmount := int(float64(annualCashFlow) * params.ReinvestmentRate / 100)
		acquisitionPool += reinvestAmount

		// Acquire as many properties as possible with the acquisition pool
		for acquisitionPool >= params.MinDownPayment {
			// Simulate acquisition with average appreciation rate
			newProperty := m.simulateAcquisitionWithRate(params, avgAppreciationRate)
			currentProperties = append(currentProperties, newProperty)

			// Add property data for the new acquisition
			propertyDataList = append(propertyDataList, propertyProjectionData{
				originalPrice:    newProperty.Property.Price,
				originalLoan:     newProperty.LoanAmount,
				appreciationRate: avgAppreciationRate,
				rentGrowthRate:   params.RentGrowthRate, // Use default for simulated
				originalRent:     newProperty.Property.EstimatedRent,
				monthlyPayment:   newProperty.MonthlyPayment,
				acquisitionYear:  year,
			})

			// Deduct from pool
			acquisitionPool -= int(float64(params.AvgPropertyPrice) * params.DownPaymentPct)
			propertiesAcquired++

			m.logger.Debug("reinvest scenario: simulated property acquisition",
				"year", year,
				"totalProperties", len(currentProperties),
				"appreciationRate", avgAppreciationRate,
				"remainingPool", acquisitionPool,
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
		yearAppreciation := portfolioValue - prevYearValue
		prevYearValue = portfolioValue

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
	return m.simulateAcquisitionWithRate(params, params.AppreciationRate)
}

// simulateAcquisitionWithRate creates a simulated property acquisition with a specific appreciation rate
func (m *ReinvestmentModeler) simulateAcquisitionWithRate(params ReinvestmentParams, appreciationRate float64) investment.PropertyInPortfolio {
	downPayment := int(float64(params.AvgPropertyPrice) * params.DownPaymentPct)
	loanAmount := params.AvgPropertyPrice - downPayment

	// Calculate monthly mortgage
	monthlyRate := params.MortgageRate / 100 / 12
	numPayments := 360.0
	monthlyMortgage := float64(loanAmount) * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) /
		(math.Pow(1+monthlyRate, numPayments) - 1)

	// Determine target state/city for expense calculation
	var targetState, targetCity string
	if params.AcquisitionMarket != nil && params.AcquisitionMarket.TargetState != "" {
		targetState = params.AcquisitionMarket.TargetState
		targetCity = params.AcquisitionMarket.TargetCity
	} else if len(params.Properties) > 0 {
		// Use most common state from portfolio
		stateCounts := make(map[string]int)
		cityCounts := make(map[string]string)
		for _, p := range params.Properties {
			stateCounts[p.Property.State]++
			cityCounts[p.Property.State] = p.Property.City
		}
		maxCount := 0
		for state, count := range stateCounts {
			if count > maxCount {
				maxCount = count
				targetState = state
				targetCity = cityCounts[state]
			}
		}
	}

	// Calculate monthly expenses using expense calculator if possible
	monthlyRent := params.AvgPropertyRent
	var monthlyExpenses int
	var annualNOI float64

	if targetState != "" {
		// Try to use detailed expense calculator
		input := expenses.PropertyInput{
			Price:         params.AvgPropertyPrice,
			State:         targetState,
			City:          targetCity,
			YearBuilt:     time.Now().Year() - 10, // Assume 10-year-old property
			EstimatedRent: params.AvgPropertyRent,
		}

		// Use market vacancy rate if available
		if params.AcquisitionMarket != nil && params.AcquisitionMarket.VacancyRate != nil {
			input.VacancyRateOverride = params.AcquisitionMarket.VacancyRate
		}

		expenseResult, err := m.expenseCalc.Calculate(input)
		if err == nil {
			monthlyExpenses = int(expenseResult.TotalMonthly)
			annualNOI = expenseResult.NOI
			m.logger.Debug("using detailed expense calculation for simulated acquisition",
				"state", targetState,
				"expenseRatio", expenseResult.ExpenseRatio,
				"monthlyExpenses", monthlyExpenses,
			)
		} else {
			// Fall back to flat percentage
			monthlyExpenses = int(float64(monthlyRent) * params.OperatingExpenses)
			annualNOI = float64(monthlyRent) * 12 * (1 - params.OperatingExpenses)
			m.logger.Warn("expense calculation failed, using flat percentage",
				"error", err,
				"operatingExpenses", params.OperatingExpenses,
			)
		}
	} else {
		// No state info, use flat percentage
		monthlyExpenses = int(float64(monthlyRent) * params.OperatingExpenses)
		annualNOI = float64(monthlyRent) * 12 * (1 - params.OperatingExpenses)
	}

	monthlyCashFlow := monthlyRent - monthlyExpenses - int(monthlyMortgage)
	capRate := (annualNOI / float64(params.AvgPropertyPrice)) * 100

	// Use target location in property if available
	city := "Various"
	state := "US"
	if targetCity != "" {
		city = targetCity
	}
	if targetState != "" {
		state = targetState
	}

	return investment.PropertyInPortfolio{
		Property: investment.Property{
			ID:            "simulated",
			Address:       "Simulated Acquisition",
			City:          city,
			State:         state,
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

// propertyProjectionData holds per-property data for projection calculations
type propertyProjectionData struct {
	originalPrice    int
	originalLoan     int
	appreciationRate float64
	rentGrowthRate   float64
	originalRent     int
	monthlyPayment   int
	acquisitionYear  int
}

func (m *ReinvestmentModeler) calculateAnnualCashFlowForYearWithRates(
	properties []investment.PropertyInPortfolio,
	propertyDataList []propertyProjectionData,
	params ReinvestmentParams,
	year int,
) int {
	total := 0

	for i, p := range properties {
		var rentGrowthRate float64
		var originalRent int
		var monthlyPayment int
		var acquisitionYear int

		if i < len(propertyDataList) {
			pd := propertyDataList[i]
			rentGrowthRate = pd.rentGrowthRate
			originalRent = pd.originalRent
			monthlyPayment = pd.monthlyPayment
			acquisitionYear = pd.acquisitionYear
		} else {
			// Fallback for properties without data
			rentGrowthRate = params.RentGrowthRate
			originalRent = p.Property.EstimatedRent
			monthlyPayment = p.MonthlyPayment
			acquisitionYear = 0
		}

		// Calculate years since acquisition
		yearsOwned := year - acquisitionYear
		if yearsOwned < 1 {
			yearsOwned = 1
		}

		// Apply property-specific rent growth
		rentGrowthFactor := math.Pow(1+rentGrowthRate/100, float64(yearsOwned))
		adjustedRent := int(float64(originalRent) * rentGrowthFactor)
		expenses := int(float64(adjustedRent) * params.OperatingExpenses)
		cashFlow := adjustedRent - expenses - monthlyPayment
		total += cashFlow * 12
	}

	return total
}
