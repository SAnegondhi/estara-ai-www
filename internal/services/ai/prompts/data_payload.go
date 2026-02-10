package prompts

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/economics"
	"github.com/estara-ai/www/internal/services/market/timeseries"
)

// DataPayload holds all assembled data for the V2 analysis prompt (ADR-074)
type DataPayload struct {
	// Location
	City  string
	State string

	// Housing market (from aggregator + metro time series)
	MedianHomePrice     int
	MedianRent          int
	PriceYoyChange      float64
	RentYoyChange       float64
	PriceCagr3Y         float64
	PriceCagr5Y         float64
	RentCagr3Y          float64
	GrossYield          float64
	NetYieldLow         float64
	NetYieldHigh        float64
	MortgageRate30      float64
	YieldToMortgageSpread float64

	// Supply/Demand (from city snapshot)
	InventoryCount     int
	MonthsOfSupply     float64
	MedianDaysOnMarket int
	MarketTemperature  string
	VacancyRate        float64
	HomesSold          int

	// Demographics (from economics aggregator)
	Population            int64
	MedianHouseholdIncome float64
	UnemploymentRate      float64
	NationalUnemployment  float64
	LaborForceParticipation float64
	CPIShelter            float64
	CPIRent               float64
	MedianAge             float64
	DemographicLevel      string // "place", "county", or "state"
	PovertyRate           float64

	// Affordability
	PriceToIncomeRatio  float64
	RentToIncomeRatio   float64
	AffordabilityIndex  float64
	AffordabilityBurden string
	HudFMR0BR           float64
	HudFMR1BR           float64
	HudFMR2BR           float64
	HudFMR3BR           float64
	HudFMR4BR           float64

	// National benchmarks
	NationalZHVI           float64
	NationalZORI           float64
	NationalGrossYield     float64
	NationalMortgage30     float64

	// Data quality
	TotalFields        int
	NonNAFields        int
	RealSources        []string
	DerivedMetrics     []string
	MissingData        []string
	UnavailableSources []string
	LaggedData         []string
}

// DataPayloadBuilder constructs DATA_PAYLOAD from existing services (ADR-074)
type DataPayloadBuilder struct {
	market    *aggregator.Aggregator
	economics *economics.Aggregator
	metro     *timeseries.MetroReader
	logger    *slog.Logger
}

// NewDataPayloadBuilder creates a new builder
func NewDataPayloadBuilder(
	market *aggregator.Aggregator,
	econ *economics.Aggregator,
	metro *timeseries.MetroReader,
) *DataPayloadBuilder {
	return &DataPayloadBuilder{
		market:    market,
		economics: econ,
		metro:     metro,
		logger:    slog.Default().With("component", "data_payload_builder"),
	}
}

