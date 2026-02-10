package trends

import (
	"math"
)

// Calculator computes statistical metrics from time series data
type Calculator struct{}

// NewCalculator creates a new trend calculator
func NewCalculator() *Calculator {
	return &Calculator{}
}

// CalculatedMetrics holds all computed metrics for a market
type CalculatedMetrics struct {
	// Price metrics
	PriceCAGR1Y  float64 `json:"priceCagr1Y"`
	PriceCAGR3Y  float64 `json:"priceCagr3Y"`
	PriceCAGR5Y  float64 `json:"priceCagr5Y"`
	PriceCAGR10Y float64 `json:"priceCagr10Y,omitempty"`

	// Rent metrics
	RentCAGR1Y float64 `json:"rentCagr1Y"`
	RentCAGR3Y float64 `json:"rentCagr3Y"`
	RentCAGR5Y float64 `json:"rentCagr5Y"`

	// Volatility
	PriceVolatility float64 `json:"priceVolatility"` // Annualized std deviation of returns
	MaxDrawdown     float64 `json:"maxDrawdown"`     // Maximum peak-to-trough decline (%)

	// Trend direction
	PriceTrendSlope float64 `json:"priceTrendSlope"` // Linear regression slope (annualized %)
	PriceTrendR2    float64 `json:"priceTrendR2"`    // R² coefficient of determination
	RentTrendSlope  float64 `json:"rentTrendSlope"`
	RentTrendR2     float64 `json:"rentTrendR2"`

	// Market cycle position
	CyclePosition   string  `json:"cyclePosition"`   // recovery, expansion, hyper_supply, recession
	CycleConfidence float64 `json:"cycleConfidence"` // 0-1

	// Investment metrics
	CapRateEstimate  float64 `json:"capRateEstimate,omitempty"`
	PriceToRentRatio float64 `json:"priceToRentRatio,omitempty"`
	GrossYield       float64 `json:"grossYield,omitempty"` // Annual rent / home value %
}

// CAGR computes compound annual growth rate
func CAGR(startVal, endVal float64, years float64) float64 {
	if startVal <= 0 || endVal <= 0 || years <= 0 {
		return 0
	}
	return (math.Pow(endVal/startVal, 1.0/years) - 1) * 100
}

// Volatility computes annualized standard deviation of monthly returns
func Volatility(series []float64) float64 {
	if len(series) < 3 {
		return 0
	}

	// Calculate monthly returns
	returns := make([]float64, 0, len(series)-1)
	for i := 1; i < len(series); i++ {
		if series[i-1] > 0 {
			ret := (series[i] - series[i-1]) / series[i-1]
			returns = append(returns, ret)
		}
	}

	if len(returns) < 2 {
		return 0
	}

	// Mean return
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	// Variance
	sumSq := 0.0
	for _, r := range returns {
		diff := r - mean
		sumSq += diff * diff
	}
	variance := sumSq / float64(len(returns)-1)

	// Annualize (sqrt(12) for monthly data)
	return math.Sqrt(variance) * math.Sqrt(12) * 100
}

// MaxDrawdown computes the maximum peak-to-trough decline as a percentage
func MaxDrawdown(series []float64) float64 {
	if len(series) < 2 {
		return 0
	}

	peak := series[0]
	maxDD := 0.0

	for _, val := range series {
		if val > peak {
			peak = val
		}
		if peak > 0 {
			dd := (peak - val) / peak * 100
			if dd > maxDD {
				maxDD = dd
			}
		}
	}

	return maxDD
}

// LinearRegression computes slope and R² for a time series
// Returns slope (annualized % change per year) and R² (0-1)
func LinearRegression(series []float64) (slope, r2 float64) {
	n := len(series)
	if n < 3 {
		return 0, 0
	}

	// x = time index (months), y = values
	sumX, sumY, sumXY, sumX2, sumY2 := 0.0, 0.0, 0.0, 0.0, 0.0

	for i, y := range series {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
		sumY2 += y * y
	}

	nf := float64(n)
	denom := nf*sumX2 - sumX*sumX
	if denom == 0 {
		return 0, 0
	}

	// Slope (per month)
	b := (nf*sumXY - sumX*sumY) / denom

	// R²
	meanY := sumY / nf
	ssTot := sumY2 - nf*meanY*meanY
	ssRes := 0.0
	a := (sumY - b*sumX) / nf
	for i, y := range series {
		pred := a + b*float64(i)
		diff := y - pred
		ssRes += diff * diff
	}

	if ssTot == 0 {
		r2 = 0
	} else {
		r2 = 1 - ssRes/ssTot
	}

	// Convert monthly slope to annualized % relative to first value
	if series[0] > 0 {
		slope = (b * 12 / series[0]) * 100
	}

	return slope, r2
}

