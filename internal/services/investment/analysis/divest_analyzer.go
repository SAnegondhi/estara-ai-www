// Package analysis provides financial engineering services for investment planning.
// Part of ADR-059: Investment Planning Enhancement - Selection Mode
package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	"github.com/estara-ai/www/internal/services/investment"
)

// DivestAnalyzer evaluates properties for keep/divest recommendations
type DivestAnalyzer struct {
	logger *slog.Logger
}

// NewDivestAnalyzer creates a new divest analyzer
func NewDivestAnalyzer(logger *slog.Logger) *DivestAnalyzer {
	if logger == nil {
		logger = slog.Default()
	}
	return &DivestAnalyzer{
		logger: logger.With("component", "divest_analyzer"),
	}
}

// DivestAnalysisParams holds parameters for divest analysis
type DivestAnalysisParams struct {
	Properties        []investment.Property
	Strategy          investment.InvestmentStrategy
	RiskTolerance     investment.RiskTolerance
	AvailableBudget   int
	MortgageRate      float64
	DownPaymentPct    float64
	MarketData        map[string]*MarketMetrics // location -> metrics
	ExistingPortfolio []investment.Property     // For portfolio fit scoring
}

// MarketMetrics holds market quality metrics for a location
type MarketMetrics struct {
	Location           string
	PriceGrowth5Y      float64 // 5-year price appreciation %
	RentGrowth5Y       float64 // 5-year rent growth %
	EmploymentGrowth   float64 // Employment growth rate %
	PopulationGrowth   float64 // Population growth rate %
	AvgCapRate         float64 // Market average cap rate
	MedianDaysOnMarket int     // Market velocity
	VacancyRate        float64 // Local vacancy rate
}

// AnalyzeProperties evaluates each property and returns recommendations
func (a *DivestAnalyzer) AnalyzeProperties(
	ctx context.Context,
	params DivestAnalysisParams,
) ([]investment.PropertyRecommendation, error) {
	a.logger.Info("analyzing properties for keep/divest recommendations",
		"propertyCount", len(params.Properties),
		"strategy", params.Strategy,
	)

	recommendations := make([]investment.PropertyRecommendation, 0, len(params.Properties))

	// Calculate portfolio-level metrics for comparison
	portfolioMetrics := a.calculatePortfolioMetrics(params.Properties, params.ExistingPortfolio)

	for _, prop := range params.Properties {
		rec, err := a.analyzeProperty(ctx, prop, params, portfolioMetrics)
		if err != nil {
			a.logger.Warn("failed to analyze property",
				"propertyId", prop.ID,
				"error", err,
			)
			continue
		}
		recommendations = append(recommendations, rec)
	}

	// Sort by action priority: DIVEST first (for capital release), then KEEP
	a.sortByActionPriority(recommendations)

	return recommendations, nil
}

// analyzeProperty evaluates a single property
func (a *DivestAnalyzer) analyzeProperty(
	ctx context.Context,
	prop investment.Property,
	params DivestAnalysisParams,
	portfolioMetrics *portfolioLevelMetrics,
) (investment.PropertyRecommendation, error) {
	// Get market metrics for this property's location
	location := fmt.Sprintf("%s, %s", prop.City, prop.State)
	marketMetrics := params.MarketData[location]
	if marketMetrics == nil {
		// Use default metrics if not available
		marketMetrics = &MarketMetrics{
			Location:      location,
			PriceGrowth5Y: 3.0, // Conservative default
			RentGrowth5Y:  2.0,
			AvgCapRate:    6.0,
		}
	}

	// Calculate individual scores
	scores := a.calculateScores(prop, params, portfolioMetrics, marketMetrics)

	// Determine action based on total score
	action, confidence := a.determineAction(scores.TotalScore)

	// Generate reasoning
	reasoning := a.generateReasoning(prop, scores, action, marketMetrics)

	// Calculate projected metrics
	projectedMetrics := a.calculateProjectedMetrics(prop, params, marketMetrics)

	rec := investment.PropertyRecommendation{
		Property: investment.PropertyInPortfolio{
			Property:       prop,
			DownPayment:    int(float64(prop.Price) * params.DownPaymentPct),
			LoanAmount:     int(float64(prop.Price) * (1 - params.DownPaymentPct)),
			CapRate:        a.calculateCapRate(prop),
			MonthlyCashFlow: a.calculateMonthlyCashFlow(prop, params),
		},
		Action:     action,
		Confidence: confidence,
		Reasoning:  reasoning,
		Scores:     scores,
		Metrics:    projectedMetrics,
	}

	// Add divest-specific fields
	if action == investment.RecommendationActionDivest {
		rec.DivestReason = a.generateDivestReason(scores)
		rec.EstimatedSaleProceeds = a.estimateSaleProceeds(prop)
		rec.BetterUseOfCapital = a.suggestBetterUse(prop, params)
	}

	return rec, nil
}