// BuildPayload fetches all data in parallel and assembles into DataPayload
func (b *DataPayloadBuilder) BuildPayload(ctx context.Context, city, state string) (*DataPayload, error) {
	p := &DataPayload{
		City:           city,
		State:          state,
		DerivedMetrics: []string{"gross yield", "net yield range", "CAGRs", "price-to-income", "rent-to-income", "yield-to-mortgage spread"},
		MissingData:    []string{"zip-level submarket data", "observed cap rates", "transaction volume", "building permits"},
		LaggedData:     []string{},
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// 1. City snapshot (full city_market_cache row)
	var snapshot *timeseries.CitySnapshot
	wg.Add(1)
	go func() {
		defer wg.Done()
		s, err := b.metro.GetCitySnapshot(ctx, city, state)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			p.UnavailableSources = append(p.UnavailableSources, fmt.Sprintf("city_market_cache: %v", err))
			b.logger.Warn("city snapshot unavailable", "city", city, "state", state, "error", err)
		} else {
			snapshot = s
			p.RealSources = append(p.RealSources, "city_market_cache")
		}
	}()

	// 2. Economics aggregator (FRED + Census + BLS)
	var econ *economics.MarketEconomics
	wg.Add(1)
	go func() {
		defer wg.Done()
		if b.economics == nil || !b.economics.IsConfigured() {
			mu.Lock()
			p.UnavailableSources = append(p.UnavailableSources, "economics: not configured")
			mu.Unlock()
			return
		}
		e, err := b.economics.GetMarketEconomics(ctx, city, state)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			p.UnavailableSources = append(p.UnavailableSources, fmt.Sprintf("economics: %v", err))
			b.logger.Warn("economics data unavailable", "error", err)
		} else {
			econ = e
			for src := range e.Sources {
				p.RealSources = append(p.RealSources, src)
			}
			if len(e.Errors) > 0 {
				p.UnavailableSources = append(p.UnavailableSources, e.Errors...)
			}
		}
	}()

	// 3. Metro time series for CAGR calculations
	wg.Add(1)
	go func() {
		defer wg.Done()
		if b.metro == nil {
			return
		}
		// First get city-metro mapping
		mapping, err := b.metro.GetCityMetroMapping(ctx, city, state)
		if err != nil || mapping.MetroName == "" {
			mu.Lock()
			p.UnavailableSources = append(p.UnavailableSources, "metro_time_series: city mapping not found")
			mu.Unlock()
			return
		}

		now := time.Now()
		fiveYearsAgo := now.AddDate(-5, 0, 0)
		series, err := b.metro.GetTimeSeries(ctx, mapping.MetroName, "msa", fiveYearsAgo, now)
		if err != nil || len(series) == 0 {
			return
		}

		mu.Lock()
		defer mu.Unlock()
		p.PriceCagr3Y = computeCAGR(series, 3, true)
		p.PriceCagr5Y = computeCAGR(series, 5, true)
		p.RentCagr3Y = computeCAGR(series, 3, false)
		p.RealSources = append(p.RealSources, "Zillow ZHVI/ZORI time series")
	}()

	// 4. National benchmarks
	wg.Add(1)
	go func() {
		defer wg.Done()
		if b.metro == nil {
			return
		}
		nat, err := b.metro.GetNationalData(ctx)
		if err != nil {
			mu.Lock()
			p.UnavailableSources = append(p.UnavailableSources, "national benchmarks: "+err.Error())
			mu.Unlock()
			return
		}
		mu.Lock()
		defer mu.Unlock()
		p.NationalZHVI = nat.ZHVI
		p.NationalZORI = nat.ZORI
		p.NationalGrossYield = nat.GrossYield
	}()

	wg.Wait()

	// Merge city snapshot data
	if snapshot != nil {
		p.MedianHomePrice = int(snapshot.MedianHomePrice)
		p.MedianRent = int(snapshot.MedianRent)
		p.PriceYoyChange = snapshot.PriceYoyChange
		p.RentYoyChange = snapshot.RentYoyChange
		p.InventoryCount = snapshot.InventoryCount
		p.MonthsOfSupply = snapshot.MonthsOfSupply
		p.MedianDaysOnMarket = snapshot.MedianDaysOnMarket
		p.MarketTemperature = snapshot.MarketTemperature
		p.VacancyRate = snapshot.VacancyRate
		p.HomesSold = snapshot.HomesSold
		p.AffordabilityIndex = snapshot.AffordabilityIndex
		p.AffordabilityBurden = snapshot.AffordabilityBurden
		p.HudFMR0BR = snapshot.HudFMR0BR
		p.HudFMR1BR = snapshot.HudFMR1BR
		p.HudFMR2BR = snapshot.HudFMR2BR
		p.HudFMR3BR = snapshot.HudFMR3BR
		p.HudFMR4BR = snapshot.HudFMR4BR
	}

	// Merge economics data
	if econ != nil {
		p.MortgageRate30 = econ.MortgageRate30Year
		p.NationalMortgage30 = econ.MortgageRate30Year // Same — it's a national rate
		p.Population = econ.Population
		p.MedianHouseholdIncome = econ.MedianHouseholdIncome
		p.UnemploymentRate = econ.StateUnemploymentRate
		p.NationalUnemployment = econ.NationalUnemployment
		p.LaborForceParticipation = econ.LaborForceParticipation
		p.CPIShelter = econ.CPIShelter
		p.CPIRent = econ.CPIRent
		p.MedianAge = econ.MedianAge
		p.DemographicLevel = econ.DemographicLevel
		p.PovertyRate = econ.PovertyRate

		// Add lagged data notice for Census
		if src, ok := econ.Sources["census"]; ok {
			p.LaggedData = append(p.LaggedData, fmt.Sprintf("Census ACS (%s)", src))
		}
	}

	// Compute derived metrics
	if p.MedianHomePrice > 0 && p.MedianRent > 0 {
		p.GrossYield = (float64(p.MedianRent) * 12 / float64(p.MedianHomePrice)) * 100
		p.NetYieldLow = p.GrossYield * 0.45  // 55% expense ratio
		p.NetYieldHigh = p.GrossYield * 0.60 // 40% expense ratio
	}

	if p.GrossYield > 0 && p.MortgageRate30 > 0 {
		p.YieldToMortgageSpread = (p.GrossYield - p.MortgageRate30) * 100 // in bps
	}

	// Price-to-income and rent-to-income
	if p.MedianHouseholdIncome > 0 && p.MedianHomePrice > 0 {
		p.PriceToIncomeRatio = float64(p.MedianHomePrice) / p.MedianHouseholdIncome
	}
	if p.MedianHouseholdIncome > 0 && p.MedianRent > 0 {
		p.RentToIncomeRatio = (float64(p.MedianRent) * 12 / p.MedianHouseholdIncome) * 100
	}

	// Data quality scoring
	b.computeDataQuality(p)

	return p, nil
}

