package discover

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/investment/expenses"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/internal/services/property/providers"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles discovery-related HTTP requests
type Handler struct {
	db           *postgres.DB
	redis        *redisClient.Client
	cfg          *config.Config
	validate     *validator.Validate
	orchestrator *finder.Orchestrator
	aggregator   *aggregator.Aggregator
	logger       *slog.Logger
}

// NewHandler creates a new discovery handler
func NewHandler(
	db *postgres.DB,
	redis *redisClient.Client,
	cfg *config.Config,
	orchestrator *finder.Orchestrator,
	aggregator *aggregator.Aggregator,
) *Handler {
	// Set the global enrichment job manager for SSE handlers
	if orchestrator != nil {
		SetEnrichmentJobManager(orchestrator.GetEnrichmentJobManager())
	}

	return &Handler{
		db:           db,
		redis:        redis,
		cfg:          cfg,
		validate:     validator.New(),
		orchestrator: orchestrator,
		aggregator:   aggregator,
		logger:       slog.Default().With("component", "discover_handler"),
	}
}

// PropertySearchResponse wraps the search response
type PropertySearchResponse struct {
	Success    bool                     `json:"success"`
	Properties []providers.Property     `json:"properties"`
	Total      int                      `json:"total"`
	HasMore    bool                     `json:"hasMore"`
	NextOffset int                      `json:"nextOffset,omitempty"`
	Metrics    finder.SearchMetrics     `json:"metrics"`
}

// MarketDefaultsResponse wraps market defaults data
// Matches client's expected format from MarketDefaultsResponse in client/src/lib/api/client.ts
type MarketDefaultsResponse struct {
	Success           bool                    `json:"success"`
	Location          MarketDefaultsLocation  `json:"location"`
	MarketData        MarketDefaultsData      `json:"marketData"`
	SuggestedDefaults *SuggestedDefaults      `json:"suggestedDefaults"`
	ExpenseDefaults   *ExpenseDefaults        `json:"expenseDefaults,omitempty"`
	DataQuality       float64                 `json:"dataQuality"`
}

// ExpenseDefaults contains state-specific operating expense rates
type ExpenseDefaults struct {
	PropertyTaxRate  float64  `json:"propertyTaxRate"`  // % of home value
	InsuranceRate    float64  `json:"insuranceRate"`    // % of home value
	MaintenanceRate  float64  `json:"maintenanceRate"`  // % of home value
	VacancyRate      float64  `json:"vacancyRate"`      // % of rent
	PropertyMgmtRate float64  `json:"propertyMgmtRate"` // % of rent
	RiskFactors      []string `json:"riskFactors,omitempty"`
	Notes            string   `json:"notes,omitempty"`
}

// MarketDefaultsLocation contains city/state info
type MarketDefaultsLocation struct {
	City  string `json:"city"`
	State string `json:"state"`
}

// MarketDefaultsData contains market metrics
type MarketDefaultsData struct {
	MedianHomePrice    *int     `json:"medianHomePrice"`
	MedianRent         *int     `json:"medianRent"`
	CapRate            *float64 `json:"capRate"`
	GrossRentalYield   *float64 `json:"grossRentalYield"`
	PriceYoyChange     *float64 `json:"priceYoyChange"`
	MedianDaysOnMarket *int     `json:"medianDaysOnMarket"`
	MortgageRate30     *float64 `json:"mortgageRate30,omitempty"` // Current 30-year fixed rate from FRED
	MortgageRate15     *float64 `json:"mortgageRate15,omitempty"` // Current 15-year fixed rate from FRED
}

// SuggestedDefaults contains price range suggestions
type SuggestedDefaults struct {
	MinPrice      int      `json:"minPrice"`
	MaxPrice      int      `json:"maxPrice"`
	MinCapRate    *float64 `json:"minCapRate"`
	MinGrossYield *float64 `json:"minGrossYield"`
}

// BatchEvaluateRequest represents a batch evaluation request
type BatchEvaluateRequest struct {
	Properties []BatchPropertyInput `json:"properties" validate:"required,min=1,max=50,dive"`
}

// BatchPropertyInput represents property data from client for evaluation
type BatchPropertyInput struct {
	ID            string  `json:"id" validate:"required"`
	Address       string  `json:"address" validate:"required"`
	City          string  `json:"city" validate:"required"`
	State         string  `json:"state" validate:"required"`
	ZipCode       string  `json:"zipCode,omitempty"`
	Price         int     `json:"price" validate:"required,gt=0"`
	Beds          int     `json:"beds,omitempty"`
	Baths         float64 `json:"baths,omitempty"`
	Sqft          int     `json:"sqft,omitempty"`
	EstimatedRent int     `json:"estimatedRent,omitempty"`
	PropertyType  string  `json:"propertyType,omitempty"`
}

// ScenarioMetrics represents metrics for a scenario
type ScenarioMetrics struct {
	MonthlyPayment  float64 `json:"monthlyPayment"`
	MonthlyCashFlow float64 `json:"monthlyCashFlow"`
	AnnualCashFlow  float64 `json:"annualCashFlow"`
	CashOnCash      float64 `json:"cashOnCash"`
	CapRate         float64 `json:"capRate"`
	TotalReturn5Yr  float64 `json:"totalReturn5Yr"`
	IRR5Yr          float64 `json:"irr5Yr"`
	BreakEvenMonths int     `json:"breakEvenMonths"`
}

// ScenarioAssumptions represents assumptions for a scenario
type ScenarioAssumptions struct {
	AppreciationRate float64 `json:"appreciationRate"`
	VacancyRate      float64 `json:"vacancyRate"`
	RentGrowthRate   float64 `json:"rentGrowthRate"`
}

