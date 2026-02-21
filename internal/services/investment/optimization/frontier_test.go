package optimization

import (
	"context"
	"log/slog"
	"math"
	"os"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/projection"
)

func TestCalculatePortfolioVolatility_Markowitz(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil)

	tests := []struct {
		name               string
		properties         []investment.Property
		weights            []float64
		expectedVolatility float64 // Approximate expected volatility
		tolerance          float64
	}{
		{
			name: "single property portfolio",
			properties: []investment.Property{
				{ID: "p1", City: "Austin", Price: 300000},
			},
			weights:            []float64{1.0},
			expectedVolatility: 5.0, // Base volatility
			tolerance:          2.0,
		},
		{
			name: "two properties same market (high correlation)",
			properties: []investment.Property{
				{ID: "p1", City: "Austin", Price: 300000},
				{ID: "p2", City: "Austin", Price: 400000},
			},
			weights:            []float64{0.5, 0.5},
			expectedVolatility: 5.0, // Similar to single property due to high correlation
			tolerance:          2.0,
		},
		{
			name: "two properties different markets (low correlation)",
			properties: []investment.Property{
				{ID: "p1", City: "Austin", Price: 300000},
				{ID: "p2", City: "Denver", Price: 400000},
			},
			weights:            []float64{0.5, 0.5},
			expectedVolatility: 4.0, // Lower due to diversification
			tolerance:          2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := PortfolioConfiguration{
				Properties: tt.properties,
				Weights:    tt.weights,
			}

			volatility := fo.calculatePortfolioVolatility(&config)

			if math.Abs(volatility-tt.expectedVolatility) > tt.tolerance {
				t.Errorf("Volatility = %.2f, expected ~%.2f (tolerance %.2f)",
					volatility, tt.expectedVolatility, tt.tolerance)
			}

			// Volatility should always be positive
			if volatility < 0 {
				t.Errorf("Volatility should be positive, got %.2f", volatility)
			}
		})
	}
}

