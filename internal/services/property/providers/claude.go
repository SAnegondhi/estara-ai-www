package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/services/ai/anthropic"
)

const (
	claudeDefaultModel     = "claude-sonnet-4-20250514"
	claudeDefaultMaxTokens = 4096
	claudeMaxWebSearches   = 5
)

// ClaudeWebSearchProvider implements the Provider interface using Claude's web_search tool
type ClaudeWebSearchProvider struct {
	client   *anthropic.Client
	enabled  bool
	priority int
	logger   *slog.Logger
}

// ClaudeWebSearchConfig holds configuration for Claude provider
type ClaudeWebSearchConfig struct {
	Client   *anthropic.Client
	Enabled  bool
	Priority int
}

// claudeProperty represents property data from Claude's response
type claudeProperty struct {
	Address       string   `json:"address"`
	City          string   `json:"city"`
	State         string   `json:"state"`
	ZipCode       string   `json:"zipCode,omitempty"`
	Price         int      `json:"price"`
	Bedrooms      int      `json:"bedrooms,omitempty"`
	Bathrooms     float64  `json:"bathrooms,omitempty"`
	SquareFeet    int      `json:"squareFeet,omitempty"`
	YearBuilt     int      `json:"yearBuilt,omitempty"`
	PropertyType  string   `json:"propertyType,omitempty"`
	Units         int      `json:"units,omitempty"`
	ListingURL    string   `json:"listingUrl,omitempty"`
	Source        string   `json:"source,omitempty"`
	AIComment     string   `json:"aiComment,omitempty"`
	RentEstimate  int      `json:"rentEstimate,omitempty"`
	NOI           int      `json:"noi,omitempty"`
	CapRate       float64  `json:"capRate,omitempty"`
	YieldEstimate float64  `json:"yieldEstimate,omitempty"`
}

// claudeMarketContext represents market benchmarks from Claude
type claudeMarketContext struct {
	AvgCapRates *struct {
		ClassA   float64 `json:"classA,omitempty"`
		ClassB   float64 `json:"classB,omitempty"`
		ClassC   float64 `json:"classC,omitempty"`
		ValueAdd string  `json:"valueAdd,omitempty"`
	} `json:"avgCapRates,omitempty"`
	VacancyRate  float64 `json:"vacancyRate,omitempty"`
	RentGrowth   float64 `json:"rentGrowth,omitempty"`
	PricePerSqFt int     `json:"pricePerSqFt,omitempty"`
}

// claudeSearchResponse represents the full response from Claude
type claudeSearchResponse struct {
	Properties          []claudeProperty `json:"properties"`
	MarketContext       *claudeMarketContext `json:"marketContext,omitempty"`
	RecommendedStrategy *struct {
		FocusAreas []string `json:"focusAreas,omitempty"`
		AvoidAreas []string `json:"avoidAreas,omitempty"`
		Reasoning  string   `json:"reasoning,omitempty"`
	} `json:"recommendedStrategy,omitempty"`
	MarketInsights string `json:"marketInsights,omitempty"`
}

// NewClaudeWebSearchProvider creates a new Claude provider
func NewClaudeWebSearchProvider(cfg ClaudeWebSearchConfig) *ClaudeWebSearchProvider {
	return &ClaudeWebSearchProvider{
		client:   cfg.Client,
		enabled:  cfg.Enabled,
		priority: cfg.Priority,
		logger:   slog.Default().With("component", "claude_provider"),
	}
}

// Name returns the provider's identifier
func (p *ClaudeWebSearchProvider) Name() string {
	return "claude"
}

// IsEnabled returns whether this provider is currently enabled
func (p *ClaudeWebSearchProvider) IsEnabled() bool {
	return p.enabled && p.client != nil && p.client.IsConfigured()
}

// Priority returns the provider's priority (lower = higher priority)
func (p *ClaudeWebSearchProvider) Priority() int {
	return p.priority
}

// HealthCheck verifies the provider is operational
func (p *ClaudeWebSearchProvider) HealthCheck(ctx context.Context) error {
	if !p.IsEnabled() {
		return fmt.Errorf("claude provider is not enabled or not configured")
	}
	return nil
}

// Search performs a property search using Claude's web_search tool
func (p *ClaudeWebSearchProvider) Search(ctx context.Context, params SearchParams) (*SearchResult, error) {
	startTime := time.Now()

	if !p.IsEnabled() {
		return nil, fmt.Errorf("claude provider is not enabled")
	}

	p.logger.Info("starting claude web search",
		"city", params.City,
		"state", params.State,
		"minPrice", params.MinPrice,
		"maxPrice", params.MaxPrice,
	)

	// Build the search prompt
	prompt := p.buildSearchPrompt(params)

	// Configure web_search tool
	webSearchTool := anthropic.WebSearchTool{
		Type:    "web_search_20250305",
		Name:    "web_search",
		MaxUses: claudeMaxWebSearches,
	}

	// Make the API call
	resp, err := p.client.CompleteWithTools(ctx, "", prompt, []interface{}{webSearchTool})
	if err != nil {
		p.logger.Error("claude API error", "error", err)
		return nil, fmt.Errorf("claude API error: %w", err)
	}

	// Parse the response
	properties, searchCount := p.parseResponse(resp)

	p.logger.Info("claude search completed",
		"propertiesFound", len(properties),
		"searchesUsed", searchCount,
		"duration", time.Since(startTime),
	)

	return &SearchResult{
		Properties: properties,
		Total:      len(properties),
		HasMore:    false,
		Provider:   "claude",
		SearchTime: time.Since(startTime),
	}, nil
}

