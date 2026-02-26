package investment

import (
	"math"
	"sort"
)

// ── ADR-090: Per-config property selection pipelines ─────────────────────────
//
// These functions replace the single-composite-ranking approach of
// RankAndSelectProperties with independent per-config pipelines:
//
//   FilterAffordable → capital filter (shared, no ranking)
//       ├─ SelectQualityCohort  → ranks by AI quality + strategy modifier
//       ├─ SelectIncomeCohort   → ranks by income metrics + risk floors
//       ├─ SelectBalancedCohort → blends quality + income by strategy weights
//       └─ SelectGrowthCohort   → ranks by price + AI score + budget capacity
//                                  (Phase E: Appreciation strategy only, replaces Quality)
//
// Each pipeline produces an independently-ranked PropertyCohort.
// BuildCohorts orchestrates the pipelines and returns []PropertyCohort for the frontier optimizer.

// rankedNorms holds the min/max for each metric across a []RankedProperty slice,
// used for [0,1] normalisation inside the pipeline selectors.
type rankedNorms struct {
	minCap, maxCap     float64
	minCoC, maxCoC     float64
	minDSCR, maxDSCR   float64
	minAI, maxAI       float64
	minDP, maxDP       float64 // DownPayment
	minPrice, maxPrice float64 // Property.Price (Phase E: Growth pipeline)
	minBC, maxBC       float64 // BudgetCapacity = budget/downPayment (Phase E: Growth pipeline)
}

// computeNorms computes per-metric min/max ranges over a RankedProperty slice.
func computeNorms(pool []RankedProperty) rankedNorms {
	if len(pool) == 0 {
		return rankedNorms{}
	}
	n := rankedNorms{
		minCap:   pool[0].EffectiveCapRate,       maxCap:   pool[0].EffectiveCapRate,
		minCoC:   pool[0].CashOnCash,             maxCoC:   pool[0].CashOnCash,
		minDSCR:  pool[0].DSCR,                   maxDSCR:  pool[0].DSCR,
		minAI:    pool[0].OverallScore,            maxAI:    pool[0].OverallScore,
		minDP:    pool[0].DownPayment,             maxDP:    pool[0].DownPayment,
		minPrice: float64(pool[0].Property.Price), maxPrice: float64(pool[0].Property.Price),
	}
	for _, r := range pool[1:] {
		price := float64(r.Property.Price)
		if r.EffectiveCapRate < n.minCap   { n.minCap   = r.EffectiveCapRate }
		if r.EffectiveCapRate > n.maxCap   { n.maxCap   = r.EffectiveCapRate }
		if r.CashOnCash       < n.minCoC   { n.minCoC   = r.CashOnCash       }
		if r.CashOnCash       > n.maxCoC   { n.maxCoC   = r.CashOnCash       }
		if r.DSCR             < n.minDSCR  { n.minDSCR  = r.DSCR             }
		if r.DSCR             > n.maxDSCR  { n.maxDSCR  = r.DSCR             }
		if r.OverallScore     < n.minAI    { n.minAI    = r.OverallScore      }
		if r.OverallScore     > n.maxAI    { n.maxAI    = r.OverallScore      }
		if r.DownPayment      < n.minDP    { n.minDP    = r.DownPayment       }
		if r.DownPayment      > n.maxDP    { n.maxDP    = r.DownPayment       }
		if price              < n.minPrice { n.minPrice = price               }
		if price              > n.maxPrice { n.maxPrice = price               }
	}
	return n
}

// computeNormsWithBudget extends computeNorms with BudgetCapacity (budget/downPayment)
// normalisation, used exclusively by the Growth pipeline (Phase E).
func computeNormsWithBudget(pool []RankedProperty, budget int) rankedNorms {
	n := computeNorms(pool)
	if len(pool) == 0 || budget <= 0 {
		return n
	}
	budgetF := float64(budget)
	initialized := false
	for _, r := range pool {
		if r.DownPayment <= 0 {
			continue // skip properties with no down payment (avoids div-by-zero)
		}
		bc := budgetF / r.DownPayment
		if !initialized {
			n.minBC, n.maxBC = bc, bc
			initialized = true
		} else {
			if bc < n.minBC { n.minBC = bc }
			if bc > n.maxBC { n.maxBC = bc }
		}
	}
	return n
}

