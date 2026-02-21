package optimization

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/projection"
)

// FrontierOptimizer generates Pareto-optimal portfolio configurations
// ADR-088 Phase 3: Multi-objective optimization for efficient frontier
type FrontierOptimizer struct {
	logger            *slog.Logger
	markowitzCalc     *MarkowitzCalculator
	reinvestModeler   *projection.ReinvestmentModeler // ADR-088 Phase 5
	mcSimulator       *projection.MonteCarloSimulator // ADR-088 Phase 6
	minProperties     int                              // Minimum properties per configuration (default: 5)
	maxProperties     int                              // Maximum properties per configuration (default: 8)
	numConfigurations int                              // Number of frontier points to generate (default: 5)
	riskFreeRate      float64                          // Risk-free rate for Sharpe ratio (default: 4.0%)
}

// NewFrontierOptimizer creates a new frontier optimizer
func NewFrontierOptimizer(
	logger *slog.Logger,
	markowitzCalc *MarkowitzCalculator,
	reinvestModeler *projection.ReinvestmentModeler,
	mcSimulator *projection.MonteCarloSimulator,
) *FrontierOptimizer {
	return &FrontierOptimizer{
		logger:            logger,
		markowitzCalc:     markowitzCalc,
		reinvestModeler:   reinvestModeler,
		mcSimulator:       mcSimulator,
		minProperties:     5,
		maxProperties:     8,
		numConfigurations: 5,
		riskFreeRate:      4.0, // 4% default risk-free rate
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
}

// GenerateFrontier generates 3-5 Pareto-optimal portfolio configurations
// ADR-088 Phase 3: Multi-objective NSGA-II-inspired algorithm
func (fo *FrontierOptimizer) GenerateFrontier(
	ctx context.Context,
	properties []investment.ScoredProperty,
	profile investment.InvestorProfile,
	params investment.InvestmentPlanningParams,
) ([]investment.FrontierPoint, error) {
	fo.logger.Info("generating efficient frontier",
		"totalProperties", len(properties),
		"minPerConfig", fo.minProperties,
		"maxPerConfig", fo.maxProperties,
		"targetConfigs", fo.numConfigurations,
	)

	if len(properties) < fo.minProperties {
		return nil, fmt.Errorf("insufficient properties: need at least %d, got %d", fo.minProperties, len(properties))
	}

	// Step 1: Generate candidate configurations with varying sizes
	candidates := fo.generateCandidates(properties, profile)
	fo.logger.Info("generated candidate configurations", "count", len(candidates))

	// Step 2: Evaluate all objectives for each candidate
	for i := range candidates {
		fo.evaluateObjectives(&candidates[i], profile)
	}

	// Step 3: Apply Pareto dominance to find non-dominated solutions
	nonDominated := fo.findNonDominatedSolutions(candidates)
	fo.logger.Info("found non-dominated solutions", "count", len(nonDominated))

	// Step 4: Rank by Sharpe ratio and select top configurations
	frontierConfigs := fo.selectFrontierPoints(nonDominated, fo.numConfigurations)

	// Step 5: Convert to FrontierPoint format
	frontierPoints := make([]investment.FrontierPoint, 0, len(frontierConfigs))
	for i, config := range frontierConfigs {
		// TODO Phase 4: Add context-aware theses (Phase 2b re-scoring)
		// TODO Phase 5: Add reinvestment plan (Track A + Track B)
		// TODO Phase 6: Add simulation results (Monte Carlo)

		properties := make([]investment.PropertyInPortfolio, 0, len(config.Properties))
		for _, prop := range config.Properties {
			// Calculate financial metrics for this property
			downPayment := int(float64(prop.Price) * 0.25) // 25% down
			loanAmount := prop.Price - downPayment
			monthlyPayment := fo.calculateMortgagePayment(loanAmount, 7.0, 30) // 7% rate, 30 years
			monthlyCashFlow := prop.EstimatedRent - monthlyPayment - 500 // $500/mo expenses estimate
			capRate := float64(prop.EstimatedRent*12) / float64(prop.Price) * 100
			cashOnCash := float64(monthlyCashFlow*12) / float64(downPayment) * 100
			dscr := float64(prop.EstimatedRent) / float64(monthlyPayment)

			properties = append(properties, investment.PropertyInPortfolio{
				Property:        prop,
				DownPayment:     downPayment,
				LoanAmount:      loanAmount,
				MonthlyPayment:  monthlyPayment,
				MonthlyCashFlow: monthlyCashFlow,
				CapRate:         capRate,
				CashOnCash:      cashOnCash,
				DSCR:            dscr,
				Score:           0, // TODO Phase 4: Add context-aware score
			})
		}

		// ADR-088 Phase 6.2: Two-phase MC + Reinvestment pipeline
		// Phase 1: Create preliminary reinvestment plan (Track A + Track B threshold)
		preliminaryPlan, err := fo.reinvestModeler.CalculateReinvestmentPlan(ctx, &investment.FrontierPoint{
			ConfigIndex: i,
			Properties:  properties,
		}, params, nil) // nil mcResults = placeholder Track B values
		if err != nil {
			fo.logger.Warn("failed to calculate preliminary reinvestment plan, using nil",
				"configIndex", i,
				"error", err,
			)
			preliminaryPlan = nil
		}

		// Phase 2: Run Monte Carlo simulation with preliminary plan
		var mcResults *investment.SimulationResults
		if fo.mcSimulator != nil && preliminaryPlan != nil {
			mcResults, err = fo.mcSimulator.SimulateConfiguration(ctx, &investment.FrontierPoint{
				ConfigIndex: i,
				Properties:  properties,
			}, preliminaryPlan)
			if err != nil {
				fo.logger.Warn("Monte Carlo simulation failed, using nil results",
					"configIndex", i,
					"error", err,
				)
				mcResults = nil
			}
		}

		// Phase 3: Update reinvestment plan with MC-derived Track B statistics
		var reinvestPlan *investment.DualTrackReinvestment
		if mcResults != nil {
			reinvestPlan, err = fo.reinvestModeler.CalculateReinvestmentPlan(ctx, &investment.FrontierPoint{
				ConfigIndex: i,
				Properties:  properties,
			}, params, mcResults) // Pass MC results for Track B statistics
			if err != nil {
				fo.logger.Warn("failed to update reinvestment plan with MC results, using preliminary",
					"configIndex", i,
					"error", err,
				)
				reinvestPlan = preliminaryPlan
			}
		} else {
			// No MC results, use preliminary plan
			reinvestPlan = preliminaryPlan
		}

		frontierPoints = append(frontierPoints, investment.FrontierPoint{
			ConfigIndex:         i,
			Properties:          properties,
			ExpectedReturn:      config.Objectives.ExpectedReturn,
			PortfolioVolatility: config.Objectives.PortfolioVolatility,
			SharpeScore:         config.SharpeScore,
			ConcentrationIndex:  config.Objectives.ConcentrationIndex,
			StressTestEquity:    config.Objectives.StressTestEquity,
			SimulationResults:   mcResults,    // Phase 6: Monte Carlo simulation results
			ReinvestmentPlan:    reinvestPlan, // Phase 5+6: Dual-track with MC-updated Track B
		})
	}

	fo.logger.Info("frontier generation complete",
		"frontierPoints", len(frontierPoints),
		"avgReturn", fo.avgReturn(frontierPoints),
		"avgVolatility", fo.avgVolatility(frontierPoints),
	)

	return frontierPoints, nil
}

// generateCandidates creates candidate portfolios with varying property counts
func (fo *FrontierOptimizer) generateCandidates(
	properties []investment.ScoredProperty,
	profile investment.InvestorProfile,
) []PortfolioConfiguration {
	candidates := []PortfolioConfiguration{}

	// Generate configurations of varying sizes (5-8 properties)
	for size := fo.minProperties; size <= fo.maxProperties && size <= len(properties); size++ {
		// Create multiple configurations per size for diversity
		numPerSize := 3 // Generate 3 configurations per size
		for i := 0; i < numPerSize; i++ {
			config := fo.createConfiguration(properties, size, i, profile)
			candidates = append(candidates, config)
		}
	}

	return candidates
}

// createConfiguration creates a single portfolio configuration
func (fo *FrontierOptimizer) createConfiguration(
	properties []investment.ScoredProperty,
	size int,
	variant int,
	profile investment.InvestorProfile,
) PortfolioConfiguration {
	// Strategy for property selection:
	// Variant 0: Top-ranked properties (highest scores)
	// Variant 1: Diversified selection (spread across markets)
	// Variant 2: Risk-balanced selection (mix of volatilities)

	selectedProps := make([]investment.Property, 0, size)
	weights := make([]float64, 0, size)

	switch variant {
	case 0:
		// Top-ranked: Select highest-scoring properties
		sorted := make([]investment.ScoredProperty, len(properties))
		copy(sorted, properties)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].OverallScore > sorted[j].OverallScore
		})
		for i := 0; i < size && i < len(sorted); i++ {
			selectedProps = append(selectedProps, sorted[i].Property)
		}

	case 1:
		// Diversified: Spread across different markets
		marketMap := make(map[string][]investment.ScoredProperty)
		for _, p := range properties {
			key := p.Property.City + ", " + p.Property.State
			marketMap[key] = append(marketMap[key], p)
		}

		// Take one property from each market (up to size)
		count := 0
		for _, props := range marketMap {
			if count >= size {
				break
			}
			// Take highest-scoring property from this market
			if len(props) > 0 {
				best := props[0]
				for _, p := range props {
					if p.OverallScore > best.OverallScore {
						best = p
					}
				}
				selectedProps = append(selectedProps, best.Property)
				count++
			}
		}

		// Fill remaining slots with top-ranked properties if needed
		if count < size {
			sorted := make([]investment.ScoredProperty, len(properties))
			copy(sorted, properties)
			sort.Slice(sorted, func(i, j int) bool {
				return sorted[i].OverallScore > sorted[j].OverallScore
			})
			for i := 0; count < size && i < len(sorted); i++ {
				// Skip if already selected
				alreadySelected := false
				for _, sp := range selectedProps {
					if sp.ID == sorted[i].Property.ID {
						alreadySelected = true
						break
					}
				}
				if !alreadySelected {
					selectedProps = append(selectedProps, sorted[i].Property)
					count++
				}
			}
		}

	case 2:
		// Risk-balanced: Mix of high and low volatility
		// For Phase 3, use estimated volatility based on market type
		sorted := make([]investment.ScoredProperty, len(properties))
		copy(sorted, properties)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].OverallScore > sorted[j].OverallScore
		})

		// Take top properties, ensuring mix of different score ranges
		step := len(sorted) / size
		if step < 1 {
			step = 1
		}
		for i := 0; i < size && i*step < len(sorted); i++ {
			selectedProps = append(selectedProps, sorted[i*step].Property)
		}
	}

	// Calculate equal weights (simple approach for Phase 3)
	// Phase 4 will use optimized weights based on portfolio context
	totalValue := 0
	for _, p := range selectedProps {
		totalValue += p.Price
	}

	for _, p := range selectedProps {
		weight := float64(p.Price) / float64(totalValue)
		weights = append(weights, weight)
	}

	return PortfolioConfiguration{
		Properties: selectedProps,
		Weights:    weights,
	}
}

