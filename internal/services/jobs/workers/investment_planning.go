package workers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/analysis"
	"github.com/estara-ai/www/internal/services/investment/locationexpander"
	"github.com/estara-ai/www/internal/services/investment/optimization"
	"github.com/estara-ai/www/internal/services/investment/projection"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/internal/services/property/providers"
)

// InvestmentPlanningWorker processes investment planning jobs
type InvestmentPlanningWorker struct {
	optimizer            *optimization.Service
	finder               *finder.Orchestrator
	market               *aggregator.Aggregator
	calculator           *projection.Calculator
	cache                *cache.HybridCache
	expander             *locationexpander.Service
	divestAnalyzer       *analysis.DivestAnalyzer
	acquisitionRec       *analysis.AcquisitionRecommender
	reinvestmentModeler  *analysis.ReinvestmentModeler
	logger               *slog.Logger
}

// InvestmentPlanningWorkerConfig holds configuration for the worker
type InvestmentPlanningWorkerConfig struct {
	Optimizer *optimization.Service
	Finder    *finder.Orchestrator
	Market    *aggregator.Aggregator
	Cache     *cache.HybridCache
	Client    *anthropic.Client // For fallback use
	Redis     *redisClient.Client
}

// ExtendedPlanningParams extends InvestmentPlanningParams with ADR-059 fields
type ExtendedPlanningParams struct {
	*investment.InvestmentPlanningParams

	// ADR-059: Selection mode fields
	SelectedProperties []investment.SelectedProperty
	Mode               investment.PlanningMode

	// ADR-059: Reinvestment fields
	ReinvestSurplusCashFlows bool
	ReinvestmentRate         float64 // 0-100
	ProjectionYears          int     // 1-10
}

