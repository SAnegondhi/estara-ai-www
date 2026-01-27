package workers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/optimization"
	"github.com/estara-ai/www/internal/services/investment/projection"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/internal/services/property/providers"
)

// InvestmentPlanningWorker processes investment planning jobs
type InvestmentPlanningWorker struct {
	optimizer  *optimization.Service
	finder     *finder.Orchestrator
	market     *aggregator.Aggregator
	calculator *projection.Calculator
	cache      *cache.HybridCache
	logger     *slog.Logger
}

// InvestmentPlanningWorkerConfig holds configuration for the worker
type InvestmentPlanningWorkerConfig struct {
	Optimizer  *optimization.Service
	Finder     *finder.Orchestrator
	Market     *aggregator.Aggregator
	Cache      *cache.HybridCache
	Client     *anthropic.Client // For fallback use
}

// NewInvestmentPlanningWorker creates a new investment planning worker
func NewInvestmentPlanningWorker(cfg InvestmentPlanningWorkerConfig) *InvestmentPlanningWorker {
	return &InvestmentPlanningWorker{
		optimizer:  cfg.Optimizer,
		finder:     cfg.Finder,
		market:     cfg.Market,
		calculator: projection.NewCalculator(nil),
		cache:      cfg.Cache,
		logger:     slog.Default().With("component", "investment_planning_worker"),
	}
}

// GetHandler returns the job handler function
func (w *InvestmentPlanningWorker) GetHandler() queue.JobHandler {
	return func(ctx context.Context, job *queue.Job, progress chan<- queue.ProgressEvent) (*queue.JobResult, error) {
		return w.Process(ctx, job, progress)
	}
}

// Process executes an investment planning job
func (w *InvestmentPlanningWorker) Process(
	ctx context.Context,
	job *queue.Job,
	progress chan<- queue.ProgressEvent,
) (*queue.JobResult, error) {
	startTime := time.Now()
	w.logger.Info("processing investment planning job",
		"job_id", job.ID,
		"user_id", job.UserID,
	)

	// Parse job parameters
	params, err := w.parseJobParams(job)
	if err != nil {
		return w.failedResult(job, fmt.Errorf("invalid job parameters: %w", err))
	}

	// Report initial progress
	w.reportProgress(progress, job.ID, 5, "Starting investment planning")

	// Step 1: Get current mortgage rate (10%)
	w.reportProgress(progress, job.ID, 10, "Fetching current mortgage rates")
	mortgageRate, err := w.getMortgageRate(ctx)
	if err != nil {
		w.logger.Warn("failed to get mortgage rate, using default", "error", err)
		mortgageRate = 0.07 // Default to 7%
	}

	// Step 2: Search for properties in specified locations (30%)
	w.reportProgress(progress, job.ID, 20, "Searching for properties")
	properties, err := w.searchProperties(ctx, params, progress, job.ID)
	if err != nil {
		return w.failedResult(job, fmt.Errorf("property search failed: %w", err))
	}

	if len(properties) == 0 {
		return w.failedResult(job, fmt.Errorf("no properties found matching criteria"))
	}

	w.logger.Info("found properties",
		"count", len(properties),
		"locations", params.Locations,
	)

	// Step 3: Fetch existing portfolio if user has one (40%)
	w.reportProgress(progress, job.ID, 40, "Analyzing existing portfolio")
	existingPortfolio, err := w.fetchExistingPortfolio(ctx, job.UserID)
	if err != nil {
		w.logger.Warn("failed to fetch existing portfolio, proceeding without", "error", err)
	}
	params.ExistingPortfolio = existingPortfolio

	// Step 4: Optimize portfolio (70%)
	var result *investment.InvestmentPlanningResult

	if len(params.YearlyBudgets) > 0 {
		// Multi-year planning
		w.reportProgress(progress, job.ID, 50, "Running multi-year optimization")
		result, err = w.runMultiYearOptimization(ctx, params, properties, mortgageRate)
	} else {
		// Single-year planning
		w.reportProgress(progress, job.ID, 50, "Running portfolio optimization")
		result, err = w.runSingleYearOptimization(ctx, params, properties, mortgageRate)
	}

	if err != nil {
		return w.failedResult(job, fmt.Errorf("optimization failed: %w", err))
	}

	// Step 5: Calculate growth projections (85%)
	w.reportProgress(progress, job.ID, 85, "Calculating growth projections")
	result.GrowthProjection = *w.calculator.CalculateGrowth(result.SelectedProperties, 10) // 10-year projection

	// Step 6: Calculate combined metrics if existing portfolio (90%)
	if existingPortfolio != nil {
		w.reportProgress(progress, job.ID, 90, "Calculating combined portfolio metrics")
		result.ExistingPortfolio = w.calculator.SummarizeExistingPortfolio(existingPortfolio)
		result.CombinedMetrics = w.calculator.CalculateCombinedMetrics(
			result.ExistingPortfolio,
			&result.Metrics,
		)
	}

	// Step 7: Cache the result (95%)
	w.reportProgress(progress, job.ID, 95, "Caching results")
	if cacheKey, ok := job.Payload["cacheKey"].(string); ok && cacheKey != "" {
		if err := w.cacheResult(ctx, job.UserID, cacheKey, result); err != nil {
			w.logger.Warn("failed to cache result", "error", err)
		}
	}

	// Complete
	w.reportProgress(progress, job.ID, 100, "Investment planning complete")

	duration := time.Since(startTime)
	w.logger.Info("investment planning job complete",
		"job_id", job.ID,
		"duration_ms", duration.Milliseconds(),
		"selected_properties", len(result.SelectedProperties),
		"total_investment", result.Metrics.TotalInvestment,
	)

	// Convert result to map for JobResult
	resultData := map[string]interface{}{
		"selected_properties": result.SelectedProperties,
		"metrics":             result.Metrics,
		"growth_projection":   result.GrowthProjection,
		"existing_portfolio":  result.ExistingPortfolio,
		"combined_metrics":    result.CombinedMetrics,
		"multi_year_plan":     result.MultiYearPlan,
	}

	return &queue.JobResult{
		JobID:       job.ID,
		Status:      queue.JobStatusCompleted,
		Data:        resultData,
		Duration:    duration,
		CompletedAt: time.Now(),
	}, nil
}