// evaluateObjectives calculates all 4 objectives for a configuration
func (fo *FrontierOptimizer) evaluateObjectives(
	config *PortfolioConfiguration,
	profile investment.InvestorProfile,
) {
	// Objective 1: Expected Return (maximize)
	config.Objectives.ExpectedReturn = fo.calculateExpectedReturn(config)

	// Objective 2: Portfolio Volatility via Markowitz (minimize)
	config.Objectives.PortfolioVolatility = fo.calculatePortfolioVolatility(config)

	// Objective 3: Concentration Index (minimize)
	config.Objectives.ConcentrationIndex = fo.calculateConcentrationIndex(config)

	// Objective 4: Stress Test Equity (maximize)
	config.Objectives.StressTestEquity = fo.calculateStressTestEquity(config, profile)

	// Calculate Sharpe Ratio for ranking
	excessReturn := config.Objectives.ExpectedReturn - fo.riskFreeRate
	if config.Objectives.PortfolioVolatility > 0 {
		config.SharpeScore = excessReturn / config.Objectives.PortfolioVolatility
	} else {
		config.SharpeScore = 0
	}
}

// calculateExpectedReturn estimates annual expected return
func (fo *FrontierOptimizer) calculateExpectedReturn(config *PortfolioConfiguration) float64 {
	// Phase 3: Simple weighted average of cap rates + appreciation estimate
	// Phase 6: Will use Monte Carlo expected value

	totalReturn := 0.0
	for i, prop := range config.Properties {
		// Cap rate (rental yield)
		capRate := 0.0
		if prop.EstimatedRent > 0 && prop.Price > 0 {
			capRate = float64(prop.EstimatedRent*12) / float64(prop.Price) * 100
		}

		// Appreciation estimate (placeholder - Phase 6 will use market data)
		appreciation := 4.0 // 4% default appreciation

		totalReturn += (capRate + appreciation) * config.Weights[i]
	}

	return totalReturn
}

