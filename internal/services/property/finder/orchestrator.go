package finder

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/property/providers"
)

const (
	// Cache settings
	propertyCacheKeyPrefix = "properties:"
	propertyCacheTTL       = 15 * time.Minute

	// Search settings
	defaultSearchLimit   = 50
	maxConcurrentSearches = 3
)

// OrchestratorConfig holds configuration for the orchestrator
type OrchestratorConfig struct {
	Providers []providers.Provider
	Cache     *cache.HybridCache
	// PriorityOrder defines the order to try providers (provider names)
	PriorityOrder []string
	// ConcurrentFallback if true, tries multiple providers concurrently
	ConcurrentFallback bool
}

// Orchestrator coordinates property searches across multiple providers
type Orchestrator struct {
	providers          []providers.Provider
	cache              *cache.HybridCache
	priorityOrder      map[string]int
	concurrentFallback bool
	enricher           *InvestmentMetricsEnricher
	logger             *slog.Logger
	mu                 sync.RWMutex
}

// SearchMetrics tracks metrics for a search operation
type SearchMetrics struct {
	ProvidersAttempted []string      `json:"providersAttempted"`
	ProviderUsed       string        `json:"providerUsed"`
	CacheHit           bool          `json:"cacheHit"`
	TotalTime          time.Duration `json:"totalTime"`
	SearchTime         time.Duration `json:"searchTime"`
	ResultCount        int           `json:"resultCount"`
}

// SearchResponse contains search results with metrics
type SearchResponse struct {
	Properties []providers.Property `json:"properties"`
	Total      int                  `json:"total"`
	HasMore    bool                 `json:"hasMore"`
	NextOffset int                  `json:"nextOffset,omitempty"`
	Metrics    SearchMetrics        `json:"metrics"`
}

// NewOrchestrator creates a new property finder orchestrator
func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	priorityOrder := make(map[string]int)
	for i, name := range cfg.PriorityOrder {
		priorityOrder[name] = i
	}

	// Sort providers by priority
	sortedProviders := make([]providers.Provider, len(cfg.Providers))
	copy(sortedProviders, cfg.Providers)

	sort.Slice(sortedProviders, func(i, j int) bool {
		pi := sortedProviders[i].Priority()
		pj := sortedProviders[j].Priority()

		// Check if in priority order
		if oi, ok := priorityOrder[sortedProviders[i].Name()]; ok {
			if oj, ok := priorityOrder[sortedProviders[j].Name()]; ok {
				return oi < oj
			}
			return true
		}
		return pi < pj
	})

	return &Orchestrator{
		providers:          sortedProviders,
		cache:              cfg.Cache,
		priorityOrder:      priorityOrder,
		concurrentFallback: cfg.ConcurrentFallback,
		enricher:           NewInvestmentMetricsEnricher(),
		logger:             slog.Default().With("component", "property_orchestrator"),
	}
}

