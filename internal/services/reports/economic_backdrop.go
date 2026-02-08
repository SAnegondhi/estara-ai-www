package reports

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/estara-ai/www/internal/services/market/economics"
)

// EconomicBackdrop represents economic conditions at report generation time (ADR-069)
type EconomicBackdrop struct {
	// Financing Environment
	MortgageRate30Y float64 `json:"mortgageRate30Y"`
	MortgageRate15Y float64 `json:"mortgageRate15Y"`

	// National Economy
	NationalUnemployment float64 `json:"nationalUnemployment"`
	InflationRate        float64 `json:"inflationRate"`
	RentalVacancyRate    float64 `json:"rentalVacancyRate"`

	// State Labor Market
	State                   string  `json:"state,omitempty"`
	StateUnemploymentRate   float64 `json:"stateUnemploymentRate,omitempty"`
	StateEmployment         float64 `json:"stateEmployment,omitempty"` // thousands
	LaborForceParticipation float64 `json:"laborForceParticipation,omitempty"`
	AverageHourlyEarnings   float64 `json:"averageHourlyEarnings,omitempty"`
	ConstructionEmployment  float64 `json:"constructionEmployment,omitempty"` // thousands
	JobOpenings             float64 `json:"jobOpenings,omitempty"`            // thousands

	// Demographics (city/state level)
	City                  string  `json:"city,omitempty"`
	Population            int64   `json:"population,omitempty"`
	MedianHouseholdIncome float64 `json:"medianHouseholdIncome,omitempty"`
	PerCapitaIncome       float64 `json:"perCapitaIncome,omitempty"`
	PovertyRate           float64 `json:"povertyRate,omitempty"`
	MedianAge             float64 `json:"medianAge,omitempty"`
	HouseholdCount        int64   `json:"householdCount,omitempty"`
	DemographicLevel      string  `json:"demographicLevel,omitempty"` // "place", "county", "state"

	// Housing Cost Indices
	CPIShelter float64 `json:"cpiShelter,omitempty"`
	CPIRent    float64 `json:"cpiRent,omitempty"`

	// Calculated Affordability (if property price provided)
	MedianHomePrice    float64 `json:"medianHomePrice,omitempty"`
	PriceToIncomeRatio float64 `json:"priceToIncomeRatio,omitempty"`
	AffordabilityIndex float64 `json:"affordabilityIndex,omitempty"`

	// Risk Indicators
	RiskFactors []RiskFactor `json:"riskFactors,omitempty"`

	// Market Comparison
	NationalComparison *MarketComparison `json:"nationalComparison,omitempty"`

	// Metadata
	DataSources map[string]string `json:"dataSources"`
	AsOfDate    time.Time         `json:"asOfDate"`
	Confidence  int               `json:"confidence"` // 0-100
	Notes       []string          `json:"notes,omitempty"`
}

// RiskFactor represents an economic risk indicator
type RiskFactor struct {
	Category    string `json:"category"`    // "employment", "housing", "affordability", "inflation"
	Level       string `json:"level"`       // "low", "moderate", "elevated", "high"
	Description string `json:"description"` // Human-readable description
	Value       string `json:"value"`       // The actual metric value
}

// MarketComparison compares local metrics to national averages
type MarketComparison struct {
	UnemploymentVsNational  float64 `json:"unemploymentVsNational,omitempty"`  // Difference in percentage points
	IncomeVsNational        float64 `json:"incomeVsNational,omitempty"`        // Ratio to national median
	VacancyVsNational       float64 `json:"vacancyVsNational,omitempty"`       // Difference in percentage points
	AffordabilityVsNational float64 `json:"affordabilityVsNational,omitempty"` // Difference in index points
}

// EconomicBackdropService generates economic backdrop for reports
type EconomicBackdropService struct {
	economics economics.Provider
	logger    *slog.Logger
}

// NewEconomicBackdropService creates a service for generating economic backdrops
func NewEconomicBackdropService(econ economics.Provider) *EconomicBackdropService {
	return &EconomicBackdropService{
		economics: econ,
		logger:    slog.Default().With("component", "economic_backdrop_service"),
	}
}

