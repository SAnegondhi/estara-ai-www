package optimization

import (
	"math"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/market/aggregator"
)

// DefaultMarketFilters returns professional-grade defaults.
func DefaultMarketFilters() investment.MarketFilterCriteria {
	return investment.MarketFilterCriteria{
		MinPriceGrowth5Y:    0,
		MaxPriceVolatility:  6,
		MaxUnemploymentRate: 8,
		MinEmploymentGrowth: 0,
		MinPopulationGrowth: -1,
		MaxVacancyRate:      9,
		MaxPriceToIncome:    6,
		MinCapRate:          0,
	}
}

// StrategyMarketFilters returns filter adjustments based on strategy.
func StrategyMarketFilters(strategy investment.InvestmentStrategy) investment.MarketFilterCriteria {
	switch strategy {
	case investment.StrategyCashFlow:
		return investment.MarketFilterCriteria{
			MinCapRate:       4,
			MaxPriceToIncome: 5,
		}
	case investment.StrategyAppreciation:
		return investment.MarketFilterCriteria{
			MinPriceGrowth5Y:    10,
			MinPopulationGrowth: 0,
			MinCapRate:          0,
		}
	case investment.StrategyRiskAdjusted:
		return investment.MarketFilterCriteria{
			MaxPriceVolatility:  4,
			MaxUnemploymentRate: 6,
			MaxVacancyRate:      7,
		}
	default:
		return investment.MarketFilterCriteria{}
	}
}

// ApplyMarketFilters applies kill-criteria to market data by location.
func ApplyMarketFilters(
	locationMarketData map[string]*aggregator.MarketData,
	filters investment.MarketFilterCriteria,
) investment.MarketFilterSummary {
	passed := make([]investment.MarketFilterResult, 0)
	failed := make([]investment.MarketFilterResult, 0)

	for location, data := range locationMarketData {
		failedCriteria := make([]string, 0)

		priceGrowth5Y := derivePriceGrowth5Y(data)
		if priceGrowth5Y != nil && *priceGrowth5Y < filters.MinPriceGrowth5Y {
			failedCriteria = append(failedCriteria, "price growth below threshold")
		}

		volatility := derivePriceVolatility(data)
		if volatility != nil && *volatility > filters.MaxPriceVolatility {
			failedCriteria = append(failedCriteria, "price volatility above threshold")
		}

		unemployment := deriveUnemploymentRate(data)
		if unemployment != nil && *unemployment > filters.MaxUnemploymentRate {
			failedCriteria = append(failedCriteria, "unemployment above threshold")
		}

		empGrowth := deriveEmploymentGrowth(data)
		if empGrowth != nil && *empGrowth < filters.MinEmploymentGrowth {
			failedCriteria = append(failedCriteria, "employment growth below threshold")
		}

		popGrowth := derivePopulationGrowth(data)
		if popGrowth != nil && *popGrowth < filters.MinPopulationGrowth {
			failedCriteria = append(failedCriteria, "population growth below threshold")
		}

		vacancy := deriveVacancyRate(data)
		if vacancy != nil && *vacancy > filters.MaxVacancyRate {
			failedCriteria = append(failedCriteria, "vacancy above threshold")
		}

		priceToIncome := derivePriceToIncome(data)
		if priceToIncome != nil && *priceToIncome > filters.MaxPriceToIncome {
			failedCriteria = append(failedCriteria, "price-to-income above threshold")
		}

		if filters.MinCapRate > 0 && data != nil && data.CapRate > 0 && data.CapRate < filters.MinCapRate {
			failedCriteria = append(failedCriteria, "cap rate below threshold")
		}

		result := investment.MarketFilterResult{
			Location:       location,
			Passed:         len(failedCriteria) == 0,
			FailedCriteria: failedCriteria,
		}

		if result.Passed {
			passed = append(passed, result)
		} else {
			failed = append(failed, result)
		}
	}

	total := len(locationMarketData)
	passRate := 0.0
	if total > 0 {
		passRate = (float64(len(passed)) / float64(total)) * 100
	}

	return investment.MarketFilterSummary{
		Passed:       passed,
		Failed:       failed,
		TotalMarkets: total,
		PassRate:     math.Round(passRate*10) / 10,
	}
}

func derivePriceGrowth5Y(data *aggregator.MarketData) *float64 {
	if data == nil || data.YearOverYearPct == 0 {
		return nil
	}
	value := data.YearOverYearPct * 5
	return &value
}

func derivePriceVolatility(_ *aggregator.MarketData) *float64 {
	return nil
}

func deriveUnemploymentRate(_ *aggregator.MarketData) *float64 {
	return nil
}

func deriveEmploymentGrowth(_ *aggregator.MarketData) *float64 {
	return nil
}

func derivePopulationGrowth(_ *aggregator.MarketData) *float64 {
	return nil
}

func deriveVacancyRate(_ *aggregator.MarketData) *float64 {
	return nil
}

func derivePriceToIncome(_ *aggregator.MarketData) *float64 {
	return nil
}
