package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/market/fred"
	"github.com/estara-ai/www/internal/services/market/timeseries"
)

const (
	// Maximum variance between sources before using primary source only
	maxSourceVariance = 0.5 // 50%

	// Cache TTL for aggregated market data
	// Market data is mostly static (updates monthly), so 7-day TTL is safe
	cacheKeyPrefix = "market_data:"
	cacheTTL       = 7 * 24 * time.Hour // 7 days (was 24 hours)

	// In-memory cache TTL for ultra-fast repeated lookups
	memoryCacheTTL = 24 * time.Hour // 1 day
)

// MarketData represents aggregated market data from multiple sources
type MarketData struct {
	City                 string    `json:"city"`
	State                string    `json:"state"`
	MedianHomePrice      int       `json:"medianHomePrice"`
	MedianRent           int       `json:"medianRent"`
	CapRate              float64   `json:"capRate"`
	MortgageRate30       float64   `json:"mortgageRate30"`
	MortgageRate15       float64   `json:"mortgageRate15"`
	YearOverYearPct      float64   `json:"yearOverYearPct"`
	RentYearOverYear     float64   `json:"rentYearOverYearPct"`
	VacancyRate          float64   `json:"vacancyRate"`          // Rental vacancy rate (%)
	UnemploymentRate     float64   `json:"unemploymentRate"`     // Local unemployment rate (%)
	EmploymentGrowthRate float64   `json:"employmentGrowthRate"` // YoY employment growth (%)
	PopulationGrowthRate float64   `json:"populationGrowthRate"` // YoY population growth (%)
	MedianDaysOnMarket   *int      `json:"medianDaysOnMarket"`   // Median DOM from Redfin (ADR-076)
	Confidence           float64   `json:"confidence"`
	Sources              []string  `json:"sources"`
	DataDate             time.Time `json:"dataDate"`
	IsAIEstimated        bool      `json:"isAiEstimated"`
}

// SourceData holds data from a single source for cross-validation
type SourceData struct {
	Source          string
	MedianHomePrice float64
	MedianRent      float64
	Date            time.Time
}

// memoryCacheEntry holds cached data with expiration
type memoryCacheEntry struct {
	data      *MarketData
	expiresAt time.Time
}

// Aggregator aggregates data from multiple market data sources
type Aggregator struct {
	metro       *timeseries.MetroReader
	fred        *fred.Service
	cache       *cache.HybridCache
	memoryCache sync.Map // In-memory cache for ultra-fast repeated lookups
	logger      *slog.Logger
}

// NewAggregator creates a new market data aggregator
// ADR-076: Uses fred.Service (3-tier cache) instead of legacy FREDClient
func NewAggregator(
	metro *timeseries.MetroReader,
	fredService *fred.Service,
	hybridCache *cache.HybridCache,
) *Aggregator {
	return &Aggregator{
		metro:  metro,
		fred:   fredService,
		cache:  hybridCache,
		logger: slog.Default().With("component", "market_aggregator"),
	}
}

