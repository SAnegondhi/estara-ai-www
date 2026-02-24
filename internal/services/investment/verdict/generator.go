package verdict

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/estara-ai/www/internal/services/investment"
)

// Generator creates decision verdicts for frontier configurations
// ADR-088 Phase 8: AI-powered recommendation (PROCEED/PAUSE/REVISE)
type Generator struct {
	logger *slog.Logger
}

// NewGenerator creates a new verdict generator
func NewGenerator(logger *slog.Logger) *Generator {
	return &Generator{
		logger: logger,
	}
}

// GenerateVerdict analyzes portfolio and scenarios to generate recommendation
func (vg *Generator) GenerateVerdict(
	ctx context.Context,
	config *investment.FrontierPoint,
	profile investment.InvestorProfile,
	wealthTarget int, // Optional goal (0 = no target)
) (*investment.DecisionVerdict, error) {
	if config == nil {
		return nil, fmt.Errorf("config required for verdict generation")
	}

	if config.Scenarios == nil {
		return nil, fmt.Errorf("scenarios required for verdict generation")
	}

	vg.logger.Info("generating decision verdict",
		"configIndex", config.ConfigIndex,
		"wealthTarget", wealthTarget,
		"strategy", profile.Strategy,
	)

	// Analyze portfolio outcomes
	recommendation := vg.determineRecommendation(config, profile, wealthTarget)
	rationale := vg.generateRationale(config, recommendation, wealthTarget, profile)
	conditions := vg.identifyKeyConditions(config)
	signals := vg.generateMonitoringSignals(config)

	verdict := &investment.DecisionVerdict{
		Recommendation:    recommendation,
		DecisionRationale: rationale,
		KeyConditions:     conditions,
		MonitoringSignals: signals,
		GeneratedAt:       time.Now().Format(time.RFC3339),
	}

	vg.logger.Info("verdict generated",
		"recommendation", recommendation,
		"conditionCount", len(conditions),
		"signalCount", len(signals),
	)

	return verdict, nil
}