// calculateScores computes the 4-factor scoring breakdown
func (a *DivestAnalyzer) calculateScores(
	prop investment.Property,
	params DivestAnalysisParams,
	portfolioMetrics *portfolioLevelMetrics,
	marketMetrics *MarketMetrics,
) investment.PropertyRecommendationScores {
	// Performance Score (40% weight, max 40 points)
	performanceScore := a.calculatePerformanceScore(prop, params, portfolioMetrics, marketMetrics)

	// Market Quality Score (30% weight, max 30 points)
	marketQualityScore := a.calculateMarketQualityScore(marketMetrics)

	// Portfolio Fit Score (20% weight, max 20 points)
	portfolioFitScore := a.calculatePortfolioFitScore(prop, params.ExistingPortfolio)

	// Opportunity Cost Score (10% weight, max 10 points)
	opportunityCost := a.calculateOpportunityCost(prop, params, marketMetrics)

	totalScore := performanceScore + marketQualityScore + portfolioFitScore + opportunityCost

	return investment.PropertyRecommendationScores{
		PerformanceScore:   performanceScore,
		MarketQualityScore: marketQualityScore,
		PortfolioFitScore:  portfolioFitScore,
		OpportunityCost:    opportunityCost,
		TotalScore:         totalScore,
	}
}

// calculatePerformanceScore evaluates property performance (0-40 points)
func (a *DivestAnalyzer) calculatePerformanceScore(
	prop investment.Property,
	params DivestAnalysisParams,
	portfolioMetrics *portfolioLevelMetrics,
	marketMetrics *MarketMetrics,
) float64 {
	score := 0.0
	maxScore := 40.0

	// Cap rate vs market average (0-15 points)
	propCapRate := a.calculateCapRate(prop)
	if marketMetrics.AvgCapRate > 0 {
		capRateRatio := propCapRate / marketMetrics.AvgCapRate
		if capRateRatio >= 1.2 {
			score += 15
		} else if capRateRatio >= 1.0 {
			score += 15 * (capRateRatio - 0.8) / 0.4
		} else if capRateRatio >= 0.8 {
			score += 10 * (capRateRatio - 0.6) / 0.4
		} else {
			score += 5 * capRateRatio / 0.8
		}
	} else {
		// Default scoring based on absolute cap rate
		if propCapRate >= 8 {
			score += 15
		} else if propCapRate >= 6 {
			score += 10
		} else if propCapRate >= 4 {
			score += 5
		}
	}

	// Cash-on-cash return vs portfolio average (0-15 points)
	propCoC := a.calculateCashOnCash(prop, params)
	if portfolioMetrics != nil && portfolioMetrics.avgCashOnCash > 0 {
		cocRatio := propCoC / portfolioMetrics.avgCashOnCash
		if cocRatio >= 1.2 {
			score += 15
		} else if cocRatio >= 1.0 {
			score += 12
		} else if cocRatio >= 0.8 {
			score += 8
		} else {
			score += 4
		}
	} else {
		// Absolute CoC scoring
		if propCoC >= 12 {
			score += 15
		} else if propCoC >= 8 {
			score += 10
		} else if propCoC >= 5 {
			score += 5
		}
	}

	// DSCR scoring (0-10 points)
	dscr := a.calculateDSCR(prop, params)
	if dscr >= 1.5 {
		score += 10
	} else if dscr >= 1.25 {
		score += 7
	} else if dscr >= 1.0 {
		score += 4
	} else {
		score += 0 // Below 1.0 DSCR is risky
	}

	return math.Min(score, maxScore)
}