// GetProperty retrieves a single property - not supported for web search
func (p *ClaudeWebSearchProvider) GetProperty(ctx context.Context, propertyID string) (*Property, error) {
	return nil, fmt.Errorf("single property lookup not supported for Claude web search provider")
}

// buildSearchPrompt creates the prompt for Claude
func (p *ClaudeWebSearchProvider) buildSearchPrompt(params SearchParams) string {
	var prompt strings.Builder

	prompt.WriteString(fmt.Sprintf("Search for real estate investment properties for sale with the following criteria:\n\nLocation: %s, %s", params.City, params.State))

	if params.MinPrice > 0 || params.MaxPrice > 0 {
		if params.MinPrice > 0 && params.MaxPrice > 0 {
			prompt.WriteString(fmt.Sprintf("\nPrice Range: $%d to $%d", params.MinPrice, params.MaxPrice))
		} else if params.MinPrice > 0 {
			prompt.WriteString(fmt.Sprintf("\nMinimum Price: $%d", params.MinPrice))
		} else {
			prompt.WriteString(fmt.Sprintf("\nMaximum Price: $%d", params.MaxPrice))
		}
	}

	if params.PropertyType != "" {
		prompt.WriteString(fmt.Sprintf("\nProperty Type: %s", params.PropertyType))
	}

	if params.MinBeds > 0 {
		prompt.WriteString(fmt.Sprintf("\nMinimum Bedrooms: %d", params.MinBeds))
	}

	if params.MinBaths > 0 {
		prompt.WriteString(fmt.Sprintf("\nMinimum Bathrooms: %.1f", params.MinBaths))
	}

	prompt.WriteString(fmt.Sprintf(`

YOU MUST USE web_search TO FIND ACTUAL PROPERTY LISTINGS. Search these queries IN ORDER:

1. "site:loopnet.com %s %s investment property for sale"
2. "site:crexi.com %s %s multifamily for sale"
3. "site:zillow.com %s %s house for sale %d to %d"
4. "site:redfin.com %s %s homes for sale"
5. "site:realtor.com %s %s property for sale"

I need ACTUAL LISTINGS with REAL STREET ADDRESSES from these sites. Look for:
- Property address (street number and name)
- Asking price
- Bedrooms/bathrooms
- Square footage
- Year built
- Listing URL

For EACH property you find, CALCULATE investment metrics:
- rentEstimate = estimated monthly rent based on bedrooms and local market
- noi = (rentEstimate × 12) × 0.60
- capRate = (noi / price) × 100
- yieldEstimate = ((rentEstimate × 12) / price) × 100

Return ALL properties you can find in this JSON format:

{
  "properties": [
    {
      "address": "1234 Oak Street",
      "city": "%s",
      "state": "%s",
      "zipCode": "12345",
      "price": 275000,
      "bedrooms": 3,
      "bathrooms": 2,
      "squareFeet": 1400,
      "yearBuilt": 1985,
      "propertyType": "house",
      "listingUrl": "https://www.zillow.com/...",
      "source": "Zillow",
      "rentEstimate": 1650,
      "noi": 11880,
      "capRate": 4.32,
      "yieldEstimate": 7.2,
      "aiComment": "Solid 3BR rental. Est. rent $1,650/mo. Cap rate 4.32%% beats market avg."
    }
  ],
  "marketContext": {
    "avgCapRates": { "classA": 4.5, "classB": 5.0, "classC": 5.5 },
    "vacancyRate": 5.0,
    "rentGrowth": 3.0,
    "pricePerSqFt": 180
  },
  "recommendedStrategy": {
    "focusAreas": ["Best neighborhoods for investment"],
    "avoidAreas": ["Areas to avoid"],
    "reasoning": "Investment strategy explanation"
  },
  "marketInsights": "Current market conditions summary"
}

CRITICAL REQUIREMENTS:
1. SEARCH AGGRESSIVELY - use ALL your web searches to find real listings
2. Each property MUST have: address (real street address), price (numeric), yearBuilt, listingUrl
3. CALCULATE rentEstimate, noi, capRate, yieldEstimate for EVERY property
4. Return WHATEVER properties you CAN find - even if only 1 or 2
5. If a listing has partial info, include it with best estimates
6. aiComment should explain the investment potential with specific numbers
7. ONLY include properties with ACTIVE listing status - actually on sale right now
8. EXCLUDE any properties that are: pending, contingent, under contract, sold, off-market`,
		params.City, params.State,
		params.City, params.State,
		params.City, params.State, params.MinPrice, params.MaxPrice,
		params.City, params.State,
		params.City, params.State,
		params.City, params.State,
	))

	return prompt.String()
}