// FormatAsXML formats the payload as structured XML for the prompt
func (b *DataPayloadBuilder) FormatAsXML(p *DataPayload) string {
	var sb strings.Builder
	now := time.Now().Format("2006-01-02")

	sb.WriteString(fmt.Sprintf(`<DATA_PAYLOAD generated="%s" location="%s, %s">`, now, p.City, p.State))
	sb.WriteString("\n\n  <HOUSING_MARKET>\n")
	writeXMLField(&sb, "median_home_price", fmtDollar(p.MedianHomePrice), "Zillow ZHVI", "high", p.MedianHomePrice > 0)
	writeXMLField(&sb, "median_rent", fmtDollarMo(p.MedianRent), "Zillow ZORI", "high", p.MedianRent > 0)
	writeXMLField(&sb, "price_yoy_change", fmtPct(p.PriceYoyChange), "Zillow ZHVI", "high", p.PriceYoyChange != 0)
	writeXMLField(&sb, "price_cagr_3y", fmtPct(p.PriceCagr3Y), "Zillow ZHVI time series", "high", p.PriceCagr3Y != 0)
	writeXMLField(&sb, "price_cagr_5y", fmtPct(p.PriceCagr5Y), "Zillow ZHVI time series", "high", p.PriceCagr5Y != 0)
	writeXMLField(&sb, "rent_yoy_change", fmtPct(p.RentYoyChange), "Zillow ZORI", "high", p.RentYoyChange != 0)
	writeXMLDerived(&sb, "gross_yield", fmtPct(p.GrossYield), "(ZORI*12)/ZHVI", "medium", p.GrossYield > 0)
	sb.WriteString(fmt.Sprintf(`    <net_yield_range estimated="true" expense_ratio="40-55%%" confidence="low">%s - %s</net_yield_range>`+"\n",
		fmtPct(p.NetYieldLow), fmtPct(p.NetYieldHigh)))
	writeXMLField(&sb, "mortgage_30y", fmtPct(p.MortgageRate30), "FRED MORTGAGE30US", "high", p.MortgageRate30 > 0)
	writeXMLDerived(&sb, "yield_to_mortgage_spread", fmt.Sprintf("%.0f bps", p.YieldToMortgageSpread), "gross_yield - mortgage_30y", "medium", p.YieldToMortgageSpread != 0)
	sb.WriteString("  </HOUSING_MARKET>\n")

	sb.WriteString("\n  <SUPPLY_DEMAND>\n")
	writeXMLField(&sb, "inventory_count", fmtInt(p.InventoryCount), "city_market_cache", "high", p.InventoryCount > 0)
	writeXMLField(&sb, "months_of_supply", fmt.Sprintf("%.1f", p.MonthsOfSupply), "city_market_cache", "high", p.MonthsOfSupply > 0)
	writeXMLField(&sb, "median_days_on_market", fmtInt(p.MedianDaysOnMarket), "city_market_cache", "high", p.MedianDaysOnMarket > 0)
	writeXMLField(&sb, "market_temperature", p.MarketTemperature, "city_market_cache", "high", p.MarketTemperature != "")
	writeXMLField(&sb, "vacancy_rate", fmtPct(p.VacancyRate), "city_market_cache", "medium", p.VacancyRate > 0)
	writeXMLField(&sb, "homes_sold", fmtInt(p.HomesSold), "city_market_cache", "high", p.HomesSold > 0)
	sb.WriteString("    <building_permits>NOT_AVAILABLE — future enhancement (Phase 1.5)</building_permits>\n")
	sb.WriteString("  </SUPPLY_DEMAND>\n")

	sb.WriteString("\n  <DEMOGRAPHICS>\n")
	if p.DemographicLevel != "" {
		sb.WriteString(fmt.Sprintf("    <data_level>%s</data_level>\n", p.DemographicLevel))
	}
	writeXMLField(&sb, "population", fmtInt64(p.Population), "Census ACS", "high", p.Population > 0)
	writeXMLField(&sb, "median_household_income", fmtDollarF(p.MedianHouseholdIncome), "Census ACS", "high", p.MedianHouseholdIncome > 0)
	writeXMLField(&sb, "median_age", fmt.Sprintf("%.1f", p.MedianAge), "Census ACS", "high", p.MedianAge > 0)
	writeXMLField(&sb, "poverty_rate", fmtPct(p.PovertyRate), "Census ACS", "high", p.PovertyRate > 0)
	writeXMLField(&sb, "state_unemployment_rate", fmtPct(p.UnemploymentRate), "BLS", "high", p.UnemploymentRate > 0)
	writeXMLField(&sb, "national_unemployment_rate", fmtPct(p.NationalUnemployment), "FRED", "high", p.NationalUnemployment > 0)
	writeXMLField(&sb, "labor_force_participation", fmtPct(p.LaborForceParticipation), "BLS", "high", p.LaborForceParticipation > 0)
	writeXMLField(&sb, "cpi_shelter_index", fmt.Sprintf("%.1f", p.CPIShelter), "BLS", "high", p.CPIShelter > 0)
	writeXMLField(&sb, "cpi_rent_index", fmt.Sprintf("%.1f", p.CPIRent), "BLS", "high", p.CPIRent > 0)
	sb.WriteString("    <cpi_note>CPI values are index numbers, not percentages. Compare current to historical for YoY change.</cpi_note>\n")
	sb.WriteString("  </DEMOGRAPHICS>\n")

	sb.WriteString("\n  <AFFORDABILITY>\n")
	writeXMLDerived(&sb, "price_to_income_ratio", fmt.Sprintf("%.1fx", p.PriceToIncomeRatio), "ZHVI/MHI", "medium", p.PriceToIncomeRatio > 0)
	writeXMLDerived(&sb, "rent_to_income_ratio", fmtPct(p.RentToIncomeRatio), "(ZORI*12)/MHI", "medium", p.RentToIncomeRatio > 0)
	writeXMLField(&sb, "affordability_index", fmt.Sprintf("%.1f", p.AffordabilityIndex), "city_market_cache", "medium", p.AffordabilityIndex > 0)
	if p.AffordabilityBurden != "" {
		writeXMLField(&sb, "affordability_burden", p.AffordabilityBurden, "city_market_cache", "medium", true)
	}
	writeXMLField(&sb, "hud_fmr_0br", fmtDollarF(p.HudFMR0BR), "city_market_cache", "high", p.HudFMR0BR > 0)
	writeXMLField(&sb, "hud_fmr_1br", fmtDollarF(p.HudFMR1BR), "city_market_cache", "high", p.HudFMR1BR > 0)
	writeXMLField(&sb, "hud_fmr_2br", fmtDollarF(p.HudFMR2BR), "city_market_cache", "high", p.HudFMR2BR > 0)
	writeXMLField(&sb, "hud_fmr_3br", fmtDollarF(p.HudFMR3BR), "city_market_cache", "high", p.HudFMR3BR > 0)
	writeXMLField(&sb, "hud_fmr_4br", fmtDollarF(p.HudFMR4BR), "city_market_cache", "high", p.HudFMR4BR > 0)
	sb.WriteString("  </AFFORDABILITY>\n")

	sb.WriteString("\n  <NATIONAL_BENCHMARKS>\n")
	writeXMLField(&sb, "national_zhvi", fmtDollarF(p.NationalZHVI), "Zillow ZHVI (metro_region_id=0)", "high", p.NationalZHVI > 0)
	writeXMLField(&sb, "national_zori", fmtDollarFMo(p.NationalZORI), "Zillow ZORI (metro_region_id=0)", "high", p.NationalZORI > 0)
	writeXMLDerived(&sb, "national_gross_yield", fmtPct(p.NationalGrossYield), "(national_ZORI*12)/national_ZHVI", "medium", p.NationalGrossYield > 0)
	writeXMLField(&sb, "national_unemployment", fmtPct(p.NationalUnemployment), "FRED", "high", p.NationalUnemployment > 0)
	writeXMLField(&sb, "national_mortgage_30y", fmtPct(p.NationalMortgage30), "FRED", "high", p.NationalMortgage30 > 0)
	sb.WriteString("  </NATIONAL_BENCHMARKS>\n")

	// Data quality
	pct := 0
	if p.TotalFields > 0 {
		pct = (p.NonNAFields * 100) / p.TotalFields
	}
	sb.WriteString("\n  <DATA_QUALITY>\n")
	sb.WriteString(fmt.Sprintf("    <data_completeness_score>%d/%d (%d%%)</data_completeness_score>\n", p.NonNAFields, p.TotalFields, pct))
	sb.WriteString(fmt.Sprintf("    <real_sources>%s</real_sources>\n", strings.Join(dedupe(p.RealSources), ", ")))
	sb.WriteString(fmt.Sprintf("    <derived_metrics>%s</derived_metrics>\n", strings.Join(p.DerivedMetrics, ", ")))
	sb.WriteString(fmt.Sprintf("    <missing_data>%s</missing_data>\n", strings.Join(p.MissingData, ", ")))
	if len(p.UnavailableSources) > 0 {
		sb.WriteString(fmt.Sprintf("    <unavailable_sources>%s</unavailable_sources>\n", strings.Join(p.UnavailableSources, ", ")))
	}
	if len(p.LaggedData) > 0 {
		sb.WriteString(fmt.Sprintf("    <lagged_data>%s</lagged_data>\n", strings.Join(p.LaggedData, ", ")))
	}
	sb.WriteString("    <note>Cap rates are NOT observed market data. Gross yield is a proxy. See ANALYSIS_INSTRUCTIONS.</note>\n")
	sb.WriteString("  </DATA_QUALITY>\n")

	sb.WriteString("\n</DATA_PAYLOAD>")
	return sb.String()
}

