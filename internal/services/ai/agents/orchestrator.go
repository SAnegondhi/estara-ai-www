package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/ai/prompts"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/market/aggregator"
)

// AnalysisRequest represents a request for market analysis
type AnalysisRequest struct {
	Location    string                 `json:"location"`
	City        string                 `json:"city"`
	State       string                 `json:"state"`
	CacheKey    string                 `json:"cache_key,omitempty"`
	MarketData  map[string]interface{} `json:"market_data,omitempty"`
	UserID      string                 `json:"user_id"`
	SkipCache   bool                   `json:"skip_cache,omitempty"`
}

// AnalysisResult represents the combined analysis result
type AnalysisResult struct {
	Location        string                 `json:"location"`
	MetricsAnalysis map[string]interface{} `json:"metrics_analysis"`
	NarrativeAnalysis map[string]interface{} `json:"narrative_analysis"`
	CombinedReport  map[string]interface{} `json:"combined_report,omitempty"`
	Confidence      float64                `json:"confidence"`
	GeneratedAt     time.Time              `json:"generated_at"`
	CacheKey        string                 `json:"cache_key,omitempty"`
	FromCache       bool                   `json:"from_cache"`
}

// AnalysisEvent represents a streaming event during analysis
type AnalysisEvent struct {
	Type     string      `json:"type"`     // "progress", "metrics", "narrative", "combined", "complete", "error"
	Stage    string      `json:"stage"`    // Current stage name
	Progress float64     `json:"progress"` // 0-100
	Data     interface{} `json:"data,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// DualAgentOrchestrator coordinates metrics and narrative agents
type DualAgentOrchestrator struct {
	client     *anthropic.Client
	cache      *cache.HybridCache
	market     *aggregator.Aggregator
	cacheTTL   time.Duration
	logger     *slog.Logger
}

// OrchestratorConfig holds configuration for the orchestrator
type OrchestratorConfig struct {
	CacheTTL time.Duration
}

// NewDualAgentOrchestrator creates a new orchestrator
func NewDualAgentOrchestrator(
	client *anthropic.Client,
	cache *cache.HybridCache,
	market *aggregator.Aggregator,
	cfg OrchestratorConfig,
) *DualAgentOrchestrator {
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = time.Hour * 24
	}

	return &DualAgentOrchestrator{
		client:   client,
		cache:    cache,
		market:   market,
		cacheTTL: cfg.CacheTTL,
		logger:   slog.Default().With("component", "dual_agent_orchestrator"),
	}
}

// Analyze runs both agents and combines results
func (o *DualAgentOrchestrator) Analyze(ctx context.Context, req AnalysisRequest) (*AnalysisResult, error) {
	startTime := time.Now()

	// Check cache first
	if !req.SkipCache && req.CacheKey != "" {
		if cached, err := o.getFromCache(ctx, req.UserID, req.CacheKey); err == nil && cached != nil {
			o.logger.Info("returning cached analysis",
				"location", req.Location,
				"cache_key", req.CacheKey,
			)
			return cached, nil
		}
	}

	// Get market data if not provided
	marketData := req.MarketData
	if marketData == nil && o.market != nil {
		data, err := o.market.GetMarketData(ctx, req.City, req.State)
		if err != nil {
			o.logger.Warn("failed to fetch market data, proceeding with limited analysis",
				"error", err,
			)
		} else {
			marketData = marketDataToMap(data)
		}
	}

	// Run metrics analysis
	metricsResult, err := o.runMetricsAgent(ctx, req.Location, marketData)
	if err != nil {
		return nil, fmt.Errorf("metrics analysis failed: %w", err)
	}

	// Run narrative analysis
	narrativeResult, err := o.runNarrativeAgent(ctx, req.Location, metricsResult)
	if err != nil {
		return nil, fmt.Errorf("narrative analysis failed: %w", err)
	}

	// Calculate confidence
	confidence := calculateConfidence(metricsResult, narrativeResult)

	result := &AnalysisResult{
		Location:          req.Location,
		MetricsAnalysis:   metricsResult,
		NarrativeAnalysis: narrativeResult,
		Confidence:        confidence,
		GeneratedAt:       time.Now(),
		CacheKey:          req.CacheKey,
		FromCache:         false,
	}

	// Cache the result
	if req.CacheKey != "" {
		if err := o.saveToCache(ctx, req.UserID, req.CacheKey, result); err != nil {
			o.logger.Warn("failed to cache analysis result", "error", err)
		}
	}

	o.logger.Info("analysis completed",
		"location", req.Location,
		"duration", time.Since(startTime),
		"confidence", confidence,
	)

	return result, nil
}

// Stream runs analysis with real-time progress updates
func (o *DualAgentOrchestrator) Stream(ctx context.Context, req AnalysisRequest, events chan<- AnalysisEvent) error {
	defer close(events)

	startTime := time.Now()

	// Progress: Starting
	sendEvent(events, AnalysisEvent{
		Type:     "progress",
		Stage:    "initializing",
		Progress: 0,
	})

	// Check cache first
	if !req.SkipCache && req.CacheKey != "" {
		if cached, err := o.getFromCache(ctx, req.UserID, req.CacheKey); err == nil && cached != nil {
			sendEvent(events, AnalysisEvent{
				Type:     "complete",
				Stage:    "cached",
				Progress: 100,
				Data:     cached,
			})
			return nil
		}
	}

	// Progress: Fetching market data
	sendEvent(events, AnalysisEvent{
		Type:     "progress",
		Stage:    "fetching_market_data",
		Progress: 10,
	})

	marketData := req.MarketData
	if marketData == nil && o.market != nil {
		data, err := o.market.GetMarketData(ctx, req.City, req.State)
		if err != nil {
			o.logger.Warn("failed to fetch market data", "error", err)
		} else {
			marketData = marketDataToMap(data)
		}
	}

	// Progress: Running metrics agent
	sendEvent(events, AnalysisEvent{
		Type:     "progress",
		Stage:    "metrics_analysis",
		Progress: 25,
	})

	metricsResult, err := o.runMetricsAgentStream(ctx, req.Location, marketData, events)
	if err != nil {
		sendEvent(events, AnalysisEvent{
			Type:  "error",
			Stage: "metrics_analysis",
			Error: err.Error(),
		})
		return err
	}

	sendEvent(events, AnalysisEvent{
		Type:     "metrics",
		Stage:    "metrics_complete",
		Progress: 50,
		Data:     metricsResult,
	})

	// Progress: Running narrative agent
	sendEvent(events, AnalysisEvent{
		Type:     "progress",
		Stage:    "narrative_analysis",
		Progress: 55,
	})

	narrativeResult, err := o.runNarrativeAgentStream(ctx, req.Location, metricsResult, events)
	if err != nil {
		sendEvent(events, AnalysisEvent{
			Type:  "error",
			Stage: "narrative_analysis",
			Error: err.Error(),
		})
		return err
	}

	sendEvent(events, AnalysisEvent{
		Type:     "narrative",
		Stage:    "narrative_complete",
		Progress: 85,
		Data:     narrativeResult,
	})

	// Progress: Finalizing
	sendEvent(events, AnalysisEvent{
		Type:     "progress",
		Stage:    "finalizing",
		Progress: 90,
	})

	confidence := calculateConfidence(metricsResult, narrativeResult)

	result := &AnalysisResult{
		Location:          req.Location,
		MetricsAnalysis:   metricsResult,
		NarrativeAnalysis: narrativeResult,
		Confidence:        confidence,
		GeneratedAt:       time.Now(),
		CacheKey:          req.CacheKey,
		FromCache:         false,
	}

	// Cache the result
	if req.CacheKey != "" {
		if err := o.saveToCache(ctx, req.UserID, req.CacheKey, result); err != nil {
			o.logger.Warn("failed to cache analysis result", "error", err)
		}
	}

	// Send complete event
	sendEvent(events, AnalysisEvent{
		Type:     "complete",
		Stage:    "complete",
		Progress: 100,
		Data:     result,
	})

	o.logger.Info("streaming analysis completed",
		"location", req.Location,
		"duration", time.Since(startTime),
	)

	return nil
}

// AnalyzeParallel runs both agents in parallel for faster results
func (o *DualAgentOrchestrator) AnalyzeParallel(ctx context.Context, req AnalysisRequest) (*AnalysisResult, error) {
	startTime := time.Now()

	// Check cache first
	if !req.SkipCache && req.CacheKey != "" {
		if cached, err := o.getFromCache(ctx, req.UserID, req.CacheKey); err == nil && cached != nil {
			return cached, nil
		}
	}

	// Get market data if not provided
	marketData := req.MarketData
	if marketData == nil && o.market != nil {
		data, err := o.market.GetMarketData(ctx, req.City, req.State)
		if err != nil {
			o.logger.Warn("failed to fetch market data", "error", err)
		} else {
			marketData = marketDataToMap(data)
		}
	}

	// Run both agents in parallel
	var wg sync.WaitGroup
	var metricsResult, narrativeResult map[string]interface{}
	var metricsErr, narrativeErr error

	wg.Add(2)

	go func() {
		defer wg.Done()
		metricsResult, metricsErr = o.runMetricsAgent(ctx, req.Location, marketData)
	}()

	go func() {
		defer wg.Done()
		// For parallel execution, narrative agent uses market data directly
		// rather than waiting for metrics result
		narrativeResult, narrativeErr = o.runNarrativeAgentFromMarketData(ctx, req.Location, marketData)
	}()

	wg.Wait()

	if metricsErr != nil {
		return nil, fmt.Errorf("metrics analysis failed: %w", metricsErr)
	}
	if narrativeErr != nil {
		return nil, fmt.Errorf("narrative analysis failed: %w", narrativeErr)
	}

	confidence := calculateConfidence(metricsResult, narrativeResult)

	result := &AnalysisResult{
		Location:          req.Location,
		MetricsAnalysis:   metricsResult,
		NarrativeAnalysis: narrativeResult,
		Confidence:        confidence,
		GeneratedAt:       time.Now(),
		CacheKey:          req.CacheKey,
		FromCache:         false,
	}

	// Cache the result
	if req.CacheKey != "" {
		if err := o.saveToCache(ctx, req.UserID, req.CacheKey, result); err != nil {
			o.logger.Warn("failed to cache analysis result", "error", err)
		}
	}

	o.logger.Info("parallel analysis completed",
		"location", req.Location,
		"duration", time.Since(startTime),
	)

	return result, nil
}

// runMetricsAgent executes the metrics analysis agent
func (o *DualAgentOrchestrator) runMetricsAgent(ctx context.Context, location string, marketData map[string]interface{}) (map[string]interface{}, error) {
	userPrompt := prompts.BuildMetricsUserPrompt(location, marketData)

	response, err := o.client.Complete(ctx, prompts.MetricsAgentSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}

	return parseJSONResponse(response)
}

// runMetricsAgentStream executes metrics agent with streaming
func (o *DualAgentOrchestrator) runMetricsAgentStream(ctx context.Context, location string, marketData map[string]interface{}, events chan<- AnalysisEvent) (map[string]interface{}, error) {
	userPrompt := prompts.BuildMetricsUserPrompt(location, marketData)

	streamEvents, err := o.client.Stream(ctx, prompts.MetricsAgentSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}

	// Collect streamed response
	response, _, err := anthropic.CollectStreamText(streamEvents)
	if err != nil {
		return nil, err
	}

	return parseJSONResponse(response)
}

// runNarrativeAgent executes the narrative analysis agent
func (o *DualAgentOrchestrator) runNarrativeAgent(ctx context.Context, location string, metricsAnalysis map[string]interface{}) (map[string]interface{}, error) {
	metricsJSON, err := json.Marshal(metricsAnalysis)
	if err != nil {
		return nil, err
	}

	userPrompt := prompts.BuildNarrativeUserPrompt(location, string(metricsJSON))

	response, err := o.client.Complete(ctx, prompts.NarrativeAgentSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}

	return parseJSONResponse(response)
}

// runNarrativeAgentStream executes narrative agent with streaming
func (o *DualAgentOrchestrator) runNarrativeAgentStream(ctx context.Context, location string, metricsAnalysis map[string]interface{}, events chan<- AnalysisEvent) (map[string]interface{}, error) {
	metricsJSON, err := json.Marshal(metricsAnalysis)
	if err != nil {
		return nil, err
	}

	userPrompt := prompts.BuildNarrativeUserPrompt(location, string(metricsJSON))

	streamEvents, err := o.client.Stream(ctx, prompts.NarrativeAgentSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}

	response, _, err := anthropic.CollectStreamText(streamEvents)
	if err != nil {
		return nil, err
	}

	return parseJSONResponse(response)
}

// runNarrativeAgentFromMarketData runs narrative agent directly from market data (for parallel execution)
func (o *DualAgentOrchestrator) runNarrativeAgentFromMarketData(ctx context.Context, location string, marketData map[string]interface{}) (map[string]interface{}, error) {
	marketJSON, err := json.Marshal(marketData)
	if err != nil {
		return nil, err
	}

	userPrompt := prompts.BuildNarrativeUserPrompt(location, string(marketJSON))

	response, err := o.client.Complete(ctx, prompts.NarrativeAgentSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}

	return parseJSONResponse(response)
}

// getFromCache retrieves a cached analysis result
func (o *DualAgentOrchestrator) getFromCache(ctx context.Context, userID, key string) (*AnalysisResult, error) {
	if o.cache == nil {
		return nil, nil
	}

	data, err := o.cache.Get(ctx, userID, "analysis:"+key)
	if err != nil {
		return nil, err
	}

	var result AnalysisResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	result.FromCache = true
	return &result, nil
}

// saveToCache stores an analysis result in cache
func (o *DualAgentOrchestrator) saveToCache(ctx context.Context, userID, key string, result *AnalysisResult) error {
	if o.cache == nil {
		return nil
	}

	data, err := json.Marshal(result)
	if err != nil {
		return err
	}

	return o.cache.Set(ctx, userID, "analysis:"+key, "market_analysis", data, o.cacheTTL)
}

// Helper functions

func sendEvent(events chan<- AnalysisEvent, event AnalysisEvent) {
	select {
	case events <- event:
	default:
		// Channel full, skip
	}
}

func parseJSONResponse(response string) (map[string]interface{}, error) {
	// Try to extract JSON from the response
	// The response might have markdown code blocks or other text
	jsonStr := extractJSON(response)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		// If JSON parsing fails, wrap the raw response
		return map[string]interface{}{
			"raw_response": response,
			"parse_error":  err.Error(),
		}, nil
	}

	return result, nil
}

func extractJSON(s string) string {
	// Find JSON object in the response
	start := -1
	end := -1
	depth := 0

	for i, c := range s {
		if c == '{' {
			if start == -1 {
				start = i
			}
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 && start != -1 {
				end = i + 1
				break
			}
		}
	}

	if start != -1 && end != -1 {
		return s[start:end]
	}

	return s
}

func calculateConfidence(metrics, narrative map[string]interface{}) float64 {
	confidence := 0.7 // Base confidence

	// Boost confidence if metrics analysis includes key indicators
	if metrics != nil {
		if _, ok := metrics["summary"]; ok {
			confidence += 0.1
		}
		if _, ok := metrics["key_metrics"]; ok {
			confidence += 0.05
		}
	}

	// Boost confidence if narrative analysis is complete
	if narrative != nil {
		if _, ok := narrative["executive_summary"]; ok {
			confidence += 0.1
		}
		if _, ok := narrative["market_narrative"]; ok {
			confidence += 0.05
		}
	}

	// Cap at 0.95
	if confidence > 0.95 {
		confidence = 0.95
	}

	return confidence
}

func marketDataToMap(data *aggregator.MarketData) map[string]interface{} {
	if data == nil {
		return nil
	}

	// Calculate price to rent ratio
	var priceToRent float64
	if data.MedianRent > 0 {
		priceToRent = float64(data.MedianHomePrice) / float64(data.MedianRent*12)
	}

	return map[string]interface{}{
		"median_home_price": data.MedianHomePrice,
		"median_rent":       data.MedianRent,
		"price_to_rent":     priceToRent,
		"mortgage_rate":     data.MortgageRate30,
		"cap_rate":          data.CapRate,
		"yoy_change":        data.YearOverYearPct,
		"rent_yoy_change":   data.RentYearOverYear,
		"data_date":         data.DataDate,
		"confidence":        data.Confidence,
		"sources":           data.Sources,
		"is_ai_estimated":   data.IsAIEstimated,
	}
}
