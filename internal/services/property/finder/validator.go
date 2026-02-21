package finder

import (
	"github.com/estara-ai/www/internal/services/property/providers"
)

// PropertyValidator validates property data quality
type PropertyValidator struct {
}

// NewPropertyValidator creates a new property validator
func NewPropertyValidator() *PropertyValidator {
	return &PropertyValidator{}
}

// ValidationResult holds validation outcome
type ValidationResult struct {
	IsValid bool     `json:"isValid"`
	Reasons []string `json:"reasons,omitempty"`
}

// ValidateProperty validates a property's data quality
// Returns true if property passes all validation checks
func (v *PropertyValidator) ValidateProperty(property providers.Property) ValidationResult {
	reasons := []string{}

	// Price validation
	if property.Price <= 0 {
		reasons = append(reasons, "Invalid price: must be > 0")
	}

	// Market-aware price ranges
	if !v.isValidPriceForMarket(property) {
		reasons = append(reasons, "Price outside realistic market range")
	}

	// Baths validation: 0.5-20.0 (float bounds)
	// ADR-088 Phase 1: Fixed to allow 0.5 baths (half bath)
	if property.Baths < 0.5 || property.Baths > 20.0 {
		reasons = append(reasons, "Invalid baths: must be 0.5-20.0")
	}

	// Beds validation
	if property.Beds < 0 || property.Beds > 20 {
		reasons = append(reasons, "Invalid beds: must be 0-20")
	}

	// Square footage validation
	if property.Sqft > 0 && (property.Sqft < 200 || property.Sqft > 50000) {
		reasons = append(reasons, "Invalid square footage: must be 200-50,000")
	}

	// Year built validation
	if property.YearBuilt > 0 && (property.YearBuilt < 1800 || property.YearBuilt > 2030) {
		reasons = append(reasons, "Invalid year built: must be 1800-2030")
	}

	return ValidationResult{
		IsValid: len(reasons) == 0,
		Reasons: reasons,
	}
}

// isValidPriceForMarket checks if price is within realistic range for market
// Market-aware price ranges (ADR-088 Phase 1)
func (v *PropertyValidator) isValidPriceForMarket(property providers.Property) bool {
	price := property.Price

	// Determine market tier based on location (simplified)
	marketTier := v.getMarketTier(property)

	switch marketTier {
	case "tier1": // High-cost markets (SF, NYC, LA, Seattle, Boston)
		return price >= 150000 && price <= 10000000
	case "tier2": // Mid-cost markets (Austin, Denver, Miami, etc.)
		return price >= 75000 && price <= 5000000
	case "tier3": // Lower-cost markets (most other markets)
		return price >= 30000 && price <= 2000000
	default:
		// Default validation if market tier unknown
		return price >= 30000 && price <= 10000000
	}
}

// getMarketTier determines market tier based on location
// tier1: High-cost metros
// tier2: Mid-cost metros
// tier3: Lower-cost metros
func (v *PropertyValidator) getMarketTier(property providers.Property) string {
	city := property.City
	state := property.State

	// Tier 1: High-cost markets
	tier1Cities := map[string]bool{
		"San Francisco": true,
		"New York":      true,
		"Los Angeles":   true,
		"Seattle":       true,
		"Boston":        true,
		"San Diego":     true,
	}

	tier1States := map[string]bool{
		"CA": true, // California overall
		"NY": true, // New York overall
	}

	if tier1Cities[city] || tier1States[state] {
		return "tier1"
	}

	// Tier 2: Mid-cost markets
	tier2Cities := map[string]bool{
		"Austin":    true,
		"Denver":    true,
		"Miami":     true,
		"Portland":  true,
		"Nashville": true,
		"Charlotte": true,
		"Dallas":    true,
		"Phoenix":   true,
	}

	if tier2Cities[city] {
		return "tier2"
	}

	// Default: Tier 3 (lower-cost markets)
	return "tier3"
}

// FilterValidProperties filters a slice of properties, keeping only valid ones
func (v *PropertyValidator) FilterValidProperties(properties []providers.Property) []providers.Property {
	valid := make([]providers.Property, 0, len(properties))

	for _, prop := range properties {
		result := v.ValidateProperty(prop)
		if result.IsValid {
			valid = append(valid, prop)
		}
	}

	return valid
}
