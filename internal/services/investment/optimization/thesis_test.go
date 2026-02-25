package optimization

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
)

func TestBuildScoringPrompt_Phase2(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{logger: logger}

	properties := []investment.Property{
		{
			ID:            "prop_123",
			Address:       "123 Main St",
			City:          "Austin",
			State:         "TX",
			Price:         300000,
			Beds:          3,
			Baths:         2.0,
			Sqft:          1500,
			EstimatedRent: 2400,
			DaysOnMarket:  15,
		},
	}

	profile := investment.InvestorProfile{
		Strategy:          investment.StrategyCashFlow,
		RiskTolerance:     investment.RiskModerate,
		AvailableCapital:  500000,
		InvestmentHorizon: "10 years",
	}

	prompt := svc.buildScoringPrompt(properties, profile, nil)

	// Verify prompt includes thesis-specific instructions
	tests := []struct {
		name     string
		contains string
	}{
		{"includes strategy", string(investment.StrategyCashFlow)},
		{"includes risk tolerance", string(investment.RiskModerate)},
		{"includes investmentThesis field", "investmentThesis"},
		{"includes keyStrengths field", "keyStrengths"},
		{"includes keyRisks field", "keyRisks"},
		{"includes marketContext field", "marketContext"},
		{"includes capexAlert field", "capexAlert"},
		{"includes property ID", "prop_123"},
		{"includes city", "Austin"},
		{"includes buyabilityScore field", "buyabilityScore"}, // Structured scoring field in prompt
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.HasPrefix(tt.contains, "!") {
				// Check for absence
				searchTerm := strings.TrimPrefix(tt.contains, "!")
				if strings.Contains(prompt, searchTerm) {
					t.Errorf("Prompt should NOT contain %q but does", searchTerm)
				}
			} else {
				// Check for presence
				if !strings.Contains(prompt, tt.contains) {
					t.Errorf("Prompt should contain %q but doesn't", tt.contains)
				}
			}
		})
	}
}

func TestParseScoringResponse_ValidThesis(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{logger: logger}

	properties := []investment.Property{
		{
			ID:      "prop_123",
			Address: "123 Main St",
			City:    "Austin",
			State:   "TX",
			Price:   300000,
		},
	}

	// Simulate AI response with thesis structure
	response := `[
  {
    "propertyId": "prop_123",
    "investmentThesis": "Strong cash flow opportunity in stable market with consistent rental demand.",
    "keyStrengths": [
      "Below-market price at $300K vs market median $350K",
      "High rental demand with 95% occupancy rate in area"
    ],
    "keyRisks": [
      "Older property may require $15-20K in deferred maintenance",
      "Moderate appreciation volatility requires longer hold period"
    ],
    "marketContext": "Growing market with stable employment and consistent rent growth.",
    "capexAlert": null
  }
]`

	scored, err := svc.parseScoringResponse(response, properties)
	if err != nil {
		t.Fatalf("parseScoringResponse() error = %v", err)
	}

	if len(scored) != 1 {
		t.Fatalf("Expected 1 scored property, got %d", len(scored))
	}

	result := scored[0]

	// Verify thesis was extracted
	if result.Thesis == nil {
		t.Fatal("Thesis should not be nil")
	}

	if result.Thesis.InvestmentThesis != "Strong cash flow opportunity in stable market with consistent rental demand." {
		t.Errorf("InvestmentThesis = %q, want %q", result.Thesis.InvestmentThesis, "Strong cash flow opportunity in stable market with consistent rental demand.")
	}

	if len(result.Thesis.KeyStrengths) != 2 {
		t.Errorf("KeyStrengths length = %d, want 2", len(result.Thesis.KeyStrengths))
	}

	if len(result.Thesis.KeyRisks) != 2 {
		t.Errorf("KeyRisks length = %d, want 2", len(result.Thesis.KeyRisks))
	}

	if result.Thesis.MarketContext != "Growing market with stable employment and consistent rent growth." {
		t.Errorf("MarketContext = %q, want %q", result.Thesis.MarketContext, "Growing market with stable employment and consistent rent growth.")
	}

	if result.Thesis.CapexAlert != nil {
		t.Errorf("CapexAlert should be nil, got %q", *result.Thesis.CapexAlert)
	}
}

