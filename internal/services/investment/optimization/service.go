package optimization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/projection"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/economics"
	"github.com/estara-ai/www/internal/services/market/timeseries"
)

// AI scoring cache TTL - matches property search cache (ADR-061, ADR-064)
const aiScoringCacheTTL = 24 * time.Hour

// Service provides portfolio optimization with AI-driven property scoring
type Service struct {
	client     *anthropic.Client
	market     *aggregator.Aggregator
	cache      *cache.HybridCache
	calculator *projection.Calculator
	logger     *slog.Logger
	// ADR-064: AI scoring cache
	db      *postgres.Pool
	redis   *redisClient.Client
	queries *queries.Queries
	// ADR-069: Economic data integration
	economics       economics.Provider
	enhancedScorer  *EconomicsEnhancedScorer
	// Market correlation analysis
	correlationAnalyzer *CorrelationAnalyzer
}

// NewService creates a new optimization service
func NewService(
	client *anthropic.Client,
	market *aggregator.Aggregator,
	cache *cache.HybridCache,
) *Service {
	return &Service{
		client:     client,
		market:     market,
		cache:      cache,
		calculator: projection.NewCalculator(nil),
		logger:     slog.Default().With("component", "portfolio_optimization"),
	}
}

// NewServiceWithDB creates a new optimization service with database access for AI scoring cache (ADR-064)
func NewServiceWithDB(
	client *anthropic.Client,
	market *aggregator.Aggregator,
	cache *cache.HybridCache,
	db *postgres.Pool,
	redis *redisClient.Client,
) *Service {
	return &Service{
		client:     client,
		market:     market,
		cache:      cache,
		calculator: projection.NewCalculator(nil),
		logger:     slog.Default().With("component", "portfolio_optimization"),
		db:         db,
		redis:      redis,
		queries:    queries.New(db),
	}
}

// NewServiceWithEconomics creates a new optimization service with economic data integration (ADR-069)
func NewServiceWithEconomics(
	client *anthropic.Client,
	market *aggregator.Aggregator,
	cache *cache.HybridCache,
	db *postgres.Pool,
	redis *redisClient.Client,
	econ economics.Provider,
	metro *timeseries.MetroReader,
) *Service {
	var corrAnalyzer *CorrelationAnalyzer
	if metro != nil {
		corrAnalyzer = NewCorrelationAnalyzer(metro)
	}
	return &Service{
		client:              client,
		market:              market,
		cache:               cache,
		calculator:          projection.NewCalculator(nil),
		logger:              slog.Default().With("component", "portfolio_optimization"),
		db:                  db,
		redis:               redis,
		queries:             queries.New(db),
		economics:           econ,
		enhancedScorer:      NewEconomicsEnhancedScorer(econ),
		correlationAnalyzer: corrAnalyzer,
	}
}

// Optimize generates an optimized portfolio from candidate properties
func (s *Service) Optimize(ctx context.Context, req investment.OptimizationRequest) (*investment.OptimizationResult, error) {
	s.logger.Info("starting portfolio optimization",
		"property_count", len(req.Properties),
		"budget", req.Budget,
		"strategy", req.Strategy,
	)

	if len(req.Properties) == 0 {
		return nil, fmt.Errorf("no properties provided for optimization")
	}

	// Filter out properties with unrealistic data (likely bad data or distressed)
	qualityFiltered := filterByDataQuality(req.Properties)
	if len(qualityFiltered) == 0 {
		s.logger.Warn("all properties filtered out by data quality checks, using original set")
		qualityFiltered = req.Properties
	} else if len(qualityFiltered) < len(req.Properties) {
		s.logger.Info("filtered properties by data quality",
			"original", len(req.Properties),
			"remaining", len(qualityFiltered),
			"removed", len(req.Properties)-len(qualityFiltered),
		)
	}

	locationMarketData := s.getLocationMarketData(ctx, qualityFiltered)
	filteredProperties := qualityFiltered
	var filterSummary *investment.MarketFilterSummary
	if len(locationMarketData) > 0 {
		filters := buildMarketFilters(req.Strategy)
		summary := ApplyMarketFilters(locationMarketData, filters)
		filterSummary = &summary
		// IMPORTANT: Use qualityFiltered (not req.Properties) to preserve data quality filtering
		qualified := filterPropertiesByMarket(qualityFiltered, summary)
		if len(qualified) > 0 {
			filteredProperties = qualified
		}
	}

	// ADR-069: Use enhanced scorer with economic data if available
	marketQuality := s.calculateMarketQualityScores(ctx, locationMarketData)

	// Pre-score and limit properties before AI scoring to prevent timeout
	// AI scoring can handle ~100 properties within reasonable time
	const maxPropertiesForAI = 100
	investorProfile := investment.InvestorProfile{
		RiskTolerance:     req.RiskTolerance,
		Strategy:          req.Strategy,
		AvailableCapital:  req.Budget,
		InvestmentHorizon: "5-10 years", // Default
	}
	preScoredProperties := preScoreAndLimitProperties(filteredProperties, investorProfile, maxPropertiesForAI, s.logger)

	// Score properties using AI
	scoredProperties, err := s.ScoreProperties(ctx, preScoredProperties, investment.InvestorProfile{
		RiskTolerance:     req.RiskTolerance,
		Strategy:          req.Strategy,
		AvailableCapital:  req.Budget,
		InvestmentHorizon: "5-10 years", // Default
	}, req.ExistingPortfolio)
	if err != nil {
		return nil, fmt.Errorf("failed to score properties: %w", err)
	}

	applyMarketQualityToScored(scoredProperties, marketQuality)

	// Filter to STRONG_BUY and BUY recommendations
	candidates := filterByRecommendation(scoredProperties)
	if len(candidates) == 0 {
		candidates = scoredProperties
	}

	selected, concentration, _ := s.selectWithTwoStage(
		candidates,
		req.Budget,
		req.DownPaymentPct,
		req.MortgageRate,
		req.RiskTolerance,
		req.MaxProperties,
	)

	// Calculate metrics for selected properties
	portfolioProperties := make([]investment.PropertyInPortfolio, 0, len(selected))
	for _, pp := range selected {
		portfolioProperties = append(portfolioProperties, pp)
	}

	metrics := s.calculator.CalculateMetrics(portfolioProperties)

	// Calculate allocations by location
	allocations := calculateAllocations(portfolioProperties, metrics.TotalInvestment)

	// Calculate risk analysis
	riskAnalysis := calculateRiskAnalysis(portfolioProperties, scoredProperties)

	// Calculate diversification analysis with market correlation
	diversificationAnalysis := s.calculateDiversificationWithCorrelation(ctx, portfolioProperties, concentration)

	// Generate per-market allocation rationale
	allocationRationale := GenerateAllocationRationale(allocations, marketQuality, diversificationAnalysis.Correlations)

	// Generate recommendations
	recommendations := generateRecommendations(portfolioProperties, metrics, riskAnalysis, diversificationAnalysis)

	s.logger.Info("optimization complete",
		"selected_count", len(portfolioProperties),
		"total_investment", metrics.TotalInvestment,
		"annual_cash_flow", metrics.AnnualCashFlow,
	)

	return &investment.OptimizationResult{
		SelectedProperties:      portfolioProperties,
		Metrics:                 *metrics,
		ScoredProperties:        scoredProperties,
		Concentration:           concentration,
		MarketFilters:           filterSummary,
		MarketQuality:           marketQuality,
		Allocations:             allocations,
		AllocationRationale:     allocationRationale,
		RiskAnalysis:            riskAnalysis,
		DiversificationAnalysis: diversificationAnalysis,
		Recommendations:         recommendations,
	}, nil
}

