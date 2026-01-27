package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/market/estimation"
	"github.com/estara-ai/www/internal/services/market/timeseries"
)

const (
	// Maximum variance between sources before triggering AI estimation
	maxSourceVariance = 0.5 // 50%

	// Cache TTL for aggregated market data
	cacheKeyPrefix = "market_data:"
	cacheTTL       = 24 * time.Hour
)

// MarketData represents aggregated market data from multiple sources
type MarketData struct {
	City             string    `json:"city"`
	State            string    `json:"state"`
	MedianHomePrice  int       `json:"medianHomePrice"`
	MedianRent       int       `json:"medianRent"`
	CapRate          float64   `json:"capRate"`
	MortgageRate30   float64   `json:"mortgageRate30"`
	MortgageRate15   float64   `json:"mortgageRate15"`
	YearOverYearPct  float64   `json:"yearOverYearPct"`
	RentYearOverYear float64   `json:"rentYearOverYearPct"`
	Confidence       float64   `json:"confidence"`
	Sources          []string  `json:"sources"`
	DataDate         time.Time `json:"dataDate"`
	IsAIEstimated    bool      `json:"isAiEstimated"`
}

// SourceData holds data from a single source for cross-validation
type SourceData struct {
	Source          string
	MedianHomePrice float64
	MedianRent      float64
	Date            time.Time
}

// Aggregator aggregates data from multiple market data sources
type Aggregator struct {
	metro     *timeseries.MetroReader
	fred      *timeseries.FREDClient
	estimator *estimation.AIEstimator
	cache     *cache.HybridCache
	logger    *slog.Logger
}

// NewAggregator creates a new market data aggregator
func NewAggregator(
	metro *timeseries.MetroReader,
	fred *timeseries.FREDClient,
	estimator *estimation.AIEstimator,
	hybridCache *cache.HybridCache,
) *Aggregator {
	return &Aggregator{
		metro:     metro,
		fred:      fred,
		estimator: estimator,
		cache:     hybridCache,
		logger:    slog.Default().With("component", "market_aggregator"),
	}
}

// GetMarketData returns aggregated market data for a city/state
func (a *Aggregator) GetMarketData(ctx context.Context, city, state string) (*MarketData, error) {
	cacheKey := a.buildCacheKey(city, state)

	// Check cache first
	if a.cache != nil {
		cached, err := a.cache.Get(ctx, "", cacheKey)
		if err == nil && cached != nil {
			a.logger.Debug("cache hit for market data", "city", city, "state", state)
			// Parse cached data
			var data MarketData
			if err := parseJSON(cached, &data); err == nil {
				return &data, nil
			}
		}
	}

	// Collect data from all sources
	sources := make([]SourceData, 0, 2)
	var mortgageData *timeseries.MortgageRateData
	var metroData *timeseries.MetroData
	var yoyData *timeseries.YearOverYearData

	// Get metro data (Zillow)
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
	}

	// Get mortgage rates (FRED)
	if a.fred != nil && a.fred.IsConfigured() {
		mortgageData, err = a.fred.GetMortgageRates(ctx)
		if err != nil {
			a.logger.Warn("failed to get mortgage rates", "error", err)
		}
	}

	// Cross-validate sources
	if len(sources) > 1 {
		if !a.validateSources(sources) {
			a.logger.Info("source variance too high, triggering AI estimation",
				"city", city,
				"state", state,
			)
			return a.getAIEstimation(ctx, city, state, mortgageData)
		}
	}

	// If no data sources, fall back to AI estimation
	if len(sources) == 0 {
		a.logger.Info("no data sources available, using AI estimation",
			"city", city,
			"state", state,
		)
		return a.getAIEstimation(ctx, city, state, mortgageData)
	}

	// Aggregate data
	data := a.aggregateData(city, state, sources, metroData, yoyData, mortgageData)

	// Cache the result
	if a.cache != nil {
		if err := a.cache.Set(ctx, "", cacheKey, "market_data", data, cacheTTL); err != nil {
			a.logger.Warn("failed to cache market data", "error", err)
		}
	}

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
	mortgage *timeseries.MortgageRateData,
) *MarketData {
	data := &MarketData{
		City:       city,
		State:      state,
		Confidence: 1.0, // High confidence for real data
		Sources:    make([]string, 0, len(sources)),
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

		// Calculate cap rate: (Annual Rent / Home Price) * 100
		if metro.ZHVI > 0 && metro.ZORI > 0 {
			annualRent := metro.ZORI * 12
			data.CapRate = (annualRent / metro.ZHVI) * 100
		}
	}

	// Add year-over-year data
	if yoy != nil {
		data.YearOverYearPct = yoy.ZHVIYoYPct
		data.RentYearOverYear = yoy.ZORIYoYPct
	}

	// Add mortgage rates
	if mortgage != nil {
		data.MortgageRate30 = mortgage.Rate30Year
		data.MortgageRate15 = mortgage.Rate15Year
		if !mortgage.Date.IsZero() && (data.DataDate.IsZero() || mortgage.Date.After(data.DataDate)) {
			// Use more recent date
			data.Sources = append(data.Sources, mortgage.Source)
		}
	}

	return data
}

// getAIEstimation falls back to AI estimation when data sources fail
func (a *Aggregator) getAIEstimation(
	ctx context.Context,
	city, state string,
	mortgage *timeseries.MortgageRateData,
) (*MarketData, error) {
	if a.estimator == nil {
		return nil, fmt.Errorf("AI estimator not available and no data sources")
	}

	estimated, err := a.estimator.EstimateMarketData(ctx, city, state)
	if err != nil {
		return nil, fmt.Errorf("AI estimation failed: %w", err)
	}

	data := &MarketData{
		City:            city,
		State:           state,
		MedianHomePrice: estimated.MedianHomePrice,
		MedianRent:      estimated.MedianRent,
		CapRate:         estimated.CapRate,
		YearOverYearPct: estimated.YearOverYearPct,
		Confidence:      estimated.Confidence,
		Sources:         []string{"AI Estimation"},
		DataDate:        time.Now(),
		IsAIEstimated:   true,
	}

	// Add mortgage rates if available
	if mortgage != nil {
		data.MortgageRate30 = mortgage.Rate30Year
		data.MortgageRate15 = mortgage.Rate15Year
		data.Sources = append(data.Sources, mortgage.Source)
	}

	// Cache AI-estimated data with shorter TTL
	if a.cache != nil {
		cacheKey := a.buildCacheKey(city, state)
		shortTTL := 6 * time.Hour // AI estimates cached for less time
		if err := a.cache.Set(ctx, "", cacheKey, "market_data_ai", data, shortTTL); err != nil {
			a.logger.Warn("failed to cache AI estimation", "error", err)
		}
	}

	return data, nil
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