// NewInvestmentPlanningWorker creates a new investment planning worker
func NewInvestmentPlanningWorker(cfg InvestmentPlanningWorkerConfig) *InvestmentPlanningWorker {
	var expander *locationexpander.Service
	if cfg.Client != nil {
		expander = locationexpander.NewService(cfg.Client, cfg.Redis)
	}

	logger := slog.Default().With("component", "investment_planning_worker")

	return &InvestmentPlanningWorker{
		optimizer:           cfg.Optimizer,
		finder:              cfg.Finder,
		market:              cfg.Market,
		calculator:          projection.NewCalculator(nil),
		cache:               cfg.Cache,
		expander:            expander,
		divestAnalyzer:      analysis.NewDivestAnalyzer(logger),
		acquisitionRec:      analysis.NewAcquisitionRecommender(logger),
		reinvestmentModeler: analysis.NewReinvestmentModeler(logger),
		logger:              logger,
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

	// Parse job parameters (including ADR-059 extended params)
	params, err := w.parseJobParams(job)
	if err != nil {
		return w.failedResult(job, fmt.Errorf("invalid job parameters: %w", err))
	}

	// Report initial progress
	w.reportProgress(progress, job.ID, 5, "Starting investment planning")

	// Determine mode
	w.logger.Info("planning mode determined",
		"mode", params.Mode,
		"selectedPropertyCount", len(params.SelectedProperties),
	)

	// Step 1: Get current mortgage rate (10%)
	w.reportProgress(progress, job.ID, 10, "Fetching current mortgage rates")
	mortgageRate, err := w.getMortgageRate(ctx)
	if err != nil {
		w.logger.Warn("failed to get mortgage rate, using default", "error", err)
		mortgageRate = 0.07 // Default to 7%
	}

	var result *investment.InvestmentPlanningResult

	// Branch based on mode
	if params.Mode == investment.PlanningModeSelection {
		result, err = w.processSelectionMode(ctx, job, params, mortgageRate, progress)
	} else {
		result, err = w.processSearchMode(ctx, job, params, mortgageRate, progress)
	}

	if err != nil {
		return w.failedResult(job, err)
	}

	// Set the mode in result
	result.Mode = params.Mode

	// ADR-059: Model cash flow reinvestment if enabled
	if params.ReinvestSurplusCashFlows && len(result.SelectedProperties) > 0 {
		w.reportProgress(progress, job.ID, 80, "Modeling cash flow reinvestment")
		reinvestAnalysis, err := w.modelReinvestment(params, result.SelectedProperties, mortgageRate)
		if err != nil {
			w.logger.Warn("failed to model reinvestment", "error", err)
		} else {
			result.ReinvestmentAnalysis = reinvestAnalysis
		}
	}

	// Calculate growth projections (85%)
	w.reportProgress(progress, job.ID, 85, "Calculating growth projections")
	projectionYears := params.ProjectionYears
	if projectionYears <= 0 {
		projectionYears = 10
	}
	result.GrowthProjection = *w.calculator.CalculateGrowth(result.SelectedProperties, projectionYears)

	// Fetch and calculate existing portfolio metrics (90%)
	existingPortfolio, err := w.fetchExistingPortfolio(ctx, job.UserID)
	if err != nil {
		w.logger.Warn("failed to fetch existing portfolio, proceeding without", "error", err)
	}
	if existingPortfolio != nil {
		w.reportProgress(progress, job.ID, 90, "Calculating combined portfolio metrics")
		result.ExistingPortfolio = w.calculator.SummarizeExistingPortfolio(existingPortfolio)
		result.CombinedMetrics = w.calculator.CalculateCombinedMetrics(
			result.ExistingPortfolio,
			&result.Metrics,
		)
	}

	// Cache the result (95%)
	w.reportProgress(progress, job.ID, 95, "Caching results")
	// Note: Handler stores "cache_key" (snake_case) in payload
	if cacheKey, ok := job.Payload["cache_key"].(string); ok && cacheKey != "" {
		if err := w.cacheResult(ctx, job.UserID, cacheKey, params, result); err != nil {
			w.logger.Warn("failed to cache result", "error", err)
		}
	}

	// Complete
	w.reportProgress(progress, job.ID, 100, "Investment planning complete")

	duration := time.Since(startTime)
	w.logger.Info("investment planning job complete",
		"job_id", job.ID,
		"mode", params.Mode,
		"duration_ms", duration.Milliseconds(),
		"selected_properties", len(result.SelectedProperties),
		"total_investment", result.Metrics.TotalInvestment,
	)

	// Convert result to map for JobResult
	resultData := map[string]interface{}{
		"selected_properties":      result.SelectedProperties,
		"metrics":                  result.Metrics,
		"growth_projection":        result.GrowthProjection,
		"existing_portfolio":       result.ExistingPortfolio,
		"combined_metrics":         result.CombinedMetrics,
		"multi_year_plan":          result.MultiYearPlan,
		"market_filters":           result.MarketFilters,
		"market_quality":           result.MarketQuality,
		"concentration":            result.Concentration,
		"mode":                     result.Mode,
		"property_recommendations": result.PropertyRecommendations,
		"reinvestment_analysis":    result.ReinvestmentAnalysis,
	}

	return &queue.JobResult{
		JobID:       job.ID,
		Status:      queue.JobStatusCompleted,
		Data:        resultData,
		Duration:    duration,
		CompletedAt: time.Now(),
	}, nil
}

// processSearchMode handles the traditional search-based investment planning
func (w *InvestmentPlanningWorker) processSearchMode(
	ctx context.Context,
	job *queue.Job,
	params *ExtendedPlanningParams,
	mortgageRate float64,
	progress chan<- queue.ProgressEvent,
) (*investment.InvestmentPlanningResult, error) {
	// Search for properties in specified locations (30%)
	w.reportProgress(progress, job.ID, 20, "Searching for properties")
	properties, err := w.searchProperties(ctx, params.InvestmentPlanningParams, progress, job.ID)
	if err != nil {
		return nil, fmt.Errorf("property search failed: %w", err)
	}

	if len(properties) == 0 {
		return nil, fmt.Errorf("no properties found matching criteria")
	}

	w.logger.Info("found properties",
		"count", len(properties),
		"locations", params.Locations,
	)

	// Fetch existing portfolio if user has one (40%)
	w.reportProgress(progress, job.ID, 40, "Analyzing existing portfolio")
	existingPortfolio, err := w.fetchExistingPortfolio(ctx, job.UserID)
	if err != nil {
		w.logger.Warn("failed to fetch existing portfolio, proceeding without", "error", err)
	}
	params.ExistingPortfolio = existingPortfolio

	// Optimize portfolio (70%)
	var result *investment.InvestmentPlanningResult

	if len(params.YearlyBudgets) > 0 {
		w.reportProgress(progress, job.ID, 50, "Running multi-year optimization")
		result, err = w.runMultiYearOptimization(ctx, params.InvestmentPlanningParams, properties, mortgageRate)
	} else {
		w.reportProgress(progress, job.ID, 50, "Running portfolio optimization")
		result, err = w.runSingleYearOptimization(ctx, params.InvestmentPlanningParams, properties, mortgageRate)
	}

	if err != nil {
		return nil, fmt.Errorf("optimization failed: %w", err)
	}

	return result, nil
}

// processSelectionMode handles ADR-059 selection-based investment planning
func (w *InvestmentPlanningWorker) processSelectionMode(
	ctx context.Context,
	job *queue.Job,
	params *ExtendedPlanningParams,
	mortgageRate float64,
	progress chan<- queue.ProgressEvent,
) (*investment.InvestmentPlanningResult, error) {
	w.logger.Info("processing selection mode",
		"selectedPropertyCount", len(params.SelectedProperties),
		"budget", params.Budget,
	)

	// Step 1: Convert selected properties to investment.Property (15%)
	w.reportProgress(progress, job.ID, 15, "Validating selected properties")
	properties := analysis.ConvertSelectedToProperties(params.SelectedProperties)

	// Step 2: Run divest analysis on selected properties (35%)
	w.reportProgress(progress, job.ID, 25, "Analyzing properties for keep/divest")
	divestParams := analysis.DivestAnalysisParams{
		Properties:      properties,
		Strategy:        params.Strategy,
		RiskTolerance:   params.RiskTolerance,
		AvailableBudget: params.Budget,
		MortgageRate:    mortgageRate,
		DownPaymentPct:  params.DownPaymentPct,
		MarketData:      make(map[string]*analysis.MarketMetrics), // TODO: Fetch market data
	}

	recommendations, err := w.divestAnalyzer.AnalyzeProperties(ctx, divestParams)
	if err != nil {
		w.logger.Warn("divest analysis failed", "error", err)
		// Continue with all properties as KEEP
	}

	// Separate KEEP and DIVEST recommendations
	keepProperties := make([]investment.Property, 0)
	divestValue := 0
	for _, rec := range recommendations {
		if rec.Action == investment.RecommendationActionKeep {
			keepProperties = append(keepProperties, rec.Property.Property)
		} else if rec.Action == investment.RecommendationActionDivest {
			divestValue += rec.EstimatedSaleProceeds
		}
	}

	w.logger.Info("divest analysis complete",
		"keepCount", len(keepProperties),
		"divestValue", divestValue,
	)

	// Step 3: Search for acquisition candidates if budget allows (50%)
	totalBudget := params.Budget + divestValue // Available budget + freed capital
	acquireRecommendations := make([]investment.PropertyRecommendation, 0)

	if totalBudget > 0 && w.finder != nil {
		w.reportProgress(progress, job.ID, 45, "Searching for acquisition candidates")

		// Search in same locations as selected properties
		acquisitionCandidates, err := w.searchProperties(ctx, params.InvestmentPlanningParams, progress, job.ID)
		if err != nil {
			w.logger.Warn("acquisition search failed", "error", err)
		} else if len(acquisitionCandidates) > 0 {
			// Score candidates
			w.reportProgress(progress, job.ID, 55, "Scoring acquisition candidates")

			// Convert to ScoredProperty (use optimizer for AI scoring)
			scoredCandidates, err := w.optimizer.ScoreProperties(ctx, acquisitionCandidates, investment.InvestorProfile{
				Strategy:         params.Strategy,
				RiskTolerance:    params.RiskTolerance,
				AvailableCapital: totalBudget,
			}, nil) // No existing portfolio for scoring context
			if err != nil {
				w.logger.Warn("candidate scoring failed", "error", err)
			} else {
				// Generate acquisition recommendations
				w.reportProgress(progress, job.ID, 65, "Generating acquisition recommendations")
				acquireParams := analysis.AcquisitionParams{
					AvailableBudget:   totalBudget,
					Candidates:        scoredCandidates,
					ExistingPortfolio: nil,
					KeepProperties:    keepProperties,
					DownPaymentPct:    params.DownPaymentPct,
					MortgageRate:      mortgageRate,
					Strategy:          params.Strategy,
					RiskTolerance:     params.RiskTolerance,
					MaxAcquisitions:   params.MaxProperties,
				}

				acquireRecommendations, err = w.acquisitionRec.GenerateRecommendations(ctx, acquireParams)
				if err != nil {
					w.logger.Warn("acquisition recommendation failed", "error", err)
				}
			}
		}
	}

	// Step 4: Build combined recommendations list
	allRecommendations := append(recommendations, acquireRecommendations...)

	// Step 5: Build selected properties from KEEP + ACQUIRE (70%)
	w.reportProgress(progress, job.ID, 70, "Building optimized portfolio")
	selectedProperties := make([]investment.PropertyInPortfolio, 0)
	for _, rec := range allRecommendations {
		if rec.Action == investment.RecommendationActionKeep || rec.Action == investment.RecommendationActionAcquire {
			selectedProperties = append(selectedProperties, rec.Property)
		}
	}

	// Calculate metrics
	metrics := w.calculator.CalculateMetrics(selectedProperties)

	result := &investment.InvestmentPlanningResult{
		SelectedProperties:      selectedProperties,
		Metrics:                 *metrics,
		PropertyRecommendations: allRecommendations,
	}

	return result, nil
}

// modelReinvestment creates reinvestment analysis using the ReinvestmentModeler
func (w *InvestmentPlanningWorker) modelReinvestment(
	params *ExtendedPlanningParams,
	properties []investment.PropertyInPortfolio,
	mortgageRate float64,
) (*investment.ReinvestmentAnalysis, error) {
	reinvestParams := analysis.ReinvestmentParams{
		Properties:        properties,
		ReinvestmentRate:  params.ReinvestmentRate,
		ProjectionYears:   params.ProjectionYears,
		MortgageRate:      mortgageRate,
		DownPaymentPct:    params.DownPaymentPct,
		AppreciationRate:  3.0, // Default 3%
		RentGrowthRate:    2.0, // Default 2%
		OperatingExpenses: 0.5, // Default 50%
	}

	return w.reinvestmentModeler.ModelReinvestment(reinvestParams)
}

// parseJobParams extracts investment planning parameters from job payload
func (w *InvestmentPlanningWorker) parseJobParams(job *queue.Job) (*ExtendedPlanningParams, error) {
	baseParams := &investment.InvestmentPlanningParams{
		Strategy:       investment.StrategyBalanced,
		RiskTolerance:  investment.RiskModerate,
		DownPaymentPct: 0.25, // Default 25%
		MaxProperties:  5,    // Default max
	}

	extParams := &ExtendedPlanningParams{
		InvestmentPlanningParams: baseParams,
		Mode:                     investment.PlanningModeSearch, // Default to search mode
		ReinvestmentRate:         100,                           // Default 100%
		ProjectionYears:          5,                             // Default 5 years
	}

	// ADR-059: Check for selected properties (triggers Selection mode)
	if selectedProps, ok := job.Payload["selectedProperties"].([]interface{}); ok && len(selectedProps) > 0 {
		extParams.Mode = investment.PlanningModeSelection
		for _, sp := range selectedProps {
			if spMap, ok := sp.(map[string]interface{}); ok {
				prop := w.parseSelectedProperty(spMap)
				extParams.SelectedProperties = append(extParams.SelectedProperties, prop)
			}
		}
		w.logger.Info("detected selection mode",
			"selectedPropertyCount", len(extParams.SelectedProperties),
		)

		// For selection mode, derive locations from selected properties
		baseParams.Locations = analysis.ExtractLocations(extParams.SelectedProperties)
	}

	// Extract locations (required for search mode, optional for selection mode)
	// Handle both []string (in-memory) and []interface{} (from JSON deserialization)
	if locations, ok := job.Payload["locations"].([]string); ok {
		baseParams.Locations = append(baseParams.Locations, locations...)
	} else if locations, ok := job.Payload["locations"].([]interface{}); ok {
		for _, loc := range locations {
			if s, ok := loc.(string); ok {
				baseParams.Locations = append(baseParams.Locations, s)
			}
		}
	}

	// Validate: search mode requires locations, selection mode requires properties
	if extParams.Mode == investment.PlanningModeSearch && len(baseParams.Locations) == 0 {
		return nil, fmt.Errorf("no locations specified for search mode")
	}
	if extParams.Mode == investment.PlanningModeSelection && len(extParams.SelectedProperties) == 0 {
		return nil, fmt.Errorf("no properties selected for selection mode")
	}

	// Extract budget (handle both int and float64 types)
	if budget, ok := job.Payload["budget"].(int); ok {
		baseParams.Budget = budget
	} else if budget, ok := job.Payload["budget"].(float64); ok {
		baseParams.Budget = int(budget)
	} else {
		return nil, fmt.Errorf("budget not specified")
	}

	// Extract optional parameters (accept snake_case and camelCase keys)
	if dp, ok := job.Payload["down_payment_percent"].(float64); ok {
		baseParams.DownPaymentPct = normalizePercent(dp)
	} else if dp, ok := job.Payload["downPaymentPct"].(float64); ok {
		baseParams.DownPaymentPct = normalizePercent(dp)
	}

	if strategy, ok := job.Payload["strategy"].(string); ok {
		baseParams.Strategy = normalizeStrategy(strategy)
	}

	if risk, ok := job.Payload["risk_tolerance"].(string); ok {
		baseParams.RiskTolerance = investment.RiskTolerance(risk)
	} else if risk, ok := job.Payload["riskTolerance"].(string); ok {
		baseParams.RiskTolerance = investment.RiskTolerance(risk)
	}

	// Handle int or float64 for max_properties
	if maxProps, ok := job.Payload["max_properties"].(int); ok {
		baseParams.MaxProperties = maxProps
	} else if maxProps, ok := job.Payload["max_properties"].(float64); ok {
		baseParams.MaxProperties = int(maxProps)
	} else if maxProps, ok := job.Payload["maxProperties"].(int); ok {
		baseParams.MaxProperties = maxProps
	} else if maxProps, ok := job.Payload["maxProperties"].(float64); ok {
		baseParams.MaxProperties = int(maxProps)
	}

	if includeSuburbs, ok := job.Payload["include_suburbs"].(bool); ok {
		baseParams.IncludeSuburbs = includeSuburbs
	} else if includeSuburbs, ok := job.Payload["includeSuburbs"].(bool); ok {
		baseParams.IncludeSuburbs = includeSuburbs
	}

	// Extract yearly budgets for multi-year planning (check both snake_case and camelCase keys)
	var yearlyBudgetsRaw []interface{}
	if yb, ok := job.Payload["yearly_budgets"].([]interface{}); ok {
		yearlyBudgetsRaw = yb
	} else if yb, ok := job.Payload["yearlyBudgets"].([]interface{}); ok {
		yearlyBudgetsRaw = yb
	}
	for _, yb := range yearlyBudgetsRaw {
		if ybMap, ok := yb.(map[string]interface{}); ok {
			year := 0
			budget := 0
			// Handle int or float64 for year
			if y, ok := ybMap["year"].(int); ok {
				year = y
			} else if y, ok := ybMap["year"].(float64); ok {
				year = int(y)
			}
			// Handle int or float64 for budget
			if b, ok := ybMap["budget"].(int); ok {
				budget = b
			} else if b, ok := ybMap["budget"].(float64); ok {
				budget = int(b)
			}
			if year > 0 && budget > 0 {
				baseParams.YearlyBudgets = append(baseParams.YearlyBudgets, investment.YearlyBudget{
					Year:   year,
					Budget: budget,
				})
			}
		}
	}

	// ADR-059: Extract reinvestment parameters
	if reinvest, ok := job.Payload["reinvestSurplusCashFlows"].(bool); ok {
		extParams.ReinvestSurplusCashFlows = reinvest
	}
	if rate, ok := job.Payload["reinvestmentRate"].(float64); ok {
		extParams.ReinvestmentRate = rate
	}
	// Handle int or float64 for projectionYears
	if years, ok := job.Payload["projectionYears"].(int); ok {
		extParams.ProjectionYears = years
	} else if years, ok := job.Payload["projectionYears"].(float64); ok {
		extParams.ProjectionYears = int(years)
	}

	return extParams, nil
}

// parseSelectedProperty extracts a SelectedProperty from a map
// Handles both int and float64 types for numeric fields (in-memory vs JSON)
func (w *InvestmentPlanningWorker) parseSelectedProperty(m map[string]interface{}) investment.SelectedProperty {
	prop := investment.SelectedProperty{}

	if id, ok := m["id"].(string); ok {
		prop.ID = id
	}
	if addr, ok := m["address"].(string); ok {
		prop.Address = addr
	}
	if city, ok := m["city"].(string); ok {
		prop.City = city
	}
	if state, ok := m["state"].(string); ok {
		prop.State = state
	}
	if zip, ok := m["zipCode"].(string); ok {
		prop.ZipCode = zip
	}
	// Handle int or float64 for price
	if price, ok := m["price"].(int); ok {
		prop.Price = price
	} else if price, ok := m["price"].(float64); ok {
		prop.Price = int(price)
	}
	// Handle int or float64 for beds
	if beds, ok := m["beds"].(int); ok {
		prop.Beds = beds
	} else if beds, ok := m["beds"].(float64); ok {
		prop.Beds = int(beds)
	}
	if baths, ok := m["baths"].(float64); ok {
		prop.Baths = baths
	}
	// Handle int or float64 for sqft
	if sqft, ok := m["sqft"].(int); ok {
		prop.Sqft = sqft
	} else if sqft, ok := m["sqft"].(float64); ok {
		prop.Sqft = int(sqft)
	}
	// Handle int or float64 for estimatedRent
	if rent, ok := m["estimatedRent"].(int); ok {
		prop.EstimatedRent = rent
	} else if rent, ok := m["estimatedRent"].(float64); ok {
		prop.EstimatedRent = int(rent)
	}
	if capRate, ok := m["capRate"].(float64); ok {
		prop.CapRate = capRate
	}
	// Handle int or float64 for yearBuilt
	if year, ok := m["yearBuilt"].(int); ok {
		prop.YearBuilt = year
	} else if year, ok := m["yearBuilt"].(float64); ok {
		prop.YearBuilt = int(year)
	}
	if propType, ok := m["propertyType"].(string); ok {
		prop.PropertyType = propType
	}
	// Handle int or float64 for daysOnMarket
	if dom, ok := m["daysOnMarket"].(int); ok {
		prop.DaysOnMarket = dom
	} else if dom, ok := m["daysOnMarket"].(float64); ok {
		prop.DaysOnMarket = int(dom)
	}
	if img, ok := m["imageUrl"].(string); ok {
		prop.ImageURL = img
	}
	if url, ok := m["listingUrl"].(string); ok {
		prop.ListingURL = url
	}

	return prop
}

func normalizePercent(value float64) float64 {
	if value > 1 {
		return value / 100
	}
	return value
}

func normalizeStrategy(value string) investment.InvestmentStrategy {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), "_", "-")
	switch normalized {
	case string(investment.StrategyCashFlow):
		return investment.StrategyCashFlow
	case string(investment.StrategyAppreciation):
		return investment.StrategyAppreciation
	case string(investment.StrategyBalanced):
		return investment.StrategyBalanced
	case string(investment.StrategyRiskAdjusted):
		return investment.StrategyRiskAdjusted
	default:
		return investment.InvestmentStrategy(normalized)
	}
}