// ScenarioResult wraps metrics and assumptions for a scenario
type ScenarioResult struct {
	Name        string              `json:"name"`
	Assumptions ScenarioAssumptions `json:"assumptions"`
	Metrics     ScenarioMetrics     `json:"metrics"`
}

// BatchPropertyInfo represents property info in evaluation result
type BatchPropertyInfo struct {
	ID      string  `json:"id"`
	Address string  `json:"address"`
	City    string  `json:"city"`
	State   string  `json:"state"`
	ZipCode string  `json:"zipCode,omitempty"`
	Price   int     `json:"price"`
	Beds    int     `json:"beds,omitempty"`
	Baths   float64 `json:"baths,omitempty"`
	Sqft    int     `json:"sqft,omitempty"`
}

// BatchEvaluationResult represents evaluation result for a property (matches client interface)
type BatchEvaluationResult struct {
	EvaluationID string             `json:"evaluationId"`
	PropertyID   string             `json:"propertyId"`
	Property     BatchPropertyInfo  `json:"property"`
	Scenarios    *BatchScenarios    `json:"scenarios,omitempty"`
	Error        string             `json:"error,omitempty"`
	Status       string             `json:"status"` // COMPLETED or ERROR
}

// BatchScenarios contains all scenario results
type BatchScenarios struct {
	Conservative ScenarioResult `json:"conservative"`
	Base         ScenarioResult `json:"base"`
	Optimistic   ScenarioResult `json:"optimistic"`
}

// BatchEvaluateResponse wraps batch evaluation results
type BatchEvaluateResponse struct {
	Success         bool                    `json:"success"`
	ReportID        string                  `json:"reportId"`
	TotalProperties int                     `json:"totalProperties"`
	CompletedCount  int                     `json:"completedCount"`
	FailedCount     int                     `json:"failedCount"`
	Evaluations     []BatchEvaluationResult `json:"evaluations"`
	MarketData      *aggregator.MarketData  `json:"marketData,omitempty"`
}

// SearchCriteria matches www_v1 /api/v2/discover/search request body
type SearchCriteria struct {
	Location      string   `json:"location" validate:"required"`
	RadiusMiles   int      `json:"radiusMiles,omitempty"`
	MinPrice      int      `json:"minPrice,omitempty"`
	MaxPrice      int      `json:"maxPrice,omitempty"`
	PropertyTypes []string `json:"propertyTypes,omitempty"`
	MinBeds       int      `json:"minBeds,omitempty"`
	MinBaths      int      `json:"minBaths,omitempty"`
	MinSqft       int      `json:"minSqft,omitempty"`
	MinCapRate    float64  `json:"minCapRate,omitempty"`
	MaxCapRate    float64  `json:"maxCapRate,omitempty"`
	MinGrossYield float64  `json:"minGrossYield,omitempty"`
	ForceRefresh  bool     `json:"forceRefresh,omitempty"`
}

// CapRateRange represents min/max cap rate
type CapRateRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// V2PropertyResult matches www_v1 response format for client compatibility
type V2PropertyResult struct {
	ID               string        `json:"id"`
	Address          string        `json:"address"`
	City             string        `json:"city"`
	State            string        `json:"state"`
	ZipCode          string        `json:"zipCode"`
	Price            int           `json:"price"`
	EstimatedRent    *int          `json:"estimatedRent,omitempty"`
	CapRateRange     *CapRateRange `json:"capRateRange,omitempty"`
	Beds             int           `json:"beds"`
	Baths            float64       `json:"baths"`
	Sqft             int           `json:"sqft"`
	YearBuilt        *int          `json:"yearBuilt,omitempty"`
	PropertyType     string        `json:"propertyType"`
	ListingDate      *string       `json:"listingDate,omitempty"`
	DaysOnMarket     *int          `json:"daysOnMarket,omitempty"`
	ImageUrl         *string       `json:"imageUrl,omitempty"`
	ListingSearchUrl string        `json:"listingSearchUrl"`
	GoogleSearchUrl  string        `json:"googleSearchUrl"`
	Latitude         *float64      `json:"latitude,omitempty"`
	Longitude        *float64      `json:"longitude,omitempty"`

	// Investment Metrics (enriched by InvestmentMetricsEnricher)
	CapRate           *float64 `json:"capRate,omitempty"`         // Cap rate with expenses (NOI/Price)
	GrossYield        *float64 `json:"grossYield,omitempty"`      // Gross rental yield
	CashOnCash        *float64 `json:"cashOnCash,omitempty"`      // Cash-on-cash return (25% down)
	MonthlyCashFlow   *int     `json:"monthlyCashFlow,omitempty"` // Monthly cash flow after mortgage
	NOI               *int     `json:"noi,omitempty"`             // Annual NOI
	PricePerSqft      *int     `json:"pricePerSqft,omitempty"`
	InvestmentScore   *int     `json:"investmentScore,omitempty"` // Overall score 0-100

	// Age-based Investment Intelligence
	PropertyAge        *int      `json:"propertyAge,omitempty"`
	AgeCategory        string    `json:"ageCategory,omitempty"`        // new_construction, modern, established, mature, vintage
	MaintenanceRisk    string    `json:"maintenanceRisk,omitempty"`    // low, moderate, high, very_high
	MaintenanceFactors []string  `json:"maintenanceFactors,omitempty"`
}

// V2SearchResponse matches www_v1 /api/v2/discover/search response
type V2SearchResponse struct {
	Success            bool               `json:"success"`
	Properties         []V2PropertyResult `json:"properties"`
	TotalCount         int                `json:"totalCount"`
	DiscoverySessionId string             `json:"discoverySessionId,omitempty"`
	EnrichmentJobID    string             `json:"enrichmentJobId,omitempty"`
	EnrichmentStatus   string             `json:"enrichmentStatus,omitempty"` // PENDING, PROCESSING, COMPLETED, FAILED
}

