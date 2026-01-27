package discover

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	redisClient "github.com/estara-ai/www/internal/db/redis"
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
	Success  bool                          `json:"success"`
	Location MarketDefaultsLocation        `json:"location"`
	MarketData MarketDefaultsData          `json:"marketData"`
	SuggestedDefaults *SuggestedDefaults   `json:"suggestedDefaults"`
	DataQuality float64                    `json:"dataQuality"`
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
	PropertyIDs []string `json:"propertyIds" validate:"required,min=1,max=20"`
	Location    string   `json:"location" validate:"required"`
}

// PropertyEvaluation represents evaluation metrics for a property
type PropertyEvaluation struct {
	PropertyID    string  `json:"propertyId"`
	EstimatedRent int     `json:"estimatedRent"`
	CapRate       float64 `json:"capRate"`
	CashOnCash    float64 `json:"cashOnCash"`
	GrossYield    float64 `json:"grossYield"`
	DSCR          float64 `json:"dscr"`
	Score         int     `json:"score"` // 0-100
	Rating        string  `json:"rating"` // poor, fair, good, excellent
}

// BatchEvaluateResponse wraps batch evaluation results
type BatchEvaluateResponse struct {
	Success     bool                   `json:"success"`
	Evaluations []PropertyEvaluation   `json:"evaluations"`
	MarketData  *aggregator.MarketData `json:"marketData"`
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
	Success    bool               `json:"success"`
	Properties []V2PropertyResult `json:"properties"`
	TotalCount int                `json:"totalCount"`
}

