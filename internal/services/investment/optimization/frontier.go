package optimization

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/projection"
	"github.com/estara-ai/www/internal/services/investment/verdict"
	"github.com/estara-ai/www/internal/services/market/fred"
)

// ProgressFunc is called to report progress during frontier generation.
// phase is 1-based (1=Validation, 2=Candidates, 3=Objectives, 4=Pareto, 5=Reinvestment, 6=MonteCarlo, 7=Scenarios, 8=Verdicts).
// ADR-088 Phase 9: Used by SSE streaming handler for real-time progress tracking.
type ProgressFunc func(phase int, totalPhases int, message string)

// FrontierOptimizer generates Pareto-optimal portfolio configurations
// ADR-088 Phase 3: Multi-objective optimization for efficient frontier
type FrontierOptimizer struct {
	logger            *slog.Logger
	markowitzCalc     *MarkowitzCalculator
	reinvestModeler   *projection.ReinvestmentModeler   // ADR-088 Phase 5
	mcSimulator       *projection.MonteCarloSimulator   // ADR-088 Phase 6
	scenarioGenerator *projection.ScenarioGenerator     // ADR-088 Phase 7
	verdictGenerator  *verdict.Generator                // ADR-088 Phase 8
	minProperties     int                                // Minimum properties per configuration (default: 5)
	maxProperties     int                                // Maximum properties per configuration (default: 8)
	numConfigurations int                                // Number of frontier points to generate (default: 5)
	riskFreeRate         float64                          // Risk-free rate for Sharpe ratio (default: 4.0%)
	fredService          *fred.Service
	correlationAnalyzer  *CorrelationAnalyzer // computes real Pearson correlations
}

// NewFrontierOptimizer creates a new frontier optimizer
func NewFrontierOptimizer(
	logger *slog.Logger,
	markowitzCalc *MarkowitzCalculator,
	reinvestModeler *projection.ReinvestmentModeler,
	mcSimulator *projection.MonteCarloSimulator,
	scenarioGenerator *projection.ScenarioGenerator,
	verdictGenerator *verdict.Generator,
	correlationAnalyzer *CorrelationAnalyzer,
	fredService *fred.Service,
) *FrontierOptimizer {
	return &FrontierOptimizer{
		logger:              logger,
		markowitzCalc:       markowitzCalc,
		reinvestModeler:     reinvestModeler,
		mcSimulator:         mcSimulator,
		scenarioGenerator:   scenarioGenerator,
		verdictGenerator:    verdictGenerator,
		minProperties:       5,
		maxProperties:       8,
		numConfigurations:   5,
		riskFreeRate:        4.0, // fallback if FRED unavailable
		correlationAnalyzer: correlationAnalyzer,
		fredService:         fredService,
	}
}

// OptimizationObjectives holds the 4 objectives for multi-objective optimization
type OptimizationObjectives struct {
	ExpectedReturn      float64 // Maximize
	PortfolioVolatility float64 // Minimize (Markowitz analytical)
	ConcentrationIndex  float64 // Minimize
	StressTestEquity    int     // Maximize
}

// PortfolioConfiguration represents a candidate portfolio configuration
type PortfolioConfiguration struct {
	Properties         []investment.Property
	Weights            []float64 // Property weights (normalized by value)
	Objectives         OptimizationObjectives
	SharpeScore        float64
	Rank               int
	DominationCount    int // Number of solutions this dominates
	DominatedBy        int // Number of solutions that dominate this
	Label              string // "Quality", "Income", or "Balanced" — set by createConfiguration
}

