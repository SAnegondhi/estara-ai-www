package verdict

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
)

func TestGenerateVerdict_Proceed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	vg := NewGenerator(logger)

	// Create config with strong fundamentals
	config := &investment.FrontierPoint{
		ConfigIndex:         0,
		ExpectedReturn:      12.0,
		PortfolioVolatility: 4.5,
		SharpeScore:         2.0,
		ConcentrationIndex:  0.4,
		Properties:          make([]investment.PropertyInPortfolio, 5),
		Scenarios: &investment.ScenarioSet{
			Base: investment.Scenario{
				Name:          "Base",
				FinalNetWorth: 2000000,
				YearlyOutcomes: []investment.YearOutcome{
					{Year: 1, NetWorth: 200000},
					{Year: 5, NetWorth: 800000},
					{Year: 10, NetWorth: 2000000},
				},
			},
			Upside: investment.Scenario{
				Name:          "Upside",
				FinalNetWorth: 2500000,
			},
			Stress: investment.Scenario{
				Name:          "Stress",
				FinalNetWorth: -200000, // Negative but not deeply negative Y5
				YearlyOutcomes: []investment.YearOutcome{
					{Year: 1, NetWorth: 100000},
					{Year: 5, NetWorth: -100000}, // Survivable
					{Year: 10, NetWorth: -200000},
				},
			},
		},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	ctx := context.Background()
	verdict, err := vg.GenerateVerdict(ctx, config, profile, 1500000)

	if err != nil {
		t.Fatalf("GenerateVerdict() error = %v", err)
	}

	// Verify recommendation is PROCEED
	if verdict.Recommendation != investment.DecisionProceed {
		t.Errorf("Recommendation = %s, want PROCEED", verdict.Recommendation)
	}

	// Verify rationale is not empty
	if len(verdict.DecisionRationale) < 100 {
		t.Errorf("DecisionRationale too short: %d chars", len(verdict.DecisionRationale))
	}

	// Verify key conditions (3-5)
	if len(verdict.KeyConditions) < 3 || len(verdict.KeyConditions) > 5 {
		t.Errorf("KeyConditions count = %d, want 3-5", len(verdict.KeyConditions))
	}

	// Verify monitoring signals (3+)
	if len(verdict.MonitoringSignals) < 3 {
		t.Errorf("MonitoringSignals count = %d, want >= 3", len(verdict.MonitoringSignals))
	}

	// Verify timestamp format
	if verdict.GeneratedAt == "" {
		t.Error("GeneratedAt should not be empty")
	}

	t.Logf("✅ PROCEED verdict: %d conditions, %d signals", len(verdict.KeyConditions), len(verdict.MonitoringSignals))
}

