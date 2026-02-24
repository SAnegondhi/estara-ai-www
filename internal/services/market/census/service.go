// Package census provides a centralized service for US Census Bureau demographic data.
// It uses three-tier caching (L0 memory -> L1 Redis -> L2 PostgreSQL) with annual TTL.
//
// Census ACS 5-Year Estimates release annually in December.
// Data includes: population, income, poverty rate, median age, household count.
//
// Geographic hierarchy: Place (city) -> County -> State (fallback)
package census

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
	"sync"
	"time"

	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
)

const (
	// Census API base URL
	baseURL = "https://api.census.gov/data"

	// ACS 5-Year estimates (most recent available)
	acsYear = "2022"

	// Cache key prefix
	cacheKeyPrefix = "census:service:"

	// Minimum cache TTL
	minCacheTTL = 1 * time.Hour

	// HTTP timeout
	httpTimeout = 30 * time.Second
)

// Census variable codes (ACS 5-Year Estimates)
const (
	varMedianIncome   = "B19013_001E" // Median Household Income
	varPopulation     = "B01003_001E" // Total Population
	varPerCapitaInc   = "B19301_001E" // Per Capita Income
	varBelowPoverty   = "B17001_002E" // Population Below Poverty Level
	varTotalPoverty   = "B17001_001E" // Total Population for Poverty calc
	varMedianAge      = "B01002_001E" // Median Age
	varHouseholdCount = "B11001_001E" // Total Households
)

// CensusData contains demographic data for a location
type CensusData struct {
	// Demographics
	Population            int64   `json:"population"`
	MedianHouseholdIncome float64 `json:"medianHouseholdIncome"`
	PerCapitaIncome       float64 `json:"perCapitaIncome"`
	PovertyRate           float64 `json:"povertyRate"` // Percentage
	MedianAge             float64 `json:"medianAge"`
	HouseholdCount        int64   `json:"householdCount"`

	// Geographic context
	City   string `json:"city"`
	County string `json:"county,omitempty"`
	State  string `json:"state"`
	Level  string `json:"level"` // "place", "county", or "state"

	// Metadata
	DataYear    int       `json:"dataYear"`
	LastUpdated time.Time `json:"lastUpdated"`
	NextRefresh time.Time `json:"nextRefresh"`
	Source      string    `json:"source"`
}

// Service provides centralized access to Census demographic data
type Service struct {
	apiKey  string
	redis   *redisClient.Client
	queries *queries.Queries
	logger  *slog.Logger
	client  *http.Client

	// In-memory cache (L0) - keyed by "city:state"
	mu       sync.RWMutex
	cache    map[string]*CensusData
	expiries map[string]time.Time
}

// NewService creates a new Census service
func NewService(apiKey string, redis *redisClient.Client, q *queries.Queries) *Service {
	return &Service{
		apiKey:   apiKey,
		redis:    redis,
		queries:  q,
		logger:   slog.Default().With("component", "census_service"),
		client:   &http.Client{Timeout: httpTimeout},
		cache:    make(map[string]*CensusData),
		expiries: make(map[string]time.Time),
	}
}

// IsConfigured returns true if the Census API key is set
func (s *Service) IsConfigured() bool {
	return s.apiKey != ""
}

