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
	RentCagr5Y          float64
	GrossYield          float64
	NetYieldLow         float64
	NetYieldHigh        float64
	MortgageRate30      float64
	YieldToMortgageSpread float64

	// Supply/Demand (from city snapshot + Redfin metro data)
	InventoryCount     int
	MonthsOfSupply     float64
	MedianDaysOnMarket int
	MarketTemperature  string
	VacancyRate        float64
	HomesSold          int
	NewListings        int

	// Competitive Indicators (from Redfin metro data)
	MedianSalePrice    float64
	MedianSalePriceYoy float64
	MedianPpsf         float64
	AvgSaleToList      float64
	SoldAboveList      float64 // Fraction of homes sold above list price
	PriceDrops         float64 // Fraction of listings with price drops
	OffMarketIn2Weeks  float64 // Fraction going off-market within 2 weeks
	HasRedfinData      bool

	// Demographics (from economics aggregator — Census ACS)
	Population            int64
	MedianHouseholdIncome float64
	PerCapitaIncome       float64
	HouseholdCount        int64
	MedianAge             float64
	DemographicLevel      string // "place", "county", or "state"
	PovertyRate           float64

	// Labor market (from economics aggregator — BLS + FRED)
	UnemploymentRate        float64
	NationalUnemployment    float64
	LaborForceParticipation float64
	StateEmployment         float64 // thousands
	ConstructionEmployment  float64 // thousands (state)
	JobOpenings             float64 // thousands (national)
	AverageHourlyEarnings   float64 // dollars
	CPIShelter              float64
	CPIRent                 float64

	// Additional rates (from FRED)
	MortgageRate15    float64
	InflationRate     float64
	RentalVacancyRate float64 // national rental vacancy

	// Derived market metrics
	PriceToRentRatio float64 // ZHVI / (ZORI*12)
	ForecastGrowth   float64 // ZHVF vs current ZHVI, pct

	// Building permits & housing starts (from FRED — national monthly)
	BuildingPermits float64 // thousands, national
	HousingStarts   float64 // thousands, national

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

	// Zip submarket analysis (ADR-076: from zip_time_series)
	ZipCount    int
	ZipMinZHVI  float64
	ZipMaxZHVI  float64
	ZipMedianZHVI float64
	ZipMinZORI  float64
	ZipMaxZORI  float64

	// National benchmarks (Zillow + Redfin)
	NationalZHVI           float64
	NationalZORI           float64
	NationalGrossYield     float64
	NationalMortgage30     float64
	NationalInventory      int
	NationalMonthsOfSupply float64
	NationalMedianDom      int
	NationalHomesSold      int
	NationalAvgSaleToList  float64
	NationalSoldAboveList  float64
	NationalPriceDrops     float64

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
		DerivedMetrics: []string{"gross yield", "net yield range", "CAGRs", "price-to-income", "price-to-rent", "rent-to-income", "yield-to-mortgage spread"},
		MissingData:    []string{"observed cap rates"},
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
			p.UnavailableSources = append(p.UnavailableSources, fmt.Sprintf("Zillow/Redfin market data: %v", err))
			b.logger.Warn("city snapshot unavailable", "city", city, "state", state, "error", err)
		} else {
			snapshot = s
			p.RealSources = append(p.RealSources, "Zillow Research")
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

	// 3. Time series for CAGR calculations + forecast growth
	// Try city-level first (more accurate), fall back to metro-level
	wg.Add(1)
	go func() {
		defer wg.Done()
		if b.metro == nil {
			return
		}

		now := time.Now()
		fiveYearsAgo := now.AddDate(-5, 0, 0)

		// Try city-level time series first (21K cities in city_time_series)
		citySeries, err := b.metro.GetCityTimeSeries(ctx, city, state, fiveYearsAgo, now)
		if err == nil && len(citySeries) > 0 {
			mu.Lock()
			p.PriceCagr3Y = computeCAGR(citySeries, 3, true)
			p.PriceCagr5Y = computeCAGR(citySeries, 5, true)
			p.RentCagr3Y = computeCAGR(citySeries, 3, false)
			p.RentCagr5Y = computeCAGR(citySeries, 5, false)
			p.RealSources = append(p.RealSources, "Zillow city-level time series")
			mu.Unlock()
		} else {
			// Fall back to metro-level time series
			mapping, mapErr := b.metro.GetCityMetroMapping(ctx, city, state)
			if mapErr != nil || mapping.MetroName == "" {
				mu.Lock()
				p.UnavailableSources = append(p.UnavailableSources, "time_series: city/metro mapping not found")
				mu.Unlock()
				return
			}

			series, serErr := b.metro.GetTimeSeries(ctx, mapping.MetroName, "msa", fiveYearsAgo, now)
			if serErr != nil || len(series) == 0 {
				return
			}

			mu.Lock()
			p.PriceCagr3Y = computeCAGR(series, 3, true)
			p.PriceCagr5Y = computeCAGR(series, 5, true)
			p.RentCagr3Y = computeCAGR(series, 3, false)
			p.RentCagr5Y = computeCAGR(series, 5, false)
			p.RealSources = append(p.RealSources, "Zillow metro-level time series")
			mu.Unlock()
		}

		// Forecast growth from metro ZHVF (only available at metro level)
		mapping, err := b.metro.GetCityMetroMapping(ctx, city, state)
		if err == nil && mapping.MetroRegionID > 0 {
			metroData, mErr := b.metro.GetLatestDataByID(ctx, mapping.MetroRegionID)
			if mErr == nil && metroData.ZHVI > 0 && metroData.ZHVIForecast > 0 {
				mu.Lock()
				p.ForecastGrowth = ((metroData.ZHVIForecast - metroData.ZHVI) / metroData.ZHVI) * 100
				mu.Unlock()
			}
		}
	}()

	// 4. National benchmarks (from metro_time_series where metro_region_id=0)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if b.metro == nil {
			return
		}
		nat, err := b.metro.GetNationalData(ctx)
		if err != nil {
			mu.Lock()
			p.UnavailableSources = append(p.UnavailableSources, "national ZHVI/ZORI: "+err.Error())
			mu.Unlock()
			return
		}
		mu.Lock()
		defer mu.Unlock()
		p.NationalZHVI = nat.ZHVI
		p.NationalZORI = nat.ZORI
		p.NationalGrossYield = nat.GrossYield
		if nat.HasRedfinData {
			p.NationalInventory = nat.Inventory
			p.NationalMonthsOfSupply = nat.MonthsOfSupply
			p.NationalMedianDom = nat.MedianDom
			p.NationalHomesSold = nat.HomesSold
			p.NationalAvgSaleToList = nat.AvgSaleToList
			p.NationalSoldAboveList = nat.SoldAboveList
			p.NationalPriceDrops = nat.PriceDrops
		}
	}()

	// 5. Zip submarket analysis (ADR-076: from zip_time_series)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if b.metro == nil {
			return
		}
		summary, err := b.metro.GetZipSubmarketSummary(ctx, city, state)
		if err != nil {
			// Not a critical failure — zip data may not exist for all cities
			b.logger.Debug("zip submarket data unavailable", "city", city, "state", state, "error", err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		p.ZipCount = summary.ZipCount
		p.ZipMinZHVI = summary.MinZHVI
		p.ZipMaxZHVI = summary.MaxZHVI
		p.ZipMedianZHVI = summary.MedianZHVI
		p.ZipMinZORI = summary.MinZORI
		p.ZipMaxZORI = summary.MaxZORI
		p.RealSources = append(p.RealSources, "Zillow zip-level data")
	}()

	wg.Wait()

	// Merge city snapshot data (city_market_cache + Redfin metro backfill)
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
		p.NewListings = snapshot.NewListings
		p.AffordabilityIndex = snapshot.AffordabilityIndex
		p.AffordabilityBurden = snapshot.AffordabilityBurden
		p.HudFMR0BR = snapshot.HudFMR0BR
		p.HudFMR1BR = snapshot.HudFMR1BR
		p.HudFMR2BR = snapshot.HudFMR2BR
		p.HudFMR3BR = snapshot.HudFMR3BR
		p.HudFMR4BR = snapshot.HudFMR4BR

		// Redfin competitive indicators
		if snapshot.HasRedfinData {
			p.HasRedfinData = true
			p.MedianSalePrice = snapshot.MedianSalePrice
			p.MedianSalePriceYoy = snapshot.MedianSalePriceYoy
			p.MedianPpsf = snapshot.MedianPpsf
			p.AvgSaleToList = snapshot.AvgSaleToList
			p.SoldAboveList = snapshot.SoldAboveList
			p.PriceDrops = snapshot.PriceDrops
			p.OffMarketIn2Weeks = snapshot.OffMarketIn2Weeks
			p.RealSources = append(p.RealSources, "Redfin")
		}
	}

	// Merge economics data
	if econ != nil {
		// FRED national rates
		p.MortgageRate30 = econ.MortgageRate30Year
		p.MortgageRate15 = econ.MortgageRate15Year
		p.NationalMortgage30 = econ.MortgageRate30Year // Same — it's a national rate
		p.NationalUnemployment = econ.NationalUnemployment
		p.InflationRate = econ.InflationRate
		p.RentalVacancyRate = econ.RentalVacancyRate

		// Census demographics
		p.Population = econ.Population
		p.MedianHouseholdIncome = econ.MedianHouseholdIncome
		p.PerCapitaIncome = econ.PerCapitaIncome
		p.HouseholdCount = econ.HouseholdCount
		p.MedianAge = econ.MedianAge
		p.DemographicLevel = econ.DemographicLevel
		p.PovertyRate = econ.PovertyRate

		// BLS labor market
		p.UnemploymentRate = econ.StateUnemploymentRate
		p.LaborForceParticipation = econ.LaborForceParticipation
		p.StateEmployment = econ.StateEmployment
		p.ConstructionEmployment = econ.ConstructionEmployment
		p.JobOpenings = econ.JobOpenings
		p.AverageHourlyEarnings = econ.AverageHourlyEarnings
		p.CPIShelter = econ.CPIShelter
		p.CPIRent = econ.CPIRent

		// Building permits & housing starts (national)
		p.BuildingPermits = econ.BuildingPermits
		p.HousingStarts = econ.HousingStarts

		// Add lagged data notice for Census
		if src, ok := econ.Sources["census"]; ok {
			p.LaggedData = append(p.LaggedData, fmt.Sprintf("Census ACS (%s)", src))
		}
	}

	// National ZHVI/ZORI from metro_region_id=0 (ADR-075 bootstrapped this data)
	if p.NationalZHVI == 0 && p.NationalZORI == 0 {
		p.MissingData = append(p.MissingData, "national ZHVI/ZORI benchmarks")
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

	// Price-to-income, rent-to-income, price-to-rent
	if p.MedianHouseholdIncome > 0 && p.MedianHomePrice > 0 {
		p.PriceToIncomeRatio = float64(p.MedianHomePrice) / p.MedianHouseholdIncome
	}
	if p.MedianHouseholdIncome > 0 && p.MedianRent > 0 {
		p.RentToIncomeRatio = (float64(p.MedianRent) * 12 / p.MedianHouseholdIncome) * 100
	}
	if p.MedianHomePrice > 0 && p.MedianRent > 0 {
		p.PriceToRentRatio = float64(p.MedianHomePrice) / (float64(p.MedianRent) * 12)
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
	writeXMLField(&sb, "rent_cagr_3y", fmtPct(p.RentCagr3Y), "Zillow ZORI time series", "high", p.RentCagr3Y != 0)
	writeXMLField(&sb, "rent_cagr_5y", fmtPct(p.RentCagr5Y), "Zillow ZORI time series", "high", p.RentCagr5Y != 0)
	writeXMLDerived(&sb, "gross_yield", fmtPct(p.GrossYield), "(ZORI*12)/ZHVI", "medium", p.GrossYield > 0)
	sb.WriteString(fmt.Sprintf(`    <net_yield_range estimated="true" expense_ratio="40-55%%" confidence="low">%s - %s</net_yield_range>`+"\n",
		fmtPct(p.NetYieldLow), fmtPct(p.NetYieldHigh)))
	writeXMLField(&sb, "mortgage_30y", fmtPct(p.MortgageRate30), "FRED MORTGAGE30US", "high", p.MortgageRate30 > 0)
	writeXMLField(&sb, "mortgage_15y", fmtPct(p.MortgageRate15), "FRED MORTGAGE15US", "high", p.MortgageRate15 > 0)
	writeXMLDerived(&sb, "yield_to_mortgage_spread", fmt.Sprintf("%.0f bps", p.YieldToMortgageSpread), "gross_yield - mortgage_30y", "medium", p.YieldToMortgageSpread != 0)
	if p.ForecastGrowth != 0 {
		writeXMLField(&sb, "zhvi_forecast_growth", fmtPct(p.ForecastGrowth), "Zillow ZHVF", "medium", true)
	}
	sb.WriteString("  </HOUSING_MARKET>\n")

	sb.WriteString("\n  <SUPPLY_DEMAND>\n")
	writeXMLField(&sb, "inventory_count", fmtInt(p.InventoryCount), "Redfin", "high", p.InventoryCount > 0)
	writeXMLField(&sb, "months_of_supply", fmt.Sprintf("%.1f", p.MonthsOfSupply), "Redfin", "high", p.MonthsOfSupply > 0)
	writeXMLField(&sb, "median_days_on_market", fmtInt(p.MedianDaysOnMarket), "Redfin", "high", p.MedianDaysOnMarket > 0)
	writeXMLField(&sb, "market_temperature", p.MarketTemperature, "Zillow Research", "high", p.MarketTemperature != "")
	writeXMLField(&sb, "vacancy_rate", fmtPct(p.VacancyRate), "Zillow Research", "medium", p.VacancyRate > 0)
	writeXMLField(&sb, "homes_sold", fmtInt(p.HomesSold), "Redfin", "high", p.HomesSold > 0)
	writeXMLField(&sb, "new_listings", fmtInt(p.NewListings), "Redfin", "high", p.NewListings > 0)
	writeXMLField(&sb, "building_permits_national", fmt.Sprintf("%.0fK units/yr", p.BuildingPermits), "FRED PERMIT", "high", p.BuildingPermits > 0)
	writeXMLField(&sb, "housing_starts_national", fmt.Sprintf("%.0fK units/yr", p.HousingStarts), "FRED HOUST", "high", p.HousingStarts > 0)
	if p.BuildingPermits == 0 {
		sb.WriteString("    <building_permits_note>See TAX_REGULATORY_INSURANCE section below for metro-level supply pipeline data</building_permits_note>\n")
	}
	sb.WriteString("  </SUPPLY_DEMAND>\n")

	// Competitive indicators from Redfin
	if p.HasRedfinData {
		sb.WriteString("\n  <COMPETITIVE_INDICATORS>\n")
		writeXMLField(&sb, "median_sale_price", fmtDollarF(p.MedianSalePrice), "Redfin", "high", p.MedianSalePrice > 0)
		writeXMLField(&sb, "median_sale_price_yoy", fmtPct(p.MedianSalePriceYoy*100), "Redfin", "high", p.MedianSalePriceYoy != 0)
		writeXMLField(&sb, "median_price_per_sqft", fmtDollarF(p.MedianPpsf), "Redfin", "high", p.MedianPpsf > 0)
		writeXMLField(&sb, "avg_sale_to_list_ratio", fmt.Sprintf("%.1f%%", p.AvgSaleToList*100), "Redfin", "high", p.AvgSaleToList > 0)
		writeXMLField(&sb, "sold_above_list_pct", fmt.Sprintf("%.1f%%", p.SoldAboveList*100), "Redfin", "high", p.SoldAboveList > 0)
		writeXMLField(&sb, "price_drops_pct", fmt.Sprintf("%.1f%%", p.PriceDrops*100), "Redfin", "high", p.PriceDrops > 0)
		writeXMLField(&sb, "off_market_in_2_weeks_pct", fmt.Sprintf("%.1f%%", p.OffMarketIn2Weeks*100), "Redfin", "high", p.OffMarketIn2Weeks > 0)
		sb.WriteString("  </COMPETITIVE_INDICATORS>\n")
	}

	sb.WriteString("\n  <DEMOGRAPHICS>\n")
	if p.DemographicLevel != "" {
		sb.WriteString(fmt.Sprintf("    <data_level>%s</data_level>\n", p.DemographicLevel))
	}
	writeXMLField(&sb, "population", fmtInt64(p.Population), "Census ACS", "high", p.Population > 0)
	writeXMLField(&sb, "median_household_income", fmtDollarF(p.MedianHouseholdIncome), "Census ACS", "high", p.MedianHouseholdIncome > 0)
	writeXMLField(&sb, "per_capita_income", fmtDollarF(p.PerCapitaIncome), "Census ACS", "high", p.PerCapitaIncome > 0)
	writeXMLField(&sb, "household_count", fmtInt64(p.HouseholdCount), "Census ACS", "high", p.HouseholdCount > 0)
	writeXMLField(&sb, "median_age", fmt.Sprintf("%.1f", p.MedianAge), "Census ACS", "high", p.MedianAge > 0)
	writeXMLField(&sb, "poverty_rate", fmtPct(p.PovertyRate), "Census ACS", "high", p.PovertyRate > 0)
	sb.WriteString("  </DEMOGRAPHICS>\n")

	sb.WriteString("\n  <LABOR_MARKET>\n")
	writeXMLField(&sb, "state_unemployment_rate", fmtPct(p.UnemploymentRate), "BLS", "high", p.UnemploymentRate > 0)
	writeXMLField(&sb, "national_unemployment_rate", fmtPct(p.NationalUnemployment), "FRED", "high", p.NationalUnemployment > 0)
	writeXMLField(&sb, "labor_force_participation", fmtPct(p.LaborForceParticipation), "BLS", "high", p.LaborForceParticipation > 0)
	writeXMLField(&sb, "state_employment", fmt.Sprintf("%.1fK", p.StateEmployment), "BLS", "high", p.StateEmployment > 0)
	writeXMLField(&sb, "construction_employment", fmt.Sprintf("%.1fK", p.ConstructionEmployment), "BLS", "high", p.ConstructionEmployment > 0)
	writeXMLField(&sb, "job_openings", fmt.Sprintf("%.0fK", p.JobOpenings), "BLS JOLTS", "high", p.JobOpenings > 0)
	writeXMLField(&sb, "avg_hourly_earnings", fmtDollarF(p.AverageHourlyEarnings), "BLS CES", "high", p.AverageHourlyEarnings > 0)
	writeXMLField(&sb, "cpi_shelter_index", fmt.Sprintf("%.1f", p.CPIShelter), "BLS", "high", p.CPIShelter > 0)
	writeXMLField(&sb, "cpi_rent_index", fmt.Sprintf("%.1f", p.CPIRent), "BLS", "high", p.CPIRent > 0)
	sb.WriteString("    <cpi_note>CPI values are index numbers, not percentages. Compare current to historical for YoY change.</cpi_note>\n")
	sb.WriteString("  </LABOR_MARKET>\n")

	sb.WriteString("\n  <AFFORDABILITY>\n")
	writeXMLDerived(&sb, "price_to_income_ratio", fmt.Sprintf("%.1fx", p.PriceToIncomeRatio), "ZHVI/MHI", "medium", p.PriceToIncomeRatio > 0)
	writeXMLDerived(&sb, "price_to_rent_ratio", fmt.Sprintf("%.1fx", p.PriceToRentRatio), "ZHVI/(ZORI*12)", "medium", p.PriceToRentRatio > 0)
	writeXMLDerived(&sb, "rent_to_income_ratio", fmtPct(p.RentToIncomeRatio), "(ZORI*12)/MHI", "medium", p.RentToIncomeRatio > 0)
	writeXMLField(&sb, "affordability_index", fmt.Sprintf("%.1f", p.AffordabilityIndex), "Zillow/Redfin", "medium", p.AffordabilityIndex > 0)
	if p.AffordabilityBurden != "" {
		writeXMLField(&sb, "affordability_burden", p.AffordabilityBurden, "Zillow/Redfin", "medium", true)
	}
	writeXMLField(&sb, "hud_fmr_0br", fmtDollarF(p.HudFMR0BR), "HUD Fair Market Rent", "high", p.HudFMR0BR > 0)
	writeXMLField(&sb, "hud_fmr_1br", fmtDollarF(p.HudFMR1BR), "HUD Fair Market Rent", "high", p.HudFMR1BR > 0)
	writeXMLField(&sb, "hud_fmr_2br", fmtDollarF(p.HudFMR2BR), "HUD Fair Market Rent", "high", p.HudFMR2BR > 0)
	writeXMLField(&sb, "hud_fmr_3br", fmtDollarF(p.HudFMR3BR), "HUD Fair Market Rent", "high", p.HudFMR3BR > 0)
	writeXMLField(&sb, "hud_fmr_4br", fmtDollarF(p.HudFMR4BR), "HUD Fair Market Rent", "high", p.HudFMR4BR > 0)
	sb.WriteString("  </AFFORDABILITY>\n")

	sb.WriteString("\n  <NATIONAL_BENCHMARKS>\n")
	writeXMLField(&sb, "national_zhvi", fmtDollarF(p.NationalZHVI), "Zillow ZHVI (national)", "high", p.NationalZHVI > 0)
	writeXMLField(&sb, "national_zori", fmtDollarFMo(p.NationalZORI), "Zillow ZORI (national)", "high", p.NationalZORI > 0)
	writeXMLDerived(&sb, "national_gross_yield", fmtPct(p.NationalGrossYield), "(national_ZORI*12)/national_ZHVI", "medium", p.NationalGrossYield > 0)
	writeXMLField(&sb, "national_unemployment", fmtPct(p.NationalUnemployment), "FRED", "high", p.NationalUnemployment > 0)
	writeXMLField(&sb, "national_mortgage_30y", fmtPct(p.NationalMortgage30), "FRED", "high", p.NationalMortgage30 > 0)
	writeXMLField(&sb, "national_inventory", fmtInt(p.NationalInventory), "Redfin (national)", "high", p.NationalInventory > 0)
	writeXMLField(&sb, "national_months_of_supply", fmt.Sprintf("%.1f", p.NationalMonthsOfSupply), "Redfin (national)", "high", p.NationalMonthsOfSupply > 0)
	writeXMLField(&sb, "national_median_dom", fmtInt(p.NationalMedianDom), "Redfin (national)", "high", p.NationalMedianDom > 0)
	writeXMLField(&sb, "national_homes_sold", fmtInt(p.NationalHomesSold), "Redfin (national)", "high", p.NationalHomesSold > 0)
	writeXMLField(&sb, "national_avg_sale_to_list", fmt.Sprintf("%.1f%%", p.NationalAvgSaleToList*100), "Redfin (national)", "high", p.NationalAvgSaleToList > 0)
	writeXMLField(&sb, "national_sold_above_list", fmt.Sprintf("%.1f%%", p.NationalSoldAboveList*100), "Redfin (national)", "high", p.NationalSoldAboveList > 0)
	writeXMLField(&sb, "national_price_drops", fmt.Sprintf("%.1f%%", p.NationalPriceDrops*100), "Redfin (national)", "high", p.NationalPriceDrops > 0)
	writeXMLField(&sb, "national_inflation_rate", fmtPct(p.InflationRate), "FRED CPIAUCSL", "high", p.InflationRate > 0)
	writeXMLField(&sb, "national_rental_vacancy", fmtPct(p.RentalVacancyRate), "FRED RRVRUSQ156N", "high", p.RentalVacancyRate > 0)
	writeXMLField(&sb, "national_building_permits", fmt.Sprintf("%.0fK units/yr", p.BuildingPermits), "FRED PERMIT", "high", p.BuildingPermits > 0)
	writeXMLField(&sb, "national_housing_starts", fmt.Sprintf("%.0fK units/yr", p.HousingStarts), "FRED HOUST", "high", p.HousingStarts > 0)
	sb.WriteString("  </NATIONAL_BENCHMARKS>\n")

	// Zip submarket analysis (ADR-076)
	if p.ZipCount > 0 {
		sb.WriteString("\n  <ZIP_SUBMARKET_ANALYSIS>\n")
		sb.WriteString(fmt.Sprintf("    <zip_count>%d</zip_count>\n", p.ZipCount))
		writeXMLField(&sb, "zhvi_min", fmtDollarF(p.ZipMinZHVI), "Zillow zip-level", "high", p.ZipMinZHVI > 0)
		writeXMLField(&sb, "zhvi_max", fmtDollarF(p.ZipMaxZHVI), "Zillow zip-level", "high", p.ZipMaxZHVI > 0)
		writeXMLField(&sb, "zhvi_median", fmtDollarF(p.ZipMedianZHVI), "Zillow zip-level", "high", p.ZipMedianZHVI > 0)
		if p.ZipMaxZHVI > 0 && p.ZipMinZHVI > 0 {
			spread := ((p.ZipMaxZHVI - p.ZipMinZHVI) / p.ZipMedianZHVI) * 100
			sb.WriteString(fmt.Sprintf("    <price_spread>%.0f%% (range/median)</price_spread>\n", spread))
		}
		if p.ZipMinZORI > 0 {
			writeXMLField(&sb, "zori_min", fmtDollarFMo(p.ZipMinZORI), "Zillow zip-level", "high", true)
			writeXMLField(&sb, "zori_max", fmtDollarFMo(p.ZipMaxZORI), "Zillow zip-level", "high", true)
		}
		sb.WriteString("  </ZIP_SUBMARKET_ANALYSIS>\n")
	}

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
		{"rent_cagr_3y", p.RentCagr3Y != 0},
		{"rent_cagr_5y", p.RentCagr5Y != 0},
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
		// Tier 1: Economics fields
		{"per_capita_income", p.PerCapitaIncome > 0},
		{"household_count", p.HouseholdCount > 0},
		{"construction_employment", p.ConstructionEmployment > 0},
		{"job_openings", p.JobOpenings > 0},
		{"avg_hourly_earnings", p.AverageHourlyEarnings > 0},
		{"inflation_rate", p.InflationRate > 0},
		// Tier 3: Derived
		{"price_to_rent_ratio", p.PriceToRentRatio > 0},
		// Tier 4: Supply pipeline
		{"building_permits", p.BuildingPermits > 0},
		// Redfin competitive indicators
		{"median_sale_price", p.MedianSalePrice > 0},
		{"avg_sale_to_list", p.AvgSaleToList > 0},
		{"sold_above_list", p.SoldAboveList > 0},
		{"price_drops", p.PriceDrops > 0},
		{"new_listings", p.NewListings > 0},
		// National Redfin benchmarks
		{"national_inventory", p.NationalInventory > 0},
		{"national_months_of_supply", p.NationalMonthsOfSupply > 0},
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
