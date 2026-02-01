package optimization

import (
	"log/slog"
	"math"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/market/aggregator"
)

// CalculateMarketQualityScore computes a 0-100 score for a market.
func CalculateMarketQualityScore(data *aggregator.MarketData) investment.MarketQualityScore {
	breakdown := investment.MarketScoreBreakdown{
		Appreciation: investment.MarketScoreCategory{Score: 0, MaxScore: 30, Components: map[string]float64{}},
		DemandHeat:   investment.MarketScoreCategory{Score: 0, MaxScore: 20, Components: map[string]float64{}},
		Employment:   investment.MarketScoreCategory{Score: 0, MaxScore: 25, Components: map[string]float64{}},
		Demographics: investment.MarketScoreCategory{Score: 0, MaxScore: 15, Components: map[string]float64{}},
		Affordability: investment.MarketScoreCategory{Score: 0, MaxScore: 10, Components: map[string]float64{}},
	}

	if data == nil {
		return investment.MarketQualityScore{Score: 50, Rating: "fair", Breakdown: breakdown}
	}

	priceGrowth5Y := derivePriceGrowth5Y(data)
	if priceGrowth5Y != nil {
		breakdown.Appreciation.Components["priceGrowth5Y"] = clamp(*priceGrowth5Y*0.3, 0, 15)
	}

	priceYoY := data.YearOverYearPct
	breakdown.Appreciation.Components["priceTrendAcceleration"] = clamp(priceYoY*0.5, 0, 5)

	volatility := derivePriceVolatility(data)
	if volatility != nil {
		breakdown.Appreciation.Components["lowVolatility"] = clamp(5-*volatility, 0, 5)
	}

	priceToRent := derivePriceToRent(data)
	if priceToRent != nil {
		breakdown.Appreciation.Components["priceToRentTrend"] = clamp(5-(*priceToRent-15)*0.5, 0, 5)
	}

	breakdown.Appreciation.Score = sumComponents(breakdown.Appreciation.Components)

	// Demand heat - use real vacancy rate if available
	breakdown.DemandHeat.Components["marketHeatIndex"] = 5
	if data.VacancyRate > 0 {
		// Lower vacancy = higher score (0-10% vacancy maps to 5-0 points)
		breakdown.DemandHeat.Components["lowVacancy"] = clamp(5-(data.VacancyRate/2), 0, 5)
	} else {
		breakdown.DemandHeat.Components["lowVacancy"] = 2.5
	}
	breakdown.DemandHeat.Components["lowInventory"] = 2.5
	breakdown.DemandHeat.Score = sumComponents(breakdown.DemandHeat.Components)

	// Employment - use real data when available
	// Employment growth: higher is better (0-4% maps to 0-10 points)
	if data.EmploymentGrowthRate != 0 {
		breakdown.Employment.Components["employmentGrowth"] = clamp(data.EmploymentGrowthRate*2.5, 0, 10)
	} else {
		breakdown.Employment.Components["employmentGrowth"] = 5
	}
	// Unemployment: lower is better (3-8% maps to 10-0 points)
	if data.UnemploymentRate > 0 {
		breakdown.Employment.Components["lowUnemployment"] = clamp(10-(data.UnemploymentRate-3)*2, 0, 10)
	} else {
		breakdown.Employment.Components["lowUnemployment"] = 2.5
	}
	breakdown.Employment.Components["industryDiversity"] = 2.5
	breakdown.Employment.Components["wageGrowth"] = 2.5
	breakdown.Employment.Score = sumComponents(breakdown.Employment.Components)

	// Demographics - use real population growth when available
	if data.PopulationGrowthRate != 0 {
		// Population growth: 0-3% maps to 0-10 points
		breakdown.Demographics.Components["populationGrowth"] = clamp(data.PopulationGrowthRate*3.33, 0, 10)
	} else {
		breakdown.Demographics.Components["populationGrowth"] = 5
	}
	breakdown.Demographics.Components["netMigration"] = 2.5
	breakdown.Demographics.Score = sumComponents(breakdown.Demographics.Components)

	// Affordability
	affordability := deriveAffordabilityIndex(data)
	if affordability != nil {
		breakdown.Affordability.Components["affordabilityIndex"] = clamp((*affordability)*0.05, 0, 5)
	} else {
		breakdown.Affordability.Components["affordabilityIndex"] = 2.5
	}
	priceToIncome := derivePriceToIncome(data)
	if priceToIncome != nil {
		breakdown.Affordability.Components["rentToIncome"] = clamp(5-(*priceToIncome-3), 0, 5)
	} else {
		breakdown.Affordability.Components["rentToIncome"] = 2.5
	}
	breakdown.Affordability.Score = sumComponents(breakdown.Affordability.Components)

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

// BuildLocationMarketAnalysis builds the per-location output.
func BuildLocationMarketAnalysis(location string, data *aggregator.MarketData) investment.LocationMarketAnalysis {
	score := CalculateMarketQualityScore(data)
	priceGrowth5Y := derivePriceGrowth5Y(data)
	priceVolatility := derivePriceVolatility(data)
	affordabilityIndex := deriveAffordabilityIndex(data)

	analysis := investment.LocationMarketAnalysis{
		Location:            location,
		MarketQualityScore:  score.Score,
		MarketQualityRating: score.Rating,
		Breakdown:           score.Breakdown,
		PriceGrowth5Y:       priceGrowth5Y,
		PriceVolatility:     priceVolatility,
		AffordabilityIndex:  affordabilityIndex,
	}

	// Debug: log what data we're receiving
	if data != nil {
		slog.Info("BuildLocationMarketAnalysis input data",
			"location", location,
			"employmentGrowth", data.EmploymentGrowthRate,
			"populationGrowth", data.PopulationGrowthRate,
			"vacancyRate", data.VacancyRate,
			"unemploymentRate", data.UnemploymentRate,
			"yoyPct", data.YearOverYearPct,
			"rentYoY", data.RentYearOverYear,
		)
	} else {
		slog.Warn("BuildLocationMarketAnalysis received nil data", "location", location)
	}

	// Populate additional metrics from MarketData if available
	if data != nil {
		// Vacancy rate
		if data.VacancyRate > 0 {
			v := data.VacancyRate
			analysis.VacancyRate = &v
		}

		// Unemployment rate
		if data.UnemploymentRate > 0 {
			u := data.UnemploymentRate
			analysis.UnemploymentRate = &u
		}

		// Employment growth rate
		if data.EmploymentGrowthRate != 0 {
			eg := data.EmploymentGrowthRate
			analysis.EmploymentGrowthRate = &eg
		}

		// Population growth rate
		if data.PopulationGrowthRate != 0 {
			pg := data.PopulationGrowthRate
			analysis.PopulationGrowth = &pg
		}

		// Rent growth (use YoY as proxy for 5Y trend if available)
		if data.RentYearOverYear != 0 {
			// Extrapolate 5Y growth from YoY (rough approximation)
			rentGrowth5Y := data.RentYearOverYear * 5
			analysis.RentGrowth5Y = &rentGrowth5Y
		}

		// Market heat index (derive from cap rate and growth - higher cap + growth = hotter)
		if data.CapRate > 0 && data.YearOverYearPct != 0 {
			// Scale: cap rate 5-10% and YoY growth contribute to heat
			heat := math.Min(100, math.Max(0, (data.CapRate*8)+(data.YearOverYearPct*2)+30))
			analysis.MarketHeatIndex = &heat
		}
	}

	return analysis
}

// CalculateMarketQualityScores returns analysis for all locations.
func CalculateMarketQualityScores(locationMarketData map[string]*aggregator.MarketData) []investment.LocationMarketAnalysis {
	results := make([]investment.LocationMarketAnalysis, 0, len(locationMarketData))
	for location, data := range locationMarketData {
		results = append(results, BuildLocationMarketAnalysis(location, data))
	}
	return results
}

func derivePriceToRent(data *aggregator.MarketData) *float64 {
	if data == nil || data.MedianRent <= 0 {
		return nil
	}
	value := float64(data.MedianHomePrice) / (float64(data.MedianRent) * 12)
	return &value
}

func deriveAffordabilityIndex(data *aggregator.MarketData) *float64 {
	if data == nil || data.MedianHomePrice <= 0 {
		return nil
	}
	// Placeholder: higher price means less affordable, scaled to 0-100.
	value := math.Max(0, 100-(float64(data.MedianHomePrice)/10000))
	return &value
}

func sumComponents(components map[string]float64) float64 {
	total := 0.0
	for _, value := range components {
		total += value
	}
	return total
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
