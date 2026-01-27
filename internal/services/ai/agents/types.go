package agents

import "time"

// MarketAnalysisRequest holds parameters for market analysis
type MarketAnalysisRequest struct {
	Location     string                 `json:"location"`
	CacheKey     string                 `json:"cacheKey"`
	MarketData   map[string]interface{} `json:"marketData,omitempty"`
	ForceRefresh bool                   `json:"forceRefresh,omitempty"`
}

// MarketAnalysisResult holds the complete market analysis result
type MarketAnalysisResult struct {
	Location          string            `json:"location"`
	MetricsAnalysis   string            `json:"metricsAnalysis"`
	NarrativeAnalysis string            `json:"narrativeAnalysis"`
	CombinedInsight   string            `json:"combinedInsight"`
	MarketData        MarketDataSummary `json:"marketData"`
	Confidence        float64           `json:"confidence"`
	GeneratedAt       time.Time         `json:"generatedAt"`
	CachedAt          *time.Time        `json:"cachedAt,omitempty"`
}

// MarketDataSummary holds key market metrics
type MarketDataSummary struct {
	MedianHomePrice int     `json:"medianHomePrice"`
	MedianRent      int     `json:"medianRent"`
	PriceChange1Y   float64 `json:"priceChange1Y"`
	RentChange1Y    float64 `json:"rentChange1Y"`
	MortgageRate    float64 `json:"mortgageRate"`
	InventoryLevel  string  `json:"inventoryLevel"`
	DaysOnMarket    int     `json:"daysOnMarket"`
}

// AnalysisJobResponse holds the response for queue endpoint
type AnalysisJobResponse struct {
	Success   bool   `json:"success"`
	JobID     string `json:"jobId"`
	StreamURL string `json:"streamUrl"`
}

// AnalysisJobDetail holds detailed job information
type AnalysisJobDetail struct {
	ID          string                 `json:"id"`
	Status      string                 `json:"status"`
	Progress    int                    `json:"progress"`
	Location    string                 `json:"location"`
	Result      *MarketAnalysisResult  `json:"result,omitempty"`
	ResultData  map[string]interface{} `json:"resultData,omitempty"` // Raw result data from job
	Error       string                 `json:"error,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
	CompletedAt *time.Time             `json:"completedAt,omitempty"`
}

// AnalysisJobListResponse holds a list of analysis jobs
type AnalysisJobListResponse struct {
	Success bool                `json:"success"`
	Jobs    []AnalysisJobDetail `json:"jobs"`
	Total   int                 `json:"total"`
}

// AnalysisEventType constants
const (
	AnalysisEventProgress  = "progress"
	AnalysisEventMetrics   = "metrics"
	AnalysisEventNarrative = "narrative"
	AnalysisEventCombined  = "combined"
	AnalysisEventComplete  = "complete"
	AnalysisEventError     = "error"
)

// AnalysisContext holds comprehensive context for AI analysis
type AnalysisContext struct {
	Location       string            `json:"location"`
	City           string            `json:"city"`
	State          string            `json:"state"`
	MarketData     MarketDataSummary `json:"marketData"`
	HistoricalData *HistoricalContext `json:"historicalData,omitempty"`
	EconomicData   *EconomicContext   `json:"economicData,omitempty"`
}

// HistoricalContext holds historical market data for context
type HistoricalContext struct {
	HomeValueChange1Y float64 `json:"homeValueChange1Y"`
	HomeValueChange3Y float64 `json:"homeValueChange3Y"`
	HomeValueChange5Y float64 `json:"homeValueChange5Y"`
	RentChange1Y      float64 `json:"rentChange1Y"`
	RentChange3Y      float64 `json:"rentChange3Y"`
}

// EconomicContext holds economic indicators
type EconomicContext struct {
	MortgageRate30Y  float64 `json:"mortgageRate30Y"`
	MortgageRate15Y  float64 `json:"mortgageRate15Y"`
	UnemploymentRate float64 `json:"unemploymentRate,omitempty"`
	InflationRate    float64 `json:"inflationRate,omitempty"`
}
