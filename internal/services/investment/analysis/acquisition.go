// Package analysis provides financial engineering services for investment planning.
// Part of ADR-059: Investment Planning Enhancement - Selection Mode
package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/estara-ai/www/internal/services/investment"
)

// AcquisitionRecommender generates acquisition recommendations within budget
type AcquisitionRecommender struct {
	logger *slog.Logger
}

// NewAcquisitionRecommender creates a new acquisition recommender
func NewAcquisitionRecommender(logger *slog.Logger) *AcquisitionRecommender {
	if logger == nil {
		logger = slog.Default()
	}
	return &AcquisitionRecommender{
		logger: logger.With("component", "acquisition_recommender"),
	}
}

// AcquisitionParams holds parameters for acquisition recommendations
type AcquisitionParams struct {
	// Available budget for new acquisitions
	AvailableBudget int

	// Candidate properties to consider (from property search)
	Candidates []investment.ScoredProperty

	// Existing properties in portfolio (for diversification scoring)
	ExistingPortfolio []investment.Property

	// Properties being kept (from divest analysis)
	KeepProperties []investment.Property

	// Financial parameters
	DownPaymentPct float64
	MortgageRate   float64

	// Strategy and constraints
	Strategy      investment.InvestmentStrategy
	RiskTolerance investment.RiskTolerance

	// Maximum number of acquisitions to recommend
	MaxAcquisitions int
}

// GenerateRecommendations creates acquisition recommendations within budget
func (r *AcquisitionRecommender) GenerateRecommendations(
	ctx context.Context,
	params AcquisitionParams,
) ([]investment.PropertyRecommendation, error) {
	r.logger.Info("generating acquisition recommendations",
		"candidateCount", len(params.Candidates),
		"budget", params.AvailableBudget,
		"maxAcquisitions", params.MaxAcquisitions,
	)

	if params.AvailableBudget <= 0 {
		r.logger.Info("no budget available for acquisitions")
		return nil, nil
	}

	if len(params.Candidates) == 0 {
		r.logger.Info("no candidates available for acquisition")
		return nil, nil
	}

	// Build combined portfolio context for diversification scoring
	portfolioContext := append(params.ExistingPortfolio, params.KeepProperties...)

	// Filter candidates to only STRONG_BUY and BUY recommendations
	eligibleCandidates := r.filterEligibleCandidates(params.Candidates)
	if len(eligibleCandidates) == 0 {
		r.logger.Info("no eligible candidates after filtering")
		return nil, nil
	}

	// Score candidates for portfolio complementarity
	scoredCandidates := r.scoreForComplementarity(eligibleCandidates, portfolioContext, params)

	// Sort by score descending
	sort.Slice(scoredCandidates, func(i, j int) bool {
		return scoredCandidates[i].score > scoredCandidates[j].score
	})

	// Select within budget constraints
	recommendations := r.selectWithinBudget(scoredCandidates, params)

	r.logger.Info("acquisition recommendations generated",
		"recommendationCount", len(recommendations),
	)

	return recommendations, nil
}

// filterEligibleCandidates keeps only STRONG_BUY and BUY recommendations
func (r *AcquisitionRecommender) filterEligibleCandidates(
	candidates []investment.ScoredProperty,
) []investment.ScoredProperty {
	eligible := make([]investment.ScoredProperty, 0, len(candidates))
	for _, c := range candidates {
		if c.Recommendation == investment.RecommendationStrongBuy ||
			c.Recommendation == investment.RecommendationBuy {
			eligible = append(eligible, c)
		}
	}
	return eligible
}

type scoredCandidate struct {
	property investment.ScoredProperty
	score    float64
	reason   string
}

// scoreForComplementarity scores candidates based on how well they complement the portfolio
func (r *AcquisitionRecommender) scoreForComplementarity(
	candidates []investment.ScoredProperty,
	portfolioContext []investment.Property,
	params AcquisitionParams,
) []scoredCandidate {
	scored := make([]scoredCandidate, 0, len(candidates))

	// Calculate current portfolio distribution
	locationDistribution := r.calculateLocationDistribution(portfolioContext)
	typeDistribution := r.calculateTypeDistribution(portfolioContext)

	for _, candidate := range candidates {
		score := r.calculateComplementarityScore(
			candidate,
			locationDistribution,
			typeDistribution,
			params,
		)

		reason := r.generateAcquisitionReason(candidate, score, locationDistribution)

		scored = append(scored, scoredCandidate{
			property: candidate,
			score:    score,
			reason:   reason,
		})
	}

	return scored
}

// calculateLocationDistribution calculates the % of portfolio in each location
func (r *AcquisitionRecommender) calculateLocationDistribution(
	properties []investment.Property,
) map[string]float64 {
	if len(properties) == 0 {
		return make(map[string]float64)
	}

	counts := make(map[string]int)
	for _, p := range properties {
		loc := fmt.Sprintf("%s, %s", p.City, p.State)
		counts[loc]++
	}

	distribution := make(map[string]float64)
	for loc, count := range counts {
		distribution[loc] = float64(count) / float64(len(properties))
	}

	return distribution
}