// parseJobParams extracts investment planning parameters from job payload
func (w *InvestmentPlanningWorker) parseJobParams(job *queue.Job) (*investment.InvestmentPlanningParams, error) {
	params := &investment.InvestmentPlanningParams{
		Strategy:       investment.StrategyBalanced,
		RiskTolerance:  investment.RiskModerate,
		DownPaymentPct: 0.25, // Default 25%
		MaxProperties:  5,    // Default max
	}

	// Extract locations
	if locations, ok := job.Payload["locations"].([]interface{}); ok {
		for _, loc := range locations {
			if s, ok := loc.(string); ok {
				params.Locations = append(params.Locations, s)
			}
		}
	}
	if len(params.Locations) == 0 {
		return nil, fmt.Errorf("no locations specified")
	}

	// Extract budget
	if budget, ok := job.Payload["budget"].(float64); ok {
		params.Budget = int(budget)
	} else {
		return nil, fmt.Errorf("budget not specified")
	}

	// Extract optional parameters
	if dp, ok := job.Payload["downPaymentPct"].(float64); ok {
		params.DownPaymentPct = dp
	}

	if strategy, ok := job.Payload["strategy"].(string); ok {
		params.Strategy = investment.InvestmentStrategy(strategy)
	}

	if risk, ok := job.Payload["riskTolerance"].(string); ok {
		params.RiskTolerance = investment.RiskTolerance(risk)
	}

	if maxProps, ok := job.Payload["maxProperties"].(float64); ok {
		params.MaxProperties = int(maxProps)
	}

	// Extract yearly budgets for multi-year planning
	if yearlyBudgets, ok := job.Payload["yearlyBudgets"].([]interface{}); ok {
		for _, yb := range yearlyBudgets {
			if ybMap, ok := yb.(map[string]interface{}); ok {
				year := 0
				budget := 0
				if y, ok := ybMap["year"].(float64); ok {
					year = int(y)
				}
				if b, ok := ybMap["budget"].(float64); ok {
					budget = int(b)
				}
				if year > 0 && budget > 0 {
					params.YearlyBudgets = append(params.YearlyBudgets, investment.YearlyBudget{
						Year:   year,
						Budget: budget,
					})
				}
			}
		}
	}

	return params, nil
}

// searchProperties searches for properties across all specified locations
func (w *InvestmentPlanningWorker) searchProperties(
	ctx context.Context,
	params *investment.InvestmentPlanningParams,
	progress chan<- queue.ProgressEvent,
	jobID string,
) ([]investment.Property, error) {
	allProperties := make([]investment.Property, 0)

	for i, location := range params.Locations {
		// Update progress
		pct := float64(20 + (i*10)/len(params.Locations))
		w.reportProgress(progress, jobID, pct, fmt.Sprintf("Searching in %s", location))

		// Calculate max price based on budget and down payment
		maxPrice := int(float64(params.Budget) / params.DownPaymentPct)

		// Parse location into city and state
		parts := strings.Split(location, ",")
		city := strings.TrimSpace(parts[0])
		state := ""
		if len(parts) > 1 {
			state = strings.TrimSpace(parts[1])
		}

		// Search properties
		results, err := w.finder.Search(ctx, providers.SearchParams{
			City:     city,
			State:    state,
			MaxPrice: maxPrice,
			Limit:    50, // Get up to 50 per location
		})
		if err != nil {
			w.logger.Warn("search failed for location",
				"location", location,
				"error", err,
			)
			continue
		}

		// Convert to investment.Property type
		for _, r := range results.Properties {
			// Get first image URL if available
			imageURL := ""
			if len(r.Images) > 0 {
				imageURL = r.Images[0]
			}

			allProperties = append(allProperties, investment.Property{
				ID:            r.ID,
				Address:       r.Address,
				City:          r.City,
				State:         r.State,
				ZipCode:       r.ZipCode,
				Price:         r.Price,
				Beds:          r.Beds,
				Baths:         r.Baths,
				Sqft:          r.Sqft,
				EstimatedRent: r.EstimatedRent,
				YearBuilt:     r.YearBuilt,
				PropertyType:  string(r.PropertyType),
				ListingURL:    r.ListingURL,
				ImageURL:      imageURL,
				DaysOnMarket:  r.DaysOnMarket,
				Provider:      r.ProviderName,
			})
		}
	}

	return allProperties, nil
}

