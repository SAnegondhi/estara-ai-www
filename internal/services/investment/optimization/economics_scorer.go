package optimization

import (
	"context"
	"log/slog"
	"math"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/economics"
)

// EconomicsEnhancedScorer enhances market quality scoring with live economic data (ADR-069)
type EconomicsEnhancedScorer struct {
	economics economics.Provider
	logger    *slog.Logger
}

// NewEconomicsEnhancedScorer creates a scorer with economic data integration
func NewEconomicsEnhancedScorer(econ economics.Provider) *EconomicsEnhancedScorer {
	return &EconomicsEnhancedScorer{
		economics: econ,
		logger:    slog.Default().With("component", "economics_enhanced_scorer"),
	}
}

// CalculateEnhancedMarketQualityScore computes score using live economic data
func (s *EconomicsEnhancedScorer) CalculateEnhancedMarketQualityScore(
	ctx context.Context,
	city, state string,
	marketData *aggregator.MarketData,
) investment.MarketQualityScore {
	breakdown := investment.MarketScoreBreakdown{
		Appreciation:  investment.MarketScoreCategory{Score: 0, MaxScore: 30, Components: map[string]float64{}},
		DemandHeat:    investment.MarketScoreCategory{Score: 0, MaxScore: 20, Components: map[string]float64{}},
		Employment:    investment.MarketScoreCategory{Score: 0, MaxScore: 25, Components: map[string]float64{}},
		Demographics:  investment.MarketScoreCategory{Score: 0, MaxScore: 15, Components: map[string]float64{}},
		Affordability: investment.MarketScoreCategory{Score: 0, MaxScore: 10, Components: map[string]float64{}},
	}

	// Get economic data
	var econData *economics.MarketEconomics
	if s.economics != nil && s.economics.IsConfigured() {
		var err error
		econData, err = s.economics.GetMarketEconomics(ctx, city, state)
		if err != nil {
			s.logger.Warn("failed to fetch economic data for scoring", "error", err, "city", city, "state", state)
		}
	}

	// === Appreciation Score (30 points max) ===
	priceGrowth5Y := derivePriceGrowth5Y(marketData)
	if priceGrowth5Y != nil {
		breakdown.Appreciation.Components["priceGrowth5Y"] = clamp(*priceGrowth5Y*0.3, 0, 15)
	}

	if marketData != nil {
		breakdown.Appreciation.Components["priceTrendAcceleration"] = clamp(marketData.YearOverYearPct*0.5, 0, 5)
	}

	volatility := derivePriceVolatility(marketData)
	if volatility != nil {
		breakdown.Appreciation.Components["lowVolatility"] = clamp(5-*volatility, 0, 5)
	}

	priceToRent := derivePriceToRent(marketData)
	if priceToRent != nil {
		breakdown.Appreciation.Components["priceToRentTrend"] = clamp(5-(*priceToRent-15)*0.5, 0, 5)
	}

	breakdown.Appreciation.Score = sumComponents(breakdown.Appreciation.Components)

	// === Demand Heat Score (20 points max) ===
	// Use FRED vacancy rate if available
	if econData != nil && econData.RentalVacancyRate > 0 {
		// Lower vacancy = higher score (0-12% vacancy maps to 10-0 points)
		breakdown.DemandHeat.Components["lowVacancy"] = clamp(10-(econData.RentalVacancyRate*0.83), 0, 10)
		s.logger.Debug("using FRED vacancy rate", "rate", econData.RentalVacancyRate)
	} else if marketData != nil && marketData.VacancyRate > 0 {
		breakdown.DemandHeat.Components["lowVacancy"] = clamp(5-(marketData.VacancyRate/2), 0, 5)
	} else {
		breakdown.DemandHeat.Components["lowVacancy"] = 2.5 // Default
	}

	// Market heat index from market data
	if marketData != nil && marketData.VacancyRate > 0 {
		breakdown.DemandHeat.Components["marketHeatIndex"] = clamp(5-(marketData.VacancyRate/2), 0, 5)
	} else {
		breakdown.DemandHeat.Components["marketHeatIndex"] = 2.5
	}

	breakdown.DemandHeat.Components["lowInventory"] = 2.5 // TODO: Add inventory data
	breakdown.DemandHeat.Score = sumComponents(breakdown.DemandHeat.Components)

	// === Employment Score (25 points max) ===
	// Use BLS state unemployment
	if econData != nil && econData.StateUnemploymentRate > 0 {
		// Lower unemployment = higher score (2-10% maps to 12-0 points)
		breakdown.Employment.Components["lowUnemployment"] = clamp(12-(econData.StateUnemploymentRate-2)*1.5, 0, 12)
		s.logger.Debug("using BLS state unemployment", "rate", econData.StateUnemploymentRate)
	} else if marketData != nil && marketData.UnemploymentRate > 0 {
		breakdown.Employment.Components["lowUnemployment"] = clamp(10-(marketData.UnemploymentRate-3)*2, 0, 10)
	} else {
		breakdown.Employment.Components["lowUnemployment"] = 5 // Default
	}

	// Employment growth from market data
	if marketData != nil && marketData.EmploymentGrowthRate != 0 {
		breakdown.Employment.Components["employmentGrowth"] = clamp(marketData.EmploymentGrowthRate*2.5, 0, 8)
	} else {
		breakdown.Employment.Components["employmentGrowth"] = 3 // Default
	}

	// Wage growth from BLS
	if econData != nil && econData.AverageHourlyEarnings > 0 {
		// Higher wages = better market (scale $20-$40/hr to 0-5 points)
		wageScore := clamp((econData.AverageHourlyEarnings-20)/4, 0, 5)
		breakdown.Employment.Components["wageLevel"] = wageScore
	} else {
		breakdown.Employment.Components["wageLevel"] = 2.5
	}

	breakdown.Employment.Score = sumComponents(breakdown.Employment.Components)

	// === Demographics Score (15 points max) ===
	// Use Census population data
	if econData != nil && econData.Population > 0 {
		// Larger markets get higher scores (log scale)
		popScore := clamp(math.Log10(float64(econData.Population))-3, 0, 7) // 1K-10M population
		breakdown.Demographics.Components["marketSize"] = popScore
		s.logger.Debug("using Census population", "population", econData.Population)
	} else {
		breakdown.Demographics.Components["marketSize"] = 3.5
	}

	// Population growth from market data
	if marketData != nil && marketData.PopulationGrowthRate != 0 {
		breakdown.Demographics.Components["populationGrowth"] = clamp(marketData.PopulationGrowthRate*3.33, 0, 5)
	} else {
		breakdown.Demographics.Components["populationGrowth"] = 2.5
	}

	// Household count as proxy for housing demand
	if econData != nil && econData.HouseholdCount > 0 {
		hhScore := clamp(math.Log10(float64(econData.HouseholdCount))-2, 0, 3)
		breakdown.Demographics.Components["householdDensity"] = hhScore
	} else {
		breakdown.Demographics.Components["householdDensity"] = 1.5
	}

	breakdown.Demographics.Score = sumComponents(breakdown.Demographics.Components)

	// === Affordability Score (10 points max) ===
	// Use Census median income for proper affordability calculation
	if econData != nil && econData.MedianHouseholdIncome > 0 && marketData != nil && marketData.MedianHomePrice > 0 {
		// Price-to-income ratio (3-8 is normal range)
		priceToIncome := float64(marketData.MedianHomePrice) / econData.MedianHouseholdIncome

		// Lower ratio = more affordable = higher score
		// 3x = 5 points, 8x = 0 points
		affordScore := clamp(5-(priceToIncome-3), 0, 5)
		breakdown.Affordability.Components["priceToIncome"] = affordScore
		s.logger.Debug("calculated price-to-income", "ratio", priceToIncome, "score", affordScore)
	} else if marketData != nil && marketData.MedianHomePrice > 0 {
		// Fallback: simple price-based score
		breakdown.Affordability.Components["priceLevel"] = clamp(5-(float64(marketData.MedianHomePrice)/100000), 0, 5)
	} else {
		breakdown.Affordability.Components["priceLevel"] = 2.5
	}

	// Rent affordability using Census income
	if econData != nil && econData.MedianHouseholdIncome > 0 && marketData != nil && marketData.MedianRent > 0 {
		// Rent-to-income ratio (annual rent / income)
		rentToIncome := (float64(marketData.MedianRent) * 12) / econData.MedianHouseholdIncome

		// 20% = great (5 pts), 30% = okay (2.5 pts), 40% = poor (0 pts)
		rentScore := clamp(5-(rentToIncome-0.2)*25, 0, 5)
		breakdown.Affordability.Components["rentToIncome"] = rentScore
	} else {
		breakdown.Affordability.Components["rentToIncome"] = 2.5
	}

	breakdown.Affordability.Score = sumComponents(breakdown.Affordability.Components)

	// === Calculate Total Score ===
	totalScore := breakdown.Appreciation.Score +
		breakdown.DemandHeat.Score +
		breakdown.Employment.Score +
		breakdown.Demographics.Score +
		breakdown.Affordability.Score

	score := int(math.Round(clamp(totalScore, 0, 100)))
	rating := "poor"
	switch {
	case score >= 70:
		rating = "excellent"
	case score >= 55:
		rating = "good"
	case score >= 40:
		rating = "fair"
	}

	return investment.MarketQualityScore{
		Score:     score,
		Rating:    rating,
		Breakdown: breakdown,
	}
}

