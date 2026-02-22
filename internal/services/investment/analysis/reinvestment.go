// Package analysis provides financial engineering services for investment planning.
// Part of ADR-059: Investment Planning Enhancement - Selection Mode
//
// Unified Cohort-Based Projection Engine with CapEx Reserves (ADR-059 + ADR-063)
//
// This file implements a single project() engine that serves both:
//   - Reinvestment analysis: base vs reinvest comparison (ModelReinvestment)
//   - Scenario projections: base/optimistic/pessimistic (ProjectScenarios)
//
// Key design decisions:
//   - Self-contained propertyCohort struct (no fragile parallel arrays)
//   - Split expense growth: value-based (appreciation) vs rent-based (rent growth)
//   - Per-property expense calculator (not flat 50% ratio)
//   - Proper amortization formula (not crude linear approximation)
//   - Component-lifecycle CapEx reserves (separate from routine maintenance)
//   - GrowthMultiplier for optimistic/pessimistic scenarios
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
	MortgageRate      float64 // Annual rate (e.g., 7.0 for 7%)
	DownPaymentPct    float64 // e.g., 0.20 for 20%
	AppreciationRate  float64 // Annual rate (e.g., 3.0 for 3%) - fallback if no market data
	RentGrowthRate    float64 // Annual rate (e.g., 2.0 for 2%) - fallback if no market data
	OperatingExpenses float64 // As % of rent (e.g., 0.50 for 50%) - fallback if expense calc fails
	MinDownPayment    int     // Minimum down payment for new acquisition
	AvgPropertyPrice  int     // Average price for acquisition simulation
	AvgPropertyRent   int     // Average rent for acquisition simulation
}

