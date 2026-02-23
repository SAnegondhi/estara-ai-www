// Package fred provides a centralized service for Federal Reserve Economic Data (FRED).
// It manages caching based on FRED's data release schedules for optimal freshness.
//
// FRED Release Schedules:
// - Mortgage rates (MORTGAGE30US, MORTGAGE15US): Weekly, Thursdays ~10am ET
// - Unemployment (UNRATE): Monthly, first Friday of month ~8:30am ET
// - CPI (CPIAUCSL): Monthly, around 10th-13th ~8:30am ET
// - Rental Vacancy (RRVRUSQ156N): Quarterly
package fred

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/market/timeseries"
)

const (
	// Cache key prefix
	cacheKeyPrefix = "fred:service:"

	// Cache keys for aggregated data
	cacheKeyAllRates = cacheKeyPrefix + "all_rates"

	// Default TTL for when we can't calculate next release
	defaultCacheTTL = 24 * time.Hour

	// Minimum cache TTL to prevent excessive API calls
	minCacheTTL = 1 * time.Hour

	// Maximum cache TTL (7 days) as fallback
	maxCacheTTL = 7 * 24 * time.Hour
)

// EconomicRates contains all cached FRED economic data
type EconomicRates struct {
	// Mortgage rates
	MortgageRate30Year float64   `json:"mortgageRate30Year"`
	MortgageRate15Year float64   `json:"mortgageRate15Year"`
	MortgageRateDate   time.Time `json:"mortgageRateDate"`

	// Labor market
	UnemploymentRate     float64   `json:"unemploymentRate"`
	UnemploymentRateDate time.Time `json:"unemploymentRateDate"`

	// Treasury bill rate (risk-free proxy for Sharpe calculations)
	TBillRate     float64   `json:"tBillRate"`
	TBillRateDate time.Time `json:"tBillRateDate"`

	// Inflation
	InflationRate     float64   `json:"inflationRate"`
	InflationRateDate time.Time `json:"inflationRateDate"`

	// Housing
	RentalVacancyRate     float64   `json:"rentalVacancyRate"`
	RentalVacancyRateDate time.Time `json:"rentalVacancyRateDate"`

	// Supply pipeline (national)
	BuildingPermits     float64   `json:"buildingPermits"`     // thousands, annualized
	BuildingPermitsDate time.Time `json:"buildingPermitsDate"`
	HousingStarts       float64   `json:"housingStarts"`       // thousands, annualized
	HousingStartsDate   time.Time `json:"housingStartsDate"`

	// Metadata
	LastUpdated time.Time `json:"lastUpdated"`
	NextRefresh time.Time `json:"nextRefresh"`
	Source      string    `json:"source"`
}

// Service provides centralized access to FRED economic data with smart caching
// Uses three-tier caching: L0 (in-memory) -> L1 (Redis) -> L2 (PostgreSQL)
type Service struct {
	client  *timeseries.FREDClient
	redis   *redisClient.Client
	queries *queries.Queries // L2 cache (PostgreSQL)
	logger  *slog.Logger

	// In-memory cache (L0) for fast access
	mu          sync.RWMutex
	cachedRates *EconomicRates
	cacheExpiry time.Time
}

// NewService creates a new FRED service
// Pass queries for L2 PostgreSQL caching (nil disables L2)
func NewService(apiKey string, redis *redisClient.Client, q *queries.Queries) *Service {
	return &Service{
		client:  timeseries.NewFREDClient(apiKey, redis),
		redis:   redis,
		queries: q,
		logger:  slog.Default().With("component", "fred_service"),
	}
}

