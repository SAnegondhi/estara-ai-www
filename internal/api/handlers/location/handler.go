package location

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/config"
	db "github.com/estara-ai/www/internal/db"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles location-related HTTP requests
type Handler struct {
	store  *db.Store
	redis  *redisClient.Client
	cfg    *config.Config
	logger *slog.Logger
}

// NewHandler creates a new location handler
func NewHandler(store *db.Store, redis *redisClient.Client, cfg *config.Config) *Handler {
	return &Handler{
		store:  store,
		redis:  redis,
		cfg:    cfg,
		logger: slog.Default().With("component", "location_handler"),
	}
}

// LocationSuggestion represents a location autocomplete suggestion
// Matches www_v1 /api/location/autocomplete response format
type LocationSuggestion struct {
	ID            string `json:"id"`                      // e.g., "city:abc123"
	Display       string `json:"display"`                 // e.g., "Austin, TX"
	Canonical     string `json:"canonical"`               // e.g., "Austin, TX"
	Type          string `json:"type"`                    // "city", "state", "metro", "county", "zip"
	State         string `json:"state"`                   // State abbreviation (e.g., "TX")
	DataAvailable bool   `json:"dataAvailable"`           // Always true for our data
	Population    *int   `json:"population,omitempty"`    // City population
}

// AutocompleteResponse matches www_v1 /api/location/autocomplete response
type AutocompleteResponse struct {
	Suggestions []LocationSuggestion `json:"suggestions"`
	Total       int                  `json:"total"`
	FromCache   bool                 `json:"fromCache"`
	CacheLayer  string               `json:"cacheLayer,omitempty"` // "memory", "redis", "none"
}

// State abbreviation mappings
var stateAbbreviations = map[string]string{
	"alabama": "AL", "alaska": "AK", "arizona": "AZ", "arkansas": "AR",
	"california": "CA", "colorado": "CO", "connecticut": "CT", "delaware": "DE",
	"florida": "FL", "georgia": "GA", "hawaii": "HI", "idaho": "ID",
	"illinois": "IL", "indiana": "IN", "iowa": "IA", "kansas": "KS",
	"kentucky": "KY", "louisiana": "LA", "maine": "ME", "maryland": "MD",
	"massachusetts": "MA", "michigan": "MI", "minnesota": "MN", "mississippi": "MS",
	"missouri": "MO", "montana": "MT", "nebraska": "NE", "nevada": "NV",
	"new hampshire": "NH", "new jersey": "NJ", "new mexico": "NM", "new york": "NY",
	"north carolina": "NC", "north dakota": "ND", "ohio": "OH", "oklahoma": "OK",
	"oregon": "OR", "pennsylvania": "PA", "rhode island": "RI", "south carolina": "SC",
	"south dakota": "SD", "tennessee": "TN", "texas": "TX", "utah": "UT",
	"vermont": "VT", "virginia": "VA", "washington": "WA", "west virginia": "WV",
	"wisconsin": "WI", "wyoming": "WY", "district of columbia": "DC",
}

var abbreviationToState = map[string]string{
	"AL": "Alabama", "AK": "Alaska", "AZ": "Arizona", "AR": "Arkansas",
	"CA": "California", "CO": "Colorado", "CT": "Connecticut", "DE": "Delaware",
	"FL": "Florida", "GA": "Georgia", "HI": "Hawaii", "ID": "Idaho",
	"IL": "Illinois", "IN": "Indiana", "IA": "Iowa", "KS": "Kansas",
	"KY": "Kentucky", "LA": "Louisiana", "ME": "Maine", "MD": "Maryland",
	"MA": "Massachusetts", "MI": "Michigan", "MN": "Minnesota", "MS": "Mississippi",
	"MO": "Missouri", "MT": "Montana", "NE": "Nebraska", "NV": "Nevada",
	"NH": "New Hampshire", "NJ": "New Jersey", "NM": "New Mexico", "NY": "New York",
	"NC": "North Carolina", "ND": "North Dakota", "OH": "Ohio", "OK": "Oklahoma",
	"OR": "Oregon", "PA": "Pennsylvania", "RI": "Rhode Island", "SC": "South Carolina",
	"SD": "South Dakota", "TN": "Tennessee", "TX": "Texas", "UT": "Utah",
	"VT": "Vermont", "VA": "Virginia", "WA": "Washington", "WV": "West Virginia",
	"WI": "Wisconsin", "WY": "Wyoming", "DC": "District of Columbia",
}

