package projection

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/estara-ai/www/internal/services/investment"
	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/stat/distuv"
)

// MonteCarloSimulator runs probabilistic projections across 1,000 paths
// ADR-088 Phase 6: Monte Carlo engine with correlated market shocks
type MonteCarloSimulator struct {
	logger         *slog.Logger
	numPaths       int     // Default: 1,000 paths
	projectionYears int     // Default: 10 years
	rng            *rand.Rand
}

// NewMonteCarloSimulator creates a new Monte Carlo simulator
func NewMonteCarloSimulator(logger *slog.Logger) *MonteCarloSimulator {
	// Use time-based seed for random variation across runs
	seed := time.Now().UnixNano()

	return &MonteCarloSimulator{
		logger:         logger,
		numPaths:       1000, // 1,000 paths as per ADR-088
		projectionYears: 10,   // 10-year projection horizon
		rng:            rand.New(rand.NewSource(seed)),
	}
}

// SimulateConfiguration runs Monte Carlo simulation for a frontier configuration
// Returns SimulationResults with 1,000 paths and percentile distributions
func (mcs *MonteCarloSimulator) SimulateConfiguration(
	ctx context.Context,
	config *investment.FrontierPoint,
	reinvestmentPlan *investment.DualTrackReinvestment,
) (*investment.SimulationResults, error) {
	mcs.logger.Info("starting Monte Carlo simulation",
		"configIndex", config.ConfigIndex,
		"numPaths", mcs.numPaths,
		"projectionYears", mcs.projectionYears,
	)

	// Step 1: Build correlation matrix for correlated shocks
	correlationMatrix := mcs.buildCorrelationMatrix(config.Properties)

	// Step 2: Generate Cholesky decomposition for correlated sampling
	cholesky, err := mcs.choleskyDecomposition(correlationMatrix)
	if err != nil {
		return nil, fmt.Errorf("Cholesky decomposition failed: %w", err)
	}

	// Step 3: Simulate 1,000 paths
	paths := make([]investment.MonteCarloPath, mcs.numPaths)
	for i := 0; i < mcs.numPaths; i++ {
		path, err := mcs.simulatePath(ctx, i, config, reinvestmentPlan, cholesky)
		if err != nil {
			return nil, fmt.Errorf("path %d simulation failed: %w", i, err)
		}
		paths[i] = path
	}

	// Step 4: Aggregate to percentile distributions
	percentiles := mcs.calculatePercentiles(paths)

	// Step 5: Extract base projection (P50 median)
	baseProjection := percentiles.P50

	results := &investment.SimulationResults{
		Paths:          paths,
		Percentiles:    percentiles,
		BaseProjection: baseProjection,
		UpsideCase:     nil, // Phase 7: Upside scenario
		StressTest:     nil, // Phase 7: Stress test scenario
		MonteCarloSeed: mcs.rng.Int63(),
	}

	mcs.logger.Info("Monte Carlo simulation complete",
		"configIndex", config.ConfigIndex,
		"pathsGenerated", len(paths),
		"medianFinalNetWorth", baseProjection[len(baseProjection)-1].NetWorth,
	)

	return results, nil
}

