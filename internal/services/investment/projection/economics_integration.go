package projection

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/estara-ai/www/internal/services/market/economics"
)

// EconomicsEnhancedCalculator wraps Calculator with live economic data (ADR-069)
type EconomicsEnhancedCalculator struct {
	*Calculator
	economics economics.Provider
	logger    *slog.Logger
}

// NewEconomicsEnhancedCalculator creates a calculator that uses live economic data
func NewEconomicsEnhancedCalculator(config *DefaultConfig, econ economics.Provider) *EconomicsEnhancedCalculator {
	calc := NewCalculator(config)
	return &EconomicsEnhancedCalculator{
		Calculator: calc,
		economics:  econ,
		logger:     slog.Default().With("component", "economics_enhanced_calculator"),
	}
}

// EconomicConfig holds live economic data for projections
type EconomicConfig struct {
	// From FRED
	MortgageRate30Year float64 `json:"mortgageRate30Year"`
	MortgageRate15Year float64 `json:"mortgageRate15Year"`
	VacancyRate        float64 `json:"vacancyRate"`
	InflationRate      float64 `json:"inflationRate"`

	// From Census
	MedianHouseholdIncome float64 `json:"medianHouseholdIncome"`
	PopulationGrowth      float64 `json:"populationGrowth"` // Annual %

	// From BLS
	StateUnemploymentRate float64 `json:"stateUnemploymentRate"`
	WageGrowth            float64 `json:"wageGrowth"` // Annual %

	// Calculated
	AffordabilityIndex float64 `json:"affordabilityIndex"`
	PriceToIncomeRatio float64 `json:"priceToIncomeRatio"`

	// Metadata
	DataSources map[string]string `json:"dataSources"`
	Confidence  int               `json:"confidence"` // 0-100
}

// GetEconomicConfig fetches live economic data for a state
func (c *EconomicsEnhancedCalculator) GetEconomicConfig(ctx context.Context, state string) (*EconomicConfig, error) {
	if c.economics == nil || !c.economics.IsConfigured() {
		return c.getDefaultEconomicConfig(), nil
	}

	// Fetch from aggregator
	data, err := c.economics.GetMarketEconomics(ctx, "", state)
	if err != nil {
		c.logger.Warn("failed to fetch economic data, using defaults", "error", err, "state", state)
		return c.getDefaultEconomicConfig(), nil
	}

	config := &EconomicConfig{
		MortgageRate30Year:    data.MortgageRate30Year,
		MortgageRate15Year:    data.MortgageRate15Year,
		VacancyRate:           data.RentalVacancyRate,
		InflationRate:         data.InflationRate,
		MedianHouseholdIncome: data.MedianHouseholdIncome,
		StateUnemploymentRate: data.StateUnemploymentRate,
		AffordabilityIndex:    data.AffordabilityIndex,
		PriceToIncomeRatio:    data.PriceToIncomeRatio,
		DataSources:           data.Sources,
		Confidence:            85, // High confidence with live data
	}

	// Estimate population growth from demographic indicators (simplified)
	if data.Population > 0 {
		// Default to 1% population growth if no historical data
		config.PopulationGrowth = 1.0
	}

	// Estimate wage growth from BLS hourly earnings
	if data.AverageHourlyEarnings > 0 {
		// Default to inflation + 1% for real wage growth
		config.WageGrowth = data.InflationRate + 1.0
	}

	return config, nil
}

// getDefaultEconomicConfig returns fallback values
func (c *EconomicsEnhancedCalculator) getDefaultEconomicConfig() *EconomicConfig {
	return &EconomicConfig{
		MortgageRate30Year:    7.0,
		MortgageRate15Year:    6.25,
		VacancyRate:           6.0,
		InflationRate:         2.5,
		MedianHouseholdIncome: 75000,
		PopulationGrowth:      1.0,
		StateUnemploymentRate: 4.0,
		WageGrowth:            3.0,
		DataSources:           map[string]string{"default": "fallback values"},
		Confidence:            50, // Low confidence with defaults
	}
}

// GetMortgageRate returns the live 30-year mortgage rate
func (c *EconomicsEnhancedCalculator) GetMortgageRate(ctx context.Context) (float64, string) {
	if c.economics == nil || !c.economics.IsConfigured() {
		return 7.0, "Default (no data source)"
	}

	rate, err := c.economics.GetMortgageRate(ctx)
	if err != nil || rate == 0 {
		c.logger.Warn("failed to fetch mortgage rate", "error", err)
		return 7.0, "Default (fetch failed)"
	}

	return rate, "FRED 30Y Fixed"
}

