package optimization

import (
	"math"
)

// MarkowitzCalculator provides portfolio variance and volatility calculations
type MarkowitzCalculator struct {
}

// NewMarkowitzCalculator creates a new Markowitz calculator
func NewMarkowitzCalculator() *MarkowitzCalculator {
	return &MarkowitzCalculator{}
}

// CalculatePortfolioVariance computes analytical portfolio variance using Markowitz formula
// Formula: σ² = Σᵢ Σⱼ wᵢ wⱼ σᵢ σⱼ ρᵢⱼ
// Where:
//   - wᵢ, wⱼ = weights (allocation %) for assets i and j
//   - σᵢ, σⱼ = standard deviations (volatilities) for assets i and j
//   - ρᵢⱼ = correlation coefficient between assets i and j
func (mc *MarkowitzCalculator) CalculatePortfolioVariance(
	weights []float64,           // Asset weights (must sum to 1.0)
	volatilities []float64,      // Asset volatilities (annual std dev)
	correlationMatrix [][]float64, // N×N correlation matrix
) float64 {
	n := len(weights)
	if n == 0 || n != len(volatilities) || n != len(correlationMatrix) {
		return 0.0 // Invalid inputs
	}

	variance := 0.0

	// Double summation: Σᵢ Σⱼ wᵢ wⱼ σᵢ σⱼ ρᵢⱼ
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i < len(correlationMatrix[j]) {
				variance += weights[i] * weights[j] * volatilities[i] * volatilities[j] * correlationMatrix[i][j]
			}
		}
	}

	return variance
}

// CalculatePortfolioVolatility computes analytical portfolio volatility (standard deviation)
// Returns: σ = √(variance)
func (mc *MarkowitzCalculator) CalculatePortfolioVolatility(
	weights []float64,
	volatilities []float64,
	correlationMatrix [][]float64,
) float64 {
	variance := mc.CalculatePortfolioVariance(weights, volatilities, correlationMatrix)
	return math.Sqrt(variance)
}

// CalculateSharpeRatio computes Sharpe ratio for a portfolio
// Formula: (E[R] - Rf) / σ
// Where:
//   - E[R] = expected portfolio return
//   - Rf = risk-free rate (typically mortgage rate or treasury yield)
//   - σ = portfolio volatility (standard deviation)
func (mc *MarkowitzCalculator) CalculateSharpeRatio(
	expectedReturn float64,  // Expected annual return (%)
	riskFreeRate float64,    // Risk-free rate (%)
	volatility float64,      // Portfolio volatility (annual std dev, %)
) float64 {
	if volatility == 0.0 {
		return 0.0 // Undefined for zero volatility
	}

	return (expectedReturn - riskFreeRate) / volatility
}

// PropertyVolatilityData holds volatility metrics for a property/market
type PropertyVolatilityData struct {
	AppreciationStdDev float64 // Annual appreciation volatility (%)
	RentGrowthStdDev   float64 // Annual rent growth volatility (%)
}

// BuildCorrelationMatrix creates a 2N×2N joint correlation matrix for N markets
// Includes both appreciation and rent growth correlations
// Matrix structure:
//   - Block [0:N, 0:N] = appreciation correlations
//   - Block [N:2N, N:2N] = rent growth correlations
//   - Off-diagonal blocks = cross-correlations (appreciation vs rent)
func BuildCorrelationMatrix(
	appreciationCorr [][]float64, // N×N appreciation correlation matrix
	rentCorr [][]float64,         // N×N rent correlation matrix
	crossCorr float64,            // Cross-correlation between appreciation and rent (e.g., 0.3)
) [][]float64 {
	n := len(appreciationCorr)
	if n == 0 {
		return [][]float64{}
	}

	// Create 2N×2N matrix
	size := 2 * n
	matrix := make([][]float64, size)
	for i := range matrix {
		matrix[i] = make([]float64, size)
	}

	// Fill appreciation block (top-left)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			matrix[i][j] = appreciationCorr[i][j]
		}
	}

	// Fill rent growth block (bottom-right)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			matrix[n+i][n+j] = rentCorr[i][j]
		}
	}

	// Fill cross-correlation blocks (off-diagonal)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				// Same market: use cross-correlation
				matrix[i][n+j] = crossCorr
				matrix[n+i][j] = crossCorr
			} else {
				// Different markets: assume 0 cross-correlation
				matrix[i][n+j] = 0.0
				matrix[n+i][j] = 0.0
			}
		}
	}

	return matrix
}

// RegularizeCorrelationMatrix ensures the correlation matrix is positive semi-definite (PSD)
// Uses eigenvalue clipping to fix numerical issues
// This is critical for Cholesky decomposition in Monte Carlo simulation
func RegularizeCorrelationMatrix(matrix [][]float64, epsilon float64) [][]float64 {
	// TODO: Phase 6 implementation will add eigenvalue decomposition and clipping
	// For now, return matrix unchanged (Phase 0 placeholder)
	// Algorithm:
	// 1. Eigendecomposition: C = Q Λ Qᵀ
	// 2. Clip negative eigenvalues: Λ_clipped = max(Λ, epsilon)
	// 3. Reconstruct: C_psd = Q Λ_clipped Qᵀ
	// 4. Rescale diagonal to 1.0 (maintain correlation property)

	return matrix
}