// Search handles POST /api/v2/discover/search - matches www_v1 API contract
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user ID from context for discovery session
	var userID string
	if user := middleware.GetUserFromContext(ctx); user != nil {
		userID = user.UserID
	}

	// Parse request body
	var criteria SearchCriteria
	if err := json.NewDecoder(r.Body).Decode(&criteria); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate request
	if err := h.validate.Struct(criteria); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Parse location into city and state
	city, state := parseLocation(criteria.Location)
	if city == "" || state == "" {
		httputil.BadRequest(w, "invalid location format, expected 'City, ST' or 'City, State'")
		return
	}

	// Check if orchestrator is configured
	if h.orchestrator == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "property search service not available")
		return
	}

	// Build search params
	params := providers.SearchParams{
		City:     city,
		State:    state,
		MinPrice: criteria.MinPrice,
		MaxPrice: criteria.MaxPrice,
		MinBeds:  criteria.MinBeds,
	}

	// Handle property types (use first one if multiple provided)
	if len(criteria.PropertyTypes) > 0 {
		params.PropertyType = providers.PropertyType(criteria.PropertyTypes[0])
	}

	h.logger.Info("searching properties (v2 search)",
		"city", city,
		"state", state,
		"minPrice", params.MinPrice,
		"maxPrice", params.MaxPrice,
		"minBeds", params.MinBeds,
	)

	// Execute search with async enrichment (properties return immediately, yearBuilt/sqft enrichment via SSE)
	result, err := h.orchestrator.SearchAsync(ctx, params)
	if err != nil {
		h.logger.Error("property search failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "property search failed")
		return
	}

	// Transform to V2 response format (properties have investment metrics, yearBuilt may come via SSE)
	// Filter out invalid properties (zero price, zero cap rate indicates enrichment failure)
	properties := make([]V2PropertyResult, 0, len(result.Properties))
	for _, p := range result.Properties {
		// Skip properties with zero or negative price - these are invalid listings
		if p.Price <= 0 {
			h.logger.Debug("skipping property with invalid price",
				"id", p.ID,
				"price", p.Price,
				"address", p.Address,
			)
			continue
		}

		// Skip properties where enrichment failed (zero cap rate when price is valid)
		// This indicates the rent estimation also failed
		if p.EstimatedCapRate <= 0 && p.EstimatedRent <= 0 {
			h.logger.Debug("skipping property with failed enrichment",
				"id", p.ID,
				"price", p.Price,
				"capRate", p.EstimatedCapRate,
				"rent", p.EstimatedRent,
			)
			continue
		}

		prop := V2PropertyResult{
			ID:               p.ID,
			Address:          p.Address,
			City:             p.City,
			State:            p.State,
			ZipCode:          p.ZipCode,
			Price:            p.Price,
			Beds:             p.Beds,
			Baths:            p.Baths,
			Sqft:             p.Sqft,
			PropertyType:     string(p.PropertyType),
			ListingSearchUrl: buildListingSearchUrl(p.Address, p.City, p.State, p.ZipCode),
			GoogleSearchUrl:  buildGoogleSearchUrl(p.Address, p.City, p.State, p.ZipCode),
			AgeCategory:      p.AgeCategory,
			MaintenanceRisk:  p.MaintenanceRisk,
			MaintenanceFactors: p.MaintenanceFactors,
		}

		// Enriched investment metrics (from InvestmentMetricsEnricher)
		if p.EstimatedRent > 0 {
			rent := p.EstimatedRent
			prop.EstimatedRent = &rent
		}
		if p.EstimatedCapRate > 0 {
			capRate := p.EstimatedCapRate
			prop.CapRate = &capRate
			// Also provide cap rate range for backward compatibility
			capRange := &CapRateRange{
				Min: capRate - 0.5,
				Max: capRate + 0.5,
			}
			prop.CapRateRange = capRange
		}
		if p.GrossYield > 0 {
			grossYield := p.GrossYield
			prop.GrossYield = &grossYield
		}
		if p.CashOnCash != 0 { // Can be negative
			coc := p.CashOnCash
			prop.CashOnCash = &coc
		}
		if p.EstimatedCashFlow != 0 { // Can be negative
			cf := p.EstimatedCashFlow
			prop.MonthlyCashFlow = &cf
		}
		if p.NOI != 0 {
			noi := p.NOI
			prop.NOI = &noi
		}
		if p.PricePerSqft > 0 {
			pps := p.PricePerSqft
			prop.PricePerSqft = &pps
		}
		if p.InvestmentScore > 0 {
			score := p.InvestmentScore
			prop.InvestmentScore = &score
		}
		if p.PropertyAge > 0 {
			age := p.PropertyAge
			prop.PropertyAge = &age
		}

		// Other optional fields
		if p.YearBuilt > 0 {
			year := p.YearBuilt
			prop.YearBuilt = &year
		}
		if p.DaysOnMarket > 0 {
			dom := p.DaysOnMarket
			prop.DaysOnMarket = &dom
		}
		if len(p.Images) > 0 {
			img := p.Images[0]
			prop.ImageUrl = &img
		}
		if p.Latitude != 0 {
			lat := p.Latitude
			prop.Latitude = &lat
		}
		if p.Longitude != 0 {
			lng := p.Longitude
			prop.Longitude = &lng
		}

		// Filter by investment criteria using enriched values
		if criteria.MinCapRate > 0 || criteria.MaxCapRate > 0 || criteria.MinGrossYield > 0 {
			capRate := p.EstimatedCapRate
			grossYield := p.GrossYield

			if criteria.MinCapRate > 0 && capRate < criteria.MinCapRate {
				continue
			}
			if criteria.MaxCapRate > 0 && capRate > criteria.MaxCapRate {
				continue
			}
			if criteria.MinGrossYield > 0 && grossYield < criteria.MinGrossYield {
				continue
			}
		}

		properties = append(properties, prop)
	}

	// Create discovery session if user is authenticated
	var discoverySessionId string
	if userID != "" && len(properties) > 0 {
		// Extract property IDs for the session
		propertyIds := make([]string, len(properties))
		for i, p := range properties {
			propertyIds[i] = p.ID
		}

		// Serialize search criteria
		criteriaJSON, err := json.Marshal(criteria)
		if err != nil {
			h.logger.Warn("failed to serialize search criteria", "error", err)
			criteriaJSON = []byte("{}")
		}

		// Create the discovery session with properties
		discoverySessionId = h.CreateDiscoverySessionForSearch(
			ctx,
			userID,
			criteriaJSON,
			criteria.Location,
			len(properties),
			propertyIds,
			properties,
		)
	}

	response := V2SearchResponse{
		Success:            true,
		Properties:         properties,
		TotalCount:         len(properties),
		DiscoverySessionId: discoverySessionId,
		EnrichmentJobID:    result.EnrichmentJobID,
		EnrichmentStatus:   result.EnrichmentStatus,
	}

	httputil.JSON(w, http.StatusOK, response)
}

