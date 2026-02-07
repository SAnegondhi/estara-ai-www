// Package bls provides a centralized service for Bureau of Labor Statistics data.
// It uses three-tier caching (L0 memory -> L1 Redis -> L2 PostgreSQL) with smart TTL.
//
// BLS data releases monthly, typically:
// - Employment data: First Friday of month ~8:30am ET
// - CPI data: Around the 10th of month ~8:30am ET
//
// Includes: CPI (shelter, rent), unemployment (national + state), wages, employment
package bls

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
)

const (
	// BLS API v2 base URL
	baseURL = "https://api.bls.gov/publicAPI/v2/timeseries/data/"

	// Cache key prefixes
	cacheKeyPrefix   = "bls:service:"
	cacheKeyNational = cacheKeyPrefix + "national"

	// Default years of data to fetch
	defaultYearsBack = 2

	// Cache TTL bounds
	minCacheTTL = 1 * time.Hour
	maxCacheTTL = 7 * 24 * time.Hour

	// HTTP timeout
	httpTimeout = 30 * time.Second
)

// BLS Series IDs
const (
	// Consumer Price Index (CPI)
	SeriesCPIAllItems   = "CUSR0000SA0"   // CPI All Items (Urban)
	SeriesCPIShelter    = "CUSR0000SAH"   // CPI Shelter
	SeriesCPIRent       = "CUSR0000SEHA"  // CPI Rent of Primary Residence

	// Employment
	SeriesUnemployment  = "LNS14000000"   // National Unemployment Rate
	SeriesLaborForce    = "LNS11300000"   // Labor Force Participation Rate
	SeriesHourlyEarnings = "CES0500000003" // Avg Hourly Earnings (Total Private)
	SeriesConstruction  = "CES2000000001" // Construction Employment (thousands)

	// Job Openings (JOLTS)
	SeriesJobOpenings = "JTS000000000000000JOL" // Total Job Openings
)

// BLSData contains BLS economic indicators
type BLSData struct {
	// CPI Inflation
	CPIAllItems float64 `json:"cpiAllItems"`
	CPIShelter  float64 `json:"cpiShelter"`
	CPIRent     float64 `json:"cpiRent"`

	// National Employment
	UnemploymentRate        float64 `json:"unemploymentRate"`
	LaborForceParticipation float64 `json:"laborForceParticipation"`
	AverageHourlyEarnings   float64 `json:"averageHourlyEarnings"`
	ConstructionEmployment  float64 `json:"constructionEmployment"` // thousands
	JobOpenings             float64 `json:"jobOpenings"`            // thousands

	// State-level (when fetched)
	StateUnemploymentRate float64 `json:"stateUnemploymentRate,omitempty"`
	StateEmployment       float64 `json:"stateEmployment,omitempty"` // thousands
	State                 string  `json:"state,omitempty"`

	// Metadata
	AsOfDate    string    `json:"asOfDate"`    // e.g., "2026-01"
	AsOfPeriod  string    `json:"asOfPeriod"`  // e.g., "January 2026"
	LastUpdated time.Time `json:"lastUpdated"`
	NextRefresh time.Time `json:"nextRefresh"`
	Source      string    `json:"source"`
}

// Service provides centralized access to BLS economic data
type Service struct {
	apiKey  string
	redis   *redisClient.Client
	queries *queries.Queries
	logger  *slog.Logger
	client  *http.Client

	// In-memory cache (L0)
	mu             sync.RWMutex
	nationalCache  *BLSData
	nationalExpiry time.Time
	stateCache     map[string]*BLSData
	stateExpiries  map[string]time.Time
}

// NewService creates a new BLS service
func NewService(apiKey string, redis *redisClient.Client, q *queries.Queries) *Service {
	return &Service{
		apiKey:        apiKey,
		redis:         redis,
		queries:       q,
		logger:        slog.Default().With("component", "bls_service"),
		client:        &http.Client{Timeout: httpTimeout},
		stateCache:    make(map[string]*BLSData),
		stateExpiries: make(map[string]time.Time),
	}
}

// IsConfigured returns true if the BLS API key is set
func (s *Service) IsConfigured() bool {
	return s.apiKey != ""
}