// Autocomplete handles location autocomplete requests
func (h *Handler) Autocomplete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	query := httputil.GetQueryParam(r, "q", "")
	if query == "" {
		httputil.BadRequest(w, "q parameter is required")
		return
	}

	// Minimum length check
	if len(query) < 2 {
		httputil.JSON(w, http.StatusOK, AutocompleteResponse{
			Suggestions: []LocationSuggestion{},
			Total:       0,
			FromCache:   false,
			CacheLayer:  "none",
		})
		return
	}

	limit := httputil.GetQueryParamInt(r, "limit", 10)
	if limit > 25 {
		limit = 25
	}

	// Check Redis cache first
	cacheKey := "location:autocomplete:" + strings.ToLower(query)
	if h.redis != nil {
		cached, err := h.redis.Client.Get(ctx, cacheKey).Bytes()
		if err == nil && len(cached) > 0 {
			var suggestions []LocationSuggestion
			if json.Unmarshal(cached, &suggestions) == nil {
				h.logger.Debug("location autocomplete cache hit", "query", query)
				httputil.JSON(w, http.StatusOK, AutocompleteResponse{
					Suggestions: limitSuggestions(suggestions, limit),
					Total:       len(suggestions),
					FromCache:   true,
					CacheLayer:  "redis",
				})
				return
			}
		}
	}

	// Query database for matching locations
	suggestions, err := h.searchLocations(ctx, query, limit)
	if err != nil {
		h.logger.Error("failed to search locations", "error", err, "query", query)
		// Return empty results on error
		httputil.JSON(w, http.StatusOK, AutocompleteResponse{
			Suggestions: []LocationSuggestion{},
			Total:       0,
			FromCache:   false,
			CacheLayer:  "none",
		})
		return
	}

	// Cache results
	if h.redis != nil && len(suggestions) > 0 {
		if data, err := json.Marshal(suggestions); err == nil {
			h.redis.Client.Set(ctx, cacheKey, data, 24*time.Hour)
		}
	}

	httputil.JSON(w, http.StatusOK, AutocompleteResponse{
		Suggestions: limitSuggestions(suggestions, limit),
		Total:       len(suggestions),
		FromCache:   false,
		CacheLayer:  "none",
	})
}

// searchLocations searches for matching locations in the market database
// Uses city_states table which contains ~30,000 US cities from SimpleMaps
func (h *Handler) searchLocations(ctx context.Context, query string, limit int) ([]LocationSuggestion, error) {
	suggestions := make([]LocationSuggestion, 0)

	// Normalize query
	queryLower := strings.ToLower(strings.TrimSpace(query))

	// Handle "City, State" format - extract city and state for better matching
	var cityPart string
	var stateFilter string
	if parts := strings.Split(queryLower, ","); len(parts) >= 2 {
		cityPart = strings.TrimSpace(parts[0])
		stateFilter = strings.ToUpper(strings.TrimSpace(parts[1]))
	} else {
		cityPart = queryLower
	}

	// Check if market database is available
	if h.store.MarketPool() == nil {
		h.logger.Warn("market database not configured, falling back to state-only suggestions")
		return h.searchStatesOnly(queryLower)
	}

	// Build query based on whether state filter is provided
	var sqlQuery string
	var args []interface{}

	if stateFilter != "" {
		// User typed "City, State" format - filter by both city prefix AND state
		sqlQuery = `
			SELECT
				id,
				city,
				state_id,
				state_name,
				population
			FROM city_states
			WHERE city_lower LIKE $1 || '%' AND state_id = $2
			ORDER BY population DESC, city
			LIMIT $3
		`
		args = []interface{}{cityPart, stateFilter, limit}
	} else {
		// User typed city only - prefix search on city
		sqlQuery = `
			SELECT
				id,
				city,
				state_id,
				state_name,
				population
			FROM city_states
			WHERE city_lower LIKE $1 || '%'
			ORDER BY population DESC, city
			LIMIT $2
		`
		args = []interface{}{cityPart, limit}
	}

	rows, err := h.store.MarketPool().Query(ctx, sqlQuery, args...)
	if err != nil {
		h.logger.Error("city_states query failed", "error", err, "query", query)
		// Fall back to state-only suggestions on error
		return h.searchStatesOnly(queryLower)
	}
	defer rows.Close()

	for rows.Next() {
		var id, city, stateId, stateName string
		var population int
		if err := rows.Scan(&id, &city, &stateId, &stateName, &population); err != nil {
			h.logger.Warn("failed to scan city row", "error", err)
			continue
		}

		suggestions = append(suggestions, LocationSuggestion{
			ID:            "city:" + id,
			Display:       city + ", " + stateId,
			Canonical:     city + ", " + stateId,
			Type:          "city",
			State:         stateId,
			DataAvailable: true,
			Population:    &population,
		})
	}

	if err := rows.Err(); err != nil {
		h.logger.Warn("error iterating city rows", "error", err)
	}

	// If we have city results, return them
	if len(suggestions) > 0 {
		return suggestions, nil
	}

	// Fallback: Check if query matches a state
	return h.searchStatesOnly(queryLower)
}