// calculateTypeDistribution calculates the % of portfolio by property type
func (r *AcquisitionRecommender) calculateTypeDistribution(
	properties []investment.Property,
) map[string]float64 {
	if len(properties) == 0 {
		return make(map[string]float64)
	}

	counts := make(map[string]int)
	for _, p := range properties {
		propType := p.PropertyType
		if propType == "" {
			propType = "Unknown"
		}
		counts[propType]++
	}

	distribution := make(map[string]float64)
	for ptype, count := range counts {
		distribution[ptype] = float64(count) / float64(len(properties))
	}

	return distribution
}

// calculateComplementarityScore scores how well a candidate complements the portfolio
func (r *AcquisitionRecommender) calculateComplementarityScore(
	candidate investment.ScoredProperty,
	locationDist map[string]float64,
	typeDist map[string]float64,
	params AcquisitionParams,
) float64 {
	score := 0.0

	// Base score from AI scoring (0-50 points)
	score += candidate.OverallScore * 0.5

	// Diversification bonus for underrepresented locations (0-25 points)
	candLocation := fmt.Sprintf("%s, %s", candidate.Property.City, candidate.Property.State)
	locationConcentration := locationDist[candLocation]
	if locationConcentration == 0 {
		score += 25 // New market = maximum diversification bonus
	} else if locationConcentration < 0.2 {
		score += 20
	} else if locationConcentration < 0.4 {
		score += 10
	} else {
		score += 5 // High concentration = minimal bonus
	}

	// Diversification bonus for underrepresented property types (0-15 points)
	candType := candidate.Property.PropertyType
	if candType == "" {
		candType = "Unknown"
	}
	typeConcentration := typeDist[candType]
	if typeConcentration == 0 {
		score += 15
	} else if typeConcentration < 0.3 {
		score += 10
	} else {
		score += 5
	}

	// Strategy alignment bonus (0-10 points)
	score += r.calculateStrategyBonus(candidate, params.Strategy)

	return score
}

// calculateStrategyBonus adds points based on strategy alignment
func (r *AcquisitionRecommender) calculateStrategyBonus(
	candidate investment.ScoredProperty,
	strategy investment.InvestmentStrategy,
) float64 {
	switch strategy {
	case investment.StrategyCashFlow:
		// Prioritize ROI score
		return candidate.ROIScore * 0.1
	case investment.StrategyAppreciation:
		// Prioritize market quality
		return candidate.MarketQualityScore * 0.1
	case investment.StrategyRiskAdjusted:
		// Balance between buyability and rentability
		return (candidate.BuyabilityScore + candidate.RentabilityScore) * 0.05
	default: // Balanced
		return candidate.OverallScore * 0.1
	}
}

// generateAcquisitionReason creates reasoning for the acquisition
func (r *AcquisitionRecommender) generateAcquisitionReason(
	candidate investment.ScoredProperty,
	score float64,
	locationDist map[string]float64,
) string {
	candLocation := fmt.Sprintf("%s, %s", candidate.Property.City, candidate.Property.State)
	concentration := locationDist[candLocation]

	if concentration == 0 {
		return fmt.Sprintf("Adds geographic diversification to %s with strong AI score of %.0f",
			candLocation, candidate.OverallScore)
	} else if concentration < 0.3 {
		return fmt.Sprintf("Strengthens position in %s market with balanced portfolio fit (score: %.0f)",
			candLocation, candidate.OverallScore)
	}
	return fmt.Sprintf("Strong investment opportunity with AI score of %.0f. %s",
		candidate.OverallScore, candidate.Rationale)
}

// selectWithinBudget selects acquisitions that fit within budget
func (r *AcquisitionRecommender) selectWithinBudget(
	scoredCandidates []scoredCandidate,
	params AcquisitionParams,
) []investment.PropertyRecommendation {
	recommendations := make([]investment.PropertyRecommendation, 0)

	remainingBudget := params.AvailableBudget
	maxAcquisitions := params.MaxAcquisitions
	if maxAcquisitions <= 0 {
		maxAcquisitions = 5 // Default
	}

	priority := 1
	for _, sc := range scoredCandidates {
		if len(recommendations) >= maxAcquisitions {
			break
		}

		requiredDownPayment := int(float64(sc.property.Property.Price) * params.DownPaymentPct)
		if requiredDownPayment > remainingBudget {
			r.logger.Debug("skipping candidate due to insufficient budget",
				"propertyId", sc.property.Property.ID,
				"requiredDownPayment", requiredDownPayment,
				"remainingBudget", remainingBudget,
			)
			continue
		}

		rec := r.createAcquisitionRecommendation(sc, params, priority)
		recommendations = append(recommendations, rec)

		remainingBudget -= requiredDownPayment
		priority++
	}

	return recommendations
}

