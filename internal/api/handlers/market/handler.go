// Package market provides handlers for market data endpoints
package market

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/bls"
	"github.com/estara-ai/www/internal/services/market/census"
	"github.com/estara-ai/www/internal/services/market/economics"
	"github.com/estara-ai/www/internal/services/market/fred"
	"github.com/estara-ai/www/internal/services/market/trends"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles market data HTTP requests
type Handler struct {
	cfg                  *config.Config
	aggregator           *aggregator.Aggregator
	trendsService        *trends.Service
	fredService          *fred.Service          // Centralized FRED service with smart caching
	censusService        *census.Service        // Census demographics service (ADR-068 Phase 2)
	blsService           *bls.Service           // BLS labor market service (ADR-068 Phase 3)
	economicsAggregator  *economics.Aggregator  // Unified economic data aggregator (ADR-068 Phase 4)
	logger               *slog.Logger
}

// NewHandler creates a new market data handler
func NewHandler(cfg *config.Config) *Handler {
	return &Handler{
		cfg:    cfg,
		logger: slog.Default().With("component", "market_handler"),
	}
}

// SetAggregator sets the market data aggregator (for dependency injection)
func (h *Handler) SetAggregator(agg *aggregator.Aggregator) {
	h.aggregator = agg
}

// SetTrendsService sets the trends service (for dependency injection)
func (h *Handler) SetTrendsService(svc *trends.Service) {
	h.trendsService = svc
}

// SetFREDService sets the FRED service (for dependency injection)
func (h *Handler) SetFREDService(svc *fred.Service) {
	h.fredService = svc
}

// SetCensusService sets the Census service (for dependency injection)
func (h *Handler) SetCensusService(svc *census.Service) {
	h.censusService = svc
}

// SetBLSService sets the BLS service (for dependency injection)
func (h *Handler) SetBLSService(svc *bls.Service) {
	h.blsService = svc
}

// SetEconomicsAggregator sets the economics aggregator (for dependency injection)
func (h *Handler) SetEconomicsAggregator(agg *economics.Aggregator) {
	h.economicsAggregator = agg
}

// MortgageRateResponse represents the mortgage rate response
type MortgageRateResponse struct {
	Success bool    `json:"success"`
	Rate    *float64 `json:"rate"`
	Message string  `json:"message"`
}

// GetMortgageRate returns the current 30-year fixed mortgage rate
// GET /api/market-data/mortgage-rate
func (h *Handler) GetMortgageRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	h.logger.Debug("fetching mortgage rate from FRED service")

	if h.fredService == nil || !h.fredService.IsConfigured() {
		h.logger.Warn("FRED service not configured")
		httputil.JSON(w, http.StatusOK, MortgageRateResponse{
			Success: true,
			Rate:    nil,
			Message: "Mortgage rate data not available (FRED not configured)",
		})
		return
	}

	rate, err := h.fredService.GetMortgageRate(ctx)
	if err != nil {
		h.logger.Error("failed to fetch mortgage rate", "error", err)
		httputil.JSON(w, http.StatusOK, MortgageRateResponse{
			Success: true,
			Rate:    nil,
			Message: "No mortgage rate data available",
		})
		return
	}

	if rate == 0 {
		h.logger.Warn("no mortgage rate data available from FRED")
		httputil.JSON(w, http.StatusOK, MortgageRateResponse{
			Success: true,
			Rate:    nil,
			Message: "No mortgage rate data available",
		})
		return
	}

	h.logger.Debug("mortgage rate fetched", "rate", rate)
	httputil.JSON(w, http.StatusOK, MortgageRateResponse{
		Success: true,
		Rate:    &rate,
		Message: "Mortgage rate fetched successfully",
	})
}

// InvestmentRatesResponse represents the investment rates response
type InvestmentRatesResponse struct {
	Success        bool    `json:"success"`
	MortgageRate30 float64 `json:"mortgageRate30"`
	MortgageRate15 float64 `json:"mortgageRate15"`
	Unemployment   float64 `json:"unemployment,omitempty"`
	Message        string  `json:"message"`
}