// simulatePath generates a single Monte Carlo path
func (mcs *MonteCarloSimulator) simulatePath(
	ctx context.Context,
	pathIndex int,
	config *investment.FrontierPoint,
	reinvestmentPlan *investment.DualTrackReinvestment,
	cholesky *mat.Cholesky,
) (investment.MonteCarloPath, error) {
	// Initialize path state for this simulation
	pathState := NewPathState(pathIndex, mcs.projectionYears)

	yearlyOutcomes := make([]investment.YearOutcome, mcs.projectionYears)

	// Initialize portfolio state from new frontier properties
	portfolioValue := 0
	totalLoanBalance := 0
	for _, prop := range config.Properties {
		portfolioValue += prop.Property.Price
		totalLoanBalance += prop.LoanAmount
	}

	// Add existing portfolio as starting baseline (ADR-089: combined portfolio projections)
	// Existing equity and ongoing cash flows are included so projections show the full picture.
	existingPortfolioValue := 0   // appreciates each year alongside new properties
	existingAnnualCashFlow := 0   // existing net cash flow added to each year's cash flow
	if config.ExistingPortfolio != nil {
		existingPortfolioValue = int(config.ExistingPortfolio.TotalValue)
		existingLoanBalance := int(config.ExistingPortfolio.TotalDebt)
		existingAnnualCashFlow = int(config.ExistingPortfolio.AnnualCashFlow)
		portfolioValue += existingPortfolioValue
		totalLoanBalance += existingLoanBalance
	}

	cumulativeCashFlow := 0
	trackBFiredYear := 0

	// Simulate each year
	for year := 1; year <= mcs.projectionYears; year++ {
		// Generate correlated market shocks for this year
		shocks := mcs.generateCorrelatedShocks(cholesky, len(config.Properties))

		// Apply market shocks to new property values
		yearAppreciation := 0
		for i, prop := range config.Properties {
			// Log-normal appreciation shock
			appreciationRate := mcs.sampleLogNormal(
				0.04,  // μ = 4% baseline appreciation
				shocks[i*2], // Use correlated shock
			)
			propAppreciation := int(float64(prop.Property.Price) * appreciationRate)
			yearAppreciation += propAppreciation
		}
		// Apply average appreciation shock to existing portfolio (same market-level movement)
		if existingPortfolioValue > 0 && len(shocks) > 0 {
			avgShock := 0.0
			for si := 0; si < len(shocks); si += 2 {
				avgShock += shocks[si]
			}
			avgShock /= float64(len(shocks) / 2)
			existingAppreciationRate := mcs.sampleLogNormal(0.04, avgShock)
			yearAppreciation += int(float64(existingPortfolioValue) * existingAppreciationRate)
			existingPortfolioValue = int(float64(existingPortfolioValue) * (1 + existingAppreciationRate))
		}
		portfolioValue += yearAppreciation

		// Apply rent growth shocks
		yearRent := 0
		for i, prop := range config.Properties {
			rentGrowthRate := mcs.sampleLogNormal(
				0.03,  // μ = 3% baseline rent growth
				shocks[i*2+1], // Use correlated shock (rent follows appreciation)
			)
			currentRent := int(float64(prop.Property.EstimatedRent) * math.Pow(1.03, float64(year-1)))
			yearRent += int(float64(currentRent) * (1 + rentGrowthRate)) * 12
		}

		// Calculate expenses (normal distribution)
		baseExpenses := int(float64(portfolioValue) * 0.01) // 1% of value per year
		expenseGrowth := mcs.sampleNormal(0.025, 0.008) // μ=2.5%, σ=0.8%
		yearExpenses := int(float64(baseExpenses) * (1 + expenseGrowth))

		// Calculate mortgage payments
		yearMortgagePayment := 0
		principalPaid := 0
		for _, prop := range config.Properties {
			yearMortgagePayment += prop.MonthlyPayment * 12
			// Simplified amortization: ~30% principal in later years
			principalPaid += int(float64(prop.MonthlyPayment) * 12 * 0.3)
		}
		totalLoanBalance -= principalPaid

		// Net cash flow for the year (new properties + existing portfolio baseline)
		yearCashFlow := yearRent - yearExpenses - yearMortgagePayment + existingAnnualCashFlow
		cumulativeCashFlow += yearCashFlow

		// Track cumulative cash for Track B threshold detection
		pathState.CumulativeCash[year-1] = cumulativeCashFlow

		// Check Track B threshold
		if trackBFiredYear == 0 && reinvestmentPlan != nil {
			fired, firedYear := pathState.CheckTrackBThreshold(reinvestmentPlan.TrackB.Threshold)
			if fired {
				trackBFiredYear = firedYear
				mcs.logger.Debug("Track B fired",
					"pathIndex", pathIndex,
					"year", firedYear,
					"cumulativeCash", cumulativeCashFlow,
				)
			}
		}

		// Calculate equity
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

	return investment.MonteCarloPath{
		PathIndex:       pathIndex,
		YearlyOutcomes:  yearlyOutcomes,
		TrackBFiredYear: trackBFiredYear,
		FinalNetWorth:   yearlyOutcomes[mcs.projectionYears-1].NetWorth,
	}, nil
}

// buildCorrelationMatrix creates 2N×2N correlation matrix (appreciation + rent per market)
func (mcs *MonteCarloSimulator) buildCorrelationMatrix(properties []investment.PropertyInPortfolio) *mat.SymDense {
	n := len(properties)
	size := n * 2 // 2N: appreciation and rent for each property

	// Build market map
	marketMap := make(map[string]int)
	marketIndex := 0
	for _, prop := range properties {
		market := prop.Property.City + ", " + prop.Property.State
		if _, exists := marketMap[market]; !exists {
			marketMap[market] = marketIndex
			marketIndex++
		}
	}

	// Initialize correlation matrix
	data := make([]float64, size*size)
	corr := mat.NewSymDense(size, data)

	// Fill correlation matrix
	for i := 0; i < n; i++ {
		marketI := marketMap[properties[i].Property.City+", "+properties[i].Property.State]

		for j := 0; j < n; j++ {
			marketJ := marketMap[properties[j].Property.City+", "+properties[j].Property.State]

			// Appreciation-appreciation correlation
			appreciationCorr := 0.3 // Default: different markets
			if marketI == marketJ {
				appreciationCorr = 0.7 // Same market: higher correlation
			}
			if i == j {
				appreciationCorr = 1.0 // Same property: perfect correlation
			}
			corr.SetSym(i*2, j*2, appreciationCorr)

			// Rent-rent correlation (follows appreciation)
			rentCorr := appreciationCorr * 0.8 // Rent slightly less correlated
			if i == j {
				rentCorr = 1.0 // Diagonal must be 1.0
			}
			corr.SetSym(i*2+1, j*2+1, rentCorr)

			// Appreciation-rent cross-correlation (same property)
			if i == j {
				corr.SetSym(i*2, j*2+1, 0.6) // Appreciation and rent moderately correlated
			}
		}
	}

	return corr
}

// choleskyDecomposition computes Cholesky decomposition with PSD regularization
func (mcs *MonteCarloSimulator) choleskyDecomposition(corr *mat.SymDense) (*mat.Cholesky, error) {
	var chol mat.Cholesky
	ok := chol.Factorize(corr)

	if !ok {
		// Matrix not positive semi-definite, apply regularization
		mcs.logger.Warn("correlation matrix not PSD, applying regularization")

		// Add regularization to diagonal to ensure PSD
		n := corr.SymmetricDim()
		for i := 0; i < n; i++ {
			val := corr.At(i, i)
			corr.SetSym(i, i, val+0.25) // Epsilon large enough to fix negative eigenvalues
		}

		// Retry Cholesky with new object
		var chol2 mat.Cholesky
		ok = chol2.Factorize(corr)
		if !ok {
			return nil, fmt.Errorf("Cholesky decomposition failed after regularization")
		}
		return &chol2, nil
	}

	return &chol, nil
}

// generateCorrelatedShocks generates correlated standard normal shocks
func (mcs *MonteCarloSimulator) generateCorrelatedShocks(cholesky *mat.Cholesky, numProperties int) []float64 {
	size := numProperties * 2

	// Generate independent standard normal samples
	independent := make([]float64, size)
	for i := 0; i < size; i++ {
		independent[i] = mcs.rng.NormFloat64()
	}

	// Transform to correlated shocks using Cholesky: correlated = L × independent
	independentVec := mat.NewVecDense(size, independent)
	var L mat.TriDense
	cholesky.LTo(&L)
	var correlatedVec mat.VecDense
	correlatedVec.MulVec(&L, independentVec)

	// Extract correlated shocks
	correlated := make([]float64, size)
	for i := 0; i < size; i++ {
		correlated[i] = correlatedVec.AtVec(i)
	}

	return correlated
}

// sampleLogNormal samples from log-normal distribution
// μ is the mean of the underlying normal (not the log-normal mean)
// shock is a standard normal random variable
func (mcs *MonteCarloSimulator) sampleLogNormal(mu float64, shock float64) float64 {
	// Log-normal: exp(μ + σ × Z) where Z ~ N(0,1)
	sigma := 0.05 // 5% volatility (placeholder, Phase 6 will use AppreciationStdDev)
	return math.Exp(mu + sigma*shock) - 1.0 // Return as rate (subtract 1)
}

// sampleNormal samples from normal distribution
func (mcs *MonteCarloSimulator) sampleNormal(mu, sigma float64) float64 {
	return mu + sigma*mcs.rng.NormFloat64()
}

// sampleBeta samples from beta distribution using method of moments
// Used for vacancy rate simulation
func (mcs *MonteCarloSimulator) sampleBeta(mean, variance float64) float64 {
	// Method of moments: convert (mean, variance) to (α, β) parameters
	// α = mean × ((mean × (1 - mean)) / variance - 1)
	// β = (1 - mean) × ((mean × (1 - mean)) / variance - 1)

	if variance >= mean*(1-mean) {
		// Variance too large, use uniform fallback
		return mean
	}

	alpha := mean * ((mean * (1 - mean)) / variance - 1)
	beta := (1 - mean) * ((mean * (1 - mean)) / variance - 1)

	// Sample from beta distribution
	betaDist := distuv.Beta{Alpha: alpha, Beta: beta, Src: mcs.rng}
	return betaDist.Rand()
}

// calculatePercentiles computes P10, P25, P50, P75, P90, P95 across all paths
func (mcs *MonteCarloSimulator) calculatePercentiles(paths []investment.MonteCarloPath) *investment.PercentileOutcomes {
	// Determine actual projection years from paths
	projectionYears := 0
	if len(paths) > 0 && len(paths[0].YearlyOutcomes) > 0 {
		projectionYears = len(paths[0].YearlyOutcomes)
	}

	percentiles := &investment.PercentileOutcomes{
		P10: make([]investment.YearOutcome, projectionYears),
		P25: make([]investment.YearOutcome, projectionYears),
		P50: make([]investment.YearOutcome, projectionYears),
		P75: make([]investment.YearOutcome, projectionYears),
		P90: make([]investment.YearOutcome, projectionYears),
		P95: make([]investment.YearOutcome, projectionYears),
	}

	// Calculate percentiles for each year
	for year := 0; year < projectionYears; year++ {
		// Extract values for this year across all paths
		portfolioValues := make([]int, len(paths))
		equities := make([]int, len(paths))
		cashFlows := make([]int, len(paths))
		netWorths := make([]int, len(paths))

		for i, path := range paths {
			portfolioValues[i] = path.YearlyOutcomes[year].PortfolioValue
			equities[i] = path.YearlyOutcomes[year].Equity
			cashFlows[i] = path.YearlyOutcomes[year].CumulativeCashFlow
			netWorths[i] = path.YearlyOutcomes[year].NetWorth
		}

		// Calculate percentiles
		percentiles.P10[year] = investment.YearOutcome{
			Year:               year + 1,
			PortfolioValue:     mcs.percentile(portfolioValues, 10),
			Equity:             mcs.percentile(equities, 10),
			CumulativeCashFlow: mcs.percentile(cashFlows, 10),
			NetWorth:           mcs.percentile(netWorths, 10),
		}

		percentiles.P25[year] = investment.YearOutcome{
			Year:               year + 1,
			PortfolioValue:     mcs.percentile(portfolioValues, 25),
			Equity:             mcs.percentile(equities, 25),
			CumulativeCashFlow: mcs.percentile(cashFlows, 25),
			NetWorth:           mcs.percentile(netWorths, 25),
		}

		percentiles.P50[year] = investment.YearOutcome{
			Year:               year + 1,
			PortfolioValue:     mcs.percentile(portfolioValues, 50),
			Equity:             mcs.percentile(equities, 50),
			CumulativeCashFlow: mcs.percentile(cashFlows, 50),
			NetWorth:           mcs.percentile(netWorths, 50),
		}

		percentiles.P75[year] = investment.YearOutcome{
			Year:               year + 1,
			PortfolioValue:     mcs.percentile(portfolioValues, 75),
			Equity:             mcs.percentile(equities, 75),
			CumulativeCashFlow: mcs.percentile(cashFlows, 75),
			NetWorth:           mcs.percentile(netWorths, 75),
		}

		percentiles.P90[year] = investment.YearOutcome{
			Year:               year + 1,
			PortfolioValue:     mcs.percentile(portfolioValues, 90),
			Equity:             mcs.percentile(equities, 90),
			CumulativeCashFlow: mcs.percentile(cashFlows, 90),
			NetWorth:           mcs.percentile(netWorths, 90),
		}

		percentiles.P95[year] = investment.YearOutcome{
			Year:               year + 1,
			PortfolioValue:     mcs.percentile(portfolioValues, 95),
			Equity:             mcs.percentile(equities, 95),
			CumulativeCashFlow: mcs.percentile(cashFlows, 95),
			NetWorth:           mcs.percentile(netWorths, 95),
		}
	}

	return percentiles
}

// percentile calculates the nth percentile of a slice of integers
func (mcs *MonteCarloSimulator) percentile(values []int, p int) int {
	if len(values) == 0 {
		return 0
	}

	// Sort values
	sorted := make([]int, len(values))
	copy(sorted, values)
	sort.Ints(sorted)

	// Calculate percentile with linear interpolation
	rank := float64(p) / 100.0 * float64(len(sorted)-1)
	lower := int(rank)
	upper := lower + 1
	fraction := rank - float64(lower)

	// Bounds checking
	if lower < 0 {
		lower = 0
	}
	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}

	// Interpolate between lower and upper values
	if fraction == 0.0 {
		return sorted[lower]
	}
	return int(float64(sorted[lower])*(1.0-fraction) + float64(sorted[upper])*fraction)
}