// normalize maps v to [0,1] within [lo, hi]. Returns 0.5 when hi == lo.
func normalize(v, lo, hi float64) float64 {
	if hi == lo {
		return 0.5
	}
	return (v - lo) / (hi - lo)
}

// applyIncomeFloors applies risk-based DSCR and cap rate hard floors for the Income pipeline.
// Falls back to full pool if fewer than minFrontierPoolSize pass both floors.
func applyIncomeFloors(pool []RankedProperty, risk RiskTolerance) []RankedProperty {
	var dscrFloor, capFloor float64
	switch risk {
	case RiskConservative:
		dscrFloor, capFloor = 1.3, 5.0
	case RiskModerate:
		dscrFloor, capFloor = 1.1, 4.0
	default: // Aggressive — no floors
		return pool
	}
	filtered := make([]RankedProperty, 0, len(pool))
	for _, r := range pool {
		if r.DSCR >= dscrFloor && r.EffectiveCapRate >= capFloor {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) < minFrontierPoolSize {
		return pool
	}
	return filtered
}

// GenerateLabel returns a human-readable label for a config based on its type, strategy, and risk.
// ADR-090 Phase D: exported so tests and downstream packages can assert label correctness directly.
// ADR-090 Phase E: added "growth" configType (Appreciation strategy only).
// ADR-091: added "pinned" configType (user-selected evaluations / memos).
//
// Label matrix (objective-first naming):
//
//	pinned   + any                   → "Your Selection"      (ADR-091: user-selected verbatim)
//	growth   + any                   → "Appreciation"        (Phase E: price-biased pipeline)
//	quality  + appreciation          → "Appreciation"        (when Growth pipeline not active)
//	quality  + cash-flow             → "Cash Flow"
//	quality  + any                   → "Quality"
//	income   + cash-flow + conserv.  → "Conservative Income"
//	income   + conservative          → "Conservative"
//	income   + any                   → "High Yield"
//	balanced + risk-adjusted         → "Risk-Adjusted"
//	balanced + any                   → "Balanced"
func GenerateLabel(configType string, strategy InvestmentStrategy, risk RiskTolerance) string {
	switch configType {
	case string(ConfigPinned): // ADR-091: user-selected properties (evaluations / memos)
		return "Your Selection"
	case string(ConfigGrowth): // Phase E: price-biased pipeline for Appreciation strategy
		return "Appreciation"
	case string(ConfigQuality):
		switch strategy {
		case StrategyAppreciation:
			return "Appreciation" // Quality cohort with Appreciation modifier (when Growth not active)
		case StrategyCashFlow:
			return "Cash Flow"
		default:
			return "Quality"
		}
	case string(ConfigIncome):
		if strategy == StrategyCashFlow && risk == RiskConservative {
			return "Conservative Income"
		}
		if risk == RiskConservative {
			return "Conservative"
		}
		return "High Yield"
	case string(ConfigBalanced):
		if strategy == StrategyRiskAdjusted {
			return "Risk-Adjusted"
		}
		return "Balanced"
	default:
		return "Config"
	}
}

// takeTop returns the first n items from a slice sorted desc by score.
func takeTop(pool []RankedProperty, scores []float64, n int) []RankedProperty {
	type entry struct {
		rp    RankedProperty
		score float64
	}
	entries := make([]entry, len(pool))
	for i := range pool {
		entries[i] = entry{pool[i], scores[i]}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].score > entries[j].score
	})
	if n > len(entries) {
		n = len(entries)
	}
	result := make([]RankedProperty, n)
	for i := range n {
		result[i] = entries[i].rp
	}
	return result
}