// searchStatesOnly returns state suggestions matching the query
// Used as fallback when market database is unavailable or no cities match
func (h *Handler) searchStatesOnly(queryLower string) ([]LocationSuggestion, error) {
	suggestions := make([]LocationSuggestion, 0)

	// Check if query matches a state name
	for stateName, abbr := range stateAbbreviations {
		if strings.HasPrefix(stateName, queryLower) || strings.HasPrefix(strings.ToLower(abbr), queryLower) {
			fullName := abbreviationToState[abbr]
			suggestions = append(suggestions, LocationSuggestion{
				ID:            "state:" + strings.ToLower(abbr),
				Display:       fullName,
				Canonical:     fullName,
				Type:          "state",
				State:         abbr,
				DataAvailable: true,
			})
		}
	}

	// Also check by abbreviation
	upperQuery := strings.ToUpper(queryLower)
	if name, ok := abbreviationToState[upperQuery]; ok {
		// Check if we already added this state
		found := false
		for _, s := range suggestions {
			if s.State == upperQuery && s.Type == "state" {
				found = true
				break
			}
		}
		if !found {
			suggestions = append(suggestions, LocationSuggestion{
				ID:            "state:" + strings.ToLower(upperQuery),
				Display:       name,
				Canonical:     name,
				Type:          "state",
				State:         upperQuery,
				DataAvailable: true,
			})
		}
	}

	return suggestions, nil
}

// Validate handles location validation requests
func (h *Handler) Validate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	location := httputil.GetQueryParam(r, "location", "")
	if location == "" {
		httputil.BadRequest(w, "location parameter is required")
		return
	}

	// Parse location (expecting "City, State" format)
	parts := strings.Split(location, ",")
	if len(parts) < 2 {
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"valid":   false,
			"message": "Invalid format. Expected 'City, State'",
		})
		return
	}

	city := strings.TrimSpace(parts[0])
	state := strings.TrimSpace(parts[1])

	// Normalize state
	stateCode := state
	if len(state) > 2 {
		if abbr, ok := stateAbbreviations[strings.ToLower(state)]; ok {
			stateCode = abbr
		}
	} else {
		stateCode = strings.ToUpper(state)
	}

	// Validate state exists
	if _, ok := abbreviationToState[stateCode]; !ok {
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"valid":   false,
			"message": "Invalid state",
		})
		return
	}

	// Check if we have data for this city
	var count int
	err := h.store.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM cached_properties WHERE LOWER(city) = LOWER($1) AND UPPER(state) = $2`,
		city, stateCode,
	).Scan(&count)

	if err != nil {
		h.logger.Warn("validation query failed", "error", err)
	}

	// We consider it valid even if we don't have cached properties
	// The property search will handle the actual data fetching
	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"valid":       true,
		"city":        city,
		"state":       abbreviationToState[stateCode],
		"stateCode":   stateCode,
		"displayName": city + ", " + stateCode,
		"hasData":     count > 0,
	})
}

// limitSuggestions limits the number of suggestions returned
func limitSuggestions(suggestions []LocationSuggestion, limit int) []LocationSuggestion {
	if len(suggestions) <= limit {
		return suggestions
	}
	return suggestions[:limit]
}