// calculatePortfolioVolatility uses Markowitz analytical formula
// σ² = Σᵢ Σⱼ wᵢ wⱼ σᵢ σⱼ ρᵢⱼ
func (fo *FrontierOptimizer) calculatePortfolioVolatility(config *PortfolioConfiguration) float64 {
	n := len(config.Properties)
	if n == 0 {
		return 0.0
	}

	// Get volatilities for each property
	volatilities := make([]float64, n)
	for i, prop := range config.Properties {
		// Phase 3: Placeholder volatility (Phase 6 will use actual market data)
		// Estimate based on property characteristics
		volatilities[i] = fo.estimatePropertyVolatility(prop)
	}

	// Build correlation matrix (placeholder - Phase 6 will use actual correlations)
	correlationMatrix := fo.buildPlaceholderCorrelationMatrix(n, config.Properties)

	// Use Markowitz calculator from Phase 0
	variance := fo.markowitzCalc.CalculatePortfolioVariance(config.Weights, volatilities, correlationMatrix)

	// Return standard deviation (volatility)
	return math.Sqrt(variance)
}

// estimatePropertyVolatility estimates volatility for a property
func (fo *FrontierOptimizer) estimatePropertyVolatility(prop investment.Property) float64 {
	// Phase 3: Simple heuristic based on price and market
	// Phase 6: Will use actual AppreciationStdDev from providers.Property

	// Base volatility: 5% for typical residential
	baseVol := 5.0

	// Adjust for price (higher price = potentially higher volatility)
	if prop.Price > 500000 {
		baseVol += 1.0
	}
	if prop.Price > 1000000 {
		baseVol += 1.0
	}

	// Adjust for location (major markets = higher volatility)
	majorMarkets := []string{"San Francisco", "New York", "Los Angeles", "Seattle", "Boston"}
	for _, market := range majorMarkets {
		if prop.City == market {
			baseVol += 2.0
			break
		}
	}

	return baseVol
}

