package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	hasDataDefaultTimeout = 60 * time.Second
	hasDataDefaultLimit   = 50
	hasDataBaseURL        = "https://api.hasdata.com"
)

// HasDataProvider implements the Provider interface for HasData API
type HasDataProvider struct {
	client   *http.Client
	apiKey   string
	baseURL  string
	enabled  bool
	priority int
	logger   *slog.Logger
}

// HasDataConfig holds configuration for HasData provider
type HasDataConfig struct {
	APIKey   string
	BaseURL  string
	Enabled  bool
	Priority int
	Timeout  time.Duration
}

// hasDataSearchRequest represents a search request to HasData API
type hasDataSearchRequest struct {
	Query       string `json:"query"`
	Location    string `json:"location,omitempty"`
	MinPrice    int    `json:"min_price,omitempty"`
	MaxPrice    int    `json:"max_price,omitempty"`
	MinBeds     int    `json:"min_beds,omitempty"`
	MaxBeds     int    `json:"max_beds,omitempty"`
	MinBaths    int    `json:"min_baths,omitempty"`
	PropertyType string `json:"property_type,omitempty"`
	Status      string `json:"status,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
}

// hasDataResponse represents a response from HasData API
type hasDataResponse struct {
	Success    bool                `json:"success"`
	Data       hasDataSearchData   `json:"data"`
	Error      string              `json:"error,omitempty"`
	RequestID  string              `json:"request_id,omitempty"`
}

// hasDataSearchData holds the search results
type hasDataSearchData struct {
	Properties []hasDataProperty `json:"properties"`
	Total      int               `json:"total"`
	Page       int               `json:"page"`
	PageSize   int               `json:"page_size"`
}

// hasDataProperty represents a property from HasData
type hasDataProperty struct {
	PropertyID    string          `json:"property_id"`
	Address       hasDataAddress  `json:"address"`
	Price         hasDataPrice    `json:"price"`
	Details       hasDataDetails  `json:"details"`
	Listing       hasDataListing  `json:"listing"`
	Location      hasDataLocation `json:"location"`
	Media         hasDataMedia    `json:"media"`
	Estimates     hasDataEstimates `json:"estimates,omitempty"`
}

type hasDataAddress struct {
	FullAddress  string `json:"full_address"`
	Street       string `json:"street"`
	City         string `json:"city"`
	State        string `json:"state"`
	ZipCode      string `json:"zip_code"`
	County       string `json:"county"`
}

type hasDataPrice struct {
	ListPrice      int    `json:"list_price"`
	OriginalPrice  int    `json:"original_price"`
	PricePerSqft   int    `json:"price_per_sqft"`
	Currency       string `json:"currency"`
}

type hasDataDetails struct {
	Beds         int     `json:"beds"`
	Baths        float64 `json:"baths"`
	Sqft         int     `json:"sqft"`
	LotSize      int     `json:"lot_size"`
	YearBuilt    int     `json:"year_built"`
	PropertyType string  `json:"property_type"`
	Stories      int     `json:"stories"`
	Garage       int     `json:"garage"`
}

type hasDataListing struct {
	Status       string `json:"status"`
	DaysOnMarket int    `json:"days_on_market"`
	MLSNumber    string `json:"mls_number"`
	ListedDate   string `json:"listed_date"`
	AgentName    string `json:"agent_name"`
	BrokerName   string `json:"broker_name"`
	ListingURL   string `json:"listing_url"`
	Description  string `json:"description"`
}

type hasDataLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type hasDataMedia struct {
	Photos []string `json:"photos"`
}

type hasDataEstimates struct {
	RentEstimate  int     `json:"rent_estimate"`
	ValueEstimate int     `json:"value_estimate"`
	CapRate       float64 `json:"cap_rate"`
}

// hasDataZillowResponse represents the response from HasData Zillow Listing API
// GET /scrape/zillow/listing
type hasDataZillowResponse struct {
	RequestMetadata hasDataRequestMetadata `json:"requestMetadata"`
	SearchInfo      hasDataSearchInfo      `json:"searchInformation"`
	Properties      []hasDataZillowProp    `json:"properties"`
}

type hasDataRequestMetadata struct {
	ID         string `json:"id"`
	Status     string `json:"status"` // "ok" or "error"
	URL        string `json:"url"`
	NumRecords int    `json:"num_records,omitempty"`
	Error      string `json:"error,omitempty"`
}

type hasDataSearchInfo struct {
	TotalResults   int `json:"totalResults"`
	ResultsPerPage int `json:"resultsPerPage"`
	CurrentPage    int `json:"currentPage"`
}

// hasDataZillowProp represents a property from HasData Zillow API
// Note: Some fields can be int or string depending on the API response,
// so we use json.Number or interface{} for flexibility
type hasDataZillowProp struct {
	ID           json.Number `json:"id"`
	PropertyID   json.Number `json:"propertyId"`
	MLSID        string      `json:"mlsId"`
	URL          string      `json:"url"`
	Price        int         `json:"price"`
	PropertyType string      `json:"propertyType"`
	Beds         int         `json:"beds"`
	Baths        float64     `json:"baths"`
	Area         int         `json:"area"` // square feet
	LotSize      int         `json:"lotSize"`
	YearBuilt    int         `json:"yearBuilt"`
	ListingType  string      `json:"listingType"`
	Status       string      `json:"status"`
	DaysOnZillow int         `json:"daysOnZillow"`
	Description  string      `json:"description"`
	RentZestimate int        `json:"rentZestimate"`
	ImageURL     string      `json:"imageUrl"`
	Photos       []string    `json:"photos"`
	BrokerName   string      `json:"brokerName"`
	AgentName    string      `json:"agentName"`
	Address      struct {
		Street    string  `json:"street"`
		City      string  `json:"city"`
		State     string  `json:"state"`
		Zipcode   string  `json:"zipcode"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"address"`
}

// NewHasDataProvider creates a new HasData provider
func NewHasDataProvider(cfg HasDataConfig) *HasDataProvider {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = hasDataDefaultTimeout
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = hasDataBaseURL
	}

	return &HasDataProvider{
		client: &http.Client{
			Timeout: timeout,
		},
		apiKey:   cfg.APIKey,
		baseURL:  baseURL,
		enabled:  cfg.Enabled,
		priority: cfg.Priority,
		logger:   slog.Default().With("component", "hasdata_provider"),
	}
}

// Name returns the provider's identifier
func (p *HasDataProvider) Name() string {
	return "hasdata"
}

// IsEnabled returns whether this provider is currently enabled
func (p *HasDataProvider) IsEnabled() bool {
	return p.enabled && p.apiKey != ""
}

// Priority returns the provider's priority
func (p *HasDataProvider) Priority() int {
	return p.priority
}

// Search performs a property search
func (p *HasDataProvider) Search(ctx context.Context, params SearchParams) (*SearchResult, error) {
	startTime := time.Now()

	if !p.IsEnabled() {
		return nil, fmt.Errorf("HasData provider is not enabled")
	}

	// Build request
	req := p.buildRequest(params)

	// Make API call
	data, err := p.callAPI(ctx, req)
	if err != nil {
		return nil, err
	}

	// Convert to standard format
	result := &SearchResult{
		Properties: make([]Property, 0, len(data.Properties)),
		Total:      data.Total,
		HasMore:    (params.Offset + len(data.Properties)) < data.Total,
		Provider:   p.Name(),
		SearchTime: time.Since(startTime),
	}

	for _, prop := range data.Properties {
		result.Properties = append(result.Properties, p.convertProperty(prop))
	}

	if result.HasMore && len(data.Properties) > 0 {
		result.NextOffset = params.Offset + len(data.Properties)
	}

	p.logger.Info("HasData search completed",
		"location", fmt.Sprintf("%s, %s", params.City, params.State),
		"results", len(result.Properties),
		"total", data.Total,
		"duration", result.SearchTime,
	)

	return result, nil
}

// GetProperty retrieves a single property by ID
func (p *HasDataProvider) GetProperty(ctx context.Context, propertyID string) (*Property, error) {
	if !p.IsEnabled() {
		return nil, fmt.Errorf("HasData provider is not enabled")
	}

	endpoint := fmt.Sprintf("%s/properties/%s", p.baseURL, url.PathEscape(propertyID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Success bool             `json:"success"`
		Data    hasDataProperty  `json:"data"`
		Error   string           `json:"error,omitempty"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API returned error: %s", apiResp.Error)
	}

	property := p.convertProperty(apiResp.Data)
	return &property, nil
}

