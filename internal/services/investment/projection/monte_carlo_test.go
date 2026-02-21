package projection

import (
	"context"
	"log/slog"
	"math"
	"os"
	"testing"

	"github.com/estara-ai/www/internal/services/investment"
	"gonum.org/v1/gonum/mat"
)

func TestNewMonteCarloSimulator(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	if mcs.numPaths != 1000 {
		t.Errorf("numPaths = %d, want 1000", mcs.numPaths)
	}

	if mcs.projectionYears != 10 {
		t.Errorf("projectionYears = %d, want 10", mcs.projectionYears)
	}

	if mcs.rng == nil {
		t.Error("rng should not be nil")
	}
}

func TestBuildCorrelationMatrix(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	properties := []investment.PropertyInPortfolio{
		{Property: investment.Property{City: "Austin", State: "TX"}},
		{Property: investment.Property{City: "Austin", State: "TX"}},
		{Property: investment.Property{City: "Denver", State: "CO"}},
	}

	corr := mcs.buildCorrelationMatrix(properties)

	// Verify matrix size (2N × 2N)
	expectedSize := len(properties) * 2
	rows, cols := corr.Dims()
	if rows != expectedSize || cols != expectedSize {
		t.Errorf("Matrix dims = %d×%d, want %d×%d", rows, cols, expectedSize, expectedSize)
	}

	// Verify diagonal is 1.0 (perfect self-correlation)
	for i := 0; i < expectedSize; i++ {
		diag := corr.At(i, i)
		if math.Abs(diag-1.0) > 1e-10 {
			t.Errorf("Diagonal[%d] = %f, want 1.0", i, diag)
		}
	}

	// Verify same-market correlation (Austin prop 0 and 1, appreciation)
	sameMarketCorr := corr.At(0, 2) // prop0 appreciation vs prop1 appreciation
	if sameMarketCorr != 0.7 {
		t.Errorf("Same-market correlation = %f, want 0.7", sameMarketCorr)
	}

	// Verify different-market correlation (Austin vs Denver)
	diffMarketCorr := corr.At(0, 4) // prop0 appreciation vs prop2 appreciation
	if diffMarketCorr != 0.3 {
		t.Errorf("Different-market correlation = %f, want 0.3", diffMarketCorr)
	}
}

func TestCholeskyDecomposition(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	// Create a simple positive definite matrix
	data := []float64{
		1.0, 0.5,
		0.5, 1.0,
	}
	corr := mat.NewSymDense(2, data)

	cholesky, err := mcs.choleskyDecomposition(corr)

	if err != nil {
		t.Fatalf("choleskyDecomposition() error = %v", err)
	}

	if cholesky == nil {
		t.Fatal("cholesky should not be nil")
	}

	// Verify Cholesky property: L × L^T = A
	var L mat.TriDense
	cholesky.LTo(&L)

	var reconstructed mat.Dense
	reconstructed.Mul(&L, L.T())

	// Check reconstruction matches original
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			original := corr.At(i, j)
			recon := reconstructed.At(i, j)
			if math.Abs(original-recon) > 1e-10 {
				t.Errorf("Reconstructed[%d,%d] = %f, want %f", i, j, recon, original)
			}
		}
	}
}

func TestCholeskyDecomposition_Regularization(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	// Create a non-PSD matrix (negative eigenvalue)
	data := []float64{
		1.0, 0.9,
		0.9, 0.5, // This makes the matrix not PSD
	}
	corr := mat.NewSymDense(2, data)

	cholesky, err := mcs.choleskyDecomposition(corr)

	// Should succeed with regularization
	if err != nil {
		t.Fatalf("choleskyDecomposition() with regularization error = %v", err)
	}

	if cholesky == nil {
		t.Fatal("cholesky should not be nil after regularization")
	}
}

func TestGenerateCorrelatedShocks(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	// Create identity correlation matrix (uncorrelated)
	size := 4 // 2 properties × 2 (appreciation + rent)
	data := make([]float64, size*size)
	for i := 0; i < size; i++ {
		data[i*size+i] = 1.0
	}
	corr := mat.NewSymDense(size, data)

	cholesky, err := mcs.choleskyDecomposition(corr)
	if err != nil {
		t.Fatalf("Cholesky decomposition failed: %v", err)
	}

	// Generate shocks
	shocks := mcs.generateCorrelatedShocks(cholesky, 2)

	if len(shocks) != size {
		t.Errorf("Generated %d shocks, want %d", len(shocks), size)
	}

	// Shocks should be standard normal (roughly -3 to +3)
	for i, shock := range shocks {
		if math.Abs(shock) > 5.0 {
			t.Errorf("Shock[%d] = %f is unusually large (possible error)", i, shock)
		}
	}
}