// FilterAffordable removes properties whose individual down payment exceeds the budget.
// Returns []RankedProperty with precomputed financial metrics (EffectiveCapRate, DSCR,
// CashOnCash, DownPayment, MonthlyPayment). Does NOT sort or cap — ranking is each
// pipeline's responsibility.
// ADR-090: shared first stage of all per-config pipelines.
func FilterAffordable(
	props []ScoredProperty,
	budget int,
	downPaymentPct float64,
	mortgageRate float64,
) []RankedProperty {
	if len(props) == 0 || budget <= 0 {
		return nil
	}
	if downPaymentPct <= 0 {
		downPaymentPct = 0.20
	}
	if mortgageRate <= 0 {
		mortgageRate = 0.075
	}

	result := make([]RankedProperty, 0, len(props))
	for _, sp := range props {
		p := sp.Property
		if p.Price <= 0 {
			continue
		}
		downPayment := float64(p.Price) * downPaymentPct
		if int(downPayment) > budget {
			continue
		}

		loanAmount := float64(p.Price) - downPayment
		monthlyPayment := rankingMortgagePayment(loanAmount, mortgageRate, 30)
		expRatio := expenseRatioForAge(p.YearBuilt)
		monthlyNOI := float64(p.EstimatedRent) * (1 - expRatio)
		annualNOI := monthlyNOI * 12
		annualDebtService := monthlyPayment * 12

		effectiveCapRate := CapRatePct(p.EstimatedRent, p.Price, p.YearBuilt)

		dscr := 0.0
		if annualDebtService > 0 {
			dscr = annualNOI / annualDebtService
		}

		annualCashFlow := annualNOI - annualDebtService
		coc := 0.0
		if downPayment > 0 {
			coc = annualCashFlow / downPayment * 100
		}

		result = append(result, RankedProperty{
			ScoredProperty:  sp,
			EffectiveCapRate: effectiveCapRate,
			DSCR:             dscr,
			CashOnCash:       coc,
			DownPayment:      downPayment,
			MonthlyPayment:   monthlyPayment,
		})
	}
	return result
}

// SelectQualityCohort selects the top maxSize properties from the affordable pool
// by AI quality score, modified by strategy.
//
// No DSCR floor is applied — the Quality pipeline's purpose is to surface the AI's
// top-ranked properties regardless of their cash flow profile. Income discipline is
// the Income pipeline's responsibility. Applying a DSCR floor here would eliminate
// all premium-priced properties (which have low DSCR at realistic mortgage rates) and
// collapse the Quality and Income cohorts to the same pool.
//
// ADR-090: Quality pipeline.
func SelectQualityCohort(
	pool []RankedProperty,
	strategy InvestmentStrategy,
	risk RiskTolerance,
	maxSize int,
) PropertyCohort {
	label := GenerateLabel(string(ConfigQuality), strategy, risk)
	if len(pool) == 0 {
		return PropertyCohort{Label: label, ConfigType: ConfigQuality}
	}

	norms := computeNorms(pool)

	scores := make([]float64, len(pool))
	for i, rp := range pool {
		aiScore := normalize(rp.OverallScore, norms.minAI, norms.maxAI)
		var modifier float64
		switch strategy {
		case StrategyCashFlow:
			// Boost properties where quality and cash flow align.
			modifier = 0.2 * normalize(rp.CashOnCash, norms.minCoC, norms.maxCoC)
		case StrategyAppreciation:
			// Higher-priced properties appreciate faster; use DownPayment as price-tier proxy.
			modifier = 0.2 * normalize(rp.DownPayment, norms.minDP, norms.maxDP)
		case StrategyRiskAdjusted:
			// Penalise properties where DSCR is below 1.1 (sub-threshold coverage).
			if rp.DSCR > 0 && rp.DSCR < 1.1 {
				modifier = -0.1
			}
		// StrategyBalanced: no modifier — pure AI score.
		}
		scores[i] = aiScore + modifier
	}

	return PropertyCohort{
		Properties: takeTop(pool, scores, maxSize),
		Label:      label,
		ConfigType: ConfigQuality,
	}
}