// calculateMarketQualityScore evaluates market conditions (0-30 points)
func (a *DivestAnalyzer) calculateMarketQualityScore(marketMetrics *MarketMetrics) float64 {
	score := 0.0
	maxScore := 30.0

	// 5-year price appreciation (0-12 points)
	if marketMetrics.PriceGrowth5Y >= 5 {
		score += 12
	} else if marketMetrics.PriceGrowth5Y >= 3 {
		score += 9
	} else if marketMetrics.PriceGrowth5Y >= 1 {
		score += 6
	} else if marketMetrics.PriceGrowth5Y >= 0 {
		score += 3
	}

	// Employment growth (0-9 points)
	if marketMetrics.EmploymentGrowth >= 3 {
		score += 9
	} else if marketMetrics.EmploymentGrowth >= 1.5 {
		score += 6
	} else if marketMetrics.EmploymentGrowth >= 0 {
		score += 3
	}

	// Population growth (0-9 points)
	if marketMetrics.PopulationGrowth >= 2 {
		score += 9
	} else if marketMetrics.PopulationGrowth >= 1 {
		score += 6
	} else if marketMetrics.PopulationGrowth >= 0 {
		score += 3
	}

	return math.Min(score, maxScore)
}

// calculatePortfolioFitScore evaluates diversification impact (0-20 points)
func (a *DivestAnalyzer) calculatePortfolioFitScore(
	prop investment.Property,
	existingPortfolio []investment.Property,
) float64 {
	if len(existingPortfolio) == 0 {
		return 15.0 // No portfolio, assume good fit
	}

	score := 0.0
	maxScore := 20.0

	// Geographic diversification (0-10 points)
	location := fmt.Sprintf("%s, %s", prop.City, prop.State)
	locationCount := 0
	totalProps := len(existingPortfolio)
	for _, p := range existingPortfolio {
		if fmt.Sprintf("%s, %s", p.City, p.State) == location {
			locationCount++
		}
	}
	concentrationRatio := float64(locationCount) / float64(totalProps)
	if concentrationRatio <= 0.2 {
		score += 10 // Good diversification
	} else if concentrationRatio <= 0.4 {
		score += 7
	} else if concentrationRatio <= 0.6 {
		score += 4
	} else {
		score += 2 // High concentration
	}

	// Property type diversification (0-10 points)
	typeCount := 0
	for _, p := range existingPortfolio {
		if p.PropertyType == prop.PropertyType {
			typeCount++
		}
	}
	typeConcentration := float64(typeCount) / float64(totalProps)
	if typeConcentration <= 0.3 {
		score += 10
	} else if typeConcentration <= 0.5 {
		score += 7
	} else {
		score += 4
	}

	return math.Min(score, maxScore)
}

// calculateOpportunityCost evaluates if capital could be better used (0-10 points)
func (a *DivestAnalyzer) calculateOpportunityCost(
	prop investment.Property,
	params DivestAnalysisParams,
	marketMetrics *MarketMetrics,
) float64 {
	score := 10.0 // Start with max, deduct for opportunity cost indicators

	propCapRate := a.calculateCapRate(prop)

	// If property cap rate is significantly below market, there's opportunity cost
	if marketMetrics.AvgCapRate > 0 && propCapRate < marketMetrics.AvgCapRate*0.8 {
		score -= 5
	}

	// If property is in a declining market
	if marketMetrics.PriceGrowth5Y < 0 {
		score -= 3
	}

	// If property has high vacancy risk
	if marketMetrics.VacancyRate > 10 {
		score -= 2
	}

	return math.Max(score, 0)
}

