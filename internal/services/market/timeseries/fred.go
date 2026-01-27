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
	"time"
)

const (
	fredBaseURL = "https://api.stlouisfed.org/fred"

	// Series IDs
	SeriesMortgage30US = "MORTGAGE30US"  // 30-Year Fixed Rate Mortgage Average
	SeriesMortgage15US = "MORTGAGE15US"  // 15-Year Fixed Rate Mortgage Average
	SeriesUnemployment = "UNRATE"        // Unemployment Rate
	SeriesCPI          = "CPIAUCSL"      // Consumer Price Index
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
	apiKey  string
	baseURL string
	logger  *slog.Logger
}

// NewFREDClient creates a new FRED API client
func NewFREDClient(apiKey string) *FREDClient {
	return &FREDClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
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
func (c *FREDClient) GetSeries(ctx context.Context, seriesID string, startDate, endDate time.Time) ([]FREDObservation, error) {
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

	return resp.Observations, nil
}

// getLatestValue returns the most recent value for a series
func (c *FREDClient) getLatestValue(ctx context.Context, seriesID string) (float64, time.Time, error) {
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

	obs := resp.Observations[0]

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

// IsConfigured returns true if the client has an API key
func (c *FREDClient) IsConfigured() bool {
	return c.apiKey != ""
}