// GetAllRates returns all cached economic rates, fetching fresh data if needed
// Cache lookup order: L0 (memory) -> L1 (Redis) -> L2 (PostgreSQL) -> FRED API
func (s *Service) GetAllRates(ctx context.Context) (*EconomicRates, error) {
	// L0: Check in-memory cache first (instant)
	s.mu.RLock()
	if s.cachedRates != nil && time.Now().Before(s.cacheExpiry) {
		rates := *s.cachedRates
		s.mu.RUnlock()
		return &rates, nil
	}
	s.mu.RUnlock()

	// L1: Try Redis cache (fast)
	if s.redis != nil {
		cached, err := s.redis.GetJSON(ctx, cacheKeyAllRates)
		if err == nil && cached != nil {
			var rates EconomicRates
			if err := json.Unmarshal(cached, &rates); err == nil {
				// Check if still valid
				if time.Now().Before(rates.NextRefresh) {
					s.updateMemoryCache(&rates)
					s.logger.Debug("FRED rates loaded from L1 (Redis)")
					return &rates, nil
				}
			}
		}
	}

	// L2: Try PostgreSQL cache (durable)
	if s.queries != nil {
		cached, err := s.queries.GetSystemCache(ctx, cacheKeyAllRates)
		if err == nil {
			var rates EconomicRates
			if err := json.Unmarshal(cached.Value, &rates); err == nil {
				// Check if still valid
				if time.Now().Before(rates.NextRefresh) {
					s.updateMemoryCache(&rates)
					s.updateRedisCache(ctx, &rates) // Warm L1 from L2
					s.logger.Debug("FRED rates loaded from L2 (PostgreSQL)")
					return &rates, nil
				}
			}
		}
	}

	// Fetch fresh data from FRED API
	return s.refreshRates(ctx)
}

// GetMortgageRate returns the current 30-year fixed mortgage rate
func (s *Service) GetMortgageRate(ctx context.Context) (float64, error) {
	rates, err := s.GetAllRates(ctx)
	if err != nil {
		return 0, err
	}
	return rates.MortgageRate30Year, nil
}

// GetMortgageRates returns both 30-year and 15-year rates
func (s *Service) GetMortgageRates(ctx context.Context) (rate30, rate15 float64, asOf time.Time, err error) {
	rates, err := s.GetAllRates(ctx)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	return rates.MortgageRate30Year, rates.MortgageRate15Year, rates.MortgageRateDate, nil
}

// GetUnemploymentRate returns the current unemployment rate
func (s *Service) GetUnemploymentRate(ctx context.Context) (float64, error) {
	rates, err := s.GetAllRates(ctx)
	if err != nil {
		return 0, err
	}
	return rates.UnemploymentRate, nil
}

// GetInflationRate returns the current YoY inflation rate
func (s *Service) GetInflationRate(ctx context.Context) (float64, error) {
	rates, err := s.GetAllRates(ctx)
	if err != nil {
		return 0, err
	}
	return rates.InflationRate, nil
}

// GetRentalVacancyRate returns the national rental vacancy rate
func (s *Service) GetRentalVacancyRate(ctx context.Context) (float64, error) {
	rates, err := s.GetAllRates(ctx)
	if err != nil {
		return 0, err
	}
	return rates.RentalVacancyRate, nil
}

// ForceRefresh forces a refresh of all cached data
func (s *Service) ForceRefresh(ctx context.Context) (*EconomicRates, error) {
	s.logger.Info("forcing FRED data refresh")
	return s.refreshRates(ctx)
}

// IsConfigured returns true if the FRED client has an API key
func (s *Service) IsConfigured() bool {
	return s.client.IsConfigured()
}