func TestDominates_ParetoDominance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil)

	tests := []struct {
		name     string
		configA  OptimizationObjectives
		configB  OptimizationObjectives
		expected bool
	}{
		{
			name: "A dominates B (better on all objectives)",
			configA: OptimizationObjectives{
				ExpectedReturn:      10.0, // Higher return (better)
				PortfolioVolatility: 4.0,  // Lower volatility (better)
				ConcentrationIndex:  0.3,  // Lower concentration (better)
				StressTestEquity:    500000, // Higher equity (better)
			},
			configB: OptimizationObjectives{
				ExpectedReturn:      8.0,
				PortfolioVolatility: 5.0,
				ConcentrationIndex:  0.5,
				StressTestEquity:    400000,
			},
			expected: true,
		},
		{
			name: "A does not dominate B (worse on one objective)",
			configA: OptimizationObjectives{
				ExpectedReturn:      10.0, // Higher return (better)
				PortfolioVolatility: 6.0,  // Higher volatility (worse!)
				ConcentrationIndex:  0.3,  // Lower concentration (better)
				StressTestEquity:    500000, // Higher equity (better)
			},
			configB: OptimizationObjectives{
				ExpectedReturn:      8.0,
				PortfolioVolatility: 5.0,
				ConcentrationIndex:  0.5,
				StressTestEquity:    400000,
			},
			expected: false,
		},
		{
			name: "A does not dominate B (equal on all objectives)",
			configA: OptimizationObjectives{
				ExpectedReturn:      10.0,
				PortfolioVolatility: 5.0,
				ConcentrationIndex:  0.4,
				StressTestEquity:    450000,
			},
			configB: OptimizationObjectives{
				ExpectedReturn:      10.0,
				PortfolioVolatility: 5.0,
				ConcentrationIndex:  0.4,
				StressTestEquity:    450000,
			},
			expected: false, // Must be strictly better on at least one objective
		},
		{
			name: "Trade-off scenario (better return, worse volatility)",
			configA: OptimizationObjectives{
				ExpectedReturn:      12.0, // Higher return
				PortfolioVolatility: 7.0,  // Higher volatility
				ConcentrationIndex:  0.4,
				StressTestEquity:    450000,
			},
			configB: OptimizationObjectives{
				ExpectedReturn:      10.0, // Lower return
				PortfolioVolatility: 5.0,  // Lower volatility
				ConcentrationIndex:  0.4,
				StressTestEquity:    450000,
			},
			expected: false, // Neither dominates (trade-off)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := PortfolioConfiguration{Objectives: tt.configA}
			b := PortfolioConfiguration{Objectives: tt.configB}

			result := fo.dominates(&a, &b)

			if result != tt.expected {
				t.Errorf("dominates() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGenerateFrontier_Integration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil)

	// Create 10 properties with varying characteristics
	properties := []investment.ScoredProperty{
		// High return, high volatility (SF)
		{Property: investment.Property{ID: "p1", City: "San Francisco", State: "CA", Price: 1000000, EstimatedRent: 4500, Latitude: 37.7749, Longitude: -122.4194}, OverallScore: 85},
		{Property: investment.Property{ID: "p2", City: "San Francisco", State: "CA", Price: 950000, EstimatedRent: 4200, Latitude: 37.7849, Longitude: -122.4094}, OverallScore: 82},

		// Moderate return, moderate volatility (Austin)
		{Property: investment.Property{ID: "p3", City: "Austin", State: "TX", Price: 400000, EstimatedRent: 2400, Latitude: 30.2672, Longitude: -97.7431}, OverallScore: 90},
		{Property: investment.Property{ID: "p4", City: "Austin", State: "TX", Price: 380000, EstimatedRent: 2300, Latitude: 30.2772, Longitude: -97.7331}, OverallScore: 88},
		{Property: investment.Property{ID: "p5", City: "Austin", State: "TX", Price: 420000, EstimatedRent: 2500, Latitude: 30.2572, Longitude: -97.7531}, OverallScore: 86},

		// Lower return, lower volatility (Kansas City)
		{Property: investment.Property{ID: "p6", City: "Kansas City", State: "MO", Price: 200000, EstimatedRent: 1500, Latitude: 39.0997, Longitude: -94.5786}, OverallScore: 78},
		{Property: investment.Property{ID: "p7", City: "Kansas City", State: "MO", Price: 210000, EstimatedRent: 1550, Latitude: 39.1097, Longitude: -94.5686}, OverallScore: 76},

		// Mixed markets
		{Property: investment.Property{ID: "p8", City: "Denver", State: "CO", Price: 500000, EstimatedRent: 2800, Latitude: 39.7392, Longitude: -104.9903}, OverallScore: 84},
		{Property: investment.Property{ID: "p9", City: "Seattle", State: "WA", Price: 700000, EstimatedRent: 3200, Latitude: 47.6062, Longitude: -122.3321}, OverallScore: 80},
		{Property: investment.Property{ID: "p10", City: "Portland", State: "OR", Price: 450000, EstimatedRent: 2600, Latitude: 45.5152, Longitude: -122.6784}, OverallScore: 83},
	}

	profile := investment.InvestorProfile{
		Strategy:          investment.StrategyCashFlow,
		RiskTolerance:     investment.RiskModerate,
		AvailableCapital:  1000000,
		InvestmentHorizon: "10 years",
	}

	ctx := context.Background()
	params := investment.InvestmentPlanningParams{
		YearlyBudgets: []investment.YearlyBudget{},
	}
	frontierPoints, err := fo.GenerateFrontier(ctx, properties, profile, params, nil)

	if err != nil {
		t.Fatalf("GenerateFrontier() error = %v", err)
	}

	// Verify frontier produces 3-5 configurations
	if len(frontierPoints) < 3 || len(frontierPoints) > 5 {
		t.Errorf("Frontier points = %d, want 3-5", len(frontierPoints))
	}

	// Verify each configuration has 5-8 properties
	for i, fp := range frontierPoints {
		if len(fp.Properties) < 5 || len(fp.Properties) > 8 {
			t.Errorf("Config %d has %d properties, want 5-8", i, len(fp.Properties))
		}

		// Verify objectives are calculated
		if fp.ExpectedReturn <= 0 {
			t.Errorf("Config %d ExpectedReturn = %.2f, should be > 0", i, fp.ExpectedReturn)
		}

		if fp.PortfolioVolatility <= 0 {
			t.Errorf("Config %d PortfolioVolatility = %.2f, should be > 0", i, fp.PortfolioVolatility)
		}

		if fp.ConcentrationIndex < 0 || fp.ConcentrationIndex > 1 {
			t.Errorf("Config %d ConcentrationIndex = %.2f, should be 0-1", i, fp.ConcentrationIndex)
		}

		if fp.StressTestEquity <= 0 {
			t.Errorf("Config %d StressTestEquity = %d, should be > 0", i, fp.StressTestEquity)
		}

		// Verify Sharpe score is calculated
		if fp.SharpeScore == 0 {
			t.Logf("Warning: Config %d SharpeScore = 0 (may be valid if return == risk-free rate)", i)
		}
	}

	// Verify frontier points are ranked by Sharpe ratio (descending)
	for i := 1; i < len(frontierPoints); i++ {
		if frontierPoints[i].SharpeScore > frontierPoints[i-1].SharpeScore {
			t.Errorf("Frontier points not sorted by Sharpe: fp[%d]=%.2f > fp[%d]=%.2f",
				i, frontierPoints[i].SharpeScore, i-1, frontierPoints[i-1].SharpeScore)
		}
	}

	t.Logf("Generated %d frontier points", len(frontierPoints))
	for i, fp := range frontierPoints {
		t.Logf("Config %d: Return=%.2f%%, Vol=%.2f%%, Sharpe=%.2f, Conc=%.2f, Stress=$%d, Props=%d",
			i, fp.ExpectedReturn, fp.PortfolioVolatility, fp.SharpeScore,
			fp.ConcentrationIndex, fp.StressTestEquity, len(fp.Properties))
	}
}

func TestCalculateMarketHHI(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil)

	tests := []struct {
		name        string
		properties  []investment.Property
		weights     []float64
		expectedHHI float64
		tolerance   float64
	}{
		{
			name: "single market (maximum concentration)",
			properties: []investment.Property{
				{ID: "p1", City: "Austin", State: "TX", Price: 300000},
				{ID: "p2", City: "Austin", State: "TX", Price: 300000},
			},
			weights:     []float64{0.5, 0.5},
			expectedHHI: 1.0, // All in one market = HHI of 1.0
			tolerance:   0.01,
		},
		{
			name: "two markets equal weight (minimum concentration)",
			properties: []investment.Property{
				{ID: "p1", City: "Austin", State: "TX", Price: 300000},
				{ID: "p2", City: "Denver", State: "CO", Price: 300000},
			},
			weights:     []float64{0.5, 0.5},
			expectedHHI: 0.5, // (0.5^2 + 0.5^2) = 0.5
			tolerance:   0.01,
		},
		{
			name: "three markets equal weight",
			properties: []investment.Property{
				{ID: "p1", City: "Austin", State: "TX", Price: 300000},
				{ID: "p2", City: "Denver", State: "CO", Price: 300000},
				{ID: "p3", City: "Seattle", State: "WA", Price: 300000},
			},
			weights:     []float64{1.0 / 3.0, 1.0 / 3.0, 1.0 / 3.0},
			expectedHHI: 0.333, // (1/3^2 + 1/3^2 + 1/3^2) = 1/3
			tolerance:   0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := PortfolioConfiguration{
				Properties: tt.properties,
				Weights:    tt.weights,
			}

			hhi := fo.calculateMarketHHI(&config)

			if math.Abs(hhi-tt.expectedHHI) > tt.tolerance {
				t.Errorf("MarketHHI = %.3f, want %.3f (tolerance %.3f)",
					hhi, tt.expectedHHI, tt.tolerance)
			}
		})
	}
}

func TestCalculateSpatialConcentration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil)

	tests := []struct {
		name               string
		properties         []investment.Property
		weights            []float64
		expectedConc       float64
		tolerance          float64
	}{
		{
			name: "same location (maximum spatial concentration)",
			properties: []investment.Property{
				{ID: "p1", City: "Austin", Latitude: 30.2672, Longitude: -97.7431},
				{ID: "p2", City: "Austin", Latitude: 30.2672, Longitude: -97.7431},
			},
			weights:      []float64{0.5, 0.5},
			expectedConc: 1.0, // Same location = distance 0 = concentration 1.0
			tolerance:    0.01,
		},
		{
			name: "very far apart (minimum spatial concentration)",
			properties: []investment.Property{
				{ID: "p1", City: "New York", Latitude: 40.7128, Longitude: -74.0060},
				{ID: "p2", City: "Los Angeles", Latitude: 34.0522, Longitude: -118.2437},
			},
			weights:      []float64{0.5, 0.5},
			expectedConc: 0.0, // >500km apart = concentration ~0.0
			tolerance:    0.1,
		},
		{
			name: "moderate distance (moderate spatial concentration)",
			properties: []investment.Property{
				{ID: "p1", City: "Austin", Latitude: 30.2672, Longitude: -97.7431},
				{ID: "p2", City: "Dallas", Latitude: 32.7767, Longitude: -96.7970},
			},
			weights:      []float64{0.5, 0.5},
			expectedConc: 0.5, // ~300km apart = moderate concentration
			tolerance:    0.2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := PortfolioConfiguration{
				Properties: tt.properties,
				Weights:    tt.weights,
			}

			conc := fo.calculateSpatialConcentration(&config)

			if math.Abs(conc-tt.expectedConc) > tt.tolerance {
				t.Errorf("SpatialConcentration = %.2f, want %.2f (tolerance %.2f)",
					conc, tt.expectedConc, tt.tolerance)
			}

			// Concentration should be 0-1
			if conc < 0 || conc > 1 {
				t.Errorf("SpatialConcentration = %.2f, should be 0-1", conc)
			}
		})
	}
}

