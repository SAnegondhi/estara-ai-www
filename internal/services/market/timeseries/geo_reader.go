// Package timeseries provides time-series data readers for market data.
// geo_reader.go adds readers for city, state, zip, and county time-series tables (ADR-076).
package timeseries

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// CityTimeSeries holds city-level ZHVI/ZORI time-series from city_time_series table
type CityTimeSeries struct {
	CityRegionID  int               `json:"cityRegionId"`
	CityName      string            `json:"cityName"`
	StateName     string            `json:"stateName"`
	MetroRegionID int               `json:"metroRegionId"`
	ZHVI          map[string]int64  `json:"zhvi"`  // date → value
	ZORI          map[string]int64  `json:"zori"`  // date → value
	LatestZHVI    float64           `json:"latestZhvi"`
	LatestZORI    float64           `json:"latestZori"`
	LatestDate    time.Time         `json:"latestDate"`
	UpdatedAt     time.Time         `json:"updatedAt"`
}

// StateTimeSeries holds state-level ZHVI/Redfin time-series from state_time_series table
type StateTimeSeries struct {
	StateRegionID int              `json:"stateRegionId"`
	StateCode     string           `json:"stateCode"`
	StateName     string           `json:"stateName"`
	ZHVI          map[string]int64 `json:"zhvi"`
	ZORI          map[string]int64 `json:"zori"`
	LatestZHVI    float64          `json:"latestZhvi"`
	UpdatedAt     time.Time        `json:"updatedAt"`
}

// ZipTimeSeries holds zip-level ZHVI/ZORI time-series from zip_time_series table
type ZipTimeSeries struct {
	ZipCode       string           `json:"zipCode"`
	City          string           `json:"city"`
	State         string           `json:"state"`
	County        string           `json:"county"`
	MetroRegionID int              `json:"metroRegionId"`
	ZHVI          map[string]int64 `json:"zhvi"`
	ZORI          map[string]int64 `json:"zori"`
	LatestZHVI    float64          `json:"latestZhvi"`
	LatestZORI    float64          `json:"latestZori"`
	UpdatedAt     time.Time        `json:"updatedAt"`
}

// ZipSubmarketSummary summarizes zip-level ZHVI spread for a city
type ZipSubmarketSummary struct {
	City     string  `json:"city"`
	State    string  `json:"state"`
	ZipCount int     `json:"zipCount"`
	MinZHVI  float64 `json:"minZhvi"`
	MaxZHVI  float64 `json:"maxZhvi"`
	MedianZHVI float64 `json:"medianZhvi"`
	MinZORI  float64 `json:"minZori"`
	MaxZORI  float64 `json:"maxZori"`
}

// CountyTimeSeries holds county-level Redfin time-series from county_time_series table
type CountyTimeSeries struct {
	CountyRegionID int              `json:"countyRegionId"`
	CountyFips     string           `json:"countyFips"`
	CountyName     string           `json:"countyName"`
	StateCode      string           `json:"stateCode"`
	MetroRegionID  int              `json:"metroRegionId"`
	Redfin         map[string]any   `json:"redfin"`
	UpdatedAt      time.Time        `json:"updatedAt"`
}

