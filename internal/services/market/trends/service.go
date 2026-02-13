package trends

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/market/fred"
	"github.com/estara-ai/www/internal/services/market/timeseries"
)

// Service provides market trends analysis
type Service struct {
	metro  *timeseries.MetroReader
	fred   *fred.Service
	ai     *anthropic.Client
	cache  *cache.HybridCache
	logger *slog.Logger
}

// ServiceConfig holds configuration for the trends service
type ServiceConfig struct {
	Metro *timeseries.MetroReader
	FRED  *fred.Service
	AI    *anthropic.Client
	Cache *cache.HybridCache
}

// NewService creates a new trends service
func NewService(cfg ServiceConfig) *Service {
	return &Service{
		metro:  cfg.Metro,
		fred:   cfg.FRED,
		ai:     cfg.AI,
		cache:  cfg.Cache,
		logger: slog.Default().With("component", "trends_service"),
	}
}

// GetTrends retrieves comprehensive market trends for a location
func (s *Service) GetTrends(ctx context.Context, req TrendsRequest) (*TrendsResult, error) {
	// Set defaults
	if req.Years <= 0 {
		req.Years = 5
	}

	// Check cache first
	if !req.ForceRefresh && req.CacheKey != "" && req.UserID != "" {
		if cached, err := s.checkCache(ctx, req.UserID, req.CacheKey); err == nil && cached != nil {
			s.logger.Info("returning cached trends",
				"location", req.Location,
				"cache_key", req.CacheKey,
			)
			return cached, nil
		}
	}

	s.logger.Info("fetching market trends",
		"location", req.Location,
		"years", req.Years,
		"include_ai", req.IncludeAI,
	)

	// Get historical data
	historical, err := s.GetHistoricalData(ctx, req.Location, req.Years)
	if err != nil {
		return nil, fmt.Errorf("failed to get historical data: %w", err)
	}

	// Calculate YoY changes
	yoyChanges := s.CalculateYoYChanges(historical)

	// Get current metrics
	currentMetrics := s.getCurrentMetrics(historical)

	// Calculate statistical metrics (ADR-073)
	calc := NewCalculator()
	calculatedMetrics := calc.CalculateAllMetrics(historical)

	result := &TrendsResult{
		Location:          req.Location,
		Historical:        historical,
		YoYChanges:        yoyChanges,
		CurrentMetrics:    currentMetrics,
		CalculatedMetrics: calculatedMetrics,
		GeneratedAt:       time.Now(),
	}

	// Generate AI synthesis if requested
	if req.IncludeAI {
		synthesis, err := s.SynthesizeTrends(ctx, historical, yoyChanges, req.Location)
		if err != nil {
			s.logger.Warn("failed to generate AI synthesis", "error", err)
			// Continue without synthesis
		} else {
			result.Synthesis = synthesis
		}
	}

	// Cache result
	if req.CacheKey != "" && req.UserID != "" {
		if err := s.cacheResult(ctx, req.UserID, req.CacheKey, result); err != nil {
			s.logger.Warn("failed to cache result", "error", err)
		}
	}

	return result, nil
}

