package optimization

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/projection"
)

// TestGenerateFrontier_WithMonteCarloSimulation tests frontier generation with full MC simulation
func TestGenerateFrontier_WithMonteCarloSimulation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger, nil)
	mcSimulator := projection.NewMonteCarloSimulator(logger)
	scenarioGenerator := projection.NewScenarioGenerator(logger)

	// Create frontier optimizer with MC simulator and scenario generator
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, mcSimulator, scenarioGenerator, nil)

	// Create test properties
	properties := []investment.ScoredProperty{
		{
			Property: investment.Property{
				Price:                   400000,
				EstimatedRent:           2500,
				City:                    "Austin",
				State:                   "TX",
				PropertyType:            "Single Family",
				Latitude:                30.2672,
				Longitude:               -97.7431,
			},
		},
		{
			Property: investment.Property{
				Price:                   350000,
				EstimatedRent:           2200,
				City:                    "Dallas",
				State:                   "TX",
				PropertyType:            "Single Family",
				Latitude:                32.7767,
				Longitude:               -96.7970,
			},
		},
		{
			Property: investment.Property{
				Price:                   450000,
				EstimatedRent:           2800,
				City:                    "Phoenix",
				State:                   "AZ",
				PropertyType:            "Condo",
				Latitude:                33.4484,
				Longitude:               -112.0740,
			},
		},
		{
			Property: investment.Property{
				Price:                   380000,
				EstimatedRent:           2400,
				City:                    "Houston",
				State:                   "TX",
				PropertyType:            "Townhouse",
				Latitude:                29.7604,
				Longitude:               -95.3698,
			},
		},
		{
			Property: investment.Property{
				Price:                   420000,
				EstimatedRent:           2600,
				City:                    "Austin",
				State:                   "TX",
				PropertyType:            "Condo",
				Latitude:                30.2672,
				Longitude:               -97.7431,
			},
		},
	}

	// Create investment profile
	profile := investment.InvestorProfile{
		RiskTolerance:     investment.RiskModerate,
		InvestmentHorizon: "10+ years",
		AvailableCapital:  500000,
		Strategy:          investment.StrategyBalanced,
	}

	// Create params (no yearly budgets for simplicity - Track A inactive)
	params := investment.InvestmentPlanningParams{
		YearlyBudgets: []investment.YearlyBudget{},
	}

	ctx := context.Background()
	cohorts := testBuildCohorts(properties, profile.Strategy, profile.RiskTolerance, profile.AvailableCapital)
	frontierPoints, err := fo.GenerateFrontier(ctx, cohorts, profile, params, nil)

	if err != nil {
		t.Fatalf("GenerateFrontier() error = %v", err)
	}

	if len(frontierPoints) == 0 {
		t.Fatal("expected at least 1 frontier point")
	}

	t.Logf("✅ Generated %d frontier points with Monte Carlo simulation", len(frontierPoints))

	// Verify each frontier point has MC results and updated reinvestment plan
	for i, point := range frontierPoints {
		if point.SimulationResults == nil {
			t.Errorf("Config %d: SimulationResults is nil, expected MC results", i)
			continue
		}

		// Verify MC results have paths
		if len(point.SimulationResults.Paths) == 0 {
			t.Errorf("Config %d: expected > 0 MC paths, got 0", i)
		}

		// Verify MC results have percentiles
		if point.SimulationResults.Percentiles == nil {
			t.Errorf("Config %d: Percentiles is nil", i)
		}

		// Verify reinvestment plan exists
		if point.ReinvestmentPlan == nil {
			t.Errorf("Config %d: ReinvestmentPlan is nil", i)
			continue
		}

		// Verify Track B statistics are computed (not placeholders)
		// Note: With empty yearly budgets, Track A should be inactive
		if point.ReinvestmentPlan.TrackA.TotalCapital != 0 {
			t.Errorf("Config %d: expected Track A inactive (TotalCapital=0), got %d",
				i, point.ReinvestmentPlan.TrackA.TotalCapital)
		}

		// Track B threshold should be set
		if point.ReinvestmentPlan.TrackB.Threshold != 50000 {
			t.Errorf("Config %d: expected Track B threshold=50000, got %d",
				i, point.ReinvestmentPlan.TrackB.Threshold)
		}

		// Verify scenarios are generated (Phase 7)
		if point.Scenarios == nil {
			t.Errorf("Config %d: Scenarios is nil, expected Base/Upside/Stress", i)
			continue
		}

		// Verify Base scenario (P50 from MC)
		if point.Scenarios.Base.Name != "Base" {
			t.Errorf("Config %d: Base scenario name = %s, want 'Base'", i, point.Scenarios.Base.Name)
		}
		if len(point.Scenarios.Base.YearlyOutcomes) == 0 {
			t.Errorf("Config %d: Base scenario has no yearly outcomes", i)
		}

		// Verify Upside scenario (P75 from MC)
		if point.Scenarios.Upside.Name != "Upside" {
			t.Errorf("Config %d: Upside scenario name = %s, want 'Upside'", i, point.Scenarios.Upside.Name)
		}
		if len(point.Scenarios.Upside.YearlyOutcomes) == 0 {
			t.Errorf("Config %d: Upside scenario has no yearly outcomes", i)
		}

		// Verify Stress scenario (deterministic worst-case)
		if point.Scenarios.Stress.Name != "Stress" {
			t.Errorf("Config %d: Stress scenario name = %s, want 'Stress'", i, point.Scenarios.Stress.Name)
		}
		if len(point.Scenarios.Stress.YearlyOutcomes) != 10 {
			t.Errorf("Config %d: Stress scenario should have 10 years, got %d", i, len(point.Scenarios.Stress.YearlyOutcomes))
		}

		t.Logf("Config %d: MC Paths=%d, P50 Final Net Worth=$%d, Track B Fired Prob=%.2f",
			i,
			len(point.SimulationResults.Paths),
			point.SimulationResults.Percentiles.P50[len(point.SimulationResults.Percentiles.P50)-1].NetWorth,
			point.ReinvestmentPlan.TrackB.FiredProbability,
		)
		t.Logf("  Scenarios: Base Y10=$%d, Upside Y10=$%d, Stress Y10=$%d",
			point.Scenarios.Base.FinalNetWorth,
			point.Scenarios.Upside.FinalNetWorth,
			point.Scenarios.Stress.FinalNetWorth,
		)
	}
}