// SearchProperties searches for properties based on criteria (GET endpoint)
func (h *Handler) SearchProperties(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse location parameter
	location := httputil.GetQueryParam(r, "location", "")
	if location == "" {
		httputil.BadRequest(w, "location is required")
		return
	}

	// Parse location into city and state
	city, state := parseLocation(location)
	if city == "" || state == "" {
		httputil.BadRequest(w, "invalid location format, expected 'City, ST' or 'City, State'")
		return
	}

	// Check if orchestrator is configured
	if h.orchestrator == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "property search service not available")
		return
	}

	// Build search params
	params := providers.SearchParams{
		City:  city,
		State: state,
	}

	// Parse optional filters
	if minPrice := httputil.GetQueryParam(r, "minPrice", ""); minPrice != "" {
		if val, err := strconv.Atoi(minPrice); err == nil {
			params.MinPrice = val
		}
	}
	if maxPrice := httputil.GetQueryParam(r, "maxPrice", ""); maxPrice != "" {
		if val, err := strconv.Atoi(maxPrice); err == nil {
			params.MaxPrice = val
		}
	}
	if minBeds := httputil.GetQueryParam(r, "minBeds", ""); minBeds != "" {
		if val, err := strconv.Atoi(minBeds); err == nil {
			params.MinBeds = val
		}
	}
	if maxBeds := httputil.GetQueryParam(r, "maxBeds", ""); maxBeds != "" {
		if val, err := strconv.Atoi(maxBeds); err == nil {
			params.MaxBeds = val
		}
	}
	if propType := httputil.GetQueryParam(r, "propertyType", ""); propType != "" {
		params.PropertyType = providers.PropertyType(propType)
	}
	if limit := httputil.GetQueryParam(r, "limit", ""); limit != "" {
		if val, err := strconv.Atoi(limit); err == nil && val > 0 && val <= 100 {
			params.Limit = val
		}
	}
	if offset := httputil.GetQueryParam(r, "offset", ""); offset != "" {
		if val, err := strconv.Atoi(offset); err == nil && val >= 0 {
			params.Offset = val
		}
	}
	if sortBy := httputil.GetQueryParam(r, "sortBy", ""); sortBy != "" {
		params.SortBy = sortBy
	}
	if sortOrder := httputil.GetQueryParam(r, "sortOrder", ""); sortOrder != "" {
		params.SortOrder = sortOrder
	}

	h.logger.Info("searching properties",
		"city", city,
		"state", state,
		"minPrice", params.MinPrice,
		"maxPrice", params.MaxPrice,
	)

	// Execute search
	result, err := h.orchestrator.Search(ctx, params)
	if err != nil {
		h.logger.Error("property search failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "property search failed")
		return
	}

	response := PropertySearchResponse{
		Success:    true,
		Properties: result.Properties,
		Total:      result.Total,
		HasMore:    result.HasMore,
		NextOffset: result.NextOffset,
		Metrics:    result.Metrics,
	}

	httputil.JSON(w, http.StatusOK, response)
}