// DefaultReinvestmentParams returns default parameters
func DefaultReinvestmentParams() ReinvestmentParams {
	return ReinvestmentParams{
		ReinvestmentRate:  100, // Reinvest all surplus
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

// ============================================================================
// Unified Cohort Engine Types
// ============================================================================

// ProjectionConfig configures the unified projection engine.
// Used by both ModelReinvestment and ProjectScenarios.
type ProjectionConfig struct {
	// Core (required)
	Properties      []investment.PropertyInPortfolio
	ProjectionYears int
	MortgageRate    float64 // Annual rate as percentage (e.g., 7.0)
	DownPaymentPct  float64 // As decimal (e.g., 0.25)
	OperatingExpenses float64 // Fallback ratio if expense calculator fails (e.g., 0.50)

	// Reinvestment (optional — off for scenario projections)
	WithReinvestment bool
	ReinvestmentRate float64     // % of cash flow to reinvest
	YearlyBudgets    map[int]int // year → additional budget

	// Growth adjustment (1.0 = base, 1.15 = optimistic, 0.85 = pessimistic)
	GrowthMultiplier float64

	// Tax modeling (on for scenario projections, off for reinvestment comparison)
	IncludeTax bool

	// Market data
	MarketLookup map[string]*investment.LocationMarketAnalysis

	// Acquisition market data
	AcquisitionMarket *AcquisitionMarketData

	// Fallback growth rates
	DefaultAppreciationRate float64
	DefaultRentGrowthRate   float64

	// Average property data for simulated acquisitions
	AvgPropertyPrice int
	AvgPropertyRent  int
}

// propertyCohort is a self-contained group of properties acquired at the same time.
// Replaces the fragile parallel arrays (currentProperties + propertyDataList).
type propertyCohort struct {
	acquisitionYear int     // 0 = initial portfolio, 1+ = acquired during projection
	count           int     // Number of properties in this cohort
	propertyAge     int     // Age at acquisition (for CapEx tier calculation)
	state           string  // State (for expense calculator on simulated acquisitions)
	city            string  // City (for management fee tier)

	// Locked-in at acquisition time (per property)
	purchasePrice       int
	monthlyRent         int
	loanAmount          int
	monthlyMortgage     int
	monthlyMortgageRate float64 // Annual rate / 12 / 100 (for amortization calc)

	// Split expenses for accurate growth modeling
	// Value-based grow with appreciation rate; rent-based grow with rent growth rate
	monthlyValueExpenses int // Property tax + insurance + maintenance (per property, monthly)
	monthlyRentExpenses  int // Vacancy allowance + property management (per property, monthly)
	monthlyVacancy       int // Vacancy allowance only (per property, monthly) — for EGI breakdown

	// Growth rates (already multiplied by GrowthMultiplier)
	appreciationRate float64
	rentGrowthRate   float64
}

// yearResult is the internal projection result — superset of both output types.
type yearResult struct {
	// Common fields
	year, portfolioValue, equity, loanBalance                        int
	annualCashFlow, cumulativeCashFlow, appreciation                 int
	grossRentalIncome, vacancyLoss, operatingExpenses, netOperatingIncome, debtService int
	propertyCount, propertiesAcquired                                int
	capExReserve, cumulativeCapExReserve                             int
	cashBalance                                                      int

	// Tax fields (populated when config.IncludeTax = true)
	interestExpense, principalPayment, depreciation int
	taxableIncome, incomeTaxes, cashFlowAfterTax    int
	cashOnCash, capRate, equityMultiple             float64
}

// annualBreakdown holds detailed income/expense breakdown for a year
type annualBreakdown struct {
	grossRentalIncome  int // Sum of (adjustedMonthlyRent × 12) for all properties
	vacancyLoss        int // Vacancy portion of operating expenses
	operatingExpenses  int // Sum of (monthlyExpenses × 12) for all properties
	netOperatingIncome int // grossRentalIncome - operatingExpenses
	debtService        int // Sum of (monthlyPayment × 12) for all properties
	cashFlow           int // netOperatingIncome - debtService
}

// ============================================================================
// ModelReinvestment — Public API
// ============================================================================

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

	// Build configs for base and reinvest scenarios
	baseConfig := m.configFromParams(params, false, 1.0, false)
	reinvestConfig := m.configFromParams(params, true, 1.0, false)

	// Run unified projection engine
	baseResults := m.project(baseConfig)
	reinvestResults := m.project(reinvestConfig)

	// Map to YearlyProjection
	baseScenario := make([]investment.YearlyProjection, len(baseResults))
	for i, r := range baseResults {
		baseScenario[i] = r.toYearlyProjection()
	}
	reinvestScenario := make([]investment.YearlyProjection, len(reinvestResults))
	for i, r := range reinvestResults {
		reinvestScenario[i] = r.toYearlyProjection()
	}

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

// ============================================================================
// ProjectScenarios — Public API (replaces CalculateAllScenarios)
// ============================================================================

// ProjectScenarios generates base, optimistic, and pessimistic projections
// using the unified cohort engine. This replaces projection.Calculator.CalculateAllScenarios().
func (m *ReinvestmentModeler) ProjectScenarios(
	properties []investment.PropertyInPortfolio,
	years int,
	marketLookup map[string]*investment.LocationMarketAnalysis,
	userAssumptions *investment.UserFinancialAssumptions,
	yearlyBudgets []investment.YearlyBudget,
	acquisitionMarket *AcquisitionMarketData,
) *investment.ScenarioProjections {
	if len(properties) == 0 || years <= 0 {
		return nil
	}

	// Get assumptions from user or defaults
	mortgageRate := 7.0
	downPaymentPct := 0.20
	operatingExpenses := 0.50
	if userAssumptions != nil {
		if userAssumptions.MortgageRate > 0 {
			mortgageRate = userAssumptions.MortgageRate
		}
		if userAssumptions.DownPaymentPercent > 0 {
			downPaymentPct = userAssumptions.DownPaymentPercent / 100
		}
		if userAssumptions.OperatingExpenses > 0 {
			operatingExpenses = userAssumptions.OperatingExpenses / 100
		}
	}

	// Convert yearlyBudgets slice to map[int]int for projection config
	budgetMap := make(map[int]int)
	for _, yb := range yearlyBudgets {
		budgetMap[yb.Year] = yb.Budget
	}

	// Enable reinvestment if there are yearly budgets (fresh capital injections)
	withReinvestment := len(yearlyBudgets) > 0

	baseConfig := ProjectionConfig{
		Properties:              properties,
		ProjectionYears:         years,
		MortgageRate:            mortgageRate,
		DownPaymentPct:          downPaymentPct,
		OperatingExpenses:       operatingExpenses,
		WithReinvestment:        withReinvestment,
		YearlyBudgets:           budgetMap,
		GrowthMultiplier:        1.0,
		IncludeTax:              true,
		MarketLookup:            marketLookup,
		AcquisitionMarket:       acquisitionMarket,
		DefaultAppreciationRate: 3.0,
		DefaultRentGrowthRate:   2.0,
	}

	baseResults := m.project(baseConfig)

	optConfig := baseConfig
	optConfig.GrowthMultiplier = 1.15
	optResults := m.project(optConfig)

	pessConfig := baseConfig
	pessConfig.GrowthMultiplier = 0.85
	pessResults := m.project(pessConfig)

	// Map to ExpandedYearProjection
	base := mapToExpanded(baseResults)
	optimistic := mapToExpanded(optResults)
	pessimistic := mapToExpanded(pessResults)

	// Build assumptions and summary
	assumptions := m.buildScenarioAssumptions(properties, marketLookup, userAssumptions, mortgageRate)
	summary := m.buildScenarioSummary(base, optimistic, pessimistic, properties)

	return &investment.ScenarioProjections{
		Base:        base,
		Optimistic:  optimistic,
		Pessimistic: pessimistic,
		Assumptions: assumptions,
		Summary:     summary,
	}
}

// ============================================================================
// Unified project() Engine
// ============================================================================

// project is the unified projection engine. All projection paths flow through here.
func (m *ReinvestmentModeler) project(config ProjectionConfig) []yearResult {
	if len(config.Properties) == 0 || config.ProjectionYears <= 0 {
		return nil
	}

	// Build initial cohorts from portfolio properties
	cohorts := m.buildInitialCohorts(config)

	// Apply growth multiplier to cohort rates
	for i := range cohorts {
		cohorts[i].appreciationRate *= config.GrowthMultiplier
		cohorts[i].rentGrowthRate *= config.GrowthMultiplier
	}

	acquisitionPool := 0
	capExReservePool := 0
	cumulativeCashFlow := 0
	prevYearValue := m.calculatePortfolioValue(config.Properties)

	// Calculate average appreciation for simulated acquisitions
	avgAppreciationRate := m.calculateWeightedAverageAppreciation(
		config.Properties, config.MarketLookup, config.DefaultAppreciationRate,
	) * config.GrowthMultiplier
	avgRentGrowthRate := config.DefaultRentGrowthRate * config.GrowthMultiplier

	// Calculate initial down payment for CashOnCash metric
	initialDownPayment := 0
	for _, p := range config.Properties {
		initialDownPayment += p.DownPayment
	}

	// Determine average state/city for acquisitions
	avgState, avgCity := m.getMostCommonStateCity(config.Properties)
	if config.AcquisitionMarket != nil && config.AcquisitionMarket.TargetState != "" {
		avgState = config.AcquisitionMarket.TargetState
		avgCity = config.AcquisitionMarket.TargetCity
	}

	results := make([]yearResult, config.ProjectionYears)

	for year := 1; year <= config.ProjectionYears; year++ {
		propertiesAcquiredThisYear := 0

		// 1. Inject yearly budget (year 2+) — only in reinvestment mode or if budgets exist
		if config.YearlyBudgets != nil {
			if yearBudget, ok := config.YearlyBudgets[year]; ok && year > 1 {
				acquisitionPool += yearBudget
			}
		}

		// 2. Pre-acquisition breakdown (for reinvestment decision)
		preBreakdown := m.aggregateBreakdown(cohorts, year)
		preCapEx := m.aggregateCapExReserve(cohorts, year)

		// 3. Pool cash flow if reinvesting
		//    CapEx is deducted BEFORE reinvestment (can't reinvest reserved cash)
		if config.WithReinvestment {
			netForReinvest := preBreakdown.cashFlow - preCapEx
			reinvestAmount := int(float64(netForReinvest) * config.ReinvestmentRate / 100)
			if reinvestAmount > 0 {
				acquisitionPool += reinvestAmount
			}
		}

		// 4. Acquire properties from pool
		if acquisitionPool > 0 {
			avgPrice := config.AvgPropertyPrice
			if avgPrice == 0 {
				avgPrice = m.avgPrice(config.Properties)
			}
			avgRent := config.AvgPropertyRent
			if avgRent == 0 {
				avgRent = m.avgRent(config.Properties)
			}

			appreciatedPrice := int(float64(avgPrice) * math.Pow(1+avgAppreciationRate/100, float64(year)))
			downPayment := int(float64(appreciatedPrice) * config.DownPaymentPct)

			for acquisitionPool >= downPayment && downPayment > 0 {
				newCohort := m.buildAcquisitionCohort(config, avgAppreciationRate, avgRentGrowthRate, year, avgState, avgCity, avgPrice, avgRent)
				cohorts = append(cohorts, newCohort)
				acquisitionPool -= downPayment
				propertiesAcquiredThisYear++
			}
		}

		// 5. Post-acquisition calculations (for OUTPUT — includes new properties)
		fullBreakdown := m.aggregateBreakdown(cohorts, year)
		fullCapEx := m.aggregateCapExReserve(cohorts, year)
		portfolioValue, loanBalance := m.aggregateValues(cohorts, year)

		// 6. Accumulate reserves and cash flow
		capExReservePool += fullCapEx
		annualCashFlow := fullBreakdown.cashFlow - fullCapEx // NOI - Debt - CapEx
		cumulativeCashFlow += annualCashFlow

		cashBalance := 0
		if config.WithReinvestment && acquisitionPool > 0 {
			cashBalance = acquisitionPool
		}

		yearAppreciation := portfolioValue - prevYearValue
		prevYearValue = portfolioValue

		// Count total properties
		totalPropertyCount := 0
		for _, c := range cohorts {
			totalPropertyCount += c.count
		}

		// 7. Tax calculations (when config.IncludeTax)
		interestExp, principalPay := 0, 0
		deprec, taxIncome, taxes, afterTaxCF := 0, 0, 0, 0
		cocReturn, capRateVal, eqMultiple := 0.0, 0.0, 0.0

		if config.IncludeTax {
			interestExp, principalPay = m.aggregateDebtSplit(cohorts, year)
			deprec = int(float64(portfolioValue) * 0.80 / 27.5)
			taxIncome = fullBreakdown.netOperatingIncome - interestExp - deprec
			if taxIncome > 0 {
				taxes = int(float64(taxIncome) * 0.25)
			}
			afterTaxCF = annualCashFlow - taxes

			if initialDownPayment > 0 {
				cocReturn = float64(annualCashFlow) / float64(initialDownPayment) * 100
			}
			if portfolioValue > 0 {
				capRateVal = float64(fullBreakdown.netOperatingIncome) / float64(portfolioValue) * 100
			}
			equity := portfolioValue - loanBalance
			if initialDownPayment > 0 {
				eqMultiple = float64(equity) / float64(initialDownPayment)
			}
		}

		// 8. Build result
		results[year-1] = yearResult{
			year:                   year,
			portfolioValue:         portfolioValue,
			equity:                 portfolioValue - loanBalance,
			loanBalance:            loanBalance,
			annualCashFlow:         annualCashFlow,
			cumulativeCashFlow:     cumulativeCashFlow,
			appreciation:           yearAppreciation,
			cashBalance:            cashBalance,
			grossRentalIncome:      fullBreakdown.grossRentalIncome,
			vacancyLoss:            fullBreakdown.vacancyLoss,
			operatingExpenses:      fullBreakdown.operatingExpenses,
			netOperatingIncome:     fullBreakdown.netOperatingIncome,
			debtService:            fullBreakdown.debtService,
			propertyCount:          totalPropertyCount,
			propertiesAcquired:     propertiesAcquiredThisYear,
			capExReserve:           fullCapEx,
			cumulativeCapExReserve: capExReservePool,
			interestExpense:        interestExp,
			principalPayment:       principalPay,
			depreciation:           deprec,
			taxableIncome:          taxIncome,
			incomeTaxes:            taxes,
			cashFlowAfterTax:       afterTaxCF,
			cashOnCash:             cocReturn,
			capRate:                capRateVal,
			equityMultiple:         eqMultiple,
		}
	}

	return results
}

// ============================================================================
// Cohort Builders
// ============================================================================

// buildInitialCohorts creates cohorts from the initial portfolio properties.
func (m *ReinvestmentModeler) buildInitialCohorts(config ProjectionConfig) []propertyCohort {
	cohorts := make([]propertyCohort, 0, len(config.Properties))
	currentYear := time.Now().Year()

	for _, p := range config.Properties {
		appreciationRate := m.getAppreciationRateForProperty(p, config.MarketLookup, config.DefaultAppreciationRate)
		rentGrowthRate := m.getRentGrowthRateForProperty(p, config.MarketLookup, config.DefaultRentGrowthRate)

		// Calculate property age
		propertyAge := 15 // default if YearBuilt is 0
		if p.Property.YearBuilt > 0 {
			propertyAge = currentYear - p.Property.YearBuilt
		}

		// Calculate monthly mortgage rate for amortization
		monthlyMortgageRate := config.MortgageRate / 100 / 12

		// Use expense calculator per property for accurate split expenses
		var monthlyValueExpenses, monthlyRentExpenses int
		expResult, err := m.expenseCalc.Calculate(expenses.PropertyInput{
			Price:         p.Property.Price,
			State:         p.Property.State,
			City:          p.Property.City,
			YearBuilt:     p.Property.YearBuilt,
			EstimatedRent: p.Property.EstimatedRent,
		})
		var monthlyVacancy int
		if err != nil {
			// Fallback to ratio-based: all expenses as rent-based
			monthlyRentExpenses = int(float64(p.Property.EstimatedRent) * config.OperatingExpenses)
			monthlyValueExpenses = 0
			// Estimate vacancy as ~8% of rent (typical default)
			monthlyVacancy = int(float64(p.Property.EstimatedRent) * 0.08)
		} else {
			// Value-based: property tax + insurance + maintenance (monthly)
			annualValueExpenses := expResult.PropertyTax + expResult.Insurance + expResult.Maintenance
			monthlyValueExpenses = int(annualValueExpenses / 12)
			// Rent-based: vacancy + management (monthly)
			monthlyRentExpenses = int((expResult.VacancyAllowance + expResult.PropertyMgmt) / 12)
			monthlyVacancy = int(expResult.VacancyAllowance / 12)
		}

		cohorts = append(cohorts, propertyCohort{
			acquisitionYear:     0,
			count:               1,
			propertyAge:         propertyAge,
			state:               p.Property.State,
			city:                p.Property.City,
			purchasePrice:       p.Property.Price,
			monthlyRent:         p.Property.EstimatedRent,
			loanAmount:          p.LoanAmount,
			monthlyMortgage:     p.MonthlyPayment,
			monthlyMortgageRate: monthlyMortgageRate,
			monthlyValueExpenses: monthlyValueExpenses,
			monthlyRentExpenses:  monthlyRentExpenses,
			monthlyVacancy:       monthlyVacancy,
			appreciationRate:    appreciationRate,
			rentGrowthRate:      rentGrowthRate,
		})
	}

	return cohorts
}

// buildAcquisitionCohort creates a cohort for a property acquired during projection.
func (m *ReinvestmentModeler) buildAcquisitionCohort(
	config ProjectionConfig,
	appreciationRate float64,
	rentGrowthRate float64,
	year int,
	avgState string,
	avgCity string,
	avgPrice int,
	avgRent int,
) propertyCohort {
	// Use market data if available, fallback to portfolio averages
	basePrice := avgPrice
	baseRent := avgRent
	targetState := avgState
	targetCity := avgCity

	if config.AcquisitionMarket != nil {
		// Prefer market median prices over portfolio averages
		if config.AcquisitionMarket.MedianHomePrice != nil && *config.AcquisitionMarket.MedianHomePrice > 0 {
			basePrice = *config.AcquisitionMarket.MedianHomePrice
		}
		if config.AcquisitionMarket.MedianRent != nil && *config.AcquisitionMarket.MedianRent > 0 {
			baseRent = *config.AcquisitionMarket.MedianRent
		}
		if config.AcquisitionMarket.TargetState != "" {
			targetState = config.AcquisitionMarket.TargetState
		}
		if config.AcquisitionMarket.TargetCity != "" {
			targetCity = config.AcquisitionMarket.TargetCity
		}
	}

	// Project forward to acquisition year using market rates
	priceFactor := math.Pow(1+appreciationRate/100, float64(year))
	rentFactor := math.Pow(1+rentGrowthRate/100, float64(year))
	appreciatedPrice := int(float64(basePrice) * priceFactor)
	appreciatedRent := int(float64(baseRent) * rentFactor)

	// Data quality validation (reject suspicious acquisitions)
	// Cap rate check: 3-12% range
	annualRent := float64(appreciatedRent * 12)
	impliedCapRate := (annualRent * 0.6) / float64(appreciatedPrice) * 100 // Assume 40% expenses
	if impliedCapRate < 3.0 || impliedCapRate > 12.0 {
		// If market data yields suspicious metrics, use more conservative portfolio averages
		appreciatedPrice = int(float64(avgPrice) * priceFactor)
		appreciatedRent = int(float64(avgRent) * rentFactor)
	}

	// Gross yield check: should be under 18%
	grossYield := (float64(appreciatedRent*12) / float64(appreciatedPrice)) * 100
	if grossYield > 18.0 {
		appreciatedPrice = int(float64(avgPrice) * priceFactor)
		appreciatedRent = int(float64(avgRent) * rentFactor)
	}

	// Price-to-rent ratio check: 6-25 months
	priceToRent := float64(appreciatedPrice) / float64(appreciatedRent)
	if priceToRent < 6.0 || priceToRent > 25.0 {
		appreciatedPrice = int(float64(avgPrice) * priceFactor)
		appreciatedRent = int(float64(avgRent) * rentFactor)
	}

	// Financing
	downPayment := int(float64(appreciatedPrice) * config.DownPaymentPct)
	loanAmount := appreciatedPrice - downPayment

	// Monthly mortgage
	monthlyRate := config.MortgageRate / 100 / 12
	var monthlyMortgage int
	if monthlyRate > 0 {
		numPayments := 360.0
		pmt := float64(loanAmount) * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) /
			(math.Pow(1+monthlyRate, numPayments) - 1)
		monthlyMortgage = int(pmt)
	} else {
		monthlyMortgage = loanAmount / 360
	}

	// Property age: prefer newer properties (15 years vs hardcoded 10)
	estimatedAge := 15
	if year <= 3 {
		// Earlier acquisitions can target slightly older properties
		estimatedAge = 20
	}

	// Expenses using expense calculator with appreciated values
	var monthlyValueExpenses, monthlyRentExpenses, monthlyVacancy int
	if targetState != "" {
		expInput := expenses.PropertyInput{
			Price:         appreciatedPrice,
			State:         targetState,
			City:          targetCity,
			YearBuilt:     time.Now().Year() - estimatedAge,
			EstimatedRent: appreciatedRent,
		}
		if config.AcquisitionMarket != nil && config.AcquisitionMarket.VacancyRate != nil {
			expInput.VacancyRateOverride = config.AcquisitionMarket.VacancyRate
		}

		expResult, err := m.expenseCalc.Calculate(expInput)
		if err == nil {
			annualValueExpenses := expResult.PropertyTax + expResult.Insurance + expResult.Maintenance
			monthlyValueExpenses = int(annualValueExpenses / 12)
			monthlyRentExpenses = int((expResult.VacancyAllowance + expResult.PropertyMgmt) / 12)
			monthlyVacancy = int(expResult.VacancyAllowance / 12)
		} else {
			monthlyRentExpenses = int(float64(appreciatedRent) * config.OperatingExpenses)
			monthlyVacancy = int(float64(appreciatedRent) * 0.08)
		}
	} else {
		monthlyRentExpenses = int(float64(appreciatedRent) * config.OperatingExpenses)
		monthlyVacancy = int(float64(appreciatedRent) * 0.08)
	}

	return propertyCohort{
		acquisitionYear:      year,
		count:                1,
		propertyAge:          estimatedAge,
		state:                targetState,
		city:                 targetCity,
		purchasePrice:        appreciatedPrice,
		monthlyRent:          appreciatedRent,
		loanAmount:           loanAmount,
		monthlyMortgage:      monthlyMortgage,
		monthlyMortgageRate:  monthlyRate,
		monthlyValueExpenses: monthlyValueExpenses,
		monthlyRentExpenses:  monthlyRentExpenses,
		monthlyVacancy:       monthlyVacancy,
		appreciationRate:     appreciationRate,
		rentGrowthRate:       rentGrowthRate,
	}
}

// ============================================================================
// Aggregate Helpers
// ============================================================================

// aggregateBreakdown computes the annual income/expense breakdown across all cohorts.
func (m *ReinvestmentModeler) aggregateBreakdown(cohorts []propertyCohort, year int) annualBreakdown {
	var bd annualBreakdown
	for _, c := range cohorts {
		yearsOwned := max(1, year-c.acquisitionYear)

		// Rent grows with rent growth rate
		rentFactor := math.Pow(1+c.rentGrowthRate/100, float64(yearsOwned))
		adjustedRent := int(float64(c.monthlyRent)*rentFactor) * c.count

		// Value-based expenses (tax, insurance, maintenance) grow with appreciation
		valueFactor := math.Pow(1+c.appreciationRate/100, float64(yearsOwned))
		valueExp := int(float64(c.monthlyValueExpenses)*valueFactor) * c.count

		// Rent-based expenses (vacancy, management) grow with rent
		rentExp := int(float64(c.monthlyRentExpenses)*rentFactor) * c.count
		vacancyExp := int(float64(c.monthlyVacancy)*rentFactor) * c.count

		totalExpenses := valueExp + rentExp
		mortgage := c.monthlyMortgage * c.count

		bd.grossRentalIncome += adjustedRent * 12
		bd.vacancyLoss += vacancyExp * 12
		bd.operatingExpenses += totalExpenses * 12
		bd.debtService += mortgage * 12
	}
	bd.netOperatingIncome = bd.grossRentalIncome - bd.operatingExpenses
	bd.cashFlow = bd.netOperatingIncome - bd.debtService
	return bd
}

// aggregateValues computes total portfolio value and loan balance using proper amortization.
func (m *ReinvestmentModeler) aggregateValues(cohorts []propertyCohort, year int) (portfolioValue, loanBalance int) {
	for _, c := range cohorts {
		yearsOwned := max(1, year-c.acquisitionYear)

		// Property value: appreciated
		priceFactor := math.Pow(1+c.appreciationRate/100, float64(yearsOwned))
		value := int(float64(c.purchasePrice)*priceFactor) * c.count

		// Loan balance: proper amortization
		loan := remainingLoanBalance(c.loanAmount, c.monthlyMortgageRate, c.monthlyMortgage, yearsOwned) * c.count

		portfolioValue += value
		loanBalance += loan
	}
	return
}

// aggregateCapExReserve computes the total annual CapEx reserve across all cohorts.
func (m *ReinvestmentModeler) aggregateCapExReserve(cohorts []propertyCohort, year int) int {
	totalReserve := 0
	for _, c := range cohorts {
		yearsOwned := max(1, year-c.acquisitionYear)
		effectiveAge := c.propertyAge + yearsOwned

		// Property value at this year (for CapEx $ calculation)
		valueFactor := math.Pow(1+c.appreciationRate/100, float64(yearsOwned))
		propertyValue := float64(c.purchasePrice) * valueFactor

		// Age-adjusted CapEx rate
		capExRate := expenses.CapExReserveRate(effectiveAge) // returns e.g. 0.83
		annualReserve := int(propertyValue*capExRate/100) * c.count
		totalReserve += annualReserve
	}
	return totalReserve
}

// aggregateDebtSplit computes interest vs principal split for tax calculations.
func (m *ReinvestmentModeler) aggregateDebtSplit(cohorts []propertyCohort, year int) (interestExpense, principalPayment int) {
	for _, c := range cohorts {
		yearsOwned := max(1, year-c.acquisitionYear)
		startBalance := remainingLoanBalance(c.loanAmount, c.monthlyMortgageRate, c.monthlyMortgage, yearsOwned-1)
		endBalance := remainingLoanBalance(c.loanAmount, c.monthlyMortgageRate, c.monthlyMortgage, yearsOwned)
		annualPrincipal := (startBalance - endBalance) * c.count
		annualDebt := c.monthlyMortgage * 12 * c.count
		principalPayment += annualPrincipal
		interestExpense += annualDebt - annualPrincipal
	}
	return
}

// remainingLoanBalance calculates the remaining balance using standard amortization.
// Formula: B = P*(1+r)^n - PMT*[(1+r)^n - 1]/r
func remainingLoanBalance(principal int, monthlyRate float64, monthlyPayment int, yearsOwned int) int {
	if yearsOwned <= 0 {
		return principal
	}
	if monthlyRate == 0 {
		balance := principal - monthlyPayment*yearsOwned*12
		if balance < 0 {
			return 0
		}
		return balance
	}
	n := float64(yearsOwned * 12)
	factor := math.Pow(1+monthlyRate, n)
	balance := float64(principal)*factor - float64(monthlyPayment)*(factor-1)/monthlyRate
	if balance < 0 {
		return 0
	}
	return int(balance)
}

// ============================================================================
// Mapping Functions
// ============================================================================

func (r yearResult) toYearlyProjection() investment.YearlyProjection {
	return investment.YearlyProjection{
		Year:                   r.year,
		PortfolioValue:         r.portfolioValue,
		Equity:                 r.equity,
		LoanBalance:            r.loanBalance,
		AnnualCashFlow:         r.annualCashFlow,
		CumulativeCashFlow:     r.cumulativeCashFlow,
		Appreciation:           r.appreciation,
		CashBalance:            r.cashBalance,
		GrossRentalIncome:      r.grossRentalIncome,
		OperatingExpenses:      r.operatingExpenses,
		NetOperatingIncome:     r.netOperatingIncome,
		DebtService:            r.debtService,
		PropertyCount:          r.propertyCount,
		PropertiesAcquired:     r.propertiesAcquired,
		CapExReserve:           r.capExReserve,
		CumulativeCapExReserve: r.cumulativeCapExReserve,
	}
}

func (r yearResult) toExpandedYearProjection() investment.ExpandedYearProjection {
	return investment.ExpandedYearProjection{
		Year:                   r.year,
		PortfolioValue:         r.portfolioValue,
		Equity:                 r.equity,
		LoanBalance:            r.loanBalance,
		AnnualCashFlow:         r.annualCashFlow,
		NetOperatingIncome:     r.netOperatingIncome,
		GrossRent:              r.grossRentalIncome,
		VacancyLoss:            r.vacancyLoss,
		EffectiveGrossIncome:   r.grossRentalIncome - r.vacancyLoss,
		OperatingExpenses:      r.operatingExpenses,
		DebtService:            r.debtService,
		InterestExpense:        r.interestExpense,
		PrincipalPayment:       r.principalPayment,
		Depreciation:           r.depreciation,
		TaxableIncome:          r.taxableIncome,
		IncomeTaxes:            r.incomeTaxes,
		CashFlowAfterTax:       r.cashFlowAfterTax,
		CashOnCash:             r.cashOnCash,
		CapRate:                r.capRate,
		EquityMultiple:         r.equityMultiple,
		CumulativeCashFlow:     r.cumulativeCashFlow,
		Appreciation:           r.appreciation,
		CapExReserve:           r.capExReserve,
		CumulativeCapExReserve: r.cumulativeCapExReserve,
	}
}

func mapToExpanded(results []yearResult) []investment.ExpandedYearProjection {
	expanded := make([]investment.ExpandedYearProjection, len(results))
	for i, r := range results {
		expanded[i] = r.toExpandedYearProjection()
	}
	return expanded
}

// ============================================================================
// Config Builder
// ============================================================================

// configFromParams builds a ProjectionConfig from ReinvestmentParams.
func (m *ReinvestmentModeler) configFromParams(
	params ReinvestmentParams,
	withReinvestment bool,
	growthMultiplier float64,
	includeTax bool,
) ProjectionConfig {
	marketLookup := m.buildMarketDataLookup(params.MarketQuality)

	yearlyBudgets := make(map[int]int)
	for _, yb := range params.YearlyBudgets {
		yearlyBudgets[yb.Year] = yb.Budget
	}

	return ProjectionConfig{
		Properties:              params.Properties,
		ProjectionYears:         params.ProjectionYears,
		MortgageRate:            params.MortgageRate,
		DownPaymentPct:          params.DownPaymentPct,
		OperatingExpenses:       params.OperatingExpenses,
		WithReinvestment:        withReinvestment,
		ReinvestmentRate:        params.ReinvestmentRate,
		YearlyBudgets:           yearlyBudgets,
		GrowthMultiplier:        growthMultiplier,
		IncludeTax:              includeTax,
		MarketLookup:            marketLookup,
		AcquisitionMarket:       params.AcquisitionMarket,
		DefaultAppreciationRate: params.AppreciationRate,
		DefaultRentGrowthRate:   params.RentGrowthRate,
		AvgPropertyPrice:        params.AvgPropertyPrice,
		AvgPropertyRent:         params.AvgPropertyRent,
	}
}

// ============================================================================
// Scenario Projection Helpers
// ============================================================================

// buildScenarioAssumptions creates transparent assumptions for scenario projections.
func (m *ReinvestmentModeler) buildScenarioAssumptions(
	properties []investment.PropertyInPortfolio,
	marketLookup map[string]*investment.LocationMarketAnalysis,
	userAssumptions *investment.UserFinancialAssumptions,
	mortgageRate float64,
) *investment.ProjectionAssumptions {
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
					rate := (math.Pow(1+totalGrowth, 0.2) - 1)
					weightedAppreciation += float64(p.Property.Price) * rate
					hasMarketData = true
				} else {
					weightedAppreciation += float64(p.Property.Price) * 0.03
				}
			} else {
				weightedAppreciation += float64(p.Property.Price) * 0.03
			}

			if marketData.RentGrowth5Y != nil {
				totalGrowth := *marketData.RentGrowth5Y / 100
				if totalGrowth > -1 {
					rate := (math.Pow(1+totalGrowth, 0.2) - 1)
					weightedRentGrowth += float64(p.Property.Price) * rate
					hasMarketData = true
				} else {
					weightedRentGrowth += float64(p.Property.Price) * 0.02
				}
			} else {
				weightedRentGrowth += float64(p.Property.Price) * 0.02
			}
		} else {
			weightedAppreciation += float64(p.Property.Price) * 0.03
			weightedRentGrowth += float64(p.Property.Price) * 0.02
		}
	}

	avgAppreciationRate := 0.0
	avgRentGrowthRate := 0.0
	if totalValue > 0 {
		avgAppreciationRate = weightedAppreciation / float64(totalValue)
		avgRentGrowthRate = weightedRentGrowth / float64(totalValue)
	}

	// Down payment
	downPaymentPct := 0.20
	if userAssumptions != nil && userAssumptions.DownPaymentPercent > 0 {
		downPaymentPct = userAssumptions.DownPaymentPercent / 100
	} else if len(properties) > 0 && totalValue > 0 {
		totalDP := 0
		for _, p := range properties {
			totalDP += p.DownPayment
		}
		downPaymentPct = float64(totalDP) / float64(totalValue)
	}

	loanTermYears := 30
	if userAssumptions != nil && userAssumptions.LoanTermYears > 0 {
		loanTermYears = userAssumptions.LoanTermYears
	}

	assumptions := &investment.ProjectionAssumptions{
		AppreciationRate: avgAppreciationRate * 100,
		RentGrowthRate:   avgRentGrowthRate * 100,
		MortgageRate:     mortgageRate,
		DownPaymentPct:   downPaymentPct,
		LoanTermYears:    loanTermYears,
		DataQualityNotes: []string{},
	}

	// Sources
	if hasMarketData {
		assumptions.AppreciationSource = "Market Data 5Y CAGR (portfolio-weighted)"
		assumptions.RentGrowthSource = "Market Data 5Y CAGR (portfolio-weighted)"
		assumptions.OverallConfidence = 85
	} else {
		assumptions.AppreciationSource = "Default (3.0%)"
		assumptions.RentGrowthSource = "Default (2.0%)"
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

	// Expense info
	if len(properties) > 0 && properties[0].Expenses != nil {
		assumptions.ExpenseSource = "State-specific calculation (per-property)"
	} else {
		assumptions.ExpenseSource = "State-specific calculation"
	}

	// CapEx reserve info
	m.populateCapExAssumptions(assumptions, properties)

	return assumptions
}