// GenerateFrontier generates 3-5 Pareto-optimal portfolio configurations.
// ADR-088 Phase 3: Multi-objective NSGA-II-inspired algorithm.
// ADR-090: accepts []PropertyCohort from BuildCohorts; each cohort drives a distinct
// set of candidate configurations so frontier points are meaningfully differentiated.
// progress may be nil; when provided it receives 8-phase updates for SSE streaming (Phase 9).
func (fo *FrontierOptimizer) GenerateFrontier(
	ctx context.Context,
	cohorts []investment.PropertyCohort,
	profile investment.InvestorProfile,
	params investment.InvestmentPlanningParams,
	progress ProgressFunc,
) ([]investment.FrontierPoint, error) {
	const totalPhases = 8
	reportProgress := func(phase int, msg string) {
		if progress != nil {
			progress(phase, totalPhases, msg)
		}
	}

	reportProgress(1, "Validating input properties")

	// Validate: at least one cohort must have enough properties for a minimum portfolio.
	maxCohortSize := 0
	for _, c := range cohorts {
		if len(c.Properties) > maxCohortSize {
			maxCohortSize = len(c.Properties)
		}
	}
	if maxCohortSize < fo.minProperties {
		return nil, fmt.Errorf("insufficient properties: need at least %d per cohort, largest has %d", fo.minProperties, maxCohortSize)
	}

	fo.logger.Info("generating efficient frontier",
		"cohorts", len(cohorts),
		"maxCohortSize", maxCohortSize,
		"minPerConfig", fo.minProperties,
		"maxPerConfig", fo.maxProperties,
		"targetConfigs", fo.numConfigurations,
	)

	// Step 1: Generate candidate configurations from each cohort at varying sizes.
	// ADR-090: each cohort's pre-ranked properties produce size variants (5-8 props).
	reportProgress(2, "Generating candidate portfolio configurations")
	candidates := fo.generateCandidatesFromCohorts(cohorts)
	fo.logger.Info("generated candidate configurations", "count", len(candidates))

	// Step 2: Evaluate all objectives for each candidate.
	// ADR-089 Phase 5: pass ExistingPortfolio so concentration penalises market overlap.
	// ADR-090: profile.Strategy and profile.RiskTolerance feed into expected-return weights
	// and risk-adjusted Sharpe calculation.
	reportProgress(3, "Evaluating multi-objective portfolio metrics")
	// Use market-data-derived appreciation rate when available; fall back to 4.0% default.
	appreciationRate := 4.0
	if params.MarketAppreciationRate > 0 {
		appreciationRate = params.MarketAppreciationRate
	}

	// Pre-compute risk-free rate from FRED (live T-bill rate).
	// Falls back to struct default (4.0%) when FRED unavailable.
	liveRiskFreeRate := fo.riskFreeRate
	if fo.fredService != nil {
		if rates, err := fo.fredService.GetAllRates(ctx); err == nil && rates != nil && rates.TBillRate > 0 {
			liveRiskFreeRate = rates.TBillRate
			fo.logger.Debug("using live FRED T-bill rate", "rate_pct", liveRiskFreeRate)
		}
	}

	// Pre-compute pairwise market correlations from ZHVI/ZORI data.
	// Build a lookup map: "City1, State1|City2, State2" -> correlation coefficient.
	// Falls back to hardcoded 0.7/0.3 when analyzer unavailable or data insufficient.
	pairCorrelations := fo.computePairCorrelations(ctx, params.Locations)

	// Pre-compute per-city annualised price volatility from ZHVI time series.
	// Falls back to heuristic when market data is unavailable.
	cityVolatilities := fo.computeCityVolatilities(ctx, params.Locations)

	// Configure MC simulator with market-data-driven rates.
	// RentGrowth defaults to appreciation x 0.6 (rent tends to grow slower than prices).
	rentGrowthRate := appreciationRate * 0.6
	if rentGrowthRate < 1.5 {
		rentGrowthRate = 1.5
	}
	if fo.mcSimulator != nil {
		fo.mcSimulator.SetMarketRates(appreciationRate, rentGrowthRate)
	}

	for i := range candidates {
		fo.evaluateObjectives(&candidates[i], profile, params, appreciationRate, liveRiskFreeRate, pairCorrelations, cityVolatilities)
	}

	// Step 3: Apply Pareto dominance to find non-dominated solutions.
	reportProgress(4, "Finding Pareto-optimal configurations")
	nonDominated := fo.findNonDominatedSolutions(candidates)
	fo.logger.Info("found non-dominated solutions", "count", len(nonDominated))

	// Step 4: Rank by Sharpe ratio and select top configurations.
	frontierConfigs := fo.selectFrontierPoints(nonDominated, fo.numConfigurations)

	// Build score and thesis lookups from all cohorts so PropertyInPortfolio is fully populated.
	scoreByID := make(map[string]float64)
	thesisByID := make(map[string]*investment.PropertyThesis)
	for _, cohort := range cohorts {
		for _, rp := range cohort.Properties {
			scoreByID[rp.Property.ID] = rp.OverallScore
			if rp.Thesis != nil {
				thesisByID[rp.Property.ID] = rp.Thesis
			}
		}
	}

	// Compute existing portfolio summary once (shared across all frontier points)
	// ADR-089 Phase 3: attach to each FrontierPoint so the frontend can display portfolio context
	var existingPortfolioSummary *investment.ExistingPortfolioSummary
	if params.ExistingPortfolio != nil && len(params.ExistingPortfolio.Properties) > 0 {
		existingPortfolioSummary = fo.summarizeExistingPortfolio(params.ExistingPortfolio)
	}

	// Step 5-8: Convert to FrontierPoint format with financial projections
	reportProgress(5, "Calculating reinvestment plans")
	frontierPoints := make([]investment.FrontierPoint, 0, len(frontierConfigs))
	for i, config := range frontierConfigs {
		portfolioProps := fo.buildPortfolioProperties(config.Properties, scoreByID, thesisByID, params)

		// Run phases 5-8 (Reinvestment → MC → Scenarios → Verdict)
		// Pass existingPortfolioSummary so MC and scenario projections include existing holdings
		fp := fo.regenerateConfigPhases(ctx, i, portfolioProps, config, profile, params, existingPortfolioSummary, progress)
		fp.ExistingPortfolio = existingPortfolioSummary // ADR-089 Phase 3
		frontierPoints = append(frontierPoints, fp)
	}

	fo.logger.Info("frontier generation complete",
		"frontierPoints", len(frontierPoints),
		"avgReturn", fo.avgReturn(frontierPoints),
		"avgVolatility", fo.avgVolatility(frontierPoints),
	)

	return frontierPoints, nil
}

// buildPortfolioProperties converts Property slice to PropertyInPortfolio.
// scoreByID maps property ID → OverallScore; thesisByID maps ID → PropertyThesis.
// Uses mortgage rate and down payment from params; falls back to 7% / 25% if zero.
func (fo *FrontierOptimizer) buildPortfolioProperties(
	scoredProps []investment.Property,
	scoreByID map[string]float64,
	thesisByID map[string]*investment.PropertyThesis,
	params investment.InvestmentPlanningParams,
) []investment.PropertyInPortfolio {
	mortgageRate := params.MortgageRate * 100 // params stores as decimal (e.g. 0.075), need %
	if mortgageRate <= 0 {
		mortgageRate = 7.0 // fallback
	}
	downPaymentPct := params.DownPaymentPct
	if downPaymentPct <= 0 {
		downPaymentPct = 0.25 // fallback
	}

	props := make([]investment.PropertyInPortfolio, 0, len(scoredProps))
	for _, prop := range scoredProps {
		downPayment := int(float64(prop.Price) * downPaymentPct)
		loanAmount := prop.Price - downPayment
		monthlyPayment := fo.calculateMortgagePayment(loanAmount, mortgageRate, 30)
		// Age-adjusted NOI: apply expense ratio that incorporates CapEx reserves.
		expRatio := investment.ExpenseRatioForAge(prop.YearBuilt)
		monthlyNOI := int(float64(prop.EstimatedRent) * (1 - expRatio))
		monthlyCashFlow := monthlyNOI - monthlyPayment
		capRate := investment.CapRatePct(prop.EstimatedRent, prop.Price, prop.YearBuilt)
		cashOnCash := float64(monthlyCashFlow*12) / float64(downPayment) * 100
		// DSCR = NOI / Debt Service (correct formula uses NOI, not gross rent)
		dscr := 0.0
		if monthlyPayment > 0 {
			dscr = float64(monthlyNOI) / float64(monthlyPayment)
		}

		props = append(props, investment.PropertyInPortfolio{
			Property:        prop,
			DownPayment:     downPayment,
			LoanAmount:      loanAmount,
			MonthlyPayment:  monthlyPayment,
			MonthlyCashFlow: monthlyCashFlow,
			CapRate:         capRate,
			CashOnCash:      cashOnCash,
			DSCR:            dscr,
			Score:           scoreByID[prop.ID],
			Thesis:          thesisByID[prop.ID], // ADR-089: carry thesis through to FrontierPoint
		})
	}
	return props
}

