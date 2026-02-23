package optimization

import (
	"github.com/estara-ai/www/internal/services/investment"
)

// testBuildCohorts converts a []ScoredProperty into []PropertyCohort for use in tests.
// Uses the given strategy and risk; budget defaults to 5M when 0 so all typical test
// properties (under $1M each at 20% down) are individually affordable.
func testBuildCohorts(
	props []investment.ScoredProperty,
	strategy investment.InvestmentStrategy,
	risk investment.RiskTolerance,
	budget int,
) []investment.PropertyCohort {
	if budget <= 0 {
		budget = 5_000_000
	}
	cohorts := investment.BuildCohorts(props, nil, strategy, risk, budget, 0.075, 0.20)
	// If BuildCohorts returns nil (all unaffordable even at 5M), return a minimal fallback
	// so tests that care about the frontier output still exercise the optimizer.
	if len(cohorts) == 0 {
		// Wrap all props as a single "Quality" cohort — last resort for test data.
		ranked := investment.FilterAffordable(props, 50_000_000, 0.20, 0.075)
		if len(ranked) > 0 {
			cohorts = []investment.PropertyCohort{
				{Properties: ranked, Label: "Quality", ConfigType: "quality"},
				{Properties: ranked, Label: "Income", ConfigType: "income"},
				{Properties: ranked, Label: "Balanced", ConfigType: "balanced"},
			}
		}
	}
	return cohorts
}
