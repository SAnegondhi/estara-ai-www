package finder

import (
	"testing"
	"time"

	"github.com/estara-ai/www/internal/services/property/providers"
)

func TestEnrichProperty_BasicMetrics(t *testing.T) {
	enricher := NewInvestmentMetricsEnricher()

	property := providers.Property{
		ID:        "test-1",
		Address:   "123 Main St",
		City:      "Austin",
		State:     "TX",
		Price:     300000,
		Beds:      3,
		Baths:     2,
		Sqft:      1500,
		YearBuilt: 2015,
	}

	enriched := enricher.EnrichProperty(property)

	// Check rent was estimated (Austin 3BR ~$2400 * 1.3 location multiplier)
	if enriched.EstimatedRent == 0 {
		t.Error("expected EstimatedRent to be set")
	}
	if enriched.EstimatedRent < 2000 || enriched.EstimatedRent > 4000 {
		t.Errorf("EstimatedRent %d is outside expected range", enriched.EstimatedRent)
	}

	// Check cap rate is calculated (should be positive for reasonable rent/price ratio)
	if enriched.EstimatedCapRate <= 0 {
		t.Error("expected EstimatedCapRate to be positive")
	}
	if enriched.EstimatedCapRate > 15 {
		t.Errorf("EstimatedCapRate %f seems too high", enriched.EstimatedCapRate)
	}

	// Check gross yield
	if enriched.GrossYield <= 0 {
		t.Error("expected GrossYield to be positive")
	}

	// Gross yield should be higher than cap rate (cap rate accounts for expenses)
	if enriched.GrossYield < enriched.EstimatedCapRate {
		t.Errorf("GrossYield %f should be higher than CapRate %f",
			enriched.GrossYield, enriched.EstimatedCapRate)
	}

	// Check NOI
	if enriched.NOI <= 0 {
		t.Error("expected NOI to be positive")
	}

	// Check price per sqft
	expectedPPS := 300000 / 1500 // 200
	if enriched.PricePerSqft != expectedPPS {
		t.Errorf("expected PricePerSqft %d, got %d", expectedPPS, enriched.PricePerSqft)
	}

	// Check age metrics
	currentYear := time.Now().Year()
	expectedAge := currentYear - 2015
	if enriched.PropertyAge != expectedAge {
		t.Errorf("expected PropertyAge %d, got %d", expectedAge, enriched.PropertyAge)
	}
	if enriched.AgeCategory != "modern" {
		t.Errorf("expected AgeCategory 'modern', got %s", enriched.AgeCategory)
	}
	if enriched.MaintenanceRisk != "moderate" {
		t.Errorf("expected MaintenanceRisk 'moderate', got %s", enriched.MaintenanceRisk)
	}

	// Check investment score
	if enriched.InvestmentScore == 0 {
		t.Error("expected InvestmentScore to be set")
	}
}

func TestEnrichProperty_WithExistingRent(t *testing.T) {
	enricher := NewInvestmentMetricsEnricher()

	property := providers.Property{
		ID:            "test-2",
		Price:         250000,
		Beds:          2,
		Baths:         1,
		Sqft:          1000,
		YearBuilt:     2000,
		EstimatedRent: 1800, // Pre-existing rent estimate from API
	}

	enriched := enricher.EnrichProperty(property)

	// Should use the existing rent estimate
	if enriched.EstimatedRent != 1800 {
		t.Errorf("expected to use existing rent 1800, got %d", enriched.EstimatedRent)
	}
}

func TestEnrichProperty_OldProperty(t *testing.T) {
	enricher := NewInvestmentMetricsEnricher()

	property := providers.Property{
		ID:        "test-3",
		Price:     200000,
		Beds:      3,
		Baths:     1.5,
		Sqft:      1200,
		YearBuilt: 1960, // Old property
	}

	enriched := enricher.EnrichProperty(property)

	// Old properties should have high maintenance risk
	if enriched.AgeCategory != "vintage" {
		t.Errorf("expected AgeCategory 'vintage' for 1960 build, got %s", enriched.AgeCategory)
	}
	if enriched.MaintenanceRisk != "very_high" {
		t.Errorf("expected MaintenanceRisk 'very_high', got %s", enriched.MaintenanceRisk)
	}
	if len(enriched.MaintenanceFactors) == 0 {
		t.Error("expected MaintenanceFactors to be set for old property")
	}

	// Old properties have higher maintenance expenses, so cap rate should be lower
	// than gross yield by a larger margin than newer properties
}