// determineRecommendation analyzes outcomes to determine PROCEED/PAUSE/REVISE
func (vg *Generator) determineRecommendation(
	config *investment.FrontierPoint,
	profile investment.InvestorProfile,
	wealthTarget int,
) investment.DecisionRecommendation {
	base := config.Scenarios.Base
	stress := config.Scenarios.Stress

	// Rule 1: If stress scenario is deeply negative Y5, recommend PAUSE
	// Find Year 5 outcome (may not be at index 4)
	for _, outcome := range stress.YearlyOutcomes {
		if outcome.Year == 5 {
			if outcome.NetWorth < -500000 {
				vg.logger.Info("PAUSE: Stress scenario Y5 deeply negative",
					"year5NetWorth", outcome.NetWorth,
				)
				return investment.DecisionPause
			}
			break
		}
	}

	// Rule 2: If wealth target provided, check probability of success
	if wealthTarget > 0 {
		baseY10 := base.FinalNetWorth
		targetGap := float64(wealthTarget-baseY10) / float64(wealthTarget)

		// If base scenario misses target by >30%, recommend REVISE
		if targetGap > 0.30 {
			vg.logger.Info("REVISE: Base scenario misses target significantly",
				"baseY10", baseY10,
				"target", wealthTarget,
				"gap", targetGap,
			)
			return investment.DecisionRevise
		}

		// If base scenario misses target by 10-30%, recommend PAUSE
		if targetGap > 0.10 {
			vg.logger.Info("PAUSE: Base scenario marginally below target",
				"baseY10", baseY10,
				"target", wealthTarget,
				"gap", targetGap,
			)
			return investment.DecisionPause
		}
	}

	// Rule 2.5: Strategy-specific cash flow checks.
	// Appreciation strategy accepts negative CoC (returns come from price growth).
	// Cash-flow and balanced strategies require positive portfolio-level cash flow.
	if profile.Strategy == investment.StrategyCashFlow || profile.Strategy == investment.StrategyBalanced {
		avgCoC, negativeCFCount := computeCashFlowMetrics(config)
		total := len(config.Properties)

		if total > 0 {
			negativeFraction := float64(negativeCFCount) / float64(total)

			if avgCoC < 0 {
				// All properties (or majority) cash-flow negative — incompatible with income strategy
				vg.logger.Info("REVISE: Negative average CoC for cash-flow/balanced strategy",
					"avgCoC", avgCoC,
					"strategy", profile.Strategy,
					"negativeCFCount", negativeCFCount,
				)
				return investment.DecisionRevise
			}
			if negativeFraction > 0.5 {
				// More than half the properties have negative cash flow
				vg.logger.Info("PAUSE: Majority of properties have negative cash flow",
					"negativeFraction", negativeFraction,
					"strategy", profile.Strategy,
				)
				return investment.DecisionPause
			}
		}
	}

	// Rule 2.6: DSCR floor for conservative risk tolerance.
	// DSCR < 1.0 means the property cannot service its debt from NOI — unacceptable for conservative investors.
	if profile.RiskTolerance == investment.RiskConservative {
		avgDSCR := computeAverageDSCR(config)
		if avgDSCR < 1.0 && avgDSCR > 0 {
			vg.logger.Info("PAUSE: Average DSCR below 1.0 for conservative risk tolerance",
				"avgDSCR", avgDSCR,
			)
			return investment.DecisionPause
		}
	}

	// Rule 3: If Sharpe ratio is very low (<0.25), recommend REVISE.
	// Real estate Sharpe ratios of 0.25–0.60 are healthy; equity-market thresholds (0.5+) are too strict.
	if config.SharpeScore < 0.25 {
		vg.logger.Info("REVISE: Poor risk-adjusted returns",
			"sharpeScore", config.SharpeScore,
		)
		return investment.DecisionRevise
	}

	// Rule 4: If volatility is very high (>10%), recommend PAUSE
	if config.PortfolioVolatility > 10.0 {
		vg.logger.Info("PAUSE: High portfolio volatility",
			"volatility", config.PortfolioVolatility,
		)
		return investment.DecisionPause
	}

	// Default: PROCEED if no issues detected
	vg.logger.Info("PROCEED: Portfolio meets criteria",
		"sharpeScore", config.SharpeScore,
		"volatility", config.PortfolioVolatility,
	)
	return investment.DecisionProceed
}

// computeCashFlowMetrics returns the average cash-on-cash return and count of negative-CF properties.
func computeCashFlowMetrics(config *investment.FrontierPoint) (avgCoC float64, negativeCFCount int) {
	if len(config.Properties) == 0 {
		return 0, 0
	}
	total := 0.0
	for _, p := range config.Properties {
		total += p.CashOnCash
		if p.MonthlyCashFlow < 0 {
			negativeCFCount++
		}
	}
	return total / float64(len(config.Properties)), negativeCFCount
}

// computeAverageDSCR returns the average DSCR across portfolio properties.
func computeAverageDSCR(config *investment.FrontierPoint) float64 {
	if len(config.Properties) == 0 {
		return 0
	}
	total := 0.0
	for _, p := range config.Properties {
		total += p.DSCR
	}
	return total / float64(len(config.Properties))
}