func TestGenerateVerdict_Pause_DeeplyNegativeStress(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	vg := NewGenerator(logger)

	// Create config with deeply negative stress scenario Y5
	config := &investment.FrontierPoint{
		ConfigIndex:         0,
		ExpectedReturn:      10.0,
		PortfolioVolatility: 6.0,
		SharpeScore:         1.2,
		ConcentrationIndex:  0.5,
		Properties:          make([]investment.PropertyInPortfolio, 5),
		Scenarios: &investment.ScenarioSet{
			Base: investment.Scenario{
				Name:          "Base",
				FinalNetWorth: 1800000,
			},
			Upside: investment.Scenario{
				Name:          "Upside",
				FinalNetWorth: 2200000,
			},
			Stress: investment.Scenario{
				Name:          "Stress",
				FinalNetWorth: -800000,
				YearlyOutcomes: []investment.YearOutcome{
					{Year: 1, NetWorth: 50000},
					{Year: 5, NetWorth: -600000}, // Deeply negative!
					{Year: 10, NetWorth: -800000},
				},
			},
		},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	ctx := context.Background()
	verdict, err := vg.GenerateVerdict(ctx, config, profile, 0)

	if err != nil {
		t.Fatalf("GenerateVerdict() error = %v", err)
	}

	// Should recommend PAUSE due to deeply negative Y5 stress
	if verdict.Recommendation != investment.DecisionPause {
		t.Errorf("Recommendation = %s, want PAUSE (deeply negative stress Y5)", verdict.Recommendation)
	}

	t.Logf("✅ PAUSE verdict (stress Y5=-$600K): %s", verdict.Recommendation)
}

func TestGenerateVerdict_Pause_HighVolatility(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	vg := NewGenerator(logger)

	// Create config with high volatility
	config := &investment.FrontierPoint{
		ConfigIndex:         0,
		ExpectedReturn:      15.0,
		PortfolioVolatility: 12.0, // Very high!
		SharpeScore:         1.0,
		ConcentrationIndex:  0.7,
		Properties:          make([]investment.PropertyInPortfolio, 5),
		Scenarios: &investment.ScenarioSet{
			Base: investment.Scenario{
				Name:          "Base",
				FinalNetWorth: 2200000,
			},
			Upside: investment.Scenario{
				Name:          "Upside",
				FinalNetWorth: 2800000,
			},
			Stress: investment.Scenario{
				Name:          "Stress",
				FinalNetWorth: -300000,
				YearlyOutcomes: []investment.YearOutcome{
					{Year: 5, NetWorth: -200000}, // Survivable
				},
			},
		},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyBalanced,
		RiskTolerance: investment.RiskModerate,
	}

	ctx := context.Background()
	verdict, err := vg.GenerateVerdict(ctx, config, profile, 0)

	if err != nil {
		t.Fatalf("GenerateVerdict() error = %v", err)
	}

	// Should recommend PAUSE due to high volatility
	if verdict.Recommendation != investment.DecisionPause {
		t.Errorf("Recommendation = %s, want PAUSE (high volatility)", verdict.Recommendation)
	}

	t.Logf("✅ PAUSE verdict (volatility=12.0%%): %s", verdict.Recommendation)
}

func TestGenerateVerdict_Revise_MissTarget(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	vg := NewGenerator(logger)

	// Create config that misses wealth target significantly
	config := &investment.FrontierPoint{
		ConfigIndex:         0,
		ExpectedReturn:      8.0,
		PortfolioVolatility: 5.0,
		SharpeScore:         1.0,
		ConcentrationIndex:  0.5,
		Properties:          make([]investment.PropertyInPortfolio, 5),
		Scenarios: &investment.ScenarioSet{
			Base: investment.Scenario{
				Name:          "Base",
				FinalNetWorth: 1200000, // Far from $2M target
			},
			Upside: investment.Scenario{
				Name:          "Upside",
				FinalNetWorth: 1500000,
			},
			Stress: investment.Scenario{
				Name:          "Stress",
				FinalNetWorth: -100000,
				YearlyOutcomes: []investment.YearOutcome{
					{Year: 5, NetWorth: -50000}, // Survivable
				},
			},
		},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	ctx := context.Background()
	verdict, err := vg.GenerateVerdict(ctx, config, profile, 2000000) // $2M target

	if err != nil {
		t.Fatalf("GenerateVerdict() error = %v", err)
	}

	// Should recommend REVISE - misses target by >30%
	if verdict.Recommendation != investment.DecisionRevise {
		t.Errorf("Recommendation = %s, want REVISE (base $1.2M vs target $2M)", verdict.Recommendation)
	}

	t.Logf("✅ REVISE verdict (target gap=40%%): %s", verdict.Recommendation)
}

func TestGenerateVerdict_Revise_LowSharpe(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	vg := NewGenerator(logger)

	// Create config with low Sharpe ratio — threshold is < 0.25
	config := &investment.FrontierPoint{
		ConfigIndex:         0,
		ExpectedReturn:      5.0,
		PortfolioVolatility: 8.0,
		SharpeScore:         0.2, // Below the 0.25 REVISE threshold
		ConcentrationIndex:  0.4,
		Properties:          make([]investment.PropertyInPortfolio, 5),
		Scenarios: &investment.ScenarioSet{
			Base: investment.Scenario{
				Name:          "Base",
				FinalNetWorth: 1500000,
			},
			Upside: investment.Scenario{
				Name:          "Upside",
				FinalNetWorth: 1700000,
			},
			Stress: investment.Scenario{
				Name:          "Stress",
				FinalNetWorth: -200000,
				YearlyOutcomes: []investment.YearOutcome{
					{Year: 5, NetWorth: -100000}, // Survivable
				},
			},
		},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyBalanced,
		RiskTolerance: investment.RiskConservative,
	}

	ctx := context.Background()
	verdict, err := vg.GenerateVerdict(ctx, config, profile, 0)

	if err != nil {
		t.Fatalf("GenerateVerdict() error = %v", err)
	}

	// Should recommend REVISE due to poor risk-adjusted returns
	if verdict.Recommendation != investment.DecisionRevise {
		t.Errorf("Recommendation = %s, want REVISE (low Sharpe)", verdict.Recommendation)
	}

	t.Logf("✅ REVISE verdict (Sharpe=0.2): %s", verdict.Recommendation)
}

func TestGenerateVerdict_MonitoringSignals(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	vg := NewGenerator(logger)

	// Create config with high concentration
	config := &investment.FrontierPoint{
		ConfigIndex:         0,
		ExpectedReturn:      10.0,
		PortfolioVolatility: 5.0,
		SharpeScore:         1.5,
		ConcentrationIndex:  0.75, // High concentration
		Properties:          make([]investment.PropertyInPortfolio, 5),
		Scenarios: &investment.ScenarioSet{
			Base: investment.Scenario{
				Name:          "Base",
				FinalNetWorth: 1800000,
			},
			Upside: investment.Scenario{
				Name:          "Upside",
				FinalNetWorth: 2200000,
			},
			Stress: investment.Scenario{
				Name:          "Stress",
				FinalNetWorth: -200000,
				YearlyOutcomes: []investment.YearOutcome{
					{Year: 5, NetWorth: -100000}, // Survivable
				},
			},
		},
		ReinvestmentPlan: &investment.DualTrackReinvestment{
			TrackB: investment.TrackBReinvestment{
				Threshold:        50000,
				FiredProbability: 0.85,
			},
		},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	ctx := context.Background()
	verdict, err := vg.GenerateVerdict(ctx, config, profile, 0)

	if err != nil {
		t.Fatalf("GenerateVerdict() error = %v", err)
	}

	// Should have 4 signals (3 base + 1 concentration)
	if len(verdict.MonitoringSignals) != 4 {
		t.Errorf("MonitoringSignals count = %d, want 4 (3 base + 1 concentration)", len(verdict.MonitoringSignals))
	}

	// Verify all signals have required fields
	for i, signal := range verdict.MonitoringSignals {
		if signal.Indicator == "" {
			t.Errorf("Signal %d: Indicator is empty", i)
		}
		if signal.Description == "" {
			t.Errorf("Signal %d: Description is empty", i)
		}
		if signal.Justification == "" {
			t.Errorf("Signal %d: Justification is empty", i)
		}
		if signal.AlertLevel <= 0 {
			t.Errorf("Signal %d: AlertLevel not set", i)
		}

		t.Logf("Signal %d: %s (current=%.2f, watch=%.2f, warn=%.2f, alert=%.2f)",
			i, signal.Indicator, signal.CurrentValue, signal.WatchLevel, signal.WarnLevel, signal.AlertLevel)
	}

	// Verify key conditions include concentration
	foundConcentration := false
	for _, condition := range verdict.KeyConditions {
		if len(condition) > 0 {
			t.Logf("Condition: %s", condition)
			if containsSubstring(condition, "concentration") {
				foundConcentration = true
			}
		}
	}

	if !foundConcentration {
		t.Error("Expected concentration-related key condition for high concentration index")
	}

	t.Logf("✅ Monitoring signals verified: %d signals, %d conditions", len(verdict.MonitoringSignals), len(verdict.KeyConditions))
}

func TestGenerateVerdict_NilConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	vg := NewGenerator(logger)

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	ctx := context.Background()
	_, err := vg.GenerateVerdict(ctx, nil, profile, 0)

	if err == nil {
		t.Error("GenerateVerdict() with nil config should return error")
	}
}

func TestGenerateVerdict_NilScenarios(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	vg := NewGenerator(logger)

	config := &investment.FrontierPoint{
		ConfigIndex: 0,
		Scenarios:   nil, // No scenarios
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	ctx := context.Background()
	_, err := vg.GenerateVerdict(ctx, config, profile, 0)

	if err == nil {
		t.Error("GenerateVerdict() with nil scenarios should return error")
	}
}

// Helper function
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