// buildScenarioSummary creates quick comparison across scenarios.
func (m *ReinvestmentModeler) buildScenarioSummary(
	base, optimistic, pessimistic []investment.ExpandedYearProjection,
	properties []investment.PropertyInPortfolio,
) *investment.ScenarioSummary {
	if len(base) == 0 {
		return nil
	}

	lastIdx := len(base) - 1

	// Calculate initial value for CAGR
	initialValue := 0
	for _, p := range properties {
		initialValue += p.Property.Price
	}
	years := len(base)

	cagr := func(finalValue int) float64 {
		if initialValue <= 0 || years <= 0 {
			return 0
		}
		return (math.Pow(float64(finalValue)/float64(initialValue), 1.0/float64(years)) - 1) * 100
	}

	// Total cash flow
	baseTotalCF, optTotalCF, pessTotalCF := 0, 0, 0
	for i := range base {
		baseTotalCF += base[i].AnnualCashFlow
		optTotalCF += optimistic[i].AnnualCashFlow
		pessTotalCF += pessimistic[i].AnnualCashFlow
	}

	return &investment.ScenarioSummary{
		BaseFinalValue:           base[lastIdx].PortfolioValue,
		BaseFinalEquity:          base[lastIdx].Equity,
		BaseTotalCashFlow:        baseTotalCF,
		BaseCAGR:                 cagr(base[lastIdx].PortfolioValue),
		OptimisticFinalValue:     optimistic[lastIdx].PortfolioValue,
		OptimisticFinalEquity:    optimistic[lastIdx].Equity,
		OptimisticTotalCashFlow:  optTotalCF,
		OptimisticCAGR:           cagr(optimistic[lastIdx].PortfolioValue),
		PessimisticFinalValue:    pessimistic[lastIdx].PortfolioValue,
		PessimisticFinalEquity:   pessimistic[lastIdx].Equity,
		PessimisticTotalCashFlow: pessTotalCF,
		PessimisticCAGR:          cagr(pessimistic[lastIdx].PortfolioValue),
		OptimisticMultiplier:     1.15,
		PessimisticMultiplier:    0.85,
	}
}

