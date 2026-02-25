package optimization

// ADR-090 Phase B+D+E: Acceptance criteria test suite.
//
// Verifies that per-config property selection pipelines + strategy-aware Markowitz weights
// produce meaningfully differentiated frontier points and property cohorts.
//
// Acceptance criteria (from ADR-090):
//  1. Distinct metrics     — FrontierPoints differ by ≥0.05 Sharpe and ≥0.3% expected return.
//  2. Distinct property sets — cohorts share ≤40% properties (Jaccard similarity ≤ 0.40).
//  3. Strategy sensitivity — CashFlow vs Appreciation produce different quality cohort rankings.
//  4. Risk sensitivity     — Conservative IncomePipeline excludes low-DSCR properties when
//     enough high-DSCR alternatives exist; Aggressive applies no floor.
//  5. Label correctness    — GenerateLabel returns the right human-readable label.
//  6. Recalculate parity   — assumption overrides preserve relative Sharpe ordering.
//  7. Label flow           — FrontierPoint.Label is non-empty and matches GenerateLabel output.
//  8. Growth cohort        — SelectGrowthCohort is used for Appreciation strategy; favours high-price props.
//  9. Growth-Income Jaccard — Growth and Income cohorts are distinct (Jaccard ≤ 0.40).
//
// Note on income formula equivalence:
//   At a fixed mortgage rate and down payment fraction, CapRate, DSCR, and CoC are
//   mathematically proportional (DSCR = CapRate / (LTV × annualMortgageConstant),
//   CoC = (CapRate - constant) / dpPct). Strategy weights on the Income formula therefore
//   cannot change the ranking order — only DSCR floors produce meaningful differentiation
//   in the Income cohort. Strategy-driven ranking differences arise in the Quality cohort
//   via the AI-score modifier (+0.2×normCoC for CashFlow, +0.2×normDownPayment for
//   Appreciation), which injects a price-based dimension independent of yield metrics.

import (
	"context"
	"log/slog"
	"math"
	"os"
	"sort"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/projection"
)

// ─── Test constants ──────────────────────────────────────────────────────────

// testMortgageRate uses 4% so that income properties (low price, high rent) achieve
// DSCR ≥ 1.3 while quality properties (high price, lower yield) remain below 1.0.
const testMortgageRate = 0.04
const testDownPayment = 0.20

// ─── Test data ───────────────────────────────────────────────────────────────