// regenerateConfigPhases runs phases 5-8 (Reinvestment, Monte Carlo, Scenarios, Verdict) for
// a single frontier configuration. Extracted to support the /recalculate fast path (Phase 9).
func (fo *FrontierOptimizer) regenerateConfigPhases(
	ctx context.Context,
	configIndex int,
	properties []investment.PropertyInPortfolio,
	config PortfolioConfiguration,
	profile investment.InvestorProfile,
	params investment.InvestmentPlanningParams,
	existingPortfolio *investment.ExistingPortfolioSummary,
	progress ProgressFunc,
) investment.FrontierPoint {
	const totalPhases = 8

	// configPoint is the shared FrontierPoint passed to MC / scenario / verdict.
	// Populate ExistingPortfolio now so all downstream phases see the combined portfolio.
	configPoint := &investment.FrontierPoint{
		ConfigIndex:       configIndex,
		Properties:        properties,
		ExistingPortfolio: existingPortfolio,
	}

	// ADR-088 Phase 6.2: Two-phase MC + Reinvestment pipeline
	// Phase 1: Create preliminary reinvestment plan (Track A + Track B threshold)
	preliminaryPlan, err := fo.reinvestModeler.CalculateReinvestmentPlan(ctx, configPoint, params, nil) // nil mcResults = placeholder Track B values
	if err != nil {
		fo.logger.Warn("failed to calculate preliminary reinvestment plan, using nil",
			"configIndex", configIndex,
			"error", err,
		)
		preliminaryPlan = nil
	}

	// Phase 2: Run Monte Carlo simulation with preliminary plan
	if progress != nil {
		progress(6, totalPhases, fmt.Sprintf("Running Monte Carlo simulation for config %d", configIndex))
	}
	var mcResults *investment.SimulationResults
	if fo.mcSimulator != nil && preliminaryPlan != nil {
		mcResults, err = fo.mcSimulator.SimulateConfiguration(ctx, configPoint, preliminaryPlan)
		if err != nil {
			fo.logger.Warn("Monte Carlo simulation failed, using nil results",
				"configIndex", configIndex,
				"error", err,
			)
			mcResults = nil
		}
	}

	// Phase 3: Update reinvestment plan with MC-derived Track B statistics
	var reinvestPlan *investment.DualTrackReinvestment
	if mcResults != nil {
		reinvestPlan, err = fo.reinvestModeler.CalculateReinvestmentPlan(ctx, configPoint, params, mcResults) // Pass MC results for Track B statistics
		if err != nil {
			fo.logger.Warn("failed to update reinvestment plan with MC results, using preliminary",
				"configIndex", configIndex,
				"error", err,
			)
			reinvestPlan = preliminaryPlan
		}
	} else {
		reinvestPlan = preliminaryPlan
	}

	// Phase 4: Generate decision support scenarios (ADR-088 Phase 7)
	if progress != nil {
		progress(7, totalPhases, fmt.Sprintf("Generating scenarios for config %d", configIndex))
	}
	var scenarios *investment.ScenarioSet
	if fo.scenarioGenerator != nil && mcResults != nil {
		scenarios, err = fo.scenarioGenerator.GenerateScenarios(ctx, mcResults, configPoint)
		if err != nil {
			fo.logger.Warn("failed to generate scenarios, using nil",
				"configIndex", configIndex,
				"error", err,
			)
			scenarios = nil
		}
	}

	// Phase 5: Generate decision verdict (ADR-088 Phase 8)
	if progress != nil {
		progress(8, totalPhases, fmt.Sprintf("Generating decision verdict for config %d", configIndex))
	}
	var decisionVerdict *investment.DecisionVerdict
	if fo.verdictGenerator != nil && scenarios != nil {
		// Populate verdict config with full context (scenarios, objectives, existing portfolio)
		configPoint.ExpectedReturn = config.Objectives.ExpectedReturn
		configPoint.PortfolioVolatility = config.Objectives.PortfolioVolatility
		configPoint.SharpeScore = config.SharpeScore
		configPoint.ConcentrationIndex = config.Objectives.ConcentrationIndex
		configPoint.Scenarios = scenarios
		configPoint.ReinvestmentPlan = reinvestPlan
		decisionVerdict, err = fo.verdictGenerator.GenerateVerdict(ctx, configPoint, profile, 0) // wealth target = 0 (no target for now)
		if err != nil {
			fo.logger.Warn("failed to generate verdict, using nil",
				"configIndex", configIndex,
				"error", err,
			)
			decisionVerdict = nil
		}
	}

	return investment.FrontierPoint{
		ConfigIndex:         configIndex,
		Label:               config.Label,
		Properties:          properties,
		ExpectedReturn:      config.Objectives.ExpectedReturn,
		PortfolioVolatility: config.Objectives.PortfolioVolatility,
		SharpeScore:         config.SharpeScore,
		ConcentrationIndex:  config.Objectives.ConcentrationIndex,
		StressTestEquity:    config.Objectives.StressTestEquity,
		SimulationResults:   mcResults,       // Phase 6: Monte Carlo simulation results
		ReinvestmentPlan:    reinvestPlan,    // Phase 5+6: Dual-track with MC-updated Track B
		Scenarios:           scenarios,       // Phase 7: Decision support scenarios
		DecisionVerdict:     decisionVerdict, // Phase 8: AI recommendation
	}
}