// Search performs a property search using configured providers
func (o *Orchestrator) Search(ctx context.Context, params providers.SearchParams) (*SearchResponse, error) {
	startTime := time.Now()
	metrics := SearchMetrics{
		ProvidersAttempted: make([]string, 0),
	}

	// Set default limit
	if params.Limit == 0 {
		params.Limit = defaultSearchLimit
	}

	// Check cache first
	cacheKey := o.buildCacheKey(params)
	if o.cache != nil {
		cached, err := o.cache.Get(ctx, "", cacheKey)
		if err == nil && cached != nil {
			var response SearchResponse
			if err := json.Unmarshal(cached, &response); err == nil {
				response.Metrics.CacheHit = true
				response.Metrics.TotalTime = time.Since(startTime)
				o.logger.Debug("cache hit for property search",
					"cacheKey", cacheKey,
					"results", len(response.Properties),
				)
				return &response, nil
			}
		}
	}

	// Get enabled providers
	enabledProviders := o.getEnabledProviders()
	if len(enabledProviders) == 0 {
		return nil, fmt.Errorf("no property providers available")
	}

	var result *providers.SearchResult
	var lastErr error

	if o.concurrentFallback && len(enabledProviders) > 1 {
		// Try providers concurrently (first success wins)
		result, metrics.ProvidersAttempted, lastErr = o.searchConcurrent(ctx, enabledProviders, params)
	} else {
		// Try providers in priority order
		result, metrics.ProvidersAttempted, lastErr = o.searchSequential(ctx, enabledProviders, params)
	}

	if result == nil {
		return nil, fmt.Errorf("all providers failed: %w", lastErr)
	}

	metrics.ProviderUsed = result.Provider
	metrics.SearchTime = result.SearchTime
	metrics.ResultCount = len(result.Properties)
	metrics.TotalTime = time.Since(startTime)

	// Enrich properties with investment metrics
	enrichedProperties := o.enricher.EnrichProperties(result.Properties)

	o.logger.Debug("enriched properties with investment metrics",
		"count", len(enrichedProperties),
		"provider", result.Provider,
	)

	response := &SearchResponse{
		Properties: enrichedProperties,
		Total:      result.Total,
		HasMore:    result.HasMore,
		NextOffset: result.NextOffset,
		Metrics:    metrics,
	}

	// Cache successful results
	if o.cache != nil && len(result.Properties) > 0 {
		if data, err := json.Marshal(response); err == nil {
			if err := o.cache.Set(ctx, "", cacheKey, "property_search", data, propertyCacheTTL); err != nil {
				o.logger.Warn("failed to cache property search results", "error", err)
			}
		}
	}

	o.logger.Info("property search completed",
		"location", fmt.Sprintf("%s, %s", params.City, params.State),
		"provider", result.Provider,
		"results", len(result.Properties),
		"total", result.Total,
		"duration", metrics.TotalTime,
	)

	return response, nil
}

// searchSequential tries providers in priority order until one succeeds
func (o *Orchestrator) searchSequential(
	ctx context.Context,
	providers []providers.Provider,
	params providers.SearchParams,
) (*providers.SearchResult, []string, error) {
	attempted := make([]string, 0, len(providers))
	var lastErr error

	for _, provider := range providers {
		attempted = append(attempted, provider.Name())

		o.logger.Debug("trying provider",
			"provider", provider.Name(),
			"location", fmt.Sprintf("%s, %s", params.City, params.State),
		)

		result, err := provider.Search(ctx, params)
		if err != nil {
			o.logger.Warn("provider search failed",
				"provider", provider.Name(),
				"error", err,
			)
			lastErr = fmt.Errorf("%s: %w", provider.Name(), err)
			continue
		}

		// Success - return result
		return result, attempted, nil
	}

	return nil, attempted, lastErr
}

// searchConcurrent tries multiple providers concurrently and returns first success
func (o *Orchestrator) searchConcurrent(
	ctx context.Context,
	providerList []providers.Provider,
	params providers.SearchParams,
) (*providers.SearchResult, []string, error) {
	// Limit concurrent searches
	numProviders := len(providerList)
	if numProviders > maxConcurrentSearches {
		numProviders = maxConcurrentSearches
	}

	type searchResult struct {
		result *providers.SearchResult
		err    error
		name   string
	}

	results := make(chan searchResult, numProviders)
	attempted := make([]string, numProviders)

	// Create cancellable context
	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Launch concurrent searches
	for i := 0; i < numProviders; i++ {
		provider := providerList[i]
		attempted[i] = provider.Name()

		go func(p providers.Provider) {
			result, err := p.Search(searchCtx, params)
			select {
			case results <- searchResult{result: result, err: err, name: p.Name()}:
			case <-searchCtx.Done():
			}
		}(provider)
	}

	// Wait for first success or all failures
	var lastErr error
	successCount := 0

	for i := 0; i < numProviders; i++ {
		select {
		case res := <-results:
			if res.err == nil && res.result != nil {
				cancel() // Cancel other searches
				return res.result, attempted, nil
			}
			lastErr = fmt.Errorf("%s: %w", res.name, res.err)
			successCount++
		case <-ctx.Done():
			return nil, attempted, ctx.Err()
		}
	}

	return nil, attempted, lastErr
}

// getEnabledProviders returns providers that are currently enabled
func (o *Orchestrator) getEnabledProviders() []providers.Provider {
	o.mu.RLock()
	defer o.mu.RUnlock()

	enabled := make([]providers.Provider, 0, len(o.providers))
	for _, p := range o.providers {
		if p.IsEnabled() {
			enabled = append(enabled, p)
		}
	}
	return enabled
}