// buildDiverseProperties returns 21 scored properties spanning 4 cities.
//
// Design invariants (all at 4% mortgage, 20% down):
//
// "Quality" properties (IDs q1–q7): AI score 80-92, Price $420K-$580K
//   → DSCR ≈ 0.5-0.7 (fail 1.0 / 1.3 floors); cap rate 3-5%
//   → Two sub-groups:
//       q1-q3: very high AI (88-92), very high price ($540K-$580K) → high DownPayment
//       q4-q7: high AI (80-86), moderate price ($420K-$480K) → lower DownPayment
//
// "Income" properties (IDs i1–i8): AI score 40-55, Price $150K-$200K
//   → DSCR ≈ 1.6-2.2 (clear 1.3 floor at 4%); cap rate 7-11%
//   → Include 2 "mixed" IDs (i7, i8) that also have AI scores near 78-79 to
//     border Quality+Income for Balanced cohort.
//
// "Middle" properties (IDs m1–m6): moderate on both axes.
//   → Appear in Balanced cohort; enough to ensure Jaccard(Q,I) < 0.40.
func buildDiverseProperties() []investment.ScoredProperty {
	return []investment.ScoredProperty{
		// ── Quality group: high AI score, expensive, low yield ──────────────
		// q1-q3: highest AI, highest price → Appreciation modifier strongly prefers these
		{Property: investment.Property{ID: "q1", City: "Austin", State: "TX", Price: 580000, EstimatedRent: 2300, PropertyType: "Single Family", YearBuilt: 2020, Latitude: 30.27, Longitude: -97.74}, OverallScore: 92},
		{Property: investment.Property{ID: "q2", City: "Denver", State: "CO", Price: 560000, EstimatedRent: 2200, PropertyType: "Condo", YearBuilt: 2021, Latitude: 39.74, Longitude: -104.98}, OverallScore: 90},
		{Property: investment.Property{ID: "q3", City: "Austin", State: "TX", Price: 540000, EstimatedRent: 2200, PropertyType: "Single Family", YearBuilt: 2019, Latitude: 30.28, Longitude: -97.73}, OverallScore: 88},
		// q4-q7: slightly lower AI, lower price → CashFlow modifier can re-order vs q1-q3
		{Property: investment.Property{ID: "q4", City: "Denver", State: "CO", Price: 480000, EstimatedRent: 2100, PropertyType: "Townhouse", YearBuilt: 2018, Latitude: 39.73, Longitude: -104.99}, OverallScore: 86},
		{Property: investment.Property{ID: "q5", City: "Phoenix", State: "AZ", Price: 460000, EstimatedRent: 2100, PropertyType: "Single Family", YearBuilt: 2020, Latitude: 33.45, Longitude: -112.07}, OverallScore: 84},
		{Property: investment.Property{ID: "q6", City: "Austin", State: "TX", Price: 440000, EstimatedRent: 2000, PropertyType: "Condo", YearBuilt: 2017, Latitude: 30.26, Longitude: -97.72}, OverallScore: 82},
		{Property: investment.Property{ID: "q7", City: "Denver", State: "CO", Price: 420000, EstimatedRent: 2000, PropertyType: "Single Family", YearBuilt: 2020, Latitude: 39.72, Longitude: -104.97}, OverallScore: 80},

		// ── Income group: low AI, low price, high yield, DSCR > 1.5 at 4% ──
		{Property: investment.Property{ID: "i1", City: "Houston", State: "TX", Price: 150000, EstimatedRent: 1900, PropertyType: "Townhouse", YearBuilt: 2018, Latitude: 29.76, Longitude: -95.37}, OverallScore: 55},
		{Property: investment.Property{ID: "i2", City: "Houston", State: "TX", Price: 160000, EstimatedRent: 2000, PropertyType: "Single Family", YearBuilt: 2019, Latitude: 29.75, Longitude: -95.36}, OverallScore: 52},
		{Property: investment.Property{ID: "i3", City: "Houston", State: "TX", Price: 170000, EstimatedRent: 2100, PropertyType: "Single Family", YearBuilt: 2020, Latitude: 29.74, Longitude: -95.35}, OverallScore: 50},
		{Property: investment.Property{ID: "i4", City: "Houston", State: "TX", Price: 175000, EstimatedRent: 2100, PropertyType: "Condo", YearBuilt: 2017, Latitude: 29.73, Longitude: -95.34}, OverallScore: 48},
		{Property: investment.Property{ID: "i5", City: "Houston", State: "TX", Price: 180000, EstimatedRent: 2200, PropertyType: "Townhouse", YearBuilt: 2021, Latitude: 29.72, Longitude: -95.33}, OverallScore: 46},
		{Property: investment.Property{ID: "i6", City: "Houston", State: "TX", Price: 190000, EstimatedRent: 2300, PropertyType: "Single Family", YearBuilt: 2020, Latitude: 29.71, Longitude: -95.32}, OverallScore: 44},
		{Property: investment.Property{ID: "i7", City: "Houston", State: "TX", Price: 195000, EstimatedRent: 2400, PropertyType: "Townhouse", YearBuilt: 2022, Latitude: 29.70, Longitude: -95.31}, OverallScore: 42},
		{Property: investment.Property{ID: "i8", City: "Houston", State: "TX", Price: 200000, EstimatedRent: 2500, PropertyType: "Single Family", YearBuilt: 2021, Latitude: 29.69, Longitude: -95.30}, OverallScore: 40},

		// ── Middle group: moderate on both axes ──────────────────────────────
		{Property: investment.Property{ID: "m1", City: "Phoenix", State: "AZ", Price: 300000, EstimatedRent: 2100, PropertyType: "Single Family", YearBuilt: 2016, Latitude: 33.44, Longitude: -112.06}, OverallScore: 68},
		{Property: investment.Property{ID: "m2", City: "Phoenix", State: "AZ", Price: 310000, EstimatedRent: 2200, PropertyType: "Condo", YearBuilt: 2018, Latitude: 33.43, Longitude: -112.05}, OverallScore: 65},
		{Property: investment.Property{ID: "m3", City: "Denver", State: "CO", Price: 320000, EstimatedRent: 2200, PropertyType: "Townhouse", YearBuilt: 2017, Latitude: 39.71, Longitude: -104.96}, OverallScore: 62},
		{Property: investment.Property{ID: "m4", City: "Austin", State: "TX", Price: 280000, EstimatedRent: 2000, PropertyType: "Single Family", YearBuilt: 2015, Latitude: 30.25, Longitude: -97.71}, OverallScore: 60},
		{Property: investment.Property{ID: "m5", City: "Phoenix", State: "AZ", Price: 290000, EstimatedRent: 2100, PropertyType: "Single Family", YearBuilt: 2019, Latitude: 33.42, Longitude: -112.04}, OverallScore: 58},
		{Property: investment.Property{ID: "m6", City: "Denver", State: "CO", Price: 260000, EstimatedRent: 1900, PropertyType: "Condo", YearBuilt: 2016, Latitude: 39.70, Longitude: -104.95}, OverallScore: 56},
	}
}

