package correlation

import (
	"context"
	"math"

	"github.com/estara-ai/www/internal/db"
)

// Service handles market correlation and volatility calculations
type Service struct {
	store *db.Store
}

// NewService creates a new correlation service
func NewService(store *db.Store) *Service {
	return &Service{
		store: store,
	}
}

// MarketVolatilityMetrics holds volatility data for a market
type MarketVolatilityMetrics struct {
	MarketID            string  `json:"marketId"`
	AppreciationStdDev  float64 `json:"appreciationStdDev"`  // Annual appreciation volatility (%)
	RentGrowthStdDev    float64 `json:"rentGrowthStdDev"`    // Annual rent growth volatility (%)
	HistoricalPeakDrop  float64 `json:"historicalPeakDrop"`  // Worst 12mo return in 20yr (%)
	MarketType          string  `json:"marketType"`          // stable, moderate, volatile
}

// GetMarketVolatilityMetrics retrieves volatility metrics for a market
// TODO: Phase 1 - Implement integration with metro_time_series and market_history tables
func (s *Service) GetMarketVolatilityMetrics(ctx context.Context, marketID string) (*MarketVolatilityMetrics, error) {
	// Placeholder implementation - Phase 1 will integrate with:
	// 1. metro_time_series.zhvf_data for appreciation volatility
	// 2. metro_time_series.redfin_data for rent growth volatility
	// 3. market_history table for historical peak drop

	// For now, return default values
	return &MarketVolatilityMetrics{
		MarketID:            marketID,
		AppreciationStdDev:  3.5,  // Default: 3.5% annual appreciation volatility
		RentGrowthStdDev:    2.0,  // Default: 2.0% annual rent growth volatility
		HistoricalPeakDrop:  -8.5, // Default: -8.5% worst 12mo drop
		MarketType:          ClassifyMarketType(3.5), // Default: moderate
	}, nil
}

// ClassifyMarketType classifies market volatility as stable/moderate/volatile
// Based on appreciation standard deviation:
//   - stable: σ < 2.5%
//   - moderate: 2.5% ≤ σ < 5.0%
//   - volatile: σ ≥ 5.0%
func ClassifyMarketType(appreciationStdDev float64) string {
	if appreciationStdDev < 2.5 {
		return "stable"
	}
	if appreciationStdDev < 5.0 {
		return "moderate"
	}
	return "volatile"
}

// CorrelationMatrix represents a 2N×2N joint correlation matrix
type CorrelationMatrix struct {
	Markets              []string    `json:"markets"`              // Market IDs
	AppreciationCorr     [][]float64 `json:"appreciationCorr"`     // N×N appreciation correlations
	RentCorr             [][]float64 `json:"rentCorr"`             // N×N rent correlations
	JointCorr            [][]float64 `json:"jointCorr"`            // 2N×2N joint matrix
	CrossCorrelation     float64     `json:"crossCorrelation"`     // Appreciation-Rent correlation (e.g., 0.3)
}

// ComputeCorrelationMatrix computes 2N×2N joint correlation matrix for N markets
// Uses Pearson correlation on ZHVI (appreciation) and ZORI (rent) time series
// TODO: Phase 1 - Implement with metro_time_series data
func (s *Service) ComputeCorrelationMatrix(ctx context.Context, marketIDs []string) (*CorrelationMatrix, error) {
	n := len(marketIDs)
	if n == 0 {
		return nil, nil
	}

	// Placeholder implementation - Phase 1 will:
	// 1. Fetch ZHVI/ZORI time series from metro_time_series
	// 2. Compute Pearson correlation for each market pair
	// 3. Build 2N×2N joint matrix
	// 4. Apply PSD regularization if needed

	// For now, return identity matrix (no correlation)
	appreciationCorr := makeIdentityMatrix(n)
	rentCorr := makeIdentityMatrix(n)

	// Build 2N×2N joint matrix
	size := 2 * n
	jointCorr := make([][]float64, size)
	for i := range jointCorr {
		jointCorr[i] = make([]float64, size)
	}

	// Fill top-left: appreciation correlations
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			jointCorr[i][j] = appreciationCorr[i][j]
		}
	}

	// Fill bottom-right: rent correlations
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			jointCorr[n+i][n+j] = rentCorr[i][j]
		}
	}

	// Fill off-diagonal: cross-correlations (appreciation vs rent)
	crossCorr := 0.3 // Default cross-correlation
	for i := 0; i < n; i++ {
		jointCorr[i][n+i] = crossCorr
		jointCorr[n+i][i] = crossCorr
	}

	return &CorrelationMatrix{
		Markets:          marketIDs,
		AppreciationCorr: appreciationCorr,
		RentCorr:         rentCorr,
		JointCorr:        jointCorr,
		CrossCorrelation: crossCorr,
	}, nil
}

// ComputePearsonCorrelation computes Pearson correlation coefficient between two time series
func ComputePearsonCorrelation(x, y []float64) float64 {
	if len(x) == 0 || len(x) != len(y) {
		return 0.0
	}

	n := float64(len(x))

	// Calculate means
	var sumX, sumY float64
	for i := range x {
		sumX += x[i]
		sumY += y[i]
	}
	meanX := sumX / n
	meanY := sumY / n

	// Calculate correlation numerator and denominators
	var numerator, sumX2, sumY2 float64
	for i := range x {
		dx := x[i] - meanX
		dy := y[i] - meanY
		numerator += dx * dy
		sumX2 += dx * dx
		sumY2 += dy * dy
	}

	denominator := math.Sqrt(sumX2 * sumY2)
	if denominator == 0 {
		return 0.0
	}

	return numerator / denominator
}

// makeIdentityMatrix creates an N×N identity matrix
func makeIdentityMatrix(n int) [][]float64 {
	matrix := make([][]float64, n)
	for i := range matrix {
		matrix[i] = make([]float64, n)
		matrix[i][i] = 1.0 // Diagonal = 1.0
	}
	return matrix
}

// RegularizeCorrelationMatrix applies PSD regularization to ensure positive semi-definite
// Uses eigenvalue clipping to fix numerical issues
// TODO: Phase 6 - Implement eigenvalue decomposition for PSD regularization
func RegularizeCorrelationMatrix(matrix [][]float64, epsilon float64) [][]float64 {
	// Placeholder - Phase 6 will implement:
	// 1. Eigendecomposition: C = Q Λ Qᵀ
	// 2. Clip negative eigenvalues: Λ_clipped = max(Λ, epsilon)
	// 3. Reconstruct: C_psd = Q Λ_clipped Qᵀ
	// 4. Rescale diagonal to 1.0

	return matrix
}
