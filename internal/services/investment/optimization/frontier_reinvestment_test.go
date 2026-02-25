package optimization

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/projection"
)

// TestFrontier_ReinvestmentPlan_TrackA tests Track A (external capital) integration
func TestFrontier_ReinvestmentPlan_TrackA(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger, nil)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil, nil, nil)

	// Create test properties
	properties := []investment.ScoredProperty{
		{Property: investment.Property{ID: "p1", City: "Austin", State: "TX", Price: 300000, EstimatedRent: 2000}},
		{Property: investment.Property{ID: "p2", City: "Austin", State: "TX", Price: 350000, EstimatedRent: 2200}},
		{Property: investment.Property{ID: "p3", City: "Denver", State: "CO", Price: 400000, EstimatedRent: 2500}},
		{Property: investment.Property{ID: "p4", City: "Denver", State: "CO", Price: 450000, EstimatedRent: 2800}},
		{Property: investment.Property{ID: "p5", City: "Phoenix", State: "AZ", Price: 320000, EstimatedRent: 2100}},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	// Define yearly budgets for Track A
	params := investment.InvestmentPlanningParams{
		YearlyBudgets: []investment.YearlyBudget{
			{Year: 2, Budget: 100000},
			{Year: 3, Budget: 150000},
			{Year: 5, Budget: 200000},
		},
	}

	ctx := context.Background()
	cohorts := testBuildCohorts(properties, profile.Strategy, profile.RiskTolerance, 0)
	frontierPoints, err := fo.GenerateFrontier(ctx, cohorts, profile, params, nil)

	if err != nil {
		t.Fatalf("GenerateFrontier() error = %v", err)
	}

	if len(frontierPoints) == 0 {
		t.Fatal("Expected at least one frontier point")
	}

	// Verify each frontier point has a reinvestment plan
	for i, point := range frontierPoints {
		if point.ReinvestmentPlan == nil {
			t.Errorf("FrontierPoint[%d].ReinvestmentPlan is nil", i)
			continue
		}

		// Verify Track A
		trackA := point.ReinvestmentPlan.TrackA
		if trackA.TotalCapital != 450000 {
			t.Errorf("Config %d: TrackA.TotalCapital = %d, want 450000",
				i, trackA.TotalCapital)
		}

		if len(trackA.YearlyBudgets) != 3 {
			t.Errorf("Config %d: TrackA.YearlyBudgets count = %d, want 3",
				i, len(trackA.YearlyBudgets))
		}

		// Verify yearly budgets
		expectedBudgets := []int{100000, 150000, 200000}
		for j, budget := range trackA.YearlyBudgets {
			if budget.Budget != expectedBudgets[j] {
				t.Errorf("Config %d: YearlyBudget[%d].Budget = %d, want %d",
					i, j, budget.Budget, expectedBudgets[j])
			}
		}

		// Verify Track B — threshold derived from property price (20% down payment)
		trackB := point.ReinvestmentPlan.TrackB
		if trackB.Threshold <= 0 {
			t.Errorf("Config %d: TrackB.Threshold = %d, want > 0 (derived from property price)", i, trackB.Threshold)
		}

		if trackB.MedianFiredYear != 0 {
			t.Errorf("Config %d: TrackB.MedianFiredYear = %d, want 0 (placeholder)",
				i, trackB.MedianFiredYear)
		}
	}

	t.Logf("✅ All %d frontier points have valid reinvestment plans", len(frontierPoints))
}