// computeDataQuality counts non-N/A fields for data completeness
func (b *DataPayloadBuilder) computeDataQuality(p *DataPayload) {
	checks := []struct {
		name  string
		valid bool
	}{
		{"median_home_price", p.MedianHomePrice > 0},
		{"median_rent", p.MedianRent > 0},
		{"price_yoy_change", p.PriceYoyChange != 0},
		{"price_cagr_3y", p.PriceCagr3Y != 0},
		{"price_cagr_5y", p.PriceCagr5Y != 0},
		{"rent_yoy_change", p.RentYoyChange != 0},
		{"mortgage_30y", p.MortgageRate30 > 0},
		{"inventory_count", p.InventoryCount > 0},
		{"months_of_supply", p.MonthsOfSupply > 0},
		{"median_days_on_market", p.MedianDaysOnMarket > 0},
		{"market_temperature", p.MarketTemperature != ""},
		{"vacancy_rate", p.VacancyRate > 0},
		{"population", p.Population > 0},
		{"median_household_income", p.MedianHouseholdIncome > 0},
		{"unemployment_rate", p.UnemploymentRate > 0},
		{"labor_force_participation", p.LaborForceParticipation > 0},
		{"cpi_shelter", p.CPIShelter > 0},
		{"affordability_index", p.AffordabilityIndex > 0},
		{"hud_fmr_2br", p.HudFMR2BR > 0},
		{"national_zhvi", p.NationalZHVI > 0},
		{"national_zori", p.NationalZORI > 0},
	}

	p.TotalFields = len(checks)
	for _, c := range checks {
		if c.valid {
			p.NonNAFields++
		}
	}
}

