package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/ai/agents"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/analysis"
	"github.com/estara-ai/www/internal/services/market/trends"
)

// MarketAnalysisWorker processes market analysis jobs
type MarketAnalysisWorker struct {
	orchestrator *agents.DualAgentOrchestrator
	market       *aggregator.Aggregator
	cache        *cache.HybridCache
	trends       *trends.Service
	mainDB       *postgres.Pool
	store        *dbstore.Store // ADR-087: For updating market_analysis_reports
	logger       *slog.Logger
}

// MarketAnalysisWorkerConfig holds configuration for the worker
type MarketAnalysisWorkerConfig struct {
	Orchestrator *agents.DualAgentOrchestrator
	Market       *aggregator.Aggregator
	Cache        *cache.HybridCache
	Trends       *trends.Service
	MainDB       *postgres.Pool
	Store        *dbstore.Store // ADR-087: For report tracking
}

// NewMarketAnalysisWorker creates a new market analysis worker
func NewMarketAnalysisWorker(cfg MarketAnalysisWorkerConfig) *MarketAnalysisWorker {
	return &MarketAnalysisWorker{
		orchestrator: cfg.Orchestrator,
		market:       cfg.Market,
		cache:        cfg.Cache,
		trends:       cfg.Trends,
		mainDB:       cfg.MainDB,
		store:        cfg.Store, // ADR-087
		logger:       slog.Default().With("component", "market_analysis_worker"),
	}
}

// GetHandler returns the job handler function
func (w *MarketAnalysisWorker) GetHandler() queue.JobHandler {
	return func(ctx context.Context, job *queue.Job, progress chan<- queue.ProgressEvent) (*queue.JobResult, error) {
		return w.Process(ctx, job, progress)
	}
}