// calculateMarketQualityScores computes market quality for each location
// ADR-069: Uses enhanced scorer with live economic data when available
func (s *Service) calculateMarketQualityScores(ctx context.Context, locationMarketData map[string]*aggregator.MarketData) []investment.LocationMarketAnalysis {
	results := make([]investment.LocationMarketAnalysis, 0, len(locationMarketData))

	// Use enhanced scorer if economic data is available
	if s.enhancedScorer != nil {
		for location, data := range locationMarketData {
			// Parse city, state from location string (format: "City, ST")
			city, state := parseLocation(location)
			analysis := s.enhancedScorer.BuildEnhancedLocationMarketAnalysis(ctx, city, state, location, data)
			results = append(results, analysis)
		}
		s.logger.Debug("used enhanced scorer with economic data", "locations", len(results))
		return results
	}

	// Fallback to standard scoring without economic data
	for location, data := range locationMarketData {
		results = append(results, BuildLocationMarketAnalysis(location, data))
	}
	return results
}

// parseLocation extracts city and state from location string (e.g., "Phoenix, AZ")
func parseLocation(location string) (city, state string) {
	parts := strings.Split(location, ", ")
	if len(parts) >= 2 {
		city = parts[0]
		state = parts[len(parts)-1]
	} else if len(parts) == 1 {
		city = parts[0]
	}
	return city, state
}

// ScoreProperties uses AI to evaluate properties on buyability, rentability, ROI
// ADR-064: Results are cached for 24 hours to avoid redundant API calls
func (s *Service) ScoreProperties(
	ctx context.Context,
	properties []investment.Property,
	profile investment.InvestorProfile,
	existingPortfolio *investment.ExistingPortfolio,
) ([]investment.ScoredProperty, error) {
	if len(properties) == 0 {
		return []investment.ScoredProperty{}, nil
	}

	// ADR-064: Check cache first (if database is configured)
	cacheKey := s.buildScoringCacheKey(properties, profile)
	if s.queries != nil {
		if cached, err := s.getScoringFromCache(ctx, cacheKey); err == nil && len(cached) > 0 {
			s.logger.Info("AI scoring cache hit",
				"cache_key", cacheKey,
				"property_count", len(cached),
			)
			return cached, nil
		}
	}

	s.logger.Info("scoring properties with AI (cache miss)",
		"property_count", len(properties),
		"strategy", profile.Strategy,
		"risk_tolerance", profile.RiskTolerance,
		"cache_key", cacheKey,
	)

	// Build prompt for AI evaluation
	prompt := s.buildScoringPrompt(properties, profile, existingPortfolio)

	// Call AI for scoring
	response, err := s.client.Complete(ctx, PropertyScoringSystemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("AI scoring failed: %w", err)
	}

	// Parse AI response
	scored, err := s.parseScoringResponse(response, properties)
	if err != nil {
		s.logger.Warn("failed to parse AI scoring response, using fallback scoring",
			"error", err,
		)
		scored = s.fallbackScoring(properties, profile)
	}

	// ADR-064: Cache the result (if database is configured)
	if s.queries != nil && len(scored) > 0 {
		if err := s.cacheScoringResult(ctx, cacheKey, scored, properties, profile); err != nil {
			s.logger.Warn("failed to cache AI scoring result", "error", err)
		}
	}

	return scored, nil
}

// buildScoringCacheKey creates a deterministic cache key for AI scoring results
// Key format: ai_score:{properties_hash}:{strategy}:{risk_tolerance}
func (s *Service) buildScoringCacheKey(properties []investment.Property, profile investment.InvestorProfile) string {
	// Sort property IDs for deterministic hash
	ids := make([]string, len(properties))
	for i, p := range properties {
		ids[i] = p.ID
	}
	sort.Strings(ids)

	// Create hash of property IDs
	h := sha256.New()
	h.Write([]byte(strings.Join(ids, ",")))
	propertiesHash := hex.EncodeToString(h.Sum(nil))[:16] // First 16 chars for brevity

	return fmt.Sprintf("ai_score:%s:%s:%s", propertiesHash, profile.Strategy, profile.RiskTolerance)
}

// getScoringFromCache retrieves cached AI scoring results
func (s *Service) getScoringFromCache(ctx context.Context, cacheKey string) ([]investment.ScoredProperty, error) {
	if s.queries == nil {
		return nil, fmt.Errorf("database not configured")
	}

	// Try Redis first (L1)
	if s.redis != nil {
		if data, err := s.redis.Client.Get(ctx, cacheKey).Bytes(); err == nil && len(data) > 0 {
			var scored []investment.ScoredProperty
			if err := json.Unmarshal(data, &scored); err == nil {
				return scored, nil
			}
		}
	}

	// Try PostgreSQL (L2)
	cached, err := s.queries.GetAIScoringCache(ctx, cacheKey)
	if err != nil {
		return nil, err
	}

	var scored []investment.ScoredProperty
	if err := json.Unmarshal(cached.ScoredProperties, &scored); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cached scores: %w", err)
	}

	// Backfill Redis cache
	if s.redis != nil {
		data, _ := json.Marshal(scored)
		if cached.ExpiresAt.Valid {
			ttl := time.Until(cached.ExpiresAt.Time)
			if ttl > 0 {
				s.redis.Client.Set(ctx, cacheKey, data, ttl)
			}
		}
	}

	return scored, nil
}

