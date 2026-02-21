package optimization

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
)

func TestGenerateContextHash(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cr := NewContextRescorer(logger)

	tests := []struct {
		name       string
		properties []investment.PropertyInPortfolio
		expectSame bool
	}{
		{
			name: "same properties same order produces same hash",
			properties: []investment.PropertyInPortfolio{
				{Property: investment.Property{ID: "prop_1"}},
				{Property: investment.Property{ID: "prop_2"}},
				{Property: investment.Property{ID: "prop_3"}},
			},
			expectSame: true,
		},
		{
			name: "same properties different order produces same hash",
			properties: []investment.PropertyInPortfolio{
				{Property: investment.Property{ID: "prop_3"}},
				{Property: investment.Property{ID: "prop_1"}},
				{Property: investment.Property{ID: "prop_2"}},
			},
			expectSame: true, // Should produce same hash (sorted before hashing)
		},
	}

	// Generate baseline hash from first test
	baselineHash := cr.generateContextHash(tests[0].properties)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := cr.generateContextHash(tt.properties)

			// Hash should be 64 characters (SHA-256 hex encoding)
			if len(hash) != 64 {
				t.Errorf("Hash length = %d, want 64", len(hash))
			}

			// Check if hash matches baseline (for same property set)
			if tt.expectSame {
				if hash != baselineHash {
					t.Errorf("Hash = %s, want %s (same properties should produce same hash)", hash, baselineHash)
				}
			}
		})
	}
}

func TestGenerateContextHash_DifferentProperties(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cr := NewContextRescorer(logger)

	props1 := []investment.PropertyInPortfolio{
		{Property: investment.Property{ID: "prop_1"}},
		{Property: investment.Property{ID: "prop_2"}},
	}

	props2 := []investment.PropertyInPortfolio{
		{Property: investment.Property{ID: "prop_1"}},
		{Property: investment.Property{ID: "prop_3"}}, // Different property
	}

	hash1 := cr.generateContextHash(props1)
	hash2 := cr.generateContextHash(props2)

	if hash1 == hash2 {
		t.Errorf("Different property sets should produce different hashes, got same: %s", hash1)
	}
}

func TestBuildPortfolioContext(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cr := NewContextRescorer(logger)

	// Create test frontier configuration
	config := &investment.FrontierPoint{
		ConfigIndex:         0,
		ExpectedReturn:      10.5,
		PortfolioVolatility: 4.2,
		ConcentrationIndex:  0.35,
		Properties: []investment.PropertyInPortfolio{
			{
				Property: investment.Property{
					ID:           "p1",
					City:         "Austin",
					State:        "TX",
					Price:        400000,
					PropertyType: "Single Family",
				},
				CapRate: 7.2,
			},
			{
				Property: investment.Property{
					ID:           "p2",
					City:         "Austin",
					State:        "TX",
					Price:        300000,
					PropertyType: "Single Family",
				},
				CapRate: 8.0,
			},
			{
				Property: investment.Property{
					ID:           "p3",
					City:         "Denver",
					State:        "CO",
					Price:        500000,
					PropertyType: "Multi-Family",
				},
				CapRate: 6.5,
			},
		},
	}

	portfolioContext := cr.buildPortfolioContext(config)

	// Verify basic metrics
	if portfolioContext.PropertyCount != 3 {
		t.Errorf("PropertyCount = %d, want 3", portfolioContext.PropertyCount)
	}

	if portfolioContext.TotalValue != 1200000 {
		t.Errorf("TotalValue = %d, want 1200000", portfolioContext.TotalValue)
	}

	// Verify average cap rate
	expectedAvgCapRate := (7.2 + 8.0 + 6.5) / 3.0
	if portfolioContext.AvgCapRate != expectedAvgCapRate {
		t.Errorf("AvgCapRate = %.2f, want %.2f", portfolioContext.AvgCapRate, expectedAvgCapRate)
	}

	// Verify market allocations
	if len(portfolioContext.Markets) != 2 {
		t.Errorf("Markets count = %d, want 2 (Austin, Denver)", len(portfolioContext.Markets))
	}

	// Austin should be first (higher allocation)
	if portfolioContext.Markets[0].Market != "Austin, TX" {
		t.Errorf("First market = %s, want Austin, TX", portfolioContext.Markets[0].Market)
	}

	if portfolioContext.Markets[0].Properties != 2 {
		t.Errorf("Austin properties = %d, want 2", portfolioContext.Markets[0].Properties)
	}

	if portfolioContext.Markets[0].Value != 700000 {
		t.Errorf("Austin value = %d, want 700000", portfolioContext.Markets[0].Value)
	}

	expectedAustinPct := 700000.0 / 1200000.0 * 100.0
	if portfolioContext.Markets[0].Percentage != expectedAustinPct {
		t.Errorf("Austin percentage = %.2f, want %.2f", portfolioContext.Markets[0].Percentage, expectedAustinPct)
	}

	// Verify property type allocations
	if len(portfolioContext.PropertyTypes) != 2 {
		t.Errorf("PropertyTypes count = %d, want 2 (Single Family, Multi-Family)", len(portfolioContext.PropertyTypes))
	}

	// Single Family should be first (higher allocation)
	if portfolioContext.PropertyTypes[0].Type != "Single Family" {
		t.Errorf("First type = %s, want Single Family", portfolioContext.PropertyTypes[0].Type)
	}

	if portfolioContext.PropertyTypes[0].Properties != 2 {
		t.Errorf("Single Family properties = %d, want 2", portfolioContext.PropertyTypes[0].Properties)
	}

	// Verify context hash is generated
	if portfolioContext.ContextHash == "" {
		t.Error("ContextHash should not be empty")
	}

	if len(portfolioContext.ContextHash) != 64 {
		t.Errorf("ContextHash length = %d, want 64", len(portfolioContext.ContextHash))
	}
}