func TestEnrichProperty_NewConstruction(t *testing.T) {
	enricher := NewInvestmentMetricsEnricher()

	currentYear := time.Now().Year()
	property := providers.Property{
		ID:        "test-4",
		Price:     400000,
		Beds:      4,
		Baths:     3,
		Sqft:      2500,
		YearBuilt: currentYear - 2, // 2 years old
	}

	enriched := enricher.EnrichProperty(property)

	// New construction should have low maintenance risk
	if enriched.AgeCategory != "new_construction" {
		t.Errorf("expected AgeCategory 'new_construction', got %s", enriched.AgeCategory)
	}
	if enriched.MaintenanceRisk != "low" {
		t.Errorf("expected MaintenanceRisk 'low', got %s", enriched.MaintenanceRisk)
	}
}

func TestEnrichProperty_NoYearBuilt(t *testing.T) {
	enricher := NewInvestmentMetricsEnricher()

	property := providers.Property{
		ID:        "test-5",
		Price:     300000,
		Beds:      3,
		Baths:     2,
		Sqft:      1500,
		YearBuilt: 0, // Unknown year
	}

	enriched := enricher.EnrichProperty(property)

	// Should default to conservative values
	if enriched.AgeCategory != "established" {
		t.Errorf("expected AgeCategory 'established' for unknown year, got %s", enriched.AgeCategory)
	}
	if enriched.MaintenanceRisk != "moderate" {
		t.Errorf("expected MaintenanceRisk 'moderate' for unknown year, got %s", enriched.MaintenanceRisk)
	}
}

func TestEnrichProperty_NoPrice(t *testing.T) {
	enricher := NewInvestmentMetricsEnricher()

	property := providers.Property{
		ID:        "test-6",
		Beds:      3,
		Baths:     2,
		Price:     0, // No price
		YearBuilt: 2010,
	}

	enriched := enricher.EnrichProperty(property)

	// Should skip enrichment for properties without price
	if enriched.EstimatedCapRate != 0 {
		t.Error("expected no cap rate calculation for property without price")
	}
}

func TestEnrichProperties_Batch(t *testing.T) {
	enricher := NewInvestmentMetricsEnricher()

	properties := []providers.Property{
		{ID: "1", Price: 200000, Beds: 2, Sqft: 1000},
		{ID: "2", Price: 300000, Beds: 3, Sqft: 1500},
		{ID: "3", Price: 400000, Beds: 4, Sqft: 2000},
	}

	enriched := enricher.EnrichProperties(properties)

	if len(enriched) != 3 {
		t.Errorf("expected 3 enriched properties, got %d", len(enriched))
	}

	for i, p := range enriched {
		if p.EstimatedRent == 0 {
			t.Errorf("property %d: expected EstimatedRent to be set", i)
		}
		if p.EstimatedCapRate == 0 {
			t.Errorf("property %d: expected EstimatedCapRate to be set", i)
		}
	}
}

func TestLocationRentMultipliers(t *testing.T) {
	enricher := NewInvestmentMetricsEnricher()

	// Test high-cost city
	sfProperty := providers.Property{
		ID:    "sf",
		City:  "San Francisco",
		Price: 500000,
		Beds:  2,
	}

	// Test low-cost city
	houstonProperty := providers.Property{
		ID:    "houston",
		City:  "Houston",
		Price: 500000,
		Beds:  2,
	}

	sfEnriched := enricher.EnrichProperty(sfProperty)
	houstonEnriched := enricher.EnrichProperty(houstonProperty)

	// SF should have higher rent than Houston for same beds
	if sfEnriched.EstimatedRent <= houstonEnriched.EstimatedRent {
		t.Errorf("SF rent %d should be higher than Houston rent %d",
			sfEnriched.EstimatedRent, houstonEnriched.EstimatedRent)
	}
}