// GetNationalData returns national economic indicators
func (s *Service) GetNationalData(ctx context.Context) (*BLSData, error) {
	// L0: Check in-memory cache
	s.mu.RLock()
	if s.nationalCache != nil && time.Now().Before(s.nationalExpiry) {
		data := *s.nationalCache
		s.mu.RUnlock()
		return &data, nil
	}
	s.mu.RUnlock()

	// L1: Try Redis cache
	if s.redis != nil {
		cached, err := s.redis.GetJSON(ctx, cacheKeyNational)
		if err == nil && cached != nil {
			var data BLSData
			if err := json.Unmarshal(cached, &data); err == nil {
				if time.Now().Before(data.NextRefresh) {
					s.updateNationalMemoryCache(&data)
					s.logger.Debug("BLS national data loaded from L1 (Redis)")
					return &data, nil
				}
			}
		}
	}

	// L2: Try PostgreSQL cache
	if s.queries != nil {
		cached, err := s.queries.GetSystemCache(ctx, cacheKeyNational)
		if err == nil {
			var data BLSData
			if err := json.Unmarshal(cached.Value, &data); err == nil {
				if time.Now().Before(data.NextRefresh) {
					s.updateNationalMemoryCache(&data)
					s.updateRedisCache(ctx, cacheKeyNational, &data)
					s.logger.Debug("BLS national data loaded from L2 (PostgreSQL)")
					return &data, nil
				}
			}
		}
	}

	// Fetch fresh data
	return s.fetchNationalData(ctx)
}

// GetStateData returns state-level unemployment and employment data
func (s *Service) GetStateData(ctx context.Context, state string) (*BLSData, error) {
	stateCode := normalizeStateCode(state)
	cacheKey := s.stateCacheKey(stateCode)

	// L0: Check in-memory cache
	s.mu.RLock()
	if data, ok := s.stateCache[stateCode]; ok {
		if expiry, ok := s.stateExpiries[stateCode]; ok && time.Now().Before(expiry) {
			s.mu.RUnlock()
			return data, nil
		}
	}
	s.mu.RUnlock()

	// L1: Try Redis
	if s.redis != nil {
		cached, err := s.redis.GetJSON(ctx, cacheKey)
		if err == nil && cached != nil {
			var data BLSData
			if err := json.Unmarshal(cached, &data); err == nil {
				if time.Now().Before(data.NextRefresh) {
					s.updateStateMemoryCache(stateCode, &data)
					s.logger.Debug("BLS state data loaded from L1", "state", stateCode)
					return &data, nil
				}
			}
		}
	}

	// L2: Try PostgreSQL
	if s.queries != nil {
		cached, err := s.queries.GetSystemCache(ctx, cacheKey)
		if err == nil {
			var data BLSData
			if err := json.Unmarshal(cached.Value, &data); err == nil {
				if time.Now().Before(data.NextRefresh) {
					s.updateStateMemoryCache(stateCode, &data)
					s.updateRedisCache(ctx, cacheKey, &data)
					s.logger.Debug("BLS state data loaded from L2", "state", stateCode)
					return &data, nil
				}
			}
		}
	}

	// Fetch fresh data
	return s.fetchStateData(ctx, stateCode)
}

// GetCombinedData returns national + state data merged
func (s *Service) GetCombinedData(ctx context.Context, state string) (*BLSData, error) {
	// Fetch both in parallel
	var wg sync.WaitGroup
	var national, stateData *BLSData
	var natErr, stateErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		national, natErr = s.GetNationalData(ctx)
	}()
	go func() {
		defer wg.Done()
		stateData, stateErr = s.GetStateData(ctx, state)
	}()
	wg.Wait()

	if natErr != nil && stateErr != nil {
		return nil, fmt.Errorf("failed to fetch BLS data: national=%v, state=%v", natErr, stateErr)
	}

	// Merge data
	result := &BLSData{
		LastUpdated: time.Now(),
		NextRefresh: s.calculateNextRefresh(),
		Source:      "Bureau of Labor Statistics",
	}

	if national != nil {
		result.CPIAllItems = national.CPIAllItems
		result.CPIShelter = national.CPIShelter
		result.CPIRent = national.CPIRent
		result.UnemploymentRate = national.UnemploymentRate
		result.LaborForceParticipation = national.LaborForceParticipation
		result.AverageHourlyEarnings = national.AverageHourlyEarnings
		result.ConstructionEmployment = national.ConstructionEmployment
		result.JobOpenings = national.JobOpenings
		result.AsOfDate = national.AsOfDate
		result.AsOfPeriod = national.AsOfPeriod
	}

	if stateData != nil {
		result.StateUnemploymentRate = stateData.StateUnemploymentRate
		result.StateEmployment = stateData.StateEmployment
		result.State = stateData.State
	}

	return result, nil
}

// ForceRefreshNational forces a refresh of national data
func (s *Service) ForceRefreshNational(ctx context.Context) (*BLSData, error) {
	return s.fetchNationalData(ctx)
}