// Recalculate re-runs phases 5-8 (Reinvestment, MC, Scenarios, Verdict) on existing frontier
// configurations with updated assumption overrides. This is the fast path for the interactive
// workspace sliders (ADR-088 Phase 9): skips property selection and Markowitz optimization.
// Phase C: re-evaluates Markowitz objectives so ExpectedReturn/Sharpe/Volatility reflect the
// new assumptions (appreciation rate override, recalculated NOI from mortgage rate override).
func (fo *FrontierOptimizer) Recalculate(
	ctx context.Context,
	existing []investment.FrontierPoint,
	profile investment.InvestorProfile,
	params investment.InvestmentPlanningParams,
	overrides investment.AssumptionOverrides,
) ([]investment.FrontierPoint, error) {
	if len(existing) == 0 {
		return nil, fmt.Errorf("no frontier configurations provided for recalculation")
	}

	// Determine effective appreciation rate for objective re-evaluation.
	appreciationRate := 4.0 // default (matches GenerateFrontier)
	if overrides.AppreciationRate != nil {
		appreciationRate = *overrides.AppreciationRate
	}

	results := make([]investment.FrontierPoint, 0, len(existing))
	for _, fp := range existing {
		// Rebuild portfolio properties with new mortgage rate (if provided).
		// Phase C: applyMortgageRateOverride now uses proper NOI formula.
		properties := fp.Properties
		if overrides.MortgageRate != nil {
			properties = fo.applyMortgageRateOverride(fp.Properties, *overrides.MortgageRate)
		}

		// Reconstruct PortfolioConfiguration with actual Properties and Weights so
		// evaluateObjectives can re-compute Markowitz metrics from scratch.
		rawProps := make([]investment.Property, len(properties))
		totalValue := 0
		for i, pip := range properties {
			rawProps[i] = pip.Property
			totalValue += pip.Property.Price
		}
		weights := make([]float64, len(rawProps))
		for i, p := range rawProps {
			if totalValue > 0 {
				weights[i] = float64(p.Price) / float64(totalValue)
			}
		}
		config := PortfolioConfiguration{
			Properties: rawProps,
			Weights:    weights,
			Label:      fp.Label,
		}

		// Re-evaluate Markowitz objectives with the updated property set and appreciation rate.
		// Pass nil for existingPortfolio: fp.ExistingPortfolio is *ExistingPortfolioSummary
		// (a condensed summary), not *ExistingPortfolio (full type). The concentration index
		// change from a mortgage-rate override is negligible so omitting portfolio context here
		// is correct — the original Pareto geometry is preserved.
		// Fetch live T-bill rate for Recalculate path.
		recalcRFR := fo.riskFreeRate
		if fo.fredService != nil {
			if rates, err := fo.fredService.GetAllRates(ctx); err == nil && rates != nil && rates.TBillRate > 0 {
				recalcRFR = rates.TBillRate
			}
		}
		recalcCorrelations := fo.computePairCorrelations(ctx, params.Locations)
		recalcVolatilities := fo.computeCityVolatilities(ctx, params.Locations)
		fo.evaluateObjectives(&config, profile, params, appreciationRate, recalcRFR, recalcCorrelations, recalcVolatilities)

		updated := fo.regenerateConfigPhases(ctx, fp.ConfigIndex, properties, config, profile, params, fp.ExistingPortfolio, nil)
		results = append(results, updated)
	}

	fo.logger.Info("recalculation complete",
		"configCount", len(results),
		"mortgageOverride", overrides.MortgageRate,
		"appreciationOverride", overrides.AppreciationRate,
		"rentGrowthOverride", overrides.RentGrowthRate,
	)

	return results, nil
}

// applyMortgageRateOverride rebuilds PropertyInPortfolio metrics using the new mortgage rate.
// Phase C: uses expense-ratio NOI formula matching buildPortfolioProperties (no flat $500 estimate).
func (fo *FrontierOptimizer) applyMortgageRateOverride(
	existing []investment.PropertyInPortfolio,
	mortgageRate float64,
) []investment.PropertyInPortfolio {
	updated := make([]investment.PropertyInPortfolio, 0, len(existing))
	for _, p := range existing {
		downPayment := p.DownPayment
		loanAmount := p.LoanAmount
		monthlyPayment := fo.calculateMortgagePayment(loanAmount, mortgageRate, 30)
		// Age-adjusted NOI: consistent with buildPortfolioProperties
		expRatio := investment.ExpenseRatioForAge(p.Property.YearBuilt)
		monthlyNOI := int(float64(p.Property.EstimatedRent) * (1 - expRatio))
		monthlyCashFlow := monthlyNOI - monthlyPayment
		cashOnCash := 0.0
		if downPayment > 0 {
			cashOnCash = float64(monthlyCashFlow*12) / float64(downPayment) * 100
		}
		// DSCR uses NOI (not gross rent) — consistent with buildPortfolioProperties
		dscr := 0.0
		if monthlyPayment > 0 {
			dscr = float64(monthlyNOI) / float64(monthlyPayment)
		}

		updated = append(updated, investment.PropertyInPortfolio{
			Property:        p.Property,
			DownPayment:     downPayment,
			LoanAmount:      loanAmount,
			MonthlyPayment:  monthlyPayment,
			MonthlyCashFlow: monthlyCashFlow,
			CapRate:         p.CapRate, // Cap rate is independent of mortgage
			CashOnCash:      cashOnCash,
			DSCR:            dscr,
			Score:           p.Score,
			Thesis:          p.Thesis,
		})
	}
	return updated
}

// generateCandidatesFromCohorts creates candidate portfolio configurations from pre-ranked
// property cohorts. Each cohort produces size variants (minProperties to maxProperties).
// ADR-090: replaces generateCandidates; per-cohort ranking is done in BuildCohorts.
func (fo *FrontierOptimizer) generateCandidatesFromCohorts(
	cohorts []investment.PropertyCohort,
) []PortfolioConfiguration {
	candidates := make([]PortfolioConfiguration, 0, len(cohorts)*(fo.maxProperties-fo.minProperties+1))
	for _, cohort := range cohorts {
		for size := fo.minProperties; size <= fo.maxProperties && size <= len(cohort.Properties); size++ {
			config := fo.createConfigFromCohort(cohort, size)
			candidates = append(candidates, config)
		}
	}
	return candidates
}

// createConfigFromCohort builds a single PortfolioConfiguration by taking the top-N
// properties from an already-ranked PropertyCohort. The cohort label is preserved.
// ADR-090: replaces createConfiguration; no re-ranking happens here.
func (fo *FrontierOptimizer) createConfigFromCohort(
	cohort investment.PropertyCohort,
	size int,
) PortfolioConfiguration {
	n := size
	if n > len(cohort.Properties) {
		n = len(cohort.Properties)
	}
	if n == 0 {
		return PortfolioConfiguration{Label: cohort.Label}
	}

	selectedProps := make([]investment.Property, n)
	for i := range n {
		selectedProps[i] = cohort.Properties[i].Property
	}

	// Value-weighted portfolio weights.
	totalValue := 0
	for _, p := range selectedProps {
		totalValue += p.Price
	}
	weights := make([]float64, n)
	for i, p := range selectedProps {
		if totalValue > 0 {
			weights[i] = float64(p.Price) / float64(totalValue)
		}
	}

	return PortfolioConfiguration{
		Properties: selectedProps,
		Weights:    weights,
		Label:      cohort.Label,
	}
}