func TestCalculateStressTestEquity(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil)

	profile := investment.InvestorProfile{
		Strategy:          investment.StrategyCashFlow,
		RiskTolerance:     investment.RiskModerate,
		AvailableCapital:  500000,
		InvestmentHorizon: "10 years",
	}

	tests := []struct {
		name          string
		properties    []investment.Property
		weights       []float64
		expectPositive bool
	}{
		{
			name: "high cash flow properties (positive equity expected)",
			properties: []investment.Property{
				{ID: "p1", Price: 300000, EstimatedRent: 3000}, // 12% gross yield
				{ID: "p2", Price: 400000, EstimatedRent: 4000}, // 12% gross yield
			},
			weights:        []float64{0.5, 0.5},
			expectPositive: true,
		},
		{
			name: "low cash flow properties (may have negative equity)",
			properties: []investment.Property{
				{ID: "p1", Price: 1000000, EstimatedRent: 3000}, // 3.6% gross yield
				{ID: "p2", Price: 900000, EstimatedRent: 2800},  // 3.7% gross yield
			},
			weights:        []float64{0.5, 0.5},
			expectPositive: false, // May be negative under stress
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := PortfolioConfiguration{
				Properties: tt.properties,
				Weights:    tt.weights,
			}

			stressEquity := fo.calculateStressTestEquity(&config, profile)

			if tt.expectPositive && stressEquity <= 0 {
				t.Errorf("Expected positive stress equity, got %d", stressEquity)
			}

			t.Logf("Stress test equity: $%d", stressEquity)
		})
	}
}