// generateRationale creates 2-3 paragraph explanation for the recommendation
func (vg *Generator) generateRationale(
	config *investment.FrontierPoint,
	recommendation investment.DecisionRecommendation,
	wealthTarget int,
	profile investment.InvestorProfile,
) string {
	base := config.Scenarios.Base
	upside := config.Scenarios.Upside
	stress := config.Scenarios.Stress

	var rationale string

	// Compute cash flow summary once — used in all rationale branches
	_, negativeCFCount := computeCashFlowMetrics(config)
	totalMonthlyCF := 0
	for _, p := range config.Properties {
		totalMonthlyCF += p.MonthlyCashFlow
	}
	cashFlowNote := buildCashFlowNote(negativeCFCount, len(config.Properties), totalMonthlyCF, profile.Strategy)

	switch recommendation {
	case investment.DecisionProceed:
		rationale = fmt.Sprintf(
			"This portfolio configuration demonstrates strong fundamentals with a Sharpe ratio of %.2f and manageable volatility of %.2f%%. "+
				"Under the base scenario (P50 Monte Carlo), the portfolio is projected to achieve a net worth of $%s by Year 10, "+
				"with an upside potential of $%s (P75). The portfolio consists of %d carefully selected properties with a concentration index of %.2f, "+
				"indicating appropriate diversification.\n\n"+
				"The stress test scenario, while challenging with a Year 10 net worth of $%s, demonstrates portfolio resilience. "+
				"Mid-term outcomes (Year 5) remain viable, suggesting the portfolio can weather adverse market conditions without catastrophic losses. "+
				"This configuration aligns with your %s strategy and %s risk tolerance.%s\n\n"+
				"Recommendation: Proceed with this allocation. The portfolio offers a favorable risk-return profile with sufficient downside protection. "+
				"Monitor the key indicators outlined below to ensure market conditions remain supportive of the investment thesis.",
			config.SharpeScore,
			config.PortfolioVolatility,
			formatCurrency(base.FinalNetWorth),
			formatCurrency(upside.FinalNetWorth),
			len(config.Properties),
			config.ConcentrationIndex,
			formatCurrency(stress.FinalNetWorth),
			profile.Strategy,
			profile.RiskTolerance,
			cashFlowNote,
		)

	case investment.DecisionPause:
		targetInfo := ""
		if wealthTarget > 0 {
			targetInfo = fmt.Sprintf(" Your wealth target of $%s may not be reliably achieved under current projections.", formatCurrency(wealthTarget))
		}

		rationale = fmt.Sprintf(
			"This portfolio configuration shows mixed signals that warrant a cautious approach. While the base scenario (P50 Monte Carlo) projects a net worth of $%s by Year 10, "+
				"the uncertainty level or market conditions suggest waiting for greater clarity.%s The portfolio volatility of %.2f%% is elevated relative to expected returns.%s\n\n"+
				"The stress test reveals potential vulnerabilities, particularly in mid-term outcomes. "+
				"Although the portfolio maintains positive equity through Year 10, the downside scenario indicates material wealth erosion under adverse conditions. "+
				"Market conditions or portfolio composition may benefit from improved entry timing or allocation adjustments.\n\n"+
				"Recommendation: Pause this allocation decision. Consider waiting 3-6 months for market conditions to stabilize, or revise allocation parameters to improve risk-adjusted returns. "+
				"Monitor leading indicators closely to identify favorable entry points.",
			formatCurrency(base.FinalNetWorth),
			targetInfo,
			config.PortfolioVolatility,
			cashFlowNote,
		)

	case investment.DecisionRevise:
		rationale = fmt.Sprintf(
			"This portfolio configuration requires adjustment to better align with your investment goals. The current allocation shows a Sharpe ratio of %.2f, "+
				"indicating suboptimal risk-adjusted returns. Under the base scenario (P50 Monte Carlo), the portfolio projects a Year 10 net worth of $%s, "+
				"which may fall short of your target of $%s.%s\n\n"+
				"The concentration index of %.2f and portfolio composition suggest opportunities for improved diversification or property selection. "+
				"The upside scenario (P75) achieves $%s, demonstrating potential, but the stress test reveals vulnerabilities that could be mitigated through revised allocation.\n\n"+
				"Recommendation: Revise this allocation. Consider adjusting property selection to improve risk-adjusted returns, increasing diversification across markets, "+
				"or modifying acquisition timing. Re-run the analysis with revised parameters to generate a more robust portfolio configuration.",
			config.SharpeScore,
			formatCurrency(base.FinalNetWorth),
			formatCurrency(wealthTarget),
			cashFlowNote,
			config.ConcentrationIndex,
			formatCurrency(upside.FinalNetWorth),
		)
	}

	return rationale
}