// SelectIncomeCohort selects the top maxSize properties from the affordable pool
// by income-generation metrics (cap rate, cash-on-cash, DSCR), weighted per strategy.
// Risk-based DSCR and minimum cap rate floors are applied before ranking.
// ADR-090: Income pipeline.
func SelectIncomeCohort(
	pool []RankedProperty,
	strategy InvestmentStrategy,
	risk RiskTolerance,
	maxSize int,
) PropertyCohort {
	label := GenerateLabel(string(ConfigIncome), strategy, risk)
	if len(pool) == 0 {
		return PropertyCohort{Label: label, ConfigType: ConfigIncome}
	}

	filtered := applyIncomeFloors(pool, risk)
	norms := computeNorms(filtered)

	scores := make([]float64, len(filtered))
	for i, rp := range filtered {
		normCap  := normalize(rp.EffectiveCapRate, norms.minCap, norms.maxCap)
		normCoC  := normalize(rp.CashOnCash,       norms.minCoC, norms.maxCoC)
		normDSCR := normalize(rp.DSCR,             norms.minDSCR, norms.maxDSCR)
		switch strategy {
		case StrategyCashFlow:
			scores[i] = 0.4*normCap + 0.4*normCoC + 0.2*normDSCR
		case StrategyAppreciation:
			// DSCR matters less for hold-for-value; maximise yield.
			scores[i] = 0.6*normCap + 0.4*normCoC
		case StrategyRiskAdjusted:
			// DSCR is dominant for risk-adjusted investors.
			scores[i] = 0.3*normCap + 0.3*normCoC + 0.4*normDSCR
		default: // Balanced
			scores[i] = 0.35*normCap + 0.35*normCoC + 0.3*normDSCR
		}
	}

	return PropertyCohort{
		Properties: takeTop(filtered, scores, maxSize),
		Label:      label,
		ConfigType: ConfigIncome,
	}
}

// SelectBalancedCohort selects the top maxSize properties from the union of the Quality
// and Income cohorts, re-ranked by a strategy-weighted blend of normalised AI score and
// income metrics. Drawing from the union ensures Balanced contains only properties that
// are strong on at least one dimension, then selects those that score well on both.
// ADR-090: Balanced pipeline.
func SelectBalancedCohort(
	affordable []RankedProperty,
	qualityCohort PropertyCohort,
	incomeCohort PropertyCohort,
	strategy InvestmentStrategy,
	risk RiskTolerance,
	maxSize int,
) PropertyCohort {
	label := GenerateLabel(string(ConfigBalanced), strategy, risk)

	// Build union of quality and income properties (by ID, no duplicates).
	unionSet := make(map[string]RankedProperty, len(qualityCohort.Properties)+len(incomeCohort.Properties))
	for _, rp := range qualityCohort.Properties {
		unionSet[rp.Property.ID] = rp
	}
	for _, rp := range incomeCohort.Properties {
		unionSet[rp.Property.ID] = rp
	}

	unionPool := make([]RankedProperty, 0, len(unionSet))
	for _, rp := range unionSet {
		unionPool = append(unionPool, rp)
	}

	// Fall back to the full affordable pool if the union is too small.
	if len(unionPool) < minFrontierPoolSize {
		unionPool = affordable
	}
	if len(unionPool) == 0 {
		return PropertyCohort{Label: label, ConfigType: ConfigBalanced}
	}

	// Strategy-dependent quality vs income blend weights.
	var qualityW, incomeW float64
	switch strategy {
	case StrategyCashFlow:
		qualityW, incomeW = 0.35, 0.65
	case StrategyAppreciation:
		qualityW, incomeW = 0.65, 0.35
	case StrategyRiskAdjusted:
		qualityW, incomeW = 0.45, 0.55
	default: // Balanced
		qualityW, incomeW = 0.50, 0.50
	}

	norms := computeNorms(unionPool)
	scores := make([]float64, len(unionPool))
	for i, rp := range unionPool {
		normAI  := normalize(rp.OverallScore,     norms.minAI,  norms.maxAI)
		normCap := normalize(rp.EffectiveCapRate,  norms.minCap, norms.maxCap)
		normCoC := normalize(rp.CashOnCash,        norms.minCoC, norms.maxCoC)
		incomeScore := 0.5*normCap + 0.5*normCoC
		scores[i] = qualityW*normAI + incomeW*incomeScore
	}

	return PropertyCohort{
		Properties: takeTop(unionPool, scores, maxSize),
		Label:      label,
		ConfigType: ConfigBalanced,
	}
}