// fetchNationalData fetches national indicators from BLS API
func (s *Service) fetchNationalData(ctx context.Context) (*BLSData, error) {
	s.logger.Info("fetching BLS national data")

	seriesIDs := []string{
		SeriesCPIAllItems,
		SeriesCPIShelter,
		SeriesCPIRent,
		SeriesUnemployment,
		SeriesLaborForce,
		SeriesHourlyEarnings,
		SeriesConstruction,
		SeriesJobOpenings,
	}

	results, err := s.fetchSeries(ctx, seriesIDs)
	if err != nil {
		return nil, err
	}

	data := &BLSData{
		CPIAllItems:             s.getLatestValue(results, SeriesCPIAllItems),
		CPIShelter:              s.getLatestValue(results, SeriesCPIShelter),
		CPIRent:                 s.getLatestValue(results, SeriesCPIRent),
		UnemploymentRate:        s.getLatestValue(results, SeriesUnemployment),
		LaborForceParticipation: s.getLatestValue(results, SeriesLaborForce),
		AverageHourlyEarnings:   s.getLatestValue(results, SeriesHourlyEarnings),
		ConstructionEmployment:  s.getLatestValue(results, SeriesConstruction),
		JobOpenings:             s.getLatestValue(results, SeriesJobOpenings),
		AsOfDate:                s.getLatestDate(results),
		AsOfPeriod:              s.getLatestPeriod(results),
		LastUpdated:             time.Now(),
		NextRefresh:             s.calculateNextRefresh(),
		Source:                  "Bureau of Labor Statistics",
	}

	// Update caches
	s.updateNationalMemoryCache(data)
	s.updateRedisCache(ctx, cacheKeyNational, data)
	s.updateL2Cache(ctx, cacheKeyNational, data)

	s.logger.Info("BLS national data fetched",
		"unemployment", data.UnemploymentRate,
		"cpiShelter", data.CPIShelter,
		"asOf", data.AsOfPeriod,
	)

	return data, nil
}

// fetchStateData fetches state-level data from BLS API
func (s *Service) fetchStateData(ctx context.Context, stateCode string) (*BLSData, error) {
	fips := getStateFIPS(stateCode)
	if fips == "" {
		return nil, fmt.Errorf("unknown state: %s", stateCode)
	}

	s.logger.Info("fetching BLS state data", "state", stateCode)

	// LAUS series: LASST{FIPS}0000000003 = unemployment, 0000000005 = employment
	unemploymentID := fmt.Sprintf("LASST%s0000000003", fips)
	employmentID := fmt.Sprintf("LASST%s0000000005", fips)

	results, err := s.fetchSeries(ctx, []string{unemploymentID, employmentID})
	if err != nil {
		return nil, err
	}

	data := &BLSData{
		StateUnemploymentRate: s.getLatestValue(results, unemploymentID),
		StateEmployment:       s.getLatestValue(results, employmentID),
		State:                 stateCode,
		AsOfDate:              s.getLatestDate(results),
		AsOfPeriod:            s.getLatestPeriod(results),
		LastUpdated:           time.Now(),
		NextRefresh:           s.calculateNextRefresh(),
		Source:                "Bureau of Labor Statistics",
	}

	// Update caches
	cacheKey := s.stateCacheKey(stateCode)
	s.updateStateMemoryCache(stateCode, data)
	s.updateRedisCache(ctx, cacheKey, data)
	s.updateL2Cache(ctx, cacheKey, data)

	s.logger.Info("BLS state data fetched",
		"state", stateCode,
		"unemployment", data.StateUnemploymentRate,
	)

	return data, nil
}

// BLS API response types
type blsResponse struct {
	Status      string   `json:"status"`
	ResponseTime int     `json:"responseTime"`
	Message     []string `json:"message"`
	Results     *struct {
		Series []blsSeries `json:"series"`
	} `json:"Results"`
}

type blsSeries struct {
	SeriesID string        `json:"seriesID"`
	Data     []blsDataPoint `json:"data"`
}

type blsDataPoint struct {
	Year       string `json:"year"`
	Period     string `json:"period"`
	PeriodName string `json:"periodName"`
	Value      string `json:"value"`
}

// fetchSeries fetches multiple series from BLS API
func (s *Service) fetchSeries(ctx context.Context, seriesIDs []string) ([]blsSeries, error) {
	currentYear := time.Now().Year()
	startYear := currentYear - defaultYearsBack

	reqBody := map[string]interface{}{
		"seriesid":  seriesIDs,
		"startyear": strconv.Itoa(startYear),
		"endyear":   strconv.Itoa(currentYear),
	}

	if s.apiKey != "" {
		reqBody["registrationkey"] = s.apiKey
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result blsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse BLS response: %w", err)
	}

	if result.Status != "REQUEST_SUCCEEDED" {
		return nil, fmt.Errorf("BLS API error: %v", result.Message)
	}

	if result.Results == nil {
		return nil, fmt.Errorf("no results in BLS response")
	}

	return result.Results.Series, nil
}

// getLatestValue extracts the most recent value from a series
func (s *Service) getLatestValue(series []blsSeries, seriesID string) float64 {
	for _, ser := range series {
		if ser.SeriesID == seriesID && len(ser.Data) > 0 {
			val, err := strconv.ParseFloat(ser.Data[0].Value, 64)
			if err == nil {
				return val
			}
		}
	}
	return 0
}