// cacheScoringResult stores AI scoring results in cache
func (s *Service) cacheScoringResult(
	ctx context.Context,
	cacheKey string,
	scored []investment.ScoredProperty,
	properties []investment.Property,
	profile investment.InvestorProfile,
) error {
	if s.queries == nil {
		return fmt.Errorf("database not configured")
	}

	// Serialize scored properties
	data, err := json.Marshal(scored)
	if err != nil {
		return fmt.Errorf("failed to marshal scored properties: %w", err)
	}

	// Extract properties hash from cache key
	parts := strings.Split(cacheKey, ":")
	propertiesHash := ""
	if len(parts) >= 2 {
		propertiesHash = parts[1]
	}

	expiresAt := time.Now().Add(aiScoringCacheTTL)

	// Store in PostgreSQL (L2)
	err = s.queries.UpsertAIScoringCache(ctx, queries.UpsertAIScoringCacheParams{
		CacheKey:         cacheKey,
		PropertiesHash:   propertiesHash,
		Strategy:         string(profile.Strategy),
		RiskTolerance:    string(profile.RiskTolerance),
		ScoredProperties: data,
		PropertyCount:    int32(len(properties)),
		ExpiresAt:        pgtype.Timestamp{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("failed to cache in database: %w", err)
	}

	// Store in Redis (L1)
	if s.redis != nil {
		s.redis.Client.Set(ctx, cacheKey, data, aiScoringCacheTTL)
	}

	s.logger.Info("cached AI scoring result",
		"cache_key", cacheKey,
		"property_count", len(scored),
		"expires_at", expiresAt,
	)

	return nil
}

// OptimizeMultiYear plans acquisitions across multiple years
func (s *Service) OptimizeMultiYear(ctx context.Context, req investment.MultiYearRequest) (*investment.MultiYearResult, error) {
	s.logger.Info("starting multi-year optimization",
		"property_count", len(req.Properties),
		"years", len(req.YearlyBudgets),
	)

	if len(req.YearlyBudgets) == 0 {
		return nil, fmt.Errorf("no yearly budgets provided")
	}

	// Filter out properties with unrealistic data (likely bad data or distressed)
	qualityFiltered := filterByDataQuality(req.Properties)
	if len(qualityFiltered) == 0 {
		s.logger.Warn("all properties filtered out by data quality checks, using original set")
		qualityFiltered = req.Properties
	} else if len(qualityFiltered) < len(req.Properties) {
		s.logger.Info("filtered properties by data quality",
			"original", len(req.Properties),
			"remaining", len(qualityFiltered),
			"removed", len(req.Properties)-len(qualityFiltered),
		)
	}

	locationMarketData := s.getLocationMarketData(ctx, qualityFiltered)
	filteredProperties := qualityFiltered
	var filterSummary *investment.MarketFilterSummary
	if len(locationMarketData) > 0 {
		filters := buildMarketFilters(req.Strategy)
		summary := ApplyMarketFilters(locationMarketData, filters)
		filterSummary = &summary
		// IMPORTANT: Use qualityFiltered (not req.Properties) to preserve data quality filtering
		qualified := filterPropertiesByMarket(qualityFiltered, summary)
		if len(qualified) > 0 {
			filteredProperties = qualified
		}
	}

	marketQuality := CalculateMarketQualityScores(locationMarketData)

	// Score all properties upfront
	profile := investment.InvestorProfile{
		RiskTolerance:     req.RiskTolerance,
		Strategy:          req.Strategy,
		AvailableCapital:  req.YearlyBudgets[0].Budget, // Use first year budget for initial scoring
		InvestmentHorizon: fmt.Sprintf("%d years", len(req.YearlyBudgets)),
	}

	// Pre-score and limit properties before AI scoring to prevent timeout
	const maxPropertiesForAI = 100
	preScoredProperties := preScoreAndLimitProperties(filteredProperties, profile, maxPropertiesForAI, s.logger)

	scoredProperties, err := s.ScoreProperties(ctx, preScoredProperties, profile, req.ExistingPortfolio)
	if err != nil {
		return nil, fmt.Errorf("failed to score properties: %w", err)
	}

	applyMarketQualityToScored(scoredProperties, marketQuality)

	// Filter to recommendations
	candidates := filterByRecommendation(scoredProperties)
	if len(candidates) == 0 {
		candidates = scoredProperties
	}
	remaining := make([]investment.ScoredProperty, len(candidates))
	copy(remaining, candidates)

	// Allocate properties to each year
	yearlyPlans := make([]investment.YearlyAcquisitionPlan, 0, len(req.YearlyBudgets))
	totalPropertyCount := 0
	totalInvestment := 0

	for _, yearBudget := range req.YearlyBudgets {
		selected, _, selectedIDs := s.selectWithTwoStage(
			remaining,
			yearBudget.Budget,
			req.DownPaymentPct,
			req.MortgageRate,
			req.RiskTolerance,
			0,
		)

		allocatedCapital := 0
		projectedCashFlow := 0
		for _, pp := range selected {
			allocatedCapital += pp.Property.Price
			projectedCashFlow += pp.MonthlyCashFlow * 12
		}

		yearPlan := investment.YearlyAcquisitionPlan{
			Year:             yearBudget.Year,
			Budget:           yearBudget.Budget,
			AllocatedCapital: allocatedCapital,
			Properties:       selected,
			Metrics: investment.YearlyPlanMetrics{
				PropertyCount:     len(selected),
				TotalInvestment:   allocatedCapital,
				ProjectedCashFlow: projectedCashFlow,
			},
		}

		yearlyPlans = append(yearlyPlans, yearPlan)

		// Remove selected properties from pool
		newRemaining := make([]investment.ScoredProperty, 0)
		for _, sp := range remaining {
			if !selectedIDs[sp.Property.ID] {
				newRemaining = append(newRemaining, sp)
			}
		}
		remaining = newRemaining

		totalPropertyCount += yearPlan.Metrics.PropertyCount
		totalInvestment += yearPlan.Metrics.TotalInvestment
	}

	// Build growth chart
	growthChart := s.buildGrowthChart(yearlyPlans, len(req.YearlyBudgets)+5) // Project 5 years beyond acquisitions

	// Calculate existing portfolio summary if provided
	var existingSummary *investment.ExistingPortfolioSummary
	var combinedMetrics *investment.CombinedPortfolioMetrics
	if req.ExistingPortfolio != nil {
		existingSummary = s.calculator.SummarizeExistingPortfolio(req.ExistingPortfolio)
		if existingSummary != nil {
			// Calculate combined metrics using the total new portfolio metrics
			newMetrics := &investment.PortfolioMetrics{
				PropertyCount:   totalPropertyCount,
				TotalInvestment: totalInvestment,
			}
			totalCashFlow := 0
			for _, plan := range yearlyPlans {
				totalCashFlow += plan.Metrics.ProjectedCashFlow
			}
			newMetrics.AnnualCashFlow = totalCashFlow
			combinedMetrics = s.calculator.CalculateCombinedMetrics(existingSummary, newMetrics)
		}
	}

	multiYearPlan := &investment.MultiYearProjection{
		Years: yearlyPlans,
		CumulativeMetrics: investment.CumulativeMetrics{
			TotalPropertyCount: totalPropertyCount,
			TotalInvestment:    totalInvestment,
			ProjectedValue:     totalInvestment, // Initial value
		},
		GrowthChart: growthChart,
	}

	s.logger.Info("multi-year optimization complete",
		"years", len(yearlyPlans),
		"total_properties", totalPropertyCount,
		"total_investment", totalInvestment,
	)

	return &investment.MultiYearResult{
		MultiYearPlan:     multiYearPlan,
		ExistingPortfolio: existingSummary,
		CombinedMetrics:   combinedMetrics,
		MarketFilters:     filterSummary,
		MarketQuality:     marketQuality,
	}, nil
}

// selectPropertiesWithinBudget selects the best properties within budget
func (s *Service) selectPropertiesWithinBudget(
	candidates []investment.ScoredProperty,
	req investment.OptimizationRequest,
) []investment.ScoredProperty {
	// Sort by overall score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].OverallScore > candidates[j].OverallScore
	})

	selected := make([]investment.ScoredProperty, 0)
	totalDownPayment := 0
	maxDownPayment := req.Budget

	for _, candidate := range candidates {
		if req.MaxProperties > 0 && len(selected) >= req.MaxProperties {
			break
		}

		propDownPayment := int(float64(candidate.Property.Price) * req.DownPaymentPct)
		if totalDownPayment+propDownPayment <= maxDownPayment {
			selected = append(selected, candidate)
			totalDownPayment += propDownPayment
		}
	}

	return selected
}