func TestBuildContextAwarePrompt(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cr := NewContextRescorer(logger)

	property := investment.Property{
		ID:            "p1",
		Address:       "123 Main St",
		City:          "Austin",
		State:         "TX",
		Price:         400000,
		Beds:          3,
		Baths:         2.0,
		Sqft:          2000,
		EstimatedRent: 2500,
		DaysOnMarket:  15,
	}

	portfolioContext := &PortfolioContext{
		PropertyCount:       5,
		TotalValue:          2000000,
		AvgCapRate:          7.5,
		PortfolioVolatility: 4.2,
		AvgExpectedReturn:   10.5,
		Markets: []MarketAllocation{
			{Market: "Austin, TX", Properties: 2, Value: 800000, Percentage: 40.0},
			{Market: "Denver, CO", Properties: 3, Value: 1200000, Percentage: 60.0},
		},
		PropertyTypes: []PropertyTypeAlloc{
			{Type: "Single Family", Properties: 4, Value: 1600000, Percentage: 80.0},
			{Type: "Multi-Family", Properties: 1, Value: 400000, Percentage: 20.0},
		},
		ConcentrationIndex: 0.35,
		ContextHash:        "abc123",
	}

	profile := investment.InvestorProfile{
		Strategy:          investment.StrategyCashFlow,
		RiskTolerance:     investment.RiskModerate,
		InvestmentHorizon: "10 years",
	}

	prompt := cr.buildContextAwarePrompt(property, portfolioContext, profile)

	// Verify prompt contains key context elements
	tests := []struct {
		name     string
		contains string
	}{
		{"includes strategy", string(investment.StrategyCashFlow)},
		{"includes risk tolerance", string(investment.RiskModerate)},
		{"includes property count", "5-property portfolio"},
		{"includes total value", "$2000000"},
		{"includes avg cap rate", "7.50%"},
		{"includes portfolio volatility", "4.20%"},
		{"includes expected return", "10.50%"},
		{"includes Austin market", "Austin, TX: 2 properties"},
		{"includes Denver market", "Denver, CO: 3 properties"},
		{"includes Single Family type", "Single Family: 4 properties"},
		{"includes property details", "123 Main St"},
		{"includes context task", "How this property fits within the existing portfolio"},
		{"includes diversification mention", "diversification"},
		{"includes portfolio allocation mention", "portfolio allocation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(prompt, tt.contains) {
				t.Errorf("Prompt should contain %q but doesn't", tt.contains)
			}
		})
	}

	// Verify JSON output format is specified
	if !strings.Contains(prompt, "investmentThesis") {
		t.Error("Prompt should specify investmentThesis field in output format")
	}

	if !strings.Contains(prompt, "keyStrengths") {
		t.Error("Prompt should specify keyStrengths field in output format")
	}

	if !strings.Contains(prompt, "keyRisks") {
		t.Error("Prompt should specify keyRisks field in output format")
	}
}

