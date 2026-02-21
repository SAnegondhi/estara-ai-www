package projection

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
)

func TestCalculateTrackA(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	rm := NewReinvestmentModeler(logger)

	tests := []struct {
		name           string
		yearlyBudgets  []investment.YearlyBudget
		expectedTotal  int
		expectedBudgets int
	}{
		{
			name: "no budgets - Track A inactive",
			yearlyBudgets: []investment.YearlyBudget{},
			expectedTotal: 0,
			expectedBudgets: 0,
		},
		{
			name: "single year budget",
			yearlyBudgets: []investment.YearlyBudget{
				{Year: 2, Budget: 100000},
			},
			expectedTotal: 100000,
			expectedBudgets: 1,
		},
		{
			name: "multiple year budgets",
			yearlyBudgets: []investment.YearlyBudget{
				{Year: 2, Budget: 100000},
				{Year: 3, Budget: 150000},
				{Year: 5, Budget: 200000},
			},
			expectedTotal: 450000,
			expectedBudgets: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trackA := rm.calculateTrackA(tt.yearlyBudgets)

			if trackA.TotalCapital != tt.expectedTotal {
				t.Errorf("TotalCapital = %d, want %d", trackA.TotalCapital, tt.expectedTotal)
			}

			if len(trackA.YearlyBudgets) != tt.expectedBudgets {
				t.Errorf("YearlyBudgets count = %d, want %d", len(trackA.YearlyBudgets), tt.expectedBudgets)
			}
		})
	}
}

func TestCalculateTrackB_Placeholder(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	rm := NewReinvestmentModeler(logger)

	config := &investment.FrontierPoint{
		ConfigIndex: 0,
		Properties: []investment.PropertyInPortfolio{
			{
				Property: investment.Property{
					ID: "p1",
					City: "Austin",
					State: "TX",
					Price: 400000,
				},
			},
		},
	}

	trackB := rm.calculateTrackB(config)

	// Phase 5: Verify placeholder values
	if trackB.Threshold != 50000 {
		t.Errorf("Threshold = %d, want 50000", trackB.Threshold)
	}

	if trackB.MedianFiredYear != 0 {
		t.Errorf("MedianFiredYear = %d, want 0 (placeholder)", trackB.MedianFiredYear)
	}

	if trackB.FiredProbability != 0.0 {
		t.Errorf("FiredProbability = %.2f, want 0.0 (placeholder)", trackB.FiredProbability)
	}

	if trackB.ExpectedAcquisitions != 0 {
		t.Errorf("ExpectedAcquisitions = %d, want 0 (placeholder)", trackB.ExpectedAcquisitions)
	}
}

func TestCalculateReinvestmentPlan(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	rm := NewReinvestmentModeler(logger)

	config := &investment.FrontierPoint{
		ConfigIndex: 0,
		Properties: []investment.PropertyInPortfolio{
			{
				Property: investment.Property{
					ID: "p1",
					City: "Austin",
					State: "TX",
					Price: 400000,
				},
			},
		},
	}

	params := investment.InvestmentPlanningParams{
		YearlyBudgets: []investment.YearlyBudget{
			{Year: 2, Budget: 100000},
			{Year: 3, Budget: 150000},
		},
	}

	ctx := context.Background()
	plan, err := rm.CalculateReinvestmentPlan(ctx, config, params)

	if err != nil {
		t.Fatalf("CalculateReinvestmentPlan() error = %v", err)
	}

	if plan == nil {
		t.Fatal("plan should not be nil")
	}

	// Verify Track A
	if plan.TrackA.TotalCapital != 250000 {
		t.Errorf("TrackA.TotalCapital = %d, want 250000", plan.TrackA.TotalCapital)
	}

	if len(plan.TrackA.YearlyBudgets) != 2 {
		t.Errorf("TrackA.YearlyBudgets count = %d, want 2", len(plan.TrackA.YearlyBudgets))
	}

	// Verify Track B (placeholder)
	if plan.TrackB.Threshold != 50000 {
		t.Errorf("TrackB.Threshold = %d, want 50000", plan.TrackB.Threshold)
	}
}