// jaccardSimilarity computes |A ∩ B| / |A ∪ B| for two RankedProperty slices.
func jaccardSimilarity(a, b []investment.RankedProperty) float64 {
	setA := make(map[string]struct{}, len(a))
	for _, rp := range a {
		setA[rp.Property.ID] = struct{}{}
	}
	inter := 0
	setB := make(map[string]struct{}, len(b))
	for _, rp := range b {
		setB[rp.Property.ID] = struct{}{}
		if _, ok := setA[rp.Property.ID]; ok {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// ─── Acceptance Criterion 2: Distinct property sets ─────────────────────────

// TestBuildCohorts_DistinctPropertySets verifies that the Quality and Income cohorts
// produced by BuildCohorts share ≤40% of their properties (Jaccard similarity ≤ 0.40).
//
// With 21 properties and cohortSize=15, Quality takes top-15 by AI score (mostly q1-q7
// + top-8 middle/income by score), Income takes top-15 by cap rate (mostly i1-i8 + some
// middle). The AI-score-ranked group and cap-rate-ranked group diverge because quality
// properties have low cap rates and income properties have low AI scores.
func TestBuildCohorts_DistinctPropertySets(t *testing.T) {
	props := buildDiverseProperties()

	cohorts := investment.BuildCohorts(
		props,
		nil,
		investment.StrategyBalanced,
		investment.RiskModerate,
		2_000_000,
		testMortgageRate,
		testDownPayment,
	)
	if len(cohorts) < 3 {
		t.Fatalf("expected 3 cohorts, got %d", len(cohorts))
	}

	quality := cohorts[0].Properties
	income := cohorts[1].Properties
	balanced := cohorts[2].Properties

	qiSim := jaccardSimilarity(quality, income)
	qbSim := jaccardSimilarity(quality, balanced)
	ibSim := jaccardSimilarity(income, balanced)

	t.Logf("Quality cohort size=%d, Income size=%d, Balanced size=%d", len(quality), len(income), len(balanced))
	t.Logf("Jaccard: Quality↔Income=%.2f, Quality↔Balanced=%.2f, Income↔Balanced=%.2f",
		qiSim, qbSim, ibSim)

	const maxSimilarity = 0.40
	if qiSim > maxSimilarity {
		t.Errorf("Quality↔Income Jaccard=%.2f exceeds threshold %.2f — cohorts are not meaningfully distinct", qiSim, maxSimilarity)
	}

	// Quality and Income cohorts must each have ≥5 properties.
	if len(quality) < 5 {
		t.Errorf("Quality cohort has only %d properties, need ≥5", len(quality))
	}
	if len(income) < 5 {
		t.Errorf("Income cohort has only %d properties, need ≥5", len(income))
	}
}

// ─── Acceptance Criterion 4: Risk sensitivity (DSCR floors) ─────────────────

// TestSelectIncomeCohort_ConservativeDSCRFloor verifies that the Income pipeline
// uses a DSCR floor for Conservative investors.
//
// At 4% mortgage rate, income properties (i1-i8, price $150K-$200K, rent $1.9K-$2.5K)
// have DSCR ≈ 1.6-2.5, well above the 1.3 floor.
// Quality properties (q1-q7, price $420K-$580K) have DSCR ≈ 0.5-0.7, failing the floor.
//
// The Conservative cohort should therefore contain only income + some middle properties,
// NOT quality properties (which fail the DSCR floor and are excluded).
func TestSelectIncomeCohort_ConservativeDSCRFloor(t *testing.T) {
	props := buildDiverseProperties()
	affordable := investment.FilterAffordable(props, 2_000_000, testDownPayment, testMortgageRate)

	conservativeIncome := investment.SelectIncomeCohort(affordable, investment.StrategyBalanced, investment.RiskConservative, 15)
	aggressiveIncome := investment.SelectIncomeCohort(affordable, investment.StrategyBalanced, investment.RiskAggressive, 15)

	t.Logf("Conservative Income: %d properties", len(conservativeIncome.Properties))
	t.Logf("Aggressive Income: %d properties", len(aggressiveIncome.Properties))

	// Check: all properties in Conservative cohort must have DSCR ≥ 1.3.
	// If the fallback kicked in (not enough passed the floor), all properties would be present
	// and quality properties would appear with DSCR < 1.3 — that's a configuration error.
	conservativeHasLowDSCR := false
	for _, rp := range conservativeIncome.Properties {
		if rp.DSCR < 1.3 {
			conservativeHasLowDSCR = true
			t.Logf("  Conservative cohort: %s DSCR=%.3f (below 1.3 floor)", rp.Property.ID, rp.DSCR)
		}
	}
	if conservativeHasLowDSCR {
		t.Error("Conservative Income cohort contains properties below the 1.3 DSCR floor — floor is not being enforced (may need ≥5 qualifying properties)")
	}

	// Aggressive should have more or equal properties than Conservative (no floor applied).
	if len(aggressiveIncome.Properties) < len(conservativeIncome.Properties) {
		t.Errorf("Aggressive Income (%d) has fewer properties than Conservative (%d) — floor incorrectly applied to Aggressive",
			len(aggressiveIncome.Properties), len(conservativeIncome.Properties))
	}

	// Conservative should EXCLUDE properties that Aggressive includes (low-DSCR ones).
	conservativeIDs := make(map[string]bool)
	for _, rp := range conservativeIncome.Properties {
		conservativeIDs[rp.Property.ID] = true
	}
	exclusions := 0
	for _, rp := range aggressiveIncome.Properties {
		if !conservativeIDs[rp.Property.ID] && rp.DSCR < 1.3 {
			exclusions++
			t.Logf("  Aggressive includes low-DSCR property %s (DSCR=%.3f) excluded by Conservative", rp.Property.ID, rp.DSCR)
		}
	}
	if exclusions == 0 {
		// Not a hard failure — if all affordable properties happen to pass 1.3, that's fine.
		t.Log("  Note: no low-DSCR properties in Aggressive cohort to compare — all properties passed the 1.3 floor")
	} else {
		t.Logf("  Conservative excludes %d low-DSCR properties that Aggressive keeps ✓", exclusions)
	}
}

// ─── Acceptance Criterion 3: Strategy sensitivity ───────────────────────────

// TestSelectQualityCohort_StrategyDifferentiation verifies that CashFlow and Appreciation
// strategies produce different Quality cohort rankings.
//
// The Quality pipeline applies a strategy modifier:
//   CashFlow:     +0.2 × norm(CashOnCash)   → boosts low-price, high-yield properties
//   Appreciation: +0.2 × norm(DownPayment)  → boosts high-price properties
//
// With the test pool:
//   q1-q3: AI ≈ 88-92, Price $540K-$580K → very high DP, very low CoC
//   q4-q7: AI ≈ 80-86, Price $420K-$480K → moderate DP, slightly higher CoC
//   i1-i8: AI ≈ 40-55, Price $150K-$200K → very low DP, very high CoC
//
// Under CashFlow: the modifier boosts income properties' CoC (+0.2×1.0); high-score
//   quality properties still dominate, but the ordering within moderate-score properties
//   shifts toward low-price, high-yield choices.
// Under Appreciation: the modifier boosts q1-q3 even further (high DP), solidifying
//   their lead over all other properties.
//
// The full ranked lists should differ in ordering because the modifier tips the balance
// among properties with similar AI scores.
func TestSelectQualityCohort_StrategyDifferentiation(t *testing.T) {
	props := buildDiverseProperties()
	affordable := investment.FilterAffordable(props, 2_000_000, testDownPayment, testMortgageRate)
	if len(affordable) < 5 {
		t.Skip("insufficient affordable properties")
	}

	cashFlowQuality := investment.SelectQualityCohort(affordable, investment.StrategyCashFlow, investment.RiskModerate, 15)
	apprQuality := investment.SelectQualityCohort(affordable, investment.StrategyAppreciation, investment.RiskModerate, 15)

	if len(cashFlowQuality.Properties) == 0 || len(apprQuality.Properties) == 0 {
		t.Fatal("quality cohort must not be empty")
	}

	t.Logf("CashFlow Quality top-5: %v", propertyIDs(cashFlowQuality.Properties[:min(5, len(cashFlowQuality.Properties))]))
	t.Logf("Appreciation Quality top-5: %v", propertyIDs(apprQuality.Properties[:min(5, len(apprQuality.Properties))]))

	// The two strategies should produce different ordered lists.
	maxCheck := len(cashFlowQuality.Properties)
	if len(apprQuality.Properties) < maxCheck {
		maxCheck = len(apprQuality.Properties)
	}
	sameOrder := true
	for i := 0; i < maxCheck; i++ {
		if cashFlowQuality.Properties[i].Property.ID != apprQuality.Properties[i].Property.ID {
			sameOrder = false
			t.Logf("  First difference at position %d: CashFlow=%s, Appreciation=%s",
				i, cashFlowQuality.Properties[i].Property.ID, apprQuality.Properties[i].Property.ID)
			break
		}
	}
	if sameOrder && maxCheck > 1 {
		t.Errorf("CashFlow and Appreciation produced identical Quality cohort ranking — the AI-score modifier is not differentiating. maxCheck=%d", maxCheck)
	}
}

// propertyIDs returns a slice of property IDs for display in test logs.
func propertyIDs(props []investment.RankedProperty) []string {
	ids := make([]string, len(props))
	for i, rp := range props {
		ids[i] = rp.Property.ID
	}
	return ids
}

// TestBuildCohorts_LabelsSensitiveToStrategyAndRisk verifies that GenerateLabel
// produces the correct human-readable label per ADR-090 configuration table.
func TestBuildCohorts_LabelsSensitiveToStrategyAndRisk(t *testing.T) {
	props := buildDiverseProperties()

	cases := []struct {
		strategy    investment.InvestmentStrategy
		risk        investment.RiskTolerance
		wantQuality string
		wantIncome  string
		wantBalance string
	}{
		{investment.StrategyAppreciation, investment.RiskModerate, "Growth", "Income", "Balanced"},
		{investment.StrategyCashFlow, investment.RiskConservative, "Quality-CF", "Defensive Income", "Balanced"},
		{investment.StrategyRiskAdjusted, investment.RiskModerate, "Quality", "Income", "Risk-Balanced"},
		{investment.StrategyBalanced, investment.RiskModerate, "Quality", "Income", "Balanced"},
	}

	for _, tc := range cases {
		cohorts := investment.BuildCohorts(props, nil, tc.strategy, tc.risk, 2_000_000, testMortgageRate, testDownPayment)
		if len(cohorts) < 3 {
			t.Errorf("strategy=%v risk=%v: expected 3 cohorts, got %d", tc.strategy, tc.risk, len(cohorts))
			continue
		}
		if cohorts[0].Label != tc.wantQuality {
			t.Errorf("strategy=%v risk=%v: Quality label=%q, want %q", tc.strategy, tc.risk, cohorts[0].Label, tc.wantQuality)
		}
		if cohorts[1].Label != tc.wantIncome {
			t.Errorf("strategy=%v risk=%v: Income label=%q, want %q", tc.strategy, tc.risk, cohorts[1].Label, tc.wantIncome)
		}
		if cohorts[2].Label != tc.wantBalance {
			t.Errorf("strategy=%v risk=%v: Balanced label=%q, want %q", tc.strategy, tc.risk, cohorts[2].Label, tc.wantBalance)
		}
		t.Logf("strategy=%v risk=%v: labels=[%s, %s, %s] ✓",
			tc.strategy, tc.risk, cohorts[0].Label, cohorts[1].Label, cohorts[2].Label)
	}
}

// ─── Acceptance Criterion 1: Distinct FrontierPoint metrics ─────────────────

// TestFrontier_DistinctMetrics verifies that for a ≥10-property pool spanning ≥3 cities,
// the generated frontier points show spread in both ExpectedReturn and SharpeScore.
//
// Criterion: max−min Sharpe ≥ 0.05; max−min ExpectedReturn ≥ 0.3%.
func TestFrontier_DistinctMetrics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger, nil)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil, nil, nil)

	props := buildDiverseProperties()
	profile := investment.InvestorProfile{
		Strategy:          investment.StrategyBalanced,
		RiskTolerance:     investment.RiskModerate,
		AvailableCapital:  2_000_000,
		InvestmentHorizon: "10+ years",
	}
	params := investment.InvestmentPlanningParams{
		MortgageRate:   testMortgageRate,
		DownPaymentPct: testDownPayment,
	}

	cohorts := investment.BuildCohorts(props, nil, profile.Strategy, profile.RiskTolerance, profile.AvailableCapital, testMortgageRate, testDownPayment)
	if len(cohorts) == 0 {
		t.Fatal("BuildCohorts returned empty")
	}

	frontierPoints, err := fo.GenerateFrontier(context.Background(), cohorts, profile, params, nil)
	if err != nil {
		t.Fatalf("GenerateFrontier error: %v", err)
	}
	if len(frontierPoints) < 2 {
		t.Fatalf("expected ≥2 frontier points, got %d", len(frontierPoints))
	}

	sharpes := make([]float64, len(frontierPoints))
	returns := make([]float64, len(frontierPoints))
	for i, fp := range frontierPoints {
		sharpes[i] = fp.SharpeScore
		returns[i] = fp.ExpectedReturn
		t.Logf("FrontierPoint[%d] label=%q Sharpe=%.4f Return=%.4f%%", i, fp.Label, fp.SharpeScore, fp.ExpectedReturn)
	}

	sort.Float64s(sharpes)
	sort.Float64s(returns)

	sharpeRange := sharpes[len(sharpes)-1] - sharpes[0]
	returnRange := returns[len(returns)-1] - returns[0]

	t.Logf("Sharpe range: %.4f (min=%.4f, max=%.4f)", sharpeRange, sharpes[0], sharpes[len(sharpes)-1])
	t.Logf("Return range: %.4f%% (min=%.4f%%, max=%.4f%%)", returnRange, returns[0], returns[len(returns)-1])

	const minSharpeSpread = 0.05
	const minReturnSpread = 0.30
	if sharpeRange < minSharpeSpread {
		t.Errorf("Sharpe spread=%.4f < %.2f — frontier configs are not sufficiently differentiated", sharpeRange, minSharpeSpread)
	}
	if returnRange < minReturnSpread {
		t.Errorf("Return spread=%.4f%% < %.2f%% — frontier configs are not sufficiently differentiated", returnRange, minReturnSpread)
	}
}