// determineAction converts score to action recommendation
func (a *DivestAnalyzer) determineAction(totalScore float64) (investment.RecommendationAction, float64) {
	if totalScore > 60 {
		confidence := math.Min(100, 50+(totalScore-60)*2.5)
		return investment.RecommendationActionKeep, confidence
	} else if totalScore >= 40 {
		// Borderline - still recommend KEEP but with lower confidence
		confidence := 40 + (totalScore-40)*0.5
		return investment.RecommendationActionKeep, confidence
	} else {
		confidence := math.Min(100, 50+(40-totalScore)*2.5)
		return investment.RecommendationActionDivest, confidence
	}
}

// generateReasoning creates human-readable reasoning for the recommendation
func (a *DivestAnalyzer) generateReasoning(
	prop investment.Property,
	scores investment.PropertyRecommendationScores,
	action investment.RecommendationAction,
	marketMetrics *MarketMetrics,
) string {
	if action == investment.RecommendationActionKeep {
		if scores.TotalScore >= 70 {
			return fmt.Sprintf("Strong performer with %.1f/100 score. Good cap rate relative to market and solid market fundamentals in %s.",
				scores.TotalScore, prop.City)
		}
		return fmt.Sprintf("Moderate performer with %.1f/100 score. Consider monitoring market conditions in %s.",
			scores.TotalScore, prop.City)
	}

	// DIVEST reasoning
	weakAreas := []string{}
	if scores.PerformanceScore < 20 {
		weakAreas = append(weakAreas, "underperforming returns")
	}
	if scores.MarketQualityScore < 15 {
		weakAreas = append(weakAreas, "weak market fundamentals")
	}
	if scores.PortfolioFitScore < 10 {
		weakAreas = append(weakAreas, "high concentration risk")
	}
	if scores.OpportunityCost < 5 {
		weakAreas = append(weakAreas, "better capital deployment opportunities exist")
	}

	if len(weakAreas) == 0 {
		weakAreas = append(weakAreas, "below-threshold overall performance")
	}

	return fmt.Sprintf("Recommendation to divest based on: %v. Score: %.1f/100.",
		weakAreas, scores.TotalScore)
}

// generateDivestReason creates a specific divest reason
func (a *DivestAnalyzer) generateDivestReason(scores investment.PropertyRecommendationScores) string {
	if scores.PerformanceScore < 15 {
		return "Low returns relative to portfolio and market averages"
	}
	if scores.MarketQualityScore < 10 {
		return "Weak local market fundamentals"
	}
	if scores.PortfolioFitScore < 8 {
		return "High geographic concentration risk"
	}
	return "Overall underperformance suggests better capital allocation opportunities"
}

// estimateSaleProceeds estimates net proceeds from selling
func (a *DivestAnalyzer) estimateSaleProceeds(prop investment.Property) int {
	// Assume 6% selling costs (agent commission, closing costs)
	sellingCosts := 0.06
	netProceeds := float64(prop.Price) * (1 - sellingCosts)
	return int(netProceeds)
}

// suggestBetterUse suggests how the freed capital could be better used
func (a *DivestAnalyzer) suggestBetterUse(prop investment.Property, params DivestAnalysisParams) string {
	equity := a.estimateSaleProceeds(prop)
	if equity >= 50000 {
		return fmt.Sprintf("Freed capital of ~$%dK could be deployed in higher-growth markets or diversified across multiple properties",
			equity/1000)
	}
	return "Consider reinvesting in higher-yield opportunities"
}

// Helper methods

func (a *DivestAnalyzer) calculateCapRate(prop investment.Property) float64 {
	if prop.Price == 0 || prop.EstimatedRent == 0 {
		return 0
	}
	annualRent := float64(prop.EstimatedRent) * 12
	// Assume 50% operating expenses
	noi := annualRent * 0.5
	return (noi / float64(prop.Price)) * 100
}

func (a *DivestAnalyzer) calculateCashOnCash(prop investment.Property, params DivestAnalysisParams) float64 {
	downPayment := float64(prop.Price) * params.DownPaymentPct
	if downPayment == 0 {
		return 0
	}
	annualCashFlow := float64(a.calculateMonthlyCashFlow(prop, params)) * 12
	return (annualCashFlow / downPayment) * 100
}

