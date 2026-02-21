package finder

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/services/market/correlation"
	"github.com/estara-ai/www/internal/services/property/providers"
)

// Investment calculation constants (from ADR-030/ADR-053)
const (
	// Base expense ratio EXCLUDING maintenance (vacancy, management, insurance, taxes, misc)
	baseExpenseRatio = 0.25 // 25% of rent for non-maintenance expenses

	// Default maintenance rate (adjusted by age)
	defaultMaintenanceRate = 0.01 // 1% of property value

	// Mortgage assumptions
	defaultDownPayment    = 0.25 // 25% down
	defaultInterestRate   = 0.07 // 7% mortgage rate
	defaultLoanTermMonths = 360  // 30 years
)

// Default rent estimates by bedroom count (fallback when no rent data)
var defaultRentByBedrooms = map[int]int{
	0: 1200, // Studio
	1: 1500, // 1BR
	2: 1900, // 2BR
	3: 2400, // 3BR
	4: 3000, // 4BR
	5: 3600, // 5BR+
}

// Location rent multipliers for major metros
var locationRentMultipliers = map[string]float64{
	"new york":      2.5,
	"san francisco": 2.3,
	"los angeles":   1.8,
	"boston":        1.7,
	"seattle":       1.6,
	"san diego":     1.5,
	"denver":        1.4,
	"austin":        1.3,
	"miami":         1.3,
	"phoenix":       1.1,
	"dallas":        1.1,
	"atlanta":       1.0,
	"chicago":       1.0,
	"houston":       0.95,
}

// InvestmentMetricsEnricher enriches properties with investment metrics
type InvestmentMetricsEnricher struct {
	logger            *slog.Logger
	correlationSvc    *correlation.Service
}

// NewInvestmentMetricsEnricher creates a new enricher
func NewInvestmentMetricsEnricher() *InvestmentMetricsEnricher {
	return &InvestmentMetricsEnricher{
		logger:         slog.Default().With("component", "investment_enricher"),
		correlationSvc: nil, // Will be set by orchestrator if available
	}
}

// WithCorrelationService sets the correlation service (for Phase 1 volatility enrichment)
func (e *InvestmentMetricsEnricher) WithCorrelationService(svc *correlation.Service) *InvestmentMetricsEnricher {
	e.correlationSvc = svc
	return e
}

// EnrichProperties enriches a slice of properties with investment metrics
func (e *InvestmentMetricsEnricher) EnrichProperties(properties []providers.Property) []providers.Property {
	enriched := make([]providers.Property, len(properties))

	for i, prop := range properties {
		enriched[i] = e.EnrichProperty(prop)
	}

	return enriched
}

// EnrichProperty enriches a single property with investment metrics
func (e *InvestmentMetricsEnricher) EnrichProperty(property providers.Property) providers.Property {
	price := property.Price
	squareFeet := property.Sqft
	if squareFeet == 0 {
		squareFeet = 1500 // Default assumption
	}

	// Skip properties with no price
	if price == 0 {
		return property
	}

	// Get or estimate rent
	estimatedRent := e.estimateRent(property)
	property.EstimatedRent = estimatedRent

	// Calculate core metrics
	annualRent := float64(estimatedRent * 12)
	pricePerSqFt := 0
	if squareFeet > 0 {
		pricePerSqFt = price / squareFeet
	}
	property.PricePerSqft = pricePerSqFt

	// Gross yield
	grossYield := 0.0
	if price > 0 {
		grossYield = (annualRent / float64(price)) * 100
	}
	property.GrossYield = roundFloat(grossYield, 2)
	property.RentToPrice = roundFloat(float64(estimatedRent)/float64(price)*100, 3)

	// NOI and Cap Rate with age-based maintenance (ADR-053)
	// Base expenses: vacancy (8%), management (8%), insurance (5%), misc (4%) = 25% of rent
	baseExpenses := annualRent * baseExpenseRatio

	// Age-based maintenance: 0.5% to 2.0% of property value depending on age
	maintenanceMultiplier := e.getMaintenanceMultiplier(property.YearBuilt)
	maintenanceExpense := float64(price) * defaultMaintenanceRate * maintenanceMultiplier

	operatingExpenses := baseExpenses + maintenanceExpense
	noi := annualRent - operatingExpenses
	property.NOI = int(noi)

	capRate := 0.0
	if price > 0 {
		capRate = (noi / float64(price)) * 100
	}
	property.EstimatedCapRate = roundFloat(capRate, 2)

	// Cash-on-Cash Return
	downPayment := float64(price) * defaultDownPayment
	loanAmount := float64(price) * (1 - defaultDownPayment)
	monthlyMortgage := e.calculateMortgagePayment(loanAmount)
	annualMortgage := monthlyMortgage * 12
	annualCashFlow := noi - annualMortgage
	monthlyCashFlow := annualCashFlow / 12

	property.EstimatedCashFlow = int(monthlyCashFlow)

	cashOnCash := 0.0
	if downPayment > 0 {
		cashOnCash = (annualCashFlow / downPayment) * 100
	}
	property.CashOnCash = roundFloat(cashOnCash, 2)

	// Age-based investment intelligence
	e.enrichAgeMetrics(&property)

	// ADR-088 Phase 1: Market volatility metrics (for Monte Carlo calibration)
	e.enrichVolatilityMetrics(&property)

	// Calculate overall investment score (0-100)
	property.InvestmentScore = e.calculateInvestmentScore(property)

	return property
}