// computeCAGR calculates compound annual growth rate from time series
func computeCAGR(series []timeseries.MetroData, years int, isPrice bool) float64 {
	if len(series) < 2 {
		return 0
	}

	// Find latest and N-years-ago values
	latest := series[len(series)-1]
	targetDate := latest.Date.AddDate(-years, 0, 0)

	var startVal float64
	for _, d := range series {
		if !d.Date.Before(targetDate) {
			if isPrice {
				startVal = d.ZHVI
			} else {
				startVal = d.ZORI
			}
			break
		}
	}

	var endVal float64
	if isPrice {
		endVal = latest.ZHVI
	} else {
		endVal = latest.ZORI
	}

	if startVal <= 0 || endVal <= 0 {
		return 0
	}

	cagr := (math.Pow(endVal/startVal, 1.0/float64(years)) - 1) * 100
	return math.Round(cagr*100) / 100 // 2 decimal places
}

// XML formatting helpers

func writeXMLField(sb *strings.Builder, name, value, source, confidence string, valid bool) {
	if !valid {
		sb.WriteString(fmt.Sprintf("    <%s>N/A — %s unavailable</%s>\n", name, source, name))
		return
	}
	sb.WriteString(fmt.Sprintf(`    <%s source="%s" confidence="%s">%s</%s>`+"\n", name, source, confidence, value, name))
}