// Search handles POST /api/v2/discover/search - matches www_v1 API contract
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	// Execute search
	result, err := h.orchestrator.Search(ctx, params)
	if err != nil {
		h.logger.Error("property search failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "property search failed")
		return
	}

	// Transform to V2 response format (properties already enriched by orchestrator)
	properties := make([]V2PropertyResult, 0, len(result.Properties))
	for _, p := range result.Properties {
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
		if criteria.MinCapRate > 0 || criteria.MinGrossYield > 0 {
			// Use enriched cap rate (with expenses) instead of calculating
			capRate := p.EstimatedCapRate
			grossYield := p.GrossYield

			// Apply filters
			if criteria.MinCapRate > 0 && capRate < criteria.MinCapRate {
				continue
			}
			if criteria.MinGrossYield > 0 && grossYield < criteria.MinGrossYield {
				continue
			}
		}

		properties = append(properties, prop)
	}

	response := V2SearchResponse{
		Success:    true,
		Properties: properties,
		TotalCount: len(properties),
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
	var suggestedDefaults *SuggestedDefaults
	if medianHomePrice > 0 {
		minPrice := int(float64(medianHomePrice) * 0.5)  // 50% of median
		maxPrice := int(float64(medianHomePrice) * 1.5)  // 150% of median

		// Round to nice numbers
		minPrice = (minPrice / 10000) * 10000
		maxPrice = (maxPrice / 10000) * 10000

		if minPrice < 50000 {
			minPrice = 50000
		}

		suggestedDefaults = &SuggestedDefaults{
			MinPrice:      minPrice,
			MaxPrice:      maxPrice,
			MinCapRate:    nil, // Let user decide
			MinGrossYield: nil,
		}
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
			MedianDaysOnMarket: nil, // Not available from current sources
		},
		SuggestedDefaults: suggestedDefaults,
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

	// Parse location
	city, state := parseLocation(req.Location)
	if city == "" || state == "" {
		httputil.BadRequest(w, "invalid location format")
		return
	}

	h.logger.Info("batch evaluating properties",
		"propertyCount", len(req.PropertyIDs),
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

	// Evaluate each property
	evaluations := make([]PropertyEvaluation, 0, len(req.PropertyIDs))

	for _, propID := range req.PropertyIDs {
		// Get property details
		var property *providers.Property
		if h.orchestrator != nil {
			var err error
			property, err = h.orchestrator.GetProperty(ctx, "", propID)
			if err != nil {
				h.logger.Warn("failed to get property", "propertyId", propID, "error", err)
				// Add placeholder evaluation for missing property
				evaluations = append(evaluations, PropertyEvaluation{
					PropertyID: propID,
					Score:      0,
					Rating:     "unknown",
				})
				continue
			}
		}

		// Calculate evaluation metrics
		eval := h.evaluateProperty(property, marketData)
		evaluations = append(evaluations, eval)
	}

	response := BatchEvaluateResponse{
		Success:     true,
		Evaluations: evaluations,
		MarketData:  marketData,
	}

	httputil.JSON(w, http.StatusOK, response)
}

// evaluateProperty calculates investment metrics for a property
func (h *Handler) evaluateProperty(property *providers.Property, marketData *aggregator.MarketData) PropertyEvaluation {
	if property == nil {
		return PropertyEvaluation{
			Score:  0,
			Rating: "unknown",
		}
	}

	eval := PropertyEvaluation{
		PropertyID: property.ID,
	}

	// Use property's estimated rent or fall back to market data
	estimatedRent := property.EstimatedRent
	if estimatedRent == 0 && marketData != nil {
		estimatedRent = marketData.MedianRent
	}
	eval.EstimatedRent = estimatedRent

	// Calculate metrics if we have price and rent
	if property.Price > 0 && estimatedRent > 0 {
		annualRent := float64(estimatedRent * 12)

		// Gross yield: (Annual Rent / Price) * 100
		eval.GrossYield = (annualRent / float64(property.Price)) * 100

		// Cap rate (simplified - assuming 35% expenses)
		netOperatingIncome := annualRent * 0.65
		eval.CapRate = (netOperatingIncome / float64(property.Price)) * 100

		// Cash on cash return (assuming 25% down payment)
		downPayment := float64(property.Price) * 0.25
		if downPayment > 0 {
			// Simplified - not accounting for mortgage details
			annualCashFlow := netOperatingIncome - (float64(property.Price) * 0.75 * 0.07) // 7% assumed rate
			eval.CashOnCash = (annualCashFlow / downPayment) * 100
		}

		// DSCR (Debt Service Coverage Ratio)
		monthlyMortgage := (float64(property.Price) * 0.75 * 0.07) / 12
		if monthlyMortgage > 0 {
			eval.DSCR = (netOperatingIncome / 12) / monthlyMortgage
		}
	}

	// Calculate score and rating
	eval.Score = h.calculatePropertyScore(eval)
	eval.Rating = h.calculatePropertyRating(eval.Score)

	return eval
}

// calculatePropertyScore calculates a 0-100 score based on metrics
func (h *Handler) calculatePropertyScore(eval PropertyEvaluation) int {
	score := 0
	factors := 0

	// Cap rate scoring (0-25 points)
	if eval.CapRate > 0 {
		factors++
		switch {
		case eval.CapRate >= 8:
			score += 25
		case eval.CapRate >= 6:
			score += 20
		case eval.CapRate >= 4:
			score += 15
		case eval.CapRate >= 2:
			score += 10
		default:
			score += 5
		}
	}

	// Cash on cash scoring (0-25 points)
	if eval.CashOnCash > 0 {
		factors++
		switch {
		case eval.CashOnCash >= 12:
			score += 25
		case eval.CashOnCash >= 8:
			score += 20
		case eval.CashOnCash >= 4:
			score += 15
		case eval.CashOnCash >= 0:
			score += 10
		default:
			score += 0
		}
	}

	// DSCR scoring (0-25 points)
	if eval.DSCR > 0 {
		factors++
		switch {
		case eval.DSCR >= 1.5:
			score += 25
		case eval.DSCR >= 1.25:
			score += 20
		case eval.DSCR >= 1.0:
			score += 15
		case eval.DSCR >= 0.8:
			score += 10
		default:
			score += 5
		}
	}

	// Gross yield scoring (0-25 points)
	if eval.GrossYield > 0 {
		factors++
		switch {
		case eval.GrossYield >= 10:
			score += 25
		case eval.GrossYield >= 8:
			score += 20
		case eval.GrossYield >= 6:
			score += 15
		case eval.GrossYield >= 4:
			score += 10
		default:
			score += 5
		}
	}

	// Normalize to 100 if we have factors
	if factors > 0 {
		maxPossible := factors * 25
		score = (score * 100) / maxPossible
	}

	return score
}

// calculatePropertyRating converts score to rating
func (h *Handler) calculatePropertyRating(score int) string {
	switch {
	case score >= 80:
		return "excellent"
	case score >= 60:
		return "good"
	case score >= 40:
		return "fair"
	default:
		return "poor"
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
	"V2_PROFESSIONAL": {
		DisplayName:  "Professional",
		PricePerYear: 1200,
		Limit:        50,
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