// Process executes a market analysis job
func (w *MarketAnalysisWorker) Process(
	ctx context.Context,
	job *queue.Job,
	progress chan<- queue.ProgressEvent,
) (*queue.JobResult, error) {
	startTime := time.Now()
	w.logger.Info("processing market analysis job",
		"job_id", job.ID,
		"user_id", job.UserID,
	)

	// Parse job parameters
	req, err := w.parseJobParams(job)
	if err != nil {
		return w.failedResult(job, fmt.Errorf("invalid job parameters: %w", err))
	}

	// Check cache first (unless force refresh)
	if !req.ForceRefresh && req.CacheKey != "" {
		if cached, err := w.checkCache(ctx, job.UserID, req.CacheKey); err == nil && cached != nil {
			w.logger.Info("returning cached analysis",
				"job_id", job.ID,
				"cache_key", req.CacheKey,
			)
			w.reportProgress(progress, job.ID, 100, "Retrieved from cache")
			return &queue.JobResult{
				JobID:       job.ID,
				Status:      queue.JobStatusCompleted,
				Data:        w.resultToMap(cached),
				Duration:    time.Since(startTime),
				CompletedAt: time.Now(),
			}, nil
		}
	}

	// Report initial progress
	w.reportProgress(progress, job.ID, 5, "Starting market analysis")

	// Step 1: Fetch market data (20%)
	w.reportProgress(progress, job.ID, 10, "Fetching market data")
	marketData, err := w.fetchMarketData(ctx, req.Location)
	if err != nil {
		w.logger.Warn("failed to fetch market data, using provided data", "error", err)
		// Use provided market data if available
		if req.MarketData != nil {
			marketData = w.convertMarketData(req.MarketData)
		}
	}

	// Route to V2 pipeline if available (ADR-074)
	if w.orchestrator.HasV2() {
		return w.processV2(ctx, job, progress, req, marketData, startTime)
	}

	// V1 fallback: dual-agent pipeline
	// Step 2: Build analysis context (30%)
	w.reportProgress(progress, job.ID, 25, "Building analysis context")
	analysisCtx := w.buildAnalysisContext(req.Location, marketData)

	// Step 3: Run dual-agent analysis (80%)
	w.reportProgress(progress, job.ID, 35, "Running metrics analysis")

	// Create analysis request for orchestrator
	orchReq := agents.AnalysisRequest{
		Location:   req.Location,
		CacheKey:   req.CacheKey,
		MarketData: w.contextToMap(analysisCtx),
	}

	// Create a channel to receive streaming events
	eventCh := make(chan agents.AnalysisEvent, 100)
	var analysisResult *agents.AnalysisResult
	var analysisErr error

	// Run analysis with streaming
	go func() {
		analysisResult, analysisErr = w.orchestrator.Analyze(ctx, orchReq)
		close(eventCh)
	}()

	// Forward events to progress channel
	for event := range eventCh {
		switch event.Type {
		case agents.AnalysisEventMetrics:
			w.reportProgress(progress, job.ID, 50, "Metrics analysis complete")
		case agents.AnalysisEventNarrative:
			w.reportProgress(progress, job.ID, 70, "Narrative analysis complete")
		case agents.AnalysisEventCombined:
			w.reportProgress(progress, job.ID, 85, "Combining insights")
		}
	}

	if analysisErr != nil {
		return w.failedResult(job, fmt.Errorf("analysis failed: %w", analysisErr))
	}

	// Step 4: Build result (90%)
	w.reportProgress(progress, job.ID, 90, "Finalizing results")

	// Convert map analysis results to JSON strings for storage
	metricsJSON, _ := json.Marshal(analysisResult.MetricsAnalysis)
	narrativeJSON, _ := json.Marshal(analysisResult.NarrativeAnalysis)
	combinedJSON, _ := json.Marshal(analysisResult.CombinedReport)

	result := &agents.MarketAnalysisResult{
		Location:          req.Location,
		MetricsAnalysis:   string(metricsJSON),
		NarrativeAnalysis: string(narrativeJSON),
		CombinedInsight:   string(combinedJSON),
		MarketData:        *marketData,
		Confidence:        analysisResult.Confidence,
		GeneratedAt:       time.Now(),
	}

	// Step 5: Cache result (95%)
	w.reportProgress(progress, job.ID, 95, "Caching results")
	if req.CacheKey != "" {
		if err := w.cacheResult(ctx, job.UserID, req.CacheKey, result); err != nil {
			w.logger.Warn("failed to cache result", "error", err)
		}
	}

	// Complete
	w.reportProgress(progress, job.ID, 100, "Market analysis complete")

	duration := time.Since(startTime)
	w.logger.Info("market analysis job complete",
		"job_id", job.ID,
		"duration_ms", duration.Milliseconds(),
		"location", req.Location,
		"confidence", result.Confidence,
	)

	// ADR-087: Update report status to completed
	w.updateReportStatus(ctx, req.Location, "completed", "", result)

	// Background: generate trends for the same location
	w.backgroundGenerateTrends(job.UserID, req.Location)

	return &queue.JobResult{
		JobID:       job.ID,
		Status:      queue.JobStatusCompleted,
		Data:        w.resultToMap(result),
		Duration:    duration,
		CompletedAt: time.Now(),
	}, nil
}

// parseJobParams extracts analysis parameters from job payload
func (w *MarketAnalysisWorker) parseJobParams(job *queue.Job) (*agents.MarketAnalysisRequest, error) {
	req := &agents.MarketAnalysisRequest{}

	if location, ok := job.Payload["location"].(string); ok {
		req.Location = location
	} else {
		return nil, fmt.Errorf("location not specified")
	}

	if cacheKey, ok := job.Payload["cacheKey"].(string); ok {
		req.CacheKey = cacheKey
	}

	if forceRefresh, ok := job.Payload["forceRefresh"].(bool); ok {
		req.ForceRefresh = forceRefresh
	}

	if marketData, ok := job.Payload["marketData"].(map[string]interface{}); ok {
		req.MarketData = marketData
	}

	return req, nil
}

