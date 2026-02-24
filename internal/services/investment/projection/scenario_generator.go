package projection

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	"github.com/estara-ai/www/internal/services/investment"
)

// ScenarioGenerator creates Base/Upside/Stress scenarios from Monte Carlo results
// ADR-088 Phase 7: Scenario generation for decision support
type ScenarioGenerator struct {
	logger *slog.Logger
}

// NewScenarioGenerator creates a new scenario generator
func NewScenarioGenerator(logger *slog.Logger) *ScenarioGenerator {
	return &ScenarioGenerator{
		logger: logger,
	}
}

// GenerateScenarios creates Base, Upside, and Stress scenarios from MC results
func (sg *ScenarioGenerator) GenerateScenarios(
	ctx context.Context,
	mcResults *investment.SimulationResults,
	config *investment.FrontierPoint,
) (*investment.ScenarioSet, error) {
	if mcResults == nil {
		return nil, fmt.Errorf("MC results required for scenario generation")
	}

	if mcResults.Percentiles == nil {
		return nil, fmt.Errorf("MC percentiles required for scenario generation")
	}

	sg.logger.Info("generating scenarios from Monte Carlo results",
		"numPaths", len(mcResults.Paths),
		"projectionYears", len(mcResults.Percentiles.P50),
	)

	// Base Scenario: P50 (median) MC path
	baseScenario := sg.extractPercentileScenario(mcResults.Percentiles.P50, "Base")

	// Upside Scenario: P75 MC path
	upsideScenario := sg.extractPercentileScenario(mcResults.Percentiles.P75, "Upside")

	// Stress Scenario: Deterministic worst-case
	stressScenario, err := sg.generateStressScenario(ctx, config)
	if err != nil {
		sg.logger.Warn("failed to generate stress scenario, using placeholder",
			"error", err,
		)
		stressScenario = sg.createPlaceholderStressScenario(len(mcResults.Percentiles.P50))
	}

	scenarios := &investment.ScenarioSet{
		Base:   baseScenario,
		Upside: upsideScenario,
		Stress: stressScenario,
	}

	sg.logger.Info("scenarios generated",
		"baseY10NetWorth", baseScenario.FinalNetWorth,
		"upsideY10NetWorth", upsideScenario.FinalNetWorth,
		"stressY10NetWorth", stressScenario.FinalNetWorth,
	)

	return scenarios, nil
}

// extractPercentileScenario converts percentile outcomes to a scenario
func (sg *ScenarioGenerator) extractPercentileScenario(
	outcomes []investment.YearOutcome,
	name string,
) investment.Scenario {
	if len(outcomes) == 0 {
		return investment.Scenario{
			Name:           name,
			Type:           name,
			YearlyOutcomes: []investment.YearOutcome{},
			FinalNetWorth:  0,
		}
	}

	return investment.Scenario{
		Name:           name,
		Type:           name,
		YearlyOutcomes: outcomes,
		FinalNetWorth:  outcomes[len(outcomes)-1].NetWorth,
	}
}

