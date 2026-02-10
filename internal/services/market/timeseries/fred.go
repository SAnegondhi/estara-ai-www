package timeseries

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	redisClient "github.com/estara-ai/www/internal/db/redis"
)

const (
	fredBaseURL = "https://api.stlouisfed.org/fred"

	// Series IDs
	SeriesMortgage30US     = "MORTGAGE30US"  // 30-Year Fixed Rate Mortgage Average
	SeriesMortgage15US     = "MORTGAGE15US"  // 15-Year Fixed Rate Mortgage Average
	SeriesUnemployment     = "UNRATE"        // Unemployment Rate
	SeriesCPI              = "CPIAUCSL"      // Consumer Price Index
	SeriesRentalVacancy    = "RRVRUSQ156N"   // Rental Vacancy Rate (national, quarterly)
	SeriesHomeownerVacancy = "RHVRUSQ156N"   // Homeowner Vacancy Rate (national, quarterly)
	SeriesBuildingPermits  = "PERMIT"        // New Private Housing Units Authorized (national, monthly, thousands)
	SeriesHousingStarts    = "HOUST"         // Housing Starts (national, monthly, thousands)

	// Cache settings - FRED data doesn't change frequently
	// 7-day TTL to avoid rate limits and IP blocks (matches www_v1 implementation)
	fredCacheTTL = 7 * 24 * time.Hour
)

// FREDObservation represents a single data point from FRED
type FREDObservation struct {
	Date  string `json:"date"`
	Value string `json:"value"`
}

// FREDResponse represents the API response from FRED
type FREDResponse struct {
	RealtimeStart   string            `json:"realtime_start"`
	RealtimeEnd     string            `json:"realtime_end"`
	ObservationStart string           `json:"observation_start"`
	ObservationEnd   string           `json:"observation_end"`
	Units           string            `json:"units"`
	OutputType      int               `json:"output_type"`
	FileType        string            `json:"file_type"`
	OrderBy         string            `json:"order_by"`
	SortOrder       string            `json:"sort_order"`
	Count           int               `json:"count"`
	Offset          int               `json:"offset"`
	Limit           int               `json:"limit"`
	Observations    []FREDObservation `json:"observations"`
}

// MortgageRateData represents mortgage rate information
type MortgageRateData struct {
	Rate30Year   float64   `json:"rate30Year"`
	Rate15Year   float64   `json:"rate15Year"`
	Date         time.Time `json:"date"`
	Source       string    `json:"source"`
}

// FREDClient fetches economic data from the Federal Reserve
type FREDClient struct {
	client  *http.Client
	redis   *redisClient.Client
	apiKey  string
	baseURL string
	logger  *slog.Logger
}

// NewFREDClient creates a new FRED API client
// Pass redis client for 7-day caching (nil disables caching)
func NewFREDClient(apiKey string, redis *redisClient.Client) *FREDClient {
	return &FREDClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		redis:   redis,
		apiKey:  apiKey,
		baseURL: fredBaseURL,
		logger:  slog.Default().With("component", "fred_client"),
	}
}

// GetMortgageRates returns the latest mortgage rates
func (c *FREDClient) GetMortgageRates(ctx context.Context) (*MortgageRateData, error) {
	// Get 30-year rate
	rate30, date30, err := c.getLatestValue(ctx, SeriesMortgage30US)
	if err != nil {
		return nil, fmt.Errorf("failed to get 30-year rate: %w", err)
	}

	// Get 15-year rate
	rate15, _, err := c.getLatestValue(ctx, SeriesMortgage15US)
	if err != nil {
		// Log but don't fail - 30-year is more important
		c.logger.Warn("failed to get 15-year rate", "error", err)
		rate15 = 0
	}

	c.logger.Debug("fetched mortgage rates",
		"rate30", rate30,
		"rate15", rate15,
		"date", date30,
	)

	return &MortgageRateData{
		Rate30Year: rate30,
		Rate15Year: rate15,
		Date:       date30,
		Source:     "FRED (Freddie Mac)",
	}, nil
}