// evaluateObjectives calculates all 4 objectives for a configuration.
// ADR-089 Phase 5: existingPortfolio is optional; when provided, concentration
// penalises market overlap with existing holdings.
// ADR-090: profile.Strategy and profile.RiskTolerance feed into expected-return blending
// and risk-adjusted Sharpe ratio (risk-free rate + volatility multiplier per risk level).
// Phase C: appreciationRate is explicit so Recalculate can override the default 4.0%.
func (fo *FrontierOptimizer) evaluateObjectives(
	config *PortfolioConfiguration,
	profile investment.InvestorProfile,
	params investment.InvestmentPlanningParams,
	appreciationRate float64,
	riskFreeRate float64,
	pairCorrelations map[string]float64,
	cityVolatilities map[string]float64,
) {
	// Objective 1: Expected Return — strategy-aware blend of income and appreciation.
	config.Objectives.ExpectedReturn = fo.calculateExpectedReturn(config, profile.Strategy, appreciationRate)

	// Objective 2: Portfolio Volatility via Markowitz (minimize).
	config.Objectives.PortfolioVolatility = fo.calculatePortfolioVolatility(config, pairCorrelations, cityVolatilities)

	// Objective 3: Concentration Index (minimize) — portfolio-aware when holdings provided.
	config.Objectives.ConcentrationIndex = fo.calculateConcentrationIndex(config, params.ExistingPortfolio)

	// Objective 4: Stress Test Equity (maximize).
	config.Objectives.StressTestEquity = fo.calculateStressTestEquity(config, profile, params)

	// Risk-adjusted Sharpe: adjust the live rate by risk tolerance tier.
	// Conservative adds 0.5% hurdle; Aggressive subtracts 1.0%.
	adjustedRFR := riskFreeRate
	switch profile.RiskTolerance {
	case investment.RiskConservative:
		adjustedRFR += 0.5
	case investment.RiskAggressive:
		adjustedRFR -= 1.0
		if adjustedRFR < 1.0 {
			adjustedRFR = 1.0
		}
	}
	volMultiplier := fo.volatilityMultiplierForRisk(profile.RiskTolerance)
	excessReturn  := config.Objectives.ExpectedReturn - adjustedRFR
	adjustedVol   := config.Objectives.PortfolioVolatility * volMultiplier
	if adjustedVol > 0 {
		config.SharpeScore = excessReturn / adjustedVol
	} else {
		config.SharpeScore = 0
	}
}

// riskFreeRateForRisk returns the Sharpe hurdle rate for the given risk tolerance.
// ADR-090: conservative investors use a higher hurdle (4.5%) so only genuinely strong
// risk-adjusted returns clear the bar; aggressive investors use 3.0%.
func (fo *FrontierOptimizer) riskFreeRateForRisk(risk investment.RiskTolerance) float64 {
	switch risk {
	case investment.RiskConservative:
		return 4.5
	case investment.RiskAggressive:
		return 3.0
	default:
		return fo.riskFreeRate // 4.0% default (Moderate)
	}
}

// volatilityMultiplierForRisk scales the Sharpe denominator (portfolio volatility)
// to amplify or reduce the risk penalty based on investor risk tolerance.
// ADR-090: Conservative ×1.3 (penalises volatility more), Aggressive ×0.7 (rewards return-seeking).
func (fo *FrontierOptimizer) volatilityMultiplierForRisk(risk investment.RiskTolerance) float64 {
	switch risk {
	case investment.RiskConservative:
		return 1.3
	case investment.RiskAggressive:
		return 0.7
	default:
		return 1.0
	}
}

// calculateExpectedReturn estimates annual expected return using a strategy-dependent
// blend of income (cap rate) and appreciation components.
// ADR-090: cash-flow investors weight income higher (70%); appreciation investors weight
// price growth higher (70%). Balanced/risk-adjusted use a 50/40-60 split.
// Phase C: appreciationRate is explicit (default 4.0) so Recalculate can thread overrides through.
func (fo *FrontierOptimizer) calculateExpectedReturn(config *PortfolioConfiguration, strategy investment.InvestmentStrategy, appreciationRate float64) float64 {
	var apprWeight, incomeWeight float64
	switch strategy {
	case investment.StrategyCashFlow:
		apprWeight, incomeWeight = 0.30, 0.70
	case investment.StrategyAppreciation:
		apprWeight, incomeWeight = 0.70, 0.30
	case investment.StrategyRiskAdjusted:
		apprWeight, incomeWeight = 0.40, 0.60
	default: // Balanced
		apprWeight, incomeWeight = 0.50, 0.50
	}

	totalReturn := 0.0
	for i, prop := range config.Properties {
		capRate := investment.CapRatePct(prop.EstimatedRent, prop.Price, prop.YearBuilt)
		propertyReturn := apprWeight*appreciationRate + incomeWeight*capRate
		totalReturn += propertyReturn * config.Weights[i]
	}
	return totalReturn
}

// calculatePortfolioVolatility uses Markowitz analytical formula
// σ² = Σᵢ Σⱼ wᵢ wⱼ σᵢ σⱼ ρᵢⱼ
func (fo *FrontierOptimizer) calculatePortfolioVolatility(config *PortfolioConfiguration, pairCorrelations map[string]float64, cityVolatilities map[string]float64) float64 {
	n := len(config.Properties)
	if n == 0 {
		return 0.0
	}

	// Get volatilities for each property using market data where available.
	volatilities := make([]float64, n)
	for i, prop := range config.Properties {
		volatilities[i] = fo.estimatePropertyVolatility(prop, cityVolatilities)
	}

	// Build correlation matrix using real Pearson correlations where available.
	correlationMatrix := fo.buildCorrelationMatrix(n, config.Properties, pairCorrelations)

	// Use Markowitz calculator from Phase 0
	variance := fo.markowitzCalc.CalculatePortfolioVariance(config.Weights, volatilities, correlationMatrix)

	// Return standard deviation (volatility)
	return math.Sqrt(variance)
}

// estimatePropertyVolatility estimates annualised price volatility for a property.
// Prefers real ZHVI-derived volatility from cityVolatilities when available,
// falling back to a heuristic based on property price and location type.
func (fo *FrontierOptimizer) estimatePropertyVolatility(prop investment.Property, cityVolatilities map[string]float64) float64 {
	// Prefer real market-data volatility (computed from ZHVI monthly returns).
	if len(cityVolatilities) > 0 {
		key := prop.City + ", " + prop.State
		if vol, ok := cityVolatilities[key]; ok && vol > 0 {
			return vol
		}
	}

	// Heuristic fallback: base 5% adjusted for price tier.
	// Higher-priced markets tend to have higher nominal volatility.
	baseVol := 5.0
	if prop.Price > 500000 {
		baseVol += 1.0
	}
	if prop.Price > 1000000 {
		baseVol += 1.0
	}
	return baseVol
}