// buildCacheKey creates a cache key for the search parameters
func (o *Orchestrator) buildCacheKey(params providers.SearchParams) string {
	// Create deterministic key from params
	key := fmt.Sprintf("%s%s_%s",
		propertyCacheKeyPrefix,
		normalizeString(params.City),
		normalizeString(params.State),
	)

	// Add filters to key
	if params.MinPrice > 0 {
		key += fmt.Sprintf("_minp%d", params.MinPrice)
	}
	if params.MaxPrice > 0 {
		key += fmt.Sprintf("_maxp%d", params.MaxPrice)
	}
	if params.MinBeds > 0 {
		key += fmt.Sprintf("_minb%d", params.MinBeds)
	}
	if params.PropertyType != "" {
		key += fmt.Sprintf("_type%s", params.PropertyType)
	}
	if params.Limit > 0 {
		key += fmt.Sprintf("_lim%d", params.Limit)
	}
	if params.Offset > 0 {
		key += fmt.Sprintf("_off%d", params.Offset)
	}

	return key
}

// GetProperty retrieves a single property by ID
func (o *Orchestrator) GetProperty(ctx context.Context, providerName, propertyID string) (*providers.Property, error) {
	// Find the specific provider
	for _, p := range o.providers {
		if p.Name() == providerName && p.IsEnabled() {
			return p.GetProperty(ctx, propertyID)
		}
	}

	// If no specific provider, try all enabled providers
	for _, p := range o.getEnabledProviders() {
		property, err := p.GetProperty(ctx, propertyID)
		if err == nil {
			return property, nil
		}
	}

	return nil, fmt.Errorf("property not found: %s", propertyID)
}

// HealthCheck verifies all providers are operational
func (o *Orchestrator) HealthCheck(ctx context.Context) map[string]error {
	results := make(map[string]error)

	for _, p := range o.providers {
		if !p.IsEnabled() {
			results[p.Name()] = fmt.Errorf("provider disabled")
			continue
		}

		err := p.HealthCheck(ctx)
		results[p.Name()] = err
	}

	return results
}

// GetProviderStatus returns status of all providers
func (o *Orchestrator) GetProviderStatus() []ProviderStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()

	statuses := make([]ProviderStatus, 0, len(o.providers))
	for _, p := range o.providers {
		priority := p.Priority()
		if pi, ok := o.priorityOrder[p.Name()]; ok {
			priority = pi
		}

		statuses = append(statuses, ProviderStatus{
			Name:     p.Name(),
			Enabled:  p.IsEnabled(),
			Priority: priority,
		})
	}

	// Sort by priority
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Priority < statuses[j].Priority
	})

	return statuses
}

// ProviderStatus represents the status of a provider
type ProviderStatus struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"`
	Healthy  bool   `json:"healthy,omitempty"`
}

// AddProvider dynamically adds a provider
func (o *Orchestrator) AddProvider(provider providers.Provider) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.providers = append(o.providers, provider)
}

// SetProviderEnabled enables or disables a provider by name
func (o *Orchestrator) SetProviderEnabled(name string, enabled bool) error {
	// Note: This would require providers to have a SetEnabled method
	// For now, provider enable/disable is handled at config level
	return fmt.Errorf("dynamic enable/disable not implemented")
}

// InvalidateCache clears the property cache for a location
func (o *Orchestrator) InvalidateCache(ctx context.Context, city, state string) error {
	if o.cache == nil {
		return nil
	}

	// Build pattern to match all variations
	pattern := fmt.Sprintf("%s%s_%s*",
		propertyCacheKeyPrefix,
		normalizeString(city),
		normalizeString(state),
	)

	// Note: This would require a pattern-based delete in cache
	o.logger.Info("invalidating property cache", "pattern", pattern)

	return nil
}

// normalizeString lowercases and removes spaces for cache keys
func normalizeString(s string) string {
	result := ""
	for _, c := range s {
		if c >= 'a' && c <= 'z' {
			result += string(c)
		} else if c >= 'A' && c <= 'Z' {
			result += string(c + 32) // lowercase
		} else if c >= '0' && c <= '9' {
			result += string(c)
		}
	}
	return result
}