// GetMarketDefaults returns market defaults for a location
func (h *Handler) GetMarketDefaults(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	location := httputil.GetQueryParam(r, "location", "")
	if location == "" {
		httputil.BadRequest(w, "location is required")
		return
	}

	// Parse location into city and state
	city, state := parseLocation(location)
	if city == "" || state == "" {
		httputil.BadRequest(w, "invalid location format, expected 'City, ST' or 'City, State'")
		return
	}

	// Check if aggregator is configured
	if h.aggregator == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market data service not available")
		return
	}

	h.logger.Info("getting market defaults",
		"city", city,
		"state", state,
	)

	// Get market data
	data, err := h.aggregator.GetMarketData(ctx, city, state)
	if err != nil {
		h.logger.Error("failed to get market data", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to retrieve market data")
		return
	}

	// Convert to client-expected format
	medianHomePrice := data.MedianHomePrice
	medianRent := data.MedianRent
	capRate := data.CapRate
	priceYoyChange := data.YearOverYearPct

	// Calculate gross rental yield: (annual rent / price) * 100
	var grossRentalYield *float64
	if medianHomePrice > 0 && medianRent > 0 {
		yield := (float64(medianRent) * 12 / float64(medianHomePrice)) * 100
		grossRentalYield = &yield
	}

	// Calculate data quality based on confidence and source
	dataQuality := data.Confidence
	if data.IsAIEstimated {
		dataQuality = dataQuality * 0.7 // Lower quality for AI estimates
	}

	// Build suggested defaults based on market data
	// Use the same price options as client dropdown for consistency
	var suggestedDefaults *SuggestedDefaults
	if medianHomePrice > 0 {
		// Price options must match client/src/components/discover/SearchCriteriaForm.tsx PRICE_OPTIONS
		priceOptions := []int{100000, 200000, 300000, 400000, 500000, 750000, 1000000, 1500000, 2000000}

		// Find the index of the price option closest to but not exceeding median
		medianIdx := -1
		for i, opt := range priceOptions {
			if opt <= medianHomePrice {
				medianIdx = i
			} else {
				break
			}
		}

		// minPrice = 1 step LOWER than median's position in the dropdown
		// maxPrice = 1 step HIGHER than median's position in the dropdown
		var minPrice, maxPrice int

		if medianIdx <= 0 {
			// Median is at or below first option - use first option for min
			minPrice = priceOptions[0]
			maxPrice = priceOptions[1]
		} else if medianIdx >= len(priceOptions)-1 {
			// Median is at or above last option - use last two options
			minPrice = priceOptions[len(priceOptions)-2]
			maxPrice = priceOptions[len(priceOptions)-1]
		} else {
			// Normal case: one step below and one step above median position
			minPrice = priceOptions[medianIdx-1]
			maxPrice = priceOptions[medianIdx+1]
		}

		suggestedDefaults = &SuggestedDefaults{
			MinPrice:      minPrice,
			MaxPrice:      maxPrice,
			MinCapRate:    nil, // Let user decide
			MinGrossYield: nil,
		}
	}

	// Get expense defaults for the state
	var expenseDefaults *ExpenseDefaults
	expenseCalc := expenses.NewCalculator()
	marketDefaults := expenseCalc.GetMarketDefaults(state)
	if marketDefaults != nil {
		// Use vacancy rate from market data (FRED) if available, otherwise use default
		vacancyRate := marketDefaults.VacancyRate
		if data.VacancyRate > 0 {
			vacancyRate = data.VacancyRate
		}

		expenseDefaults = &ExpenseDefaults{
			PropertyTaxRate:  marketDefaults.PropertyTaxRate,
			InsuranceRate:    marketDefaults.InsuranceRate,
			MaintenanceRate:  marketDefaults.MaintenanceRate,
			VacancyRate:      vacancyRate,
			PropertyMgmtRate: marketDefaults.PropertyMgmtRate,
			RiskFactors:      marketDefaults.InsuranceRisk,
			Notes:            marketDefaults.Notes,
		}
	}

	// Get mortgage rates (may be 0 if FRED not configured)
	var mortgageRate30, mortgageRate15 *float64
	if data.MortgageRate30 > 0 {
		mortgageRate30 = &data.MortgageRate30
	}
	if data.MortgageRate15 > 0 {
		mortgageRate15 = &data.MortgageRate15
	}

	response := MarketDefaultsResponse{
		Success: true,
		Location: MarketDefaultsLocation{
			City:  city,
			State: state,
		},
		MarketData: MarketDefaultsData{
			MedianHomePrice:    &medianHomePrice,
			MedianRent:         &medianRent,
			CapRate:            &capRate,
			GrossRentalYield:   grossRentalYield,
			PriceYoyChange:     &priceYoyChange,
			MedianDaysOnMarket: data.MedianDaysOnMarket, // From Redfin via CitySnapshot (ADR-076)
			MortgageRate30:     mortgageRate30,
			MortgageRate15:     mortgageRate15,
		},
		SuggestedDefaults: suggestedDefaults,
		ExpenseDefaults:   expenseDefaults,
		DataQuality:       dataQuality,
	}

	httputil.JSON(w, http.StatusOK, response)
}

// BatchEvaluate evaluates multiple properties
func (h *Handler) BatchEvaluate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse request body
	var req BatchEvaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate request
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Get location from first property
	if len(req.Properties) == 0 {
		httputil.BadRequest(w, "no properties provided")
		return
	}
	city := req.Properties[0].City
	state := req.Properties[0].State

	h.logger.Info("batch evaluating properties",
		"propertyCount", len(req.Properties),
		"city", city,
		"state", state,
	)

	// Get market data for evaluation context
	var marketData *aggregator.MarketData
	if h.aggregator != nil {
		var err error
		marketData, err = h.aggregator.GetMarketData(ctx, city, state)
		if err != nil {
			h.logger.Warn("failed to get market data for evaluation", "error", err)
		}
	}

	// Generate report ID
	reportID := uuid.New().String()

	// Evaluate each property using data from request (no need to fetch)
	evaluations := make([]BatchEvaluationResult, 0, len(req.Properties))
	completedCount := 0

	for _, prop := range req.Properties {
		// Calculate evaluation with scenarios directly from input
		eval := h.evaluatePropertyInputWithScenarios(&prop, marketData)
		evaluations = append(evaluations, eval)
		completedCount++
	}

	response := BatchEvaluateResponse{
		Success:         true,
		ReportID:        reportID,
		TotalProperties: len(req.Properties),
		CompletedCount:  completedCount,
		FailedCount:     0,
		Evaluations:     evaluations,
		MarketData:      marketData,
	}

	httputil.JSON(w, http.StatusOK, response)
}

