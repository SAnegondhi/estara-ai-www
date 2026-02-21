package projection

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/estara-ai/www/internal/services/investment"
)

// ReinvestmentModeler calculates dual-track reinvestment plans for frontier configurations
// ADR-088 Phase 5: Track A (external capital) + Track B (internal cash flow)
type ReinvestmentModeler struct {
	logger *slog.Logger
	// TODO Phase 5: Add market data service for acquisition pricing
}

// NewReinvestmentModeler creates a new reinvestment calculator
func NewReinvestmentModeler(logger *slog.Logger) *ReinvestmentModeler {
	return &ReinvestmentModeler{
		logger: logger,
	}
}

// CalculateReinvestmentPlan generates dual-track reinvestment plan for a frontier configuration
// Track A: User-declared yearly budgets (deterministic)
// Track B: Internal cash flow reinvestment (per-path timing, threshold-based)
func (rm *ReinvestmentModeler) CalculateReinvestmentPlan(
	ctx context.Context,
	config *investment.FrontierPoint,
	params investment.InvestmentPlanningParams,
) (*investment.DualTrackReinvestment, error) {
	rm.logger.Info("calculating dual-track reinvestment plan",
		"configIndex", config.ConfigIndex,
		"yearlyBudgets", len(params.YearlyBudgets),
	)

	// Track A: External capital (user-declared budgets)
	trackA := rm.calculateTrackA(params.YearlyBudgets)

	// Track B: Internal cash flow (threshold-based)
	// Phase 5: Placeholder until Monte Carlo is implemented (Phase 6)
	trackB := rm.calculateTrackB(config)

	plan := &investment.DualTrackReinvestment{
		TrackA: trackA,
		TrackB: trackB,
	}

	rm.logger.Info("dual-track reinvestment plan calculated",
		"trackATotalCapital", trackA.TotalCapital,
		"trackBThreshold", trackB.Threshold,
	)

	return plan, nil
}

// calculateTrackA computes external capital reinvestment schedule
// Track A fires deterministically at user-declared years across all MC paths
func (rm *ReinvestmentModeler) calculateTrackA(yearlyBudgets []investment.YearlyBudget) investment.TrackAReinvestment {
	// If no yearly budgets provided, Track A is inactive
	if len(yearlyBudgets) == 0 {
		return investment.TrackAReinvestment{
			YearlyBudgets: []investment.YearlyBudget{},
			TotalCapital:  0,
		}
	}

	// Calculate total capital across all years
	totalCapital := 0
	for _, budget := range yearlyBudgets {
		totalCapital += budget.Budget
	}

	return investment.TrackAReinvestment{
		YearlyBudgets: yearlyBudgets,
		TotalCapital:  totalCapital,
	}
}

// calculateTrackB computes internal cash flow reinvestment parameters
// Track B fires at different times across MC paths (per-path timing)
// Phase 5: Placeholder implementation until Monte Carlo (Phase 6)
func (rm *ReinvestmentModeler) calculateTrackB(config *investment.FrontierPoint) investment.TrackBReinvestment {
	// Default threshold: $50,000 cumulative cash flow
	const defaultThreshold = 50000

	// Phase 5 Placeholder: These will be computed by Monte Carlo in Phase 6
	// For now, return conservative estimates
	return investment.TrackBReinvestment{
		Threshold:            defaultThreshold,
		MedianFiredYear:      0,  // 0 = unknown/not fired (Phase 6 will compute)
		FiredProbability:     0.0, // Phase 6 will compute % of paths where Track B fires
		ExpectedAcquisitions: 0,  // Phase 6 will compute expected # of properties
	}
}

// AcquisitionPricer provides market-based pricing for simulated property acquisitions
// ADR-088 Phase 5: Market median pricing for both Track A and Track B acquisitions
type AcquisitionPricer struct {
	logger *slog.Logger
	// TODO Phase 5: Add market data service for median price/rent lookups
}

// NewAcquisitionPricer creates a new acquisition pricer
func NewAcquisitionPricer(logger *slog.Logger) *AcquisitionPricer {
	return &AcquisitionPricer{
		logger: logger,
	}
}

// GetMarketMedianPrice returns median home price for a market
// Used for Track A (deterministic) pricing
func (ap *AcquisitionPricer) GetMarketMedianPrice(ctx context.Context, city, state string) (int, error) {
	// TODO Phase 5: Query market data service for median price
	// For now, return placeholder
	ap.logger.Warn("using placeholder median price",
		"city", city,
		"state", state,
		"reason", "market data service not yet integrated",
	)
	return 400000, nil // Placeholder: $400K median
}

// GetMarketMedianRent returns median monthly rent for a market
// Used for Track A (deterministic) pricing
func (ap *AcquisitionPricer) GetMarketMedianRent(ctx context.Context, city, state string) (int, error) {
	// TODO Phase 5: Query market data service for median rent
	// For now, return placeholder
	ap.logger.Warn("using placeholder median rent",
		"city", city,
		"state", state,
		"reason", "market data service not yet integrated",
	)
	return 2500, nil // Placeholder: $2,500/mo median rent
}