// refreshRates fetches all rates from FRED and updates caches
func (s *Service) refreshRates(ctx context.Context) (*EconomicRates, error) {
	s.logger.Info("refreshing FRED rates from API")

	rates := &EconomicRates{
		LastUpdated: time.Now(),
		Source:      "FRED (Federal Reserve Economic Data)",
	}

	// Fetch mortgage rates
	mortgageRates, err := s.client.GetMortgageRates(ctx)
	if err != nil {
		s.logger.Warn("failed to fetch mortgage rates", "error", err)
	} else if mortgageRates != nil {
		rates.MortgageRate30Year = mortgageRates.Rate30Year
		rates.MortgageRate15Year = mortgageRates.Rate15Year
		rates.MortgageRateDate = mortgageRates.Date
	}

	// Fetch unemployment rate
	unemployment, unemploymentDate, err := s.client.GetUnemploymentRate(ctx)
	if err != nil {
		s.logger.Warn("failed to fetch unemployment rate", "error", err)
	} else {
		rates.UnemploymentRate = unemployment
		rates.UnemploymentRateDate = unemploymentDate
	}

	// Fetch 3-month T-bill rate (risk-free rate proxy for Sharpe calculations)
	tbill, tbillDate, err := s.client.GetTBillRate(ctx)
	if err != nil {
		s.logger.Warn("failed to fetch T-bill rate", "error", err)
	} else {
		rates.TBillRate = tbill
		rates.TBillRateDate = tbillDate
	}

	// Fetch inflation rate
	inflation, inflationDate, err := s.client.GetInflationRate(ctx)
	if err != nil {
		s.logger.Warn("failed to fetch inflation rate", "error", err)
	} else {
		rates.InflationRate = inflation
		rates.InflationRateDate = inflationDate
	}

	// Fetch rental vacancy rate
	vacancy, vacancyDate, err := s.client.GetRentalVacancyRate(ctx)
	if err != nil {
		s.logger.Warn("failed to fetch rental vacancy rate", "error", err)
	} else {
		rates.RentalVacancyRate = vacancy
		rates.RentalVacancyRateDate = vacancyDate
	}

	// Fetch building permits (national, monthly, thousands annualized)
	permits, permitsDate, err := s.client.GetBuildingPermits(ctx)
	if err != nil {
		s.logger.Warn("failed to fetch building permits", "error", err)
	} else {
		rates.BuildingPermits = permits
		rates.BuildingPermitsDate = permitsDate
	}

	// Fetch housing starts (national, monthly, thousands annualized)
	starts, startsDate, err := s.client.GetHousingStarts(ctx)
	if err != nil {
		s.logger.Warn("failed to fetch housing starts", "error", err)
	} else {
		rates.HousingStarts = starts
		rates.HousingStartsDate = startsDate
	}

	// Calculate next refresh time based on release schedules
	rates.NextRefresh = s.calculateNextRefresh()

	// Update all cache layers
	s.updateMemoryCache(rates)
	s.updateRedisCache(ctx, rates)
	s.updateL2Cache(ctx, rates)

	s.logger.Info("FRED rates refreshed",
		"mortgageRate30", rates.MortgageRate30Year,
		"unemployment", rates.UnemploymentRate,
		"nextRefresh", rates.NextRefresh,
	)

	return rates, nil
}

// updateMemoryCache updates the in-memory cache
func (s *Service) updateMemoryCache(rates *EconomicRates) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cachedRates = rates
	s.cacheExpiry = rates.NextRefresh
}

// updateRedisCache updates the L1 Redis cache
func (s *Service) updateRedisCache(ctx context.Context, rates *EconomicRates) {
	if s.redis == nil {
		return
	}

	data, err := json.Marshal(rates)
	if err != nil {
		s.logger.Warn("failed to marshal rates for L1 cache", "error", err)
		return
	}

	ttl := time.Until(rates.NextRefresh)
	if ttl < minCacheTTL {
		ttl = minCacheTTL
	}
	if ttl > maxCacheTTL {
		ttl = maxCacheTTL
	}

	if err := s.redis.SetJSON(ctx, cacheKeyAllRates, data, ttl); err != nil {
		s.logger.Warn("failed to cache rates in L1 (Redis)", "error", err)
	} else {
		s.logger.Debug("cached FRED rates in L1 (Redis)", "ttl", ttl)
	}
}

// updateL2Cache updates the L2 PostgreSQL cache for durability
func (s *Service) updateL2Cache(ctx context.Context, rates *EconomicRates) {
	if s.queries == nil {
		return
	}

	data, err := json.Marshal(rates)
	if err != nil {
		s.logger.Warn("failed to marshal rates for L2 cache", "error", err)
		return
	}

	expiresAt := rates.NextRefresh
	if expiresAt.Before(time.Now().Add(minCacheTTL)) {
		expiresAt = time.Now().Add(minCacheTTL)
	}

	_, err = s.queries.UpsertSystemCache(ctx, queries.UpsertSystemCacheParams{
		Key:       cacheKeyAllRates,
		Value:     data,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		s.logger.Warn("failed to cache rates in L2 (PostgreSQL)", "error", err)
	} else {
		s.logger.Debug("cached FRED rates in L2 (PostgreSQL)", "expiresAt", expiresAt)
	}
}

// calculateNextRefresh determines when to next refresh based on FRED release schedules
func (s *Service) calculateNextRefresh() time.Time {
	now := time.Now()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.UTC
	}
	nowET := now.In(loc)

	// Find the next Thursday at 11am ET (mortgage rates release ~10am, add buffer)
	nextThursday := s.nextWeekday(nowET, time.Thursday, 11, 0)

	// Find the next first Friday of month at 9am ET (jobs report ~8:30am)
	nextFirstFriday := s.nextFirstFriday(nowET, 9, 0)

	// Find the next ~12th of month at 9am ET (CPI release)
	nextCPIRelease := s.nextMonthDay(nowET, 12, 9, 0)

	// Return the earliest of these (most frequent is mortgage rates on Thursdays)
	earliest := nextThursday
	if nextFirstFriday.Before(earliest) {
		earliest = nextFirstFriday
	}
	if nextCPIRelease.Before(earliest) {
		earliest = nextCPIRelease
	}

	// Ensure minimum TTL
	minTime := now.Add(minCacheTTL)
	if earliest.Before(minTime) {
		earliest = minTime
	}

	return earliest
}