// GetVacancyRate returns the live rental vacancy rate
func (c *EconomicsEnhancedCalculator) GetVacancyRate(ctx context.Context) (float64, string) {
	if c.economics == nil || !c.economics.IsConfigured() {
		return 5.0, "Default (no data source)"
	}

	rate, err := c.economics.GetVacancyRate(ctx)
	if err != nil || rate == 0 {
		c.logger.Warn("failed to fetch vacancy rate", "error", err)
		return 5.0, "Default (fetch failed)"
	}

	return rate / 100, fmt.Sprintf("FRED (%.1f%%)", rate)
}

// EnhancedDefaultConfig returns projection defaults enhanced with live data
func (c *EconomicsEnhancedCalculator) EnhancedDefaultConfig(ctx context.Context, state string) DefaultConfig {
	econData, _ := c.GetEconomicConfig(ctx, state)

	// Calculate rent growth based on inflation + population growth
	rentGrowth := 0.02 // Base 2%
	if econData != nil {
		// Rent grows with inflation plus population-driven demand
		rentGrowth = (econData.InflationRate / 100) + (econData.PopulationGrowth / 200)
		if rentGrowth < 0.01 {
			rentGrowth = 0.01 // Minimum 1%
		}
		if rentGrowth > 0.06 {
			rentGrowth = 0.06 // Cap at 6%
		}
	}

	// Calculate vacancy rate
	vacancyRate := 0.05 // Default 5%
	if econData != nil && econData.VacancyRate > 0 {
		vacancyRate = econData.VacancyRate / 100
	}

	return DefaultConfig{
		AppreciationRate: 0.03,        // Keep 3% as conservative default
		RentGrowthRate:   rentGrowth,  // Dynamic based on inflation + population
		ExpenseRatio:     0.35,        // Keep 35% (state-specific handled elsewhere)
		VacancyRate:      vacancyRate, // From FRED
		LoanTermYears:    30,
	}
}

// BuildEconomicsAssumptions creates transparent assumptions from economic data
func (c *EconomicsEnhancedCalculator) BuildEconomicsAssumptions(ctx context.Context, state string, mortgageRate float64) *EconomicsAssumptions {
	econData, _ := c.GetEconomicConfig(ctx, state)

	assumptions := &EconomicsAssumptions{
		MortgageRate: mortgageRate,
		DataSources:  make(map[string]string),
		Notes:        []string{},
	}

	if econData != nil {
		assumptions.VacancyRate = econData.VacancyRate
		assumptions.InflationRate = econData.InflationRate
		assumptions.StateUnemploymentRate = econData.StateUnemploymentRate
		assumptions.MedianHouseholdIncome = econData.MedianHouseholdIncome
		assumptions.DataSources = econData.DataSources
		assumptions.Confidence = econData.Confidence

		if econData.Confidence >= 80 {
			assumptions.Notes = append(assumptions.Notes, "Using live economic data from FRED/Census/BLS")
		} else if econData.Confidence >= 50 {
			assumptions.Notes = append(assumptions.Notes, "Using partial economic data with defaults")
		} else {
			assumptions.Notes = append(assumptions.Notes, "Using default assumptions (live data unavailable)")
		}
	}

	// Set mortgage source
	if mortgageRate == 7.0 {
		assumptions.MortgageSource = "Default (7%)"
	} else {
		assumptions.MortgageSource = "FRED 30Y Fixed"
	}

	return assumptions
}

// EconomicsAssumptions captures economic data used in projections
type EconomicsAssumptions struct {
	// Rates
	MortgageRate          float64 `json:"mortgageRate"`
	MortgageSource        string  `json:"mortgageSource"`
	VacancyRate           float64 `json:"vacancyRate"`
	InflationRate         float64 `json:"inflationRate"`
	StateUnemploymentRate float64 `json:"stateUnemploymentRate"`

	// Demographics
	MedianHouseholdIncome float64 `json:"medianHouseholdIncome"`

	// Metadata
	DataSources map[string]string `json:"dataSources"`
	Confidence  int               `json:"confidence"`
	Notes       []string          `json:"notes"`
}
