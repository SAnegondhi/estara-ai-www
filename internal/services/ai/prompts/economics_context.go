package prompts

import (
	"context"
	"fmt"

	"github.com/estara-ai/www/internal/services/market/economics"
)

// EconomicContextBuilder creates economic context for AI prompts (ADR-069)
type EconomicContextBuilder struct {
	economics economics.Provider
}

// NewEconomicContextBuilder creates a builder with economic data access
func NewEconomicContextBuilder(econ economics.Provider) *EconomicContextBuilder {
	return &EconomicContextBuilder{
		economics: econ,
	}
}

// BuildEconomicContext creates XML-formatted economic context for prompts
func (b *EconomicContextBuilder) BuildEconomicContext(ctx context.Context, city, state string, medianHomePrice float64) string {
	if b.economics == nil || !b.economics.IsConfigured() {
		return b.buildDefaultContext()
	}

	var data *economics.MarketEconomics
	var err error

	if medianHomePrice > 0 {
		data, err = b.economics.GetMarketEconomicsWithPrice(ctx, city, state, medianHomePrice)
	} else {
		data, err = b.economics.GetMarketEconomics(ctx, city, state)
	}

	if err != nil || data == nil {
		return b.buildDefaultContext()
	}

	return b.formatEconomicContext(data, medianHomePrice)
}

