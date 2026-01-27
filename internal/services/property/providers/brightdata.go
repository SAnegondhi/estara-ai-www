package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	brightDataDefaultTimeout = 60 * time.Second
	brightDataDefaultLimit   = 50
)

// BrightDataProvider implements the Provider interface for BrightData API
type BrightDataProvider struct {
	client   *http.Client
	apiKey   string
	baseURL  string
	enabled  bool
	priority int
	logger   *slog.Logger
}

// BrightDataConfig holds configuration for BrightData provider
type BrightDataConfig struct {
	APIKey   string
	BaseURL  string
	Enabled  bool
	Priority int
	Timeout  time.Duration
}

// brightDataRequest represents a request to the BrightData API
type brightDataRequest struct {
	URL         string `json:"url,omitempty"`
	Location    string `json:"location,omitempty"`
	ListingType string `json:"listing_type,omitempty"`
	MinPrice    int    `json:"min_price,omitempty"`
	MaxPrice    int    `json:"max_price,omitempty"`
	MinBeds     int    `json:"min_beds,omitempty"`
	MaxBeds     int    `json:"max_beds,omitempty"`
	MinBaths    int    `json:"min_baths,omitempty"`
	MaxBaths    int    `json:"max_baths,omitempty"`
	PropertyType string `json:"property_type,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// brightDataResponse represents a response from the BrightData API
type brightDataResponse struct {
	Status  string               `json:"status"`
	Data    []brightDataProperty `json:"data"`
	Total   int                  `json:"total"`
	Message string               `json:"message,omitempty"`
}

// brightDataProperty represents a property from BrightData
type brightDataProperty struct {
	ID             string   `json:"id"`
	Address        string   `json:"address"`
	City           string   `json:"city"`
	State          string   `json:"state"`
	ZipCode        string   `json:"zip_code"`
	Price          int      `json:"price"`
	OriginalPrice  int      `json:"original_price"`
	Beds           int      `json:"beds"`
	Baths          float64  `json:"baths"`
	Sqft           int      `json:"sqft"`
	LotSize        int      `json:"lot_size"`
	YearBuilt      int      `json:"year_built"`
	PropertyType   string   `json:"property_type"`
	Status         string   `json:"status"`
	DaysOnMarket   int      `json:"days_on_market"`
	Latitude       float64  `json:"latitude"`
	Longitude      float64  `json:"longitude"`
	ListingURL     string   `json:"listing_url"`
	Images         []string `json:"images"`
	Description    string   `json:"description"`
	MLSNumber      string   `json:"mls_number"`
	EstimatedRent  int      `json:"estimated_rent"`
	ListedDate     string   `json:"listed_date"`
}

// NewBrightDataProvider creates a new BrightData provider
func NewBrightDataProvider(cfg BrightDataConfig) *BrightDataProvider {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = brightDataDefaultTimeout
	}

	return &BrightDataProvider{
		client: &http.Client{
			Timeout: timeout,
		},
		apiKey:   cfg.APIKey,
		baseURL:  cfg.BaseURL,
		enabled:  cfg.Enabled,
		priority: cfg.Priority,
		logger:   slog.Default().With("component", "brightdata_provider"),
	}
}

// Name returns the provider's identifier
func (p *BrightDataProvider) Name() string {
	return "brightdata"
}

// IsEnabled returns whether this provider is currently enabled
func (p *BrightDataProvider) IsEnabled() bool {
	return p.enabled && p.apiKey != ""
}

// Priority returns the provider's priority
func (p *BrightDataProvider) Priority() int {
	return p.priority
}

// Search performs a property search
func (p *BrightDataProvider) Search(ctx context.Context, params SearchParams) (*SearchResult, error) {
	startTime := time.Now()

	if !p.IsEnabled() {
		return nil, fmt.Errorf("BrightData provider is not enabled")
	}

	// Build request
	req := p.buildRequest(params)

	// Make API call
	properties, total, err := p.callAPI(ctx, req)
	if err != nil {
		return nil, err
	}

	// Convert to standard format
	result := &SearchResult{
		Properties: make([]Property, 0, len(properties)),
		Total:      total,
		HasMore:    len(properties) < total,
		Provider:   p.Name(),
		SearchTime: time.Since(startTime),
	}

	for _, prop := range properties {
		result.Properties = append(result.Properties, p.convertProperty(prop))
	}

	if result.HasMore && len(properties) > 0 {
		result.NextOffset = params.Offset + len(properties)
	}

	p.logger.Info("BrightData search completed",
		"location", fmt.Sprintf("%s, %s", params.City, params.State),
		"results", len(result.Properties),
		"total", total,
		"duration", result.SearchTime,
	)

	return result, nil
}

// GetProperty retrieves a single property by ID
func (p *BrightDataProvider) GetProperty(ctx context.Context, propertyID string) (*Property, error) {
	if !p.IsEnabled() {
		return nil, fmt.Errorf("BrightData provider is not enabled")
	}

	// BrightData typically uses listing URLs for single property lookup
	// This would need the specific API endpoint for single property retrieval
	return nil, fmt.Errorf("single property lookup not implemented for BrightData")
}

// HealthCheck verifies the provider is operational
func (p *BrightDataProvider) HealthCheck(ctx context.Context) error {
	if !p.IsEnabled() {
		return fmt.Errorf("BrightData provider is disabled")
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

// buildRequest converts SearchParams to BrightData API request format
func (p *BrightDataProvider) buildRequest(params SearchParams) brightDataRequest {
	limit := params.Limit
	if limit == 0 {
		limit = brightDataDefaultLimit
	}

	location := params.City
	if params.State != "" {
		location = fmt.Sprintf("%s, %s", params.City, params.State)
	}
	if params.ZipCode != "" {
		location = params.ZipCode
	}

	req := brightDataRequest{
		Location:    location,
		ListingType: "for_sale",
		Limit:       limit,
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
	if params.MaxBaths > 0 {
		req.MaxBaths = int(params.MaxBaths)
	}

	if params.PropertyType != "" {
		req.PropertyType = p.mapPropertyType(params.PropertyType)
	}

	return req
}

// callAPI makes the HTTP request to BrightData
func (p *BrightDataProvider) callAPI(ctx context.Context, req brightDataRequest) ([]brightDataProperty, int, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		p.logger.Error("BrightData API error",
			"status", resp.StatusCode,
			"body", string(respBody),
		)
		return nil, 0, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	var apiResp brightDataResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if apiResp.Status != "success" && apiResp.Status != "" {
		return nil, 0, fmt.Errorf("API returned error: %s", apiResp.Message)
	}

	return apiResp.Data, apiResp.Total, nil
}

// convertProperty converts BrightData property to standard format
func (p *BrightDataProvider) convertProperty(prop brightDataProperty) Property {
	property := Property{
		ID:            prop.ID,
		ProviderID:    prop.ID,
		ProviderName:  p.Name(),
		Address:       prop.Address,
		City:          prop.City,
		State:         prop.State,
		ZipCode:       prop.ZipCode,
		Latitude:      prop.Latitude,
		Longitude:     prop.Longitude,
		Price:         prop.Price,
		OriginalPrice: prop.OriginalPrice,
		Beds:          prop.Beds,
		Baths:         prop.Baths,
		Sqft:          prop.Sqft,
		LotSize:       prop.LotSize,
		YearBuilt:     prop.YearBuilt,
		PropertyType:  p.parsePropertyType(prop.PropertyType),
		Status:        p.parseListingStatus(prop.Status),
		DaysOnMarket:  prop.DaysOnMarket,
		MLSNumber:     prop.MLSNumber,
		EstimatedRent: prop.EstimatedRent,
		Images:        prop.Images,
		ListingURL:    prop.ListingURL,
		Description:   prop.Description,
		FetchedAt:     time.Now(),
		Source:        "BrightData",
	}

	// Calculate price per sqft
	if prop.Sqft > 0 {
		property.PricePerSqft = prop.Price / prop.Sqft
	}

	// Calculate price change
	if prop.OriginalPrice > 0 {
		property.PriceChange = prop.Price - prop.OriginalPrice
	}

	// Parse listed date
	if prop.ListedDate != "" {
		if t, err := time.Parse("2006-01-02", prop.ListedDate); err == nil {
			property.ListedDate = t
		}
	}

	// Note: Investment metrics (capRate, cashOnCash, etc.) are calculated by
	// the InvestmentMetricsEnricher after all properties are fetched.
	// This ensures consistent calculation logic across all providers.

	return property
}

// mapPropertyType maps standard property type to BrightData format
func (p *BrightDataProvider) mapPropertyType(pt PropertyType) string {
	switch pt {
	case PropertyTypeSingleFamily:
		return "single_family"
	case PropertyTypeCondo:
		return "condo"
	case PropertyTypeTownhouse:
		return "townhouse"
	case PropertyTypeMultiFamily:
		return "multi_family"
	case PropertyTypeLand:
		return "land"
	default:
		return ""
	}
}

// parsePropertyType converts BrightData property type to standard format
func (p *BrightDataProvider) parsePropertyType(s string) PropertyType {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "single") || strings.Contains(s, "house"):
		return PropertyTypeSingleFamily
	case strings.Contains(s, "condo"):
		return PropertyTypeCondo
	case strings.Contains(s, "town"):
		return PropertyTypeTownhouse
	case strings.Contains(s, "multi") || strings.Contains(s, "duplex") || strings.Contains(s, "triplex"):
		return PropertyTypeMultiFamily
	case strings.Contains(s, "land") || strings.Contains(s, "lot"):
		return PropertyTypeLand
	default:
		return PropertyTypeOther
	}
}

// parseListingStatus converts BrightData status to standard format
func (p *BrightDataProvider) parseListingStatus(s string) ListingStatus {
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

// Helper to safely parse int from string
func parseInt(s string) int {
	if s == "" {
		return 0
	}
	// Remove non-numeric characters
	cleaned := ""
	for _, c := range s {
		if c >= '0' && c <= '9' {
			cleaned += string(c)
		}
	}
	if cleaned == "" {
		return 0
	}
	v, _ := strconv.Atoi(cleaned)
	return v
}
