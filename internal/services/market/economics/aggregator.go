// Package economics provides a unified aggregator for economic data from
// FRED, Census, and BLS services (ADR-068 Phase 4).
//
// The aggregator fetches data from all sources in parallel and merges them
// into a single MarketEconomics structure with calculated derived metrics.
package economics

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/estara-ai/www/internal/services/market/bls"
	"github.com/estara-ai/www/internal/services/market/census"
	"github.com/estara-ai/www/internal/services/market/fred"
)

// MarketEconomics contains unified economic data from all sources
type MarketEconomics struct {
	// Location context
	City  string `json:"city"`
	State string `json:"state"`

	// From FRED (national rates)
	MortgageRate30Year    float64 `json:"mortgageRate30Year"`
	MortgageRate15Year    float64 `json:"mortgageRate15Year"`
	NationalUnemployment  float64 `json:"nationalUnemployment"`
	InflationRate         float64 `json:"inflationRate"`
	RentalVacancyRate     float64 `json:"rentalVacancyRate"`

	// From Census (city/state demographics)
	Population            int64   `json:"population"`
	MedianHouseholdIncome float64 `json:"medianHouseholdIncome"`
	PerCapitaIncome       float64 `json:"perCapitaIncome"`
	PovertyRate           float64 `json:"povertyRate"`
	MedianAge             float64 `json:"medianAge"`
	HouseholdCount        int64   `json:"householdCount"`
	DemographicLevel      string  `json:"demographicLevel"` // "place", "county", or "state"

	// From BLS (labor market)
	StateUnemploymentRate   float64 `json:"stateUnemploymentRate"`
	StateEmployment         float64 `json:"stateEmployment"` // thousands
	CPIShelter              float64 `json:"cpiShelter"`
	CPIRent                 float64 `json:"cpiRent"`
	AverageHourlyEarnings   float64 `json:"averageHourlyEarnings"`
	ConstructionEmployment  float64 `json:"constructionEmployment"` // thousands
	JobOpenings             float64 `json:"jobOpenings"`            // thousands
	LaborForceParticipation float64 `json:"laborForceParticipation"`

	// Calculated Metrics
	PriceToIncomeRatio float64 `json:"priceToIncomeRatio,omitempty"` // Requires median home price input
	AffordabilityIndex float64 `json:"affordabilityIndex,omitempty"` // Based on mortgage, income, prices

	// Metadata
	LastUpdated time.Time         `json:"lastUpdated"`
	Sources     map[string]string `json:"sources"` // {"fred": "2026-02-07", "census": "2022", "bls": "2026-01"}
	Errors      []string          `json:"errors,omitempty"`
}

// Aggregator combines data from FRED, Census, and BLS services
type Aggregator struct {
	fred   *fred.Service
	census *census.Service
	bls    *bls.Service
	logger *slog.Logger
}

// NewAggregator creates a new economic data aggregator
func NewAggregator(fredSvc *fred.Service, censusSvc *census.Service, blsSvc *bls.Service) *Aggregator {
	return &Aggregator{
		fred:   fredSvc,
		census: censusSvc,
		bls:    blsSvc,
		logger: slog.Default().With("component", "economics_aggregator"),
	}
}

// GetMarketEconomics fetches and merges data from all sources
// Fetches in parallel for optimal performance
func (a *Aggregator) GetMarketEconomics(ctx context.Context, city, state string) (*MarketEconomics, error) {
	a.logger.Info("fetching market economics", "city", city, "state", state)

	result := &MarketEconomics{
		City:        city,
		State:       state,
		LastUpdated: time.Now(),
		Sources:     make(map[string]string),
		Errors:      []string{},
	}

	// Fetch from all sources in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex

	var fredData *fred.EconomicRates
	var censusData *census.CensusData
	var blsData *bls.BLSData

	// FRED (national economic rates)
	if a.fred != nil && a.fred.IsConfigured() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := a.fred.GetAllRates(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("FRED: %v", err))
				a.logger.Warn("failed to fetch FRED data", "error", err)
			} else {
				fredData = data
				result.Sources["fred"] = data.LastUpdated.Format("2006-01-02")
			}
		}()
	}

	// Census (demographics)
	if a.census != nil && a.census.IsConfigured() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := a.census.GetDemographics(ctx, city, state)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Census: %v", err))
				a.logger.Warn("failed to fetch Census data", "error", err, "city", city, "state", state)
			} else {
				censusData = data
				result.Sources["census"] = fmt.Sprintf("%d", data.DataYear)
			}
		}()
	}

	// BLS (labor market - combined national + state)
	if a.bls != nil && a.bls.IsConfigured() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := a.bls.GetCombinedData(ctx, state)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("BLS: %v", err))
				a.logger.Warn("failed to fetch BLS data", "error", err, "state", state)
			} else {
				blsData = data
				result.Sources["bls"] = data.AsOfDate
			}
		}()
	}

	wg.Wait()

	// Merge FRED data
	if fredData != nil {
		result.MortgageRate30Year = fredData.MortgageRate30Year
		result.MortgageRate15Year = fredData.MortgageRate15Year
		result.NationalUnemployment = fredData.UnemploymentRate
		result.InflationRate = fredData.InflationRate
		result.RentalVacancyRate = fredData.RentalVacancyRate
	}

	// Merge Census data
	if censusData != nil {
		result.Population = censusData.Population
		result.MedianHouseholdIncome = censusData.MedianHouseholdIncome
		result.PerCapitaIncome = censusData.PerCapitaIncome
		result.PovertyRate = censusData.PovertyRate
		result.MedianAge = censusData.MedianAge
		result.HouseholdCount = censusData.HouseholdCount
		result.DemographicLevel = censusData.Level
	}

	// Merge BLS data
	if blsData != nil {
		result.StateUnemploymentRate = blsData.StateUnemploymentRate
		result.StateEmployment = blsData.StateEmployment
		result.CPIShelter = blsData.CPIShelter
		result.CPIRent = blsData.CPIRent
		result.AverageHourlyEarnings = blsData.AverageHourlyEarnings
		result.ConstructionEmployment = blsData.ConstructionEmployment
		result.JobOpenings = blsData.JobOpenings
		result.LaborForceParticipation = blsData.LaborForceParticipation
	}

	// Clear errors if empty
	if len(result.Errors) == 0 {
		result.Errors = nil
	}

	a.logger.Info("market economics fetched",
		"city", city,
		"state", state,
		"sources", len(result.Sources),
		"errors", len(result.Errors),
	)

	return result, nil
}