// generateStressScenario creates a deterministic worst-case scenario
// ADR-088 Phase 7: Historical peak drop, adverse shocks
func (sg *ScenarioGenerator) generateStressScenario(
	ctx context.Context,
	config *investment.FrontierPoint,
) (investment.Scenario, error) {
	projectionYears := 10
	yearlyOutcomes := make([]investment.YearOutcome, projectionYears)

	// Initialize portfolio state using actual financing data from PropertyInPortfolio
	portfolioValue := 0
	totalLoanBalance := 0
	baseMonthlyPayment := 0
	baseMortgageRate := 0.065 // fallback rate if not derivable
	for _, prop := range config.Properties {
		portfolioValue += prop.Property.Price
		if prop.LoanAmount > 0 {
			totalLoanBalance += prop.LoanAmount
		} else {
			// Fallback: estimate 80% LTV only when actual data is missing
			totalLoanBalance += int(float64(prop.Property.Price) * 0.8)
		}
		if prop.MonthlyPayment > 0 {
			baseMonthlyPayment += prop.MonthlyPayment
		}
	}

	cumulativeCashFlow := 0

	// Stress test parameters (ADR-088 Phase 7)
	const (
		appreciationShock = -0.15 // Historical peak drop: -15% worst 12mo
		rentShock         = -0.05 // Rent shock: -2σ ≈ -5%
		vacancySpike      = 0.50  // Vacancy spike: +50% (from 5% to 7.5%)
		expenseShock      = 0.15  // Expense increase: +15%
		mortgageRateShock = 0.02  // Mortgage rate shock: +2%
		baseVacancyRate   = 0.05  // Base vacancy rate: 5%
		baseMgmtRate      = 0.08  // Base management rate: 8%
		baseExpenseRate   = 0.005 // Base monthly expense rate: 0.5% of value
		mortgageTermYears = 30    // 30-year mortgage
	)

	// Calculate stressed mortgage payment.
	// If we have actual monthly payments, scale them by the rate increase ratio.
	// Otherwise compute from the loan balance using the stressed rate.
	monthlyPayment := 0
	if baseMonthlyPayment > 0 && totalLoanBalance > 0 {
		// Scale actual payment by the rate shock ratio (approximate but avoids re-amortizing)
		stressedRate := baseMortgageRate + mortgageRateShock
		monthlyPayment = int(float64(baseMonthlyPayment) * (stressedRate / baseMortgageRate))
	} else if totalLoanBalance > 0 {
		stressedRate := baseMortgageRate + mortgageRateShock
		monthlyRate := stressedRate / 12.0
		numPayments := mortgageTermYears * 12
		monthlyPayment = int(float64(totalLoanBalance) * monthlyRate *
			math.Pow(1+monthlyRate, float64(numPayments)) /
			(math.Pow(1+monthlyRate, float64(numPayments)) - 1))
	}

	sg.logger.Info("stress scenario parameters",
		"appreciationShock", appreciationShock,
		"rentShock", rentShock,
		"vacancySpike", vacancySpike,
		"expenseShock", expenseShock,
		"mortgageRateShock", mortgageRateShock,
		"totalLoanBalance", totalLoanBalance,
		"baseMonthlyPayment", baseMonthlyPayment,
		"stressedMonthlyPayment", monthlyPayment,
	)

	// Simulate each year with stress conditions
	for year := 1; year <= projectionYears; year++ {
		// Apply appreciation shock in year 1, then gradual recovery
		yearAppreciation := 0
		if year == 1 {
			// Year 1: Full shock (-15%)
			yearAppreciation = int(float64(portfolioValue) * appreciationShock)
		} else {
			// Year 2+: Gradual recovery (2% annual appreciation)
			yearAppreciation = int(float64(portfolioValue) * 0.02)
		}
		portfolioValue += yearAppreciation

		// Calculate stressed cash flow
		yearRent := 0
		yearExpenses := 0
		for _, prop := range config.Properties {
			// Rent with shock and vacancy spike
			baseRent := prop.Property.EstimatedRent
			stressedRent := int(float64(baseRent) * (1 + rentShock)) // -5% rent
			stressedVacancy := baseVacancyRate * (1 + vacancySpike)  // 7.5% vacancy
			effectiveRent := int(float64(stressedRent) * (1 - stressedVacancy - baseMgmtRate))
			yearRent += effectiveRent * 12

			// Expenses with shock (+15%)
			baseExpenses := int(float64(prop.Property.Price) * baseExpenseRate * 12)
			stressedExpenses := int(float64(baseExpenses) * (1 + expenseShock))
			yearExpenses += stressedExpenses
		}

		// Calculate year cash flow
		yearMortgagePayments := monthlyPayment * 12
		yearCashFlow := yearRent - yearExpenses - yearMortgagePayments
		cumulativeCashFlow += yearCashFlow

		// Calculate equity (simplified - no amortization tracking)
		equity := portfolioValue - totalLoanBalance

		// Calculate net worth
		netWorth := equity + cumulativeCashFlow

		yearlyOutcomes[year-1] = investment.YearOutcome{
			Year:               year,
			PortfolioValue:     portfolioValue,
			Equity:             equity,
			CumulativeCashFlow: cumulativeCashFlow,
			NetWorth:           netWorth,
		}
	}

	return investment.Scenario{
		Name:           "Stress",
		Type:           "Stress",
		YearlyOutcomes: yearlyOutcomes,
		FinalNetWorth:  yearlyOutcomes[projectionYears-1].NetWorth,
	}, nil
}

// createPlaceholderStressScenario creates a fallback stress scenario
func (sg *ScenarioGenerator) createPlaceholderStressScenario(projectionYears int) investment.Scenario {
	yearlyOutcomes := make([]investment.YearOutcome, projectionYears)
	for i := range projectionYears {
		yearlyOutcomes[i] = investment.YearOutcome{
			Year:               i + 1,
			PortfolioValue:     0,
			Equity:             0,
			CumulativeCashFlow: 0,
			NetWorth:           0,
		}
	}

	return investment.Scenario{
		Name:           "Stress",
		Type:           "Stress",
		YearlyOutcomes: yearlyOutcomes,
		FinalNetWorth:  0,
	}
}