// ─── Acceptance Criterion 3: Strategy influences FrontierPoint metrics ────────

// TestFrontier_StrategyInfluencesExpectedReturn verifies that CashFlow and Appreciation
// strategies on the same property pool produce different best-config ExpectedReturn values.
//
// CashFlow blends 30% appreciation + 70% income (cap rate weighted).
// Appreciation blends 70% appreciation + 30% income.
// With a diverse pool (high cap rate income props + low cap rate quality props), the two
// strategies select different top configs and produce visibly different return values.
func TestFrontier_StrategyInfluencesExpectedReturn(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger, nil)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil, nil, nil)

	props := buildDiverseProperties()
	params := investment.InvestmentPlanningParams{MortgageRate: testMortgageRate, DownPaymentPct: testDownPayment}

	runFrontier := func(strategy investment.InvestmentStrategy) []investment.FrontierPoint {
		profile := investment.InvestorProfile{
			Strategy:         strategy,
			RiskTolerance:    investment.RiskModerate,
			AvailableCapital: 2_000_000,
		}
		cohorts := investment.BuildCohorts(props, nil, strategy, investment.RiskModerate, 2_000_000, testMortgageRate, testDownPayment)
		fps, err := fo.GenerateFrontier(context.Background(), cohorts, profile, params, nil)
		if err != nil {
			t.Fatalf("GenerateFrontier(%v) error: %v", strategy, err)
		}
		return fps
	}

	cashFlowFPs := runFrontier(investment.StrategyCashFlow)
	apprFPs := runFrontier(investment.StrategyAppreciation)

	if len(cashFlowFPs) == 0 || len(apprFPs) == 0 {
		t.Fatal("frontier must not be empty")
	}

	// Find highest-return config in each run (CashFlow maximises income, Appreciation maximises appreciation).
	maxReturnCF := cashFlowFPs[0].ExpectedReturn
	for _, fp := range cashFlowFPs[1:] {
		if fp.ExpectedReturn > maxReturnCF {
			maxReturnCF = fp.ExpectedReturn
		}
	}
	maxReturnAppr := apprFPs[0].ExpectedReturn
	for _, fp := range apprFPs[1:] {
		if fp.ExpectedReturn > maxReturnAppr {
			maxReturnAppr = fp.ExpectedReturn
		}
	}

	t.Logf("CashFlow best return=%.4f%%, Appreciation best return=%.4f%%", maxReturnCF, maxReturnAppr)

	// Appreciation should have higher expected return on this pool because:
	// - quality properties have low cap rates (≈3-5%) but the Appreciation formula weights
	//   the fixed 4% appreciation rate at 70% → higher blended return than CashFlow.
	// - CashFlow weights income (cap rate) at 70% → income props dominate, but cap rates
	//   compete against the fixed appreciation baseline.
	returnDiff := math.Abs(maxReturnCF - maxReturnAppr)
	t.Logf("Return difference: %.4f%%", returnDiff)

	if returnDiff < 0.1 {
		t.Errorf("CashFlow and Appreciation best configs have nearly identical ExpectedReturn (diff=%.4f%%) — strategy weights are not producing differentiated outcomes", returnDiff)
	}
}