func TestEstimatePropertyAge(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	pricer := NewAcquisitionPricer(logger)

	tests := []struct {
		name            string
		acquisitionYear int
		expectedAge     int
	}{
		{
			name:            "initial acquisition (year 0)",
			acquisitionYear: 0,
			expectedAge:     20,
		},
		{
			name:            "initial acquisition (year 1)",
			acquisitionYear: 1,
			expectedAge:     20,
		},
		{
			name:            "later acquisition (year 2)",
			acquisitionYear: 2,
			expectedAge:     15,
		},
		{
			name:            "later acquisition (year 5)",
			acquisitionYear: 5,
			expectedAge:     15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			age := pricer.EstimatePropertyAge(tt.acquisitionYear)

			if age != tt.expectedAge {
				t.Errorf("EstimatePropertyAge(%d) = %d, want %d",
					tt.acquisitionYear, age, tt.expectedAge)
			}
		})
	}
}

func TestCreateCohort(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	rm := NewReinvestmentModeler(logger)
	pricer := NewAcquisitionPricer(logger)

	ctx := context.Background()

	tests := []struct {
		name                  string
		year                  int
		budget                int
		expectedPropertyCount int
		shouldError           bool
	}{
		{
			name:                  "sufficient budget for multiple properties",
			year:                  2,
			budget:                300000, // $300K budget
			expectedPropertyCount: 3,      // At $400K median, 20% down = $80K per property
			shouldError:           false,
		},
		{
			name:                  "minimal budget for one property",
			year:                  3,
			budget:                80000,  // Exactly 1 property
			expectedPropertyCount: 1,
			shouldError:           false,
		},
		{
			name:                  "insufficient budget",
			year:                  4,
			budget:                50000,  // Not enough for even 1 property
			expectedPropertyCount: 0,
			shouldError:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cohort, err := rm.CreateCohort(ctx, tt.year, tt.budget, pricer, "Austin, TX")

			if tt.shouldError {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("CreateCohort() error = %v", err)
			}

			if cohort == nil {
				t.Fatal("cohort should not be nil")
			}

			// Verify property count
			if cohort.PropertyCount != tt.expectedPropertyCount {
				t.Errorf("PropertyCount = %d, want %d", cohort.PropertyCount, tt.expectedPropertyCount)
			}

			// Verify year
			if cohort.Year != tt.year {
				t.Errorf("Year = %d, want %d", cohort.Year, tt.year)
			}

			// Verify capital deployed
			if cohort.CapitalDeployed != tt.budget {
				t.Errorf("CapitalDeployed = %d, want %d", cohort.CapitalDeployed, tt.budget)
			}

			// Verify track
			if cohort.Track != "A" {
				t.Errorf("Track = %s, want A", cohort.Track)
			}

			// Verify median price/rent (placeholders)
			if cohort.MedianPrice != 400000 {
				t.Errorf("MedianPrice = %d, want 400000 (placeholder)", cohort.MedianPrice)
			}

			if cohort.MedianRent != 2500 {
				t.Errorf("MedianRent = %d, want 2500 (placeholder)", cohort.MedianRent)
			}

			// Verify property age
			expectedAge := 20
			if tt.year > 1 {
				expectedAge = 15
			}
			if cohort.PropertyAge != expectedAge {
				t.Errorf("PropertyAge = %d, want %d", cohort.PropertyAge, expectedAge)
			}
		})
	}
}

func TestPathState_NewPathState(t *testing.T) {
	pathID := 42
	projectionYears := 10

	ps := NewPathState(pathID, projectionYears)

	if ps.PathID != pathID {
		t.Errorf("PathID = %d, want %d", ps.PathID, pathID)
	}

	if len(ps.CumulativeCash) != projectionYears {
		t.Errorf("CumulativeCash length = %d, want %d", len(ps.CumulativeCash), projectionYears)
	}

	if len(ps.TrackBCohorts) != 0 {
		t.Errorf("TrackBCohorts should be empty initially, got %d", len(ps.TrackBCohorts))
	}

	if ps.TrackBFiredYear != 0 {
		t.Errorf("TrackBFiredYear = %d, want 0 (not fired)", ps.TrackBFiredYear)
	}

	if ps.MarketPrices == nil {
		t.Error("MarketPrices should be initialized")
	}
}

