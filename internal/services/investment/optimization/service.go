package optimization

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"

	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/projection"
	"github.com/estara-ai/www/internal/services/market/aggregator"
)

// Service provides portfolio optimization with AI-driven property scoring
type Service struct {
	client     *anthropic.Client
	market     *aggregator.Aggregator
	cache      *cache.HybridCache
	calculator *projection.Calculator
	logger     *slog.Logger
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

	marketQuality := CalculateMarketQualityScores(locationMarketData)

	// Score properties using AI
	scoredProperties, err := s.ScoreProperties(ctx, filteredProperties, investment.InvestorProfile{
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

	// Calculate diversification analysis
	diversificationAnalysis := calculateDiversificationAnalysis(portfolioProperties, concentration)

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
		RiskAnalysis:            riskAnalysis,
		DiversificationAnalysis: diversificationAnalysis,
		Recommendations:         recommendations,
	}, nil
}

// ScoreProperties uses AI to evaluate properties on buyability, rentability, ROI
func (s *Service) ScoreProperties(
	ctx context.Context,
	properties []investment.Property,
	profile investment.InvestorProfile,
	existingPortfolio *investment.ExistingPortfolio,
) ([]investment.ScoredProperty, error) {
	if len(properties) == 0 {
		return []investment.ScoredProperty{}, nil
	}

	s.logger.Info("scoring properties with AI",
		"property_count", len(properties),
		"strategy", profile.Strategy,
		"risk_tolerance", profile.RiskTolerance,
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
		return s.fallbackScoring(properties, profile), nil
	}

	return scored, nil
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

	scoredProperties, err := s.ScoreProperties(ctx, filteredProperties, profile, req.ExistingPortfolio)
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

// calculateDiversificationAnalysis computes diversification metrics
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

// filterByDataQuality removes properties with unrealistic data that indicates
// bad data quality or distressed properties that shouldn't be recommended.
// Thresholds:
// - Price < $40,000: Likely distressed/uninhabitable
// - Price-to-rent ratio < 6: Unrealistic (normal is 12-20x annual rent)
// - Implied cap rate > 20%: Unrealistic (normal is 4-10%)
// - No rent estimate: Cannot evaluate
func filterByDataQuality(properties []investment.Property) []investment.Property {
	const (
		minPrice          = 40000  // $40k minimum
		minPriceToRent    = 6.0    // Minimum price / annual rent ratio
		maxImpliedCapRate = 20.0   // Maximum cap rate percentage
	)

	filtered := make([]investment.Property, 0, len(properties))
	for _, p := range properties {
		// Skip if no price
		if p.Price <= 0 {
			continue
		}

		// Skip very cheap properties (likely distressed)
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