// getLatestDate returns the date of the most recent data point
func (s *Service) getLatestDate(series []blsSeries) string {
	if len(series) == 0 || len(series[0].Data) == 0 {
		return ""
	}
	dp := series[0].Data[0]
	// Convert M01 -> 01
	month := dp.Period[1:] // Remove 'M'
	return fmt.Sprintf("%s-%s", dp.Year, month)
}

// getLatestPeriod returns the human-readable period
func (s *Service) getLatestPeriod(series []blsSeries) string {
	if len(series) == 0 || len(series[0].Data) == 0 {
		return ""
	}
	dp := series[0].Data[0]
	return fmt.Sprintf("%s %s", dp.PeriodName, dp.Year)
}

// calculateNextRefresh determines next refresh based on BLS release schedule
// Employment data releases first Friday of month, CPI around 10th
func (s *Service) calculateNextRefresh() time.Time {
	now := time.Now()
	loc, _ := time.LoadLocation("America/New_York")
	nowET := now.In(loc)

	// Next first Friday at 9am ET (jobs report)
	nextFirstFriday := s.nextFirstFriday(nowET, 9, 0)

	// Next 10th of month at 9am ET (CPI release)
	nextCPI := s.nextMonthDay(nowET, 10, 9, 0)

	// Return the earlier of the two
	if nextFirstFriday.Before(nextCPI) {
		return nextFirstFriday
	}
	return nextCPI
}

// nextFirstFriday finds the next first Friday of a month
func (s *Service) nextFirstFriday(from time.Time, hour, minute int) time.Time {
	year, month, _ := from.Date()

	for i := 0; i < 3; i++ {
		first := time.Date(year, month, 1, hour, minute, 0, 0, from.Location())
		daysUntilFriday := int(time.Friday - first.Weekday())
		if daysUntilFriday < 0 {
			daysUntilFriday += 7
		}
		firstFriday := first.AddDate(0, 0, daysUntilFriday)

		if firstFriday.After(from) {
			return firstFriday
		}

		month++
		if month > 12 {
			month = 1
			year++
		}
	}

	return from.Add(24 * time.Hour)
}

// nextMonthDay finds the next occurrence of a day in a month
func (s *Service) nextMonthDay(from time.Time, day, hour, minute int) time.Time {
	year, month, _ := from.Date()

	target := time.Date(year, month, day, hour, minute, 0, 0, from.Location())
	if target.After(from) {
		return target
	}

	month++
	if month > 12 {
		month = 1
		year++
	}
	return time.Date(year, month, day, hour, minute, 0, 0, from.Location())
}

// stateCacheKey generates a cache key for state data
func (s *Service) stateCacheKey(stateCode string) string {
	return fmt.Sprintf("%sstate:%s", cacheKeyPrefix, stateCode)
}

// updateNationalMemoryCache updates the L0 cache for national data
func (s *Service) updateNationalMemoryCache(data *BLSData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nationalCache = data
	s.nationalExpiry = data.NextRefresh
}

// updateStateMemoryCache updates the L0 cache for state data
func (s *Service) updateStateMemoryCache(stateCode string, data *BLSData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateCache[stateCode] = data
	s.stateExpiries[stateCode] = data.NextRefresh
}

// updateRedisCache updates the L1 Redis cache
func (s *Service) updateRedisCache(ctx context.Context, key string, data *BLSData) {
	if s.redis == nil {
		return
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		s.logger.Warn("failed to marshal BLS data for L1", "error", err)
		return
	}

	ttl := time.Until(data.NextRefresh)
	if ttl < minCacheTTL {
		ttl = minCacheTTL
	}
	if ttl > maxCacheTTL {
		ttl = maxCacheTTL
	}

	if err := s.redis.SetJSON(ctx, key, jsonData, ttl); err != nil {
		s.logger.Warn("failed to cache BLS data in L1", "error", err)
	}
}

// updateL2Cache updates the L2 PostgreSQL cache
func (s *Service) updateL2Cache(ctx context.Context, key string, data *BLSData) {
	if s.queries == nil {
		return
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		s.logger.Warn("failed to marshal BLS data for L2", "error", err)
		return
	}

	_, err = s.queries.UpsertSystemCache(ctx, queries.UpsertSystemCacheParams{
		Key:       key,
		Value:     jsonData,
		ExpiresAt: data.NextRefresh,
	})
	if err != nil {
		s.logger.Warn("failed to cache BLS data in L2", "error", err)
	}
}

// String returns a string representation for logging
func (d *BLSData) String() string {
	return fmt.Sprintf("BLSData{unemployment=%.1f%%, cpiShelter=%.1f, asOf=%s}",
		d.UnemploymentRate, d.CPIShelter, d.AsOfPeriod)
}