// GenerateBackdrop creates an economic backdrop for a report
func (s *EconomicBackdropService) GenerateBackdrop(ctx context.Context, city, state string, medianHomePrice float64) (*EconomicBackdrop, error) {
	backdrop := &EconomicBackdrop{
		DataSources: make(map[string]string),
		AsOfDate:    time.Now(),
		Notes:       []string{},
	}

	if s.economics == nil || !s.economics.IsConfigured() {
		s.logger.Warn("economic services not configured, using defaults")
		return s.generateDefaultBackdrop(city, state), nil
	}

	// Fetch economic data
	var data *economics.MarketEconomics
	var err error

	if medianHomePrice > 0 {
		data, err = s.economics.GetMarketEconomicsWithPrice(ctx, city, state, medianHomePrice)
	} else {
		data, err = s.economics.GetMarketEconomics(ctx, city, state)
	}

	if err != nil {
		s.logger.Warn("failed to fetch economic data", "error", err)
		return s.generateDefaultBackdrop(city, state), nil
	}

	// Populate backdrop from economic data
	backdrop.City = data.City
	backdrop.State = data.State

	// Financing
	backdrop.MortgageRate30Y = data.MortgageRate30Year
	backdrop.MortgageRate15Y = data.MortgageRate15Year

	// National economy
	backdrop.NationalUnemployment = data.NationalUnemployment
	backdrop.InflationRate = data.InflationRate
	backdrop.RentalVacancyRate = data.RentalVacancyRate

	// State labor market
	backdrop.StateUnemploymentRate = data.StateUnemploymentRate
	backdrop.StateEmployment = data.StateEmployment
	backdrop.LaborForceParticipation = data.LaborForceParticipation
	backdrop.AverageHourlyEarnings = data.AverageHourlyEarnings
	backdrop.ConstructionEmployment = data.ConstructionEmployment
	backdrop.JobOpenings = data.JobOpenings

	// Demographics
	backdrop.Population = data.Population
	backdrop.MedianHouseholdIncome = data.MedianHouseholdIncome
	backdrop.PerCapitaIncome = data.PerCapitaIncome
	backdrop.PovertyRate = data.PovertyRate
	backdrop.MedianAge = data.MedianAge
	backdrop.HouseholdCount = data.HouseholdCount
	backdrop.DemographicLevel = data.DemographicLevel

	// Housing costs
	backdrop.CPIShelter = data.CPIShelter
	backdrop.CPIRent = data.CPIRent

	// Affordability
	if medianHomePrice > 0 {
		backdrop.MedianHomePrice = medianHomePrice
	}
	backdrop.PriceToIncomeRatio = data.PriceToIncomeRatio
	backdrop.AffordabilityIndex = data.AffordabilityIndex

	// Metadata
	backdrop.DataSources = data.Sources
	backdrop.AsOfDate = data.LastUpdated
	backdrop.Confidence = s.calculateConfidence(data)

	// Generate risk factors
	backdrop.RiskFactors = s.assessRiskFactors(data)

	// Generate national comparison
	backdrop.NationalComparison = s.calculateNationalComparison(data)

	return backdrop, nil
}

// generateDefaultBackdrop returns a backdrop with default values
func (s *EconomicBackdropService) generateDefaultBackdrop(city, state string) *EconomicBackdrop {
	return &EconomicBackdrop{
		City:                 city,
		State:                state,
		MortgageRate30Y:      7.0,
		MortgageRate15Y:      6.25,
		NationalUnemployment: 4.0,
		InflationRate:        2.5,
		RentalVacancyRate:    6.0,
		DataSources:          map[string]string{"default": "fallback values"},
		AsOfDate:             time.Now(),
		Confidence:           30,
		Notes:                []string{"Using default economic assumptions - live data unavailable"},
	}
}

// calculateConfidence determines data quality score
func (s *EconomicBackdropService) calculateConfidence(data *economics.MarketEconomics) int {
	confidence := 0

	// FRED data (30 points)
	if data.MortgageRate30Year > 0 {
		confidence += 15
	}
	if data.NationalUnemployment > 0 {
		confidence += 10
	}
	if data.RentalVacancyRate > 0 {
		confidence += 5
	}

	// Census data (35 points)
	if data.MedianHouseholdIncome > 0 {
		confidence += 20
	}
	if data.Population > 0 {
		confidence += 10
	}
	if data.DemographicLevel == "place" {
		confidence += 5 // Bonus for city-level data
	}

	// BLS data (35 points)
	if data.StateUnemploymentRate > 0 {
		confidence += 20
	}
	if data.AverageHourlyEarnings > 0 {
		confidence += 10
	}
	if data.LaborForceParticipation > 0 {
		confidence += 5
	}

	return confidence
}