// parseResponse extracts properties from Claude's response
func (p *ClaudeWebSearchProvider) parseResponse(resp *anthropic.MessageResponse) ([]Property, int) {
	var rawText strings.Builder
	searchCount := 0

	// Extract text and count web searches
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			rawText.WriteString(block.Text)
		case "server_tool_use":
			if block.Name == "web_search" {
				searchCount++
			}
		}
	}

	text := rawText.String()
	p.logger.Debug("parsing claude response",
		"textLength", len(text),
		"searchCount", searchCount,
	)

	// Try to extract JSON
	var claudeResp claudeSearchResponse

	// Find JSON block in response
	jsonPattern := regexp.MustCompile(`\{[\s\S]*"properties"[\s\S]*\}`)
	jsonMatch := jsonPattern.FindString(text)

	if jsonMatch != "" {
		if err := json.Unmarshal([]byte(jsonMatch), &claudeResp); err != nil {
			p.logger.Warn("failed to parse JSON from Claude response", "error", err)
			// Try fallback extraction
			claudeResp.Properties = p.extractPropertiesFallback(text)
		}
	} else {
		p.logger.Debug("no JSON block found, using fallback extraction")
		claudeResp.Properties = p.extractPropertiesFallback(text)
	}

	// Convert to Property format
	properties := make([]Property, 0, len(claudeResp.Properties))
	for i, cp := range claudeResp.Properties {
		if cp.Price <= 0 {
			continue // Skip invalid properties
		}

		prop := Property{
			ID:           fmt.Sprintf("claude-%d-%d", time.Now().Unix(), i),
			ProviderID:   fmt.Sprintf("claude-%d", i),
			ProviderName: "claude",
			Address:      cp.Address,
			City:         cp.City,
			State:        cp.State,
			ZipCode:      cp.ZipCode,
			Price:        cp.Price,
			Beds:         cp.Bedrooms,
			Baths:        cp.Bathrooms,
			Sqft:         cp.SquareFeet,
			YearBuilt:    cp.YearBuilt,
			PropertyType: p.mapPropertyType(cp.PropertyType),
			Status:       ListingStatusActive,
			ListingURL:   cp.ListingURL,
			Description:  cp.AIComment,
			Source:       "claude",
			FetchedAt:    time.Now(),
		}

		// Add investment metrics from Claude
		if cp.RentEstimate > 0 {
			prop.EstimatedRent = cp.RentEstimate
		}
		if cp.CapRate > 0 {
			prop.EstimatedCapRate = cp.CapRate
		}
		if cp.YieldEstimate > 0 {
			prop.GrossYield = cp.YieldEstimate
		}
		if cp.NOI > 0 {
			prop.NOI = cp.NOI
		}

		// Calculate price per sqft
		if cp.SquareFeet > 0 {
			prop.PricePerSqft = cp.Price / cp.SquareFeet
		}

		properties = append(properties, prop)
	}

	p.logger.Info("parsed claude properties",
		"total", len(claudeResp.Properties),
		"valid", len(properties),
	)

	return properties, searchCount
}

// extractPropertiesFallback attempts to extract properties from unstructured text
func (p *ClaudeWebSearchProvider) extractPropertiesFallback(text string) []claudeProperty {
	var properties []claudeProperty

	// Try to find address patterns
	addressPattern := regexp.MustCompile(`(\d+\s+[\w\s]+(?:St|Ave|Blvd|Dr|Ln|Rd|Way|Ct|Pl)\.?(?:\s+#?\d+)?)\s*[,\n]\s*(\w[\w\s]+),\s*([A-Z]{2})`)
	matches := addressPattern.FindAllStringSubmatch(text, -1)

	for _, match := range matches {
		if len(match) >= 4 {
			properties = append(properties, claudeProperty{
				Address: strings.TrimSpace(match[1]),
				City:    strings.TrimSpace(match[2]),
				State:   match[3],
				Source:  "claude-extraction",
			})
		}
	}

	p.logger.Debug("fallback extraction",
		"propertiesFound", len(properties),
	)

	return properties
}

// mapPropertyType converts property type strings to standard format
func (p *ClaudeWebSearchProvider) mapPropertyType(ptype string) PropertyType {
	if ptype == "" {
		return PropertyTypeSingleFamily
	}

	switch strings.ToLower(ptype) {
	case "house", "single-family", "single family":
		return PropertyTypeSingleFamily
	case "condo", "condominium":
		return PropertyTypeCondo
	case "townhouse", "townhome":
		return PropertyTypeTownhouse
	case "multifamily", "multi-family", "duplex", "triplex", "quadplex", "apartment":
		return PropertyTypeMultiFamily
	case "land", "lot":
		return PropertyTypeLand
	default:
		return PropertyTypeOther
	}
}