func (s *Service) selectWithTwoStage(
	candidates []investment.ScoredProperty,
	budget int,
	downPaymentPct float64,
	mortgageRate float64,
	riskTolerance investment.RiskTolerance,
	maxProperties int,
) ([]investment.PropertyInPortfolio, *investment.ConcentrationMetrics, map[string]bool) {
	if len(candidates) == 0 {
		return []investment.PropertyInPortfolio{}, nil, map[string]bool{}
	}

	optimizable := MapToOptimizableProperties(candidates, downPaymentPct, mortgageRate, s.calculator.CalculatePropertyMetrics)
	if len(optimizable) == 0 {
		return []investment.PropertyInPortfolio{}, nil, map[string]bool{}
	}

	config := BuildOptimizerConfig(budget, riskTolerance)
	optResult := OptimizePortfolioTwoStage(optimizable, config)
	selectedIndices := optResult.SelectedIndices

	if len(selectedIndices) == 0 {
		fallbackReq := investment.OptimizationRequest{
			Budget:         budget,
			DownPaymentPct: downPaymentPct,
			MaxProperties:  maxProperties,
		}
		fallback := s.selectPropertiesWithinBudget(candidates, fallbackReq)
		selected := make([]investment.PropertyInPortfolio, 0, len(fallback))
		selectedIDs := make(map[string]bool)
		for _, sp := range fallback {
			pp := s.calculator.CalculatePropertyMetrics(sp.Property, downPaymentPct, mortgageRate)
			pp.Score = sp.OverallScore
			selected = append(selected, pp)
			selectedIDs[sp.Property.ID] = true
		}
		return selected, nil, selectedIDs
	}

	if maxProperties > 0 && len(selectedIndices) > maxProperties {
		sort.SliceStable(selectedIndices, func(i, j int) bool {
			left := candidates[optimizable[selectedIndices[i]].OriginalIndex].OverallScore
			right := candidates[optimizable[selectedIndices[j]].OriginalIndex].OverallScore
			return left > right
		})
		selectedIndices = selectedIndices[:maxProperties]
	}

	selection := make([]bool, len(optimizable))
	for _, idx := range selectedIndices {
		if idx >= 0 && idx < len(selection) {
			selection[idx] = true
		}
	}

	concentration := optResult.Concentration
	if maxProperties > 0 && len(optResult.SelectedIndices) > maxProperties {
		concentration = CalculateConcentrationMetrics(selection, optimizable, config)
	}

	selected := make([]investment.PropertyInPortfolio, 0, len(selectedIndices))
	selectedIDs := make(map[string]bool)
	for _, idx := range selectedIndices {
		sp := candidates[optimizable[idx].OriginalIndex]
		pp := s.calculator.CalculatePropertyMetrics(sp.Property, downPaymentPct, mortgageRate)
		pp.Score = sp.OverallScore
		selected = append(selected, pp)
		selectedIDs[sp.Property.ID] = true
	}

	return selected, &concentration, selectedIDs
}

func buildMarketFilters(strategy investment.InvestmentStrategy) investment.MarketFilterCriteria {
	filters := DefaultMarketFilters()
	switch strategy {
	case investment.StrategyCashFlow:
		filters.MinCapRate = 4
		filters.MaxPriceToIncome = 5
	case investment.StrategyAppreciation:
		filters.MinPriceGrowth5Y = 10
		filters.MinPopulationGrowth = 0
		filters.MinCapRate = 0
	case investment.StrategyRiskAdjusted:
		filters.MaxPriceVolatility = 4
		filters.MaxUnemploymentRate = 6
		filters.MaxVacancyRate = 7
	}
	return filters
}

func filterPropertiesByMarket(
	properties []investment.Property,
	summary investment.MarketFilterSummary,
) []investment.Property {
	if len(summary.Passed) == 0 {
		return nil
	}
	passed := make(map[string]bool, len(summary.Passed))
	for _, result := range summary.Passed {
		passed[result.Location] = true
	}

	filtered := make([]investment.Property, 0, len(properties))
	for _, prop := range properties {
		location := buildLocationKey(prop.City, prop.State)
		if passed[location] {
			filtered = append(filtered, prop)
		}
	}
	return filtered
}

func applyMarketQualityToScored(
	scored []investment.ScoredProperty,
	marketQuality []investment.LocationMarketAnalysis,
) {
	if len(scored) == 0 || len(marketQuality) == 0 {
		return
	}

	qualityByLocation := make(map[string]investment.LocationMarketAnalysis, len(marketQuality))
	for _, analysis := range marketQuality {
		qualityByLocation[analysis.Location] = analysis
	}

	for i := range scored {
		location := buildLocationKey(scored[i].Property.City, scored[i].Property.State)
		if analysis, ok := qualityByLocation[location]; ok {
			scored[i].MarketQualityScore = float64(analysis.MarketQualityScore)
			scored[i].MarketQualityRating = analysis.MarketQualityRating
		}
	}
}

// marketDataResult holds the result of a concurrent market data fetch
type marketDataResult struct {
	location string
	data     *aggregator.MarketData
	err      error
}

func (s *Service) getLocationMarketData(
	ctx context.Context,
	properties []investment.Property,
) map[string]*aggregator.MarketData {
	results := make(map[string]*aggregator.MarketData)
	if s.market == nil {
		s.logger.Warn("market aggregator is nil, cannot fetch market data")
		return results
	}

	// Collect unique locations
	locationSet := make(map[string]struct{})
	for _, prop := range properties {
		location := buildLocationKey(prop.City, prop.State)
		if location != "" {
			locationSet[location] = struct{}{}
		}
	}

	locations := make([]string, 0, len(locationSet))
	for loc := range locationSet {
		locations = append(locations, loc)
	}

	s.logger.Info("fetching market data for locations concurrently", "count", len(locations))

	// Fetch market data concurrently
	resultChan := make(chan marketDataResult, len(locations))

	for _, location := range locations {
		go func(loc string) {
			city, state := splitLocation(loc)
			if city == "" || state == "" {
				resultChan <- marketDataResult{location: loc, err: fmt.Errorf("invalid location format")}
				return
			}
			data, err := s.market.GetMarketData(ctx, city, state)
			resultChan <- marketDataResult{location: loc, data: data, err: err}
		}(location)
	}

	// Collect results
	for i := 0; i < len(locations); i++ {
		result := <-resultChan
		if result.err != nil {
			s.logger.Warn("failed to fetch market data", "location", result.location, "error", result.err)
			continue
		}
		s.logger.Info("market data fetched",
			"location", result.location,
			"employmentGrowth", result.data.EmploymentGrowthRate,
			"populationGrowth", result.data.PopulationGrowthRate,
			"vacancyRate", result.data.VacancyRate,
			"unemploymentRate", result.data.UnemploymentRate,
		)
		results[result.location] = result.data
	}

	return results
}

