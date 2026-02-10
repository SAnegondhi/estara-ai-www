package trends

import "time"

// TrendsRequest holds parameters for market trends retrieval
type TrendsRequest struct {
	Location     string `json:"location"`
	Years        int    `json:"years"`        // Default 5
	IncludeAI    bool   `json:"includeAi"`    // Include AI synthesis
	CacheKey     string `json:"cacheKey"`
	UserID       string `json:"userId"`       // User ID for caching
	ForceRefresh bool   `json:"forceRefresh"`
}

// TrendsResult holds the complete market trends result
type TrendsResult struct {
	Location          string             `json:"location"`
	Historical        *HistoricalData    `json:"historical"`
	YoYChanges        *YoYChanges        `json:"yoyChanges"`
	CurrentMetrics    *CurrentMetrics    `json:"currentMetrics"`
	CalculatedMetrics *CalculatedMetrics `json:"calculatedMetrics,omitempty"` // ADR-073: CAGR, volatility, R², cycle
	Synthesis         *TrendSynthesis    `json:"synthesis,omitempty"`
	GeneratedAt       time.Time          `json:"generatedAt"`
	CachedAt          *time.Time         `json:"cachedAt,omitempty"`
}

// HistoricalData holds time series data for various metrics
type HistoricalData struct {
	// Home Values (ZHVI)
	HomeValues []TimeSeriesPoint `json:"homeValues"`

	// Rental Values (ZORI)
	RentValues []TimeSeriesPoint `json:"rentValues"`

	// Mortgage Rates (FRED)
	MortgageRates []TimeSeriesPoint `json:"mortgageRates"`

	// Additional economic indicators
	UnemploymentRates []TimeSeriesPoint `json:"unemploymentRates,omitempty"`
	InflationRates    []TimeSeriesPoint `json:"inflationRates,omitempty"`
}

// TimeSeriesPoint represents a single data point in a time series
type TimeSeriesPoint struct {
	Date  time.Time `json:"date"`
	Value float64   `json:"value"`
}

// YoYChanges holds year-over-year and multi-year changes
type YoYChanges struct {
	HomeValueChange    float64 `json:"homeValueChange"`    // Percentage
	RentChange         float64 `json:"rentChange"`         // Percentage
	MortgageRateChange float64 `json:"mortgageRateChange"` // Basis points

	// Multi-year changes
	HomeValue3Y float64 `json:"homeValue3Y"`
	HomeValue5Y float64 `json:"homeValue5Y"`
	Rent3Y      float64 `json:"rent3Y"`
	Rent5Y      float64 `json:"rent5Y"`
}

// CurrentMetrics holds the latest market metrics
type CurrentMetrics struct {
	MedianHomeValue  int     `json:"medianHomeValue"`
	MedianRent       int     `json:"medianRent"`
	MortgageRate30Y  float64 `json:"mortgageRate30Y"`
	MortgageRate15Y  float64 `json:"mortgageRate15Y"`
	PriceToRentRatio float64 `json:"priceToRentRatio"`
	GrossYield       float64 `json:"grossYield"` // Annual rent / home value
}

// TrendSynthesis holds AI-generated trend insights
type TrendSynthesis struct {
	Summary          string   `json:"summary"`
	KeyInsights      []string `json:"keyInsights"`
	MarketOutlook    string   `json:"marketOutlook"`    // bullish, bearish, neutral
	InvestmentTiming string   `json:"investmentTiming"` // favorable, unfavorable, neutral
	RiskFactors      []string `json:"riskFactors"`
	Confidence       float64  `json:"confidence"`
}

// ChartRequest holds parameters for chart data endpoint
type ChartRequest struct {
	Location string `json:"location"`
	Metric   string `json:"metric"` // homeValues, rentValues, mortgageRates
	Years    int    `json:"years"`
}

// ChartData holds data formatted for chart visualization
type ChartData struct {
	Metric    string            `json:"metric"`
	Location  string            `json:"location"`
	Points    []TimeSeriesPoint `json:"points"`
	YoYChange float64           `json:"yoyChange"`
	Min       float64           `json:"min"`
	Max       float64           `json:"max"`
	Avg       float64           `json:"avg"`
}

// TrendsResponse wraps the trends result for API response
type TrendsResponse struct {
	Success bool          `json:"success"`
	Data    *TrendsResult `json:"data,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// ChartResponse wraps the chart data for API response
type ChartResponse struct {
	Success bool       `json:"success"`
	Data    *ChartData `json:"data,omitempty"`
	Error   string     `json:"error,omitempty"`
}

// SynthesisRequest holds parameters for AI synthesis endpoint
type SynthesisRequest struct {
	Location     string `json:"location"`
	Years        int    `json:"years"`
	CacheKey     string `json:"cacheKey"`
	ForceRefresh bool   `json:"forceRefresh"`
}

// SynthesisResponse wraps the synthesis result for API response
type SynthesisResponse struct {
	Success bool            `json:"success"`
	Data    *TrendSynthesis `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}