// GetSeries returns historical data for a series
// Uses 7-day Redis cache to avoid rate limits (matches www_v1)
func (c *FREDClient) GetSeries(ctx context.Context, seriesID string, startDate, endDate time.Time) ([]FREDObservation, error) {
	cacheKey := fmt.Sprintf("fred:%s:%s:%s", seriesID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))

	// Check cache first
	if c.redis != nil {
		cached, err := c.redis.GetJSON(ctx, cacheKey)
		if err == nil && cached != nil {
			var obs []FREDObservation
			if err := json.Unmarshal(cached, &obs); err == nil {
				c.logger.Debug("FRED series cache hit", "series", seriesID)
				return obs, nil
			}
		}
	}

	c.logger.Debug("FRED series cache miss, fetching from API", "series", seriesID)

	params := url.Values{
		"series_id":         {seriesID},
		"api_key":           {c.apiKey},
		"file_type":         {"json"},
		"observation_start": {startDate.Format("2006-01-02")},
		"observation_end":   {endDate.Format("2006-01-02")},
		"sort_order":        {"asc"},
	}

	resp, err := c.makeRequest(ctx, "/series/observations", params)
	if err != nil {
		return nil, err
	}

	// Cache the result for 7 days
	if c.redis != nil && len(resp.Observations) > 0 {
		if data, err := json.Marshal(resp.Observations); err == nil {
			if err := c.redis.SetJSON(ctx, cacheKey, data, fredCacheTTL); err != nil {
				c.logger.Warn("failed to cache FRED series", "series", seriesID, "error", err)
			} else {
				c.logger.Debug("cached FRED series for 7 days", "series", seriesID)
			}
		}
	}

	return resp.Observations, nil
}

// getLatestValue returns the most recent value for a series
// Uses 7-day Redis cache to avoid rate limits (matches www_v1)
func (c *FREDClient) getLatestValue(ctx context.Context, seriesID string) (float64, time.Time, error) {
	cacheKey := fmt.Sprintf("fred:%s:1", seriesID)

	// Check cache first
	if c.redis != nil {
		cached, err := c.redis.GetJSON(ctx, cacheKey)
		if err == nil && cached != nil {
			var obs []FREDObservation
			if err := json.Unmarshal(cached, &obs); err == nil && len(obs) > 0 {
				c.logger.Debug("FRED cache hit", "series", seriesID)
				return c.parseObservation(obs[0], seriesID)
			}
		}
	}

	c.logger.Debug("FRED cache miss, fetching from API", "series", seriesID)

	params := url.Values{
		"series_id":  {seriesID},
		"api_key":    {c.apiKey},
		"file_type":  {"json"},
		"sort_order": {"desc"},
		"limit":      {"1"},
	}

	resp, err := c.makeRequest(ctx, "/series/observations", params)
	if err != nil {
		return 0, time.Time{}, err
	}

	if len(resp.Observations) == 0 {
		return 0, time.Time{}, fmt.Errorf("no observations found for series %s", seriesID)
	}

	// Cache the result for 7 days
	if c.redis != nil {
		if data, err := json.Marshal(resp.Observations); err == nil {
			if err := c.redis.SetJSON(ctx, cacheKey, data, fredCacheTTL); err != nil {
				c.logger.Warn("failed to cache FRED data", "series", seriesID, "error", err)
			} else {
				c.logger.Debug("cached FRED data for 7 days", "series", seriesID)
			}
		}
	}

	return c.parseObservation(resp.Observations[0], seriesID)
}

// parseObservation extracts value and date from a FRED observation
func (c *FREDClient) parseObservation(obs FREDObservation, seriesID string) (float64, time.Time, error) {
	// Parse value (FRED returns "." for missing values)
	if obs.Value == "." {
		return 0, time.Time{}, fmt.Errorf("missing value for series %s", seriesID)
	}

	value, err := strconv.ParseFloat(obs.Value, 64)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to parse value: %w", err)
	}

	// Parse date
	date, err := time.Parse("2006-01-02", obs.Date)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to parse date: %w", err)
	}

	return value, date, nil
}

