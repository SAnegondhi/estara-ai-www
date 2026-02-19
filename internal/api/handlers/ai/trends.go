package ai

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/estara-ai/www/internal/api/handlers/util"
	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/market/trends"
	"github.com/estara-ai/www/pkg/httputil"
)

// TrendsHandler handles market trends endpoints
type TrendsHandler struct {
	store  *db.Store
	trends *trends.Service
	cache  *cache.HybridCache
	logger *slog.Logger
}

// NewTrendsHandler creates a new trends handler
func NewTrendsHandler(
	store *db.Store,
	trendsService *trends.Service,
	cache *cache.HybridCache,
) *TrendsHandler {
	return &TrendsHandler{
		store:  store,
		trends: trendsService,
		cache:  cache,
		logger: slog.Default().With("component", "trends_handler"),
	}
}

// GetMarketTrends handles GET /api/ai/market-trends
func (h *TrendsHandler) GetMarketTrends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Parse query parameters
	location := r.URL.Query().Get("location")
	if location == "" {
		httputil.Error(w, http.StatusBadRequest, "location is required")
		return
	}

	years := 5 // default
	if yearsStr := r.URL.Query().Get("years"); yearsStr != "" {
		if parsed, err := strconv.Atoi(yearsStr); err == nil && parsed > 0 && parsed <= 10 {
			years = parsed
		}
	}

	includeAI := false
	if includeAIStr := r.URL.Query().Get("includeAi"); includeAIStr == "true" {
		includeAI = true
	}

	forceRefresh := false
	if forceRefreshStr := r.URL.Query().Get("forceRefresh"); forceRefreshStr == "true" {
		forceRefresh = true
	}

	cacheKey := r.URL.Query().Get("cacheKey")

	h.logger.Info("fetching market trends",
		"user_id", user.UserID,
		"location", location,
		"years", years,
		"include_ai", includeAI,
	)

	// Get trends
	result, err := h.trends.GetTrends(ctx, trends.TrendsRequest{
		Location:     location,
		Years:        years,
		IncludeAI:    includeAI,
		CacheKey:     cacheKey,
		ForceRefresh: forceRefresh,
	})
	if err != nil {
		h.logger.Error("failed to get market trends", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get market trends")
		return
	}

	// Log feature usage for customer support and chargeback defense
	_ = util.LogFeatureUsage(ctx, h.store, r, user.UserID, "FEATURE_MARKET_TRENDS",
		fmt.Sprintf("Market trends: %s (%d years)", location, years),
		map[string]any{
			"location":     location,
			"years":        years,
			"includeAI":    includeAI,
			"forceRefresh": forceRefresh,
			"cacheKey":     cacheKey,
		},
	)

	httputil.JSON(w, http.StatusOK, trends.TrendsResponse{
		Success: true,
		Data:    result,
	})
}

// GetHistoricalChart handles GET /api/ai/market-trends/chart
func (h *TrendsHandler) GetHistoricalChart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Parse query parameters
	location := r.URL.Query().Get("location")
	if location == "" {
		httputil.Error(w, http.StatusBadRequest, "location is required")
		return
	}

	metric := r.URL.Query().Get("metric")
	if metric == "" {
		metric = "homeValues" // default
	}

	// Validate metric
	validMetrics := map[string]bool{
		"homeValues":    true,
		"rentValues":    true,
		"mortgageRates": true,
	}
	if !validMetrics[metric] {
		httputil.Error(w, http.StatusBadRequest, "invalid metric: must be homeValues, rentValues, or mortgageRates")
		return
	}

	years := 5 // default
	if yearsStr := r.URL.Query().Get("years"); yearsStr != "" {
		if parsed, err := strconv.Atoi(yearsStr); err == nil && parsed > 0 && parsed <= 10 {
			years = parsed
		}
	}

	h.logger.Info("fetching chart data",
		"user_id", user.UserID,
		"location", location,
		"metric", metric,
		"years", years,
	)

	// Get chart data
	chartData, err := h.trends.GetChartData(ctx, trends.ChartRequest{
		Location: location,
		Metric:   metric,
		Years:    years,
	})
	if err != nil {
		h.logger.Error("failed to get chart data", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get chart data")
		return
	}

	httputil.JSON(w, http.StatusOK, trends.ChartResponse{
		Success: true,
		Data:    chartData,
	})
}

// SynthesizeTrendsRequest holds the request body for synthesis endpoint
type SynthesizeTrendsRequest struct {
	Location     string `json:"location" validate:"required"`
	Years        int    `json:"years"`
	CacheKey     string `json:"cacheKey"`
	ForceRefresh bool   `json:"forceRefresh,omitempty"`
}

// SynthesizeTrends handles POST /api/ai/market-trends/synthesize
func (h *TrendsHandler) SynthesizeTrends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Parse request body
	var req SynthesizeTrendsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate required fields
	if req.Location == "" {
		httputil.Error(w, http.StatusBadRequest, "location is required")
		return
	}

	// Set defaults
	if req.Years <= 0 {
		req.Years = 5
	}

	h.logger.Info("synthesizing market trends",
		"user_id", user.UserID,
		"location", req.Location,
		"years", req.Years,
	)

	// First get the historical data
	historical, err := h.trends.GetHistoricalData(ctx, req.Location, req.Years)
	if err != nil {
		h.logger.Error("failed to get historical data", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get historical data")
		return
	}

	// Calculate YoY changes
	yoyChanges := h.trends.CalculateYoYChanges(historical)

	// Generate AI synthesis
	synthesis, err := h.trends.SynthesizeTrends(ctx, historical, yoyChanges, req.Location)
	if err != nil {
		h.logger.Error("failed to synthesize trends", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to synthesize trends")
		return
	}

	httputil.JSON(w, http.StatusOK, trends.SynthesisResponse{
		Success: true,
		Data:    synthesis,
	})
}
