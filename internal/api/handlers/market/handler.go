// Package market provides handlers for market data endpoints
package market

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/timeseries"
	"github.com/estara-ai/www/internal/services/market/trends"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles market data HTTP requests
type Handler struct {
	cfg           *config.Config
	aggregator    *aggregator.Aggregator
	trendsService *trends.Service
	fredClient    *timeseries.FREDClient
	logger        *slog.Logger
}

// NewHandler creates a new market data handler
func NewHandler(cfg *config.Config) *Handler {
	h := &Handler{
		cfg:    cfg,
		logger: slog.Default().With("component", "market_handler"),
	}

	// Initialize FRED client if API key is available
	// Note: Redis passed as nil here - main FRED usage via aggregator has caching
	if cfg.Market.FREDAPIKey != "" {
		h.fredClient = timeseries.NewFREDClient(cfg.Market.FREDAPIKey, nil)
		h.logger.Info("FRED client initialized")
	}

	return h
}

// SetAggregator sets the market data aggregator (for dependency injection)
func (h *Handler) SetAggregator(agg *aggregator.Aggregator) {
	h.aggregator = agg
}

// SetTrendsService sets the trends service (for dependency injection)
func (h *Handler) SetTrendsService(svc *trends.Service) {
	h.trendsService = svc
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

	h.logger.Info("fetching mortgage rate from FRED")

	if h.fredClient == nil || !h.fredClient.IsConfigured() {
		h.logger.Warn("FRED client not configured")
		httputil.JSON(w, http.StatusOK, MortgageRateResponse{
			Success: true,
			Rate:    nil,
			Message: "Mortgage rate data not available (FRED not configured)",
		})
		return
	}

	rates, err := h.fredClient.GetMortgageRates(ctx)
	if err != nil {
		h.logger.Error("failed to fetch mortgage rates", "error", err)
		httputil.JSON(w, http.StatusOK, MortgageRateResponse{
			Success: true,
			Rate:    nil,
			Message: "No mortgage rate data available",
		})
		return
	}

	if rates == nil || rates.Rate30Year == 0 {
		h.logger.Warn("no mortgage rate data available from FRED")
		httputil.JSON(w, http.StatusOK, MortgageRateResponse{
			Success: true,
			Rate:    nil,
			Message: "No mortgage rate data available",
		})
		return
	}

	h.logger.Info("mortgage rate fetched", "rate", rates.Rate30Year)
	httputil.JSON(w, http.StatusOK, MortgageRateResponse{
		Success: true,
		Rate:    &rates.Rate30Year,
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

	h.logger.Info("fetching investment rates")

	response := InvestmentRatesResponse{
		Success: true,
		Message: "Investment rates fetched",
	}

	if h.fredClient != nil && h.fredClient.IsConfigured() {
		// Get mortgage rates
		rates, err := h.fredClient.GetMortgageRates(ctx)
		if err != nil {
			h.logger.Warn("failed to fetch mortgage rates", "error", err)
		} else if rates != nil {
			response.MortgageRate30 = rates.Rate30Year
			response.MortgageRate15 = rates.Rate15Year
		}
	}

	httputil.JSON(w, http.StatusOK, response)
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