// TestRecalculate_MortgageRateOverride verifies that Recalculate re-runs phases 5-8
// with updated mortgage rate and returns updated PropertyInPortfolio metrics.
// ADR-088 Phase 9: Interactive Workspace fast recalculate path.
func TestRecalculate_MortgageRateOverride(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil)

	// Use the actual mortgage calculator to build consistent test data at 7%
	loanAmountP1 := 300000 // $400K price, 25% down
	loanAmountP2 := 285000 // $380K price, 25% down
	origRate := 7.0
	origPayP1 := fo.calculateMortgagePayment(loanAmountP1, origRate, 30)
	origPayP2 := fo.calculateMortgagePayment(loanAmountP2, origRate, 30)

	existing := []investment.FrontierPoint{
		{
			ConfigIndex: 0, ExpectedReturn: 9.5, PortfolioVolatility: 4.2,
			SharpeScore: 1.5, ConcentrationIndex: 0.35, StressTestEquity: 95000,
			Properties: []investment.PropertyInPortfolio{
				{
					Property: investment.Property{
						ID: "p1", City: "Austin", State: "TX",
						Price: 400000, EstimatedRent: 2400,
					},
					DownPayment: 100000, LoanAmount: loanAmountP1,
					MonthlyPayment:  origPayP1,
					MonthlyCashFlow: 2400 - origPayP1 - 500,
					CapRate:         float64(2400*12) / float64(400000) * 100,
					CashOnCash:      float64((2400-origPayP1-500)*12) / float64(100000) * 100,
					DSCR:            float64(2400) / float64(origPayP1),
				},
				{
					Property: investment.Property{
						ID: "p2", City: "Austin", State: "TX",
						Price: 380000, EstimatedRent: 2300,
					},
					DownPayment: 95000, LoanAmount: loanAmountP2,
					MonthlyPayment:  origPayP2,
					MonthlyCashFlow: 2300 - origPayP2 - 500,
					CapRate:         float64(2300*12) / float64(380000) * 100,
					CashOnCash:      float64((2300-origPayP2-500)*12) / float64(95000) * 100,
					DSCR:            float64(2300) / float64(origPayP2),
				},
			},
		},
	}

	profile := investment.InvestorProfile{
		Strategy: investment.StrategyCashFlow, RiskTolerance: investment.RiskModerate,
		AvailableCapital: 500000,
	}

	// Apply mortgage rate override: 5.5% (lower than original 7%)
	newRate := 5.5
	overrides := investment.AssumptionOverrides{MortgageRate: &newRate}
	newPayP1 := fo.calculateMortgagePayment(loanAmountP1, newRate, 30)
	newPayP2 := fo.calculateMortgagePayment(loanAmountP2, newRate, 30)
	t.Logf("Expected: P1 payment $%d→$%d, P2 payment $%d→$%d", origPayP1, newPayP1, origPayP2, newPayP2)

	ctx := context.Background()
	updated, err := fo.Recalculate(ctx, existing, profile, investment.InvestmentPlanningParams{}, overrides)
	if err != nil {
		t.Fatalf("Recalculate() error = %v", err)
	}

	if len(updated) != len(existing) {
		t.Fatalf("Recalculate() returned %d configs, want %d", len(updated), len(existing))
	}

	for configIdx, fp := range updated {
		for propIdx, prop := range fp.Properties {
			originalProp := existing[configIdx].Properties[propIdx]

			// Lower rate → lower monthly payment
			if prop.MonthlyPayment >= originalProp.MonthlyPayment {
				t.Errorf("Config %d Prop %d: MonthlyPayment not reduced: was $%d, now $%d",
					configIdx, propIdx, originalProp.MonthlyPayment, prop.MonthlyPayment)
			}

			// Cap rate should be unchanged (independent of mortgage)
			if math.Abs(prop.CapRate-originalProp.CapRate) > 0.01 {
				t.Errorf("Config %d Prop %d: CapRate changed unexpectedly: was %.2f, now %.2f",
					configIdx, propIdx, originalProp.CapRate, prop.CapRate)
			}

			t.Logf("Config %d Prop %s: payment $%d→$%d, CoC %.2f%%→%.2f%%",
				configIdx, prop.Property.ID, originalProp.MonthlyPayment, prop.MonthlyPayment,
				originalProp.CashOnCash, prop.CashOnCash)
		}
	}
}