func TestSampleLogNormal(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	// Sample multiple times to verify reasonable distribution
	samples := make([]float64, 1000)
	for i := 0; i < 1000; i++ {
		shock := mcs.rng.NormFloat64()
		samples[i] = mcs.sampleLogNormal(0.04, shock)
	}

	// Calculate mean (should be close to 4% for μ=0.04)
	mean := 0.0
	for _, s := range samples {
		mean += s
	}
	mean /= float64(len(samples))

	// Mean should be roughly 4% ± some variance
	if math.Abs(mean-0.04) > 0.02 {
		t.Errorf("Mean of log-normal samples = %f, expected ~0.04", mean)
	}
}

func TestSampleNormal(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	// Sample multiple times
	samples := make([]float64, 1000)
	mu := 0.025 // 2.5%
	sigma := 0.008 // 0.8%

	for i := 0; i < 1000; i++ {
		samples[i] = mcs.sampleNormal(mu, sigma)
	}

	// Calculate mean
	mean := 0.0
	for _, s := range samples {
		mean += s
	}
	mean /= float64(len(samples))

	// Mean should be close to μ
	if math.Abs(mean-mu) > 0.005 {
		t.Errorf("Mean of normal samples = %f, expected ~%f", mean, mu)
	}
}

func TestSampleBeta(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	mean := 0.05 // 5% vacancy
	variance := 0.001 // Small variance

	// Sample multiple times
	samples := make([]float64, 1000)
	for i := 0; i < 1000; i++ {
		samples[i] = mcs.sampleBeta(mean, variance)
	}

	// Calculate mean
	sampleMean := 0.0
	for _, s := range samples {
		sampleMean += s
	}
	sampleMean /= float64(len(samples))

	// Mean should be close to specified mean
	if math.Abs(sampleMean-mean) > 0.02 {
		t.Errorf("Mean of beta samples = %f, expected ~%f", sampleMean, mean)
	}

	// All samples should be in [0, 1]
	for i, s := range samples {
		if s < 0.0 || s > 1.0 {
			t.Errorf("Sample[%d] = %f is outside [0, 1]", i, s)
		}
	}
}

func TestPercentile(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	values := []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

	tests := []struct {
		p        int
		expected int
	}{
		{p: 10, expected: 19},  // Linear interpolation: 10 * 0.1 + 20 * 0.9
		{p: 25, expected: 32},  // Linear interpolation: 30 * 0.75 + 40 * 0.25
		{p: 50, expected: 55},  // Linear interpolation: 50 * 0.5 + 60 * 0.5
		{p: 75, expected: 77},  // Linear interpolation: 70 * 0.25 + 80 * 0.75
		{p: 90, expected: 91},  // Linear interpolation: 90 * 0.9 + 100 * 0.1
	}

	for _, tt := range tests {
		result := mcs.percentile(values, tt.p)
		if result != tt.expected {
			t.Errorf("percentile(%d) = %d, want %d", tt.p, result, tt.expected)
		}
	}
}

func TestCalculatePercentiles(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	// Create mock paths
	paths := []investment.MonteCarloPath{
		{
			PathIndex: 0,
			YearlyOutcomes: []investment.YearOutcome{
				{Year: 1, PortfolioValue: 100000, Equity: 50000, CumulativeCashFlow: 5000, NetWorth: 55000},
				{Year: 2, PortfolioValue: 110000, Equity: 60000, CumulativeCashFlow: 12000, NetWorth: 72000},
			},
		},
		{
			PathIndex: 1,
			YearlyOutcomes: []investment.YearOutcome{
				{Year: 1, PortfolioValue: 120000, Equity: 60000, CumulativeCashFlow: 6000, NetWorth: 66000},
				{Year: 2, PortfolioValue: 130000, Equity: 70000, CumulativeCashFlow: 14000, NetWorth: 84000},
			},
		},
	}

	percentiles := mcs.calculatePercentiles(paths)

	// Verify P50 (median) for year 1
	if percentiles.P50[0].Year != 1 {
		t.Errorf("P50 Year = %d, want 1", percentiles.P50[0].Year)
	}

	// P50 PortfolioValue should be median of [100000, 120000] = 110000
	expectedP50 := 110000
	if percentiles.P50[0].PortfolioValue != expectedP50 {
		t.Errorf("P50 PortfolioValue = %d, want %d", percentiles.P50[0].PortfolioValue, expectedP50)
	}
}