// GetHistoricalData retrieves historical time series data
func (s *Service) GetHistoricalData(ctx context.Context, location string, years int) (*HistoricalData, error) {
	// Parse location
	parts := strings.Split(location, ",")
	city := strings.TrimSpace(parts[0])
	var state string
	if len(parts) > 1 {
		state = strings.TrimSpace(parts[1])
	}

	historical := &HistoricalData{
		HomeValues:    make([]TimeSeriesPoint, 0),
		RentValues:    make([]TimeSeriesPoint, 0),
		MortgageRates: make([]TimeSeriesPoint, 0),
	}

	// Calculate date range
	endDate := time.Now()
	startDate := endDate.AddDate(-years, 0, 0)

	// Fetch home values and rent values from metro time series
	if s.metro != nil {
		// Build region name with state if available
		regionName := city
		if state != "" {
			regionName = city + ", " + state
		}
		// Get time series data (contains both ZHVI and ZORI)
		metroData, err := s.metro.GetTimeSeries(ctx, regionName, "city", startDate, endDate)
		if err != nil {
			s.logger.Warn("failed to get metro time series data", "error", err)
		} else {
			for _, v := range metroData {
				if v.ZHVI > 0 {
					historical.HomeValues = append(historical.HomeValues, TimeSeriesPoint{
						Date:  v.Date,
						Value: v.ZHVI,
					})
				}
				if v.ZORI > 0 {
					historical.RentValues = append(historical.RentValues, TimeSeriesPoint{
						Date:  v.Date,
						Value: v.ZORI,
					})
				}
			}
		}
	}

	// Fetch mortgage rates from FRED
	if s.fred != nil {
		mortgageObs, err := s.fred.GetSeries(ctx, timeseries.SeriesMortgage30US, startDate, endDate)
		if err != nil {
			s.logger.Warn("failed to get mortgage rates", "error", err)
		} else {
			for _, v := range mortgageObs {
				if v.Value == "." {
					continue // Skip missing values
				}
				date, _ := time.Parse("2006-01-02", v.Date)
				var value float64
				fmt.Sscanf(v.Value, "%f", &value)
				historical.MortgageRates = append(historical.MortgageRates, TimeSeriesPoint{
					Date:  date,
					Value: value,
				})
			}
		}

		// Fetch unemployment rates
		unemploymentObs, err := s.fred.GetSeries(ctx, timeseries.SeriesUnemployment, startDate, endDate)
		if err != nil {
			s.logger.Warn("failed to get unemployment rates", "error", err)
		} else {
			historical.UnemploymentRates = make([]TimeSeriesPoint, 0, len(unemploymentObs))
			for _, v := range unemploymentObs {
				if v.Value == "." {
					continue // Skip missing values
				}
				date, _ := time.Parse("2006-01-02", v.Date)
				var value float64
				fmt.Sscanf(v.Value, "%f", &value)
				historical.UnemploymentRates = append(historical.UnemploymentRates, TimeSeriesPoint{
					Date:  date,
					Value: value,
				})
			}
		}
	}

	return historical, nil
}

// CalculateYoYChanges calculates year-over-year changes for all metrics
func (s *Service) CalculateYoYChanges(data *HistoricalData) *YoYChanges {
	changes := &YoYChanges{}

	if len(data.HomeValues) >= 2 {
		// 1-year change
		changes.HomeValueChange = s.calculatePercentChange(
			data.HomeValues,
			1,
		)
		// 3-year change
		changes.HomeValue3Y = s.calculatePercentChange(
			data.HomeValues,
			3,
		)
		// 5-year change
		changes.HomeValue5Y = s.calculatePercentChange(
			data.HomeValues,
			5,
		)
	}

	if len(data.RentValues) >= 2 {
		// 1-year change
		changes.RentChange = s.calculatePercentChange(
			data.RentValues,
			1,
		)
		// 3-year change
		changes.Rent3Y = s.calculatePercentChange(
			data.RentValues,
			3,
		)
		// 5-year change
		changes.Rent5Y = s.calculatePercentChange(
			data.RentValues,
			5,
		)
	}

	if len(data.MortgageRates) >= 2 {
		// Mortgage rate change in basis points
		latestIdx := len(data.MortgageRates) - 1
		yearAgoIdx := s.findYearAgoIndex(data.MortgageRates, 1)
		if yearAgoIdx >= 0 {
			changes.MortgageRateChange = (data.MortgageRates[latestIdx].Value - data.MortgageRates[yearAgoIdx].Value) * 100
		}
	}

	return changes
}