// fetchMarketData retrieves market data from the aggregator
func (w *MarketAnalysisWorker) fetchMarketData(ctx context.Context, location string) (*agents.MarketDataSummary, error) {
	if w.market == nil {
		return &agents.MarketDataSummary{}, fmt.Errorf("market aggregator not configured")
	}

	// Parse location
	parts := strings.Split(location, ",")
	city := strings.TrimSpace(parts[0])
	state := ""
	if len(parts) > 1 {
		state = strings.TrimSpace(parts[1])
	}

	// Fetch from aggregator
	data, err := w.market.GetMarketData(ctx, city, state)
	if err != nil {
		return nil, err
	}

	// Convert to summary
	summary := &agents.MarketDataSummary{
		MedianHomePrice: data.MedianHomePrice,
		MedianRent:      data.MedianRent,
		PriceChange1Y:   data.YearOverYearPct,
		RentChange1Y:    data.RentYearOverYear,
		MortgageRate:    data.MortgageRate30,
	}

	// Set default inventory level based on confidence
	if data.Confidence >= 0.8 {
		summary.InventoryLevel = "normal"
	} else {
		summary.InventoryLevel = "unknown"
	}

	return summary, nil
}

// convertMarketData converts map to MarketDataSummary
func (w *MarketAnalysisWorker) convertMarketData(data map[string]interface{}) *agents.MarketDataSummary {
	summary := &agents.MarketDataSummary{}

	if v, ok := data["medianHomePrice"].(float64); ok {
		summary.MedianHomePrice = int(v)
	}
	if v, ok := data["medianRent"].(float64); ok {
		summary.MedianRent = int(v)
	}
	if v, ok := data["priceChange1Y"].(float64); ok {
		summary.PriceChange1Y = v
	}
	if v, ok := data["rentChange1Y"].(float64); ok {
		summary.RentChange1Y = v
	}
	if v, ok := data["mortgageRate"].(float64); ok {
		summary.MortgageRate = v
	}
	if v, ok := data["daysOnMarket"].(float64); ok {
		summary.DaysOnMarket = int(v)
	}
	if v, ok := data["inventoryLevel"].(string); ok {
		summary.InventoryLevel = v
	}

	return summary
}

// buildAnalysisContext creates context for AI analysis
func (w *MarketAnalysisWorker) buildAnalysisContext(location string, marketData *agents.MarketDataSummary) *agents.AnalysisContext {
	parts := strings.Split(location, ",")
	city := strings.TrimSpace(parts[0])
	state := ""
	if len(parts) > 1 {
		state = strings.TrimSpace(parts[1])
	}

	ctx := &agents.AnalysisContext{
		Location: location,
		City:     city,
		State:    state,
	}

	if marketData != nil {
		ctx.MarketData = *marketData
	}

	return ctx
}

// contextToMap converts AnalysisContext to map for orchestrator
func (w *MarketAnalysisWorker) contextToMap(ctx *agents.AnalysisContext) map[string]interface{} {
	return map[string]interface{}{
		"location":        ctx.Location,
		"city":            ctx.City,
		"state":           ctx.State,
		"medianHomePrice": ctx.MarketData.MedianHomePrice,
		"medianRent":      ctx.MarketData.MedianRent,
		"priceChange1Y":   ctx.MarketData.PriceChange1Y,
		"rentChange1Y":    ctx.MarketData.RentChange1Y,
		"mortgageRate":    ctx.MarketData.MortgageRate,
		"daysOnMarket":    ctx.MarketData.DaysOnMarket,
		"inventoryLevel":  ctx.MarketData.InventoryLevel,
	}
}