// GetCityLevelTimeSeries returns city-level ZHVI/ZORI from city_time_series table (ADR-075/076).
// Returns full JSONB maps with latest value extraction. For date-filtered []MetroData suitable
// for CAGR computation, use GetCityTimeSeries in metro.go instead (same underlying table).
func (r *MetroReader) GetCityLevelTimeSeries(ctx context.Context, city, state string) (*CityTimeSeries, error) {
	if city == "" || state == "" {
		return nil, ErrInvalidInput
	}

	query := `
		SELECT city_region_id, city_name, COALESCE(state_name, ''),
		       COALESCE(metro_region_id, 0),
		       COALESCE(zhvi_data, '{}'::jsonb), COALESCE(zori_data, '{}'::jsonb),
		       updated_at
		FROM city_time_series
		WHERE city_name ILIKE $1 AND state_name = $2
		LIMIT 1
	`

	var (
		regionID     int
		cityName     string
		stateName    string
		metroID      int
		zhviJSON     []byte
		zoriJSON     []byte
		updatedAt    time.Time
	)

	err := r.db.QueryRow(ctx, query, city, state).Scan(
		&regionID, &cityName, &stateName, &metroID,
		&zhviJSON, &zoriJSON, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	result := &CityTimeSeries{
		CityRegionID:  regionID,
		CityName:      cityName,
		StateName:     stateName,
		MetroRegionID: metroID,
		UpdatedAt:     updatedAt,
	}

	// Parse ZHVI JSONB
	if err := json.Unmarshal(zhviJSON, &result.ZHVI); err != nil {
		r.logger.Warn("failed to parse city ZHVI data", "city", city, "error", err)
		result.ZHVI = make(map[string]int64)
	}

	// Parse ZORI JSONB
	if err := json.Unmarshal(zoriJSON, &result.ZORI); err != nil {
		r.logger.Warn("failed to parse city ZORI data", "city", city, "error", err)
		result.ZORI = make(map[string]int64)
	}

	// Extract latest values
	result.LatestZHVI, result.LatestDate = latestFromTimeSeries(result.ZHVI)
	result.LatestZORI, _ = latestFromTimeSeries(result.ZORI)

	return result, nil
}

// GetStateTimeSeries returns state-level ZHVI time-series data
func (r *MetroReader) GetStateTimeSeries(ctx context.Context, stateCode string) (*StateTimeSeries, error) {
	if stateCode == "" {
		return nil, ErrInvalidInput
	}

	query := `
		SELECT state_region_id, state_code, state_name,
		       COALESCE(zhvi_data, '{}'::jsonb), COALESCE(zori_data, '{}'::jsonb),
		       updated_at
		FROM state_time_series
		WHERE state_code = $1
		LIMIT 1
	`

	var (
		regionID  int
		code      string
		name      string
		zhviJSON  []byte
		zoriJSON  []byte
		updatedAt time.Time
	)

	err := r.db.QueryRow(ctx, query, stateCode).Scan(
		&regionID, &code, &name, &zhviJSON, &zoriJSON, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	result := &StateTimeSeries{
		StateRegionID: regionID,
		StateCode:     code,
		StateName:     name,
		UpdatedAt:     updatedAt,
	}

	if err := json.Unmarshal(zhviJSON, &result.ZHVI); err != nil {
		r.logger.Warn("failed to parse state ZHVI data", "state", stateCode, "error", err)
		result.ZHVI = make(map[string]int64)
	}
	if err := json.Unmarshal(zoriJSON, &result.ZORI); err != nil {
		result.ZORI = make(map[string]int64)
	}

	result.LatestZHVI, _ = latestFromTimeSeries(result.ZHVI)

	return result, nil
}

// GetZipTimeSeries returns zip-level ZHVI/ZORI time-series data
func (r *MetroReader) GetZipTimeSeries(ctx context.Context, zipCode string) (*ZipTimeSeries, error) {
	if zipCode == "" {
		return nil, ErrInvalidInput
	}

	query := `
		SELECT zip_code, COALESCE(city, ''), COALESCE(state, ''),
		       COALESCE(county, ''), COALESCE(metro_region_id, 0),
		       COALESCE(zhvi_data, '{}'::jsonb), COALESCE(zori_data, '{}'::jsonb),
		       updated_at
		FROM zip_time_series
		WHERE zip_code = $1
		LIMIT 1
	`

	var (
		zip       string
		city      string
		state     string
		county    string
		metroID   int
		zhviJSON  []byte
		zoriJSON  []byte
		updatedAt time.Time
	)

	err := r.db.QueryRow(ctx, query, zipCode).Scan(
		&zip, &city, &state, &county, &metroID,
		&zhviJSON, &zoriJSON, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	result := &ZipTimeSeries{
		ZipCode:       zip,
		City:          city,
		State:         state,
		County:        county,
		MetroRegionID: metroID,
		UpdatedAt:     updatedAt,
	}

	if err := json.Unmarshal(zhviJSON, &result.ZHVI); err != nil {
		result.ZHVI = make(map[string]int64)
	}
	if err := json.Unmarshal(zoriJSON, &result.ZORI); err != nil {
		result.ZORI = make(map[string]int64)
	}

	result.LatestZHVI, _ = latestFromTimeSeries(result.ZHVI)
	result.LatestZORI, _ = latestFromTimeSeries(result.ZORI)

	return result, nil
}

// GetZipSubmarketSummary returns a summary of zip-level ZHVI/ZORI spread for a city
// Used by DataPayloadBuilder to include submarket analysis in DATA_PAYLOAD (ADR-076)
func (r *MetroReader) GetZipSubmarketSummary(ctx context.Context, city, state string) (*ZipSubmarketSummary, error) {
	if city == "" || state == "" {
		return nil, ErrInvalidInput
	}

	query := `
		SELECT zip_code, COALESCE(zhvi_data, '{}'::jsonb), COALESCE(zori_data, '{}'::jsonb)
		FROM zip_time_series
		WHERE city ILIKE $1 AND state = $2
		ORDER BY zip_code
	`

	rows, err := r.db.Query(ctx, query, city, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var zhviValues []float64
	var zoriValues []float64

	for rows.Next() {
		var zipCode string
		var zhviJSON, zoriJSON []byte

		if err := rows.Scan(&zipCode, &zhviJSON, &zoriJSON); err != nil {
			continue
		}

		var zhvi map[string]int64
		if err := json.Unmarshal(zhviJSON, &zhvi); err == nil {
			if v, _ := latestFromTimeSeries(zhvi); v > 0 {
				zhviValues = append(zhviValues, v)
			}
		}

		var zori map[string]int64
		if err := json.Unmarshal(zoriJSON, &zori); err == nil {
			if v, _ := latestFromTimeSeries(zori); v > 0 {
				zoriValues = append(zoriValues, v)
			}
		}
	}

	if len(zhviValues) == 0 {
		return nil, ErrNotFound
	}

	sort.Float64s(zhviValues)
	sort.Float64s(zoriValues)

	summary := &ZipSubmarketSummary{
		City:     city,
		State:    state,
		ZipCount: len(zhviValues),
		MinZHVI:  zhviValues[0],
		MaxZHVI:  zhviValues[len(zhviValues)-1],
		MedianZHVI: zhviValues[len(zhviValues)/2],
	}

	if len(zoriValues) > 0 {
		summary.MinZORI = zoriValues[0]
		summary.MaxZORI = zoriValues[len(zoriValues)-1]
	}

	r.logger.Debug("zip submarket summary",
		"city", city, "state", state,
		"zips", summary.ZipCount,
		"zhvi_range", [2]float64{summary.MinZHVI, summary.MaxZHVI},
	)

	return summary, nil
}

// GetCountyTimeSeries returns county-level Redfin time-series data
func (r *MetroReader) GetCountyTimeSeries(ctx context.Context, countyFips string) (*CountyTimeSeries, error) {
	if countyFips == "" {
		return nil, ErrInvalidInput
	}

	query := `
		SELECT county_region_id, COALESCE(county_fips, ''), county_name,
		       state_code, COALESCE(metro_region_id, 0),
		       COALESCE(redfin_data, '{}'::jsonb), updated_at
		FROM county_time_series
		WHERE county_fips = $1
		LIMIT 1
	`

	var (
		regionID   int
		fips       string
		name       string
		stateCode  string
		metroID    int
		redfinJSON []byte
		updatedAt  time.Time
	)

	err := r.db.QueryRow(ctx, query, countyFips).Scan(
		&regionID, &fips, &name, &stateCode, &metroID,
		&redfinJSON, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	result := &CountyTimeSeries{
		CountyRegionID: regionID,
		CountyFips:     fips,
		CountyName:     name,
		StateCode:      stateCode,
		MetroRegionID:  metroID,
		UpdatedAt:      updatedAt,
	}

	if err := json.Unmarshal(redfinJSON, &result.Redfin); err != nil {
		result.Redfin = make(map[string]any)
	}

	return result, nil
}

// latestFromTimeSeries extracts the latest value and date from a JSONB time-series map
func latestFromTimeSeries(data map[string]int64) (float64, time.Time) {
	if len(data) == 0 {
		return 0, time.Time{}
	}

	var latestKey string
	for k := range data {
		if k > latestKey {
			latestKey = k
		}
	}

	if latestKey == "" {
		return 0, time.Time{}
	}

	date, _ := time.Parse("2006-01-02", latestKey)
	return float64(data[latestKey]), date
}