// SynthesizeTrends uses AI to generate trend insights
func (s *Service) SynthesizeTrends(
	ctx context.Context,
	data *HistoricalData,
	changes *YoYChanges,
	location string,
) (*TrendSynthesis, error) {
	if s.ai == nil {
		return nil, fmt.Errorf("AI client not configured")
	}

	prompt := s.buildSynthesisPrompt(data, changes, location)

	content, err := s.ai.Complete(ctx, trendSynthesisSystemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	// Parse the AI response
	synthesis, err := s.parseSynthesisResponse(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	return synthesis, nil
}

// GetChartData retrieves data formatted for chart visualization
func (s *Service) GetChartData(ctx context.Context, req ChartRequest) (*ChartData, error) {
	// Set defaults
	if req.Years <= 0 {
		req.Years = 5
	}

	historical, err := s.GetHistoricalData(ctx, req.Location, req.Years)
	if err != nil {
		return nil, err
	}

	var points []TimeSeriesPoint
	switch req.Metric {
	case "homeValues":
		points = historical.HomeValues
	case "rentValues":
		points = historical.RentValues
	case "mortgageRates":
		points = historical.MortgageRates
	default:
		return nil, fmt.Errorf("unknown metric: %s", req.Metric)
	}

	if len(points) == 0 {
		return nil, fmt.Errorf("no data available for metric: %s", req.Metric)
	}

	// Calculate statistics
	min, max, sum := points[0].Value, points[0].Value, 0.0
	for _, p := range points {
		if p.Value < min {
			min = p.Value
		}
		if p.Value > max {
			max = p.Value
		}
		sum += p.Value
	}
	avg := sum / float64(len(points))

	// Calculate YoY change
	yoyChange := s.calculatePercentChange(points, 1)

	return &ChartData{
		Metric:    req.Metric,
		Location:  req.Location,
		Points:    points,
		YoYChange: yoyChange,
		Min:       min,
		Max:       max,
		Avg:       avg,
	}, nil
}

// Helper functions

func (s *Service) calculatePercentChange(points []TimeSeriesPoint, years int) float64 {
	if len(points) < 2 {
		return 0
	}

	latestIdx := len(points) - 1
	yearAgoIdx := s.findYearAgoIndex(points, years)
	if yearAgoIdx < 0 {
		return 0
	}

	oldValue := points[yearAgoIdx].Value
	newValue := points[latestIdx].Value

	if oldValue == 0 {
		return 0
	}

	return ((newValue - oldValue) / oldValue) * 100
}

func (s *Service) findYearAgoIndex(points []TimeSeriesPoint, years int) int {
	if len(points) == 0 {
		return -1
	}

	targetDate := points[len(points)-1].Date.AddDate(-years, 0, 0)

	// Find closest date to target
	closestIdx := 0
	closestDiff := points[0].Date.Sub(targetDate).Abs()

	for i, p := range points {
		diff := p.Date.Sub(targetDate).Abs()
		if diff < closestDiff {
			closestIdx = i
			closestDiff = diff
		}
	}

	return closestIdx
}

func (s *Service) getCurrentMetrics(data *HistoricalData) *CurrentMetrics {
	metrics := &CurrentMetrics{}

	if len(data.HomeValues) > 0 {
		metrics.MedianHomeValue = int(data.HomeValues[len(data.HomeValues)-1].Value)
	}

	if len(data.RentValues) > 0 {
		metrics.MedianRent = int(data.RentValues[len(data.RentValues)-1].Value)
	}

	if len(data.MortgageRates) > 0 {
		metrics.MortgageRate30Y = data.MortgageRates[len(data.MortgageRates)-1].Value
	}

	// Calculate price-to-rent ratio and gross yield
	if metrics.MedianHomeValue > 0 && metrics.MedianRent > 0 {
		annualRent := float64(metrics.MedianRent * 12)
		metrics.PriceToRentRatio = float64(metrics.MedianHomeValue) / annualRent
		metrics.GrossYield = (annualRent / float64(metrics.MedianHomeValue)) * 100
	}

	return metrics
}

func (s *Service) buildSynthesisPrompt(data *HistoricalData, changes *YoYChanges, location string) string {
	return fmt.Sprintf(`Analyze the following market trends for %s:

YEAR-OVER-YEAR CHANGES:
- Home Value Change: %.1f%%
- Rent Change: %.1f%%
- Mortgage Rate Change: %.0f basis points

MULTI-YEAR CHANGES:
- Home Value 3-Year: %.1f%%
- Home Value 5-Year: %.1f%%
- Rent 3-Year: %.1f%%
- Rent 5-Year: %.1f%%

CURRENT METRICS:
- Latest Home Value: $%d
- Latest Rent: $%d
- Current Mortgage Rate: %.2f%%

DATA POINTS:
- Home Value History: %d data points
- Rent History: %d data points
- Mortgage Rate History: %d data points

Provide a comprehensive trend analysis with insights, market outlook, investment timing assessment, and risk factors.`,
		location,
		changes.HomeValueChange,
		changes.RentChange,
		changes.MortgageRateChange,
		changes.HomeValue3Y,
		changes.HomeValue5Y,
		changes.Rent3Y,
		changes.Rent5Y,
		s.getLatestValue(data.HomeValues),
		s.getLatestValue(data.RentValues),
		s.getLatestValueFloat(data.MortgageRates),
		len(data.HomeValues),
		len(data.RentValues),
		len(data.MortgageRates),
	)
}

func (s *Service) getLatestValue(points []TimeSeriesPoint) int {
	if len(points) == 0 {
		return 0
	}
	return int(points[len(points)-1].Value)
}

func (s *Service) getLatestValueFloat(points []TimeSeriesPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	return points[len(points)-1].Value
}

func (s *Service) parseSynthesisResponse(content string) (*TrendSynthesis, error) {
	// Strip markdown code fences if present (Claude often wraps JSON in ```json ... ```)
	cleaned := strings.TrimSpace(content)
	if strings.HasPrefix(cleaned, "```") {
		// Remove opening fence (```json or ```)
		if idx := strings.Index(cleaned, "\n"); idx >= 0 {
			cleaned = cleaned[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(cleaned, "```"); idx >= 0 {
			cleaned = cleaned[:idx]
		}
		cleaned = strings.TrimSpace(cleaned)
	}

	// Try to parse as JSON
	var synthesis TrendSynthesis
	if err := json.Unmarshal([]byte(cleaned), &synthesis); err == nil {
		return &synthesis, nil
	}

	// Second attempt: extract JSON object from mixed text
	if start := strings.Index(content, "{"); start >= 0 {
		if end := strings.LastIndex(content, "}"); end > start {
			if err := json.Unmarshal([]byte(content[start:end+1]), &synthesis); err == nil {
				return &synthesis, nil
			}
		}
	}

	// Fallback: treat entire response as summary text
	synthesis = TrendSynthesis{
		Summary:          content,
		KeyInsights:      []string{},
		MarketOutlook:    "neutral",
		InvestmentTiming: "neutral",
		RiskFactors:      []string{},
		Confidence:       0.7,
	}

	return &synthesis, nil
}

func (s *Service) checkCache(ctx context.Context, userID, cacheKey string) (*TrendsResult, error) {
	if s.cache == nil {
		return nil, fmt.Errorf("cache not configured")
	}

	fullKey := fmt.Sprintf("market_trends:%s", cacheKey)
	data, err := s.cache.Get(ctx, userID, fullKey)
	if err != nil {
		return nil, err
	}

	var result TrendsResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	now := time.Now()
	result.CachedAt = &now

	return &result, nil
}

func (s *Service) cacheResult(ctx context.Context, userID, cacheKey string, result *TrendsResult) error {
	if s.cache == nil {
		return nil
	}

	fullKey := fmt.Sprintf("market_trends:%s", cacheKey)
	return s.cache.Set(ctx, userID, fullKey, "market_trends", result, 24*time.Hour) // Cache for 24 hours
}

// System prompt for trend synthesis
const trendSynthesisSystemPrompt = `You are a real estate market analyst specializing in trend analysis.
Your task is to analyze market data and provide investment insights.

CRITICAL: Respond with ONLY a valid JSON object. No markdown, no code fences, no explanation text before or after the JSON.

{
  "summary": "2-3 sentence market summary with specific data points",
  "keyInsights": ["insight 1", "insight 2", "insight 3", "insight 4"],
  "marketOutlook": "bullish|bearish|neutral",
  "investmentTiming": "favorable|unfavorable|neutral",
  "riskFactors": ["risk 1", "risk 2", "risk 3"],
  "confidence": 0.0-1.0
}

GUIDELINES:
- Provide 3-5 key insights and 2-4 risk factors
- Use data-driven language: "data indicates", "analysis shows"
- Never use prescriptive language: "you should", "I recommend"
- Base confidence on data quality and completeness
- Consider both short-term and long-term trends
- Identify divergences between home values and rents
- Factor in mortgage rate impacts on affordability
- Reference specific percentage changes and dollar values from the data`