// nextWeekday finds the next occurrence of a weekday at a specific time
func (s *Service) nextWeekday(from time.Time, weekday time.Weekday, hour, minute int) time.Time {
	daysUntil := int(weekday - from.Weekday())
	if daysUntil <= 0 {
		daysUntil += 7
	}

	// If it's the same day but after the time, use this week
	if daysUntil == 7 {
		target := time.Date(from.Year(), from.Month(), from.Day(), hour, minute, 0, 0, from.Location())
		if from.Before(target) {
			return target
		}
	}

	next := from.AddDate(0, 0, daysUntil)
	return time.Date(next.Year(), next.Month(), next.Day(), hour, minute, 0, 0, from.Location())
}

// nextFirstFriday finds the next first Friday of a month at a specific time
func (s *Service) nextFirstFriday(from time.Time, hour, minute int) time.Time {
	// Start from the 1st of current month
	year, month, _ := from.Date()

	for i := 0; i < 3; i++ { // Check up to 3 months
		// Find first Friday of this month
		first := time.Date(year, month, 1, hour, minute, 0, 0, from.Location())
		daysUntilFriday := int(time.Friday - first.Weekday())
		if daysUntilFriday < 0 {
			daysUntilFriday += 7
		}
		firstFriday := first.AddDate(0, 0, daysUntilFriday)

		if firstFriday.After(from) {
			return firstFriday
		}

		// Move to next month
		month++
		if month > 12 {
			month = 1
			year++
		}
	}

	// Fallback
	return from.Add(defaultCacheTTL)
}

// nextMonthDay finds the next occurrence of a specific day of month
func (s *Service) nextMonthDay(from time.Time, day, hour, minute int) time.Time {
	year, month, _ := from.Date()

	target := time.Date(year, month, day, hour, minute, 0, 0, from.Location())
	if target.After(from) {
		return target
	}

	// Next month
	month++
	if month > 12 {
		month = 1
		year++
	}
	return time.Date(year, month, day, hour, minute, 0, 0, from.Location())
}

// StartBackgroundRefresh starts a background goroutine that refreshes data
// based on release schedules. Call this on service startup.
func (s *Service) StartBackgroundRefresh(ctx context.Context) {
	go func() {
		// Initial fetch
		if _, err := s.GetAllRates(ctx); err != nil {
			s.logger.Warn("initial FRED fetch failed", "error", err)
		}

		for {
			// Calculate sleep duration until next refresh
			s.mu.RLock()
			nextRefresh := s.cacheExpiry
			s.mu.RUnlock()

			sleepDuration := time.Until(nextRefresh)
			if sleepDuration < minCacheTTL {
				sleepDuration = minCacheTTL
			}

			s.logger.Debug("FRED background refresh sleeping", "until", nextRefresh, "duration", sleepDuration)

			select {
			case <-ctx.Done():
				s.logger.Info("FRED background refresh stopped")
				return
			case <-time.After(sleepDuration):
				if _, err := s.ForceRefresh(ctx); err != nil {
					s.logger.Warn("background FRED refresh failed", "error", err)
				}
			}
		}
	}()

	s.logger.Info("FRED background refresh started")
}

// GetMetroEmploymentGrowth delegates to the underlying client
func (s *Service) GetMetroEmploymentGrowth(ctx context.Context, city, state string) (float64, error) {
	return s.client.GetMetroEmploymentGrowth(ctx, city, state)
}

// GetSeries delegates to the underlying client for historical data
func (s *Service) GetSeries(ctx context.Context, seriesID string, startDate, endDate time.Time) ([]timeseries.FREDObservation, error) {
	return s.client.GetSeries(ctx, seriesID, startDate, endDate)
}

// String returns a string representation for logging
func (r *EconomicRates) String() string {
	return fmt.Sprintf("EconomicRates{mortgage30=%.2f%%, unemployment=%.1f%%, inflation=%.1f%%, vacancy=%.1f%%}",
		r.MortgageRate30Year, r.UnemploymentRate, r.InflationRate, r.RentalVacancyRate)
}