// TestGenerateFrontier_WithYearlyBudgets tests MC simulation with Track A budgets
func TestGenerateFrontier_WithYearlyBudgets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger, nil)
	mcSimulator := projection.NewMonteCarloSimulator(logger)
	scenarioGenerator := projection.NewScenarioGenerator(logger)

	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, mcSimulator, scenarioGenerator, nil)

	// Create test properties (need at least 5)
	properties := []investment.ScoredProperty{
		{
			Property: investment.Property{
				Price:         400000,
				EstimatedRent: 2500,
				City:          "Austin",
				State:         "TX",
				PropertyType:  "Single Family",
				Latitude:      30.2672,
				Longitude:     -97.7431,
			},
		},
		{
			Property: investment.Property{
				Price:         350000,
				EstimatedRent: 2200,
				City:          "Dallas",
				State:         "TX",
				PropertyType:  "Single Family",
				Latitude:      32.7767,
				Longitude:     -96.7970,
			},
		},
		{
			Property: investment.Property{
				Price:         450000,
				EstimatedRent: 2800,
				City:          "Phoenix",
				State:         "AZ",
				PropertyType:  "Condo",
				Latitude:      33.4484,
				Longitude:     -112.0740,
			},
		},
		{
			Property: investment.Property{
				Price:         380000,
				EstimatedRent: 2400,
				City:          "Houston",
				State:         "TX",
				PropertyType:  "Townhouse",
				Latitude:      29.7604,
				Longitude:     -95.3698,
			},
		},
		{
			Property: investment.Property{
				Price:         420000,
				EstimatedRent: 2600,
				City:          "San Antonio",
				State:         "TX",
				PropertyType:  "Single Family",
				Latitude:      29.4241,
				Longitude:     -98.4936,
			},
		},
	}

	profile := investment.InvestorProfile{
		RiskTolerance:     investment.RiskModerate,
		InvestmentHorizon: "10+ years",
		AvailableCapital:  500000,
		Strategy:          investment.StrategyBalanced,
	}

	// Create params with yearly budgets (Track A active)
	params := investment.InvestmentPlanningParams{
		YearlyBudgets: []investment.YearlyBudget{
			{Year: 2, Budget: 100000},
			{Year: 5, Budget: 150000},
		},
	}

	ctx := context.Background()
	cohorts := testBuildCohorts(properties, profile.Strategy, profile.RiskTolerance, profile.AvailableCapital)
	frontierPoints, err := fo.GenerateFrontier(ctx, cohorts, profile, params, nil)

	if err != nil {
		t.Fatalf("GenerateFrontier() error = %v", err)
	}

	if len(frontierPoints) == 0 {
		t.Fatal("expected at least 1 frontier point")
	}

	// Verify Track A is active
	for i, point := range frontierPoints {
		if point.ReinvestmentPlan == nil {
			t.Errorf("Config %d: ReinvestmentPlan is nil", i)
			continue
		}

		// Track A should have total capital from yearly budgets
		expectedCapital := 250000 // 100K + 150K
		if point.ReinvestmentPlan.TrackA.TotalCapital != expectedCapital {
			t.Errorf("Config %d: expected Track A TotalCapital=%d, got %d",
				i, expectedCapital, point.ReinvestmentPlan.TrackA.TotalCapital)
		}

		// Verify Track A budgets
		if len(point.ReinvestmentPlan.TrackA.YearlyBudgets) != 2 {
			t.Errorf("Config %d: expected 2 yearly budgets, got %d",
				i, len(point.ReinvestmentPlan.TrackA.YearlyBudgets))
		}

		t.Logf("Config %d: Track A Capital=$%d, Track B Fired Prob=%.2f, MC Paths=%d",
			i,
			point.ReinvestmentPlan.TrackA.TotalCapital,
			point.ReinvestmentPlan.TrackB.FiredProbability,
			len(point.SimulationResults.Paths),
		)
	}
}