// identifyKeyConditions extracts 3-5 critical conditions for thesis to hold
func (vg *Generator) identifyKeyConditions(config *investment.FrontierPoint) []string {
	conditions := []string{
		fmt.Sprintf("Portfolio maintains average annual return of %.2f%% across the 10-year projection period", config.ExpectedReturn),
		fmt.Sprintf("Property values appreciate at historical rates without prolonged market downturns exceeding -15%%"),
		fmt.Sprintf("Rental income remains stable with vacancy rates staying below 10%% annually"),
	}

	// Add concentration-specific condition if highly concentrated
	if config.ConcentrationIndex > 0.6 {
		conditions = append(conditions,
			fmt.Sprintf("Local market conditions in concentrated regions remain favorable (concentration index: %.2f)", config.ConcentrationIndex),
		)
	}

	// Add Track B condition if reinvestment plan exists
	if config.ReinvestmentPlan != nil && config.ReinvestmentPlan.TrackB.FiredProbability > 0.5 {
		conditions = append(conditions,
			fmt.Sprintf("Portfolio cash flow consistently exceeds $%s threshold for Track B reinvestment (%.0f%% probability)",
				formatCurrency(config.ReinvestmentPlan.TrackB.Threshold),
				config.ReinvestmentPlan.TrackB.FiredProbability*100,
			),
		)
	}

	return conditions
}

// generateMonitoringSignals creates 3+ leading indicators with thresholds
func (vg *Generator) generateMonitoringSignals(config *investment.FrontierPoint) []investment.MonitoringSignal {
	signals := []investment.MonitoringSignal{
		{
			Indicator:     "Portfolio Annual Return",
			Description:   "Actual annual appreciation vs projected baseline",
			CurrentValue:  config.ExpectedReturn,
			WatchLevel:    config.ExpectedReturn * 0.85, // -15%
			WarnLevel:     config.ExpectedReturn * 0.70, // -30%
			AlertLevel:    config.ExpectedReturn * 0.50, // -50%
			Justification: "Sustained underperformance suggests market conditions have deteriorated below projection assumptions",
		},
		{
			Indicator:     "Rental Vacancy Rate",
			Description:   "Weighted average vacancy across portfolio properties",
			CurrentValue:  5.0, // Assumption: 5% baseline
			WatchLevel:    7.5, // +50%
			WarnLevel:     10.0, // Double
			AlertLevel:    15.0, // Triple
			Justification: "Rising vacancy indicates weakening rental demand or oversupply in target markets",
		},
		{
			Indicator:     "Market Home Price Index (Aggregate)",
			Description:   "Weighted ZHVI across portfolio metro areas",
			CurrentValue:  100.0, // Baseline index
			WatchLevel:    95.0, // -5%
			WarnLevel:     90.0, // -10%
			AlertLevel:    85.0, // -15% (stress scenario threshold)
			Justification: "Home price declines reduce equity cushion and may trigger negative cash flow under stress conditions",
		},
	}

	// Add concentration-specific signal if highly concentrated
	if config.ConcentrationIndex > 0.6 {
		signals = append(signals, investment.MonitoringSignal{
			Indicator:     "Primary Market Employment Index",
			Description:   "Employment growth in the most concentrated metro area",
			CurrentValue:  100.0, // Baseline
			WatchLevel:    97.0, // -3%
			WarnLevel:     94.0, // -6%
			AlertLevel:    90.0, // -10%
			Justification: "Employment declines in concentrated markets directly impact rental demand and property values",
		})
	}

	return signals
}

// buildCashFlowNote constructs an informational note about negative cash flow properties.
// Returns empty string when all properties have positive cash flow.
func buildCashFlowNote(negativeCFCount, total, totalMonthlyCF int, strategy investment.InvestmentStrategy) string {
	if negativeCFCount == 0 || total == 0 {
		return ""
	}
	note := fmt.Sprintf(
		"\n\nCash Flow Note: %d of %d properties in this configuration have negative estimated monthly cash flow (portfolio total: %s/month). ",
		negativeCFCount, total, formatCurrency(totalMonthlyCF),
	)
	if strategy == investment.StrategyAppreciation {
		note += "For an appreciation-focused strategy this is expected — returns are primarily driven by property value growth rather than monthly income. " +
			"Ensure you have sufficient capital reserves to cover the monthly shortfall during the holding period."
	} else {
		note += "Budget for this monthly outlay in addition to rental income before proceeding."
	}
	return note
}

// formatCurrency formats an integer as a currency string
func formatCurrency(amount int) string {
	if amount < 0 {
		return fmt.Sprintf("-$%d", -amount)
	}
	return fmt.Sprintf("$%d", amount)
}