// GetMarketData returns aggregated market data for a city/state
func (a *Aggregator) GetMarketData(ctx context.Context, city, state string) (*MarketData, error) {
	cacheKey := a.buildCacheKey(city, state)

	// L0: Check in-memory cache first (ultra-fast, ~1µs)
	if entry, ok := a.memoryCache.Load(cacheKey); ok {
		if cached, ok := entry.(*memoryCacheEntry); ok && time.Now().Before(cached.expiresAt) {
			a.logger.Debug("memory cache hit for market data", "city", city, "state", state)
			return cached.data, nil
		}
		// Expired entry, delete it
		a.memoryCache.Delete(cacheKey)
	}

	// L1/L2: Check Redis/DB cache (fast, ~1-5ms)
	if a.cache != nil {
		cached, err := a.cache.Get(ctx, "", cacheKey)
		if err == nil && cached != nil {
			a.logger.Debug("hybrid cache hit for market data", "city", city, "state", state)
			// Parse cached data
			var data MarketData
			if err := parseJSON(cached, &data); err == nil {
				// Store in memory cache for faster subsequent lookups
				a.memoryCache.Store(cacheKey, &memoryCacheEntry{
					data:      &data,
					expiresAt: time.Now().Add(memoryCacheTTL),
				})
				return &data, nil
			}
		}
	}

	// Collect data from all sources
	sources := make([]SourceData, 0, 2)
	var metroData *timeseries.MetroData
	var yoyData *timeseries.YearOverYearData
	var fredRates *fred.EconomicRates

	// Get metro data (Zillow)
	var snapshot *timeseries.CitySnapshot
	metroData, err := a.metro.GetMarketData(ctx, city, state)
	if err != nil {
		a.logger.Warn("failed to get metro data", "city", city, "state", state, "error", err)
	} else {
		sources = append(sources, SourceData{
			Source:          "Zillow",
			MedianHomePrice: metroData.ZHVI,
			MedianRent:      metroData.ZORI,
			Date:            metroData.Date,
		})

		// Get year-over-year data if we have region ID
		if metroData.RegionID > 0 {
			yoyData, err = a.metro.GetYearOverYearChange(ctx, metroData.RegionID)
			if err != nil {
				a.logger.Warn("failed to get YoY data", "regionId", metroData.RegionID, "error", err)
			}
		}

		// Get CitySnapshot for Redfin DOM data (ADR-076)
		snapshot, err = a.metro.GetCitySnapshot(ctx, city, state)
		if err != nil {
			a.logger.Debug("failed to get city snapshot for DOM", "city", city, "state", state, "error", err)
		}
	}

	// Get all FRED rates via fred.Service (3-tier cache, release-schedule-aware TTL)
	if a.fred != nil && a.fred.IsConfigured() {
		fredRates, err = a.fred.GetAllRates(ctx)
		if err != nil {
			a.logger.Warn("failed to get FRED rates", "error", err)
		}
	}

	// Extract vacancy rate from FRED or use default
	var vacancyRate float64
	if fredRates != nil && fredRates.RentalVacancyRate > 0 {
		vacancyRate = fredRates.RentalVacancyRate
	} else {
		vacancyRate = 6.5 // Default national average
	}

	// Get employment growth from FRED
	var employmentGrowth float64
	if a.fred != nil && a.fred.IsConfigured() {
		eg, err := a.fred.GetMetroEmploymentGrowth(ctx, city, state)
		if err != nil {
			a.logger.Debug("failed to get metro employment growth, using default", "city", city, "state", state, "error", err)
			employmentGrowth = 1.5 // Default moderate growth
		} else {
			a.logger.Info("fetched metro employment growth", "city", city, "state", state, "growth", eg)
			employmentGrowth = eg
		}
	} else {
		a.logger.Debug("FRED not configured, using default employment growth", "city", city, "state", state)
		employmentGrowth = 1.5 // Default when FRED not configured
	}

	// Population growth - Census API deprecated since 2020, use regional defaults
	// TODO: Integrate Census flat files for accurate population data
	populationGrowth := getRegionalPopulationGrowthDefault(state)
	a.logger.Debug("population growth for state", "state", state, "growth", populationGrowth)

	// Cross-validate sources — with only one source (Zillow), variance check is N/A
	if len(sources) > 1 && !a.validateSources(sources) {
		a.logger.Warn("source variance too high, using primary source only",
			"city", city,
			"state", state,
		)
		// Per ADR-050/076: no AI fabrication. Use first source only.
		sources = sources[:1]
	}

	// If no data sources, return nil (ADR-050/076: no AI-fabricated fallback)
	if len(sources) == 0 {
		a.logger.Warn("no market data sources available",
			"city", city,
			"state", state,
		)
		return nil, fmt.Errorf("no market data available for %s, %s", city, state)
	}

	// Aggregate data
	data := a.aggregateData(city, state, sources, metroData, yoyData, fredRates, snapshot, vacancyRate, employmentGrowth, populationGrowth)

	// Cache the result in L1/L2 (Redis/DB)
	if a.cache != nil {
		if err := a.cache.Set(ctx, "", cacheKey, "market_data", data, cacheTTL); err != nil {
			a.logger.Warn("failed to cache market data", "error", err)
		}
	}

	// Also cache in L0 (memory) for ultra-fast subsequent lookups
	a.memoryCache.Store(cacheKey, &memoryCacheEntry{
		data:      data,
		expiresAt: time.Now().Add(memoryCacheTTL),
	})

	return data, nil
}

// validateSources checks if sources agree within acceptable variance
func (a *Aggregator) validateSources(sources []SourceData) bool {
	if len(sources) < 2 {
		return true
	}

	// Check home price variance
	var prices []float64
	for _, s := range sources {
		if s.MedianHomePrice > 0 {
			prices = append(prices, s.MedianHomePrice)
		}
	}

	if len(prices) >= 2 {
		variance := calculateVariance(prices)
		if variance > maxSourceVariance {
			a.logger.Warn("high variance in home prices",
				"variance", fmt.Sprintf("%.0f%%", variance*100),
				"prices", prices,
			)
			return false
		}
	}

	// Check rent variance
	var rents []float64
	for _, s := range sources {
		if s.MedianRent > 0 {
			rents = append(rents, s.MedianRent)
		}
	}

	if len(rents) >= 2 {
		variance := calculateVariance(rents)
		if variance > maxSourceVariance {
			a.logger.Warn("high variance in rents",
				"variance", fmt.Sprintf("%.0f%%", variance*100),
				"rents", rents,
			)
			return false
		}
	}

	return true
}