// ─── Acceptance Criterion 6: Recalculate parity ─────────────────────────────

// TestFrontier_RecalculateMaintainsRelativeOrdering verifies that applying an assumption
// override via Recalculate does not change the relative Sharpe ordering of frontier points.
func TestFrontier_RecalculateMaintainsRelativeOrdering(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger, nil)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil, nil, nil)

	props := buildDiverseProperties()
	profile := investment.InvestorProfile{
		Strategy:         investment.StrategyBalanced,
		RiskTolerance:    investment.RiskModerate,
		AvailableCapital: 2_000_000,
	}
	params := investment.InvestmentPlanningParams{MortgageRate: testMortgageRate, DownPaymentPct: testDownPayment}

	cohorts := investment.BuildCohorts(props, nil, profile.Strategy, profile.RiskTolerance, profile.AvailableCapital, testMortgageRate, testDownPayment)
	originalFPs, err := fo.GenerateFrontier(context.Background(), cohorts, profile, params, nil)
	if err != nil {
		t.Fatalf("GenerateFrontier error: %v", err)
	}
	if len(originalFPs) < 2 {
		t.Skip("need ≥2 frontier points to verify ordering")
	}

	// Recalculate with a modestly higher mortgage rate.
	newRate := testMortgageRate + 0.01 // 5%
	overrides := investment.AssumptionOverrides{MortgageRate: &newRate}
	recalcFPs, err := fo.Recalculate(context.Background(), originalFPs, profile, params, overrides)
	if err != nil {
		t.Fatalf("Recalculate error: %v", err)
	}
	if len(recalcFPs) != len(originalFPs) {
		t.Errorf("Recalculate returned %d points, expected %d", len(recalcFPs), len(originalFPs))
	}

	// Build label → Sharpe mapping for original and recalculated.
	sharpeByLabel := func(fps []investment.FrontierPoint) map[string]float64 {
		m := make(map[string]float64, len(fps))
		for _, fp := range fps {
			if _, exists := m[fp.Label]; !exists {
				m[fp.Label] = fp.SharpeScore
			}
		}
		return m
	}
	origOrder := sharpeByLabel(originalFPs)
	recalcOrder := sharpeByLabel(recalcFPs)

	t.Logf("Original Sharpe by label: %v", origOrder)
	t.Logf("Recalc Sharpe by label:   %v", recalcOrder)

	// For each pair of labels present in both maps, relative ordering must be preserved.
	labels := make([]string, 0, len(origOrder))
	for l := range origOrder {
		labels = append(labels, l)
	}
	for i := 0; i < len(labels); i++ {
		for j := i + 1; j < len(labels); j++ {
			la, lb := labels[i], labels[j]
			recalcA, hasA := recalcOrder[la]
			recalcB, hasB := recalcOrder[lb]
			if !hasA || !hasB {
				continue
			}
			origWin := origOrder[la] > origOrder[lb]
			recalcWin := recalcA > recalcB
			if origWin != recalcWin {
				t.Errorf("Relative Sharpe ordering of %q vs %q flipped after Recalculate (orig: %.4f vs %.4f; recalc: %.4f vs %.4f)",
					la, lb, origOrder[la], origOrder[lb], recalcA, recalcB)
			}
		}
	}
	t.Logf("✅ Relative Sharpe ordering preserved after mortgage-rate override")
}