// SelectPinnedCohort returns all properties whose ID appears in pinnedIDs as a verbatim cohort.
// ADR-091: user-selected evaluations and memo properties bypass FilterAffordable and ranking
// algorithms entirely — the user's selection is treated as deliberate intent, not as candidates
// competing for cohort slots. All pinned properties are included regardless of budget compliance
// (budget violations should be surfaced as warnings by the frontend/handler, not silent drops).
func SelectPinnedCohort(
	props []ScoredProperty,
	pinnedIDs map[string]bool,
	strategy InvestmentStrategy,
	risk RiskTolerance,
) PropertyCohort {
	label := GenerateLabel(string(ConfigPinned), strategy, risk)
	if len(pinnedIDs) == 0 {
		return PropertyCohort{Label: label, ConfigType: ConfigPinned}
	}
	pinned := make([]RankedProperty, 0, len(pinnedIDs))
	for _, sp := range props {
		if !pinnedIDs[sp.Property.ID] {
			continue
		}
		// Include verbatim — financial metrics are zero (not filtered through FilterAffordable).
		// The frontier optimizer handles missing metrics gracefully.
		pinned = append(pinned, RankedProperty{ScoredProperty: sp})
	}
	return PropertyCohort{
		Properties: pinned,
		Label:      label,
		ConfigType: ConfigPinned,
	}
}

// BuildCohorts is the Phase 2b entry point for the frontier pipeline.
// It replaces RankAndSelectProperties. It:
//   1. Calls FilterAffordable (shared capital filter, no composite ranking).
//   2. Runs three independent per-config pipelines over the affordable pool.
//   3. Returns []PropertyCohort — one cohort per config type — for GenerateFrontier.
//
// ADR-091: when pinnedIDs is non-empty, Config 0 is a PinnedCohort (user-selected evaluations
// verbatim), and Configs 1/2 are drawn from the NON-pinned subset of props so the optimizer
// produces genuinely distinct configurations alongside the user's selection.
// Pass nil for pinnedIDs for auto-discover and session sources (unchanged behaviour).
//
// SelectGrowthCohort ranks the affordable pool by a price-biased formula designed for
// investors whose primary objective is capital appreciation.
//
// ADR-090 Phase E: formula = 0.4×norm(Price) + 0.35×norm(OverallScore) + 0.25×norm(BudgetCapacity)
//
// BudgetCapacity = budget / downPayment — measures how many properties like this the investor's
// budget could support individually. It provides a gentle affordability correction so the pipeline
// does not exclusively select the most expensive properties; properties that leave room for
// portfolio diversification are rewarded.
//
// The Growth cohort is only generated when strategy == StrategyAppreciation, where it replaces
// the Quality cohort in BuildCohorts. For all other strategies BuildCohorts uses SelectQualityCohort.
func SelectGrowthCohort(pool []RankedProperty, strategy InvestmentStrategy, risk RiskTolerance, budget int, maxSize int) PropertyCohort {
	label := GenerateLabel(string(ConfigGrowth), strategy, risk)
	if len(pool) == 0 {
		return PropertyCohort{Label: label, ConfigType: ConfigGrowth}
	}

	norms := computeNormsWithBudget(pool, budget)
	budgetF := float64(budget)

	scores := make([]float64, len(pool))
	for i, rp := range pool {
		normPrice := normalize(float64(rp.Property.Price), norms.minPrice, norms.maxPrice)
		normAI := normalize(rp.OverallScore, norms.minAI, norms.maxAI)
		// BudgetCapacity: how many of this property the budget supports (higher = cheaper relative to budget)
		bc := 0.0
		if rp.DownPayment > 0 {
			bc = budgetF / rp.DownPayment
		}
		normBC := normalize(bc, norms.minBC, norms.maxBC)
		scores[i] = 0.4*normPrice + 0.35*normAI + 0.25*normBC
	}

	return PropertyCohort{
		Properties: takeTop(pool, scores, maxSize),
		Label:      label,
		ConfigType: ConfigGrowth,
	}
}