// BuildEnhancedLocationMarketAnalysis creates location analysis with economic data
func (s *EconomicsEnhancedScorer) BuildEnhancedLocationMarketAnalysis(
	ctx context.Context,
	city, state, location string,
	marketData *aggregator.MarketData,
) investment.LocationMarketAnalysis {
	score := s.CalculateEnhancedMarketQualityScore(ctx, city, state, marketData)
	priceGrowth5Y := derivePriceGrowth5Y(marketData)
	priceVolatility := derivePriceVolatility(marketData)

	// Get economic data for additional metrics
	var econData *economics.MarketEconomics
	if s.economics != nil && s.economics.IsConfigured() {
		econData, _ = s.economics.GetMarketEconomics(ctx, city, state)
	}

	analysis := investment.LocationMarketAnalysis{
		Location:            location,
		MarketQualityScore:  score.Score,
		MarketQualityRating: score.Rating,
		Breakdown:           score.Breakdown,
		PriceGrowth5Y:       priceGrowth5Y,
		PriceVolatility:     priceVolatility,
	}

	// Populate from market data
	if marketData != nil {
		if marketData.VacancyRate > 0 {
			v := marketData.VacancyRate
			analysis.VacancyRate = &v
		}
		if marketData.UnemploymentRate > 0 {
			u := marketData.UnemploymentRate
			analysis.UnemploymentRate = &u
		}
		if marketData.EmploymentGrowthRate != 0 {
			eg := marketData.EmploymentGrowthRate
			analysis.EmploymentGrowthRate = &eg
		}
		if marketData.PopulationGrowthRate != 0 {
			pg := marketData.PopulationGrowthRate
			analysis.PopulationGrowth = &pg
		}
		if marketData.RentYearOverYear != 0 {
			rentGrowth5Y := marketData.RentYearOverYear * 5
			analysis.RentGrowth5Y = &rentGrowth5Y
		}
		if marketData.CapRate > 0 && marketData.YearOverYearPct != 0 {
			heat := math.Min(100, math.Max(0, (marketData.CapRate*8)+(marketData.YearOverYearPct*2)+30))
			analysis.MarketHeatIndex = &heat
		}
	}

	// Override with more accurate economic data
	if econData != nil {
		// Use FRED vacancy rate (more reliable national data)
		if econData.RentalVacancyRate > 0 {
			v := econData.RentalVacancyRate
			analysis.VacancyRate = &v
		}

		// Use BLS state unemployment (more accurate than estimates)
		if econData.StateUnemploymentRate > 0 {
			u := econData.StateUnemploymentRate
			analysis.UnemploymentRate = &u
		}

		// Calculate proper affordability index
		if econData.AffordabilityIndex > 0 {
			a := econData.AffordabilityIndex
			analysis.AffordabilityIndex = &a
		} else if econData.MedianHouseholdIncome > 0 && marketData != nil && marketData.MedianHomePrice > 0 {
			// Calculate manually: (qualifying income / actual income) * 100
			// Affordability of 100 = median income can afford median home
			monthlyIncome := econData.MedianHouseholdIncome / 12
			qualifyingPayment := monthlyIncome * 0.28 // 28% housing ratio

			// Estimate monthly payment (80% LTV, 30yr, 7% rate)
			loanAmount := float64(marketData.MedianHomePrice) * 0.8
			monthlyRate := 0.07 / 12
			numPayments := 360.0
			monthlyPayment := loanAmount * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) / (math.Pow(1+monthlyRate, numPayments) - 1)

			// Add taxes and insurance (~1.5% of value annually)
			monthlyTI := float64(marketData.MedianHomePrice) * 0.015 / 12
			totalHousing := monthlyPayment + monthlyTI

			affordability := (qualifyingPayment / totalHousing) * 100
			analysis.AffordabilityIndex = &affordability
		}
	}

	return analysis
}