// HealthCheck verifies the provider is operational
func (p *HasDataProvider) HealthCheck(ctx context.Context) error {
	if !p.IsEnabled() {
		return fmt.Errorf("HasData provider is disabled")
	}

	// Simple test search to verify API connectivity
	testParams := SearchParams{
		City:  "Austin",
		State: "TX",
		Limit: 1,
	}

	_, err := p.Search(ctx, testParams)
	return err
}

// buildRequest converts SearchParams to HasData API request format
func (p *HasDataProvider) buildRequest(params SearchParams) hasDataSearchRequest {
	limit := params.Limit
	if limit == 0 {
		limit = hasDataDefaultLimit
	}

	location := params.City
	if params.State != "" {
		location = fmt.Sprintf("%s, %s", params.City, params.State)
	}
	if params.ZipCode != "" {
		location = params.ZipCode
	}

	req := hasDataSearchRequest{
		Query:    "for sale",
		Location: location,
		Limit:    limit,
		Offset:   params.Offset,
		Status:   "active",
	}

	if params.MinPrice > 0 {
		req.MinPrice = params.MinPrice
	}
	if params.MaxPrice > 0 {
		req.MaxPrice = params.MaxPrice
	}
	if params.MinBeds > 0 {
		req.MinBeds = params.MinBeds
	}
	if params.MaxBeds > 0 {
		req.MaxBeds = params.MaxBeds
	}
	if params.MinBaths > 0 {
		req.MinBaths = int(params.MinBaths)
	}

	if params.PropertyType != "" {
		req.PropertyType = p.mapPropertyType(params.PropertyType)
	}

	if params.Status != "" {
		req.Status = p.mapListingStatus(params.Status)
	}

	return req
}