// aggregateData combines data from all sources into final MarketData
func (a *Aggregator) aggregateData(
	city, state string,
	sources []SourceData,
	metro *timeseries.MetroData,
	yoy *timeseries.YearOverYearData,
	rates *fred.EconomicRates,
	snapshot *timeseries.CitySnapshot,
	vacancyRate float64,
	employmentGrowth float64,
	populationGrowth float64,
) *MarketData {
	data := &MarketData{
		City:                 city,
		State:                state,
		Confidence:           1.0, // High confidence for real data
		Sources:              make([]string, 0, len(sources)),
		EmploymentGrowthRate: employmentGrowth,
		PopulationGrowthRate: populationGrowth,
	}

	// Collect source names
	for _, s := range sources {
		data.Sources = append(data.Sources, s.Source)
	}

	// Use metro data if available
	if metro != nil {
		data.MedianHomePrice = int(math.Round(metro.ZHVI))
		data.MedianRent = int(math.Round(metro.ZORI))
		data.DataDate = metro.Date

		// Calculate cap rate: (NOI / Home Price) * 100
		// NOI = Annual Rent - Operating Expenses
		// Typical expense ratio is ~40% (taxes, insurance, maintenance, vacancy, management)
		if metro.ZHVI > 0 && metro.ZORI > 0 {
			annualRent := metro.ZORI * 12
			expenseRatio := 0.40 // 40% of gross rent goes to operating expenses
			noi := annualRent * (1 - expenseRatio)
			data.CapRate = (noi / metro.ZHVI) * 100
		}

	}

	// Add year-over-year data
	if yoy != nil {
		data.YearOverYearPct = yoy.ZHVIYoYPct
		data.RentYearOverYear = yoy.ZORIYoYPct
	}

	// Add mortgage rates from FRED service
	if rates != nil {
		data.MortgageRate30 = rates.MortgageRate30Year
		data.MortgageRate15 = rates.MortgageRate15Year
		if !rates.MortgageRateDate.IsZero() {
			data.Sources = append(data.Sources, rates.Source)
		}
	}

	// Add vacancy rate (from FRED or default)
	data.VacancyRate = vacancyRate

	// Add median days on market from CitySnapshot (Redfin data, ADR-076)
	if snapshot != nil && snapshot.MedianDaysOnMarket > 0 {
		dom := snapshot.MedianDaysOnMarket
		data.MedianDaysOnMarket = &dom
	}

	return data
}

// buildCacheKey creates a cache key for market data
func (a *Aggregator) buildCacheKey(city, state string) string {
	return fmt.Sprintf("%s%s_%s", cacheKeyPrefix, normalizeString(city), normalizeString(state))
}

// calculateVariance calculates the coefficient of variation
func calculateVariance(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}

	min, max := values[0], values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	if min == 0 {
		return 1 // Treat zero as maximum variance
	}

	return (max - min) / min
}

// normalizeString lowercases and removes spaces for cache keys
func normalizeString(s string) string {
	result := ""
	for _, c := range s {
		if c >= 'a' && c <= 'z' {
			result += string(c)
		} else if c >= 'A' && c <= 'Z' {
			result += string(c + 32) // lowercase
		} else if c >= '0' && c <= '9' {
			result += string(c)
		}
	}
	return result
}

// parseJSON is a helper to unmarshal JSON data
func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// getRegionalPopulationGrowthDefault returns estimated population growth by state
// Based on Census Bureau 2020-2024 estimates and regional trends
// TODO: Replace with actual Census flat file integration
func getRegionalPopulationGrowthDefault(state string) float64 {
	// High-growth states (Sun Belt, tech hubs)
	highGrowth := map[string]bool{
		"TX": true, "FL": true, "AZ": true, "NV": true, "UT": true,
		"ID": true, "MT": true, "SC": true, "NC": true, "TN": true,
		"GA": true, "CO": true,
	}

	// Moderate-growth states
	moderateGrowth := map[string]bool{
		"WA": true, "OR": true, "DE": true, "SD": true, "ND": true,
		"NE": true, "MN": true, "VA": true, "MD": true, "NH": true,
	}

	// Low/negative growth states (Rust Belt, Northeast)
	lowGrowth := map[string]bool{
		"NY": true, "IL": true, "CA": true, "PA": true, "OH": true,
		"MI": true, "NJ": true, "CT": true, "WV": true, "LA": true,
		"MS": true, "HI": true,
	}

	if highGrowth[state] {
		return 1.5 // 1.5% annual growth
	}
	if moderateGrowth[state] {
		return 0.8 // 0.8% annual growth
	}
	if lowGrowth[state] {
		return 0.1 // Near-flat or slight decline
	}

	// Default for other states
	return 0.5
}