// makeRequest makes an HTTP request to the FRED API
func (c *FREDClient) makeRequest(ctx context.Context, endpoint string, params url.Values) (*FREDResponse, error) {
	reqURL := c.baseURL + endpoint + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("FRED API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result FREDResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// GetUnemploymentRate returns the latest unemployment rate
func (c *FREDClient) GetUnemploymentRate(ctx context.Context) (float64, time.Time, error) {
	return c.getLatestValue(ctx, SeriesUnemployment)
}

// GetInflationRate returns the latest CPI-based inflation rate
func (c *FREDClient) GetInflationRate(ctx context.Context) (float64, time.Time, error) {
	// Get current and year-ago values to calculate YoY inflation
	endDate := time.Now()
	startDate := endDate.AddDate(-1, -1, 0) // 13 months ago

	obs, err := c.GetSeries(ctx, SeriesCPI, startDate, endDate)
	if err != nil {
		return 0, time.Time{}, err
	}

	if len(obs) < 12 {
		return 0, time.Time{}, fmt.Errorf("insufficient data for inflation calculation")
	}

	// Get latest and year-ago values
	latest := obs[len(obs)-1]
	yearAgo := obs[0]

	latestVal, err := strconv.ParseFloat(latest.Value, 64)
	if err != nil {
		return 0, time.Time{}, err
	}

	yearAgoVal, err := strconv.ParseFloat(yearAgo.Value, 64)
	if err != nil {
		return 0, time.Time{}, err
	}

	// Calculate YoY inflation rate
	inflation := ((latestVal - yearAgoVal) / yearAgoVal) * 100

	date, _ := time.Parse("2006-01-02", latest.Date)

	return inflation, date, nil
}

// GetRentalVacancyRate returns the latest national rental vacancy rate
// FRED series RRVRUSQ156N is quarterly data
func (c *FREDClient) GetRentalVacancyRate(ctx context.Context) (float64, time.Time, error) {
	return c.getLatestValue(ctx, SeriesRentalVacancy)
}

// GetBuildingPermits returns the latest national building permits (thousands of units, annualized)
func (c *FREDClient) GetBuildingPermits(ctx context.Context) (float64, time.Time, error) {
	return c.getLatestValue(ctx, SeriesBuildingPermits)
}

// GetHousingStarts returns the latest national housing starts (thousands of units, annualized)
func (c *FREDClient) GetHousingStarts(ctx context.Context) (float64, time.Time, error) {
	return c.getLatestValue(ctx, SeriesHousingStarts)
}

// IsConfigured returns true if the client has an API key
func (c *FREDClient) IsConfigured() bool {
	return c.apiKey != ""
}

// GetMetroEmploymentGrowth returns the year-over-year employment growth rate for a metro area
// Uses FRED's metro employment series (e.g., PEOI for Peoria, IL)
func (c *FREDClient) GetMetroEmploymentGrowth(ctx context.Context, city, state string) (float64, error) {
	// Try to find a FRED series for this metro
	seriesID := c.lookupMetroEmploymentSeries(city, state)
	if seriesID == "" {
		// Fall back to national unemployment rate as proxy (inverted)
		c.logger.Debug("no metro employment series found, using national proxy",
			"city", city, "state", state)
		return c.getNationalEmploymentGrowthProxy(ctx)
	}

	// Get 13 months of data to calculate YoY
	endDate := time.Now()
	startDate := endDate.AddDate(-1, -1, 0)

	obs, err := c.GetSeries(ctx, seriesID, startDate, endDate)
	if err != nil {
		c.logger.Warn("failed to get metro employment series, using national proxy",
			"series", seriesID, "error", err)
		return c.getNationalEmploymentGrowthProxy(ctx)
	}

	if len(obs) < 12 {
		c.logger.Debug("insufficient metro employment data, using national proxy",
			"series", seriesID, "observations", len(obs))
		return c.getNationalEmploymentGrowthProxy(ctx)
	}

	// Calculate YoY growth
	latest := obs[len(obs)-1]
	yearAgo := obs[0]

	latestVal, err := strconv.ParseFloat(latest.Value, 64)
	if err != nil || latest.Value == "." {
		return c.getNationalEmploymentGrowthProxy(ctx)
	}

	yearAgoVal, err := strconv.ParseFloat(yearAgo.Value, 64)
	if err != nil || yearAgo.Value == "." || yearAgoVal == 0 {
		return c.getNationalEmploymentGrowthProxy(ctx)
	}

	growth := ((latestVal - yearAgoVal) / yearAgoVal) * 100

	c.logger.Debug("calculated metro employment growth",
		"city", city, "state", state,
		"series", seriesID, "growth", growth)

	return growth, nil
}

// getNationalEmploymentGrowthProxy returns an estimated employment growth
// based on national unemployment rate (lower unemployment = higher growth)
func (c *FREDClient) getNationalEmploymentGrowthProxy(ctx context.Context) (float64, error) {
	unemployment, _, err := c.GetUnemploymentRate(ctx)
	if err != nil {
		return 1.5, nil // Default moderate growth
	}

	// Rough proxy: if unemployment is low (3-4%), growth is higher (2-3%)
	// If unemployment is high (6%+), growth is lower or negative
	// This is a simplification but better than hardcoded defaults
	if unemployment <= 4.0 {
		return 2.5, nil
	} else if unemployment <= 5.0 {
		return 1.5, nil
	} else if unemployment <= 6.0 {
		return 0.5, nil
	}
	return 0.0, nil
}

// lookupMetroEmploymentSeries maps a city/state to a FRED employment series ID
// FRED uses various patterns like:
// - {PREFIX}NA for MSA total nonfarm (e.g., PEOI for Peoria)
// - SMS{STATE}{METRO}0000000001 for some metros
// This is a known metros mapping - expand as needed
func (c *FREDClient) lookupMetroEmploymentSeries(city, state string) string {
	// Normalize city name for lookup
	cityUpper := strings.ToUpper(city)
	stateUpper := strings.ToUpper(state)

	// Common metro employment series
	// Pattern: typically first 4 letters of metro + state abbreviation or similar
	// These are FRED series IDs for Total Nonfarm All Employees (Thousands)
	metroSeries := map[string]string{
		// Illinois
		"PEORIA_IL":       "PEOI",
		"CHICAGO_IL":      "CHIC",
		"SPRINGFIELD_IL":  "SPRI",
		"ROCKFORD_IL":     "ROCK",
		"CHAMPAIGN_IL":    "CHAM",
		"BLOOMINGTON_IL":  "BLOO",
		// Texas
		"HOUSTON_TX":      "HOUS",
		"DALLAS_TX":       "DALL",
		"AUSTIN_TX":       "AUST",
		"SAN ANTONIO_TX":  "SANA",
		// California
		"LOS ANGELES_CA":  "LOSA",
		"SAN FRANCISCO_CA":"SANF",
		"SAN DIEGO_CA":    "SAND",
		"SAN JOSE_CA":     "SANJ",
		// Florida
		"MIAMI_FL":        "MIAM",
		"TAMPA_FL":        "TAMP",
		"ORLANDO_FL":      "ORLA",
		"JACKSONVILLE_FL": "JACK",
		// New York
		"NEW YORK_NY":     "NEWY",
		"BUFFALO_NY":      "BUFF",
		// Other major metros
		"PHOENIX_AZ":      "PHOE",
		"DENVER_CO":       "DENV",
		"SEATTLE_WA":      "SEAT",
		"PORTLAND_OR":     "PORT",
		"ATLANTA_GA":      "ATLA",
		"BOSTON_MA":       "BOST",
		"DETROIT_MI":      "DETR",
		"MINNEAPOLIS_MN":  "MINN",
		"CHARLOTTE_NC":    "CHAR",
		"NASHVILLE_TN":    "NASH",
		"LAS VEGAS_NV":    "LASV",
	}

	key := cityUpper + "_" + stateUpper
	if series, ok := metroSeries[key]; ok {
		return series
	}

	return ""
}