// estimateRent estimates rent for a property
func (e *InvestmentMetricsEnricher) estimateRent(property providers.Property) int {
	// Use existing rent estimate if available and reasonable
	if property.EstimatedRent > 500 {
		return property.EstimatedRent
	}

	// Calculate based on property characteristics
	bedrooms := property.Beds
	if bedrooms > 5 {
		bedrooms = 5
	}
	if bedrooms < 0 {
		bedrooms = 3 // Default
	}

	baseRent := defaultRentByBedrooms[bedrooms]
	if baseRent == 0 {
		baseRent = 2000
	}

	// Apply location multiplier
	cityLower := strings.ToLower(property.City)
	for city, multiplier := range locationRentMultipliers {
		if strings.Contains(cityLower, city) {
			baseRent = int(float64(baseRent) * multiplier)
			break
		}
	}

	// Adjust for square footage (if larger/smaller than expected)
	expectedSqFt := bedrooms*500 + 500 // Rough estimate
	actualSqFt := property.Sqft
	if actualSqFt == 0 {
		actualSqFt = expectedSqFt
	}

	sqFtRatio := float64(actualSqFt) / float64(expectedSqFt)
	if sqFtRatio > 1.2 {
		baseRent = int(float64(baseRent) * 1.1) // Larger = premium
	}
	if sqFtRatio < 0.8 {
		baseRent = int(float64(baseRent) * 0.9) // Smaller = discount
	}

	// Adjust for bathrooms (bonus for extra baths)
	if property.Baths > float64(bedrooms) {
		baseRent = int(float64(baseRent) * 1.05)
	}

	return baseRent
}

// getMaintenanceMultiplier returns age-based maintenance multiplier (ADR-053)
// Older properties require more maintenance due to aging systems
func (e *InvestmentMetricsEnricher) getMaintenanceMultiplier(yearBuilt int) float64 {
	if yearBuilt == 0 {
		return 1.0 // Default if unknown
	}

	currentYear := time.Now().Year()
	age := currentYear - yearBuilt

	if age <= 10 {
		return 0.5 // 0.5% of value - minimal repairs
	}
	if age <= 25 {
		return 1.0 // 1.0% of value - standard maintenance
	}
	if age <= 50 {
		return 1.5 // 1.5% of value - increased repairs
	}
	return 2.0 // 2.0% of value - major systems aging
}

// calculateMortgagePayment calculates monthly mortgage payment (P&I)
func (e *InvestmentMetricsEnricher) calculateMortgagePayment(loanAmount float64) float64 {
	if loanAmount <= 0 {
		return 0
	}

	monthlyRate := defaultInterestRate / 12
	numPayments := float64(defaultLoanTermMonths)

	// M = P * [r(1+r)^n] / [(1+r)^n - 1]
	numerator := monthlyRate * pow(1+monthlyRate, numPayments)
	denominator := pow(1+monthlyRate, numPayments) - 1

	if denominator == 0 {
		return 0
	}

	return loanAmount * (numerator / denominator)
}