func TestSimulatePath_Integration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	// Create test configuration
	config := &investment.FrontierPoint{
		ConfigIndex: 0,
		Properties: []investment.PropertyInPortfolio{
			{
				Property: investment.Property{
					ID:            "p1",
					City:          "Austin",
					State:         "TX",
					Price:         400000,
					EstimatedRent: 2500,
				},
				LoanAmount:      300000,
				MonthlyPayment:  2000,
				MonthlyCashFlow: 500,
			},
		},
	}

	// Create correlation matrix and Cholesky
	corr := mcs.buildCorrelationMatrix(config.Properties)
	cholesky, err := mcs.choleskyDecomposition(corr)
	if err != nil {
		t.Fatalf("Cholesky decomposition failed: %v", err)
	}

	// Simulate a single path
	ctx := context.Background()
	reinvestmentPlan := &investment.DualTrackReinvestment{
		TrackB: investment.TrackBReinvestment{
			Threshold: 50000,
		},
	}

	path, err := mcs.simulatePath(ctx, 0, config, reinvestmentPlan, cholesky)

	if err != nil {
		t.Fatalf("simulatePath() error = %v", err)
	}

	// Verify path structure
	if path.PathIndex != 0 {
		t.Errorf("PathIndex = %d, want 0", path.PathIndex)
	}

	if len(path.YearlyOutcomes) != 10 {
		t.Errorf("YearlyOutcomes length = %d, want 10", len(path.YearlyOutcomes))
	}

	// Verify yearly outcomes are populated
	for i, outcome := range path.YearlyOutcomes {
		if outcome.Year != i+1 {
			t.Errorf("Year[%d] = %d, want %d", i, outcome.Year, i+1)
		}

		if outcome.PortfolioValue <= 0 {
			t.Errorf("Year %d: PortfolioValue = %d, should be > 0", outcome.Year, outcome.PortfolioValue)
		}

		if outcome.NetWorth <= 0 {
			t.Errorf("Year %d: NetWorth = %d, should be > 0", outcome.Year, outcome.NetWorth)
		}
	}

	// Verify final net worth
	if path.FinalNetWorth <= 0 {
		t.Errorf("FinalNetWorth = %d, should be > 0", path.FinalNetWorth)
	}
}

func TestSimulateConfiguration_Integration(t *testing.T) {
	// This test verifies the full Monte Carlo simulation with 1,000 paths
	// It's slow (~5-10s) but comprehensive
	if testing.Short() {
		t.Skip("Skipping full Monte Carlo simulation in short mode")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mcs := NewMonteCarloSimulator(logger)

	// Override to fewer paths for faster testing
	mcs.numPaths = 100 // Use 100 paths for faster test

	config := &investment.FrontierPoint{
		ConfigIndex: 0,
		Properties: []investment.PropertyInPortfolio{
			{
				Property: investment.Property{
					ID:            "p1",
					City:          "Austin",
					State:         "TX",
					Price:         400000,
					EstimatedRent: 2500,
				},
				LoanAmount:      300000,
				MonthlyPayment:  2000,
				MonthlyCashFlow: 500,
			},
			{
				Property: investment.Property{
					ID:            "p2",
					City:          "Denver",
					State:         "CO",
					Price:         450000,
					EstimatedRent: 2800,
				},
				LoanAmount:      337500,
				MonthlyPayment:  2250,
				MonthlyCashFlow: 550,
			},
		},
	}

	reinvestmentPlan := &investment.DualTrackReinvestment{
		TrackB: investment.TrackBReinvestment{
			Threshold: 50000,
		},
	}

	ctx := context.Background()
	results, err := mcs.SimulateConfiguration(ctx, config, reinvestmentPlan)

	if err != nil {
		t.Fatalf("SimulateConfiguration() error = %v", err)
	}

	// Verify results structure
	if results == nil {
		t.Fatal("results should not be nil")
	}

	if len(results.Paths) != 100 {
		t.Errorf("Paths length = %d, want 100", len(results.Paths))
	}

	if results.Percentiles == nil {
		t.Fatal("Percentiles should not be nil")
	}

	// Verify percentiles are populated
	if len(results.Percentiles.P50) != 10 {
		t.Errorf("P50 length = %d, want 10", len(results.Percentiles.P50))
	}

	// Verify base projection (P50) is populated
	if len(results.BaseProjection) != 10 {
		t.Errorf("BaseProjection length = %d, want 10", len(results.BaseProjection))
	}

	// Verify percentile ordering: P10 < P25 < P50 < P75 < P90 < P95
	finalYear := 9 // Year 10 (index 9)
	p10 := results.Percentiles.P10[finalYear].NetWorth
	p25 := results.Percentiles.P25[finalYear].NetWorth
	p50 := results.Percentiles.P50[finalYear].NetWorth
	p75 := results.Percentiles.P75[finalYear].NetWorth
	p90 := results.Percentiles.P90[finalYear].NetWorth
	p95 := results.Percentiles.P95[finalYear].NetWorth

	if !(p10 <= p25 && p25 <= p50 && p50 <= p75 && p75 <= p90 && p90 <= p95) {
		t.Errorf("Percentiles not properly ordered: P10=%d, P25=%d, P50=%d, P75=%d, P90=%d, P95=%d",
			p10, p25, p50, p75, p90, p95)
	}

	t.Logf("✅ Monte Carlo simulation completed: %d paths, P50 final net worth = $%d", len(results.Paths), p50)
}