func TestCountPropertiesInMarket(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cr := NewContextRescorer(logger)

	property := investment.Property{
		City:  "Austin",
		State: "TX",
	}

	portfolioContext := &PortfolioContext{
		Markets: []MarketAllocation{
			{Market: "Austin, TX", Properties: 3},
			{Market: "Denver, CO", Properties: 2},
		},
	}

	count := cr.countPropertiesInMarket(property, portfolioContext)

	if count != 3 {
		t.Errorf("countPropertiesInMarket() = %d, want 3", count)
	}

	// Test property not in portfolio
	otherProperty := investment.Property{
		City:  "Seattle",
		State: "WA",
	}

	count = cr.countPropertiesInMarket(otherProperty, portfolioContext)

	if count != 1 {
		t.Errorf("countPropertiesInMarket() for unknown market = %d, want 1 (default)", count)
	}
}

func TestGetMarketAllocation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cr := NewContextRescorer(logger)

	property := investment.Property{
		City:  "Austin",
		State: "TX",
	}

	portfolioContext := &PortfolioContext{
		Markets: []MarketAllocation{
			{Market: "Austin, TX", Percentage: 40.0},
			{Market: "Denver, CO", Percentage: 60.0},
		},
	}

	allocation := cr.getMarketAllocation(property, portfolioContext)

	if allocation != 40.0 {
		t.Errorf("getMarketAllocation() = %.2f, want 40.0", allocation)
	}

	// Test property not in portfolio
	otherProperty := investment.Property{
		City:  "Seattle",
		State: "WA",
	}

	allocation = cr.getMarketAllocation(otherProperty, portfolioContext)

	if allocation != 0.0 {
		t.Errorf("getMarketAllocation() for unknown market = %.2f, want 0.0", allocation)
	}
}

func TestRescorePropertyWithContext_PlaceholderThesis(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cr := NewContextRescorer(logger)

	property := investment.Property{
		ID:    "p1",
		City:  "Austin",
		State: "TX",
		Price: 400000,
	}

	portfolioContext := &PortfolioContext{
		PropertyCount: 5,
		TotalValue:    2000000,
		Markets: []MarketAllocation{
			{Market: "Austin, TX", Properties: 2, Percentage: 40.0},
		},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	ctx := context.Background()
	thesis, err := cr.rescorePropertyWithContext(ctx, property, portfolioContext, profile)

	if err != nil {
		t.Fatalf("rescorePropertyWithContext() error = %v", err)
	}

	if thesis == nil {
		t.Fatal("thesis should not be nil")
	}

	// Verify placeholder thesis has required fields
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

	// Verify thesis mentions portfolio context
	if !strings.Contains(thesis.InvestmentThesis, "portfolio") {
		t.Error("InvestmentThesis should mention portfolio")
	}

	if !strings.Contains(thesis.MarketContext, "Austin") {
		t.Error("MarketContext should mention Austin")
	}

	// Verify strengths mention diversification
	foundDiversification := false
	for _, strength := range thesis.KeyStrengths {
		if strings.Contains(strings.ToLower(strength), "diversif") {
			foundDiversification = true
			break
		}
	}

	if !foundDiversification {
		t.Error("KeyStrengths should mention diversification")
	}
}

func TestRescoreConfiguration_Integration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cr := NewContextRescorer(logger)

	config := &investment.FrontierPoint{
		ConfigIndex:         0,
		ExpectedReturn:      10.5,
		PortfolioVolatility: 4.2,
		ConcentrationIndex:  0.35,
		Properties: []investment.PropertyInPortfolio{
			{
				Property: investment.Property{
					ID:    "p1",
					City:  "Austin",
					State: "TX",
					Price: 400000,
				},
				CapRate: 7.2,
			},
			{
				Property: investment.Property{
					ID:    "p2",
					City:  "Denver",
					State: "CO",
					Price: 500000,
				},
				CapRate: 6.5,
			},
		},
	}

	profile := investment.InvestorProfile{
		Strategy:          investment.StrategyCashFlow,
		RiskTolerance:     investment.RiskModerate,
		InvestmentHorizon: "10 years",
	}

	ctx := context.Background()
	rescored, err := cr.RescoreConfiguration(ctx, config, profile)

	if err != nil {
		t.Fatalf("RescoreConfiguration() error = %v", err)
	}

	if rescored == nil {
		t.Fatal("rescored configuration should not be nil")
	}

	// Verify configuration is preserved
	if rescored.ConfigIndex != config.ConfigIndex {
		t.Errorf("ConfigIndex = %d, want %d", rescored.ConfigIndex, config.ConfigIndex)
	}

	if len(rescored.Properties) != len(config.Properties) {
		t.Errorf("Properties count = %d, want %d", len(rescored.Properties), len(config.Properties))
	}

	// TODO Phase 4: Verify theses were updated when thesis field is added to PropertyInPortfolio
}
