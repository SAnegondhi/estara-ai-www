package correlation

import (
	"context"
	"math"
	"testing"
)

func TestComputePearsonCorrelation(t *testing.T) {
	tests := []struct {
		name     string
		x        []float64
		y        []float64
		expected float64
	}{
		{
			name:     "perfect positive correlation",
			x:        []float64{1, 2, 3, 4, 5},
			y:        []float64{2, 4, 6, 8, 10},
			expected: 1.0,
		},
		{
			name:     "perfect negative correlation",
			x:        []float64{1, 2, 3, 4, 5},
			y:        []float64{10, 8, 6, 4, 2},
			expected: -1.0,
		},
		{
			name:     "weak correlation",
			x:        []float64{1, 2, 3, 4, 5},
			y:        []float64{5, 3, 4, 2, 1},
			expected: -0.9, // Actual calculated correlation
		},
		{
			name:     "moderate positive correlation",
			x:        []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			y:        []float64{2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
			expected: 1.0, // Perfect linear relationship
		},
		{
			name:     "empty arrays",
			x:        []float64{},
			y:        []float64{},
			expected: 0.0,
		},
		{
			name:     "mismatched lengths",
			x:        []float64{1, 2, 3},
			y:        []float64{1, 2},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComputePearsonCorrelation(tt.x, tt.y)

			// Allow small floating point differences
			if math.Abs(result-tt.expected) > 0.1 {
				t.Errorf("ComputePearsonCorrelation() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestClassifyMarketType(t *testing.T) {
	tests := []struct {
		name              string
		appreciationStdDev float64
		expected          string
	}{
		{
			name:              "stable market (low volatility)",
			appreciationStdDev: 1.5,
			expected:          "stable",
		},
		{
			name:              "stable market (edge case)",
			appreciationStdDev: 2.4,
			expected:          "stable",
		},
		{
			name:              "moderate market (mid volatility)",
			appreciationStdDev: 3.5,
			expected:          "moderate",
		},
		{
			name:              "moderate market (edge case)",
			appreciationStdDev: 4.9,
			expected:          "moderate",
		},
		{
			name:              "volatile market (high volatility)",
			appreciationStdDev: 6.0,
			expected:          "volatile",
		},
		{
			name:              "very volatile market",
			appreciationStdDev: 10.5,
			expected:          "volatile",
		},
		{
			name:              "zero volatility",
			appreciationStdDev: 0.0,
			expected:          "stable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyMarketType(tt.appreciationStdDev)
			if result != tt.expected {
				t.Errorf("ClassifyMarketType(%v) = %v, want %v", tt.appreciationStdDev, result, tt.expected)
			}
		})
	}
}

func TestMakeIdentityMatrix(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{"1x1 matrix", 1},
		{"2x2 matrix", 2},
		{"3x3 matrix", 3},
		{"5x5 matrix", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matrix := makeIdentityMatrix(tt.n)

			// Check dimensions
			if len(matrix) != tt.n {
				t.Errorf("Matrix rows = %v, want %v", len(matrix), tt.n)
			}

			// Check all elements
			for i := 0; i < tt.n; i++ {
				if len(matrix[i]) != tt.n {
					t.Errorf("Matrix row %d cols = %v, want %v", i, len(matrix[i]), tt.n)
				}

				for j := 0; j < tt.n; j++ {
					expected := 0.0
					if i == j {
						expected = 1.0 // Diagonal
					}

					if matrix[i][j] != expected {
						t.Errorf("Matrix[%d][%d] = %v, want %v", i, j, matrix[i][j], expected)
					}
				}
			}
		})
	}
}

func TestCorrelationMatrix_2x2Structure(t *testing.T) {
	// Create service without store (placeholder test)
	svc := &Service{store: nil}

	marketIDs := []string{"Austin, TX", "Denver, CO"}
	ctx := context.Background()

	result, err := svc.ComputeCorrelationMatrix(ctx, marketIDs)
	if err != nil {
		t.Fatalf("ComputeCorrelationMatrix() error = %v", err)
	}

	if result == nil {
		t.Fatal("ComputeCorrelationMatrix() returned nil")
	}

	// Check 2N×2N structure (2 markets → 4×4 matrix)
	n := len(marketIDs)
	expectedSize := 2 * n

	if len(result.JointCorr) != expectedSize {
		t.Errorf("JointCorr rows = %v, want %v", len(result.JointCorr), expectedSize)
	}

	for i, row := range result.JointCorr {
		if len(row) != expectedSize {
			t.Errorf("JointCorr row %d cols = %v, want %v", i, len(row), expectedSize)
		}
	}

	// Check diagonal elements are 1.0 (self-correlation)
	for i := 0; i < expectedSize; i++ {
		if result.JointCorr[i][i] != 1.0 {
			t.Errorf("JointCorr[%d][%d] = %v, want 1.0 (diagonal should be 1.0)", i, i, result.JointCorr[i][i])
		}
	}

	// Check cross-correlation block (appreciation vs rent)
	// JointCorr[0][2] should be crossCorr (market 0 appreciation vs market 0 rent)
	if result.JointCorr[0][n] != result.CrossCorrelation {
		t.Errorf("JointCorr[0][%d] = %v, want %v (cross-correlation)", n, result.JointCorr[0][n], result.CrossCorrelation)
	}
}