// GetInvestmentRates returns current investment-relevant rates
// GET /api/market-data/investment-rates
func (h *Handler) GetInvestmentRates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	h.logger.Debug("fetching investment rates from FRED service")

	response := InvestmentRatesResponse{
		Success: true,
		Message: "Investment rates fetched",
	}

	if h.fredService != nil && h.fredService.IsConfigured() {
		// Get all rates from centralized service (uses smart caching)
		rates, err := h.fredService.GetAllRates(ctx)
		if err != nil {
			h.logger.Warn("failed to fetch rates from FRED service", "error", err)
		} else if rates != nil {
			response.MortgageRate30 = rates.MortgageRate30Year
			response.MortgageRate15 = rates.MortgageRate15Year
			response.Unemployment = rates.UnemploymentRate
		}
	}

	httputil.JSON(w, http.StatusOK, response)
}

// EconomicRatesResponse represents all economic rates with metadata
type EconomicRatesResponse struct {
	Success bool `json:"success"`

	// Mortgage rates
	MortgageRate30 float64 `json:"mortgageRate30"`
	MortgageRate15 float64 `json:"mortgageRate15"`
	MortgageDate   string  `json:"mortgageDate,omitempty"`

	// Labor market
	Unemployment     float64 `json:"unemployment"`
	UnemploymentDate string  `json:"unemploymentDate,omitempty"`

	// Inflation
	Inflation     float64 `json:"inflation"`
	InflationDate string  `json:"inflationDate,omitempty"`

	// Housing
	RentalVacancy     float64 `json:"rentalVacancy"`
	RentalVacancyDate string  `json:"rentalVacancyDate,omitempty"`

	// Metadata
	LastUpdated string `json:"lastUpdated"`
	NextRefresh string `json:"nextRefresh"`
	Source      string `json:"source"`
	Message     string `json:"message"`
}

// GetEconomicRates returns all cached economic rates with metadata
// GET /api/market-data/economic-rates
func (h *Handler) GetEconomicRates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	h.logger.Debug("fetching all economic rates from FRED service")

	if h.fredService == nil || !h.fredService.IsConfigured() {
		httputil.JSON(w, http.StatusOK, EconomicRatesResponse{
			Success: false,
			Message: "FRED service not configured",
		})
		return
	}

	rates, err := h.fredService.GetAllRates(ctx)
	if err != nil {
		h.logger.Error("failed to fetch economic rates", "error", err)
		httputil.JSON(w, http.StatusInternalServerError, EconomicRatesResponse{
			Success: false,
			Message: "Failed to fetch economic rates",
		})
		return
	}

	httputil.JSON(w, http.StatusOK, EconomicRatesResponse{
		Success:           true,
		MortgageRate30:    rates.MortgageRate30Year,
		MortgageRate15:    rates.MortgageRate15Year,
		MortgageDate:      rates.MortgageRateDate.Format(time.RFC3339),
		Unemployment:      rates.UnemploymentRate,
		UnemploymentDate:  rates.UnemploymentRateDate.Format(time.RFC3339),
		Inflation:         rates.InflationRate,
		InflationDate:     rates.InflationRateDate.Format(time.RFC3339),
		RentalVacancy:     rates.RentalVacancyRate,
		RentalVacancyDate: rates.RentalVacancyRateDate.Format(time.RFC3339),
		LastUpdated:       rates.LastUpdated.Format(time.RFC3339),
		NextRefresh:       rates.NextRefresh.Format(time.RFC3339),
		Source:            rates.Source,
		Message:           "Economic rates fetched successfully",
	})
}