// buildCorrelationMatrix creates a correlation matrix using real Pearson correlations where available.
func (fo *FrontierOptimizer) buildCorrelationMatrix(n int, properties []investment.Property, pairCorrelations map[string]float64) [][]float64 {
	matrix := make([][]float64, n)
	for i := 0; i < n; i++ {
		matrix[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			if i == j {
				matrix[i][j] = 1.0
				continue
			}
			marketI := properties[i].City + ", " + properties[i].State
			marketJ := properties[j].City + ", " + properties[j].State
			if marketI == marketJ {
				matrix[i][j] = 0.7 // same-market fallback (will be overridden by real data if available)
			} else {
				matrix[i][j] = 0.3 // cross-market fallback
			}
			// Override with real Pearson correlation if available
			key := marketI + "|" + marketJ
			if corr, ok := pairCorrelations[key]; ok {
				// Clamp to valid [-1, 1] and handle NaN
				if corr >= -1.0 && corr <= 1.0 {
					matrix[i][j] = corr
				}
			}
		}
	}
	return matrix
}

// calculateConcentrationIndex computes weighted concentration metric.
// Combines HHI (market, submarket, type) + spatial concentration + portfolio overlap (ADR-089 Phase 5).
// existingPortfolio is optional; when nil the formula is unchanged from the original 3-component blend.
func (fo *FrontierOptimizer) calculateConcentrationIndex(
	config *PortfolioConfiguration,
	existingPortfolio *investment.ExistingPortfolio,
) float64 {
	// HHI (Herfindahl-Hirschman Index) for market concentration
	marketHHI := fo.calculateMarketHHI(config)

	// Spatial concentration (geographic clustering)
	spatialConc := fo.calculateSpatialConcentration(config)

	// Property type concentration
	typeHHI := fo.calculateTypeHHI(config)

	if existingPortfolio == nil || len(existingPortfolio.Properties) == 0 {
		// Original formula: 50% market HHI, 30% spatial, 20% type
		return 0.5*marketHHI + 0.3*spatialConc + 0.2*typeHHI
	}

	// ADR-089 Phase 5: portfolio overlap — fraction of candidate value in markets
	// where the user already has holdings. Higher overlap → worse diversification.
	overlapScore := fo.calculatePortfolioOverlap(config, existingPortfolio)

	// With portfolio context: 40% market HHI, 25% spatial, 15% type, 20% portfolio overlap
	return 0.40*marketHHI + 0.25*spatialConc + 0.15*typeHHI + 0.20*overlapScore
}

// calculatePortfolioOverlap returns a [0, 1] score representing how much the candidate
// configuration's market exposure overlaps with the user's existing holdings.
// 0 = fully independent markets (ideal diversification), 1 = complete overlap.
// ADR-089 Phase 5: correlation-aware diversification for frontier multi-objective evaluation.
func (fo *FrontierOptimizer) calculatePortfolioOverlap(
	config *PortfolioConfiguration,
	ep *investment.ExistingPortfolio,
) float64 {
	if ep == nil || len(ep.Properties) == 0 {
		return 0.0
	}

	// Build set of markets already in the user's portfolio
	existingMarkets := make(map[string]struct{}, len(ep.Properties))
	for _, p := range ep.Properties {
		market := p.City + ", " + p.State
		existingMarkets[market] = struct{}{}
	}

	// Total candidate portfolio value
	totalValue := 0.0
	for _, prop := range config.Properties {
		totalValue += float64(prop.Price)
	}
	if totalValue == 0 {
		return 0.0
	}

	// Fraction of candidate value in overlapping markets
	overlapValue := 0.0
	for _, prop := range config.Properties {
		market := prop.City + ", " + prop.State
		if _, exists := existingMarkets[market]; exists {
			overlapValue += float64(prop.Price)
		}
	}

	return math.Min(overlapValue/totalValue, 1.0)
}

// calculateMarketHHI computes Herfindahl-Hirschman Index for markets
// HHI = Σ(market_share²) where market_share = value_in_market / total_value
func (fo *FrontierOptimizer) calculateMarketHHI(config *PortfolioConfiguration) float64 {
	marketShares := make(map[string]float64)
	totalValue := 0.0

	// Calculate total portfolio value
	for i, prop := range config.Properties {
		value := float64(prop.Price) * config.Weights[i]
		totalValue += value
	}

	// Calculate market shares
	for i, prop := range config.Properties {
		market := prop.City + ", " + prop.State
		value := float64(prop.Price) * config.Weights[i]
		marketShares[market] += value / totalValue
	}

	// Calculate HHI
	hhi := 0.0
	for _, share := range marketShares {
		hhi += share * share
	}

	return hhi
}

// calculateSpatialConcentration measures geographic clustering
// Uses pairwise distance weighted by property values
func (fo *FrontierOptimizer) calculateSpatialConcentration(config *PortfolioConfiguration) float64 {
	if len(config.Properties) < 2 {
		return 0.0
	}

	// Calculate average pairwise distance (weighted)
	totalWeightedDistance := 0.0
	totalWeight := 0.0

	for i := 0; i < len(config.Properties); i++ {
		for j := i + 1; j < len(config.Properties); j++ {
			// Calculate distance between properties
			distance := fo.calculateDistance(
				config.Properties[i].Latitude,
				config.Properties[i].Longitude,
				config.Properties[j].Latitude,
				config.Properties[j].Longitude,
			)

			// Weight by product of property weights
			weight := config.Weights[i] * config.Weights[j]
			totalWeightedDistance += distance * weight
			totalWeight += weight
		}
	}

	avgDistance := totalWeightedDistance / totalWeight

	// Convert to concentration metric (inverse of distance)
	// Normalize: 0 km = 1.0 concentration, 500+ km = 0.0 concentration
	maxDistance := 500.0 // km
	concentration := 1.0 - math.Min(avgDistance/maxDistance, 1.0)

	return concentration
}