func (a *DivestAnalyzer) calculateMonthlyCashFlow(prop investment.Property, params DivestAnalysisParams) int {
	if prop.EstimatedRent == 0 {
		return 0
	}

	monthlyRent := prop.EstimatedRent
	// Assume 50% operating expenses
	monthlyExpenses := int(float64(monthlyRent) * 0.5)

	// Calculate monthly mortgage payment
	loanAmount := float64(prop.Price) * (1 - params.DownPaymentPct)
	monthlyRate := params.MortgageRate / 100 / 12
	numPayments := 360.0 // 30 years

	var monthlyMortgage float64
	if monthlyRate > 0 {
		monthlyMortgage = loanAmount * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) /
			(math.Pow(1+monthlyRate, numPayments) - 1)
	}

	return monthlyRent - monthlyExpenses - int(monthlyMortgage)
}

func (a *DivestAnalyzer) calculateDSCR(prop investment.Property, params DivestAnalysisParams) float64 {
	if prop.EstimatedRent == 0 {
		return 0
	}

	// NOI = Rent - Operating Expenses (assume 50%)
	annualNOI := float64(prop.EstimatedRent) * 12 * 0.5

	// Annual debt service
	loanAmount := float64(prop.Price) * (1 - params.DownPaymentPct)
	monthlyRate := params.MortgageRate / 100 / 12
	numPayments := 360.0

	var annualDebtService float64
	if monthlyRate > 0 {
		monthlyPayment := loanAmount * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) /
			(math.Pow(1+monthlyRate, numPayments) - 1)
		annualDebtService = monthlyPayment * 12
	}

	if annualDebtService == 0 {
		return 10.0 // No debt = infinite DSCR, cap at 10
	}

	return annualNOI / annualDebtService
}

func (a *DivestAnalyzer) calculateProjectedMetrics(
	prop investment.Property,
	params DivestAnalysisParams,
	marketMetrics *MarketMetrics,
) investment.PropertyRecommendationMetrics {
	return investment.PropertyRecommendationMetrics{
		ProjectedAnnualCashFlow: a.calculateMonthlyCashFlow(prop, params) * 12,
		ProjectedCapRate:        a.calculateCapRate(prop),
		ProjectedROI:            a.calculateCashOnCash(prop, params),
		ProjectedAppreciation:   marketMetrics.PriceGrowth5Y,
	}
}

type portfolioLevelMetrics struct {
	avgCapRate    float64
	avgCashOnCash float64
	totalValue    int
}

func (a *DivestAnalyzer) calculatePortfolioMetrics(
	properties []investment.Property,
	existing []investment.Property,
) *portfolioLevelMetrics {
	all := append(properties, existing...)
	if len(all) == 0 {
		return nil
	}

	var totalCapRate, totalValue float64
	for _, p := range all {
		totalCapRate += a.calculateCapRate(p)
		totalValue += float64(p.Price)
	}

	return &portfolioLevelMetrics{
		avgCapRate: totalCapRate / float64(len(all)),
		totalValue: int(totalValue),
	}
}

func (a *DivestAnalyzer) sortByActionPriority(recommendations []investment.PropertyRecommendation) {
	// Sort: DIVEST first (to identify freed capital), then KEEP
	// Within each group, sort by confidence descending
	for i := 0; i < len(recommendations)-1; i++ {
		for j := i + 1; j < len(recommendations); j++ {
			shouldSwap := false
			if recommendations[i].Action == investment.RecommendationActionKeep &&
				recommendations[j].Action == investment.RecommendationActionDivest {
				shouldSwap = true
			} else if recommendations[i].Action == recommendations[j].Action &&
				recommendations[i].Confidence < recommendations[j].Confidence {
				shouldSwap = true
			}
			if shouldSwap {
				recommendations[i], recommendations[j] = recommendations[j], recommendations[i]
			}
		}
	}
}