func writeXMLDerived(sb *strings.Builder, name, value, formula, confidence string, valid bool) {
	if !valid {
		sb.WriteString(fmt.Sprintf("    <%s>N/A — insufficient data to derive</%s>\n", name, name))
		return
	}
	sb.WriteString(fmt.Sprintf(`    <%s derived="true" formula="%s" confidence="%s">%s</%s>`+"\n", name, formula, confidence, value, name))
}

func fmtDollar(v int) string {
	if v == 0 {
		return "N/A"
	}
	return fmt.Sprintf("$%s", addCommas(v))
}

func fmtDollarMo(v int) string {
	if v == 0 {
		return "N/A"
	}
	return fmt.Sprintf("$%s/mo", addCommas(v))
}

func fmtDollarF(v float64) string {
	if v == 0 {
		return "N/A"
	}
	return fmt.Sprintf("$%s", addCommas(int(math.Round(v))))
}

func fmtDollarFMo(v float64) string {
	if v == 0 {
		return "N/A"
	}
	return fmt.Sprintf("$%s/mo", addCommas(int(math.Round(v))))
}

func fmtPct(v float64) string {
	if v == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%.2f%%", v)
}

func fmtInt(v int) string {
	if v == 0 {
		return "N/A"
	}
	return addCommas(v)
}

func fmtInt64(v int64) string {
	if v == 0 {
		return "N/A"
	}
	return addCommas(int(v))
}

func addCommas(n int) string {
	if n < 0 {
		return "-" + addCommas(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	result := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result += ","
		}
		result += string(c)
	}
	return result
}

func dedupe(ss []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
