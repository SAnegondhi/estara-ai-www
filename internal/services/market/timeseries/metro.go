package timeseries

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/estara-ai/www/internal/db/postgres"
)

var (
	ErrNotFound     = errors.New("data not found")
	ErrInvalidInput = errors.New("invalid input")
)

// MetroData represents Zillow market data for a metro area
type MetroData struct {
	RegionID     int       `json:"regionId"`
	RegionName   string    `json:"regionName"`
	RegionType   string    `json:"regionType"`
	StateCode    string    `json:"stateCode"`
	Metro        string    `json:"metro"`
	Date         time.Time `json:"date"`
	ZHVI         float64   `json:"zhvi"`         // Zillow Home Value Index
	ZORI         float64   `json:"zori"`         // Zillow Observed Rent Index
	ZHVIForecast float64   `json:"zhviForecast"`
	ZORIForecast float64   `json:"zoriForecast"`
}

// YearOverYearData represents year-over-year changes
type YearOverYearData struct {
	CurrentZHVI float64   `json:"currentZhvi"`
	CurrentZORI float64   `json:"currentZori"`
	LatestDate  time.Time `json:"latestDate"`
	ZHVIYoYPct  float64   `json:"zhviYoyPct"`
	ZORIYoYPct  float64   `json:"zoriYoyPct"`
}

// CityMetroMapping represents a city to metro mapping
type CityMetroMapping struct {
	City          string `json:"city"`
	StateCode     string `json:"stateCode"`
	MetroName     string `json:"metroName"`
	MetroRegionID int    `json:"metroRegionId"`
}

// MetroTimeSeries represents the raw database row with JSONB columns
// Matches www_v1's Prisma schema: metro_time_series table
type MetroTimeSeries struct {
	ID               int              `json:"id"`
	MetroRegionID    int              `json:"metroRegionId"`
	MetroName        string           `json:"metroName"`
	ZHVIData         map[string]int64 `json:"zhviData"`         // JSONB: {"2024-01": 341000, ...}
	ZORIData         map[string]int64 `json:"zoriData"`         // JSONB: {"2024-01": 2100, ...}
	ZHVIForecastData map[string]int64 `json:"zhviForecastData"` // JSONB
	ZORIForecastData map[string]int64 `json:"zoriForecastData"` // JSONB
	CreatedAt        time.Time        `json:"createdAt"`
	UpdatedAt        time.Time        `json:"updatedAt"`
}

// RedfinMetrics represents one month's Redfin data for a metro area.
// Stored in metro_time_series.redfin_data as {"YYYY-MM": {fields...}}.
type RedfinMetrics struct {
	HomesSold          int     `json:"homesSold"`
	HomesSoldYoy       float64 `json:"homesSoldYoy"`
	Inventory          int     `json:"inventory"`
	MedianDom          int     `json:"medianDom"`
	MedianListPrice    float64 `json:"medianListPrice"`
	MedianListPriceYoy float64 `json:"medianListPriceYoy"`
	MedianSalePrice    float64 `json:"medianSalePrice"`
	MedianSalePriceYoy float64 `json:"medianSalePriceYoy"`
	MedianPpsf         float64 `json:"medianPpsf"`
	MonthsOfSupply     float64 `json:"monthsOfSupply"`
	NewListings        int     `json:"newListings"`
	AvgSaleToList      float64 `json:"avgSaleToList"`
	SoldAboveList      float64 `json:"soldAboveList"`
	PriceDrops         float64 `json:"priceDrops"`
	OffMarketIn2Weeks  float64 `json:"offMarketIn2Weeks"`
}

// MetroReader reads Zillow time series data from PostgreSQL
// Handles www_v1's JSONB-based schema where data is stored as {"YYYY-MM": value}
type MetroReader struct {
	db     *postgres.Pool
	logger *slog.Logger
}

// NewMetroReader creates a new metro time series reader
func NewMetroReader(db *postgres.Pool) *MetroReader {
	return &MetroReader{
		db:     db,
		logger: slog.Default().With("component", "metro_reader"),
	}
}

