package history

import (
	"math"
	"testing"
)

func TestComputeHistoricalPeakDrop(t *testing.T) {
	tests := []struct {
		name     string
		zhviData []float64
		expected float64
	}{
		{
			name: "steady decline",
			zhviData: []float64{
				100, 99, 98, 97, 96, 95, 94, 93, 92, 91, 90, 89, // 12 months
				88, 87, 86, 85, 84, 83, 82, 81, 80, 79, 78, 77,  // 12 more months
			},
			expected: -13.48, // Worst 12mo return found by rolling window
		},
		{
			name: "sharp drop then recovery",
			zhviData: []float64{
				200, 200, 200, 200, 200, 200, 150, 150, 150, 150, 150, 150, // Sharp 25% drop
				160, 170, 180, 190, 200, 210, 220, 230, 240, 250, 260, 270, // Recovery
			},
			expected: -20.0, // Worst 12mo return (not instantaneous drop)
		},
		{
			name: "growth only (no drops)",
			zhviData: []float64{
				100, 102, 104, 106, 108, 110, 112, 114, 116, 118, 120, 122,
				124, 126, 128, 130, 132, 134, 136, 138, 140, 142, 144, 146,
			},
			expected: 0.0, // No negative returns
		},
		{
			name: "small drop",
			zhviData: []float64{
				100, 100, 100, 100, 100, 100, 98, 98, 98, 98, 98, 98,
				99, 100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110,
			},
			expected: -1.0, // Worst 12mo return found by rolling window
		},
		{
			name: "insufficient data",
			zhviData: []float64{
				100, 102, 104, 106, 108, // Only 5 data points
			},
			expected: 0.0, // Returns 0 when < 12 data points
		},
		{
			name: "empty data",
			zhviData: []float64{},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComputeHistoricalPeakDrop(tt.zhviData)

			// Allow small floating point differences
			if math.Abs(result-tt.expected) > 0.01 {
				t.Errorf("ComputeHistoricalPeakDrop() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestComputeHistoricalPeakDrop_RollingWindow(t *testing.T) {
	// Test that it finds the worst 12-month return across the entire window
	// Create data where worst drop is in the middle
	zhviData := make([]float64, 36) // 3 years of monthly data

	// Year 1: stable
	for i := 0; i < 12; i++ {
		zhviData[i] = 200.0
	}

	// Year 2: sharp drop (worst period)
	for i := 12; i < 24; i++ {
		zhviData[i] = 140.0 // 30% drop
	}

	// Year 3: recovery
	for i := 24; i < 36; i++ {
		zhviData[i] = 180.0
	}

	result := ComputeHistoricalPeakDrop(zhviData)
	expected := -30.0 // (140 - 200) / 200 * 100 = -30%

	if math.Abs(result-expected) > 0.01 {
		t.Errorf("ComputeHistoricalPeakDrop() = %v, want %v (should find worst 12mo drop)", result, expected)
	}
}