// callAPI makes the HTTP request to HasData Zillow Listing API
// Uses GET request with query parameters (bracket notation for filters)
// See: https://hasdata.com/docs/zillow-listing-api
func (p *HasDataProvider) callAPI(ctx context.Context, req hasDataSearchRequest) (*hasDataSearchData, error) {
	// Build query parameters with bracket notation
	queryParams := url.Values{}
	queryParams.Set("keyword", req.Location)
	queryParams.Set("type", "forSale") // HasData uses camelCase: forSale, forRent, sold

	// Page (1-indexed)
	page := 1
	if req.Offset > 0 && req.Limit > 0 {
		page = (req.Offset / req.Limit) + 1
	}
	queryParams.Set("page", fmt.Sprintf("%d", page))

	// Price filters with bracket notation
	if req.MinPrice > 0 {
		queryParams.Set("price[min]", fmt.Sprintf("%d", req.MinPrice))
	}
	if req.MaxPrice > 0 {
		queryParams.Set("price[max]", fmt.Sprintf("%d", req.MaxPrice))
	}

	// Bedroom filters with bracket notation
	if req.MinBeds > 0 {
		queryParams.Set("beds[min]", fmt.Sprintf("%d", req.MinBeds))
	}
	if req.MaxBeds > 0 {
		queryParams.Set("beds[max]", fmt.Sprintf("%d", req.MaxBeds))
	}

	// Bathroom filters with bracket notation
	if req.MinBaths > 0 {
		queryParams.Set("baths[min]", fmt.Sprintf("%d", req.MinBaths))
	}

	// Property type filter (homeTypes[] array notation)
	if req.PropertyType != "" {
		queryParams.Add("homeTypes[]", req.PropertyType)
	}

	// Build the full URL
	endpoint := fmt.Sprintf("%s/scrape/zillow/listing?%s", p.baseURL, queryParams.Encode())

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// HasData uses x-api-key header for authentication
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("Accept", "application/json")

	p.logger.Debug("HasData API request",
		"endpoint", endpoint,
		"location", req.Location,
	)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		p.logger.Error("HasData API error",
			"status", resp.StatusCode,
			"body", string(respBody),
		)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	// Parse the Zillow Listing API response
	var apiResp hasDataZillowResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for error in response
	if apiResp.RequestMetadata.Status == "error" {
		return nil, fmt.Errorf("API returned error: %s", apiResp.RequestMetadata.Error)
	}

	// Convert to internal format
	result := &hasDataSearchData{
		Properties: make([]hasDataProperty, 0, len(apiResp.Properties)),
		Total:      apiResp.SearchInfo.TotalResults,
		Page:       page,
		PageSize:   len(apiResp.Properties),
	}

	for _, zp := range apiResp.Properties {
		result.Properties = append(result.Properties, p.convertZillowProperty(zp))
	}

	return result, nil
}