func buildLocationKey(city, state string) string {
	city = strings.TrimSpace(city)
	state = strings.TrimSpace(state)
	if city == "" || state == "" {
		return ""
	}
	return fmt.Sprintf("%s, %s", city, state)
}

func splitLocation(location string) (string, string) {
	parts := strings.Split(location, ",")
	if len(parts) < 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// calculateAllocations computes allocation breakdown by location
func calculateAllocations(properties []investment.PropertyInPortfolio, totalInvestment int) map[string]investment.LocationAllocation {
	allocations := make(map[string]investment.LocationAllocation)
	if len(properties) == 0 || totalInvestment == 0 {
		return allocations
	}

	for _, prop := range properties {
		location := buildLocationKey(prop.Property.City, prop.Property.State)
		if location == "" {
			continue
		}
		alloc := allocations[location]
		alloc.PropertyCount++
		alloc.InvestmentAmount += prop.Property.Price
		allocations[location] = alloc
	}

	// Calculate percentages
	for loc, alloc := range allocations {
		alloc.Percentage = float64(alloc.InvestmentAmount) / float64(totalInvestment) * 100
		allocations[loc] = alloc
	}

	return allocations
}

// calculateRiskAnalysis computes portfolio risk metrics
func calculateRiskAnalysis(properties []investment.PropertyInPortfolio, scored []investment.ScoredProperty) *investment.RiskAnalysis {
	if len(properties) == 0 {
		return &investment.RiskAnalysis{
			PortfolioRisk:    50,
			RiskDistribution: investment.RiskDistribution{Low: 33, Medium: 34, High: 33},
			Warnings:         []string{},
		}
	}

	// Build score map for selected properties
	scoreMap := make(map[string]float64)
	for _, s := range scored {
		scoreMap[s.Property.ID] = s.OverallScore
	}

	// Calculate risk distribution based on property scores
	var lowCount, medCount, highCount int
	var totalScore float64
	for _, prop := range properties {
		score := scoreMap[prop.Property.ID]
		if score == 0 {
			score = prop.Score
		}
		totalScore += score

		if score >= 70 {
			lowCount++
		} else if score >= 50 {
			medCount++
		} else {
			highCount++
		}
	}

	total := float64(len(properties))
	avgScore := totalScore / total

	// Portfolio risk is inverse of average score
	portfolioRisk := 100 - avgScore

	// Generate warnings
	var warnings []string
	if highCount > 0 {
		warnings = append(warnings, fmt.Sprintf("⚠️ %d properties have elevated risk profiles", highCount))
	}
	if portfolioRisk > 50 {
		warnings = append(warnings, "⚠️ Portfolio risk is above moderate threshold")
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

// calculateDiversificationWithCorrelation computes diversification using actual market correlations
// when available, falling back to simple location counting otherwise.
func (s *Service) calculateDiversificationWithCorrelation(ctx context.Context, properties []investment.PropertyInPortfolio, concentration *investment.ConcentrationMetrics) *investment.DiversificationAnalysis {
	// Collect unique locations
	locationSet := make(map[string]bool)
	for _, prop := range properties {
		location := buildLocationKey(prop.Property.City, prop.Property.State)
		if location != "" {
			locationSet[location] = true
		}
	}
	locations := make([]string, 0, len(locationSet))
	for loc := range locationSet {
		locations = append(locations, loc)
	}

	// Try correlation-based analysis
	if s.correlationAnalyzer != nil && len(locations) >= 2 {
		result := s.correlationAnalyzer.CalculateCorrelations(ctx, locations)

		// Convert opportunities to string messages
		var opportunities []string
		for _, opp := range result.Opportunities {
			opportunities = append(opportunities, fmt.Sprintf("%s ↔ %s: %s", opp.Market1, opp.Market2, opp.Reasoning))
		}

		// Add generic suggestions if score is low
		if result.Score < 40 && len(locations) < 3 {
			opportunities = append(opportunities, "Adding properties in a different region could improve diversification")
		}

		return &investment.DiversificationAnalysis{
			DiversificationScore: result.Score,
			Correlations:         result.Correlations,
			Opportunities:        opportunities,
			DataQualityNote:      result.DataQualityNote,
		}
	}

	// Fallback to simple location-based analysis
	return calculateDiversificationAnalysis(properties, concentration)
}

// calculateDiversificationAnalysis computes diversification metrics (simple fallback)
func calculateDiversificationAnalysis(properties []investment.PropertyInPortfolio, concentration *investment.ConcentrationMetrics) *investment.DiversificationAnalysis {
	if len(properties) == 0 {
		return &investment.DiversificationAnalysis{
			DiversificationScore: 0,
			Correlations:         []investment.MarketCorrelation{},
			Opportunities:        []string{},
		}
	}

	// Count unique locations
	locationSet := make(map[string]bool)
	for _, prop := range properties {
		location := buildLocationKey(prop.Property.City, prop.Property.State)
		if location != "" {
			locationSet[location] = true
		}
	}
	numLocations := len(locationSet)

	// Calculate diversification score
	// More locations = better diversification, up to 5 locations
	diversificationScore := math.Min(100, float64(numLocations)*20)

	// Adjust based on concentration if available
	if concentration != nil && concentration.ConcentrationIndex > 0 {
		diversificationScore = math.Max(0, diversificationScore-concentration.ConcentrationIndex)
	}

	// Generate opportunities
	var opportunities []string
	if numLocations == 1 {
		opportunities = append(opportunities, "Consider expanding to additional markets for better diversification")
	}
	if numLocations < 3 {
		opportunities = append(opportunities, "Adding properties in different metros could reduce concentration risk")
	}

	return &investment.DiversificationAnalysis{
		DiversificationScore: diversificationScore,
		Correlations:         []investment.MarketCorrelation{}, // Could be populated with actual correlation data
		Opportunities:        opportunities,
	}
}

// generateRecommendations creates user-friendly recommendation strings
func generateRecommendations(
	properties []investment.PropertyInPortfolio,
	metrics *investment.PortfolioMetrics,
	riskAnalysis *investment.RiskAnalysis,
	diversification *investment.DiversificationAnalysis,
) []string {
	var recommendations []string

	if metrics == nil {
		return recommendations
	}

	// Diversification recommendation
	if diversification != nil && diversification.DiversificationScore >= 60 {
		recommendations = append(recommendations,
			fmt.Sprintf("✅ Good diversification (%.1f/100) across %d locations",
				diversification.DiversificationScore, len(properties)))
	} else if diversification != nil {
		recommendations = append(recommendations,
			fmt.Sprintf("⚠️ Limited diversification (%.1f/100) - consider additional markets",
				diversification.DiversificationScore))
	}

	// Cash flow recommendation
	if metrics.MonthlyCashFlow > 0 {
		recommendations = append(recommendations,
			fmt.Sprintf("✅ Positive monthly cash flow: $%d", metrics.MonthlyCashFlow))
	} else {
		recommendations = append(recommendations, "⚠️ Negative cash flow - review financing terms")
	}

	// Cap rate recommendation
	if metrics.AvgCapRate >= 6 {
		recommendations = append(recommendations,
			fmt.Sprintf("✅ Strong average cap rate: %.1f%%", metrics.AvgCapRate))
	}

	// Risk recommendation
	if riskAnalysis != nil {
		if riskAnalysis.PortfolioRisk < 40 {
			recommendations = append(recommendations, "✅ Portfolio risk is within acceptable range")
		} else if riskAnalysis.PortfolioRisk > 60 {
			recommendations = append(recommendations, "⚠️ Elevated portfolio risk - review property selection")
		}
	}

	return recommendations
}

// allocatePropertiesForYear selects properties for a specific year's budget
func (s *Service) allocatePropertiesForYear(
	candidates []investment.ScoredProperty,
	yearBudget investment.YearlyBudget,
	downPaymentPct float64,
	mortgageRate float64,
) investment.YearlyAcquisitionPlan {
	// Sort by score
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].OverallScore > candidates[j].OverallScore
	})

	selected := make([]investment.PropertyInPortfolio, 0)
	totalDownPayment := 0
	allocatedCapital := 0
	projectedCashFlow := 0

	for _, candidate := range candidates {
		propDownPayment := int(float64(candidate.Property.Price) * downPaymentPct)
		if totalDownPayment+propDownPayment <= yearBudget.Budget {
			pp := s.calculator.CalculatePropertyMetrics(candidate.Property, downPaymentPct, mortgageRate)
			pp.Score = candidate.OverallScore
			selected = append(selected, pp)
			totalDownPayment += propDownPayment
			allocatedCapital += candidate.Property.Price
			projectedCashFlow += pp.MonthlyCashFlow * 12
		}
	}

	return investment.YearlyAcquisitionPlan{
		Year:             yearBudget.Year,
		Budget:           yearBudget.Budget,
		AllocatedCapital: allocatedCapital,
		Properties:       selected,
		Metrics: investment.YearlyPlanMetrics{
			PropertyCount:     len(selected),
			TotalInvestment:   allocatedCapital,
			ProjectedCashFlow: projectedCashFlow,
		},
	}
}