// EstimatePropertyAge returns estimated property age for acquisition year
// ADR-088 Phase 5 spec: 15 years for later acquisitions, 20 years for initial
func (ap *AcquisitionPricer) EstimatePropertyAge(acquisitionYear int) int {
	if acquisitionYear <= 1 {
		return 20 // Initial acquisition: assume 20 years old
	}
	return 15 // Later acquisitions: assume 15 years old
}

// PropertyCohort represents a group of properties acquired in the same year
// Used by both Track A (deterministic) and Track B (per-path)
type PropertyCohort struct {
	Year             int     `json:"year"`             // Acquisition year (0 = initial, 1-10 = reinvestment)
	PropertyCount    int     `json:"propertyCount"`    // Number of properties in cohort
	CapitalDeployed  int     `json:"capitalDeployed"`  // Total capital deployed
	MedianPrice      int     `json:"medianPrice"`      // Median price per property
	MedianRent       int     `json:"medianRent"`       // Median monthly rent per property
	PropertyAge      int     `json:"propertyAge"`      // Estimated property age at acquisition
	Track            string  `json:"track"`            // "A" (external) or "B" (internal)
	ExpectedCashFlow int     `json:"expectedCashFlow"` // Projected monthly cash flow (cohort total)
}

// CreateCohort creates a property cohort for Track A acquisition
// ADR-088 Phase 5: Deterministic cohort creation at user-declared years
func (rm *ReinvestmentModeler) CreateCohort(
	ctx context.Context,
	year int,
	budget int,
	pricer *AcquisitionPricer,
	primaryMarket string, // "City, State" format
) (*PropertyCohort, error) {
	// TODO Phase 5: Parse primaryMarket to extract city/state
	// For now, use placeholder values
	city := "Austin"
	state := "TX"

	// Get market median pricing
	medianPrice, err := pricer.GetMarketMedianPrice(ctx, city, state)
	if err != nil {
		return nil, fmt.Errorf("failed to get median price: %w", err)
	}

	medianRent, err := pricer.GetMarketMedianRent(ctx, city, state)
	if err != nil {
		return nil, fmt.Errorf("failed to get median rent: %w", err)
	}

	// Calculate property count (assume 20% down payment)
	downPaymentPct := 0.20
	capitalPerProperty := int(float64(medianPrice) * downPaymentPct)
	propertyCount := budget / capitalPerProperty

	if propertyCount == 0 {
		rm.logger.Warn("insufficient budget for cohort",
			"budget", budget,
			"medianPrice", medianPrice,
			"capitalPerProperty", capitalPerProperty,
		)
		return nil, fmt.Errorf("insufficient budget: need at least $%d for 1 property", capitalPerProperty)
	}

	// Estimate property age
	propertyAge := pricer.EstimatePropertyAge(year)

	// Calculate expected cash flow
	monthlyRent := medianRent
	estimatedExpenses := int(float64(medianPrice) * 0.005) // 0.5% of value per month
	monthlyCashFlow := monthlyRent - estimatedExpenses
	cohortTotalCashFlow := monthlyCashFlow * propertyCount

	cohort := &PropertyCohort{
		Year:             year,
		PropertyCount:    propertyCount,
		CapitalDeployed:  budget,
		MedianPrice:      medianPrice,
		MedianRent:       medianRent,
		PropertyAge:      propertyAge,
		Track:            "A", // External capital
		ExpectedCashFlow: cohortTotalCashFlow,
	}

	rm.logger.Info("created Track A cohort",
		"year", year,
		"propertyCount", propertyCount,
		"capitalDeployed", budget,
		"medianPrice", medianPrice,
		"propertyAge", propertyAge,
	)

	return cohort, nil
}

// PathState tracks per-path state for Monte Carlo simulation
// ADR-088 Phase 5: Required for Track B per-path timing
type PathState struct {
	PathID            int                     `json:"pathId"`            // MC path identifier (0-999)
	CumulativeCash    []int                   `json:"cumulativeCash"`    // Cumulative cash flow by year
	TrackBCohorts     []PropertyCohort        `json:"trackBCohorts"`     // Track B acquisitions for this path
	TrackBFiredYear   int                     `json:"trackBFiredYear"`   // Year Track B fired (0 = never)
	MarketPrices      map[string][]int        `json:"marketPrices"`      // Per-path market prices by year
}

// NewPathState creates initial path state for Monte Carlo path
func NewPathState(pathID int, projectionYears int) *PathState {
	return &PathState{
		PathID:          pathID,
		CumulativeCash:  make([]int, projectionYears),
		TrackBCohorts:   []PropertyCohort{},
		TrackBFiredYear: 0,
		MarketPrices:    make(map[string][]int),
	}
}

// CheckTrackBThreshold checks if cumulative cash flow has reached threshold
// Returns: (fired bool, year int) - year is 1-indexed (0 = never fired)
func (ps *PathState) CheckTrackBThreshold(threshold int) (bool, int) {
	for year, cumCash := range ps.CumulativeCash {
		if cumCash >= threshold {
			return true, year + 1 // Convert to 1-indexed year
		}
	}
	return false, 0
}

// AddTrackBCohort adds a Track B cohort to this path's state
func (ps *PathState) AddTrackBCohort(cohort PropertyCohort) {
	ps.TrackBCohorts = append(ps.TrackBCohorts, cohort)
	ps.TrackBFiredYear = cohort.Year
}