// GetDemographics returns demographic data for a city/state
// Uses geographic fallback: Place (city) -> County -> State
func (s *Service) GetDemographics(ctx context.Context, city, state string) (*CensusData, error) {
	if !s.IsConfigured() {
		return nil, fmt.Errorf("census API key not configured")
	}

	cacheKey := s.cacheKey(city, state)

	// L0: Check in-memory cache
	s.mu.RLock()
	if data, ok := s.cache[cacheKey]; ok {
		if expiry, ok := s.expiries[cacheKey]; ok && time.Now().Before(expiry) {
			s.mu.RUnlock()
			return data, nil
		}
	}
	s.mu.RUnlock()

	// L1: Try Redis cache
	if s.redis != nil {
		cached, err := s.redis.GetJSON(ctx, cacheKey)
		if err == nil && cached != nil {
			var data CensusData
			if err := json.Unmarshal(cached, &data); err == nil {
				if time.Now().Before(data.NextRefresh) {
					s.updateMemoryCache(cacheKey, &data)
					s.logger.Debug("census data loaded from L1 (Redis)", "city", city, "state", state)
					return &data, nil
				}
			}
		}
	}

	// L2: Try PostgreSQL cache
	if s.queries != nil {
		cached, err := s.queries.GetSystemCache(ctx, cacheKey)
		if err == nil {
			var data CensusData
			if err := json.Unmarshal(cached.Value, &data); err == nil {
				if time.Now().Before(data.NextRefresh) {
					s.updateMemoryCache(cacheKey, &data)
					s.updateRedisCache(ctx, cacheKey, &data)
					s.logger.Debug("census data loaded from L2 (PostgreSQL)", "city", city, "state", state)
					return &data, nil
				}
			}
		}
	}

	// Fetch fresh data from Census API
	return s.fetchAndCache(ctx, city, state)
}

// GetStateDemographics returns state-level demographic data
func (s *Service) GetStateDemographics(ctx context.Context, state string) (*CensusData, error) {
	return s.GetDemographics(ctx, "", state)
}

// ForceRefresh forces a refresh of cached data for a location
func (s *Service) ForceRefresh(ctx context.Context, city, state string) (*CensusData, error) {
	return s.fetchAndCache(ctx, city, state)
}

// fetchAndCache fetches data from Census API and updates all cache layers
func (s *Service) fetchAndCache(ctx context.Context, city, state string) (*CensusData, error) {
	s.logger.Info("fetching census data", "city", city, "state", state)

	stateCode := normalizeStateCode(state)
	stateFIPS := getStateFIPS(stateCode)
	if stateFIPS == "" {
		return nil, fmt.Errorf("unknown state: %s", state)
	}

	var data *CensusData
	var err error

	// Try geographic levels in order: Place -> County -> State
	if city != "" {
		// Try city (place) level
		data, err = s.fetchPlaceData(ctx, city, stateFIPS)
		if err != nil {
			s.logger.Debug("place data not found, trying county", "city", city, "error", err)
		}

		// Try county level
		if data == nil {
			data, err = s.fetchCountyData(ctx, city, stateFIPS)
			if err != nil {
				s.logger.Debug("county data not found, using state", "city", city, "error", err)
			}
		}
	}

	// Final fallback: State level
	if data == nil {
		data, err = s.fetchStateData(ctx, stateFIPS)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch census data: %w", err)
		}
	}

	// Set metadata
	data.City = city
	data.State = stateCode
	data.DataYear = 2022
	data.LastUpdated = time.Now()
	data.NextRefresh = s.calculateNextRefresh()
	data.Source = "US Census Bureau ACS 5-Year Estimates"

	// Update all cache layers
	cacheKey := s.cacheKey(city, state)
	s.updateMemoryCache(cacheKey, data)
	s.updateRedisCache(ctx, cacheKey, data)
	s.updateL2Cache(ctx, cacheKey, data)

	s.logger.Info("census data fetched",
		"city", city,
		"state", state,
		"level", data.Level,
		"population", data.Population,
		"medianIncome", data.MedianHouseholdIncome,
	)

	return data, nil
}