// ─── Acceptance Criterion 7: Label flow end-to-end ──────────────────────────

// TestGenerateLabel_DirectMatrix verifies the exported GenerateLabel function against
// the full ADR-090 label matrix (Phase D).
func TestGenerateLabel_DirectMatrix(t *testing.T) {
	cases := []struct {
		configType string
		strategy   investment.InvestmentStrategy
		risk       investment.RiskTolerance
		want       string
	}{
		// Quality cohort
		{"quality", investment.StrategyAppreciation, investment.RiskModerate, "Growth"},
		{"quality", investment.StrategyAppreciation, investment.RiskConservative, "Growth"},
		{"quality", investment.StrategyCashFlow, investment.RiskModerate, "Quality-CF"},
		{"quality", investment.StrategyCashFlow, investment.RiskConservative, "Quality-CF"},
		{"quality", investment.StrategyBalanced, investment.RiskModerate, "Quality"},
		{"quality", investment.StrategyRiskAdjusted, investment.RiskAggressive, "Quality"},
		// Income cohort
		{"income", investment.StrategyCashFlow, investment.RiskConservative, "Defensive Income"},
		{"income", investment.StrategyBalanced, investment.RiskConservative, "Defensive"},
		{"income", investment.StrategyBalanced, investment.RiskModerate, "Income"},
		{"income", investment.StrategyAppreciation, investment.RiskAggressive, "Income"},
		// Balanced cohort
		{"balanced", investment.StrategyRiskAdjusted, investment.RiskModerate, "Risk-Balanced"},
		{"balanced", investment.StrategyBalanced, investment.RiskModerate, "Balanced"},
		{"balanced", investment.StrategyCashFlow, investment.RiskConservative, "Balanced"},
	}

	for _, tc := range cases {
		got := investment.GenerateLabel(tc.configType, tc.strategy, tc.risk)
		if got != tc.want {
			t.Errorf("GenerateLabel(%q, %v, %v) = %q; want %q",
				tc.configType, tc.strategy, tc.risk, got, tc.want)
		} else {
			t.Logf("  ✓ %-10s %-15v %-15v → %q", tc.configType, tc.strategy, tc.risk, got)
		}
	}
}