// ============================================================================
// Preserved Helper Methods (unchanged from previous implementation)
// ============================================================================

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
	if params.AvgPropertyPrice == 0 {
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
	if params.MinDownPayment == 0 {
		params.MinDownPayment = int(float64(params.AvgPropertyPrice) * params.DownPaymentPct)
	}
	if params.ReinvestmentRate == 0 {
		params.ReinvestmentRate = defaults.ReinvestmentRate
	}

	return params
}

// buildAssumptions creates a transparent summary of all assumptions used in projections
func (m *ReinvestmentModeler) buildAssumptions(params ReinvestmentParams) *investment.ProjectionAssumptions {
	assumptions := &investment.ProjectionAssumptions{
		MortgageRate:     params.MortgageRate,
		DownPaymentPct:   params.DownPaymentPct,
		LoanTermYears:    30,
		AppreciationRate: params.AppreciationRate,
		RentGrowthRate:   params.RentGrowthRate,
		AcquisitionPrice: params.AvgPropertyPrice,
		AcquisitionRent:  params.AvgPropertyRent,
		DataQualityNotes: []string{},
	}

	defaults := DefaultReinvestmentParams()

	// Mortgage rate source
	if params.AcquisitionMarket != nil && params.AcquisitionMarket.MortgageRate != nil {
		assumptions.MortgageSource = "FRED 30Y Fixed"
	} else if params.MortgageRate == defaults.MortgageRate {
		assumptions.MortgageSource = fmt.Sprintf("Default (%.1f%%)", defaults.MortgageRate)
	} else {
		assumptions.MortgageSource = "User-specified"
	}

	// Appreciation/rent growth sources
	marketLookup := m.buildMarketDataLookup(params.MarketQuality)
	if len(params.Properties) > 0 {
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
			avgRate := m.calculateWeightedAverageAppreciation(params.Properties, marketLookup, params.AppreciationRate)
			assumptions.AppreciationRate = avgRate
			assumptions.AppreciationSource = "Market Data 5Y CAGR (portfolio-weighted)"
		} else {
			assumptions.AppreciationSource = fmt.Sprintf("Default (%.1f%%)", defaults.AppreciationRate)
			assumptions.DataQualityNotes = append(assumptions.DataQualityNotes,
				"Appreciation rate using historical average (market data unavailable)")
		}

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

	// Acquisition price/rent/location sources
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

	if params.AcquisitionMarket != nil && params.AcquisitionMarket.TargetCity != "" {
		assumptions.AcquisitionLocation = fmt.Sprintf("%s, %s",
			params.AcquisitionMarket.TargetCity, params.AcquisitionMarket.TargetState)
	} else if len(params.Properties) > 0 {
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

	// Expense breakdown
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

	// CapEx reserve assumptions
	m.populateCapExAssumptions(assumptions, params.Properties)

	assumptions.OverallConfidence = m.calculateConfidenceScore(assumptions)

	return assumptions
}

// populateCapExAssumptions adds CapEx reserve data to assumptions.
func (m *ReinvestmentModeler) populateCapExAssumptions(
	assumptions *investment.ProjectionAssumptions,
	properties []investment.PropertyInPortfolio,
) {
	currentYear := time.Now().Year()

	// Calculate average property age
	avgAge := 15 // default
	if len(properties) > 0 {
		totalAge := 0
		for _, p := range properties {
			if p.Property.YearBuilt > 0 {
				totalAge += currentYear - p.Property.YearBuilt
			} else {
				totalAge += 15
			}
		}
		avgAge = totalAge / len(properties)
	}

	assumptions.CapExReserveRate = expenses.CapExReserveRate(avgAge)
	assumptions.CapExReserveSource = "Component lifecycle annualization (age-adjusted)"

	// Calculate average property value for component risk assessment
	avgValue := 250000 // default
	if len(properties) > 0 {
		totalValue := 0
		for _, p := range properties {
			totalValue += p.Property.Price
		}
		avgValue = totalValue / len(properties)
	}

	risks := expenses.ComponentRiskAssessment(avgAge, avgValue)
	assumptions.CapExComponents = make([]investment.CapExComponentDetail, len(risks))
	for i, r := range risks {
		assumptions.CapExComponents[i] = investment.CapExComponentDetail{
			Name:             r.Name,
			LifespanYears:    r.LifespanYears,
			EstRemainingLife: r.EstRemainingLife,
			AnnualReserve:    r.AnnualReserve,
			RiskLevel:        r.RiskLevel,
		}
	}
}

// buildExpenseBreakdown creates detailed expense breakdown for acquisitions
func (m *ReinvestmentModeler) buildExpenseBreakdown(params ReinvestmentParams) *investment.ProjectionExpenseBreakdown {
	var targetState, targetCity string

	if params.AcquisitionMarket != nil && params.AcquisitionMarket.TargetState != "" {
		targetState = params.AcquisitionMarket.TargetState
		targetCity = params.AcquisitionMarket.TargetCity
	} else if len(params.Properties) > 0 {
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

	if targetState == "" {
		return nil
	}

	input := expenses.PropertyInput{
		Price:         params.AvgPropertyPrice,
		State:         targetState,
		City:          targetCity,
		YearBuilt:     time.Now().Year() - 10,
		EstimatedRent: params.AvgPropertyRent,
	}

	if params.AcquisitionMarket != nil && params.AcquisitionMarket.VacancyRate != nil {
		input.VacancyRateOverride = params.AcquisitionMarket.VacancyRate
	}

	result, err := m.expenseCalc.Calculate(input)
	if err != nil {
		m.logger.Warn("expense calculation failed, using defaults", "error", err, "state", targetState)
		return nil
	}

	marketClass, _, _ := m.expenseCalc.GetMarketClassInfo(targetCity)

	ageCategory := "established"
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

// calculateConfidenceScore calculates an overall confidence score (0-100)
func (m *ReinvestmentModeler) calculateConfidenceScore(assumptions *investment.ProjectionAssumptions) float64 {
	score := 100.0
	deductionPerNote := 15.0
	score -= float64(len(assumptions.DataQualityNotes)) * deductionPerNote

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
		score += 10
	}

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
		lookup[strings.ToLower(marketQuality[i].Location)] = &marketQuality[i]
	}
	return lookup
}

// getPropertyLocation returns a normalized location key for a property
func (m *ReinvestmentModeler) getPropertyLocation(prop investment.PropertyInPortfolio) string {
	return strings.ToLower(fmt.Sprintf("%s, %s", prop.Property.City, prop.Property.State))
}

// getAppreciationRateForProperty returns the annual appreciation rate for a property
func (m *ReinvestmentModeler) getAppreciationRateForProperty(
	prop investment.PropertyInPortfolio,
	marketLookup map[string]*investment.LocationMarketAnalysis,
	defaultRate float64,
) float64 {
	location := m.getPropertyLocation(prop)
	if marketData, ok := marketLookup[location]; ok && marketData.PriceGrowth5Y != nil {
		totalGrowth := *marketData.PriceGrowth5Y / 100
		if totalGrowth > -1 {
			annualRate := (math.Pow(1+totalGrowth, 0.2) - 1) * 100
			return annualRate
		}
	}
	return defaultRate
}

// getRentGrowthRateForProperty returns the annual rent growth rate for a property
func (m *ReinvestmentModeler) getRentGrowthRateForProperty(
	prop investment.PropertyInPortfolio,
	marketLookup map[string]*investment.LocationMarketAnalysis,
	defaultRate float64,
) float64 {
	location := m.getPropertyLocation(prop)
	if marketData, ok := marketLookup[location]; ok && marketData.RentGrowth5Y != nil {
		totalGrowth := *marketData.RentGrowth5Y / 100
		if totalGrowth > -1 {
			annualRate := (math.Pow(1+totalGrowth, 0.2) - 1) * 100
			return annualRate
		}
	}
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

// calculateCumulativeDiff calculates the difference between scenarios at key years
func (m *ReinvestmentModeler) calculateCumulativeDiff(
	base, reinvest []investment.YearlyProjection,
	years int,
) investment.ReinvestmentDiff {
	diff := investment.ReinvestmentDiff{}

	if years >= 5 && len(base) >= 5 && len(reinvest) >= 5 {
		diff.Year5 = investment.ReinvestmentDiffPoint{
			Value:    (reinvest[4].PortfolioValue + reinvest[4].CashBalance) - base[4].PortfolioValue,
			CashFlow: reinvest[4].AnnualCashFlow - base[4].AnnualCashFlow,
			Equity:   (reinvest[4].Equity + reinvest[4].CashBalance) - base[4].Equity,
		}
	}

	if years >= 10 && len(base) >= 10 && len(reinvest) >= 10 {
		diff.Year10 = investment.ReinvestmentDiffPoint{
			Value:    (reinvest[9].PortfolioValue + reinvest[9].CashBalance) - base[9].PortfolioValue,
			CashFlow: reinvest[9].AnnualCashFlow - base[9].AnnualCashFlow,
			Equity:   (reinvest[9].Equity + reinvest[9].CashBalance) - base[9].Equity,
		}
	} else if years > 0 && len(base) > 0 && len(reinvest) > 0 {
		lastIdx := len(base) - 1
		diff.Year10 = investment.ReinvestmentDiffPoint{
			Value:    (reinvest[lastIdx].PortfolioValue + reinvest[lastIdx].CashBalance) - base[lastIdx].PortfolioValue,
			CashFlow: reinvest[lastIdx].AnnualCashFlow - base[lastIdx].AnnualCashFlow,
			Equity:   (reinvest[lastIdx].Equity + reinvest[lastIdx].CashBalance) - base[lastIdx].Equity,
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

	baseCumulativeCashFlow := base[baseLastIdx].CumulativeCashFlow
	totalReinvested := baseCumulativeCashFlow

	additionalValue := (reinvest[lastIdx].PortfolioValue + reinvest[lastIdx].CashBalance) - base[baseLastIdx].PortfolioValue
	additionalCashFlow := reinvest[lastIdx].AnnualCashFlow - base[baseLastIdx].AnnualCashFlow

	return investment.CompoundedReturns{
		TotalReinvested:         totalReinvested,
		AdditionalPropertyValue: additionalValue,
		AdditionalCashFlow:      additionalCashFlow,
	}
}

// calculatePortfolioValue sums property prices
func (m *ReinvestmentModeler) calculatePortfolioValue(properties []investment.PropertyInPortfolio) int {
	total := 0
	for _, p := range properties {
		total += p.Property.Price
	}
	return total
}

// getMostCommonStateCity returns the most common state and city from properties
func (m *ReinvestmentModeler) getMostCommonStateCity(properties []investment.PropertyInPortfolio) (string, string) {
	if len(properties) == 0 {
		return "", ""
	}
	stateCounts := make(map[string]int)
	cityCounts := make(map[string]string)
	for _, p := range properties {
		stateCounts[p.Property.State]++
		cityCounts[p.Property.State] = p.Property.City
	}
	maxCount := 0
	var targetState, targetCity string
	for state, count := range stateCounts {
		if count > maxCount {
			maxCount = count
			targetState = state
			targetCity = cityCounts[state]
		}
	}
	return targetState, targetCity
}

// avgPrice calculates average property price
func (m *ReinvestmentModeler) avgPrice(properties []investment.PropertyInPortfolio) int {
	if len(properties) == 0 {
		return 300000
	}
	total := 0
	for _, p := range properties {
		total += p.Property.Price
	}
	return total / len(properties)
}

// avgRent calculates average property rent
func (m *ReinvestmentModeler) avgRent(properties []investment.PropertyInPortfolio) int {
	if len(properties) == 0 {
		return 2000
	}
	total := 0
	for _, p := range properties {
		total += p.Property.EstimatedRent
	}
	return total / len(properties)
}