// GetMarketData returns aggregated market data for a location
// GET /api/market-data?city=Austin&state=TX
func (h *Handler) GetMarketData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	city := r.URL.Query().Get("city")
	state := r.URL.Query().Get("state")

	if city == "" {
		httputil.BadRequest(w, "city parameter is required")
		return
	}

	h.logger.Info("fetching market data", "city", city, "state", state)

	if h.aggregator == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "Market data service not available")
		return
	}

	data, err := h.aggregator.GetMarketData(ctx, city, state)
	if err != nil {
		h.logger.Error("failed to fetch market data", "error", err, "city", city, "state", state)
		httputil.Error(w, http.StatusInternalServerError, "Failed to fetch market data")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// TrendsSearchRequest represents a metro search request
type TrendsSearchRequest struct {
	Query string `json:"q"`
	Limit int    `json:"limit"`
}

// TrendsSearchResult represents a metro search result
type TrendsSearchResult struct {
	Name          string            `json:"name"`
	CanonicalName string            `json:"canonicalName"`
	StateName     *string           `json:"stateName"`
	DataAvailable map[string]int    `json:"dataAvailable"`
}

// SearchMetros handles metro search for market trends
// GET /api/market-trends/search?q=austin&limit=10
func (h *Handler) SearchMetros(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" || len(query) < 2 {
		httputil.BadRequest(w, "Search query must be at least 2 characters")
		return
	}

	// TODO: Implement metro search against market data database
	// For now, return empty results
	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"metros": []TrendsSearchResult{},
		"total":  0,
	})
}

// GetHistoricalTrends returns historical trend data
// GET /api/market-trends/historical?location=Austin,TX&years=5
func (h *Handler) GetHistoricalTrends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	location := r.URL.Query().Get("location")
	if location == "" {
		httputil.BadRequest(w, "location parameter is required")
		return
	}

	years := 5 // default
	if yearsParam := r.URL.Query().Get("years"); yearsParam != "" {
		var y int
		if _, err := fmt.Sscanf(yearsParam, "%d", &y); err == nil && y > 0 && y <= 20 {
			years = y
		}
	}

	if h.trendsService == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "Trends service not available")
		return
	}

	data, err := h.trendsService.GetHistoricalData(ctx, location, years)
	if err != nil {
		h.logger.Error("failed to get historical data", "error", err, "location", location)
		httputil.Error(w, http.StatusInternalServerError, "Failed to fetch historical data")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// SynthesizeTrendsRequest represents a synthesis request
type SynthesizeTrendsRequest struct {
	Metro             string                 `json:"metro"`
	CalculatedMetrics map[string]interface{} `json:"calculatedMetrics"`
	Comparisons       map[string]interface{} `json:"comparisons,omitempty"`
	CacheKey          string                 `json:"cacheKey,omitempty"`
	ForceRefresh      bool                   `json:"forceRefresh"`
}

// SynthesizeTrends generates AI synthesis for market trends
// POST /api/market-trends/synthesize
func (h *Handler) SynthesizeTrends(w http.ResponseWriter, r *http.Request) {
	// ctx := r.Context() // Will be used when full AI synthesis is implemented

	var req SynthesizeTrendsRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "Invalid request body")
		return
	}

	if req.Metro == "" || req.CalculatedMetrics == nil {
		httputil.BadRequest(w, "metro and calculatedMetrics are required")
		return
	}

	// TODO: Implement full AI synthesis with Claude
	// For now, return a placeholder response
	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"cached":  false,
		"data": map[string]interface{}{
			"summary":        "Market trends synthesis for " + req.Metro,
			"insights":       []string{},
			"recommendation": "Analysis pending full implementation",
			"riskLevel":      "medium",
			"confidence":     0.5,
			"generatedAt":    time.Now().Format(time.RFC3339),
		},
	})
}

// =============================================================================
// Census Demographics Endpoints (ADR-068 Phase 2)
// =============================================================================

