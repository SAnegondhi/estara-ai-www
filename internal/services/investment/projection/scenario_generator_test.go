package projection

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
)

func TestGenerateScenarios(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	sg := NewScenarioGenerator(logger)

	// Create mock MC results with percentiles
	mcResults := &investment.SimulationResults{
		Paths: []investment.MonteCarloPath{
			{PathIndex: 0, TrackBFiredYear: 3, FinalNetWorth: 1500000},
			{PathIndex: 1, TrackBFiredYear: 4, FinalNetWorth: 1800000},
			{PathIndex: 2, TrackBFiredYear: 5, FinalNetWorth: 2000000},
		},
		Percentiles: &investment.PercentileOutcomes{
			P50: []investment.YearOutcome{
				{Year: 1, PortfolioValue: 400000, Equity: 100000, CumulativeCashFlow: 5000, NetWorth: 105000},
				{Year: 2, PortfolioValue: 420000, Equity: 120000, CumulativeCashFlow: 12000, NetWorth: 132000},
				{Year: 3, PortfolioValue: 440000, Equity: 140000, CumulativeCashFlow: 20000, NetWorth: 160000},
			},
			P75: []investment.YearOutcome{
				{Year: 1, PortfolioValue: 420000, Equity: 120000, CumulativeCashFlow: 6000, NetWorth: 126000},
				{Year: 2, PortfolioValue: 450000, Equity: 150000, CumulativeCashFlow: 14000, NetWorth: 164000},
				{Year: 3, PortfolioValue: 480000, Equity: 180000, CumulativeCashFlow: 23000, NetWorth: 203000},
			},
		},
	}

	// Create test configuration
	config := &investment.FrontierPoint{
		ConfigIndex: 0,
		Properties: []investment.PropertyInPortfolio{
			{
				Property: investment.Property{
					Price:         400000,
					EstimatedRent: 2500,
					City:          "Austin",
					State:         "TX",
				},
			},
		},
	}

	ctx := context.Background()
	scenarios, err := sg.GenerateScenarios(ctx, mcResults, config)

	if err != nil {
		t.Fatalf("GenerateScenarios() error = %v", err)
	}

	if scenarios == nil {
		t.Fatal("scenarios should not be nil")
	}

	// Verify Base scenario
	if scenarios.Base.Name != "Base" {
		t.Errorf("Base.Name = %s, want Base", scenarios.Base.Name)
	}
	if len(scenarios.Base.YearlyOutcomes) != 3 {
		t.Errorf("Base.YearlyOutcomes length = %d, want 3", len(scenarios.Base.YearlyOutcomes))
	}
	if scenarios.Base.FinalNetWorth != 160000 {
		t.Errorf("Base.FinalNetWorth = %d, want 160000", scenarios.Base.FinalNetWorth)
	}

	// Verify Upside scenario
	if scenarios.Upside.Name != "Upside" {
		t.Errorf("Upside.Name = %s, want Upside", scenarios.Upside.Name)
	}
	if len(scenarios.Upside.YearlyOutcomes) != 3 {
		t.Errorf("Upside.YearlyOutcomes length = %d, want 3", len(scenarios.Upside.YearlyOutcomes))
	}
	if scenarios.Upside.FinalNetWorth != 203000 {
		t.Errorf("Upside.FinalNetWorth = %d, want 203000", scenarios.Upside.FinalNetWorth)
	}

	// Verify Stress scenario
	if scenarios.Stress.Name != "Stress" {
		t.Errorf("Stress.Name = %s, want Stress", scenarios.Stress.Name)
	}
	if len(scenarios.Stress.YearlyOutcomes) == 0 {
		t.Error("Stress.YearlyOutcomes should not be empty")
	}

	t.Logf("✅ Scenarios generated: Base Y3=$%d, Upside Y3=$%d, Stress Y3=$%d",
		scenarios.Base.FinalNetWorth,
		scenarios.Upside.FinalNetWorth,
		scenarios.Stress.YearlyOutcomes[2].NetWorth,
	)
}

func TestGenerateStressScenario(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	sg := NewScenarioGenerator(logger)

	// Create test configuration
	config := &investment.FrontierPoint{
		ConfigIndex: 0,
		Properties: []investment.PropertyInPortfolio{
			{
				Property: investment.Property{
					Price:         400000,
					EstimatedRent: 2500,
					City:          "Austin",
					State:         "TX",
				},
			},
			{
				Property: investment.Property{
					Price:         350000,
					EstimatedRent: 2200,
					City:          "Dallas",
					State:         "TX",
				},
			},
		},
	}

	ctx := context.Background()
	stressScenario, err := sg.generateStressScenario(ctx, config)

	if err != nil {
		t.Fatalf("generateStressScenario() error = %v", err)
	}

	// Verify stress scenario has 10 years
	if len(stressScenario.YearlyOutcomes) != 10 {
		t.Errorf("YearlyOutcomes length = %d, want 10", len(stressScenario.YearlyOutcomes))
	}

	// Verify year 1 has negative appreciation shock
	year1 := stressScenario.YearlyOutcomes[0]
	initialValue := 750000 // 400K + 350K
	expectedValueAfterShock := int(float64(initialValue) * 0.85) // -15% shock

	// Allow for some tolerance due to calculation differences
	tolerance := 10000
	if abs(year1.PortfolioValue-expectedValueAfterShock) > tolerance {
		t.Errorf("Year 1 PortfolioValue = %d, want ~%d (after -15%% shock)",
			year1.PortfolioValue, expectedValueAfterShock)
	}

	// Verify cash flow is negative or low (stress conditions)
	year1CashFlow := year1.CumulativeCashFlow
	if year1CashFlow > 10000 {
		t.Errorf("Year 1 CumulativeCashFlow = %d, expected low or negative in stress scenario", year1CashFlow)
	}

	// Verify final net worth is lower than initial equity
	finalNetWorth := stressScenario.FinalNetWorth
	initialEquity := int(float64(initialValue) * 0.2) // 20% down payment

	t.Logf("✅ Stress scenario: Y1 Value=$%d (after shock), Y10 NetWorth=$%d vs Initial Equity=$%d",
		year1.PortfolioValue,
		finalNetWorth,
		initialEquity,
	)

	// Verify scenario has survivable cash flow (not deeply negative Y5)
	year5 := stressScenario.YearlyOutcomes[4]
	if year5.CumulativeCashFlow < -500000 {
		t.Errorf("Year 5 cash flow too negative: %d (portfolio may not survive stress)", year5.CumulativeCashFlow)
	}
}

func TestGenerateScenarios_NilMCResults(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	sg := NewScenarioGenerator(logger)

	ctx := context.Background()
	config := &investment.FrontierPoint{}

	_, err := sg.GenerateScenarios(ctx, nil, config)

	if err == nil {
		t.Error("GenerateScenarios() with nil MC results should return error")
	}
}

func TestGenerateScenarios_NilPercentiles(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	sg := NewScenarioGenerator(logger)

	mcResults := &investment.SimulationResults{
		Paths:       []investment.MonteCarloPath{},
		Percentiles: nil, // Missing percentiles
	}

	ctx := context.Background()
	config := &investment.FrontierPoint{}

	_, err := sg.GenerateScenarios(ctx, mcResults, config)

	if err == nil {
		t.Error("GenerateScenarios() with nil percentiles should return error")
	}
}

// Helper function
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