// buildGrowthChart creates growth projections for visualization
func (s *Service) buildGrowthChart(yearlyPlans []investment.YearlyAcquisitionPlan, totalYears int) []investment.GrowthChartPoint {
	chart := make([]investment.GrowthChartPoint, 0, totalYears)

	// Collect all properties with their acquisition year
	type propertyWithYear struct {
		property        investment.PropertyInPortfolio
		acquisitionYear int
	}
	allProperties := make([]propertyWithYear, 0)
	for _, plan := range yearlyPlans {
		for _, p := range plan.Properties {
			allProperties = append(allProperties, propertyWithYear{
				property:        p,
				acquisitionYear: plan.Year,
			})
		}
	}

	appreciationRate := 0.03 // 3% annual appreciation

	for year := 1; year <= totalYears; year++ {
		totalValue := 0
		totalCashFlow := 0
		totalEquity := 0

		for _, pw := range allProperties {
			if year >= pw.acquisitionYear {
				yearsOwned := year - pw.acquisitionYear + 1
				// Calculate appreciated value
				value := float64(pw.property.Property.Price) * pow(1+appreciationRate, yearsOwned)
				totalValue += int(value)

				// Calculate equity (simplified - assumes principal paydown)
				equity := pw.property.DownPayment + int(float64(pw.property.LoanAmount)*0.02*float64(yearsOwned))
				totalEquity += equity

				// Cash flow grows with rent increases
				cashFlow := float64(pw.property.MonthlyCashFlow*12) * pow(1.02, yearsOwned-1)
				totalCashFlow += int(cashFlow)
			}
		}

		chart = append(chart, investment.GrowthChartPoint{
			Year:           year,
			PortfolioValue: totalValue,
			CashFlow:       totalCashFlow,
			Equity:         totalEquity,
		})
	}

	return chart
}

// buildScoringPrompt constructs the prompt for AI property scoring
func (s *Service) buildScoringPrompt(
	properties []investment.Property,
	profile investment.InvestorProfile,
	existingPortfolio *investment.ExistingPortfolio,
) string {
	prompt := fmt.Sprintf(`Evaluate these %d properties for a real estate investment portfolio.

INVESTOR PROFILE:
- Goal: %s
- Risk Tolerance: %s
- Available Capital: $%d
- Investment Horizon: %s

`, len(properties), profile.Strategy, profile.RiskTolerance, profile.AvailableCapital, profile.InvestmentHorizon)

	// Add existing portfolio context if available
	if existingPortfolio != nil && len(existingPortfolio.Properties) > 0 {
		prompt += "EXISTING PORTFOLIO:\n"
		totalValue := 0
		totalCashFlow := 0
		for _, p := range existingPortfolio.Properties {
			totalValue += p.CurrentValue
			totalCashFlow += p.MonthlyCashFlow
		}
		prompt += fmt.Sprintf("- Properties: %d\n", len(existingPortfolio.Properties))
		prompt += fmt.Sprintf("- Total Value: $%d\n", totalValue)
		prompt += fmt.Sprintf("- Monthly Cash Flow: $%d\n\n", totalCashFlow)
	}

	prompt += "CANDIDATE PROPERTIES:\n"
	for i, p := range properties {
		prompt += fmt.Sprintf(`
Property %d (ID: %s):
- Address: %s, %s, %s
- Price: $%d
- Beds: %d, Baths: %.1f, SqFt: %d
- Estimated Rent: $%d/mo
- Days on Market: %d
`, i+1, p.ID, p.Address, p.City, p.State, p.Price, p.Beds, p.Baths, p.Sqft, p.EstimatedRent, p.DaysOnMarket)
	}

	prompt += `

EVALUATION CRITERIA:
1. Buyability (0-100): Is this a good deal? Consider price vs value, days on market, seller motivation
2. Rentability (0-100): Can it be rented profitably? Consider market rent, vacancy rates, property condition
3. ROI Potential (0-100): Does it meet investor goals? Consider cap rate, cash flow, appreciation potential
4. Portfolio Fit (0-100): Does it complement or duplicate existing holdings?

OUTPUT FORMAT (JSON array):
[
  {
    "propertyId": "prop_123",
    "overallScore": 85,
    "buyabilityScore": 80,
    "rentabilityScore": 90,
    "roiScore": 85,
    "portfolioFit": 85,
    "recommendation": "STRONG_BUY",
    "rationale": "Strong rental demand area with below-market price..."
  }
]

Recommendations: STRONG_BUY (score >= 80), BUY (60-79), HOLD (40-59), PASS (< 40)`

	return prompt
}