// TestRecalculate_EmptyInput verifies Recalculate returns an error for empty input.
func TestRecalculate_EmptyInput(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil)

	ctx := context.Background()
	_, err := fo.Recalculate(ctx, nil, investment.InvestorProfile{}, investment.InvestmentPlanningParams{}, investment.AssumptionOverrides{})
	if err == nil {
		t.Error("Recalculate() with empty input: expected error, got nil")
	}
}

// TestRecalculate_NoOverrides verifies Recalculate without overrides returns equivalent configs.
func TestRecalculate_NoOverrides(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil)

	existing := []investment.FrontierPoint{
		{
			ConfigIndex: 0, ExpectedReturn: 9.5,
			Properties: []investment.PropertyInPortfolio{
				{Property: investment.Property{ID: "p1", Price: 300000, EstimatedRent: 1800},
					DownPayment: 75000, LoanAmount: 225000, MonthlyPayment: 1207, MonthlyCashFlow: 93, CapRate: 7.2},
				{Property: investment.Property{ID: "p2", Price: 280000, EstimatedRent: 1700},
					DownPayment: 70000, LoanAmount: 210000, MonthlyPayment: 1126, MonthlyCashFlow: 74, CapRate: 7.29},
				{Property: investment.Property{ID: "p3", Price: 320000, EstimatedRent: 1900},
					DownPayment: 80000, LoanAmount: 240000, MonthlyPayment: 1287, MonthlyCashFlow: 113, CapRate: 7.13},
				{Property: investment.Property{ID: "p4", Price: 350000, EstimatedRent: 2100},
					DownPayment: 87500, LoanAmount: 262500, MonthlyPayment: 1408, MonthlyCashFlow: 192, CapRate: 7.2},
				{Property: investment.Property{ID: "p5", Price: 310000, EstimatedRent: 1850},
					DownPayment: 77500, LoanAmount: 232500, MonthlyPayment: 1247, MonthlyCashFlow: 103, CapRate: 7.16},
			},
		},
	}

	ctx := context.Background()
	updated, err := fo.Recalculate(ctx, existing, investment.InvestorProfile{}, investment.InvestmentPlanningParams{}, investment.AssumptionOverrides{})
	if err != nil {
		t.Fatalf("Recalculate() error = %v", err)
	}

	if len(updated) != 1 {
		t.Fatalf("Recalculate() returned %d configs, want 1", len(updated))
	}

	// Config index and core metrics should be preserved
	if updated[0].ConfigIndex != existing[0].ConfigIndex {
		t.Errorf("ConfigIndex mismatch: got %d, want %d", updated[0].ConfigIndex, existing[0].ConfigIndex)
	}
	if updated[0].ExpectedReturn != existing[0].ExpectedReturn {
		t.Errorf("ExpectedReturn changed: was %.2f, now %.2f", existing[0].ExpectedReturn, updated[0].ExpectedReturn)
	}

	t.Logf("Recalculate with no overrides: %d properties preserved", len(updated[0].Properties))
}
