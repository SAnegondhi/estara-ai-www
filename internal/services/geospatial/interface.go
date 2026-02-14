// Package geospatial defines the interface for comparable sales and rental data.
// A concrete provider will be wired later; until then, the stub returns ErrNotConfigured.
package geospatial

import (
	"context"
	"errors"
	"time"
)

// ErrNotConfigured is returned when no geo-spatial provider is wired.
var ErrNotConfigured = errors.New("geospatial: no provider configured")

// Service defines the interface for comparable sales and rental data lookups.
type Service interface {
	// GetComparableSales returns recent sales near a property.
	GetComparableSales(ctx context.Context, req ComparableSalesRequest) (*ComparableSalesResponse, error)
	// GetComparableRentals returns active/recent rentals near a property.
	GetComparableRentals(ctx context.Context, req ComparableRentalsRequest) (*ComparableRentalsResponse, error)
	// IsConfigured reports whether a real provider is available.
	IsConfigured() bool
}

// ComparableSalesRequest specifies filters for finding comparable sales.
type ComparableSalesRequest struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	ZipCode     string  `json:"zipCode"`
	RadiusMiles float64 `json:"radiusMiles"` // default 0.5
	PropertyType string `json:"propertyType"`
	Beds        int     `json:"beds"`
	MinSqft     int     `json:"minSqft"`
	MaxSqft     int     `json:"maxSqft"`
	MonthsBack  int     `json:"monthsBack"` // default 6
	Limit       int     `json:"limit"`      // default 5
}

// ComparableSale represents a single comparable sale.
type ComparableSale struct {
	Address      string    `json:"address"`
	City         string    `json:"city"`
	State        string    `json:"state"`
	SalePrice    int       `json:"salePrice"`
	PricePerSqft float64  `json:"pricePerSqft"`
	RentEstimate int       `json:"rentEstimate,omitempty"`
	Beds         int       `json:"beds"`
	Baths        float64   `json:"baths"`
	Sqft         int       `json:"sqft"`
	YearBuilt    int       `json:"yearBuilt,omitempty"`
	SaleDate     time.Time `json:"saleDate"`
	Distance     float64   `json:"distance"` // miles from subject
}

// SalesSummary aggregates comparable sale statistics.
type SalesSummary struct {
	MedianPrice    int     `json:"medianPrice"`
	MedianPPSF     float64 `json:"medianPPSF"`
	MinPrice       int     `json:"minPrice"`
	MaxPrice       int     `json:"maxPrice"`
	Count          int     `json:"count"`
}

// ComparableSalesResponse wraps the result of a comparable sales query.
type ComparableSalesResponse struct {
	Sales   []ComparableSale `json:"sales"`
	Summary SalesSummary     `json:"summary"`
}

// ComparableRentalsRequest specifies filters for finding comparable rentals.
type ComparableRentalsRequest struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	ZipCode     string  `json:"zipCode"`
	RadiusMiles float64 `json:"radiusMiles"`
	PropertyType string `json:"propertyType"`
	Beds        int     `json:"beds"`
	Limit       int     `json:"limit"`
}

// ComparableRental represents a single comparable rental listing.
type ComparableRental struct {
	Address     string  `json:"address"`
	MonthlyRent int     `json:"monthlyRent"`
	Beds        int     `json:"beds"`
	Baths       float64 `json:"baths"`
	Sqft        int     `json:"sqft"`
	Distance    float64 `json:"distance"` // miles from subject
}

// RentalSummary aggregates comparable rental statistics.
type RentalSummary struct {
	MedianRent int `json:"medianRent"`
	MinRent    int `json:"minRent"`
	MaxRent    int `json:"maxRent"`
	Count      int `json:"count"`
}

// ComparableRentalsResponse wraps the result of a comparable rentals query.
type ComparableRentalsResponse struct {
	Rentals []ComparableRental `json:"rentals"`
	Summary RentalSummary      `json:"summary"`
}