// BuildCohorts filters to affordable properties then runs the independent per-config pipelines.
// ADR-090: Per-config property selection pipelines.
// ADR-090 Phase E: when strategy == StrategyAppreciation, SelectGrowthCohort replaces
// SelectQualityCohort so the frontier receives a price-biased config instead of an AI-score config.
// ADR-091: when pinnedIDs is non-empty, Config 0 = user selection verbatim (PinnedCohort);
// Configs 1/2 drawn from non-pinned props only. Pass nil for auto-discover / session sources.
func BuildCohorts(
	props []ScoredProperty,
	pinnedIDs map[string]bool,
	strategy InvestmentStrategy,
	risk RiskTolerance,
	budget int,
	mortgageRate float64,
	downPaymentPct float64,
) []PropertyCohort {
	const cohortSize = 15

	// ADR-091: pinned path — Config 0 = user selection, Configs 1/2 = optimizer over pool-only props.
	if len(pinnedIDs) > 0 {
		pinned := SelectPinnedCohort(props, pinnedIDs, strategy, risk)
		if len(pinned.Properties) == 0 {
			// None of the pinned IDs were found in props — fall through to normal path.
			goto normalPath
		}

		// Configs 1/2 draw from NON-pinned props so they are genuinely distinct from Config 0.
		poolProps := make([]ScoredProperty, 0, len(props)-len(pinnedIDs))
		for _, sp := range props {
			if !pinnedIDs[sp.Property.ID] {
				poolProps = append(poolProps, sp)
			}
		}

		affordable := FilterAffordable(poolProps, budget, downPaymentPct, mortgageRate)
		if len(affordable) == 0 {
			// No pool properties — return pinned cohort only.
			return []PropertyCohort{pinned}
		}

		income   := SelectIncomeCohort(affordable, strategy, risk, cohortSize)
		balanced := SelectBalancedCohort(affordable, pinned, income, strategy, risk, cohortSize)
		return []PropertyCohort{pinned, income, balanced}
	}

normalPath:
	affordable := FilterAffordable(props, budget, downPaymentPct, mortgageRate)
	if len(affordable) == 0 {
		return nil
	}

	var primary PropertyCohort
	if strategy == StrategyAppreciation {
		// Phase E: Growth pipeline surfaces high-price, capital-appreciation-oriented properties.
		primary = SelectGrowthCohort(affordable, strategy, risk, budget, cohortSize)
	} else {
		primary = SelectQualityCohort(affordable, strategy, risk, cohortSize)
	}

	income   := SelectIncomeCohort(affordable, strategy, risk, cohortSize)
	balanced := SelectBalancedCohort(affordable, primary, income, strategy, risk, cohortSize)

	return []PropertyCohort{primary, income, balanced}
}

const (
	// minFrontierPoolSize must match FrontierOptimizer.minProperties.
	// The pool passed to the frontier must contain at least this many properties
	// whose combined down payments fit within the capital budget.
	minFrontierPoolSize = 5

	defaultMaxPool = 40
)

// PropertyRanking holds derived investment metrics for a scored property.
type PropertyRanking struct {
	ScoredProperty
	EffectiveCapRate float64 // Age-adjusted net cap rate: NOI/price (%)
	DSCR             float64 // Net Operating Income / Annual Debt Service
	CashOnCash       float64 // Annual cash flow / cash invested (%)
	DownPayment      float64 // Required down payment ($)
	// BudgetCapacity is how many of this property the budget could purchase individually,
	// capped at minFrontierPoolSize. Higher capacity = more portfolio combinations enabled.
	BudgetCapacity int
	CompositeScore float64 // Weighted ranking score — higher is better
}

// rankingMortgagePayment returns the monthly mortgage payment.
func rankingMortgagePayment(principal, annualRate float64, termYears int) float64 {
	if principal <= 0 || annualRate <= 0 || termYears <= 0 {
		return 0
	}
	r := annualRate / 12
	n := float64(termYears * 12)
	return principal * r * math.Pow(1+r, n) / (math.Pow(1+r, n) - 1)
}

// hasAffordableCombination returns true if pool contains at least k properties
// whose combined down payments fit within budget (uses greedy cheapest-k check).
func hasAffordableCombination(pool []PropertyRanking, budget int, k int) bool {
	if len(pool) < k {
		return false
	}
	// Sort a copy by down payment ascending; cheapest k is the best chance of fitting.
	byPrice := make([]PropertyRanking, len(pool))
	copy(byPrice, pool)
	sort.Slice(byPrice, func(i, j int) bool {
		return byPrice[i].DownPayment < byPrice[j].DownPayment
	})
	total := 0.0
	for i := range k {
		total += byPrice[i].DownPayment
	}
	return int(total) <= budget
}