// DemographicsResponse represents the demographics response
type DemographicsResponse struct {
	Success bool `json:"success"`

	// Demographics
	Population            int64   `json:"population"`
	MedianHouseholdIncome float64 `json:"medianHouseholdIncome"`
	PerCapitaIncome       float64 `json:"perCapitaIncome"`
	PovertyRate           float64 `json:"povertyRate"`
	MedianAge             float64 `json:"medianAge"`
	HouseholdCount        int64   `json:"householdCount"`

	// Geographic context
	City   string `json:"city"`
	County string `json:"county,omitempty"`
	State  string `json:"state"`
	Level  string `json:"level"` // "place", "county", or "state"

	// Metadata
	DataYear    int    `json:"dataYear"`
	LastUpdated string `json:"lastUpdated"`
	NextRefresh string `json:"nextRefresh"`
	Source      string `json:"source"`
	Message     string `json:"message"`
}

// GetDemographics returns Census demographics for a city/state
// GET /api/market-data/demographics?city=Austin&state=TX
func (h *Handler) GetDemographics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	city := r.URL.Query().Get("city")
	state := r.URL.Query().Get("state")

	if state == "" {
		httputil.BadRequest(w, "state parameter is required")
		return
	}

	h.logger.Info("fetching demographics", "city", city, "state", state)

	if h.censusService == nil || !h.censusService.IsConfigured() {
		httputil.JSON(w, http.StatusOK, DemographicsResponse{
			Success: false,
			Message: "Census service not configured",
		})
		return
	}

	data, err := h.censusService.GetDemographics(ctx, city, state)
	if err != nil {
		h.logger.Error("failed to fetch demographics", "error", err, "city", city, "state", state)
		httputil.JSON(w, http.StatusInternalServerError, DemographicsResponse{
			Success: false,
			Message: "Failed to fetch demographics: " + err.Error(),
		})
		return
	}

	httputil.JSON(w, http.StatusOK, DemographicsResponse{
		Success:               true,
		Population:            data.Population,
		MedianHouseholdIncome: data.MedianHouseholdIncome,
		PerCapitaIncome:       data.PerCapitaIncome,
		PovertyRate:           data.PovertyRate,
		MedianAge:             data.MedianAge,
		HouseholdCount:        data.HouseholdCount,
		City:                  data.City,
		County:                data.County,
		State:                 data.State,
		Level:                 data.Level,
		DataYear:              data.DataYear,
		LastUpdated:           data.LastUpdated.Format(time.RFC3339),
		NextRefresh:           data.NextRefresh.Format(time.RFC3339),
		Source:                data.Source,
		Message:               "Demographics fetched successfully",
	})
}

// =============================================================================
// BLS Labor Market Endpoints (ADR-068 Phase 3)
// =============================================================================

// LaborDataResponse represents the labor market response
type LaborDataResponse struct {
	Success bool `json:"success"`

	// CPI Inflation
	CPIAllItems float64 `json:"cpiAllItems"`
	CPIShelter  float64 `json:"cpiShelter"`
	CPIRent     float64 `json:"cpiRent"`

	// National Employment
	UnemploymentRate        float64 `json:"unemploymentRate"`
	LaborForceParticipation float64 `json:"laborForceParticipation"`
	AverageHourlyEarnings   float64 `json:"averageHourlyEarnings"`
	ConstructionEmployment  float64 `json:"constructionEmployment"`
	JobOpenings             float64 `json:"jobOpenings"`

	// State-level (when requested)
	StateUnemploymentRate float64 `json:"stateUnemploymentRate,omitempty"`
	StateEmployment       float64 `json:"stateEmployment,omitempty"`
	State                 string  `json:"state,omitempty"`

	// Metadata
	AsOfDate    string `json:"asOfDate"`
	AsOfPeriod  string `json:"asOfPeriod"`
	LastUpdated string `json:"lastUpdated"`
	NextRefresh string `json:"nextRefresh"`
	Source      string `json:"source"`
	Message     string `json:"message"`
}