// TestFrontier_ReinvestmentPlan_NoYearlyBudgets tests Track A inactive (no budgets)
func TestFrontier_ReinvestmentPlan_NoYearlyBudgets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger, nil)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil, nil, nil)

	properties := []investment.ScoredProperty{
		{Property: investment.Property{ID: "p1", City: "Austin", State: "TX", Price: 300000, EstimatedRent: 2000}},
		{Property: investment.Property{ID: "p2", City: "Austin", State: "TX", Price: 350000, EstimatedRent: 2200}},
		{Property: investment.Property{ID: "p3", City: "Denver", State: "CO", Price: 400000, EstimatedRent: 2500}},
		{Property: investment.Property{ID: "p4", City: "Denver", State: "CO", Price: 450000, EstimatedRent: 2800}},
		{Property: investment.Property{ID: "p5", City: "Phoenix", State: "AZ", Price: 320000, EstimatedRent: 2100}},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	// No yearly budgets (Track A inactive)
	params := investment.InvestmentPlanningParams{
		YearlyBudgets: []investment.YearlyBudget{},
	}

	ctx := context.Background()
	cohorts := testBuildCohorts(properties, profile.Strategy, profile.RiskTolerance, 0)
	frontierPoints, err := fo.GenerateFrontier(ctx, cohorts, profile, params, nil)

	if err != nil {
		t.Fatalf("GenerateFrontier() error = %v", err)
	}

	if len(frontierPoints) == 0 {
		t.Fatal("Expected at least one frontier point")
	}

	// Verify reinvestment plans exist but Track A is inactive
	for i, point := range frontierPoints {
		if point.ReinvestmentPlan == nil {
			t.Errorf("FrontierPoint[%d].ReinvestmentPlan is nil", i)
			continue
		}

		trackA := point.ReinvestmentPlan.TrackA
		if trackA.TotalCapital != 0 {
			t.Errorf("Config %d: TrackA.TotalCapital = %d, want 0 (inactive)",
				i, trackA.TotalCapital)
		}

		if len(trackA.YearlyBudgets) != 0 {
			t.Errorf("Config %d: TrackA.YearlyBudgets count = %d, want 0 (inactive)",
				i, len(trackA.YearlyBudgets))
		}

		// Track B threshold derived from property price (20% down payment)
		trackB := point.ReinvestmentPlan.TrackB
		if trackB.Threshold <= 0 {
			t.Errorf("Config %d: TrackB.Threshold = %d, want > 0", i, trackB.Threshold)
		}
	}

	t.Logf("✅ All %d frontier points have Track A inactive as expected", len(frontierPoints))
}

// TestFrontier_ReinvestmentPlan_SingleYearBudget tests Track A with single year
func TestFrontier_ReinvestmentPlan_SingleYearBudget(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger, nil)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil, nil, nil)

	properties := []investment.ScoredProperty{
		{Property: investment.Property{ID: "p1", City: "Austin", State: "TX", Price: 300000, EstimatedRent: 2000}},
		{Property: investment.Property{ID: "p2", City: "Austin", State: "TX", Price: 350000, EstimatedRent: 2200}},
		{Property: investment.Property{ID: "p3", City: "Denver", State: "CO", Price: 400000, EstimatedRent: 2500}},
		{Property: investment.Property{ID: "p4", City: "Denver", State: "CO", Price: 450000, EstimatedRent: 2800}},
		{Property: investment.Property{ID: "p5", City: "Phoenix", State: "AZ", Price: 320000, EstimatedRent: 2100}},
	}

	profile := investment.InvestorProfile{
		Strategy:      investment.StrategyCashFlow,
		RiskTolerance: investment.RiskModerate,
	}

	// Single year budget
	params := investment.InvestmentPlanningParams{
		YearlyBudgets: []investment.YearlyBudget{
			{Year: 3, Budget: 200000},
		},
	}

	ctx := context.Background()
	cohorts := testBuildCohorts(properties, profile.Strategy, profile.RiskTolerance, 0)
	frontierPoints, err := fo.GenerateFrontier(ctx, cohorts, profile, params, nil)

	if err != nil {
		t.Fatalf("GenerateFrontier() error = %v", err)
	}

	if len(frontierPoints) == 0 {
		t.Fatal("Expected at least one frontier point")
	}

	// Verify Track A has single budget
	for i, point := range frontierPoints {
		if point.ReinvestmentPlan == nil {
			t.Errorf("FrontierPoint[%d].ReinvestmentPlan is nil", i)
			continue
		}

		trackA := point.ReinvestmentPlan.TrackA
		if trackA.TotalCapital != 200000 {
			t.Errorf("Config %d: TrackA.TotalCapital = %d, want 200000",
				i, trackA.TotalCapital)
		}

		if len(trackA.YearlyBudgets) != 1 {
			t.Errorf("Config %d: TrackA.YearlyBudgets count = %d, want 1",
				i, len(trackA.YearlyBudgets))
			continue
		}

		if trackA.YearlyBudgets[0].Year != 3 {
			t.Errorf("Config %d: YearlyBudget.Year = %d, want 3",
				i, trackA.YearlyBudgets[0].Year)
		}

		if trackA.YearlyBudgets[0].Budget != 200000 {
			t.Errorf("Config %d: YearlyBudget.Budget = %d, want 200000",
				i, trackA.YearlyBudgets[0].Budget)
		}
	}

	t.Logf("✅ All %d frontier points have single-year Track A budget", len(frontierPoints))
}