// buildPlaceholderCorrelationMatrix creates a correlation matrix
func (fo *FrontierOptimizer) buildPlaceholderCorrelationMatrix(n int, properties []investment.Property) [][]float64 {
	// Phase 3: Placeholder correlation matrix
	// Phase 6: Will use actual correlation service from Phase 1

	matrix := make([][]float64, n)
	for i := 0; i < n; i++ {
		matrix[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			if i == j {
				matrix[i][j] = 1.0 // Self-correlation = 1
			} else {
				// Same market = higher correlation
				if properties[i].City == properties[j].City {
					matrix[i][j] = 0.7 // High correlation within same market
				} else {
					matrix[i][j] = 0.3 // Low correlation across markets
				}
			}
		}
	}

	return matrix
}

// calculateConcentrationIndex computes weighted concentration metric
// Combines HHI (market, submarket, type) + spatial concentration
func (fo *FrontierOptimizer) calculateConcentrationIndex(config *PortfolioConfiguration) float64 {
	// HHI (Herfindahl-Hirschman Index) for market concentration
	marketHHI := fo.calculateMarketHHI(config)

	// Spatial concentration (geographic clustering)
	spatialConc := fo.calculateSpatialConcentration(config)

	// Property type concentration
	typeHHI := fo.calculateTypeHHI(config)

	// Weighted average: 50% market HHI, 30% spatial, 20% type
	concentrationIndex := 0.5*marketHHI + 0.3*spatialConc + 0.2*typeHHI

	return concentrationIndex
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
) int {
	// Phase 3: Simple stress test calculation
	// Phase 6: Will use Monte Carlo stress paths

	totalEquity := 0
	for i, prop := range config.Properties {
		// Assume 25% down payment
		downPayment := int(float64(prop.Price) * 0.25)
		loanAmount := prop.Price - downPayment

		// Stress scenario: 20% value drop, 10% rent drop, held for 10 years
		stressedValue := int(float64(prop.Price) * 0.80)
		stressedRent := int(float64(prop.EstimatedRent) * 0.90)

		// Mortgage payment (7% rate, 30 years)
		monthlyPayment := fo.calculateMortgagePayment(loanAmount, 7.0, 30)

		// Cash flow under stress
		monthlyExpenses := 500 // Estimate
		stressedCashFlow := stressedRent - monthlyPayment - monthlyExpenses

		// Equity after 10 years
		yearsHeld := 10
		principalPaid := fo.calculatePrincipalPaid(loanAmount, 7.0, 30, yearsHeld)
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

// selectFrontierPoints selects top N configurations by Sharpe ratio
func (fo *FrontierOptimizer) selectFrontierPoints(
	nonDominated []PortfolioConfiguration,
	count int,
) []PortfolioConfiguration {
	// Sort by Sharpe ratio (descending)
	sort.Slice(nonDominated, func(i, j int) bool {
		return nonDominated[i].SharpeScore > nonDominated[j].SharpeScore
	})

	// Take top N
	if len(nonDominated) > count {
		return nonDominated[:count]
	}

	return nonDominated
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