// checkCache checks for cached analysis result
func (w *MarketAnalysisWorker) checkCache(ctx context.Context, userID, cacheKey string) (*agents.MarketAnalysisResult, error) {
	if w.cache == nil {
		return nil, fmt.Errorf("cache not configured")
	}

	// Try V2 key format first (plain cacheKey, used by SetAnalysisReport)
	data, err := w.cache.Get(ctx, userID, cacheKey)
	if err != nil {
		// Fallback: try V1 key format (prefixed with market_analysis:{userID}:)
		fullKey := fmt.Sprintf("market_analysis:%s:%s", userID, cacheKey)
		data, err = w.cache.Get(ctx, userID, fullKey)
		if err != nil {
			return nil, err
		}
	}

	var result agents.MarketAnalysisResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	now := time.Now()
	result.CachedAt = &now

	return &result, nil
}

// cacheResult stores the result in the hybrid cache with dynamic TTL (ADR-087)
func (w *MarketAnalysisWorker) cacheResult(
	ctx context.Context,
	userID string,
	cacheKey string,
	result *agents.MarketAnalysisResult,
) error {
	if w.cache == nil {
		return nil
	}

	// ADR-087: Calculate dynamic TTL based on data freshness
	// Use standard data sources as we don't track individual timestamps yet
	ttl := analysis.CalculateCacheTTL(analysis.GetStandardDataSources())

	w.logger.Info("caching market analysis with dynamic TTL",
		"cache_key", cacheKey,
		"ttl_hours", ttl.Hours(),
	)

	fullKey := fmt.Sprintf("market_analysis:%s:%s", userID, cacheKey)
	return w.cache.Set(ctx, userID, fullKey, "market_analysis", result, ttl)
}

// reportProgress sends a progress event
func (w *MarketAnalysisWorker) reportProgress(
	progress chan<- queue.ProgressEvent,
	jobID string,
	percent float64,
	message string,
) {
	if progress == nil {
		return
	}

	select {
	case progress <- queue.ProgressEvent{
		JobID:    jobID,
		Progress: percent,
		Stage:    "processing",
		Message:  message,
	}:
	default:
		// Channel full, skip
	}
}

// failedResult creates a failed job result
func (w *MarketAnalysisWorker) failedResult(job *queue.Job, err error) (*queue.JobResult, error) {
	w.logger.Error("market analysis job failed",
		"job_id", job.ID,
		"error", err,
	)

	// ADR-087: Update report status to failed
	if location, ok := job.Payload["location"].(string); ok {
		w.updateReportStatus(context.Background(), location, "failed", err.Error(), nil)
	}

	return &queue.JobResult{
		JobID:       job.ID,
		Status:      queue.JobStatusFailed,
		Error:       err.Error(),
		CompletedAt: time.Now(),
	}, err
}

