package optimization

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

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

	// Score properties using AI
	scoredProperties, err := s.ScoreProperties(ctx, req.Properties, investment.InvestorProfile{
		RiskTolerance:     req.RiskTolerance,
		Strategy:          req.Strategy,
		AvailableCapital:  req.Budget,
		InvestmentHorizon: "5-10 years", // Default
	}, req.ExistingPortfolio)
	if err != nil {
		return nil, fmt.Errorf("failed to score properties: %w", err)
	}

	// Filter to STRONG_BUY and BUY recommendations
	candidates := filterByRecommendation(scoredProperties)

	// Select properties within budget
	selected := s.selectPropertiesWithinBudget(candidates, req)

	// Calculate metrics for selected properties
	portfolioProperties := make([]investment.PropertyInPortfolio, 0, len(selected))
	for _, sp := range selected {
		pp := s.calculator.CalculatePropertyMetrics(sp.Property, req.DownPaymentPct, req.MortgageRate)
		pp.Score = sp.OverallScore
		portfolioProperties = append(portfolioProperties, pp)
	}

	metrics := s.calculator.CalculateMetrics(portfolioProperties)

	s.logger.Info("optimization complete",
		"selected_count", len(portfolioProperties),
		"total_investment", metrics.TotalInvestment,
		"annual_cash_flow", metrics.AnnualCashFlow,
	)

	return &investment.OptimizationResult{
		SelectedProperties: portfolioProperties,
		Metrics:            *metrics,
		ScoredProperties:   scoredProperties,
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

	// Score all properties upfront
	profile := investment.InvestorProfile{
		RiskTolerance:     req.RiskTolerance,
		Strategy:          req.Strategy,
		AvailableCapital:  req.YearlyBudgets[0].Budget, // Use first year budget for initial scoring
		InvestmentHorizon: fmt.Sprintf("%d years", len(req.YearlyBudgets)),
	}

	scoredProperties, err := s.ScoreProperties(ctx, req.Properties, profile, req.ExistingPortfolio)
	if err != nil {
		return nil, fmt.Errorf("failed to score properties: %w", err)
	}

	// Filter to recommendations
	candidates := filterByRecommendation(scoredProperties)
	remaining := make([]investment.ScoredProperty, len(candidates))
	copy(remaining, candidates)

	// Allocate properties to each year
	yearlyPlans := make([]investment.YearlyAcquisitionPlan, 0, len(req.YearlyBudgets))
	totalPropertyCount := 0
	totalInvestment := 0

	for _, yearBudget := range req.YearlyBudgets {
		yearPlan := s.allocatePropertiesForYear(remaining, yearBudget, req.DownPaymentPct, req.MortgageRate)
		yearlyPlans = append(yearlyPlans, yearPlan)

		// Remove selected properties from pool
		selectedIDs := make(map[string]bool)
		for _, pp := range yearPlan.Properties {
			selectedIDs[pp.Property.ID] = true
		}

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
	maxDownPayment := int(float64(req.Budget) * req.DownPaymentPct)

	for _, candidate := range candidates {
		if len(selected) >= req.MaxProperties {
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
		property      investment.PropertyInPortfolio
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

		rentabilityScore := 50.0 + (grossYield - 5) * 10 // Base 50, +10 for each % above 5%
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