// createAcquisitionRecommendation creates a PropertyRecommendation for acquisition
func (r *AcquisitionRecommender) createAcquisitionRecommendation(
	sc scoredCandidate,
	params AcquisitionParams,
	priority int,
) investment.PropertyRecommendation {
	prop := sc.property.Property
	downPayment := int(float64(prop.Price) * params.DownPaymentPct)
	loanAmount := prop.Price - downPayment

	// Calculate financial metrics
	capRate := 0.0
	if prop.Price > 0 && prop.EstimatedRent > 0 {
		annualNOI := float64(prop.EstimatedRent) * 12 * 0.5 // 50% operating expenses
		capRate = (annualNOI / float64(prop.Price)) * 100
	}

	monthlyCashFlow := r.calculateMonthlyCashFlow(prop, params)

	return investment.PropertyRecommendation{
		Property: investment.PropertyInPortfolio{
			Property:        prop,
			DownPayment:     downPayment,
			LoanAmount:      loanAmount,
			CapRate:         capRate,
			MonthlyCashFlow: monthlyCashFlow,
			Score:           sc.score,
		},
		Action:     investment.RecommendationActionAcquire,
		Confidence: sc.property.OverallScore, // Use AI score as confidence
		Reasoning:  sc.reason,
		Scores: investment.PropertyRecommendationScores{
			PerformanceScore:   sc.property.ROIScore * 0.4,
			MarketQualityScore: sc.property.MarketQualityScore * 0.3,
			PortfolioFitScore:  sc.property.PortfolioFit * 0.2,
			OpportunityCost:    10, // Acquisition = positive opportunity
			TotalScore:         sc.score,
		},
		Metrics: investment.PropertyRecommendationMetrics{
			ProjectedAnnualCashFlow: monthlyCashFlow * 12,
			ProjectedCapRate:        capRate,
			ProjectedROI:            r.calculateCashOnCash(prop, params),
			ProjectedAppreciation:   3.0, // Default 3% appreciation
		},
		AcquisitionPriority: priority,
		RequiredDownPayment: downPayment,
	}
}

// calculateMonthlyCashFlow calculates monthly cash flow for a property
func (r *AcquisitionRecommender) calculateMonthlyCashFlow(
	prop investment.Property,
	params AcquisitionParams,
) int {
	if prop.EstimatedRent == 0 {
		return 0
	}

	monthlyRent := prop.EstimatedRent
	monthlyExpenses := int(float64(monthlyRent) * 0.5) // 50% operating expenses

	// Calculate monthly mortgage payment
	loanAmount := float64(prop.Price) * (1 - params.DownPaymentPct)
	monthlyRate := params.MortgageRate / 100 / 12
	numPayments := 360.0 // 30 years

	var monthlyMortgage float64
	if monthlyRate > 0 {
		factor := (monthlyRate * pow(1+monthlyRate, numPayments)) /
			(pow(1+monthlyRate, numPayments) - 1)
		monthlyMortgage = loanAmount * factor
	}

	return monthlyRent - monthlyExpenses - int(monthlyMortgage)
}

// calculateCashOnCash calculates cash-on-cash return
func (r *AcquisitionRecommender) calculateCashOnCash(
	prop investment.Property,
	params AcquisitionParams,
) float64 {
	downPayment := float64(prop.Price) * params.DownPaymentPct
	if downPayment == 0 {
		return 0
	}

	annualCashFlow := float64(r.calculateMonthlyCashFlow(prop, params)) * 12
	return (annualCashFlow / downPayment) * 100
}

// pow calculates x^y for float64
func pow(x, y float64) float64 {
	result := 1.0
	for i := 0; i < int(y); i++ {
		result *= x
	}
	return result
}

// ExtractLocations extracts unique locations from selected properties
func ExtractLocations(properties []investment.SelectedProperty) []string {
	locationSet := make(map[string]bool)
	for _, p := range properties {
		loc := fmt.Sprintf("%s, %s", p.City, p.State)
		locationSet[loc] = true
	}

	locations := make([]string, 0, len(locationSet))
	for loc := range locationSet {
		locations = append(locations, loc)
	}

	return locations
}

// ConvertSelectedToProperties converts SelectedProperty to Property
func ConvertSelectedToProperties(selected []investment.SelectedProperty) []investment.Property {
	properties := make([]investment.Property, len(selected))
	for i, s := range selected {
		properties[i] = investment.Property{
			ID:            s.ID,
			Address:       s.Address,
			City:          s.City,
			State:         s.State,
			ZipCode:       s.ZipCode,
			Price:         s.Price,
			Beds:          s.Beds,
			Baths:         s.Baths,
			Sqft:          s.Sqft,
			EstimatedRent: s.EstimatedRent,
			YearBuilt:     s.YearBuilt,
			PropertyType:  s.PropertyType,
			DaysOnMarket:  s.DaysOnMarket,
			ImageURL:      s.ImageURL,
			ListingURL:    s.ListingURL,
		}
	}
	return properties
}