// GetLaborData returns BLS national labor market data
// GET /api/market-data/labor
func (h *Handler) GetLaborData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	h.logger.Debug("fetching national labor data from BLS")

	if h.blsService == nil || !h.blsService.IsConfigured() {
		httputil.JSON(w, http.StatusOK, LaborDataResponse{
			Success: false,
			Message: "BLS service not configured",
		})
		return
	}

	data, err := h.blsService.GetNationalData(ctx)
	if err != nil {
		h.logger.Error("failed to fetch labor data", "error", err)
		httputil.JSON(w, http.StatusInternalServerError, LaborDataResponse{
			Success: false,
			Message: "Failed to fetch labor data: " + err.Error(),
		})
		return
	}

	httputil.JSON(w, http.StatusOK, LaborDataResponse{
		Success:                 true,
		CPIAllItems:             data.CPIAllItems,
		CPIShelter:              data.CPIShelter,
		CPIRent:                 data.CPIRent,
		UnemploymentRate:        data.UnemploymentRate,
		LaborForceParticipation: data.LaborForceParticipation,
		AverageHourlyEarnings:   data.AverageHourlyEarnings,
		ConstructionEmployment:  data.ConstructionEmployment,
		JobOpenings:             data.JobOpenings,
		AsOfDate:                data.AsOfDate,
		AsOfPeriod:              data.AsOfPeriod,
		LastUpdated:             data.LastUpdated.Format(time.RFC3339),
		NextRefresh:             data.NextRefresh.Format(time.RFC3339),
		Source:                  data.Source,
		Message:                 "Labor data fetched successfully",
	})
}

// GetStateLaborData returns BLS state-level labor market data
// GET /api/market-data/labor/state/{state}
func (h *Handler) GetStateLaborData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract state from URL path (e.g., /api/market-data/labor/state/TX)
	state := r.PathValue("state")
	if state == "" {
		httputil.BadRequest(w, "state parameter is required")
		return
	}

	h.logger.Info("fetching state labor data", "state", state)

	if h.blsService == nil || !h.blsService.IsConfigured() {
		httputil.JSON(w, http.StatusOK, LaborDataResponse{
			Success: false,
			Message: "BLS service not configured",
		})
		return
	}

	// Get combined national + state data
	data, err := h.blsService.GetCombinedData(ctx, state)
	if err != nil {
		h.logger.Error("failed to fetch state labor data", "error", err, "state", state)
		httputil.JSON(w, http.StatusInternalServerError, LaborDataResponse{
			Success: false,
			Message: "Failed to fetch state labor data: " + err.Error(),
		})
		return
	}

	httputil.JSON(w, http.StatusOK, LaborDataResponse{
		Success:                 true,
		CPIAllItems:             data.CPIAllItems,
		CPIShelter:              data.CPIShelter,
		CPIRent:                 data.CPIRent,
		UnemploymentRate:        data.UnemploymentRate,
		LaborForceParticipation: data.LaborForceParticipation,
		AverageHourlyEarnings:   data.AverageHourlyEarnings,
		ConstructionEmployment:  data.ConstructionEmployment,
		JobOpenings:             data.JobOpenings,
		StateUnemploymentRate:   data.StateUnemploymentRate,
		StateEmployment:         data.StateEmployment,
		State:                   data.State,
		AsOfDate:                data.AsOfDate,
		AsOfPeriod:              data.AsOfPeriod,
		LastUpdated:             data.LastUpdated.Format(time.RFC3339),
		NextRefresh:             data.NextRefresh.Format(time.RFC3339),
		Source:                  data.Source,
		Message:                 "State labor data fetched successfully",
	})
}

// =============================================================================
// Unified Economics Endpoint (ADR-068 Phase 4)
// =============================================================================