func TestPathState_CheckTrackBThreshold(t *testing.T) {
	tests := []struct {
		name           string
		cumulativeCash []int
		threshold      int
		expectedFired  bool
		expectedYear   int
	}{
		{
			name:           "threshold never reached",
			cumulativeCash: []int{10000, 20000, 30000, 40000},
			threshold:      50000,
			expectedFired:  false,
			expectedYear:   0,
		},
		{
			name:           "threshold reached in year 3",
			cumulativeCash: []int{10000, 30000, 55000, 80000},
			threshold:      50000,
			expectedFired:  true,
			expectedYear:   3, // 1-indexed (index 2)
		},
		{
			name:           "threshold reached in year 1",
			cumulativeCash: []int{60000, 80000, 100000},
			threshold:      50000,
			expectedFired:  true,
			expectedYear:   1, // 1-indexed (index 0)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := &PathState{
				PathID:         0,
				CumulativeCash: tt.cumulativeCash,
			}

			fired, year := ps.CheckTrackBThreshold(tt.threshold)

			if fired != tt.expectedFired {
				t.Errorf("fired = %v, want %v", fired, tt.expectedFired)
			}

			if year != tt.expectedYear {
				t.Errorf("year = %d, want %d", year, tt.expectedYear)
			}
		})
	}
}

func TestPathState_AddTrackBCohort(t *testing.T) {
	ps := NewPathState(0, 10)

	cohort := PropertyCohort{
		Year:            3,
		PropertyCount:   2,
		CapitalDeployed: 150000,
		Track:           "B",
	}

	ps.AddTrackBCohort(cohort)

	// Verify cohort was added
	if len(ps.TrackBCohorts) != 1 {
		t.Errorf("TrackBCohorts length = %d, want 1", len(ps.TrackBCohorts))
	}

	// Verify fired year was set
	if ps.TrackBFiredYear != 3 {
		t.Errorf("TrackBFiredYear = %d, want 3", ps.TrackBFiredYear)
	}

	// Verify cohort details
	if ps.TrackBCohorts[0].Year != 3 {
		t.Errorf("cohort Year = %d, want 3", ps.TrackBCohorts[0].Year)
	}

	if ps.TrackBCohorts[0].PropertyCount != 2 {
		t.Errorf("cohort PropertyCount = %d, want 2", ps.TrackBCohorts[0].PropertyCount)
	}
}

func TestPathState_MultipleTrackBCohorts(t *testing.T) {
	ps := NewPathState(0, 10)

	// Add first cohort
	cohort1 := PropertyCohort{
		Year:            3,
		PropertyCount:   2,
		CapitalDeployed: 100000,
		Track:           "B",
	}
	ps.AddTrackBCohort(cohort1)

	// Add second cohort
	cohort2 := PropertyCohort{
		Year:            6,
		PropertyCount:   3,
		CapitalDeployed: 150000,
		Track:           "B",
	}
	ps.AddTrackBCohort(cohort2)

	// Verify both cohorts exist
	if len(ps.TrackBCohorts) != 2 {
		t.Errorf("TrackBCohorts length = %d, want 2", len(ps.TrackBCohorts))
	}

	// Verify fired year is updated to latest
	if ps.TrackBFiredYear != 6 {
		t.Errorf("TrackBFiredYear = %d, want 6 (latest)", ps.TrackBFiredYear)
	}

	// Verify total property count
	totalProperties := ps.TrackBCohorts[0].PropertyCount + ps.TrackBCohorts[1].PropertyCount
	if totalProperties != 5 {
		t.Errorf("total properties = %d, want 5", totalProperties)
	}
}