// TestFrontier_LabelFlowToFrontierPoint verifies that FrontierPoint.Label is
// non-empty after GenerateFrontier and matches the label set by BuildCohorts.
// ADR-090 Phase D: "No frontend changes required — the label field already flows
// through FrontierPoint to the comparison table and tooltip."
func TestFrontier_LabelFlowToFrontierPoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	markowitzCalc := NewMarkowitzCalculator()
	reinvestModeler := projection.NewReinvestmentModeler(logger, nil)
	fo := NewFrontierOptimizer(logger, markowitzCalc, reinvestModeler, nil, nil, nil, nil, nil)

	props := buildDiverseProperties()
	profile := investment.InvestorProfile{
		Strategy:          investment.StrategyBalanced,
		RiskTolerance:     investment.RiskModerate,
		AvailableCapital:  2_000_000,
		InvestmentHorizon: "10+ years",
	}
	params := investment.InvestmentPlanningParams{
		MortgageRate:   testMortgageRate,
		DownPaymentPct: testDownPayment,
	}

	cohorts := investment.BuildCohorts(props, nil, profile.Strategy, profile.RiskTolerance, profile.AvailableCapital, testMortgageRate, testDownPayment)
	// Build expected label set from cohorts (before frontier reduces to 5)
	expectedLabels := make(map[string]struct{}, len(cohorts))
	for _, c := range cohorts {
		expectedLabels[c.Label] = struct{}{}
	}

	frontierPoints, err := fo.GenerateFrontier(context.Background(), cohorts, profile, params, nil)
	if err != nil {
		t.Fatalf("GenerateFrontier error: %v", err)
	}
	if len(frontierPoints) == 0 {
		t.Fatal("GenerateFrontier returned no points")
	}

	for _, fp := range frontierPoints {
		if fp.Label == "" {
			t.Errorf("FrontierPoint[%d] has empty Label", fp.ConfigIndex)
			continue
		}
		if _, ok := expectedLabels[fp.Label]; !ok {
			t.Errorf("FrontierPoint[%d].Label=%q not in cohort labels %v",
				fp.ConfigIndex, fp.Label, expectedLabels)
		}
		t.Logf("  ✓ FrontierPoint[%d].Label=%q", fp.ConfigIndex, fp.Label)
	}
	t.Log("✅ All FrontierPoint.Label fields non-empty and trace back to cohort labels")
}

// ─── Acceptance Criteria 8+9: Growth cohort (Phase E) ───────────────────────

// TestBuildCohorts_AppreciationUsesGrowthCohort verifies that when strategy == Appreciation,
// BuildCohorts returns a Growth cohort (label "Growth", ConfigType "growth") as the primary cohort,
// and that the Growth cohort is NOT produced for other strategies.
// ADR-090 Phase E: SelectGrowthCohort replaces SelectQualityCohort for Appreciation strategy.
func TestBuildCohorts_AppreciationUsesGrowthCohort(t *testing.T) {
	props := buildDiverseProperties()
	const budget = 2_000_000

	// Appreciation strategy → primary cohort should be "Growth" with ConfigType "growth"
	apprCohorts := investment.BuildCohorts(props, nil, investment.StrategyAppreciation, investment.RiskModerate, budget, testMortgageRate, testDownPayment)
	if len(apprCohorts) < 3 {
		t.Fatalf("Appreciation: expected ≥3 cohorts, got %d", len(apprCohorts))
	}
	primary := apprCohorts[0]
	if primary.Label != "Growth" {
		t.Errorf("Appreciation primary cohort label=%q; want %q", primary.Label, "Growth")
	}
	if primary.ConfigType != investment.ConfigGrowth {
		t.Errorf("Appreciation primary cohort ConfigType=%q; want %q", primary.ConfigType, investment.ConfigGrowth)
	}
	t.Logf("  ✓ Appreciation: primary cohort Label=%q, ConfigType=%q, Properties=%d",
		primary.Label, primary.ConfigType, len(primary.Properties))

	// Other strategies → primary cohort should NOT be Growth
	for _, strat := range []investment.InvestmentStrategy{
		investment.StrategyBalanced, investment.StrategyCashFlow, investment.StrategyRiskAdjusted,
	} {
		cohorts := investment.BuildCohorts(props, nil, strat, investment.RiskModerate, budget, testMortgageRate, testDownPayment)
		if len(cohorts) < 1 {
			continue
		}
		if cohorts[0].ConfigType == investment.ConfigGrowth {
			t.Errorf("strategy=%v: primary cohort is Growth; expected Quality", strat)
		}
		t.Logf("  ✓ strategy=%v: primary ConfigType=%q (not Growth)", strat, cohorts[0].ConfigType)
	}
}