// assessRiskFactors identifies economic risk indicators
func (s *EconomicBackdropService) assessRiskFactors(data *economics.MarketEconomics) []RiskFactor {
	var factors []RiskFactor

	// Employment risk
	if data.StateUnemploymentRate > 0 {
		level := "low"
		desc := "Local labor market is healthy"
		switch {
		case data.StateUnemploymentRate > 7:
			level = "high"
			desc = "Elevated unemployment may impact rental demand"
		case data.StateUnemploymentRate > 5:
			level = "moderate"
			desc = "Unemployment slightly above historical norms"
		case data.StateUnemploymentRate > 4:
			level = "low"
			desc = "Labor market is stable"
		}
		factors = append(factors, RiskFactor{
			Category:    "employment",
			Level:       level,
			Description: desc,
			Value:       formatPercent(data.StateUnemploymentRate),
		})
	}

	// Affordability risk
	if data.PriceToIncomeRatio > 0 {
		level := "low"
		desc := "Housing is affordable relative to income"
		switch {
		case data.PriceToIncomeRatio > 7:
			level = "high"
			desc = "Housing affordability is severely stretched"
		case data.PriceToIncomeRatio > 5:
			level = "elevated"
			desc = "Housing affordability is strained"
		case data.PriceToIncomeRatio > 4:
			level = "moderate"
			desc = "Housing is moderately affordable"
		}
		factors = append(factors, RiskFactor{
			Category:    "affordability",
			Level:       level,
			Description: desc,
			Value:       formatRatio(data.PriceToIncomeRatio),
		})
	}

	// Vacancy risk
	if data.RentalVacancyRate > 0 {
		level := "low"
		desc := "Rental market is tight with low vacancy"
		switch {
		case data.RentalVacancyRate > 10:
			level = "high"
			desc = "High vacancy may pressure rents downward"
		case data.RentalVacancyRate > 7:
			level = "moderate"
			desc = "Vacancy is above historical average"
		case data.RentalVacancyRate > 5:
			level = "low"
			desc = "Vacancy is within healthy range"
		}
		factors = append(factors, RiskFactor{
			Category:    "housing",
			Level:       level,
			Description: desc,
			Value:       formatPercent(data.RentalVacancyRate),
		})
	}

	// Inflation risk
	if data.InflationRate > 0 {
		level := "low"
		desc := "Inflation is near Fed target"
		switch {
		case data.InflationRate > 5:
			level = "high"
			desc = "High inflation may increase operating costs and rates"
		case data.InflationRate > 3.5:
			level = "elevated"
			desc = "Inflation is above target, watch for rate increases"
		case data.InflationRate > 2.5:
			level = "moderate"
			desc = "Inflation is slightly elevated"
		}
		factors = append(factors, RiskFactor{
			Category:    "inflation",
			Level:       level,
			Description: desc,
			Value:       formatPercent(data.InflationRate),
		})
	}

	return factors
}

// calculateNationalComparison compares local metrics to national averages
func (s *EconomicBackdropService) calculateNationalComparison(data *economics.MarketEconomics) *MarketComparison {
	if data.NationalUnemployment == 0 {
		return nil
	}

	comparison := &MarketComparison{}

	// Unemployment comparison
	if data.StateUnemploymentRate > 0 && data.NationalUnemployment > 0 {
		comparison.UnemploymentVsNational = data.StateUnemploymentRate - data.NationalUnemployment
	}

	// Income comparison (vs US median of ~$75K)
	usMedianIncome := 75000.0
	if data.MedianHouseholdIncome > 0 {
		comparison.IncomeVsNational = data.MedianHouseholdIncome / usMedianIncome
	}

	// Vacancy comparison (national avg ~6%)
	nationalVacancy := 6.0
	if data.RentalVacancyRate > 0 {
		comparison.VacancyVsNational = data.RentalVacancyRate - nationalVacancy
	}

	// Affordability comparison (national avg ~100)
	if data.AffordabilityIndex > 0 {
		comparison.AffordabilityVsNational = data.AffordabilityIndex - 100
	}

	return comparison
}

// Formatting helpers
func formatPercent(v float64) string {
	return fmt.Sprintf("%.1f%%", v)
}

func formatRatio(v float64) string {
	return fmt.Sprintf("%.1fx", v)
}