// evaluatePropertyInputWithScenarios calculates investment metrics from BatchPropertyInput
func (h *Handler) evaluatePropertyInputWithScenarios(prop *BatchPropertyInput, marketData *aggregator.MarketData) BatchEvaluationResult {
	result := BatchEvaluationResult{
		EvaluationID: uuid.New().String(),
		PropertyID:   prop.ID,
		Property: BatchPropertyInfo{
			ID:      prop.ID,
			Address: prop.Address,
			City:    prop.City,
			State:   prop.State,
			ZipCode: prop.ZipCode,
			Price:   prop.Price,
			Beds:    prop.Beds,
			Baths:   prop.Baths,
			Sqft:    prop.Sqft,
		},
		Status: "COMPLETED",
	}

	// Use property's estimated rent or fall back to market data
	baseRent := prop.EstimatedRent
	if baseRent == 0 && marketData != nil {
		baseRent = marketData.MedianRent
	}

	if prop.Price > 0 && baseRent > 0 {
		// Scenario assumptions
		conservativeAssumptions := ScenarioAssumptions{
			AppreciationRate: 0.02,
			VacancyRate:      0.10,
			RentGrowthRate:   0.01,
		}
		baseAssumptions := ScenarioAssumptions{
			AppreciationRate: 0.03,
			VacancyRate:      0.05,
			RentGrowthRate:   0.02,
		}
		optimisticAssumptions := ScenarioAssumptions{
			AppreciationRate: 0.05,
			VacancyRate:      0.03,
			RentGrowthRate:   0.03,
		}

		result.Scenarios = &BatchScenarios{
			Conservative: h.calculateScenarioMetrics("Conservative", prop.Price, baseRent, conservativeAssumptions),
			Base:         h.calculateScenarioMetrics("Base", prop.Price, baseRent, baseAssumptions),
			Optimistic:   h.calculateScenarioMetrics("Optimistic", prop.Price, baseRent, optimisticAssumptions),
		}
	}

	return result
}

// calculateScenarioMetrics calculates metrics for a given price, rent, and assumptions
func (h *Handler) calculateScenarioMetrics(name string, price, monthlyRent int, assumptions ScenarioAssumptions) ScenarioResult {
	priceFloat := float64(price)

	// Apply vacancy rate to rent
	effectiveRent := float64(monthlyRent) * (1 - assumptions.VacancyRate)
	annualRent := effectiveRent * 12

	// Cap rate (assuming 35% expenses)
	netOperatingIncome := annualRent * 0.65
	capRate := (netOperatingIncome / priceFloat) * 100

	// Cash on cash return (assuming 25% down payment, 7% interest rate)
	downPayment := priceFloat * 0.25
	loanAmount := priceFloat * 0.75
	interestRate := 0.07
	// Monthly mortgage payment (simplified: P&I using standard formula)
	monthlyRate := interestRate / 12
	numPayments := 360.0 // 30-year loan
	monthlyPayment := loanAmount * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) / (math.Pow(1+monthlyRate, numPayments) - 1)
	annualDebtService := monthlyPayment * 12

	annualCashFlow := netOperatingIncome - annualDebtService
	cashOnCash := 0.0
	if downPayment > 0 {
		cashOnCash = (annualCashFlow / downPayment) * 100
	}

	// Monthly cash flow
	monthlyCashFlow := annualCashFlow / 12

	// 5-year total return (appreciation + cash flow)
	totalAppreciation := priceFloat * math.Pow(1+assumptions.AppreciationRate, 5) - priceFloat
	totalCashFlow := annualCashFlow * 5
	totalReturn5Yr := 0.0
	if downPayment > 0 {
		totalReturn5Yr = ((totalAppreciation + totalCashFlow) / downPayment) * 100
	}

	// IRR 5-year (simplified approximation)
	irr5Yr := totalReturn5Yr / 5

	// Break-even months (time to recover down payment from cash flow)
	breakEvenMonths := 0
	if monthlyCashFlow > 0 {
		breakEvenMonths = int(math.Ceil(downPayment / monthlyCashFlow))
	}

	return ScenarioResult{
		Name:        name,
		Assumptions: assumptions,
		Metrics: ScenarioMetrics{
			MonthlyPayment:  math.Round(monthlyPayment*100) / 100,
			MonthlyCashFlow: math.Round(monthlyCashFlow*100) / 100,
			AnnualCashFlow:  math.Round(annualCashFlow*100) / 100,
			CashOnCash:      math.Round(cashOnCash*100) / 100,
			CapRate:         math.Round(capRate*100) / 100,
			TotalReturn5Yr:  math.Round(totalReturn5Yr*100) / 100,
			IRR5Yr:          math.Round(irr5Yr*100) / 100,
			BreakEvenMonths: breakEvenMonths,
		},
	}
}

// parseLocation splits "City, ST" or "City, State" into components
func parseLocation(location string) (city, state string) {
	parts := strings.Split(location, ",")
	if len(parts) < 2 {
		return "", ""
	}

	city = strings.TrimSpace(parts[0])
	state = strings.TrimSpace(parts[1])

	// Handle full state names to abbreviations (common cases)
	stateAbbreviations := map[string]string{
		"alabama": "AL", "alaska": "AK", "arizona": "AZ", "arkansas": "AR",
		"california": "CA", "colorado": "CO", "connecticut": "CT", "delaware": "DE",
		"florida": "FL", "georgia": "GA", "hawaii": "HI", "idaho": "ID",
		"illinois": "IL", "indiana": "IN", "iowa": "IA", "kansas": "KS",
		"kentucky": "KY", "louisiana": "LA", "maine": "ME", "maryland": "MD",
		"massachusetts": "MA", "michigan": "MI", "minnesota": "MN", "mississippi": "MS",
		"missouri": "MO", "montana": "MT", "nebraska": "NE", "nevada": "NV",
		"new hampshire": "NH", "new jersey": "NJ", "new mexico": "NM", "new york": "NY",
		"north carolina": "NC", "north dakota": "ND", "ohio": "OH", "oklahoma": "OK",
		"oregon": "OR", "pennsylvania": "PA", "rhode island": "RI", "south carolina": "SC",
		"south dakota": "SD", "tennessee": "TN", "texas": "TX", "utah": "UT",
		"vermont": "VT", "virginia": "VA", "washington": "WA", "west virginia": "WV",
		"wisconsin": "WI", "wyoming": "WY", "district of columbia": "DC",
	}

	// Check if state is a full name and convert to abbreviation
	stateLower := strings.ToLower(state)
	if abbr, ok := stateAbbreviations[stateLower]; ok {
		state = abbr
	} else if len(state) == 2 {
		// Already an abbreviation, uppercase it
		state = strings.ToUpper(state)
	}

	return city, state
}