func TestParseScoringResponse_WithCapexAlert(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{logger: logger}

	properties := []investment.Property{
		{ID: "prop_456"},
	}

	response := `[
  {
    "propertyId": "prop_456",
    "investmentThesis": "Value-add opportunity with deferred maintenance.",
    "keyStrengths": ["Good location", "Strong rental demand"],
    "keyRisks": ["Deferred maintenance", "Older systems"],
    "marketContext": "Stable market",
    "capexAlert": "Expect $20-25K in roof and HVAC repairs within 2 years"
  }
]`

	scored, err := svc.parseScoringResponse(response, properties)
	if err != nil {
		t.Fatalf("parseScoringResponse() error = %v", err)
	}

	if scored[0].Thesis == nil {
		t.Fatal("Thesis should not be nil")
	}

	if scored[0].Thesis.CapexAlert == nil {
		t.Fatal("CapexAlert should not be nil")
	}

	expectedAlert := "Expect $20-25K in roof and HVAC repairs within 2 years"
	if *scored[0].Thesis.CapexAlert != expectedAlert {
		t.Errorf("CapexAlert = %q, want %q", *scored[0].Thesis.CapexAlert, expectedAlert)
	}
}

func TestDeriveRecommendationFromThesis(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{logger: logger}

	tests := []struct {
		name         string
		thesis       *investment.PropertyThesis
		explicitRec  string
		expectedRec  investment.Recommendation
	}{
		{
			name:        "valid thesis with no explicit recommendation",
			thesis:      &investment.PropertyThesis{InvestmentThesis: "Good opportunity"},
			explicitRec: "",
			expectedRec: investment.RecommendationBuy,
		},
		{
			name:        "nil thesis",
			thesis:      nil,
			explicitRec: "",
			expectedRec: investment.RecommendationHold,
		},
		{
			name:        "empty thesis",
			thesis:      &investment.PropertyThesis{InvestmentThesis: ""},
			explicitRec: "",
			expectedRec: investment.RecommendationHold,
		},
		{
			name:        "explicit STRONG_BUY overrides",
			thesis:      &investment.PropertyThesis{InvestmentThesis: "Excellent"},
			explicitRec: "STRONG_BUY",
			expectedRec: investment.RecommendationStrongBuy,
		},
		{
			name:        "explicit PASS overrides",
			thesis:      &investment.PropertyThesis{InvestmentThesis: "Good"},
			explicitRec: "PASS",
			expectedRec: investment.RecommendationPass,
		},
		{
			name:        "invalid explicit recommendation ignored",
			thesis:      &investment.PropertyThesis{InvestmentThesis: "OK opportunity"},
			explicitRec: "INVALID_REC",
			expectedRec: investment.RecommendationBuy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.deriveRecommendationFromThesis(tt.thesis, tt.explicitRec)
			if result != tt.expectedRec {
				t.Errorf("deriveRecommendationFromThesis() = %v, want %v", result, tt.expectedRec)
			}
		})
	}
}

func TestGenerateFallbackThesis(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{logger: logger}

	tests := []struct {
		name          string
		prop          investment.Property
		grossYield    float64
		pricePerSqft  float64
		strategy      investment.InvestmentStrategy
		expectCapex   bool
	}{
		{
			name: "strong rental yield",
			prop: investment.Property{
				ID:            "prop_1",
				City:          "Austin",
				State:         "TX",
				Price:         200000,
				Beds:          3,
				Baths:         2.0,
				Sqft:          1500,
				EstimatedRent: 2000,
				DaysOnMarket:  30,
				YearBuilt:     2010,
			},
			grossYield:   8.0,
			pricePerSqft: 133.33,
			strategy:     investment.StrategyCashFlow,
			expectCapex:  false,
		},
		{
			name: "older property with capex alert",
			prop: investment.Property{
				ID:            "prop_2",
				City:          "Dallas",
				State:         "TX",
				Price:         150000,
				Beds:          2,
				Baths:         1.0,
				Sqft:          1000,
				EstimatedRent: 1200,
				DaysOnMarket:  90,
				YearBuilt:     1975,
			},
			grossYield:   9.6,
			pricePerSqft: 150.0,
			strategy:     investment.StrategyCashFlow,
			expectCapex:  true, // Built before 1990
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thesis := svc.generateFallbackThesis(tt.prop, tt.grossYield, tt.pricePerSqft, tt.strategy)

			if thesis == nil {
				t.Fatal("thesis should not be nil")
			}

			// Verify required fields
			if thesis.InvestmentThesis == "" {
				t.Error("InvestmentThesis should not be empty")
			}

			if len(thesis.KeyStrengths) != 2 {
				t.Errorf("KeyStrengths length = %d, want 2", len(thesis.KeyStrengths))
			}

			if len(thesis.KeyRisks) != 2 {
				t.Errorf("KeyRisks length = %d, want 2", len(thesis.KeyRisks))
			}

			if thesis.MarketContext == "" {
				t.Error("MarketContext should not be empty")
			}

			// Verify CapEx alert presence/absence
			if tt.expectCapex && thesis.CapexAlert == nil {
				t.Error("Expected CapexAlert for older property, got nil")
			}

			if !tt.expectCapex && thesis.CapexAlert != nil {
				t.Errorf("Expected no CapexAlert for newer property, got %q", *thesis.CapexAlert)
			}
		})
	}
}
