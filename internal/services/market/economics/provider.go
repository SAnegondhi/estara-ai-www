// Package economics provides unified economic data access (ADR-068/ADR-069)
package economics

import (
	"context"
	"log/slog"
)

// Provider interface for economic data access
// Used by services that need economic context (ADR-069)
type Provider interface {
	// GetMarketEconomics returns unified economic data for a location
	GetMarketEconomics(ctx context.Context, city, state string) (*MarketEconomics, error)

	// GetMarketEconomicsWithPrice returns economics with calculated affordability metrics
	GetMarketEconomicsWithPrice(ctx context.Context, city, state string, medianHomePrice float64) (*MarketEconomics, error)

	// GetMortgageRate returns the current 30-year fixed mortgage rate
	GetMortgageRate(ctx context.Context) (float64, error)

	// GetVacancyRate returns the national rental vacancy rate
	GetVacancyRate(ctx context.Context) (float64, error)

	// IsConfigured returns true if at least one data source is available
	IsConfigured() bool
}

// Ensure Aggregator implements Provider
var _ Provider = (*Aggregator)(nil)

// GetMortgageRate returns the 30-year fixed mortgage rate from FRED
func (a *Aggregator) GetMortgageRate(ctx context.Context) (float64, error) {
	if a.fred == nil || !a.fred.IsConfigured() {
		return 0, nil
	}
	return a.fred.GetMortgageRate(ctx)
}

// GetVacancyRate returns the national rental vacancy rate from FRED
func (a *Aggregator) GetVacancyRate(ctx context.Context) (float64, error) {
	if a.fred == nil || !a.fred.IsConfigured() {
		return 0, nil
	}
	rates, err := a.fred.GetAllRates(ctx)
	if err != nil {
		return 0, err
	}
	return rates.RentalVacancyRate, nil
}

// DefaultProvider provides fallback values when no economic data is available
type DefaultProvider struct {
	logger *slog.Logger
}

// NewDefaultProvider creates a provider that returns sensible defaults
func NewDefaultProvider() *DefaultProvider {
	return &DefaultProvider{
		logger: slog.Default().With("component", "economics_default_provider"),
	}
}

// GetMarketEconomics returns default economic data
func (p *DefaultProvider) GetMarketEconomics(ctx context.Context, city, state string) (*MarketEconomics, error) {
	p.logger.Warn("using default economic data (no services configured)", "city", city, "state", state)
	return &MarketEconomics{
		City:                  city,
		State:                 state,
		MortgageRate30Year:    7.0,  // Default 7%
		MortgageRate15Year:    6.25, // Default 15-year
		NationalUnemployment:  4.0,  // Historical average
		InflationRate:         2.5,  // Fed target
		RentalVacancyRate:     6.0,  // Historical average
		StateUnemploymentRate: 4.0,
		Sources:               map[string]string{"default": "fallback values"},
		Errors:                []string{"No economic data services configured - using defaults"},
	}, nil
}

// GetMarketEconomicsWithPrice returns defaults with basic affordability calculation
func (p *DefaultProvider) GetMarketEconomicsWithPrice(ctx context.Context, city, state string, medianHomePrice float64) (*MarketEconomics, error) {
	data, err := p.GetMarketEconomics(ctx, city, state)
	if err != nil {
		return nil, err
	}

	// Default median income for affordability (US median ~$75K)
	defaultIncome := 75000.0
	data.MedianHouseholdIncome = defaultIncome
	data.PriceToIncomeRatio = medianHomePrice / defaultIncome

	return data, nil
}

// GetMortgageRate returns the default mortgage rate
func (p *DefaultProvider) GetMortgageRate(ctx context.Context) (float64, error) {
	return 7.0, nil
}

// GetVacancyRate returns the default vacancy rate
func (p *DefaultProvider) GetVacancyRate(ctx context.Context) (float64, error) {
	return 6.0, nil
}

// IsConfigured returns false for the default provider
func (p *DefaultProvider) IsConfigured() bool {
	return false
}