// fetchPlaceData fetches city (place) level data
func (s *Service) fetchPlaceData(ctx context.Context, city, stateFIPS string) (*CensusData, error) {
	// First find the place code
	placeCode, err := s.findPlaceCode(ctx, city, stateFIPS)
	if err != nil || placeCode == "" {
		return nil, fmt.Errorf("place not found: %s", city)
	}

	variables := s.getVariableList()
	apiURL := fmt.Sprintf("%s/%s/acs/acs5?get=NAME,%s&for=place:%s&in=state:%s&key=%s",
		baseURL, acsYear, variables, placeCode, stateFIPS, s.apiKey)

	data, err := s.fetchAndParse(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	data.Level = "place"
	return data, nil
}

// fetchCountyData fetches county level data
func (s *Service) fetchCountyData(ctx context.Context, countyName, stateFIPS string) (*CensusData, error) {
	// Find county code
	countyCode, err := s.findCountyCode(ctx, countyName, stateFIPS)
	if err != nil || countyCode == "" {
		return nil, fmt.Errorf("county not found: %s", countyName)
	}

	variables := s.getVariableList()
	apiURL := fmt.Sprintf("%s/%s/acs/acs5?get=NAME,%s&for=county:%s&in=state:%s&key=%s",
		baseURL, acsYear, variables, countyCode, stateFIPS, s.apiKey)

	data, err := s.fetchAndParse(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	data.Level = "county"
	data.County = countyName
	return data, nil
}

// fetchStateData fetches state level data
func (s *Service) fetchStateData(ctx context.Context, stateFIPS string) (*CensusData, error) {
	variables := s.getVariableList()
	apiURL := fmt.Sprintf("%s/%s/acs/acs5?get=NAME,%s&for=state:%s&key=%s",
		baseURL, acsYear, variables, stateFIPS, s.apiKey)

	data, err := s.fetchAndParse(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	data.Level = "state"
	return data, nil
}

// findPlaceCode looks up the Census place code for a city.
// Census NAME format is "Peoria city, Illinois" or "Peoria Heights village, Illinois".
// We extract the place name before the type suffix and prefer an exact match to avoid
// returning a smaller place (e.g. "Peoria Heights") when the target is "Peoria".
func (s *Service) findPlaceCode(ctx context.Context, city, stateFIPS string) (string, error) {
	apiURL := fmt.Sprintf("%s/%s/acs/acs5?get=NAME&for=place:*&in=state:%s&key=%s",
		baseURL, acsYear, stateFIPS, s.apiKey)

	resp, err := s.doRequest(ctx, apiURL)
	if err != nil {
		return "", err
	}

	cityLower := strings.ToLower(strings.TrimSpace(city))

	// Census place type suffixes that appear after the place name in the NAME field.
	typeSuffixes := []string{
		" city,", " town,", " village,", " borough,", " cdp,",
		" township,", " municipality,", " urban district,", " city-county,",
		" consolidated government,",
	}

	var substringFallback string
	for i := 1; i < len(resp); i++ {
		row := resp[i]
		if len(row) < 3 {
			continue
		}
		name := strings.ToLower(row[0])
		placeCode := row[len(row)-1]

		// Prefer exact match: strip type suffix and compare.
		for _, suffix := range typeSuffixes {
			if idx := strings.Index(name, suffix); idx >= 0 {
				if name[:idx] == cityLower {
					return placeCode, nil
				}
				break
			}
		}

		// Keep first substring match as fallback if no exact match is found.
		if substringFallback == "" && strings.Contains(name, cityLower) {
			substringFallback = placeCode
		}
	}

	if substringFallback != "" {
		return substringFallback, nil
	}

	return "", fmt.Errorf("place not found: %s", city)
}

// findCountyCode looks up the Census county code
func (s *Service) findCountyCode(ctx context.Context, countyName, stateFIPS string) (string, error) {
	apiURL := fmt.Sprintf("%s/%s/acs/acs5?get=NAME&for=county:*&in=state:%s&key=%s",
		baseURL, acsYear, stateFIPS, s.apiKey)

	resp, err := s.doRequest(ctx, apiURL)
	if err != nil {
		return "", err
	}

	countyLower := strings.ToLower(countyName)
	for i := 1; i < len(resp); i++ {
		row := resp[i]
		if len(row) < 3 {
			continue
		}
		name := strings.ToLower(row[0])
		if strings.Contains(name, countyLower) {
			// County code is the last element
			return row[len(row)-1], nil
		}
	}

	return "", fmt.Errorf("county not found: %s", countyName)
}

// fetchAndParse fetches data from Census API and parses the response
func (s *Service) fetchAndParse(ctx context.Context, apiURL string) (*CensusData, error) {
	resp, err := s.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	if len(resp) < 2 {
		return nil, fmt.Errorf("no data returned")
	}

	return s.parseResponse(resp)
}

// doRequest makes an HTTP request to the Census API
func (s *Service) doRequest(ctx context.Context, apiURL string) ([][]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("census API error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result [][]string
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// parseResponse parses the Census API response into CensusData
func (s *Service) parseResponse(data [][]string) (*CensusData, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("insufficient data")
	}

	header := data[0]
	values := data[1]

	getValue := func(code string) float64 {
		for i, h := range header {
			if h == code && i < len(values) {
				val, err := strconv.ParseFloat(values[i], 64)
				if err != nil || val < 0 { // Census uses negative for missing
					return 0
				}
				return val
			}
		}
		return 0
	}

	belowPoverty := getValue(varBelowPoverty)
	totalPoverty := getValue(varTotalPoverty)
	var povertyRate float64
	if totalPoverty > 0 {
		povertyRate = (belowPoverty / totalPoverty) * 100
	}

	return &CensusData{
		Population:            int64(getValue(varPopulation)),
		MedianHouseholdIncome: getValue(varMedianIncome),
		PerCapitaIncome:       getValue(varPerCapitaInc),
		PovertyRate:           povertyRate,
		MedianAge:             getValue(varMedianAge),
		HouseholdCount:        int64(getValue(varHouseholdCount)),
	}, nil
}

// getVariableList returns comma-separated list of Census variables
func (s *Service) getVariableList() string {
	return url.QueryEscape(strings.Join([]string{
		varMedianIncome,
		varPopulation,
		varPerCapitaInc,
		varBelowPoverty,
		varTotalPoverty,
		varMedianAge,
		varHouseholdCount,
	}, ","))
}

// calculateNextRefresh returns the next December 15 (after ACS release)
func (s *Service) calculateNextRefresh() time.Time {
	now := time.Now()
	nextDec := time.Date(now.Year(), time.December, 15, 0, 0, 0, 0, time.UTC)

	if now.After(nextDec) {
		nextDec = nextDec.AddDate(1, 0, 0)
	}
	return nextDec
}

// cacheKey generates a cache key for a location
func (s *Service) cacheKey(city, state string) string {
	city = strings.ToLower(strings.TrimSpace(city))
	state = strings.ToLower(strings.TrimSpace(state))
	if city == "" {
		return fmt.Sprintf("%sstate:%s", cacheKeyPrefix, state)
	}
	return fmt.Sprintf("%s%s:%s", cacheKeyPrefix, city, state)
}

// updateMemoryCache updates the L0 in-memory cache
func (s *Service) updateMemoryCache(key string, data *CensusData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[key] = data
	s.expiries[key] = data.NextRefresh
}

// updateRedisCache updates the L1 Redis cache
func (s *Service) updateRedisCache(ctx context.Context, key string, data *CensusData) {
	if s.redis == nil {
		return
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		s.logger.Warn("failed to marshal census data for L1", "error", err)
		return
	}

	ttl := time.Until(data.NextRefresh)
	if ttl < minCacheTTL {
		ttl = minCacheTTL
	}

	if err := s.redis.SetJSON(ctx, key, jsonData, ttl); err != nil {
		s.logger.Warn("failed to cache census data in L1", "error", err)
	}
}

// updateL2Cache updates the L2 PostgreSQL cache
func (s *Service) updateL2Cache(ctx context.Context, key string, data *CensusData) {
	if s.queries == nil {
		return
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		s.logger.Warn("failed to marshal census data for L2", "error", err)
		return
	}

	_, err = s.queries.UpsertSystemCache(ctx, queries.UpsertSystemCacheParams{
		Key:       key,
		Value:     jsonData,
		ExpiresAt: data.NextRefresh,
	})
	if err != nil {
		s.logger.Warn("failed to cache census data in L2", "error", err)
	}
}

// String returns a string representation for logging
func (d *CensusData) String() string {
	return fmt.Sprintf("CensusData{city=%s, state=%s, level=%s, pop=%d, income=$%.0f}",
		d.City, d.State, d.Level, d.Population, d.MedianHouseholdIncome)
}