// CalculateAllMetrics computes all metrics from historical data
func (c *Calculator) CalculateAllMetrics(data *HistoricalData) *CalculatedMetrics {
	m := &CalculatedMetrics{}

	// Extract value arrays
	homeValues := extractValues(data.HomeValues)
	rentValues := extractValues(data.RentValues)

	// CAGR calculations
	if len(homeValues) > 0 {
		latest := homeValues[len(homeValues)-1]
		m.PriceCAGR1Y = cagrFromSeries(homeValues, 12, latest)
		m.PriceCAGR3Y = cagrFromSeries(homeValues, 36, latest)
		m.PriceCAGR5Y = cagrFromSeries(homeValues, 60, latest)
		m.PriceCAGR10Y = cagrFromSeries(homeValues, 120, latest)
	}

	if len(rentValues) > 0 {
		latest := rentValues[len(rentValues)-1]
		m.RentCAGR1Y = cagrFromSeries(rentValues, 12, latest)
		m.RentCAGR3Y = cagrFromSeries(rentValues, 36, latest)
		m.RentCAGR5Y = cagrFromSeries(rentValues, 60, latest)
	}

	// Volatility & drawdown
	m.PriceVolatility = Volatility(homeValues)
	m.MaxDrawdown = MaxDrawdown(homeValues)

	// Trend direction (linear regression)
	m.PriceTrendSlope, m.PriceTrendR2 = LinearRegression(homeValues)
	m.RentTrendSlope, m.RentTrendR2 = LinearRegression(rentValues)

	// Cycle position detection
	m.CyclePosition, m.CycleConfidence = c.detectCyclePosition(m)

	// Investment metrics
	if len(homeValues) > 0 && len(rentValues) > 0 {
		latestHome := homeValues[len(homeValues)-1]
		latestRent := rentValues[len(rentValues)-1]
		if latestHome > 0 && latestRent > 0 {
			annualRent := latestRent * 12
			m.PriceToRentRatio = latestHome / annualRent
			m.GrossYield = (annualRent / latestHome) * 100
			// Simple cap rate estimate: gross yield - ~40% for expenses
			m.CapRateEstimate = m.GrossYield * 0.6
		}
	}

	return m
}

// detectCyclePosition determines the market cycle phase based on price and rent trends
func (c *Calculator) detectCyclePosition(m *CalculatedMetrics) (position string, confidence float64) {
	// Use 1Y CAGR and trend slope to classify cycle
	priceGrowth := m.PriceCAGR1Y
	rentGrowth := m.RentCAGR1Y
	trendSlope := m.PriceTrendSlope

	switch {
	case priceGrowth > 5 && rentGrowth > 3 && trendSlope > 3:
		// Strong growth in both price and rent = expansion
		return "expansion", math.Min(0.9, m.PriceTrendR2+0.2)

	case priceGrowth > 8 && priceGrowth > rentGrowth*2:
		// Prices growing much faster than rents = hyper-supply risk
		return "hyper_supply", math.Min(0.8, m.PriceTrendR2+0.1)

	case priceGrowth < -2 || trendSlope < -3:
		// Declining prices = recession
		return "recession", math.Min(0.85, math.Abs(m.PriceTrendR2)+0.15)

	case priceGrowth > 0 && priceGrowth <= 5 && trendSlope > 0:
		// Moderate positive growth after decline or stagnation = recovery
		return "recovery", math.Min(0.75, m.PriceTrendR2+0.1)

	default:
		return "expansion", 0.5 // Default with low confidence
	}
}

// cagrFromSeries calculates CAGR looking back N months from the latest value
func cagrFromSeries(values []float64, months int, latestValue float64) float64 {
	if len(values) <= months {
		// Use all available data
		if len(values) < 2 {
			return 0
		}
		years := float64(len(values)-1) / 12.0
		if years <= 0 {
			return 0
		}
		return CAGR(values[0], latestValue, years)
	}

	startIdx := len(values) - months - 1
	if startIdx < 0 {
		startIdx = 0
	}

	years := float64(months) / 12.0
	return CAGR(values[startIdx], latestValue, years)
}

// extractValues extracts float64 values from TimeSeriesPoint slice
func extractValues(points []TimeSeriesPoint) []float64 {
	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.Value
	}
	return values
}