// convertZillowProperty converts HasData Zillow API property to internal format
func (p *HasDataProvider) convertZillowProperty(zp hasDataZillowProp) hasDataProperty {
	photos := zp.Photos
	if len(photos) == 0 && zp.ImageURL != "" {
		photos = []string{zp.ImageURL}
	}

	// Convert json.Number to string for PropertyID
	propertyID := zp.PropertyID.String()
	if propertyID == "" {
		propertyID = zp.ID.String()
	}

	return hasDataProperty{
		PropertyID: propertyID,
		Address: hasDataAddress{
			FullAddress: fmt.Sprintf("%s, %s, %s %s", zp.Address.Street, zp.Address.City, zp.Address.State, zp.Address.Zipcode),
			Street:      zp.Address.Street,
			City:        zp.Address.City,
			State:       zp.Address.State,
			ZipCode:     zp.Address.Zipcode,
		},
		Price: hasDataPrice{
			ListPrice: zp.Price,
		},
		Details: hasDataDetails{
			Beds:         zp.Beds,
			Baths:        zp.Baths,
			Sqft:         zp.Area,
			LotSize:      zp.LotSize,
			YearBuilt:    zp.YearBuilt,
			PropertyType: zp.PropertyType,
		},
		Listing: hasDataListing{
			Status:       zp.Status,
			DaysOnMarket: zp.DaysOnZillow,
			MLSNumber:    zp.MLSID,
			ListingURL:   zp.URL,
			Description:  zp.Description,
			AgentName:    zp.AgentName,
			BrokerName:   zp.BrokerName,
		},
		Location: hasDataLocation{
			Latitude:  zp.Address.Latitude,
			Longitude: zp.Address.Longitude,
		},
		Media: hasDataMedia{
			Photos: photos,
		},
		Estimates: hasDataEstimates{
			RentEstimate: zp.RentZestimate,
		},
	}
}