// searchProperties searches for properties across all specified locations
func (w *InvestmentPlanningWorker) searchProperties(
	ctx context.Context,
	params *investment.InvestmentPlanningParams,
	progress chan<- queue.ProgressEvent,
	jobID string,
) ([]investment.Property, error) {
	allProperties := make([]investment.Property, 0)

	locations := params.Locations
	if params.IncludeSuburbs && w.expander != nil {
		w.reportProgress(progress, jobID, 15, "Expanding locations")
		expansions, err := w.expander.ExpandLocations(ctx, params.Locations, locationexpander.LocationExpansionOptions{})
		if err != nil {
			w.logger.Warn("failed to expand locations, using originals", "error", err)
		} else {
			locations = w.expander.GetUniqueLocations(expansions)
			if len(locations) == 0 {
				locations = params.Locations
			}
		}
	}

	for i, location := range locations {
		// Update progress
		pct := float64(20 + (i*10)/len(locations))
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
				Latitude:      r.Latitude,
				Longitude:     r.Longitude,
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
		SelectedProperties:      optResult.SelectedProperties,
		Metrics:                 optResult.Metrics,
		MarketFilters:           optResult.MarketFilters,
		MarketQuality:           optResult.MarketQuality,
		Concentration:           optResult.Concentration,
		Allocations:             optResult.Allocations,
		Recommendations:         optResult.Recommendations,
		DiversificationAnalysis: optResult.DiversificationAnalysis,
		RiskAnalysis:            optResult.RiskAnalysis,
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

	// Calculate additional analysis (same logic as single-year)
	allocations := calculateAllocations(allSelected, metrics.TotalInvestment)
	riskAnalysis := calculateBasicRiskAnalysis(allSelected)
	diversificationAnalysis := calculateBasicDiversification(allSelected)
	recommendations := generateBasicRecommendations(allSelected, metrics, diversificationAnalysis)

	return &investment.InvestmentPlanningResult{
		SelectedProperties:      allSelected,
		Metrics:                 *metrics,
		ExistingPortfolio:       multiResult.ExistingPortfolio,
		CombinedMetrics:         multiResult.CombinedMetrics,
		MultiYearPlan:           multiResult.MultiYearPlan,
		MarketFilters:           multiResult.MarketFilters,
		MarketQuality:           multiResult.MarketQuality,
		Allocations:             allocations,
		Recommendations:         recommendations,
		DiversificationAnalysis: diversificationAnalysis,
		RiskAnalysis:            riskAnalysis,
	}, nil
}

// cacheResult stores the result in the hybrid cache with location and metricsData for history display
func (w *InvestmentPlanningWorker) cacheResult(
	ctx context.Context,
	userID string,
	cacheKey string,
	params *ExtendedPlanningParams,
	result *investment.InvestmentPlanningResult,
) error {
	if w.cache == nil {
		return nil
	}

	// Build location string from params (e.g., "Peoria, IL; Chicago, IL")
	location := strings.Join(params.Locations, "; ")

	// Build metricsData for history card display (matches www_v1 format)
	// Key names must match handler's mapMetricsForHistory expectations:
	// - avgCashOnCash (not averageCashOnCash)
	// - expectedAnnualReturn (for projectedReturn)
	metricsData := map[string]interface{}{
		"strategy":             string(params.Strategy),
		"availableCapital":     params.Budget,
		"propertyCount":        len(result.SelectedProperties),
		"totalInvestment":      result.Metrics.TotalInvestment,
		"annualCashFlow":       result.Metrics.AnnualCashFlow,
		"averageCapRate":       result.Metrics.AvgCapRate,
		"avgCashOnCash":        result.Metrics.AvgCashOnCash,
		"expectedAnnualReturn": result.Metrics.ExpectedAnnualReturn,
		"mode":                 string(params.Mode),
		// Plan card display fields (handler looks for these specific names)
		// totalValue is used for equity calculation: equity = totalValue - totalDebt
		"totalValue":           result.Metrics.TotalInvestment,  // Asset value = sum of property prices
		"projectedValue":       result.Metrics.ProjectedValue,   // Alias for backward compatibility
		"totalDebt":            result.Metrics.TotalLoanAmount,  // Alias for handler mapping
		// Additional metrics for portfolio view
		"sharpeRatio":          result.Metrics.SharpeRatio,
		"debtServiceCoverage":  result.Metrics.DebtServiceCoverage,
		"diversificationScore": result.Metrics.DiversificationScore,
		"totalROI":             result.Metrics.TotalROI,
		"locationCount":        result.Metrics.LocationCount,
		"totalDownPayment":     result.Metrics.TotalDownPayment,
		"totalLoanAmount":      result.Metrics.TotalLoanAmount,
		"monthlyCashFlow":      result.Metrics.MonthlyCashFlow,
		"portfolioDscr":        result.Metrics.PortfolioDSCR,
		"fiveYearProjection":   result.Metrics.FiveYearProjection,
	}

	opts := cache.InvestmentPlanCacheOptions{
		Location:    location,
		MetricsData: metricsData,
	}

	// Use SetInvestmentPlan to store with location and metricsData
	return w.cache.SetInvestmentPlan(ctx, userID, cacheKey, result, opts, 24*time.Hour)
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

// Helper functions for multi-year result building

func calculateAllocations(properties []investment.PropertyInPortfolio, totalInvestment int) map[string]investment.LocationAllocation {
	allocations := make(map[string]investment.LocationAllocation)
	if len(properties) == 0 || totalInvestment == 0 {
		return allocations
	}

	for _, prop := range properties {
		location := prop.Property.City + ", " + prop.Property.State
		if location == ", " {
			continue
		}
		alloc := allocations[location]
		alloc.PropertyCount++
		alloc.InvestmentAmount += prop.Property.Price
		allocations[location] = alloc
	}

	for loc, alloc := range allocations {
		alloc.Percentage = float64(alloc.InvestmentAmount) / float64(totalInvestment) * 100
		allocations[loc] = alloc
	}

	return allocations
}

func calculateBasicRiskAnalysis(properties []investment.PropertyInPortfolio) *investment.RiskAnalysis {
	if len(properties) == 0 {
		return &investment.RiskAnalysis{
			PortfolioRisk:    50,
			RiskDistribution: investment.RiskDistribution{Low: 33, Medium: 34, High: 33},
		}
	}

	var lowCount, medCount, highCount int
	var totalScore float64
	for _, prop := range properties {
		totalScore += prop.Score
		if prop.Score >= 70 {
			lowCount++
		} else if prop.Score >= 50 {
			medCount++
		} else {
			highCount++
		}
	}

	total := float64(len(properties))
	avgScore := totalScore / total
	portfolioRisk := 100 - avgScore

	var warnings []string
	if highCount > 0 {
		warnings = append(warnings, fmt.Sprintf("⚠️ %d properties have elevated risk profiles", highCount))
	}

	return &investment.RiskAnalysis{
		PortfolioRisk: portfolioRisk,
		RiskDistribution: investment.RiskDistribution{
			Low:    float64(lowCount) / total * 100,
			Medium: float64(medCount) / total * 100,
			High:   float64(highCount) / total * 100,
		},
		Warnings: warnings,
	}
}

func calculateBasicDiversification(properties []investment.PropertyInPortfolio) *investment.DiversificationAnalysis {
	if len(properties) == 0 {
		return &investment.DiversificationAnalysis{DiversificationScore: 0}
	}

	locationSet := make(map[string]bool)
	for _, prop := range properties {
		location := prop.Property.City + ", " + prop.Property.State
		if location != ", " {
			locationSet[location] = true
		}
	}

	numLocations := len(locationSet)
	diversificationScore := float64(numLocations) * 20
	if diversificationScore > 100 {
		diversificationScore = 100
	}

	var opportunities []string
	if numLocations == 1 {
		opportunities = append(opportunities, "Consider expanding to additional markets for better diversification")
	}

	return &investment.DiversificationAnalysis{
		DiversificationScore: diversificationScore,
		Opportunities:        opportunities,
	}
}

func generateBasicRecommendations(
	properties []investment.PropertyInPortfolio,
	metrics *investment.PortfolioMetrics,
	diversification *investment.DiversificationAnalysis,
) []string {
	var recommendations []string

	if diversification != nil && diversification.DiversificationScore >= 60 {
		recommendations = append(recommendations,
			fmt.Sprintf("✅ Good diversification (%.1f/100)", diversification.DiversificationScore))
	}

	if metrics != nil && metrics.MonthlyCashFlow > 0 {
		recommendations = append(recommendations,
			fmt.Sprintf("✅ Positive monthly cash flow: $%d", metrics.MonthlyCashFlow))
	}

	if metrics != nil && metrics.AvgCapRate >= 6 {
		recommendations = append(recommendations,
			fmt.Sprintf("✅ Strong average cap rate: %.1f%%", metrics.AvgCapRate))
	}

	return recommendations
}