// formatEconomicContext formats economic data as XML for AI prompts
func (b *EconomicContextBuilder) formatEconomicContext(data *economics.MarketEconomics, medianHomePrice float64) string {
	result := "<ECONOMIC_CONDITIONS>\n"

	// Financing Environment
	result += "  <financing>\n"
	if data.MortgageRate30Year > 0 {
		result += fmt.Sprintf("    <mortgage_rate_30y>%.2f%%</mortgage_rate_30y>\n", data.MortgageRate30Year)
	}
	if data.MortgageRate15Year > 0 {
		result += fmt.Sprintf("    <mortgage_rate_15y>%.2f%%</mortgage_rate_15y>\n", data.MortgageRate15Year)
	}
	result += fmt.Sprintf("    <source>%s</source>\n", getSourceInfo(data.Sources, "fred"))
	result += "  </financing>\n"

	// National Economic Indicators
	result += "  <national_economy>\n"
	if data.NationalUnemployment > 0 {
		result += fmt.Sprintf("    <unemployment_rate>%.1f%%</unemployment_rate>\n", data.NationalUnemployment)
	}
	if data.InflationRate > 0 {
		result += fmt.Sprintf("    <inflation_rate>%.1f%%</inflation_rate>\n", data.InflationRate)
	}
	if data.RentalVacancyRate > 0 {
		result += fmt.Sprintf("    <rental_vacancy_rate>%.1f%%</rental_vacancy_rate>\n", data.RentalVacancyRate)
	}
	result += "  </national_economy>\n"

	// State/Local Labor Market
	if data.State != "" {
		result += fmt.Sprintf("  <state_labor_market state=\"%s\">\n", data.State)
		if data.StateUnemploymentRate > 0 {
			result += fmt.Sprintf("    <unemployment_rate>%.1f%%</unemployment_rate>\n", data.StateUnemploymentRate)
		}
		if data.StateEmployment > 0 {
			result += fmt.Sprintf("    <employment_level>%.0fK jobs</employment_level>\n", data.StateEmployment)
		}
		if data.LaborForceParticipation > 0 {
			result += fmt.Sprintf("    <labor_force_participation>%.1f%%</labor_force_participation>\n", data.LaborForceParticipation)
		}
		if data.AverageHourlyEarnings > 0 {
			result += fmt.Sprintf("    <average_hourly_wage>$%.2f</average_hourly_wage>\n", data.AverageHourlyEarnings)
		}
		if data.ConstructionEmployment > 0 {
			result += fmt.Sprintf("    <construction_employment>%.0fK jobs</construction_employment>\n", data.ConstructionEmployment)
		}
		if data.JobOpenings > 0 {
			result += fmt.Sprintf("    <job_openings>%.0fK</job_openings>\n", data.JobOpenings)
		}
		result += fmt.Sprintf("    <source>%s</source>\n", getSourceInfo(data.Sources, "bls"))
		result += "  </state_labor_market>\n"
	}

	// Demographics
	if data.Population > 0 || data.MedianHouseholdIncome > 0 {
		result += "  <demographics>\n"
		if data.City != "" {
			result += fmt.Sprintf("    <location>%s, %s</location>\n", data.City, data.State)
			result += fmt.Sprintf("    <data_level>%s</data_level>\n", data.DemographicLevel)
		}
		if data.Population > 0 {
			result += fmt.Sprintf("    <population>%d</population>\n", data.Population)
		}
		if data.MedianHouseholdIncome > 0 {
			result += fmt.Sprintf("    <median_household_income>$%.0f</median_household_income>\n", data.MedianHouseholdIncome)
		}
		if data.PerCapitaIncome > 0 {
			result += fmt.Sprintf("    <per_capita_income>$%.0f</per_capita_income>\n", data.PerCapitaIncome)
		}
		if data.MedianAge > 0 {
			result += fmt.Sprintf("    <median_age>%.1f years</median_age>\n", data.MedianAge)
		}
		if data.HouseholdCount > 0 {
			result += fmt.Sprintf("    <household_count>%d</household_count>\n", data.HouseholdCount)
		}
		if data.PovertyRate > 0 {
			result += fmt.Sprintf("    <poverty_rate>%.1f%%</poverty_rate>\n", data.PovertyRate)
		}
		result += fmt.Sprintf("    <source>%s</source>\n", getSourceInfo(data.Sources, "census"))
		result += "  </demographics>\n"
	}

	// Housing Cost Indicators
	result += "  <housing_costs>\n"
	if data.CPIShelter > 0 {
		result += fmt.Sprintf("    <cpi_shelter>%.1f</cpi_shelter>\n", data.CPIShelter)
	}
	if data.CPIRent > 0 {
		result += fmt.Sprintf("    <cpi_rent>%.1f</cpi_rent>\n", data.CPIRent)
	}
	result += "  </housing_costs>\n"

	// Affordability Metrics (if home price provided)
	if medianHomePrice > 0 && data.MedianHouseholdIncome > 0 {
		result += "  <affordability_analysis>\n"
		result += fmt.Sprintf("    <median_home_price>$%.0f</median_home_price>\n", medianHomePrice)
		if data.PriceToIncomeRatio > 0 {
			result += fmt.Sprintf("    <price_to_income_ratio>%.1fx</price_to_income_ratio>\n", data.PriceToIncomeRatio)
			result += fmt.Sprintf("    <interpretation>%s</interpretation>\n", interpretPriceToIncome(data.PriceToIncomeRatio))
		}
		if data.AffordabilityIndex > 0 {
			result += fmt.Sprintf("    <affordability_index>%.0f</affordability_index>\n", data.AffordabilityIndex)
			result += fmt.Sprintf("    <index_interpretation>%s</index_interpretation>\n", interpretAffordabilityIndex(data.AffordabilityIndex))
		}
		result += "  </affordability_analysis>\n"
	}

	// Data Quality
	if len(data.Sources) > 0 {
		result += "  <data_quality>\n"
		for source, date := range data.Sources {
			result += fmt.Sprintf("    <%s_data_date>%s</%s_data_date>\n", source, date, source)
		}
		if len(data.Errors) > 0 {
			result += "    <warnings>\n"
			for _, err := range data.Errors {
				result += fmt.Sprintf("      <warning>%s</warning>\n", err)
			}
			result += "    </warnings>\n"
		}
		result += "  </data_quality>\n"
	}

	result += "</ECONOMIC_CONDITIONS>"
	return result
}

// buildDefaultContext returns minimal context when no data available
func (b *EconomicContextBuilder) buildDefaultContext() string {
	return `<ECONOMIC_CONDITIONS>
  <note>Live economic data unavailable - using general assumptions</note>
  <defaults>
    <mortgage_rate_30y>7.0%</mortgage_rate_30y>
    <unemployment_rate>4.0%</unemployment_rate>
    <inflation_rate>2.5%</inflation_rate>
    <rental_vacancy_rate>6.0%</rental_vacancy_rate>
  </defaults>
</ECONOMIC_CONDITIONS>`
}