// calculateDistance computes Haversine distance between two coordinates
func (fo *FrontierOptimizer) calculateDistance(lat1, lon1, lat2, lon2 float64) float64 {
	// Haversine formula
	const earthRadius = 6371.0 // km

	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
		math.Sin(dLon/2)*math.Sin(dLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadius * c
}

// calculateTypeHHI computes Herfindahl-Hirschman Index for property types
func (fo *FrontierOptimizer) calculateTypeHHI(config *PortfolioConfiguration) float64 {
	typeShares := make(map[string]float64)
	totalValue := 0.0

	// Calculate total portfolio value
	for i, prop := range config.Properties {
		value := float64(prop.Price) * config.Weights[i]
		totalValue += value
	}

	// Calculate type shares
	for i, prop := range config.Properties {
		propType := prop.PropertyType
		if propType == "" {
			propType = "Single Family" // Default
		}
		value := float64(prop.Price) * config.Weights[i]
		typeShares[propType] += value / totalValue
	}

	// Calculate HHI
	hhi := 0.0
	for _, share := range typeShares {
		hhi += share * share
	}

	return hhi
}

// calculateStressTestEquity estimates final equity under stress scenario
func (fo *FrontierOptimizer) calculateStressTestEquity(
	config *PortfolioConfiguration,
	profile investment.InvestorProfile,
	params investment.InvestmentPlanningParams,
) int {
	totalEquity := 0
	for i, prop := range config.Properties {
		downPaymentPct := params.DownPaymentPct
		if downPaymentPct <= 0 || downPaymentPct > 1 {
			downPaymentPct = 0.25 // fallback
		}
		downPayment := int(float64(prop.Price) * downPaymentPct)
		loanAmount := prop.Price - downPayment

		// Stress scenario: 20% value drop, 10% rent drop, held for 10 years
		stressedValue := int(float64(prop.Price) * 0.80)
		stressedRent := int(float64(prop.EstimatedRent) * 0.90)

		mortgageRate := params.MortgageRate * 100 // params stores as decimal (0.07), function expects percent (7.0)
		if mortgageRate <= 0 || mortgageRate > 20 {
			mortgageRate = 7.0 // fallback
		}
		monthlyPayment := fo.calculateMortgagePayment(loanAmount, mortgageRate, 30)

		// Age-adjusted expense estimate: ~1.2% of property value annually (industry standard)
		// Divided by 12 for monthly. Property age adds ~0.02% per year above 10 years.
		propAge := 0
		if prop.YearBuilt > 0 {
			propAge = time.Now().Year() - prop.YearBuilt
		}
		expenseRatePct := 1.2 + math.Max(0, float64(propAge-10))*0.02
		monthlyExpenses := int(float64(prop.Price) * expenseRatePct / 100 / 12)
		if monthlyExpenses < 200 {
			monthlyExpenses = 200
		}
		stressedCashFlow := stressedRent - monthlyPayment - monthlyExpenses

		// Equity after 10 years
		yearsHeld := 10
		principalPaid := fo.calculatePrincipalPaid(loanAmount, mortgageRate, 30, yearsHeld)
		remainingLoan := loanAmount - principalPaid

		// Cumulative cash flow (if positive)
		cumulativeCash := 0
		if stressedCashFlow > 0 {
			cumulativeCash = stressedCashFlow * 12 * yearsHeld
		}

		propertyEquity := stressedValue - remainingLoan + cumulativeCash

		// Weight by portfolio allocation
		totalEquity += int(float64(propertyEquity) * config.Weights[i])
	}

	return totalEquity
}

// calculateMortgagePayment calculates monthly mortgage payment
func (fo *FrontierOptimizer) calculateMortgagePayment(principal int, annualRate float64, years int) int {
	if principal == 0 {
		return 0
	}

	monthlyRate := annualRate / 100.0 / 12.0
	numPayments := years * 12

	if monthlyRate == 0 {
		return principal / numPayments
	}

	payment := float64(principal) * monthlyRate * math.Pow(1+monthlyRate, float64(numPayments)) /
		(math.Pow(1+monthlyRate, float64(numPayments)) - 1)

	return int(payment)
}

// calculatePrincipalPaid calculates principal paid after specified years
func (fo *FrontierOptimizer) calculatePrincipalPaid(principal int, annualRate float64, years int, yearsHeld int) int {
	monthlyRate := annualRate / 100.0 / 12.0
	monthsPaid := yearsHeld * 12
	totalMonths := years * 12

	if monthlyRate == 0 {
		return (principal * monthsPaid) / totalMonths
	}

	monthlyPayment := float64(principal) * monthlyRate * math.Pow(1+monthlyRate, float64(totalMonths)) /
		(math.Pow(1+monthlyRate, float64(totalMonths)) - 1)

	// Calculate remaining balance after yearsHeld
	remainingBalance := float64(principal)*math.Pow(1+monthlyRate, float64(monthsPaid)) -
		monthlyPayment*(math.Pow(1+monthlyRate, float64(monthsPaid))-1)/monthlyRate

	principalPaid := float64(principal) - remainingBalance

	return int(principalPaid)
}

// findNonDominatedSolutions applies Pareto dominance filtering
func (fo *FrontierOptimizer) findNonDominatedSolutions(candidates []PortfolioConfiguration) []PortfolioConfiguration {
	// NSGA-II fast non-dominated sorting
	nonDominated := []PortfolioConfiguration{}

	for i := range candidates {
		isDominated := false

		for j := range candidates {
			if i == j {
				continue
			}

			// Check if candidate j dominates candidate i
			if fo.dominates(&candidates[j], &candidates[i]) {
				isDominated = true
				candidates[i].DominatedBy++
				break
			}

			// Count how many candidates i dominates
			if fo.dominates(&candidates[i], &candidates[j]) {
				candidates[i].DominationCount++
			}
		}

		if !isDominated {
			nonDominated = append(nonDominated, candidates[i])
		}
	}

	return nonDominated
}

// dominates checks if solution a Pareto-dominates solution b
// a dominates b if a is better or equal on all objectives, and strictly better on at least one
func (fo *FrontierOptimizer) dominates(a, b *PortfolioConfiguration) bool {
	// Objective 1: Expected Return (maximize)
	returnBetter := a.Objectives.ExpectedReturn >= b.Objectives.ExpectedReturn
	returnStrictlyBetter := a.Objectives.ExpectedReturn > b.Objectives.ExpectedReturn

	// Objective 2: Portfolio Volatility (minimize)
	volBetter := a.Objectives.PortfolioVolatility <= b.Objectives.PortfolioVolatility
	volStrictlyBetter := a.Objectives.PortfolioVolatility < b.Objectives.PortfolioVolatility

	// Objective 3: Concentration Index (minimize)
	concBetter := a.Objectives.ConcentrationIndex <= b.Objectives.ConcentrationIndex
	concStrictlyBetter := a.Objectives.ConcentrationIndex < b.Objectives.ConcentrationIndex

	// Objective 4: Stress Test Equity (maximize)
	stressBetter := a.Objectives.StressTestEquity >= b.Objectives.StressTestEquity
	stressStrictlyBetter := a.Objectives.StressTestEquity > b.Objectives.StressTestEquity

	// a dominates b if all objectives are better or equal, and at least one is strictly better
	allBetterOrEqual := returnBetter && volBetter && concBetter && stressBetter
	atLeastOneStrictlyBetter := returnStrictlyBetter || volStrictlyBetter || concStrictlyBetter || stressStrictlyBetter

	return allBetterOrEqual && atLeastOneStrictlyBetter
}

// selectFrontierPoints selects up to N configurations by Sharpe ratio, preferring
// metrically distinct configurations. Near-duplicates (within 0.05 Sharpe and 0.05%
// return) are skipped in favour of the next-best distinct config. If there are fewer
// than N distinct configs, the remainder is backfilled with the best near-duplicates
// so the caller always gets as many points as the pool allows.
func (fo *FrontierOptimizer) selectFrontierPoints(
	nonDominated []PortfolioConfiguration,
	count int,
) []PortfolioConfiguration {
	// Sort by Sharpe ratio (descending)
	sort.Slice(nonDominated, func(i, j int) bool {
		return nonDominated[i].SharpeScore > nonDominated[j].SharpeScore
	})

	const epsilon = 0.05 // configs within 0.05 Sharpe AND 0.05% return are near-identical

	// Pass 1: collect metrically distinct configurations.
	selected := make([]PortfolioConfiguration, 0, count)
	for _, c := range nonDominated {
		if len(selected) >= count {
			break
		}
		duplicate := false
		for _, s := range selected {
			if math.Abs(c.SharpeScore-s.SharpeScore) < epsilon &&
				math.Abs(c.Objectives.ExpectedReturn-s.Objectives.ExpectedReturn) < epsilon {
				duplicate = true
				break
			}
		}
		if !duplicate {
			selected = append(selected, c)
		}
	}

	// Pass 2: if deduplication left us short, backfill with best remaining
	// (even if metrically similar) so callers always receive a full set.
	if len(selected) < count {
		for _, c := range nonDominated {
			if len(selected) >= count {
				break
			}
			alreadyIn := false
			for _, s := range selected {
				if s.SharpeScore == c.SharpeScore &&
					s.Objectives.ExpectedReturn == c.Objectives.ExpectedReturn &&
					s.Objectives.PortfolioVolatility == c.Objectives.PortfolioVolatility {
					alreadyIn = true
					break
				}
			}
			if !alreadyIn {
				selected = append(selected, c)
			}
		}
	}

	return selected
}

// avgReturn calculates average expected return across frontier points
func (fo *FrontierOptimizer) avgReturn(points []investment.FrontierPoint) float64 {
	if len(points) == 0 {
		return 0.0
	}
	sum := 0.0
	for _, p := range points {
		sum += p.ExpectedReturn
	}
	return sum / float64(len(points))
}

// summarizeExistingPortfolio computes a summary of the user's existing portfolio.
// ADR-089 Phase 3: used to attach portfolio context to each FrontierPoint.
func (fo *FrontierOptimizer) summarizeExistingPortfolio(ep *investment.ExistingPortfolio) *investment.ExistingPortfolioSummary {
	if ep == nil || len(ep.Properties) == 0 {
		return nil
	}

	summary := &investment.ExistingPortfolioSummary{
		PropertyCount: len(ep.Properties),
		Locations:     make([]string, 0),
	}

	locationSet := make(map[string]bool)
	totalCapRate := 0.0
	capRateCount := 0

	for _, p := range ep.Properties {
		summary.TotalValue += p.CurrentValue
		summary.TotalEquity += p.Equity
		summary.TotalDebt += p.MortgageBalance
		summary.MonthlyCashFlow += p.MonthlyCashFlow

		location := p.City + ", " + p.State
		if !locationSet[location] {
			locationSet[location] = true
			summary.Locations = append(summary.Locations, location)
		}
		if p.CapRate > 0 {
			totalCapRate += p.CapRate
			capRateCount++
		}
	}

	summary.AnnualCashFlow = summary.MonthlyCashFlow * 12
	if capRateCount > 0 {
		summary.AvgCapRate = totalCapRate / float64(capRateCount)
	}

	return summary
}

// avgVolatility calculates average volatility across frontier points
func (fo *FrontierOptimizer) avgVolatility(points []investment.FrontierPoint) float64 {
	if len(points) == 0 {
		return 0.0
	}
	sum := 0.0
	for _, p := range points {
		sum += p.PortfolioVolatility
	}
	return sum / float64(len(points))
}

// computePairCorrelations fetches pairwise Pearson correlations between markets using
// the CorrelationAnalyzer. Returns a lookup map "market1|market2" -> correlation.
// computeCityVolatilities fetches annualised ZHVI volatility for each location.
// Returns a map "City, State" -> volatility (%). Falls back to heuristic per property
// when data unavailable.
func (fo *FrontierOptimizer) computeCityVolatilities(ctx context.Context, locations []string) map[string]float64 {
	if fo.correlationAnalyzer == nil || len(locations) == 0 {
		return make(map[string]float64)
	}
	return fo.correlationAnalyzer.ComputeMarketVolatilities(ctx, locations)
}

// Returns an empty map (falls back to hardcoded values) when analyzer unavailable.
func (fo *FrontierOptimizer) computePairCorrelations(ctx context.Context, locations []string) map[string]float64 {
	result := make(map[string]float64)
	if fo.correlationAnalyzer == nil || len(locations) < 2 {
		return result
	}
	cr := fo.correlationAnalyzer.CalculateCorrelations(ctx, locations)
	if cr == nil {
		return result
	}
	for _, mc := range cr.Correlations {
		// Store both orderings so lookup is order-independent.
		key1 := mc.Market1 + "|" + mc.Market2
		key2 := mc.Market2 + "|" + mc.Market1
		result[key1] = mc.Correlation
		result[key2] = mc.Correlation
	}
	return result
}