// GetMarketEconomicsWithPrice fetches data and calculates price-based metrics
// medianHomePrice should be in dollars (e.g., 450000)
func (a *Aggregator) GetMarketEconomicsWithPrice(ctx context.Context, city, state string, medianHomePrice float64) (*MarketEconomics, error) {
	result, err := a.GetMarketEconomics(ctx, city, state)
	if err != nil {
		return nil, err
	}

	// Calculate Price-to-Income Ratio
	if result.MedianHouseholdIncome > 0 && medianHomePrice > 0 {
		result.PriceToIncomeRatio = medianHomePrice / result.MedianHouseholdIncome
	}

	// Calculate Affordability Index
	// Based on: Can a median-income household afford a median-priced home?
	// Index of 100 = exactly affordable, >100 = more affordable, <100 = less affordable
	if result.MedianHouseholdIncome > 0 && medianHomePrice > 0 && result.MortgageRate30Year > 0 {
		result.AffordabilityIndex = a.calculateAffordabilityIndex(
			result.MedianHouseholdIncome,
			medianHomePrice,
			result.MortgageRate30Year,
		)
	}

	return result, nil
}

// calculateAffordabilityIndex computes housing affordability
// Returns index where 100 = exactly affordable (28% of income to housing)
func (a *Aggregator) calculateAffordabilityIndex(income, homePrice, mortgageRate float64) float64 {
	// Assume 20% down payment, 30-year mortgage
	downPayment := homePrice * 0.20
	loanAmount := homePrice - downPayment

	// Monthly mortgage rate
	monthlyRate := (mortgageRate / 100) / 12
	numPayments := float64(30 * 12)

	// Monthly payment (P&I only)
	var monthlyPayment float64
	if monthlyRate > 0 {
		monthlyPayment = loanAmount * (monthlyRate * pow(1+monthlyRate, numPayments)) / (pow(1+monthlyRate, numPayments) - 1)
	} else {
		monthlyPayment = loanAmount / numPayments
	}

	// Add estimated taxes and insurance (~1.5% of home value annually)
	monthlyTaxInsurance := (homePrice * 0.015) / 12
	totalMonthlyHousing := monthlyPayment + monthlyTaxInsurance

	// Monthly income
	monthlyIncome := income / 12

	// Qualifying ratio (28% of income for housing is standard)
	qualifyingPayment := monthlyIncome * 0.28

	// Affordability index: (qualifying payment / actual payment) * 100
	if totalMonthlyHousing > 0 {
		return (qualifyingPayment / totalMonthlyHousing) * 100
	}
	return 0
}

// pow calculates x^n for float64
func pow(x float64, n float64) float64 {
	result := 1.0
	for i := 0; i < int(n); i++ {
		result *= x
	}
	return result
}

// IsConfigured returns true if at least one data source is available
func (a *Aggregator) IsConfigured() bool {
	fredOK := a.fred != nil && a.fred.IsConfigured()
	censusOK := a.census != nil && a.census.IsConfigured()
	blsOK := a.bls != nil && a.bls.IsConfigured()
	return fredOK || censusOK || blsOK
}

// AvailableSources returns which data sources are configured
func (a *Aggregator) AvailableSources() []string {
	sources := []string{}
	if a.fred != nil && a.fred.IsConfigured() {
		sources = append(sources, "fred")
	}
	if a.census != nil && a.census.IsConfigured() {
		sources = append(sources, "census")
	}
	if a.bls != nil && a.bls.IsConfigured() {
		sources = append(sources, "bls")
	}
	return sources
}

// String returns a string representation for logging
func (m *MarketEconomics) String() string {
	return fmt.Sprintf("MarketEconomics{city=%s, state=%s, mortgage=%.2f%%, stateUnemp=%.1f%%, income=$%.0f, pop=%d}",
		m.City, m.State, m.MortgageRate30Year, m.StateUnemploymentRate, m.MedianHouseholdIncome, m.Population)
}