// fetchExistingPortfolio retrieves the user's current portfolio
func (w *InvestmentPlanningWorker) fetchExistingPortfolio(ctx context.Context, userID string) (*investment.ExistingPortfolio, error) {
	// TODO: Implement database query for user's V2PortfolioProperty records
	// For now, return nil (no existing portfolio)
	return nil, nil
}

// getMortgageRate fetches current mortgage rate from market data
func (w *InvestmentPlanningWorker) getMortgageRate(ctx context.Context) (float64, error) {
	if w.market == nil {
		return 0.07, nil // Default
	}

	// Try to get from market aggregator
	// TODO: Implement FRED API call for mortgage rates
	return 0.07, nil
}

// runSingleYearOptimization runs optimization for a single budget
func (w *InvestmentPlanningWorker) runSingleYearOptimization(
	ctx context.Context,
	params *investment.InvestmentPlanningParams,
	properties []investment.Property,
	mortgageRate float64,
) (*investment.InvestmentPlanningResult, error) {
	optResult, err := w.optimizer.Optimize(ctx, investment.OptimizationRequest{
		Properties:        properties,
		Budget:            params.Budget,
		DownPaymentPct:    params.DownPaymentPct,
		Strategy:          params.Strategy,
		RiskTolerance:     params.RiskTolerance,
		MaxProperties:     params.MaxProperties,
		ExistingPortfolio: params.ExistingPortfolio,
		MortgageRate:      mortgageRate,
	})
	if err != nil {
		return nil, err
	}

	return &investment.InvestmentPlanningResult{
		SelectedProperties: optResult.SelectedProperties,
		Metrics:            optResult.Metrics,
	}, nil
}

// runMultiYearOptimization runs optimization across multiple years
func (w *InvestmentPlanningWorker) runMultiYearOptimization(
	ctx context.Context,
	params *investment.InvestmentPlanningParams,
	properties []investment.Property,
	mortgageRate float64,
) (*investment.InvestmentPlanningResult, error) {
	multiResult, err := w.optimizer.OptimizeMultiYear(ctx, investment.MultiYearRequest{
		Properties:        properties,
		YearlyBudgets:     params.YearlyBudgets,
		DownPaymentPct:    params.DownPaymentPct,
		Strategy:          params.Strategy,
		RiskTolerance:     params.RiskTolerance,
		ExistingPortfolio: params.ExistingPortfolio,
		MortgageRate:      mortgageRate,
	})
	if err != nil {
		return nil, err
	}

	// Aggregate selected properties from all years
	allSelected := make([]investment.PropertyInPortfolio, 0)
	for _, year := range multiResult.MultiYearPlan.Years {
		allSelected = append(allSelected, year.Properties...)
	}

	// Calculate aggregate metrics
	metrics := w.calculator.CalculateMetrics(allSelected)

	return &investment.InvestmentPlanningResult{
		SelectedProperties: allSelected,
		Metrics:            *metrics,
		ExistingPortfolio:  multiResult.ExistingPortfolio,
		CombinedMetrics:    multiResult.CombinedMetrics,
		MultiYearPlan:      multiResult.MultiYearPlan,
	}, nil
}

// cacheResult stores the result in the hybrid cache
func (w *InvestmentPlanningWorker) cacheResult(
	ctx context.Context,
	userID string,
	cacheKey string,
	result *investment.InvestmentPlanningResult,
) error {
	if w.cache == nil {
		return nil
	}

	fullKey := fmt.Sprintf("investment_plan:%s:%s", userID, cacheKey)
	return w.cache.Set(ctx, userID, fullKey, "investment_planning", result, 24*time.Hour) // Cache for 24 hours
}

// reportProgress sends a progress event
func (w *InvestmentPlanningWorker) reportProgress(
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
func (w *InvestmentPlanningWorker) failedResult(job *queue.Job, err error) (*queue.JobResult, error) {
	w.logger.Error("investment planning job failed",
		"job_id", job.ID,
		"error", err,
	)

	return &queue.JobResult{
		JobID:       job.ID,
		Status:      queue.JobStatusFailed,
		Error:       err.Error(),
		CompletedAt: time.Now(),
	}, err
}