// buildListingSearchUrl creates a Google search URL for finding the property listing
func buildListingSearchUrl(address, city, state, zipCode string) string {
	parts := []string{}
	if address != "" {
		parts = append(parts, address)
	}
	if city != "" {
		parts = append(parts, city)
	}
	if state != "" {
		parts = append(parts, state)
	}
	if zipCode != "" {
		parts = append(parts, zipCode)
	}
	fullAddress := strings.Join(parts, " ")
	query := fullAddress + " real estate listing"
	return "https://www.google.com/search?q=" + strings.ReplaceAll(query, " ", "+")
}

// buildGoogleSearchUrl creates a Google search URL for the property
func buildGoogleSearchUrl(address, city, state, zipCode string) string {
	parts := []string{}
	if address != "" {
		parts = append(parts, address)
	}
	if city != "" {
		parts = append(parts, city)
	}
	if state != "" {
		parts = append(parts, state)
	}
	if zipCode != "" {
		parts = append(parts, zipCode)
	}
	fullAddress := strings.Join(parts, " ")
	query := fullAddress + " for sale"
	return "https://www.google.com/search?q=" + strings.ReplaceAll(query, " ", "+")
}

// calculateCapRateRange calculates the cap rate range based on price and rent
func calculateCapRateRange(price, estimatedRent int) *CapRateRange {
	if estimatedRent <= 0 || price <= 0 {
		return nil
	}

	annualRent := float64(estimatedRent * 12)
	baseCapRate := (annualRent / float64(price)) * 100

	return &CapRateRange{
		Min: baseCapRate - 0.5,
		Max: baseCapRate + 0.5,
	}
}

// QuotaResponse matches www_v1 /api/v2/quota response format
type QuotaResponse struct {
	HasSubscription    bool     `json:"hasSubscription"`
	Tier               string   `json:"tier"`
	Used               int      `json:"used"`
	Limit              int      `json:"limit"`
	Remaining          any      `json:"remaining"` // int or "unlimited"
	IsUnlimited        bool     `json:"isUnlimited"`
	PeriodStart        string   `json:"periodStart"`
	PeriodEnd          string   `json:"periodEnd"`
	DaysRemaining      int      `json:"daysRemaining"`
	WarningThreshold   bool     `json:"warningThreshold"`
	DisplayName        string   `json:"displayName"`
	PricePerYear       int      `json:"pricePerYear"`
	TierFeatures       []string `json:"tierFeatures"`
}

// Tier configuration
var tierConfigs = map[string]struct {
	DisplayName  string
	PricePerYear int
	Limit        int
	Features     []string
}{
	"V2_FREE": {
		DisplayName:  "Free",
		PricePerYear: 0,
		Limit:        5,
		Features:     []string{"discover", "propertySearch"},
	},
	"V2_STARTER": {
		DisplayName:  "Starter",
		PricePerYear: 300,
		Limit:        20,
		Features:     []string{"discover", "propertySearch", "aiEvaluation"},
	},
	"V2_ANNUAL_ACCESS": {
		DisplayName:  "Annual Access",
		PricePerYear: 1200,
		Limit:        500,
		Features:     []string{"discover", "propertySearch", "aiEvaluation", "investmentPlanning", "portfolioTracking"},
	},
	"V2_PROFESSIONAL_ALLOCATOR": {
		DisplayName:  "Professional Allocator",
		PricePerYear: 2400,
		Limit:        1000,
		Features:     []string{"discover", "propertySearch", "aiEvaluation", "investmentPlanning", "portfolioTracking", "prioritySupport"},
	},
	// Backwards compatibility
	"V2_PROFESSIONAL": {
		DisplayName:  "Annual Access",
		PricePerYear: 1200,
		Limit:        500,
		Features:     []string{"discover", "propertySearch", "aiEvaluation", "investmentPlanning", "portfolioTracking"},
	},
	"V2_ENTERPRISE": {
		DisplayName:  "Enterprise",
		PricePerYear: 0,
		Limit:        -1, // unlimited
		Features:     []string{"discover", "propertySearch", "aiEvaluation", "investmentPlanning", "portfolioTracking", "apiAccess", "prioritySupport"},
	},
}

