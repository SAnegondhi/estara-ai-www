package locationexpander

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/ai/anthropic"
)

// ExpandedLocation represents a suburb or nearby location.
type ExpandedLocation struct {
	Name               string   `json:"name"`
	State              string   `json:"state"`
	ZipCodes           []string `json:"zipCodes"`
	Type               string   `json:"type"`
	DistanceFromCenter string   `json:"distanceFromCenter,omitempty"`
}

// LocationExpansionResult is the structured expansion response.
type LocationExpansionResult struct {
	PrimaryLocation  string             `json:"primaryLocation"`
	ExpandedLocations []ExpandedLocation `json:"expandedLocations"`
	MetroAreaName    string             `json:"metroAreaName"`
	TotalZipCodes    int                `json:"totalZipCodes"`
	TotalLocations   int                `json:"totalLocations"`
}

// LocationExpansionOptions configures expansion behavior.
type LocationExpansionOptions struct {
	Radius string // close|medium|wide
}

const (
	cacheTTL              = 30 * 24 * time.Hour
	defaultRadius         = "medium"
	locationExpansionPrompt = `You are a US geography expert. Given a city/location, return the major suburbs, nearby towns, and commonly referenced communities in that metro area.

Return JSON only (no markdown) in this exact format:
{
  "metroAreaName": "Greater [City] Metropolitan Area",
  "locations": [
    {
      "name": "Town Name",
      "state": "XX",
      "zipCodes": ["12345", "12346"],
      "type": "suburb|town|township|village|city",
      "distanceFromCenter": "X miles N/S/E/W"
    }
  ]
}

Include:
- The primary city itself with all its major ZIP codes
- Major suburbs (population > 5,000)
- Nearby towns within the specified radius
- Commonly referenced communities that people associate with this metro

Do NOT include:
- Unincorporated areas without clear boundaries
- Duplicate ZIP codes across multiple entries
- Areas beyond the specified radius
- Very small hamlets with population under 2,000

IMPORTANT: Include at least 5 locations but no more than 50.
Order by distance from city center (nearest first).

Radius for this search:`
)

// Service expands locations using Anthropic and caches results in Redis.
type Service struct {
	client *anthropic.Client
	redis  *redisClient.Client
	logger *slog.Logger
}

// NewService creates a new location expansion service.
func NewService(client *anthropic.Client, redis *redisClient.Client) *Service {
	return &Service{
		client: client,
		redis:  redis,
		logger: slog.Default().With("component", "location_expander"),
	}
}

// ExpandLocation expands a single location into nearby towns/suburbs.
func (s *Service) ExpandLocation(ctx context.Context, location string, opts LocationExpansionOptions) (*LocationExpansionResult, error) {
	if s.client == nil {
		return nil, fmt.Errorf("anthropic client not configured")
	}

	radius := opts.Radius
	if radius == "" {
		radius = defaultRadius
	}

	cacheKey := s.cacheKey(location, radius)
	if s.redis != nil {
		if cached, err := s.redis.GetJSON(ctx, cacheKey); err == nil && cached != nil {
			var result LocationExpansionResult
			if err := json.Unmarshal(cached, &result); err == nil {
				s.logger.Debug("location expansion cache hit", "location", location)
				return &result, nil
			}
		}
	}

	city, state, err := parseLocation(location)
	if err != nil {
		return nil, err
	}

	radiusMiles := radiusToMiles(radius)
	prompt := fmt.Sprintf("%s %d miles\n\nLocation to analyze: %s, %s", locationExpansionPrompt, radiusMiles, city, state)

	response, err := s.client.Complete(ctx, "", prompt)
	if err != nil {
		return nil, fmt.Errorf("location expansion failed: %w", err)
	}

	parsed, err := parseExpansionResponse(response)
	if err != nil {
		return nil, err
	}

	result := &LocationExpansionResult{
		PrimaryLocation:  location,
		ExpandedLocations: parsed.Locations,
		MetroAreaName:    parsed.MetroAreaName,
	}
	result.TotalLocations = len(result.ExpandedLocations)
	for _, loc := range result.ExpandedLocations {
		result.TotalZipCodes += len(loc.ZipCodes)
	}

	if s.redis != nil {
		_ = s.redis.SetJSON(ctx, cacheKey, result, cacheTTL)
	}

	return result, nil
}

// ExpandLocations expands multiple locations in parallel.
func (s *Service) ExpandLocations(ctx context.Context, locations []string, opts LocationExpansionOptions) ([]LocationExpansionResult, error) {
	results := make([]LocationExpansionResult, 0, len(locations))
	for _, loc := range locations {
		result, err := s.ExpandLocation(ctx, loc, opts)
		if err != nil {
			return nil, err
		}
		results = append(results, *result)
	}
	return results, nil
}

// GetUniqueLocations flattens expansion results into unique location strings.
func (s *Service) GetUniqueLocations(expansions []LocationExpansionResult) []string {
	seen := make(map[string]struct{})
	for _, expansion := range expansions {
		for _, loc := range expansion.ExpandedLocations {
			if loc.Name == "" || loc.State == "" {
				continue
			}
			key := fmt.Sprintf("%s, %s", loc.Name, loc.State)
			seen[key] = struct{}{}
		}
	}

	unique := make([]string, 0, len(seen))
	for loc := range seen {
		unique = append(unique, loc)
	}
	return unique
}

type expansionResponse struct {
	MetroAreaName string             `json:"metroAreaName"`
	Locations     []ExpandedLocation `json:"locations"`
}

func parseExpansionResponse(raw string) (*expansionResponse, error) {
	clean := strings.TrimSpace(raw)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.TrimSpace(clean)

	var parsed expansionResponse
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse expansion response: %w", err)
	}

	if parsed.MetroAreaName == "" {
		parsed.MetroAreaName = "Metro Area"
	}

	return &parsed, nil
}

func parseLocation(location string) (string, string, error) {
	parts := strings.Split(location, ",")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid location format: %s", location)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

func radiusToMiles(radius string) int {
	switch strings.ToLower(radius) {
	case "close":
		return 10
	case "wide":
		return 30
	default:
		return 20
	}
}

func (s *Service) cacheKey(location, radius string) string {
	normalized := strings.ToLower(location)
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == ',' {
			return r
		}
		return -1
	}, normalized)
	return fmt.Sprintf("location_expansion:%s:%s", normalized, radius)
}