// BuildStressTestContext creates economic context specifically for stress tests
func (b *EconomicContextBuilder) BuildStressTestContext(ctx context.Context, state string) string {
	if b.economics == nil || !b.economics.IsConfigured() {
		return b.buildDefaultStressContext()
	}

	data, err := b.economics.GetMarketEconomics(ctx, "", state)
	if err != nil || data == nil {
		return b.buildDefaultStressContext()
	}

	result := "<STRESS_TEST_BASELINE>\n"
	result += "  <current_conditions>\n"
	result += fmt.Sprintf("    <mortgage_rate>%.2f%%</mortgage_rate>\n", data.MortgageRate30Year)
	result += fmt.Sprintf("    <national_unemployment>%.1f%%</national_unemployment>\n", data.NationalUnemployment)
	if data.StateUnemploymentRate > 0 {
		result += fmt.Sprintf("    <state_unemployment>%.1f%%</state_unemployment>\n", data.StateUnemploymentRate)
	}
	result += fmt.Sprintf("    <inflation>%.1f%%</inflation>\n", data.InflationRate)
	result += fmt.Sprintf("    <rental_vacancy>%.1f%%</rental_vacancy>\n", data.RentalVacancyRate)
	result += "  </current_conditions>\n"

	// Suggested stress scenarios based on current conditions
	result += "  <scenario_guidance>\n"

	// Interest rate shock
	rateShock := data.MortgageRate30Year + 2.0
	result += fmt.Sprintf("    <interest_rate_shock>If rates rise to %.1f%%, monthly payments increase ~%.0f%% on new financing</interest_rate_shock>\n",
		rateShock, calculatePaymentIncrease(data.MortgageRate30Year, 2.0))

	// Recession scenario
	recessUnemployment := data.NationalUnemployment + 4.0
	result += fmt.Sprintf("    <recession_scenario>If unemployment rises to %.1f%%, expect rent pressure and vacancy increases</recession_scenario>\n",
		recessUnemployment)

	// Inflation scenario
	highInflation := data.InflationRate + 3.0
	result += fmt.Sprintf("    <stagflation_scenario>If inflation rises to %.1f%%, operating expenses increase but rate hikes may follow</stagflation_scenario>\n",
		highInflation)

	result += "  </scenario_guidance>\n"
	result += "</STRESS_TEST_BASELINE>"

	return result
}

// buildDefaultStressContext returns stress test context without live data
func (b *EconomicContextBuilder) buildDefaultStressContext() string {
	return `<STRESS_TEST_BASELINE>
  <current_conditions>
    <mortgage_rate>7.0%</mortgage_rate>
    <national_unemployment>4.0%</national_unemployment>
    <inflation>2.5%</inflation>
    <rental_vacancy>6.0%</rental_vacancy>
    <note>Using estimated baseline values</note>
  </current_conditions>
  <scenario_guidance>
    <interest_rate_shock>If rates rise to 9%, monthly payments increase ~20% on new financing</interest_rate_shock>
    <recession_scenario>If unemployment rises to 8%, expect rent pressure and vacancy increases</recession_scenario>
  </scenario_guidance>
</STRESS_TEST_BASELINE>`
}

// Helper functions

func getSourceInfo(sources map[string]string, key string) string {
	if date, ok := sources[key]; ok {
		return fmt.Sprintf("%s (as of %s)", key, date)
	}
	return key
}

func interpretPriceToIncome(ratio float64) string {
	switch {
	case ratio < 3:
		return "Very affordable - homes are less than 3x annual income"
	case ratio < 4:
		return "Affordable - within traditional lending standards"
	case ratio < 5:
		return "Moderately affordable - typical for growing metros"
	case ratio < 7:
		return "Less affordable - may stress household budgets"
	default:
		return "Unaffordable - significantly above income-based norms"
	}
}

func interpretAffordabilityIndex(index float64) string {
	switch {
	case index >= 120:
		return "Highly affordable - median family can easily afford median home"
	case index >= 100:
		return "Affordable - median family can afford median home"
	case index >= 80:
		return "Moderately affordable - median family may need to stretch"
	case index >= 60:
		return "Less affordable - median family faces challenges"
	default:
		return "Unaffordable - median home out of reach for median family"
	}
}

func calculatePaymentIncrease(currentRate, rateIncrease float64) float64 {
	// Simplified: Each 1% rate increase adds ~10-12% to payment
	return rateIncrease * 10
}