// processV2 runs the V2 data-driven analysis pipeline (ADR-074)
func (w *MarketAnalysisWorker) processV2(
	ctx context.Context,
	job *queue.Job,
	progress chan<- queue.ProgressEvent,
	req *agents.MarketAnalysisRequest,
	marketData *agents.MarketDataSummary,
	startTime time.Time,
) (*queue.JobResult, error) {
	// Step 2: Fetch market context (10% — usually cached, near-instant)
	w.reportProgress(progress, job.ID, 10, "Fetching market context")

	// Parse location for V2 request
	parts := strings.Split(req.Location, ",")
	city := strings.TrimSpace(parts[0])
	state := ""
	if len(parts) > 1 {
		state = strings.TrimSpace(parts[1])
	}

	orchReq := agents.AnalysisRequest{
		Location: req.Location,
		City:     city,
		State:    state,
		CacheKey: req.CacheKey,
		UserID:   job.UserID,
	}

	// Step 3: Build data payload (30% — parallel fetches from existing services)
	w.reportProgress(progress, job.ID, 30, "Building data payload")

	// Step 4: Generate analysis (50% — Sonnet 12K, the longest step)
	w.reportProgress(progress, job.ID, 50, "Generating analysis")

	v2Result, err := w.orchestrator.AnalyzeV2(ctx, orchReq)
	if err != nil {
		return w.failedResult(job, fmt.Errorf("V2 analysis failed: %w", err))
	}

	// Step 5: Finalize (90%)
	w.reportProgress(progress, job.ID, 90, "Finalizing and caching")

	// Cache V2 report in analysis_cache with fullReport column
	if w.cache != nil && req.CacheKey != "" {
		// ADR-087: Calculate dynamic TTL based on data freshness
		ttl := analysis.CalculateCacheTTL(analysis.GetStandardDataSources())

		w.logger.Info("caching V2 report with dynamic TTL",
			"cache_key", req.CacheKey,
			"user_id", job.UserID,
			"report_len", len(v2Result.FullReport),
			"ttl_hours", ttl.Hours(),
		)
		if err := w.cache.SetAnalysisReport(ctx, job.UserID, req.CacheKey, v2Result, cache.AnalysisReportCacheOptions{
			Location:   req.Location,
			FullReport: v2Result.FullReport,
		}, ttl); err != nil {
			w.logger.Error("failed to cache V2 report", "error", err, "cache_key", req.CacheKey)
		}
	} else {
		w.logger.Warn("skipping V2 report cache",
			"cache_nil", w.cache == nil,
			"cache_key_empty", req.CacheKey == "",
		)
	}

	// Build result compatible with existing job result format
	// V2 puts markdown in fullReport/narrativeAnalysis for display
	result := &agents.MarketAnalysisResult{
		Location:          req.Location,
		NarrativeAnalysis: v2Result.FullReport, // Markdown report
		Confidence:        float64(v2Result.DataCompleteness) / 100.0,
		GeneratedAt:       v2Result.GeneratedAt,
	}
	if marketData != nil {
		result.MarketData = *marketData
	}

	// Complete
	w.reportProgress(progress, job.ID, 100, "Market analysis complete")

	duration := time.Since(startTime)
	w.logger.Info("V2 market analysis job complete",
		"job_id", job.ID,
		"duration_ms", duration.Milliseconds(),
		"location", req.Location,
		"data_completeness", v2Result.DataCompleteness,
		"report_length", len(v2Result.FullReport),
	)

	// ADR-087: Update report status to completed
	w.updateReportStatus(ctx, req.Location, "completed", "", result)

	// Background: generate trends for the same location
	w.backgroundGenerateTrends(job.UserID, req.Location)

	return &queue.JobResult{
		JobID:       job.ID,
		Status:      queue.JobStatusCompleted,
		Data:        w.resultToMap(result),
		Duration:    duration,
		CompletedAt: time.Now(),
	}, nil
}

// backgroundGenerateTrends fires a goroutine to generate and cache trends for the same location
func (w *MarketAnalysisWorker) backgroundGenerateTrends(userID, location string) {
	if w.trends == nil || w.mainDB == nil {
		return
	}

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		cacheKey := "trends_" + normalizeTrendLocation(location)

		w.logger.Info("background trend generation started",
			"user_id", userID,
			"location", location,
			"cache_key", cacheKey,
		)

		result, err := w.trends.GetTrends(bgCtx, trends.TrendsRequest{
			Location:  location,
			Years:     5,
			IncludeAI: true,
		})
		if err != nil {
			w.logger.Warn("background trend generation failed", "error", err, "location", location)
			return
		}

		// Store in analysis_cache for history
		data, err := json.Marshal(result)
		if err != nil {
			w.logger.Warn("failed to marshal background trend result", "error", err)
			return
		}

		var fullReport string
		if result.Synthesis != nil {
			fullReport = result.Synthesis.Summary
		}

		expiresAt := time.Now().Add(7 * 24 * time.Hour) // 7-day TTL

		_, err = w.mainDB.Exec(bgCtx, `
			INSERT INTO analysis_cache (
				id, key, "userId", location, feature, content, "fullReport",
				"expiresAt", "createdAt", "lastAccessedAt", metadata
			) VALUES ($1, $2, $3, $4, 'market_trends', $5, $6, $7, NOW(), NOW(), '{}')
			ON CONFLICT (key) DO UPDATE SET
				content = EXCLUDED.content,
				"fullReport" = EXCLUDED."fullReport",
				"expiresAt" = EXCLUDED."expiresAt",
				"lastAccessedAt" = NOW()
		`,
			fmt.Sprintf("trends_%s_%d", normalizeTrendLocation(location), time.Now().Unix()),
			cacheKey,
			userID,
			location,
			string(data),
			fullReport,
			expiresAt,
		)
		if err != nil {
			w.logger.Warn("failed to store background trend in cache", "error", err, "location", location)
			return
		}

		w.logger.Info("background trend generation complete", "location", location, "cache_key", cacheKey)
	}()
}

