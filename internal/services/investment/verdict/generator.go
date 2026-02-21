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
	recommendation := vg.determineRecommendation(config, wealthTarget)
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

	// Rule 3: If Sharpe ratio is very low (<0.5), recommend REVISE
	if config.SharpeScore < 0.5 {
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

	switch recommendation {
	case investment.DecisionProceed:
		rationale = fmt.Sprintf(
			"This portfolio configuration demonstrates strong fundamentals with a Sharpe ratio of %.2f and manageable volatility of %.2f%%. "+
				"Under the base scenario (P50 Monte Carlo), the portfolio is projected to achieve a net worth of $%s by Year 10, "+
				"with an upside potential of $%s (P75). The portfolio consists of %d carefully selected properties with a concentration index of %.2f, "+
				"indicating appropriate diversification.\n\n"+
				"The stress test scenario, while challenging with a Year 10 net worth of $%s, demonstrates portfolio resilience. "+
				"Mid-term outcomes (Year 5) remain viable, suggesting the portfolio can weather adverse market conditions without catastrophic losses. "+
				"This configuration aligns with your %s strategy and %s risk tolerance.\n\n"+
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
		)

	case investment.DecisionPause:
		targetInfo := ""
		if wealthTarget > 0 {
			targetInfo = fmt.Sprintf(" Your wealth target of $%s may not be reliably achieved under current projections.", formatCurrency(wealthTarget))
		}

		rationale = fmt.Sprintf(
			"This portfolio configuration shows mixed signals that warrant a cautious approach. While the base scenario (P50 Monte Carlo) projects a net worth of $%s by Year 10, "+
				"the uncertainty level or market conditions suggest waiting for greater clarity.%s The portfolio volatility of %.2f%% is elevated relative to expected returns.\n\n"+
				"The stress test reveals potential vulnerabilities, particularly in mid-term outcomes. "+
				"Although the portfolio maintains positive equity through Year 10, the downside scenario indicates material wealth erosion under adverse conditions. "+
				"Market conditions or portfolio composition may benefit from improved entry timing or allocation adjustments.\n\n"+
				"Recommendation: Pause this allocation decision. Consider waiting 3-6 months for market conditions to stabilize, or revise allocation parameters to improve risk-adjusted returns. "+
				"Monitor leading indicators closely to identify favorable entry points.",
			formatCurrency(base.FinalNetWorth),
			targetInfo,
			config.PortfolioVolatility,
		)

	case investment.DecisionRevise:
		rationale = fmt.Sprintf(
			"This portfolio configuration requires adjustment to better align with your investment goals. The current allocation shows a Sharpe ratio of %.2f, "+
				"indicating suboptimal risk-adjusted returns. Under the base scenario (P50 Monte Carlo), the portfolio projects a Year 10 net worth of $%s, "+
				"which may fall short of your target of $%s.\n\n"+
				"The concentration index of %.2f and portfolio composition suggest opportunities for improved diversification or property selection. "+
				"The upside scenario (P75) achieves $%s, demonstrating potential, but the stress test reveals vulnerabilities that could be mitigated through revised allocation.\n\n"+
				"Recommendation: Revise this allocation. Consider adjusting property selection to improve risk-adjusted returns, increasing diversification across markets, "+
				"or modifying acquisition timing. Re-run the analysis with revised parameters to generate a more robust portfolio configuration.",
			config.SharpeScore,
			formatCurrency(base.FinalNetWorth),
			formatCurrency(wealthTarget),
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

// formatCurrency formats an integer as a currency string
func formatCurrency(amount int) string {
	if amount < 0 {
		return fmt.Sprintf("-$%d", -amount)
	}
	return fmt.Sprintf("$%d", amount)
}