// convertProperty converts HasData property to standard format
func (p *HasDataProvider) convertProperty(prop hasDataProperty) Property {
	property := Property{
		ID:            prop.PropertyID,
		ProviderID:    prop.PropertyID,
		ProviderName:  p.Name(),
		Address:       prop.Address.FullAddress,
		City:          prop.Address.City,
		State:         prop.Address.State,
		ZipCode:       prop.Address.ZipCode,
		County:        prop.Address.County,
		Latitude:      prop.Location.Latitude,
		Longitude:     prop.Location.Longitude,
		Price:         prop.Price.ListPrice,
		OriginalPrice: prop.Price.OriginalPrice,
		PricePerSqft:  prop.Price.PricePerSqft,
		Beds:          prop.Details.Beds,
		Baths:         prop.Details.Baths,
		Sqft:          prop.Details.Sqft,
		LotSize:       prop.Details.LotSize,
		YearBuilt:     prop.Details.YearBuilt,
		PropertyType:  p.parsePropertyType(prop.Details.PropertyType),
		Status:        p.parseListingStatus(prop.Listing.Status),
		DaysOnMarket:  prop.Listing.DaysOnMarket,
		MLSNumber:     prop.Listing.MLSNumber,
		Images:        prop.Media.Photos,
		ListingURL:    prop.Listing.ListingURL,
		Description:   prop.Listing.Description,
		FetchedAt:     time.Now(),
		Source:        "HasData",
	}

	// Add estimates if available - these will be used or overridden by the enricher
	// The enricher will calculate proper cap rate with expenses if rent is available
	if prop.Estimates.RentEstimate > 0 {
		property.EstimatedRent = prop.Estimates.RentEstimate
	}

	// Calculate price change
	if prop.Price.OriginalPrice > 0 {
		property.PriceChange = prop.Price.ListPrice - prop.Price.OriginalPrice
	}

	// Parse listed date
	if prop.Listing.ListedDate != "" {
		if t, err := time.Parse("2006-01-02", prop.Listing.ListedDate); err == nil {
			property.ListedDate = t
		}
	}

	// Note: Investment metrics (capRate, cashOnCash, etc.) are calculated by
	// the InvestmentMetricsEnricher after all properties are fetched.
	// This ensures consistent calculation logic across all providers.

	return property
}

// mapPropertyType maps standard property type to HasData format
// HasData uses: house, townhome, condo, multiFamily, lot, apartment, manufactured
func (p *HasDataProvider) mapPropertyType(pt PropertyType) string {
	switch pt {
	case PropertyTypeSingleFamily:
		return "house"
	case PropertyTypeCondo:
		return "condo"
	case PropertyTypeTownhouse:
		return "townhome"
	case PropertyTypeMultiFamily:
		return "multiFamily"
	case PropertyTypeLand:
		return "lot"
	default:
		return ""
	}
}

// mapListingStatus maps standard listing status to HasData format
func (p *HasDataProvider) mapListingStatus(status ListingStatus) string {
	switch status {
	case ListingStatusActive:
		return "active"
	case ListingStatusPending:
		return "pending"
	case ListingStatusSold:
		return "sold"
	case ListingStatusContingent:
		return "contingent"
	default:
		return "active"
	}
}

// parsePropertyType converts HasData property type to standard format
func (p *HasDataProvider) parsePropertyType(s string) PropertyType {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "single") || strings.Contains(s, "house"):
		return PropertyTypeSingleFamily
	case strings.Contains(s, "condo"):
		return PropertyTypeCondo
	case strings.Contains(s, "town"):
		return PropertyTypeTownhouse
	case strings.Contains(s, "multi") || strings.Contains(s, "duplex"):
		return PropertyTypeMultiFamily
	case strings.Contains(s, "land"):
		return PropertyTypeLand
	default:
		return PropertyTypeOther
	}
}

// parseListingStatus converts HasData status to standard format
func (p *HasDataProvider) parseListingStatus(s string) ListingStatus {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "active"):
		return ListingStatusActive
	case strings.Contains(s, "pending"):
		return ListingStatusPending
	case strings.Contains(s, "sold"):
		return ListingStatusSold
	case strings.Contains(s, "contingent"):
		return ListingStatusContingent
	default:
		return ListingStatusActive
	}
}