// normalizeTrendLocation creates a cache-friendly key from a location string
func normalizeTrendLocation(loc string) string {
	s := strings.ToLower(strings.TrimSpace(loc))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, ",", "_")
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	s = strings.Trim(s, "_")
	return s
}

// resultToMap converts MarketAnalysisResult to a map for JobResult.Data
func (w *MarketAnalysisWorker) resultToMap(result *agents.MarketAnalysisResult) map[string]interface{} {
	return map[string]interface{}{
		"location":          result.Location,
		"metricsAnalysis":   result.MetricsAnalysis,
		"narrativeAnalysis": result.NarrativeAnalysis,
		"combinedInsight":   result.CombinedInsight,
		"marketData":        result.MarketData,
		"confidence":        result.Confidence,
		"generatedAt":       result.GeneratedAt,
		"cachedAt":          result.CachedAt,
	}
}

// updateReportStatus updates the market_analysis_reports table status (ADR-087)
func (w *MarketAnalysisWorker) updateReportStatus(
	ctx context.Context,
	location string,
	status string,
	errorMessage string,
	result *agents.MarketAnalysisResult,
) {
	if w.store == nil {
		return // Store not configured
	}

	// Normalize location for cache key
	locationNormalized := strings.ToLower(strings.ReplaceAll(location, " ", "_"))
	locationNormalized = strings.ReplaceAll(locationNormalized, ",", "")
	cacheKey := fmt.Sprintf("market_analysis:%s", locationNormalized)

	// Calculate metadata
	var dataFreshnessDate *time.Time
	var reportSizeBytes *int32
	if result != nil {
		// Use generated time as data freshness date
		dataFreshnessDate = &result.GeneratedAt

		// Calculate approximate report size
		size := len(result.MetricsAnalysis) + len(result.NarrativeAnalysis) + len(result.CombinedInsight)
		size32 := int32(size)
		reportSizeBytes = &size32
	}

	// Update report status
	if dataFreshnessDate != nil && reportSizeBytes != nil {
		err := w.store.Q().UpdateReportStatusWithMetadata(ctx, queries.UpdateReportStatusWithMetadataParams{
			CacheKey:           cacheKey,
			Status:             status,
			ErrorMessage:       pgtype.Text{String: errorMessage, Valid: errorMessage != ""},
			DataFreshnessDate:  pgtype.Timestamptz{Time: *dataFreshnessDate, Valid: true},
			ReportSizeBytes:    pgtype.Int4{Int32: *reportSizeBytes, Valid: true},
		})
		if err != nil {
			w.logger.Warn("failed to update report status with metadata",
				"error", err,
				"cache_key", cacheKey,
				"status", status,
			)
		}
	} else {
		err := w.store.Q().UpdateReportStatus(ctx, queries.UpdateReportStatusParams{
			CacheKey:     cacheKey,
			Status:       status,
			ErrorMessage: pgtype.Text{String: errorMessage, Valid: errorMessage != ""},
		})
		if err != nil {
			w.logger.Warn("failed to update report status",
				"error", err,
				"cache_key", cacheKey,
				"status", status,
			)
		}
	}

	w.logger.Info("updated report status",
		"cache_key", cacheKey,
		"status", status,
		"has_metadata", dataFreshnessDate != nil,
	)
}