// RankAndSelectProperties ranks a scored property pool by investment quality and
// returns a pool suitable for efficient frontier generation.
//
// It performs three stages:
//
//  1. Metric computation + per-property capital filter
//     Each property's effective cap rate, DSCR, and cash-on-cash are computed using
//     age-adjusted operating expenses. Properties whose single down payment exceeds
//     the full budget are removed (they can never be purchased).
//
//  2. Holistic composite scoring
//     Weights are determined by WeightsForStrategy(strategy, risk). The DSCR direction
//     is strategy-dependent: cash-flow and conservative investors prefer higher DSCR
//     (more coverage cushion); appreciation and aggressive investors prefer lower DSCR
//     (more efficient leverage). Properties with DSCR < 1.0 receive a penalty scaled
//     by risk tolerance (×0.25 for conservative, ×0.5 for others), except appreciation
//     strategy where negative cash flow carries no penalty.
//
//  3. Portfolio coverage guarantee
//     After sorting, the function verifies the top pool contains at least minFrontierPoolSize
//     properties whose COMBINED down payments fit within the budget.
//     If not, it extends the pool with the next-best-scoring affordable properties until
//     a valid combination exists. This prevents frontier generation from failing because
//     the pool is full of individually fine but collectively over-budget properties.
//
// maxPool caps the initial sort window (default 40). Pass 0 for the default.
func RankAndSelectProperties(
	properties []ScoredProperty,
	budget int,
	downPaymentPct float64,
	mortgageRate float64,
	maxPool int,
	strategy InvestmentStrategy,
	risk RiskTolerance,
) []ScoredProperty {
	if len(properties) == 0 || budget <= 0 {
		return properties
	}
	if downPaymentPct <= 0 {
		downPaymentPct = 0.20
	}
	if mortgageRate <= 0 {
		mortgageRate = 0.075
	}
	if maxPool <= 0 {
		maxPool = defaultMaxPool
	}

	// ── Stage 1: compute metrics, drop individually unaffordable properties ────
	ranked := make([]PropertyRanking, 0, len(properties))
	for _, sp := range properties {
		p := sp.Property
		if p.Price <= 0 {
			continue
		}

		downPayment := float64(p.Price) * downPaymentPct
		// Remove properties that alone would consume the entire budget.
		if int(downPayment) > budget {
			continue
		}

		expRatio := expenseRatioForAge(p.YearBuilt)
		loanAmount := float64(p.Price) - downPayment
		monthlyPayment := rankingMortgagePayment(loanAmount, mortgageRate, 30)

		monthlyNOI := float64(p.EstimatedRent) * (1 - expRatio)
		annualNOI := monthlyNOI * 12
		annualDebtService := monthlyPayment * 12

		effectiveCapRate := CapRatePct(p.EstimatedRent, p.Price, p.YearBuilt)

		dscr := 0.0
		if annualDebtService > 0 {
			dscr = annualNOI / annualDebtService
		}

		annualCashFlow := annualNOI - annualDebtService
		coc := 0.0
		if downPayment > 0 {
			coc = annualCashFlow / downPayment * 100
		}

		// Budget utilization: how many of this property could the budget purchase?
		// Capped at minFrontierPoolSize (beyond that, further capacity doesn't help).
		capacity := int(float64(budget) / downPayment)
		capacity = min(capacity, minFrontierPoolSize)

		ranked = append(ranked, PropertyRanking{
			ScoredProperty:   sp,
			EffectiveCapRate:  effectiveCapRate,
			DSCR:              dscr,
			CashOnCash:        coc,
			DownPayment:       downPayment,
			BudgetCapacity:    capacity,
		})
	}

	if len(ranked) == 0 {
		return nil
	}

	// ── Stage 2: normalise metrics and compute composite score ────────────────
	weights := WeightsForStrategy(strategy, risk)
	minCap, maxCap := ranked[0].EffectiveCapRate, ranked[0].EffectiveCapRate
	minDSCR, maxDSCR := ranked[0].DSCR, ranked[0].DSCR
	minCoC, maxCoC := ranked[0].CashOnCash, ranked[0].CashOnCash

	for _, m := range ranked[1:] {
		if m.EffectiveCapRate < minCap {
			minCap = m.EffectiveCapRate
		}
		if m.EffectiveCapRate > maxCap {
			maxCap = m.EffectiveCapRate
		}
		if m.DSCR < minDSCR {
			minDSCR = m.DSCR
		}
		if m.DSCR > maxDSCR {
			maxDSCR = m.DSCR
		}
		if m.CashOnCash < minCoC {
			minCoC = m.CashOnCash
		}
		if m.CashOnCash > maxCoC {
			maxCoC = m.CashOnCash
		}
	}

	norm := func(v, lo, hi float64) float64 {
		if hi == lo {
			return 0.5
		}
		return (v - lo) / (hi - lo)
	}

	for i := range ranked {
		m := &ranked[i]

		// Effective cap rate: higher → better
		capScore := norm(m.EffectiveCapRate, minCap, maxCap)

		// CoC: higher → better
		cocScore := norm(m.CashOnCash, minCoC, maxCoC)

		// DSCR: direction is strategy-dependent.
		//   DSCRHigherIsBetter=true  → conservative/cash-flow: higher cushion is better
		//   DSCRHigherIsBetter=false → appreciation/balanced: lower DSCR = more efficient leverage
		var dscrScore float64
		if weights.DSCRHigherIsBetter {
			dscrScore = norm(m.DSCR, minDSCR, maxDSCR) // higher = better
		} else {
			dscrScore = 1 - norm(m.DSCR, minDSCR, maxDSCR) // lower = better
		}

		// DSCR penalty for negative cash flow (DSCR > 0 and < 1.0).
		// Cash-flow + conservative: heavy penalty (×0.25) — no tolerance for shortfall.
		// Cash-flow or risk-adjusted: standard penalty (×0.5).
		// Appreciation/aggressive: no penalty — negative cash flow is accepted.
		if m.DSCR > 0 && m.DSCR < 1.0 {
			switch {
			case weights.DSCRHigherIsBetter && risk == RiskConservative:
				dscrScore *= 0.25
			case weights.DSCRHigherIsBetter:
				dscrScore *= 0.5
			// default: appreciation/aggressive — no penalty applied
			}
		}

		// Budget utilisation: higher capacity → more portfolio combinations enabled
		budgetScore := float64(m.BudgetCapacity) / float64(minFrontierPoolSize)

		m.CompositeScore = weights.CapRate*capScore +
			weights.CashOnCash*cocScore +
			weights.DSCR*dscrScore +
			weights.BudgetUtilization*budgetScore
	}

	// Sort all ranked properties by composite score (best first).
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].CompositeScore > ranked[j].CompositeScore
	})

	// ── Stage 3: portfolio coverage guarantee ────────────────────────────────
	// Take the top maxPool properties as our initial pool.
	pool := ranked
	if len(pool) > maxPool {
		pool = ranked[:maxPool]
	}

	// If the top pool cannot form even one valid minFrontierPoolSize-property
	// combination within budget, extend it with the next-best properties until
	// a valid combination exists.
	//
	// This handles the scenario where all top-ranked properties are high-priced
	// and only 3-4 fit in budget together — the frontier needs at least 5.
	if !hasAffordableCombination(pool, budget, minFrontierPoolSize) {
		overflow := ranked[len(pool):]
		for _, candidate := range overflow {
			pool = append(pool, candidate)
			if hasAffordableCombination(pool, budget, minFrontierPoolSize) {
				break
			}
		}
		// If still no valid combination after exhausting all candidates, return the
		// full ranked list so the caller can surface a meaningful budget error.
		if !hasAffordableCombination(pool, budget, minFrontierPoolSize) {
			pool = ranked
		}
	}

	result := make([]ScoredProperty, len(pool))
	for i, m := range pool {
		result[i] = m.ScoredProperty
	}
	return result
}