// GetQuota returns the user's quota/subscription status
func (h *Handler) GetQuota(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	uid := user.UserID

	// Query user's V2 evaluation quota from v2_evaluation_quotas table
	// This matches the auth handler's getV2Quota query
	query := `
		SELECT tier, annual_limit, used_this_period,
		       period_start_date, period_end_date
		FROM v2_evaluation_quotas
		WHERE user_id = $1
	`

	var tier string
	var annualLimit, usedThisPeriod int
	var periodStart, periodEnd time.Time

	err := h.db.Main.QueryRow(ctx, query, uid).Scan(&tier, &annualLimit, &usedThisPeriod, &periodStart, &periodEnd)
	if err != nil {
		h.logger.Error("failed to get user quota", "error", err, "user_id", uid)
		// Return default free tier on error
		httputil.JSON(w, http.StatusOK, QuotaResponse{
			HasSubscription:  false,
			Tier:             "V2_FREE",
			Used:             0,
			Limit:            5,
			Remaining:        5,
			IsUnlimited:      false,
			PeriodStart:      "",
			PeriodEnd:        "",
			DaysRemaining:    0,
			WarningThreshold: false,
			DisplayName:      "Free",
			PricePerYear:     0,
			TierFeatures:     []string{"discover", "propertySearch"},
		})
		return
	}

	// Use tier from database
	effectiveTier := tier
	if effectiveTier == "" {
		effectiveTier = "V2_FREE"
	}

	config, ok := tierConfigs[effectiveTier]
	if !ok {
		config = tierConfigs["V2_FREE"]
	}

	// Calculate remaining - use annual_limit from database
	limit := annualLimit
	if limit == 0 {
		limit = config.Limit
	}
	var remaining any
	isUnlimited := limit == -1
	if isUnlimited {
		remaining = "unlimited"
	} else {
		rem := limit - usedThisPeriod
		if rem < 0 {
			rem = 0
		}
		remaining = rem
	}

	// Calculate days remaining
	daysRemaining := 0
	periodStartStr := periodStart.Format(time.RFC3339)
	periodEndStr := periodEnd.Format(time.RFC3339)
	days := int(periodEnd.Sub(getNow()).Hours() / 24)
	if days > 0 {
		daysRemaining = days
	}

	// Warning threshold at 80% usage
	warningThreshold := false
	if !isUnlimited && limit > 0 {
		warningThreshold = float64(usedThisPeriod) >= float64(limit)*0.8
	}

	// Determine if user has active subscription (not free tier and period not expired)
	hasSubscription := effectiveTier != "V2_FREE" && periodEnd.After(getNow())

	response := QuotaResponse{
		HasSubscription:  hasSubscription,
		Tier:             effectiveTier,
		Used:             usedThisPeriod,
		Limit:            limit,
		Remaining:        remaining,
		IsUnlimited:      isUnlimited,
		PeriodStart:      periodStartStr,
		PeriodEnd:        periodEndStr,
		DaysRemaining:    daysRemaining,
		WarningThreshold: warningThreshold,
		DisplayName:      config.DisplayName,
		PricePerYear:     config.PricePerYear,
		TierFeatures:     config.Features,
	}

	httputil.JSON(w, http.StatusOK, response)
}

// Helper functions for time parsing
func parseTime(s string) (time.Time, error) {
	// Try common formats
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse time: %s", s)
}

func getNow() time.Time {
	return time.Now()
}

// RecordsResponse matches www_v1 /api/v2/records response format
type RecordsResponse struct {
	Success    bool                `json:"success"`
	Records    []DecisionRecord    `json:"records"`
	Pagination RecordsPagination   `json:"pagination"`
}

type DecisionRecord struct {
	ID           string           `json:"id"`
	EvaluationID *string          `json:"evaluationId,omitempty"`
	Property     RecordProperty   `json:"property"`
	PDFUrl       *string          `json:"pdfUrl,omitempty"`
	ExportedAt   *string          `json:"exportedAt,omitempty"`
	CreatedAt    string           `json:"createdAt"`
}

type RecordProperty struct {
	Address       string  `json:"address"`
	City          string  `json:"city"`
	State         string  `json:"state"`
	PurchasePrice float64 `json:"purchasePrice"`
	MonthlyRent   float64 `json:"monthlyRent"`
}

type RecordsPagination struct {
	Total   int  `json:"total"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"hasMore"`
}

// GetRecords returns the user's decision records (discover history)
func (h *Handler) GetRecords(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	uid := user.UserID

	// Parse pagination params
	limit := httputil.GetQueryParamInt(r, "limit", 20)
	if limit > 100 {
		limit = 100
	}
	offset := httputil.GetQueryParamInt(r, "offset", 0)

	// Query decision records from v2_decision_records table joined with v2_evaluations
	// Property info comes from v2_evaluations, not cached_properties
	query := `
		SELECT
			r.id,
			r.evaluation_id,
			r.pdf_url,
			r.exported_at,
			r.created_at,
			e.property_address,
			e.property_city,
			e.property_state,
			e.purchase_price,
			e.monthly_rent
		FROM v2_decision_records r
		JOIN v2_evaluations e ON r.evaluation_id = e.id
		WHERE r.user_id = $1
		ORDER BY r.exported_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := h.db.Main.Query(ctx, query, uid, limit, offset)
	if err != nil {
		h.logger.Error("failed to get decision records", "error", err)
		// Return empty records on error
		httputil.JSON(w, http.StatusOK, RecordsResponse{
			Success: true,
			Records: []DecisionRecord{},
			Pagination: RecordsPagination{
				Total:   0,
				Limit:   limit,
				Offset:  offset,
				HasMore: false,
			},
		})
		return
	}
	defer rows.Close()

	records := make([]DecisionRecord, 0)
	for rows.Next() {
		var rec DecisionRecord
		var evalID, pdfUrl *string
		var exportedAt, createdAt time.Time

		err := rows.Scan(
			&rec.ID, &evalID, &pdfUrl, &exportedAt, &createdAt,
			&rec.Property.Address, &rec.Property.City, &rec.Property.State,
			&rec.Property.PurchasePrice, &rec.Property.MonthlyRent,
		)
		if err != nil {
			h.logger.Warn("failed to scan decision record", "error", err)
			continue
		}

		rec.EvaluationID = evalID
		rec.PDFUrl = pdfUrl
		exportedAtStr := exportedAt.Format(time.RFC3339)
		rec.ExportedAt = &exportedAtStr
		rec.CreatedAt = createdAt.Format(time.RFC3339)

		records = append(records, rec)
	}

	// Get total count
	var total int
	h.db.Main.QueryRow(ctx, `SELECT COUNT(*) FROM v2_decision_records WHERE user_id = $1`, uid).Scan(&total)

	response := RecordsResponse{
		Success: true,
		Records: records,
		Pagination: RecordsPagination{
			Total:   total,
			Limit:   limit,
			Offset:  offset,
			HasMore: offset+len(records) < total,
		},
	}

	httputil.JSON(w, http.StatusOK, response)
}