// MarketEconomicsResponse represents the unified economics response
type MarketEconomicsResponse struct {
	Success bool `json:"success"`

	// Location context
	City  string `json:"city"`
	State string `json:"state"`

	// From FRED (national rates)
	MortgageRate30Year   float64 `json:"mortgageRate30Year"`
	MortgageRate15Year   float64 `json:"mortgageRate15Year"`
	NationalUnemployment float64 `json:"nationalUnemployment"`
	InflationRate        float64 `json:"inflationRate"`
	RentalVacancyRate    float64 `json:"rentalVacancyRate"`

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

	// Calculated Metrics (when medianHomePrice provided)
	PriceToIncomeRatio float64 `json:"priceToIncomeRatio,omitempty"`
	AffordabilityIndex float64 `json:"affordabilityIndex,omitempty"`

	// Metadata
	LastUpdated string            `json:"lastUpdated"`
	Sources     map[string]string `json:"sources"` // {\"fred\": \"2026-02-07\", \"census\": \"2022\", \"bls\": \"2026-01\"}
	Errors      []string          `json:"errors,omitempty"`
	Message     string            `json:"message"`
}

// GetUnifiedEconomics returns unified economic data from FRED, Census, and BLS
// GET /api/market-data/economics?city=Austin&state=TX&medianHomePrice=450000
func (h *Handler) GetUnifiedEconomics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	city := r.URL.Query().Get("city")
	state := r.URL.Query().Get("state")

	if state == "" {
		httputil.BadRequest(w, "state parameter is required")
		return
	}

	h.logger.Info("fetching unified economics", "city", city, "state", state)

	if h.economicsAggregator == nil || !h.economicsAggregator.IsConfigured() {
		httputil.JSON(w, http.StatusOK, MarketEconomicsResponse{
			Success: false,
			Message: "Economics aggregator not configured (no data sources available)",
		})
		return
	}

	// Check for optional median home price for calculated metrics
	var medianHomePrice float64
	if priceStr := r.URL.Query().Get("medianHomePrice"); priceStr != "" {
		if price, err := strconv.ParseFloat(priceStr, 64); err == nil && price > 0 {
			medianHomePrice = price
		}
	}

	var data *economics.MarketEconomics
	var err error

	if medianHomePrice > 0 {
		data, err = h.economicsAggregator.GetMarketEconomicsWithPrice(ctx, city, state, medianHomePrice)
	} else {
		data, err = h.economicsAggregator.GetMarketEconomics(ctx, city, state)
	}

	if err != nil {
		h.logger.Error("failed to fetch unified economics", "error", err, "city", city, "state", state)
		httputil.JSON(w, http.StatusInternalServerError, MarketEconomicsResponse{
			Success: false,
			Message: "Failed to fetch unified economics: " + err.Error(),
		})
		return
	}

	httputil.JSON(w, http.StatusOK, MarketEconomicsResponse{
		Success: true,

		// Location
		City:  data.City,
		State: data.State,

		// FRED
		MortgageRate30Year:   data.MortgageRate30Year,
		MortgageRate15Year:   data.MortgageRate15Year,
		NationalUnemployment: data.NationalUnemployment,
		InflationRate:        data.InflationRate,
		RentalVacancyRate:    data.RentalVacancyRate,

		// Census
		Population:            data.Population,
		MedianHouseholdIncome: data.MedianHouseholdIncome,
		PerCapitaIncome:       data.PerCapitaIncome,
		PovertyRate:           data.PovertyRate,
		MedianAge:             data.MedianAge,
		HouseholdCount:        data.HouseholdCount,
		DemographicLevel:      data.DemographicLevel,

		// BLS
		StateUnemploymentRate:   data.StateUnemploymentRate,
		StateEmployment:         data.StateEmployment,
		CPIShelter:              data.CPIShelter,
		CPIRent:                 data.CPIRent,
		AverageHourlyEarnings:   data.AverageHourlyEarnings,
		ConstructionEmployment:  data.ConstructionEmployment,
		JobOpenings:             data.JobOpenings,
		LaborForceParticipation: data.LaborForceParticipation,

		// Calculated
		PriceToIncomeRatio: data.PriceToIncomeRatio,
		AffordabilityIndex: data.AffordabilityIndex,

		// Metadata
		LastUpdated: data.LastUpdated.Format(time.RFC3339),
		Sources:     data.Sources,
		Errors:      data.Errors,
		Message:     "Unified economics data fetched successfully",
	})
}