// enrichAgeMetrics adds age category and maintenance risk to property
func (e *InvestmentMetricsEnricher) enrichAgeMetrics(property *providers.Property) {
	if property.YearBuilt == 0 {
		property.AgeCategory = "established" // Conservative default
		property.MaintenanceRisk = "moderate"
		property.MaintenanceFactors = []string{"Property age unknown - assume standard maintenance needs"}
		return
	}

	currentYear := time.Now().Year()
	age := currentYear - property.YearBuilt
	property.PropertyAge = age

	// Age category
	switch {
	case age <= 5:
		property.AgeCategory = "new_construction"
	case age <= 15:
		property.AgeCategory = "modern"
	case age <= 30:
		property.AgeCategory = "established"
	case age <= 50:
		property.AgeCategory = "mature"
	default:
		property.AgeCategory = "vintage"
	}

	// Maintenance risk assessment
	switch {
	case age <= 10:
		property.MaintenanceRisk = "low"
		property.MaintenanceFactors = []string{"Modern systems", "Possible builder warranty", "Energy efficient"}
	case age <= 25:
		property.MaintenanceRisk = "moderate"
		property.MaintenanceFactors = []string{"Standard maintenance expected", "HVAC may need service soon"}
	case age <= 40:
		property.MaintenanceRisk = "high"
		property.MaintenanceFactors = []string{
			"HVAC replacement likely within 5 years",
			"Roof assessment recommended",
			"Water heater may need replacement",
		}
	default:
		property.MaintenanceRisk = "very_high"
		property.MaintenanceFactors = []string{
			"Major systems aging (HVAC, roof, plumbing)",
			"Electrical system may need updating",
			"Foundation inspection recommended",
			"Higher insurance costs possible",
		}
	}
}

// calculateInvestmentScore calculates an overall investment score (0-100)
// Based on balanced criteria (cashflow + appreciation potential)
func (e *InvestmentMetricsEnricher) calculateInvestmentScore(property providers.Property) int {
	score := 0.0

	// Cap rate contribution (0-40 points)
	// Good cap rate: 5%+, Excellent: 8%+
	capRateScore := property.EstimatedCapRate * 5 // 5 points per 1%
	if capRateScore > 40 {
		capRateScore = 40
	}
	score += capRateScore

	// Cash-on-cash contribution (0-30 points)
	// Good CoC: 4%+, Excellent: 8%+
	cocScore := property.CashOnCash * 3.75 // 3.75 points per 1%
	if cocScore > 30 {
		cocScore = 30
	}
	if cocScore < 0 {
		cocScore = 0 // Don't penalize negative cash flow below 0
	}
	score += cocScore

	// Price per sqft value (0-20 points)
	// Lower is better - assume $150/sqft is good, $300+ is expensive
	if property.PricePerSqft > 0 {
		ppsScore := 20 - (float64(property.PricePerSqft-100) / 15)
		if ppsScore > 20 {
			ppsScore = 20
		}
		if ppsScore < 0 {
			ppsScore = 0
		}
		score += ppsScore
	}

	// Age/maintenance risk (0-10 points)
	switch property.MaintenanceRisk {
	case "low":
		score += 10
	case "moderate":
		score += 7
	case "high":
		score += 4
	case "very_high":
		score += 1
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return int(score)
}

// Helper functions

// pow calculates x^n (simple implementation for positive integer exponents)
func pow(x float64, n float64) float64 {
	result := 1.0
	for i := 0; i < int(n); i++ {
		result *= x
	}
	return result
}

// roundFloat rounds a float to n decimal places
func roundFloat(val float64, precision int) float64 {
	ratio := 1.0
	for i := 0; i < precision; i++ {
		ratio *= 10
	}
	return float64(int(val*ratio+0.5)) / ratio
}

// enrichVolatilityMetrics enriches property with market volatility metrics (ADR-088 Phase 1)
// Adds: AppreciationStdDev, RentGrowthStdDev, HistoricalPeakDrop, MarketType
func (e *InvestmentMetricsEnricher) enrichVolatilityMetrics(property *providers.Property) {
	// Skip if no correlation service available
	if e.correlationSvc == nil {
		return
	}

	// Construct market ID from city and state
	marketID := property.City + ", " + property.State
	if marketID == ", " {
		return // Skip if location data missing
	}

	// Fetch volatility metrics from correlation service
	ctx := context.Background()
	metrics, err := e.correlationSvc.GetMarketVolatilityMetrics(ctx, marketID)
	if err != nil {
		e.logger.Warn("Failed to fetch volatility metrics",
			"marketId", marketID,
			"error", err,
		)
		return
	}

	// Populate property with volatility metrics
	property.AppreciationStdDev = metrics.AppreciationStdDev
	property.RentGrowthStdDev = metrics.RentGrowthStdDev
	property.HistoricalPeakDrop = metrics.HistoricalPeakDrop
	property.MarketType = metrics.MarketType
}