// parseScoringResponse parses the AI response into scored properties
func (s *Service) parseScoringResponse(response string, properties []investment.Property) ([]investment.ScoredProperty, error) {
	// Find JSON in response
	start := -1
	end := -1
	for i, c := range response {
		if c == '[' && start == -1 {
			start = i
		}
		if c == ']' {
			end = i + 1
		}
	}

	if start == -1 || end == -1 {
		return nil, fmt.Errorf("no JSON array found in response")
	}

	jsonStr := response[start:end]

	var aiScores []struct {
		PropertyID       string  `json:"propertyId"`
		OverallScore     float64 `json:"overallScore"`
		BuyabilityScore  float64 `json:"buyabilityScore"`
		RentabilityScore float64 `json:"rentabilityScore"`
		ROIScore         float64 `json:"roiScore"`
		PortfolioFit     float64 `json:"portfolioFit"`
		Recommendation   string  `json:"recommendation"`
		Rationale        string  `json:"rationale"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &aiScores); err != nil {
		return nil, fmt.Errorf("failed to parse AI scores: %w", err)
	}

	// Map properties by ID for quick lookup
	propMap := make(map[string]investment.Property)
	for _, p := range properties {
		propMap[p.ID] = p
	}

	scored := make([]investment.ScoredProperty, 0, len(aiScores))
	for _, score := range aiScores {
		prop, ok := propMap[score.PropertyID]
		if !ok {
			continue
		}

		scored = append(scored, investment.ScoredProperty{
			Property:         prop,
			OverallScore:     score.OverallScore,
			BuyabilityScore:  score.BuyabilityScore,
			RentabilityScore: score.RentabilityScore,
			ROIScore:         score.ROIScore,
			PortfolioFit:     score.PortfolioFit,
			Recommendation:   investment.Recommendation(score.Recommendation),
			Rationale:        score.Rationale,
		})
	}

	return scored, nil
}

// fallbackScoring provides algorithmic scoring when AI fails
func (s *Service) fallbackScoring(properties []investment.Property, profile investment.InvestorProfile) []investment.ScoredProperty {
	s.logger.Info("using fallback algorithmic scoring")

	scored := make([]investment.ScoredProperty, 0, len(properties))

	for _, prop := range properties {
		// Calculate basic metrics
		grossYield := 0.0
		if prop.Price > 0 && prop.EstimatedRent > 0 {
			grossYield = (float64(prop.EstimatedRent) * 12 / float64(prop.Price)) * 100
		}

		pricePerSqft := 0.0
		if prop.Sqft > 0 {
			pricePerSqft = float64(prop.Price) / float64(prop.Sqft)
		}

		// Score based on metrics (simplified algorithm)
		buyabilityScore := 50.0
		if prop.DaysOnMarket > 60 {
			buyabilityScore += 20 // Motivated seller
		}
		if prop.DaysOnMarket > 0 && prop.DaysOnMarket < 14 {
			buyabilityScore += 10 // Hot property
		}

		rentabilityScore := 50.0 + (grossYield-5)*10 // Base 50, +10 for each % above 5%
		if rentabilityScore > 100 {
			rentabilityScore = 100
		}
		if rentabilityScore < 0 {
			rentabilityScore = 0
		}

		roiScore := 50.0
		switch profile.Strategy {
		case investment.StrategyCashFlow:
			roiScore = rentabilityScore
		case investment.StrategyAppreciation:
			// Lower price/sqft = better appreciation potential (oversimplified)
			if pricePerSqft < 200 {
				roiScore = 80
			} else if pricePerSqft < 300 {
				roiScore = 60
			}
		case investment.StrategyBalanced:
			roiScore = (rentabilityScore + 50) / 2
		case investment.StrategyRiskAdjusted:
			roiScore = (rentabilityScore + 60) / 2
		}

		portfolioFit := 70.0 // Default good fit

		overallScore := (buyabilityScore + rentabilityScore + roiScore + portfolioFit) / 4

		recommendation := investment.RecommendationPass
		if overallScore >= 80 {
			recommendation = investment.RecommendationStrongBuy
		} else if overallScore >= 60 {
			recommendation = investment.RecommendationBuy
		} else if overallScore >= 40 {
			recommendation = investment.RecommendationHold
		}

		scored = append(scored, investment.ScoredProperty{
			Property:         prop,
			OverallScore:     overallScore,
			BuyabilityScore:  buyabilityScore,
			RentabilityScore: rentabilityScore,
			ROIScore:         roiScore,
			PortfolioFit:     portfolioFit,
			Recommendation:   recommendation,
			Rationale:        fmt.Sprintf("Algorithmic scoring: %.1f%% gross yield, $%.0f/sqft", grossYield, pricePerSqft),
		})
	}

	return scored
}

// filterByRecommendation filters to STRONG_BUY and BUY recommendations
func filterByRecommendation(properties []investment.ScoredProperty) []investment.ScoredProperty {
	filtered := make([]investment.ScoredProperty, 0)
	for _, p := range properties {
		if p.Recommendation == investment.RecommendationStrongBuy ||
			p.Recommendation == investment.RecommendationBuy {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// preScoreAndLimitProperties uses algorithmic scoring to pre-filter properties
// before sending to AI scoring. This prevents timeout issues when too many
// properties (e.g., 900+) would be sent to the AI.
// Returns at most maxCount properties, sorted by algorithmic score.
func preScoreAndLimitProperties(
	properties []investment.Property,
	profile investment.InvestorProfile,
	maxCount int,
	logger *slog.Logger,
) []investment.Property {
	if len(properties) <= maxCount {
		return properties
	}

	logger.Info("pre-scoring properties to limit AI input",
		"input_count", len(properties),
		"max_count", maxCount,
	)

	// Score each property algorithmically
	type scoredProp struct {
		prop  investment.Property
		score float64
	}
	scored := make([]scoredProp, 0, len(properties))

	for _, prop := range properties {
		score := calculateQuickScore(prop, profile)
		scored = append(scored, scoredProp{prop: prop, score: score})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take top N
	result := make([]investment.Property, 0, maxCount)
	for i := 0; i < maxCount && i < len(scored); i++ {
		result = append(result, scored[i].prop)
	}

	logger.Info("pre-scoring complete",
		"output_count", len(result),
		"top_score", scored[0].score,
		"cutoff_score", scored[min(maxCount-1, len(scored)-1)].score,
	)

	return result
}

// calculateQuickScore provides a fast algorithmic score for a property
// without using AI. Used for pre-filtering before AI scoring.
func calculateQuickScore(prop investment.Property, profile investment.InvestorProfile) float64 {
	score := 50.0 // Base score

	// 1. Gross yield score (most important for investment)
	if prop.Price > 0 && prop.EstimatedRent > 0 {
		grossYield := (float64(prop.EstimatedRent) * 12 / float64(prop.Price)) * 100
		// Good yield is 6-12%, excellent is >12%
		if grossYield >= 12 {
			score += 30
		} else if grossYield >= 8 {
			score += 20
		} else if grossYield >= 6 {
			score += 10
		} else if grossYield < 4 {
			score -= 10 // Penalize very low yield
		}
	}

	// 2. Price per square foot (value indicator)
	if prop.Sqft > 0 {
		pricePerSqft := float64(prop.Price) / float64(prop.Sqft)
		// Lower $/sqft generally indicates better value
		if pricePerSqft < 100 {
			score += 15
		} else if pricePerSqft < 150 {
			score += 10
		} else if pricePerSqft < 200 {
			score += 5
		} else if pricePerSqft > 400 {
			score -= 10 // Penalize expensive markets
		}
	}

	// 3. Days on market (motivated seller indicator)
	if prop.DaysOnMarket > 60 {
		score += 10 // Potential negotiating room
	} else if prop.DaysOnMarket > 30 {
		score += 5
	}
	if prop.DaysOnMarket > 0 && prop.DaysOnMarket < 7 {
		score += 5 // Hot property, desirable
	}

	// 4. Property size bonus (family rentals)
	if prop.Beds >= 3 {
		score += 5 // Family-friendly
	}
	if prop.Sqft >= 1500 {
		score += 5
	}

	// 5. Strategy-specific adjustments
	switch profile.Strategy {
	case investment.StrategyCashFlow:
		// Cash flow investors prioritize yield
		if prop.Price > 0 && prop.EstimatedRent > 0 {
			grossYield := (float64(prop.EstimatedRent) * 12 / float64(prop.Price)) * 100
			if grossYield >= 10 {
				score += 10 // Extra bonus for high yield
			}
		}
	case investment.StrategyAppreciation:
		// Appreciation investors prefer lower entry price in growing markets
		if prop.Price < 200000 {
			score += 10 // Lower entry = more appreciation potential
		}
	case investment.StrategyRiskAdjusted:
		// Risk-adjusted prefers moderate yield and price
		if prop.Price >= 150000 && prop.Price <= 350000 {
			score += 10 // Sweet spot for stability
		}
	}

	// Cap score to reasonable range
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}

	return score
}

// filterByDataQuality removes properties with unrealistic data that indicates
// bad data quality or distressed properties that shouldn't be recommended.
// Uses metro-aware minimum prices:
// - Large metros (Chicago, NYC, LA, etc.): $100,000 minimum
// - Medium markets: $75,000 minimum
// - Small cities: $50,000 minimum
// Other thresholds:
// - Price-to-rent ratio < 6: Unrealistic (normal is 12-20x annual rent)
// - Implied cap rate > 20%: Unrealistic (normal is 4-10%)
// - No rent estimate: Cannot evaluate
func filterByDataQuality(properties []investment.Property) []investment.Property {
	const (
		minPriceToRent    = 6.0  // Minimum price / annual rent ratio
		maxImpliedCapRate = 20.0 // Maximum cap rate percentage
	)

	filtered := make([]investment.Property, 0, len(properties))
	for _, p := range properties {
		// Skip if no price
		if p.Price <= 0 {
			continue
		}

		// Get metro-aware minimum price
		minPrice := getMinimumPrice(p.City, p.State)
		if p.Price < minPrice {
			continue
		}

		// Skip if no rent estimate
		if p.EstimatedRent <= 0 {
			continue
		}

		// Calculate price-to-rent ratio (price / annual rent)
		annualRent := p.EstimatedRent * 12
		priceToRent := float64(p.Price) / float64(annualRent)

		// Skip if price-to-rent ratio is too low (unrealistic)
		if priceToRent < minPriceToRent {
			continue
		}

		// Calculate implied cap rate (assuming 40% expenses)
		// NOI = annual rent * 0.95 (vacancy) * 0.65 (expenses) = annual rent * 0.6175
		noi := float64(annualRent) * 0.6175
		impliedCapRate := (noi / float64(p.Price)) * 100

		// Skip if cap rate is unrealistically high
		if impliedCapRate > maxImpliedCapRate {
			continue
		}

		filtered = append(filtered, p)
	}

	return filtered
}

// getMinimumPrice returns the minimum acceptable price based on location.
// Large metros have higher minimums due to higher cost of living.
func getMinimumPrice(city, state string) int {
	// Large metros - $100K minimum
	largeMetros := map[string]bool{
		"chicago":       true,
		"new york":      true,
		"los angeles":   true,
		"san francisco": true,
		"seattle":       true,
		"boston":        true,
		"denver":        true,
		"miami":         true,
		"atlanta":       true,
		"dallas":        true,
		"houston":       true,
		"phoenix":       true,
		"san diego":     true,
		"austin":        true,
		"nashville":     true,
		"portland":      true,
		"minneapolis":   true,
		"tampa":         true,
		"orlando":       true,
		"charlotte":     true,
		"raleigh":       true,
		"san antonio":   true,
		"philadelphia":  true,
		"washington":    true,
	}

	// High cost states - $100K minimum for any city
	highCostStates := map[string]bool{
		"CA": true, "NY": true, "MA": true, "WA": true,
		"CO": true, "HI": true, "NJ": true, "CT": true,
	}

	cityLower := strings.ToLower(city)

	// Check if large metro
	if largeMetros[cityLower] {
		return 100000
	}

	// Check if high-cost state
	if highCostStates[strings.ToUpper(state)] {
		return 100000
	}

	// Medium markets (state capitals, college towns, etc.) - $75K minimum
	mediumMarkets := map[string]bool{
		"indianapolis": true, "columbus": true, "jacksonville": true,
		"memphis": true, "louisville": true, "baltimore": true,
		"milwaukee": true, "albuquerque": true, "tucson": true,
		"fresno": true, "sacramento": true, "kansas city": true,
		"las vegas": true, "cleveland": true, "cincinnati": true,
		"pittsburgh": true, "st louis": true, "san jose": true,
		"oklahoma city": true, "omaha": true, "richmond": true,
		"new orleans": true, "salt lake city": true, "boise": true,
	}

	if mediumMarkets[cityLower] {
		return 75000
	}

	// Small cities - $50K minimum (but not too low)
	return 50000
}

// Simple power function to avoid importing math for this
func pow(base float64, exp int) float64 {
	result := 1.0
	for i := 0; i < exp; i++ {
		result *= base
	}
	return result
}

// PropertyScoringSystemPrompt is the system prompt for property scoring
const PropertyScoringSystemPrompt = `You are a real estate investment analyst specializing in property evaluation. Your role is to objectively evaluate properties for investment potential.

EVALUATION FRAMEWORK:

1. BUYABILITY (Deal Quality)
- Price vs comparable market value
- Days on market (longer = potential motivated seller)
- Price reductions in listing history
- Property condition indicators

2. RENTABILITY (Rental Potential)
- Estimated rent vs market rates
- Neighborhood rental demand
- Property type suitability for renters
- Vacancy rate considerations

3. ROI POTENTIAL (Investment Returns)
- Cap rate analysis (NOI / Price)
- Cash-on-cash return potential
- Appreciation trajectory
- Alignment with investor's stated goals

4. PORTFOLIO FIT (Diversification)
- Geographic diversification vs existing holdings
- Property type mix
- Risk correlation with existing portfolio
- Concentration risk

SCORING GUIDELINES:
- 90-100: Exceptional opportunity, rare find
- 80-89: Strong investment, recommend strongly
- 70-79: Good investment, worth considering
- 60-69: Acceptable, monitor for better options
- 50-59: Marginal, proceed with caution
- Below 50: Not recommended for this investor

OUTPUT REQUIREMENTS:
- Return valid JSON array only
- Include all properties in response
- Provide specific, data-driven rationale
- Consider investor profile in all evaluations

COMPLIANCE NOTE: Present as analysis, not advice. Use neutral language like "data suggests" rather than "you should".`
