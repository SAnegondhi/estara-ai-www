package providers

import (
	"context"
	"time"
)

// PropertyType represents types of properties
type PropertyType string

const (
	PropertyTypeSingleFamily PropertyType = "single_family"
	PropertyTypeCondo        PropertyType = "condo"
	PropertyTypeTownhouse    PropertyType = "townhouse"
	PropertyTypeMultiFamily  PropertyType = "multi_family"
	PropertyTypeLand         PropertyType = "land"
	PropertyTypeOther        PropertyType = "other"
)

// ListingStatus represents the status of a property listing
type ListingStatus string

const (
	ListingStatusActive      ListingStatus = "active"
	ListingStatusPending     ListingStatus = "pending"
	ListingStatusSold        ListingStatus = "sold"
	ListingStatusContingent  ListingStatus = "contingent"
	ListingStatusOffMarket   ListingStatus = "off_market"
)

// SearchParams defines parameters for property searches
type SearchParams struct {
	// Location
	City      string `json:"city"`
	State     string `json:"state"`
	ZipCode   string `json:"zipCode,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	Radius    float64 `json:"radius,omitempty"` // Miles

	// Price
	MinPrice int `json:"minPrice,omitempty"`
	MaxPrice int `json:"maxPrice,omitempty"`

	// Property characteristics
	MinBeds      int          `json:"minBeds,omitempty"`
	MaxBeds      int          `json:"maxBeds,omitempty"`
	MinBaths     float64      `json:"minBaths,omitempty"`
	MaxBaths     float64      `json:"maxBaths,omitempty"`
	MinSqft      int          `json:"minSqft,omitempty"`
	MaxSqft      int          `json:"maxSqft,omitempty"`
	PropertyType PropertyType `json:"propertyType,omitempty"`

	// Listing
	Status ListingStatus `json:"status,omitempty"`
	DaysOnMarket int     `json:"daysOnMarket,omitempty"`

	// Pagination
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`

	// Sorting
	SortBy    string `json:"sortBy,omitempty"`    // price, date, sqft
	SortOrder string `json:"sortOrder,omitempty"` // asc, desc
}

// Property represents a property listing
type Property struct {
	// Identifiers
	ID           string `json:"id"`
	ProviderID   string `json:"providerId"`
	ProviderName string `json:"providerName"`

	// Location
	Address    string  `json:"address"`
	City       string  `json:"city"`
	State      string  `json:"state"`
	ZipCode    string  `json:"zipCode"`
	County     string  `json:"county,omitempty"`
	Latitude   float64 `json:"latitude,omitempty"`
	Longitude  float64 `json:"longitude,omitempty"`

	// Pricing
	Price         int    `json:"price"`
	PricePerSqft  int    `json:"pricePerSqft,omitempty"`
	OriginalPrice int    `json:"originalPrice,omitempty"`
	PriceChange   int    `json:"priceChange,omitempty"`

	// Characteristics
	Beds         int          `json:"beds"`
	Baths        float64      `json:"baths"`
	Sqft         int          `json:"sqft"`
	LotSize      int          `json:"lotSize,omitempty"`
	YearBuilt    int          `json:"yearBuilt,omitempty"`
	PropertyType PropertyType `json:"propertyType"`

	// Listing Info
	Status        ListingStatus `json:"status"`
	ListedDate    time.Time     `json:"listedDate,omitempty"`
	DaysOnMarket  int           `json:"daysOnMarket,omitempty"`
	MLSNumber     string        `json:"mlsNumber,omitempty"`

	// Investment Metrics (may be estimated)
	EstimatedRent     int     `json:"estimatedRent,omitempty"`
	EstimatedCapRate  float64 `json:"estimatedCapRate,omitempty"`
	EstimatedCashFlow int     `json:"estimatedCashFlow,omitempty"`
	RentToPrice       float64 `json:"rentToPrice,omitempty"`
	GrossYield        float64 `json:"grossYield,omitempty"`
	NOI               int     `json:"noi,omitempty"`
	CashOnCash        float64 `json:"cashOnCash,omitempty"`

	// Age-based Investment Intelligence
	PropertyAge       int    `json:"propertyAge,omitempty"`
	AgeCategory       string `json:"ageCategory,omitempty"`       // new_construction, modern, established, mature, vintage
	MaintenanceRisk   string `json:"maintenanceRisk,omitempty"`   // low, moderate, high, very_high
	MaintenanceFactors []string `json:"maintenanceFactors,omitempty"`

	// Overall Investment Score (0-100)
	InvestmentScore   int    `json:"investmentScore,omitempty"`

	// ADR-088 Phase 1: Market Volatility Metrics (for Monte Carlo calibration)
	AppreciationStdDev  float64 `json:"appreciationStdDev,omitempty"` // Annual appreciation volatility (%)
	RentGrowthStdDev    float64 `json:"rentGrowthStdDev,omitempty"`   // Annual rent growth volatility (%)
	HistoricalPeakDrop  float64 `json:"historicalPeakDrop,omitempty"` // Worst 12mo return in 20yr window (%)
	MarketType          string  `json:"marketType,omitempty"`         // stable, moderate, volatile

	// Media
	Images      []string `json:"images,omitempty"`
	ListingURL  string   `json:"listingUrl,omitempty"`
	Description string   `json:"description,omitempty"`

	// Metadata
	FetchedAt time.Time `json:"fetchedAt"`
	Source    string    `json:"source"`
}

// SearchResult represents the result of a property search
type SearchResult struct {
	Properties []Property `json:"properties"`
	Total      int        `json:"total"`
	HasMore    bool       `json:"hasMore"`
	NextOffset int        `json:"nextOffset,omitempty"`
	Provider   string     `json:"provider"`
	SearchTime time.Duration `json:"searchTime"`
}

// Provider defines the interface for property data providers
type Provider interface {
	// Name returns the provider's identifier
	Name() string

	// Search performs a property search with the given parameters
	Search(ctx context.Context, params SearchParams) (*SearchResult, error)

	// GetProperty retrieves a single property by ID
	GetProperty(ctx context.Context, propertyID string) (*Property, error)

	// IsEnabled returns whether this provider is currently enabled
	IsEnabled() bool

	// Priority returns the provider's priority (lower = higher priority)
	Priority() int

	// HealthCheck verifies the provider is operational
	HealthCheck(ctx context.Context) error
}

// ProviderConfig holds configuration for a provider
type ProviderConfig struct {
	Name     string
	Enabled  bool
	Priority int
	APIKey   string
	BaseURL  string
	Timeout  time.Duration
	RateLimit int // Requests per minute
}