// TestSelectGrowthCohort_PriceBiasedRanking verifies that SelectGrowthCohort places
// the highest-price properties at the top of the cohort ahead of lower-price properties
// with equivalent AI scores. The price component (40%) should dominate when prices differ.
func TestSelectGrowthCohort_PriceBiasedRanking(t *testing.T) {
	props := buildDiverseProperties()
	cohorts := investment.BuildCohorts(props, nil, investment.StrategyAppreciation, investment.RiskModerate, 2_000_000, testMortgageRate, testDownPayment)
	if len(cohorts) == 0 {
		t.Fatal("BuildCohorts returned empty for Appreciation strategy")
	}
	growth := cohorts[0]
	if len(growth.Properties) < 3 {
		t.Fatalf("Growth cohort too small: %d properties", len(growth.Properties))
	}

	// The top property in the Growth cohort should have a higher price than the bottom property.
	// With 40% weight on price and our test data (q1=$580K AI=92 vs i1=$150K AI=55), the
	// Growth cohort must prefer expensive properties over cheap high-yield ones.
	topPrice := growth.Properties[0].Property.Price
	lastPrice := growth.Properties[len(growth.Properties)-1].Property.Price
	if topPrice <= lastPrice {
		t.Errorf("Growth cohort: topPrice=%d ≤ lastPrice=%d; expected price-descending order at top",
			topPrice, lastPrice)
	}

	// Specifically: the top property must come from the quality group (price > $400K),
	// not the income group (price $150-200K).
	if topPrice < 400_000 {
		t.Errorf("Growth cohort top property price=%d; expected a premium property (>$400K)", topPrice)
	}
	t.Logf("  ✓ Growth cohort[0] ID=%q price=%d AI=%.0f",
		growth.Properties[0].Property.ID, topPrice, growth.Properties[0].OverallScore)
	t.Log("✅ Growth cohort is price-biased as expected")
}

// TestGrowthIncomeJaccard verifies that the Growth and Income cohorts are sufficiently distinct
// (Jaccard similarity ≤ 0.40) when Appreciation strategy is used.
// ADR-090 Phase E, Criterion 9.
func TestGrowthIncomeJaccard(t *testing.T) {
	props := buildDiverseProperties()
	cohorts := investment.BuildCohorts(props, nil, investment.StrategyAppreciation, investment.RiskModerate, 2_000_000, testMortgageRate, testDownPayment)
	if len(cohorts) < 2 {
		t.Fatalf("expected ≥2 cohorts, got %d", len(cohorts))
	}
	growth := cohorts[0]
	income := cohorts[1]

	growthIDs := make(map[string]struct{}, len(growth.Properties))
	for _, rp := range growth.Properties {
		growthIDs[rp.Property.ID] = struct{}{}
	}
	incomeIDs := make(map[string]struct{}, len(income.Properties))
	for _, rp := range income.Properties {
		incomeIDs[rp.Property.ID] = struct{}{}
	}

	intersection := 0
	for id := range growthIDs {
		if _, ok := incomeIDs[id]; ok {
			intersection++
		}
	}
	union := len(growthIDs) + len(incomeIDs) - intersection
	jaccard := 0.0
	if union > 0 {
		jaccard = float64(intersection) / float64(union)
	}

	const maxSimilarity = 0.40
	t.Logf("  Growth size=%d, Income size=%d, intersection=%d, Jaccard=%.2f",
		len(growth.Properties), len(income.Properties), intersection, jaccard)
	if jaccard > maxSimilarity {
		t.Errorf("Jaccard(Growth,Income)=%.2f exceeds max %.2f — cohorts too similar", jaccard, maxSimilarity)
	} else {
		t.Logf("✅ Jaccard(Growth,Income)=%.2f ≤ %.2f", jaccard, maxSimilarity)
	}
}

// TestGenerateLabel_GrowthCohortType verifies that GenerateLabel returns "Growth" for
// the "growth" configType regardless of strategy or risk — Phase E extension of TestGenerateLabel_DirectMatrix.
func TestGenerateLabel_GrowthCohortType(t *testing.T) {
	for _, strat := range []investment.InvestmentStrategy{
		investment.StrategyAppreciation, investment.StrategyBalanced,
		investment.StrategyCashFlow, investment.StrategyRiskAdjusted,
	} {
		for _, risk := range []investment.RiskTolerance{
			investment.RiskConservative, investment.RiskModerate, investment.RiskAggressive,
		} {
			got := investment.GenerateLabel(string(investment.ConfigGrowth), strat, risk)
			if got != "Growth" {
				t.Errorf("GenerateLabel(growth, %v, %v)=%q; want %q", strat, risk, got, "Growth")
			}
		}
	}
	t.Log("✅ GenerateLabel returns 'Growth' for ConfigGrowth across all strategy×risk combinations")
}