// GetLatestData returns the most recent data for a metro by name
func (r *MetroReader) GetLatestData(ctx context.Context, metroName, regionType string) (*MetroData, error) {
	if metroName == "" {
		return nil, ErrInvalidInput
	}

	// Query the metro_time_series table with JSONB columns
	// Note: zhvf_data is ZHVI forecast (not zhvi_forecast_data)
	// No ZORI forecast column exists in this table
	query := `
		SELECT
			metro_region_id,
			metro_name,
			COALESCE(zhvi_data, '{}'::jsonb),
			COALESCE(zori_data, '{}'::jsonb),
			COALESCE(zhvf_data, '{}'::jsonb)
		FROM metro_time_series
		WHERE metro_name ILIKE $1
		LIMIT 1
	`

	var (
		regionID         int
		name             string
		zhviDataJSON     []byte
		zoriDataJSON     []byte
		zhvfDataJSON     []byte
	)

	err := r.db.QueryRow(ctx, query, metroName).Scan(
		&regionID,
		&name,
		&zhviDataJSON,
		&zoriDataJSON,
		&zhvfDataJSON,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Parse JSONB data and extract latest values
	data := &MetroData{
		RegionID:   regionID,
		RegionName: name,
		RegionType: regionType,
		Metro:      name,
	}

	data.ZHVI, data.Date = extractLatestValue(zhviDataJSON)
	data.ZORI, _ = extractLatestValue(zoriDataJSON)
	data.ZHVIForecast, _ = extractLatestValue(zhvfDataJSON)
	// No ZORI forecast available in the database

	r.logger.Debug("fetched metro data",
		"region", name,
		"type", regionType,
		"zhvi", data.ZHVI,
		"date", data.Date,
	)

	return data, nil
}

// GetLatestDataByID returns the most recent data for a metro by region ID
func (r *MetroReader) GetLatestDataByID(ctx context.Context, regionID int) (*MetroData, error) {
	// Note: zhvf_data is ZHVI forecast (not zhvi_forecast_data)
	// No ZORI forecast column exists in this table
	query := `
		SELECT
			metro_region_id,
			metro_name,
			COALESCE(zhvi_data, '{}'::jsonb),
			COALESCE(zori_data, '{}'::jsonb),
			COALESCE(zhvf_data, '{}'::jsonb)
		FROM metro_time_series
		WHERE metro_region_id = $1
		LIMIT 1
	`

	var (
		id           int
		name         string
		zhviDataJSON []byte
		zoriDataJSON []byte
		zhvfDataJSON []byte
	)

	err := r.db.QueryRow(ctx, query, regionID).Scan(
		&id,
		&name,
		&zhviDataJSON,
		&zoriDataJSON,
		&zhvfDataJSON,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	data := &MetroData{
		RegionID:   id,
		RegionName: name,
		RegionType: "msa",
		Metro:      name,
	}

	data.ZHVI, data.Date = extractLatestValue(zhviDataJSON)
	data.ZORI, _ = extractLatestValue(zoriDataJSON)
	data.ZHVIForecast, _ = extractLatestValue(zhvfDataJSON)
	// No ZORI forecast available in the database

	return data, nil
}

// GetTimeSeries returns historical data for a metro region
func (r *MetroReader) GetTimeSeries(ctx context.Context, metroName, regionType string, startDate, endDate time.Time) ([]MetroData, error) {
	query := `
		SELECT
			metro_region_id,
			metro_name,
			COALESCE(zhvi_data, '{}'::jsonb),
			COALESCE(zori_data, '{}'::jsonb)
		FROM metro_time_series
		WHERE metro_name ILIKE $1
		LIMIT 1
	`

	var (
		regionID     int
		name         string
		zhviDataJSON []byte
		zoriDataJSON []byte
	)

	err := r.db.QueryRow(ctx, query, metroName).Scan(
		&regionID,
		&name,
		&zhviDataJSON,
		&zoriDataJSON,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Parse JSONB and filter by date range
	zhviMap := parseJSONBData(zhviDataJSON)
	zoriMap := parseJSONBData(zoriDataJSON)

	// Get all unique dates
	dateSet := make(map[string]bool)
	for date := range zhviMap {
		dateSet[date] = true
	}
	for date := range zoriMap {
		dateSet[date] = true
	}

	var dates []string
	for date := range dateSet {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	var series []MetroData
	for _, dateStr := range dates {
		date, err := time.Parse("2006-01", dateStr)
		if err != nil {
			continue
		}

		// Filter by date range
		if date.Before(startDate) || date.After(endDate) {
			continue
		}

		data := MetroData{
			RegionID:   regionID,
			RegionName: name,
			RegionType: regionType,
			Metro:      name,
			Date:       date,
			ZHVI:       zhviMap[dateStr],
			ZORI:       zoriMap[dateStr],
		}
		series = append(series, data)
	}

	return series, nil
}

// GetCityMetroMapping returns the metro mapping for a city
// Uses the city_market_cache table joined with metro_time_series for metro name
func (r *MetroReader) GetCityMetroMapping(ctx context.Context, city, stateCode string) (*CityMetroMapping, error) {
	// Join city_market_cache with metro_time_series to get metro name
	query := `
		SELECT
			c.city,
			c.state,
			COALESCE(m.metro_name, ''),
			COALESCE(c.metro_region_id, 0)
		FROM city_market_cache c
		LEFT JOIN metro_time_series m ON c.metro_region_id = m.metro_region_id
		WHERE c.city ILIKE $1 AND c.state = $2
		LIMIT 1
	`

	var mapping CityMetroMapping
	err := r.db.QueryRow(ctx, query, city, stateCode).Scan(
		&mapping.City,
		&mapping.StateCode,
		&mapping.MetroName,
		&mapping.MetroRegionID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	r.logger.Debug("found city mapping",
		"city", city,
		"state", stateCode,
		"metro", mapping.MetroName,
	)

	return &mapping, nil
}

// GetMarketData returns market data for a city, falling back to metro if needed
func (r *MetroReader) GetMarketData(ctx context.Context, city, stateCode string) (*MetroData, error) {
	// First try city_market_cache for cached city data
	// Join with metro_time_series to get metro name
	cacheQuery := `
		SELECT
			COALESCE(c.metro_region_id, 0),
			c.city,
			c.state,
			COALESCE(m.metro_name, ''),
			COALESCE(c.median_home_price, 0),
			COALESCE(c.median_rent, 0),
			COALESCE(c.price_yoy_change, 0),
			c.last_updated
		FROM city_market_cache c
		LEFT JOIN metro_time_series m ON c.metro_region_id = m.metro_region_id
		WHERE c.city ILIKE $1 AND c.state = $2
		LIMIT 1
	`

	var (
		metroRegionID   int
		cityName        string
		state           string
		metroName       string
		medianHomePrice float64
		medianRent      float64
		priceYoyChange  float64
		lastUpdated     time.Time
	)

	err := r.db.QueryRow(ctx, cacheQuery, city, stateCode).Scan(
		&metroRegionID,
		&cityName,
		&state,
		&metroName,
		&medianHomePrice,
		&medianRent,
		&priceYoyChange,
		&lastUpdated,
	)
	if err == nil && medianHomePrice > 0 {
		// Return cached city data
		return &MetroData{
			RegionID:   metroRegionID,
			RegionName: cityName,
			RegionType: "city",
			StateCode:  state,
			Metro:      metroName,
			Date:       lastUpdated,
			ZHVI:       medianHomePrice,
			ZORI:       medianRent,
		}, nil
	}

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		r.logger.Warn("city cache query error", "error", err)
	}

	// Fall back to metro-level data
	mapping, err := r.GetCityMetroMapping(ctx, city, stateCode)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			r.logger.Warn("no city or metro mapping found", "city", city, "state", stateCode)
			return nil, ErrNotFound
		}
		return nil, err
	}

	r.logger.Info("falling back to metro data",
		"city", city,
		"metro", mapping.MetroName,
	)

	// Use metro region ID if available
	if mapping.MetroRegionID > 0 {
		return r.GetLatestDataByID(ctx, mapping.MetroRegionID)
	}

	// Otherwise search by metro name
	return r.GetLatestData(ctx, mapping.MetroName, "msa")
}

// GetYearOverYearChange returns year-over-year changes for a metro region
func (r *MetroReader) GetYearOverYearChange(ctx context.Context, regionID int) (*YearOverYearData, error) {
	query := `
		SELECT
			COALESCE(zhvi_data, '{}'::jsonb),
			COALESCE(zori_data, '{}'::jsonb)
		FROM metro_time_series
		WHERE metro_region_id = $1
		LIMIT 1
	`

	var (
		zhviDataJSON []byte
		zoriDataJSON []byte
	)

	err := r.db.QueryRow(ctx, query, regionID).Scan(
		&zhviDataJSON,
		&zoriDataJSON,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	zhviMap := parseJSONBData(zhviDataJSON)
	zoriMap := parseJSONBData(zoriDataJSON)

	// Get latest and year-ago values
	currentZHVI, latestDate := extractLatestValue(zhviDataJSON)
	currentZORI, _ := extractLatestValue(zoriDataJSON)

	// Calculate year-ago date key
	yearAgoDate := latestDate.AddDate(-1, 0, 0)
	yearAgoKey := yearAgoDate.Format("2006-01")

	var zhviYoY, zoriYoY float64

	if yearAgoZHVI, ok := zhviMap[yearAgoKey]; ok && yearAgoZHVI > 0 {
		zhviYoY = (currentZHVI - yearAgoZHVI) / yearAgoZHVI * 100
	}

	if yearAgoZORI, ok := zoriMap[yearAgoKey]; ok && yearAgoZORI > 0 {
		zoriYoY = (currentZORI - yearAgoZORI) / yearAgoZORI * 100
	}

	return &YearOverYearData{
		CurrentZHVI: currentZHVI,
		CurrentZORI: currentZORI,
		LatestDate:  latestDate,
		ZHVIYoYPct:  zhviYoY,
		ZORIYoYPct:  zoriYoY,
	}, nil
}

// SearchRegions searches for metro regions by name
func (r *MetroReader) SearchRegions(ctx context.Context, query string, limit int) ([]MetroData, error) {
	if limit <= 0 {
		limit = 10
	}

	sqlQuery := `
		SELECT DISTINCT metro_region_id, metro_name
		FROM metro_time_series
		WHERE metro_name ILIKE $1
		ORDER BY metro_name
		LIMIT $2
	`

	rows, err := r.db.Query(ctx, sqlQuery, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var regions []MetroData
	for rows.Next() {
		var data MetroData
		if err := rows.Scan(&data.RegionID, &data.RegionName); err != nil {
			return nil, err
		}
		data.RegionType = "msa"
		data.Metro = data.RegionName
		regions = append(regions, data)
	}

	return regions, rows.Err()
}

// GetCityTimeSeries returns historical ZHVI/ZORI data from city_time_series table.
// Returns city-level time series for more accurate CAGRs than metro-level.
// Falls back to ErrNotFound if city not in city_time_series.
func (r *MetroReader) GetCityTimeSeries(ctx context.Context, city, stateCode string, startDate, endDate time.Time) ([]MetroData, error) {
	query := `
		SELECT
			city_region_id,
			city_name,
			COALESCE(state_name, ''),
			COALESCE(zhvi_data, '{}'::jsonb),
			COALESCE(zori_data, '{}'::jsonb)
		FROM city_time_series
		WHERE city_name ILIKE $1 AND state_name ILIKE $2
		LIMIT 1
	`

	var (
		regionID     int
		name         string
		stateName    string
		zhviDataJSON []byte
		zoriDataJSON []byte
	)

	err := r.db.QueryRow(ctx, query, city, stateCode).Scan(
		&regionID,
		&name,
		&stateName,
		&zhviDataJSON,
		&zoriDataJSON,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Parse JSONB and filter by date range
	zhviMap := parseJSONBData(zhviDataJSON)
	zoriMap := parseJSONBData(zoriDataJSON)

	dateSet := make(map[string]bool)
	for date := range zhviMap {
		dateSet[date] = true
	}
	for date := range zoriMap {
		dateSet[date] = true
	}

	var dates []string
	for date := range dateSet {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	var series []MetroData
	for _, dateStr := range dates {
		date, parseErr := time.Parse("2006-01", dateStr)
		if parseErr != nil {
			continue
		}
		if date.Before(startDate) || date.After(endDate) {
			continue
		}
		series = append(series, MetroData{
			RegionID:   regionID,
			RegionName: name,
			RegionType: "city",
			StateCode:  stateName,
			Date:       date,
			ZHVI:       zhviMap[dateStr],
			ZORI:       zoriMap[dateStr],
		})
	}

	r.logger.Debug("fetched city time series",
		"city", city, "state", stateCode,
		"points", len(series),
	)

	return series, nil
}

// CitySnapshot holds all fields from city_market_cache (ADR-074)
type CitySnapshot struct {
	City                  string  `json:"city"`
	State                 string  `json:"state"`
	MetroRegionID         int     `json:"metroRegionId"`
	MetroName             string  `json:"metroName"`
	MedianHomePrice       float64 `json:"medianHomePrice"`
	MedianPricePerSqft    float64 `json:"medianPricePerSqft"`
	MedianListPrice       float64 `json:"medianListPrice"`
	HomesSold             int     `json:"homesSold"`
	InventoryCount        int     `json:"inventoryCount"`
	MonthsOfSupply        float64 `json:"monthsOfSupply"`
	MedianDaysOnMarket    int     `json:"medianDaysOnMarket"`
	PriceYoyChange        float64 `json:"priceYoyChange"`
	ForecastGrowth        float64 `json:"forecastGrowth"`
	MedianRent            float64 `json:"medianRent"`
	RentYoyChange         float64 `json:"rentYoyChange"`
	RentalYield           float64 `json:"rentalYield"`
	CapRate               float64 `json:"capRate"`
	PriceToRentRatio      float64 `json:"priceToRentRatio"`
	VacancyRate           float64 `json:"vacancyRate"`
	HudFMR0BR             float64 `json:"hudFmr0br"`
	HudFMR1BR             float64 `json:"hudFmr1br"`
	HudFMR2BR             float64 `json:"hudFmr2br"`
	HudFMR3BR             float64 `json:"hudFmr3br"`
	HudFMR4BR             float64 `json:"hudFmr4br"`
	Population            int     `json:"population"`
	PopulationGrowthRate  float64 `json:"populationGrowthRate"`
	MedianHouseholdIncome float64 `json:"medianHouseholdIncome"`
	UnemploymentRate      float64 `json:"unemploymentRate"`
	EmploymentGrowthRate  float64 `json:"employmentGrowthRate"`
	MarketHeatIndex       float64 `json:"marketHeatIndex"`
	MarketTemperature     string  `json:"marketTemperature"`
	AffordabilityIndex    float64 `json:"affordabilityIndex"`
	AffordabilityBurden   string  `json:"affordabilityBurden"`
	PriceToIncomeRatio    float64 `json:"priceToIncomeRatio"`
	DataQualityScore      int     `json:"dataQualityScore"`
	LastUpdated           time.Time `json:"lastUpdated"`

	// Redfin-sourced fields (from metro_time_series.redfin_data, metro-level)
	RedfinDate         time.Time `json:"redfinDate"`         // Date of Redfin data point
	NewListings        int       `json:"newListings"`
	MedianSalePrice    float64   `json:"medianSalePrice"`
	MedianSalePriceYoy  float64   `json:"medianSalePriceYoy"`
	RedfinListPrice     float64   `json:"redfinListPrice"`     // Redfin median list price (metro)
	RedfinListPriceYoy  float64   `json:"redfinListPriceYoy"`
	MedianPpsf         float64   `json:"medianPpsf"`
	AvgSaleToList      float64   `json:"avgSaleToList"`
	SoldAboveList      float64   `json:"soldAboveList"`
	PriceDrops         float64   `json:"priceDrops"`
	OffMarketIn2Weeks  float64   `json:"offMarketIn2Weeks"`
	HasRedfinData      bool      `json:"hasRedfinData"`
}

// NationalData holds national ZHVI/ZORI and Redfin benchmark data (ADR-074)
type NationalData struct {
	ZHVI         float64   `json:"zhvi"`
	ZORI         float64   `json:"zori"`
	ZHVIForecast float64   `json:"zhviForecast"`
	GrossYield   float64   `json:"grossYield"` // Derived: (ZORI*12)/ZHVI
	Date         time.Time `json:"date"`

	// Redfin national metrics
	RedfinDate        time.Time `json:"redfinDate"`
	Inventory         int       `json:"inventory"`
	MonthsOfSupply    float64   `json:"monthsOfSupply"`
	MedianDom         int       `json:"medianDom"`
	HomesSold         int       `json:"homesSold"`
	NewListings       int       `json:"newListings"`
	MedianSalePrice   float64   `json:"medianSalePrice"`
	AvgSaleToList     float64   `json:"avgSaleToList"`
	SoldAboveList     float64   `json:"soldAboveList"`
	PriceDrops        float64   `json:"priceDrops"`
	OffMarketIn2Weeks float64   `json:"offMarketIn2Weeks"`
	HasRedfinData     bool      `json:"hasRedfinData"`
}

// GetCitySnapshot returns all city_market_cache fields for a city, merged with
// Redfin metro data from metro_time_series.redfin_data (ADR-074).
// City-level data from city_market_cache is primary; Redfin metro data backfills
// any NULL/zero fields and provides additional competitive indicators.
func (r *MetroReader) GetCitySnapshot(ctx context.Context, city, stateCode string) (*CitySnapshot, error) {
	query := `
		SELECT
			c.city, c.state, COALESCE(c.metro_region_id, 0), COALESCE(m.metro_name, ''),
			COALESCE(c.median_home_price, 0), COALESCE(c.median_price_per_sqft, 0),
			COALESCE(c.median_list_price, 0), COALESCE(c.homes_sold, 0),
			COALESCE(c.inventory_count, 0), COALESCE(c.months_of_supply, 0),
			COALESCE(c.median_days_on_market, 0), COALESCE(c.price_yoy_change, 0),
			COALESCE(c.forecast_growth, 0), COALESCE(c.median_rent, 0),
			COALESCE(c.rent_yoy_change, 0), COALESCE(c.rental_yield, 0),
			COALESCE(c.cap_rate, 0), COALESCE(c.price_to_rent_ratio, 0),
			COALESCE(c.vacancy_rate, 0),
			COALESCE(c.hud_fmr_0br, 0), COALESCE(c.hud_fmr_1br, 0),
			COALESCE(c.hud_fmr_2br, 0), COALESCE(c.hud_fmr_3br, 0),
			COALESCE(c.hud_fmr_4br, 0),
			COALESCE(c.population, 0), COALESCE(c.population_growth_rate, 0),
			COALESCE(c.median_household_income, 0), COALESCE(c.unemployment_rate, 0),
			COALESCE(c.employment_growth_rate, 0), COALESCE(c.market_heat_index, 0),
			COALESCE(c.market_temperature, ''), COALESCE(c.affordability_index, 0),
			COALESCE(c.affordability_burden, ''), COALESCE(c.price_to_income_ratio, 0),
			c.data_quality_score, c.last_updated,
			COALESCE(m.redfin_data, '{}'::jsonb)
		FROM city_market_cache c
		LEFT JOIN metro_time_series m ON c.metro_region_id = m.metro_region_id
		WHERE c.city ILIKE $1 AND c.state = $2
		LIMIT 1
	`

	var s CitySnapshot
	var redfinJSON []byte
	err := r.db.QueryRow(ctx, query, city, stateCode).Scan(
		&s.City, &s.State, &s.MetroRegionID, &s.MetroName,
		&s.MedianHomePrice, &s.MedianPricePerSqft,
		&s.MedianListPrice, &s.HomesSold,
		&s.InventoryCount, &s.MonthsOfSupply,
		&s.MedianDaysOnMarket, &s.PriceYoyChange,
		&s.ForecastGrowth, &s.MedianRent,
		&s.RentYoyChange, &s.RentalYield,
		&s.CapRate, &s.PriceToRentRatio,
		&s.VacancyRate,
		&s.HudFMR0BR, &s.HudFMR1BR,
		&s.HudFMR2BR, &s.HudFMR3BR,
		&s.HudFMR4BR,
		&s.Population, &s.PopulationGrowthRate,
		&s.MedianHouseholdIncome, &s.UnemploymentRate,
		&s.EmploymentGrowthRate, &s.MarketHeatIndex,
		&s.MarketTemperature, &s.AffordabilityIndex,
		&s.AffordabilityBurden, &s.PriceToIncomeRatio,
		&s.DataQualityScore, &s.LastUpdated,
		&redfinJSON,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Merge Redfin metro data: backfill NULLs and add Redfin-only fields
	rf, rfDate := parseRedfinLatest(redfinJSON)
	if rf != nil {
		s.HasRedfinData = true
		s.RedfinDate = rfDate

		// Backfill city_market_cache NULLs with Redfin metro data
		if s.InventoryCount == 0 && rf.Inventory > 0 {
			s.InventoryCount = rf.Inventory
		}
		if s.MonthsOfSupply == 0 && rf.MonthsOfSupply > 0 {
			s.MonthsOfSupply = rf.MonthsOfSupply
		}
		if s.MedianDaysOnMarket == 0 && rf.MedianDom > 0 {
			s.MedianDaysOnMarket = rf.MedianDom
		}
		if s.HomesSold == 0 && rf.HomesSold > 0 {
			s.HomesSold = rf.HomesSold
		}

		// Redfin-only competitive indicators (always set when available)
		s.NewListings = rf.NewListings
		s.MedianSalePrice = rf.MedianSalePrice
		s.MedianSalePriceYoy = rf.MedianSalePriceYoy
		s.RedfinListPrice = rf.MedianListPrice
		s.RedfinListPriceYoy = rf.MedianListPriceYoy
		s.MedianPpsf = rf.MedianPpsf
		s.AvgSaleToList = rf.AvgSaleToList
		s.SoldAboveList = rf.SoldAboveList
		s.PriceDrops = rf.PriceDrops
		s.OffMarketIn2Weeks = rf.OffMarketIn2Weeks
	}

	r.logger.Debug("fetched city snapshot",
		"city", city, "state", stateCode,
		"quality", s.DataQualityScore,
		"hasRedfin", s.HasRedfinData,
	)

	return &s, nil
}

// GetNationalData returns national ZHVI/ZORI and Redfin data from metro_time_series.
// Queries both convention row (metro_region_id=0) and Zillow's national row (102001),
// preferring the most complete row for Zillow data. Redfin national data is at metro_region_id=0.
func (r *MetroReader) GetNationalData(ctx context.Context) (*NationalData, error) {
	// Zillow row: prefer the row with more complete data
	zillowQuery := `
		SELECT
			COALESCE(zhvi_data, '{}'::jsonb),
			COALESCE(zori_data, '{}'::jsonb),
			COALESCE(zhvf_data, '{}'::jsonb)
		FROM metro_time_series
		WHERE metro_region_id IN (0, 102001)
		ORDER BY
			(CASE WHEN zori_data IS NOT NULL THEN 1 ELSE 0 END) +
			(CASE WHEN zhvi_data IS NOT NULL THEN 1 ELSE 0 END) DESC
		LIMIT 1
	`

	var zhviJSON, zoriJSON, zhvfJSON []byte
	err := r.db.QueryRow(ctx, zillowQuery).Scan(&zhviJSON, &zoriJSON, &zhvfJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	data := &NationalData{}
	data.ZHVI, data.Date = extractLatestValue(zhviJSON)
	data.ZORI, _ = extractLatestValue(zoriJSON)
	data.ZHVIForecast, _ = extractLatestValue(zhvfJSON)

	if data.ZHVI > 0 && data.ZORI > 0 {
		data.GrossYield = (data.ZORI * 12 / data.ZHVI) * 100
	}

	// Redfin national data is at metro_region_id=0
	redfinQuery := `
		SELECT COALESCE(redfin_data, '{}'::jsonb)
		FROM metro_time_series
		WHERE metro_region_id = 0 AND redfin_data IS NOT NULL
		LIMIT 1
	`
	var redfinJSON []byte
	err = r.db.QueryRow(ctx, redfinQuery).Scan(&redfinJSON)
	if err == nil {
		rf, rfDate := parseRedfinLatest(redfinJSON)
		if rf != nil {
			data.HasRedfinData = true
			data.RedfinDate = rfDate
			data.Inventory = rf.Inventory
			data.MonthsOfSupply = rf.MonthsOfSupply
			data.MedianDom = rf.MedianDom
			data.HomesSold = rf.HomesSold
			data.NewListings = rf.NewListings
			data.MedianSalePrice = rf.MedianSalePrice
			data.AvgSaleToList = rf.AvgSaleToList
			data.SoldAboveList = rf.SoldAboveList
			data.PriceDrops = rf.PriceDrops
			data.OffMarketIn2Weeks = rf.OffMarketIn2Weeks
		}
	}

	r.logger.Debug("fetched national data",
		"zhvi", data.ZHVI,
		"zori", data.ZORI,
		"grossYield", data.GrossYield,
		"hasRedfin", data.HasRedfinData,
	)

	return data, nil
}

// Helper functions for parsing JSONB time-series data

// parseJSONBData parses JSONB bytes into a map of date -> value
func parseJSONBData(data []byte) map[string]float64 {
	result := make(map[string]float64)
	if len(data) == 0 {
		return result
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return result
	}

	return result
}

// extractLatestValue extracts the most recent value from JSONB time-series data
// JSONB format: {"2024-01": 341000, "2024-02": 343500, ...}
func extractLatestValue(data []byte) (float64, time.Time) {
	dataMap := parseJSONBData(data)
	if len(dataMap) == 0 {
		return 0, time.Time{}
	}

	// Find the latest date (keys are "YYYY-MM" format, sortable lexicographically)
	var latestKey string
	for key := range dataMap {
		if key > latestKey {
			latestKey = key
		}
	}

	if latestKey == "" {
		return 0, time.Time{}
	}

	// Parse the date
	date, err := time.Parse("2006-01", latestKey)
	if err != nil {
		return float64(dataMap[latestKey]), time.Now()
	}

	return float64(dataMap[latestKey]), date
}

// parseRedfinLatest extracts the most recent month's Redfin metrics from JSONB.
// redfin_data format: {"2024-01": {homesSold: 245, inventory: 890, ...}, "2024-02": {...}}
func parseRedfinLatest(data []byte) (*RedfinMetrics, time.Time) {
	if len(data) == 0 {
		return nil, time.Time{}
	}

	// First parse as map of date -> raw JSON
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, time.Time{}
	}
	if len(raw) == 0 {
		return nil, time.Time{}
	}

	// Find latest date key
	var latestKey string
	for key := range raw {
		if key > latestKey {
			latestKey = key
		}
	}

	var metrics RedfinMetrics
	if err := json.Unmarshal(raw[latestKey], &metrics); err != nil {
		return nil, time.Time{}
	}

	date, err := time.Parse("2006-01", latestKey)
	if err != nil {
		return &metrics, time.Now()
	}
	return &metrics, date
}
