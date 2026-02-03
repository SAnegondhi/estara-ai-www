package finder

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/property/providers"
)

const (
	// Cache settings
	// Property search cache TTL: 24 hours to match upstream data freshness
	// (third-party portals like Zillow/Realtor.com update every 12-24 hours)
	// See ADR-061 for rationale
	propertyCacheKeyPrefix = "properties:"
	propertyCacheTTL       = 24 * time.Hour
	propertyReadCacheKeyPrefix = "property_read:"
	propertyReadCacheTTL       = 24 * time.Hour

	// Search settings
	defaultSearchLimit   = 50
	maxConcurrentSearches = 3

	// YearBuilt enrichment settings
	defaultEnrichmentConcurrency = 10
	enrichmentTimeout            = 30 * time.Second

	// Streaming search settings
	streamingEnrichmentTimeout = 2 * time.Minute
)

// OrchestratorConfig holds configuration for the orchestrator
type OrchestratorConfig struct {
	Providers []providers.Provider
	Cache     *cache.HybridCache
	// PropertyCache is the size-based cache for individual property reads (ADR-061)
	// If nil, falls back to using Cache (HybridCache) with TTL-based caching
	PropertyCache *cache.PropertyCache
	// PriorityOrder defines the order to try providers (provider names)
	PriorityOrder []string
	// ConcurrentFallback if true, tries multiple providers concurrently
	ConcurrentFallback bool
	// EnrichmentConcurrency is max concurrent yearBuilt enrichment calls (default: 10)
	EnrichmentConcurrency int
}

// Orchestrator coordinates property searches across multiple providers
type Orchestrator struct {
	providers              []providers.Provider
	cache                  *cache.HybridCache    // Used for property search caching (24h TTL)
	propertyCache          *cache.PropertyCache  // Used for property read caching (size-based FIFO, ADR-061)
	priorityOrder          map[string]int
	concurrentFallback     bool
	enrichmentConcurrency  int
	enricher               *InvestmentMetricsEnricher
	enrichmentJobManager   *EnrichmentJobManager
	logger                 *slog.Logger
	mu                     sync.RWMutex
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
	Properties       []providers.Property `json:"properties"`
	Total            int                  `json:"total"`
	HasMore          bool                 `json:"hasMore"`
	NextOffset       int                  `json:"nextOffset,omitempty"`
	Metrics          SearchMetrics        `json:"metrics"`
	EnrichmentJobID  string               `json:"enrichmentJobId,omitempty"`
	EnrichmentStatus string               `json:"enrichmentStatus,omitempty"`
}

// SearchOptions configures search behavior
type SearchOptions struct {
	// AsyncEnrichment if true, returns properties immediately and enriches in background
	AsyncEnrichment bool
}

// StreamingResult represents a result sent via the streaming channel
type StreamingResult struct {
	Type     string              // "property", "progress", "complete", "error", "failed", "nodata"
	Property *providers.Property // For "property" type
	Progress *ProgressUpdate     // For "progress" type
	Complete *CompleteStatus     // For "complete" type
	Error    error               // For "error" type
}

// ProgressUpdate represents progress in streaming search
type ProgressUpdate struct {
	Processed int `json:"processed"`
	Total     int `json:"total"`
}

// CompleteStatus represents completion status
type CompleteStatus struct {
	Total    int `json:"total"`
	Enriched int `json:"enriched"`
	Failed   int `json:"failed"`
	NoData   int `json:"noData"`
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

	enrichmentConcurrency := cfg.EnrichmentConcurrency
	if enrichmentConcurrency <= 0 {
		enrichmentConcurrency = defaultEnrichmentConcurrency
	}

	orch := &Orchestrator{
		providers:             sortedProviders,
		cache:                 cfg.Cache,
		propertyCache:         cfg.PropertyCache,
		priorityOrder:         priorityOrder,
		concurrentFallback:    cfg.ConcurrentFallback,
		enrichmentConcurrency: enrichmentConcurrency,
		enricher:              NewInvestmentMetricsEnricher(),
		logger:                slog.Default().With("component", "property_orchestrator"),
	}

	// Initialize enrichment job manager for async enrichment
	// Pass PropertyCache for size-based caching (ADR-061), fallback to HybridCache
	orch.enrichmentJobManager = NewEnrichmentJobManager(cfg.Cache, cfg.PropertyCache, sortedProviders, enrichmentConcurrency)

	return orch
}

// GetEnrichmentJobManager returns the enrichment job manager for SSE handlers
func (o *Orchestrator) GetEnrichmentJobManager() *EnrichmentJobManager {
	return o.enrichmentJobManager
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

	// Enrich properties with yearBuilt from Property API (if missing)
	enrichedWithYearBuilt := o.enrichPropertiesWithYearBuilt(ctx, result.Properties)

	// Enrich properties with investment metrics
	enrichedProperties := o.enricher.EnrichProperties(enrichedWithYearBuilt)

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
		// Cache raw property reads for 24 hours to reduce provider calls.
		o.cachePropertyReads(ctx, result.Provider, result.Properties)
		// Pass response directly - cache.Set will marshal it
		// (passing pre-marshaled bytes would cause double-encoding)
		if err := o.cache.Set(ctx, "", cacheKey, "property_search", response, propertyCacheTTL); err != nil {
			o.logger.Warn("failed to cache property search results", "error", err)
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

// SearchAsync performs a property search but returns immediately without waiting for enrichment.
// Properties are returned with investment metrics calculated, and yearBuilt enrichment
// happens in background. Returns a job ID for tracking enrichment progress via SSE.
func (o *Orchestrator) SearchAsync(ctx context.Context, params providers.SearchParams) (*SearchResponse, error) {
	startTime := time.Now()
	metrics := SearchMetrics{
		ProvidersAttempted: make([]string, 0),
	}

	// Set default limit
	if params.Limit == 0 {
		params.Limit = defaultSearchLimit
	}

	// Check cache first - if cached, it's already fully enriched
	cacheKey := o.buildCacheKey(params)
	if o.cache != nil {
		cached, err := o.cache.Get(ctx, "", cacheKey)
		if err == nil && cached != nil {
			var response SearchResponse
			if err := json.Unmarshal(cached, &response); err == nil {
				response.Metrics.CacheHit = true
				response.Metrics.TotalTime = time.Since(startTime)
				response.EnrichmentStatus = EnrichmentStatusCompleted
				o.logger.Debug("cache hit for async property search",
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
		result, metrics.ProvidersAttempted, lastErr = o.searchConcurrent(ctx, enabledProviders, params)
	} else {
		result, metrics.ProvidersAttempted, lastErr = o.searchSequential(ctx, enabledProviders, params)
	}

	if result == nil {
		return nil, fmt.Errorf("all providers failed: %w", lastErr)
	}

	metrics.ProviderUsed = result.Provider
	metrics.SearchTime = result.SearchTime
	metrics.ResultCount = len(result.Properties)
	metrics.TotalTime = time.Since(startTime)

	// Enrich with investment metrics (this is fast, doesn't need external API calls)
	enrichedProperties := o.enricher.EnrichProperties(result.Properties)

	// Start background enrichment job for yearBuilt/sqft/etc
	var enrichmentJobID string
	var enrichmentStatus string
	if o.enrichmentJobManager != nil {
		jobID, err := o.enrichmentJobManager.StartEnrichmentJob(ctx, enrichedProperties)
		if err != nil {
			o.logger.Warn("failed to start enrichment job", "error", err)
			enrichmentStatus = EnrichmentStatusFailed
		} else {
			enrichmentJobID = jobID
			// Check initial status
			status, _ := o.enrichmentJobManager.GetJobStatus(ctx, jobID)
			if status != nil {
				enrichmentStatus = status.Status
			} else {
				enrichmentStatus = EnrichmentStatusPending
			}
		}
	}

	response := &SearchResponse{
		Properties:       enrichedProperties,
		Total:            result.Total,
		HasMore:          result.HasMore,
		NextOffset:       result.NextOffset,
		Metrics:          metrics,
		EnrichmentJobID:  enrichmentJobID,
		EnrichmentStatus: enrichmentStatus,
	}

	// Cache property reads for 24 hours (but not the search results since they're not fully enriched)
	// Run in background to avoid blocking the response - PostgreSQL upserts are slow
	if o.cache != nil && len(result.Properties) > 0 {
		providerName := result.Provider
		propertiesToCache := make([]providers.Property, len(result.Properties))
		copy(propertiesToCache, result.Properties)
		go o.cachePropertyReads(context.Background(), providerName, propertiesToCache)
	}

	o.logger.Info("async property search completed",
		"location", fmt.Sprintf("%s, %s", params.City, params.State),
		"provider", result.Provider,
		"results", len(result.Properties),
		"total", result.Total,
		"duration", metrics.TotalTime,
		"enrichmentJobId", enrichmentJobID,
	)

	return response, nil
}

// SearchWithStreaming performs search and enrichment, sending results via channel as properties are enriched.
// This replaces the two-phase approach (search → async enrichment) with inline enrichment streamed to client.
func (o *Orchestrator) SearchWithStreaming(
	ctx context.Context,
	params providers.SearchParams,
	results chan<- StreamingResult,
) error {
	startTime := time.Now()

	// Set default limit
	if params.Limit == 0 {
		params.Limit = defaultSearchLimit
	}

	// Get enabled providers
	enabledProviders := o.getEnabledProviders()
	if len(enabledProviders) == 0 {
		return fmt.Errorf("no property providers available")
	}

	// Step 1: Call Listing API to get properties
	var searchResult *providers.SearchResult
	var lastErr error

	if o.concurrentFallback && len(enabledProviders) > 1 {
		searchResult, _, lastErr = o.searchConcurrent(ctx, enabledProviders, params)
	} else {
		searchResult, _, lastErr = o.searchSequential(ctx, enabledProviders, params)
	}

	if searchResult == nil {
		return fmt.Errorf("property search failed: %w", lastErr)
	}

	o.logger.Info("streaming search: listing API returned properties",
		"location", fmt.Sprintf("%s, %s", params.City, params.State),
		"provider", searchResult.Provider,
		"count", len(searchResult.Properties),
	)

	// Send initial progress
	results <- StreamingResult{
		Type: "progress",
		Progress: &ProgressUpdate{
			Processed: 0,
			Total:     len(searchResult.Properties),
		},
	}

	// Find HasData provider for enrichment
	var hasDataProvider *providers.HasDataProvider
	for _, p := range o.providers {
		if hp, ok := p.(*providers.HasDataProvider); ok && hp.IsEnabled() {
			hasDataProvider = hp
			break
		}
	}

	if hasDataProvider == nil {
		o.logger.Warn("HasData provider not available for streaming enrichment, returning unenriched properties")
		// Still return properties without yearBuilt enrichment
		for i, prop := range searchResult.Properties {
			enrichedProp := o.enricher.EnrichProperty(prop)
			results <- StreamingResult{
				Type:     "property",
				Property: &enrichedProp,
			}
			results <- StreamingResult{
				Type: "progress",
				Progress: &ProgressUpdate{
					Processed: i + 1,
					Total:     len(searchResult.Properties),
				},
			}
		}
		return nil
	}

	// Step 2: For each property, enrich via Property API and send to channel
	enrichCtx, cancel := context.WithTimeout(ctx, streamingEnrichmentTimeout)
	defer cancel()

	// Use semaphore for concurrency control
	sem := make(chan struct{}, o.enrichmentConcurrency)
	var wg sync.WaitGroup

	// Counters for stats
	var processed, enriched, failed, noData int32

	// Process properties in parallel but send results in order
	// We use a buffered channel per property to maintain order
	type orderedResult struct {
		index    int
		property *providers.Property
		status   string // "enriched", "failed", "nodata"
	}
	orderedResults := make(chan orderedResult, len(searchResult.Properties))

	for i := range searchResult.Properties {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-enrichCtx.Done():
				atomic.AddInt32(&failed, 1)
				orderedResults <- orderedResult{index: idx, status: "failed"}
				return
			}

			prop := searchResult.Properties[idx]

			// Check if property needs enrichment (has Zillow URL and missing yearBuilt)
			needsEnrichment := prop.YearBuilt == 0 &&
				prop.ListingURL != "" &&
				strings.Contains(prop.ListingURL, "zillow.com")

			o.logger.Info("streaming search: checking property for enrichment",
				"propertyId", prop.ID,
				"yearBuilt", prop.YearBuilt,
				"listingURL", prop.ListingURL,
				"needsEnrichment", needsEnrichment,
			)

			if needsEnrichment {
				// Check cache first
				if cachedProp, isEnriched := o.getCachedEnrichedProperty(enrichCtx, prop.ProviderName, prop.ID); isEnriched {
					// Use cached enriched data
					o.applyEnrichedData(&prop, cachedProp)
					o.logger.Debug("streaming search: used cached property", "propertyId", prop.ID)
				} else {
					// Call Property API for enrichment
					propData, err := hasDataProvider.GetPropertyByURL(enrichCtx, prop.ListingURL)
					if err != nil {
						o.logger.Warn("streaming search: enrichment API error",
							"propertyId", prop.ID,
							"listingURL", prop.ListingURL,
							"error", err,
						)
						// Continue with unenriched property
					} else {
						o.logger.Info("streaming search: Property API returned data",
							"propertyId", prop.ID,
							"yearBuilt", propData.YearBuilt,
							"livingArea", propData.LivingArea,
							"rentZestimate", propData.RentZestimate,
						)

						// Apply enrichment data from API response
						wasEnriched := false

						if propData.YearBuilt > 0 && prop.YearBuilt == 0 {
							prop.YearBuilt = propData.YearBuilt
							wasEnriched = true
						}

						if prop.Sqft == 0 {
							if propData.LivingArea > 0 {
								prop.Sqft = propData.LivingArea
								wasEnriched = true
							} else if propData.Area > 0 {
								prop.Sqft = propData.Area
								wasEnriched = true
							}
						}

						if propData.LotSize > 0 && prop.LotSize == 0 {
							prop.LotSize = propData.LotSize
							wasEnriched = true
						}

						if propData.RentZestimate > 0 && prop.EstimatedRent == 0 {
							prop.EstimatedRent = propData.RentZestimate
							wasEnriched = true
						}

						if wasEnriched {
							// Cache the enriched property
							go o.cachePropertyRead(context.Background(), prop.ProviderName, &prop)
						}
					}
				}
			}

			// Enrich with investment metrics
			enrichedProp := o.enricher.EnrichProperty(prop)

			// Determine status
			status := "enriched"
			if enrichedProp.YearBuilt == 0 && needsEnrichment {
				status = "nodata"
				atomic.AddInt32(&noData, 1)
			} else {
				atomic.AddInt32(&enriched, 1)
			}

			orderedResults <- orderedResult{
				index:    idx,
				property: &enrichedProp,
				status:   status,
			}
		}(i)
	}

	// Close orderedResults when all goroutines complete
	go func() {
		wg.Wait()
		close(orderedResults)
	}()

	// Collect and send results in order
	resultBuffer := make(map[int]*orderedResult)
	nextIndex := 0

	for result := range orderedResults {
		resultBuffer[result.index] = &result

		// Send any consecutive results starting from nextIndex
		for {
			if r, ok := resultBuffer[nextIndex]; ok {
				atomic.AddInt32(&processed, 1)

				// Send property event
				if r.property != nil && r.status != "failed" {
					results <- StreamingResult{
						Type:     "property",
						Property: r.property,
					}
				} else if r.status == "failed" {
					results <- StreamingResult{
						Type: "failed",
					}
				} else if r.status == "nodata" {
					results <- StreamingResult{
						Type: "nodata",
					}
				}

				// Send progress
				results <- StreamingResult{
					Type: "progress",
					Progress: &ProgressUpdate{
						Processed: int(atomic.LoadInt32(&processed)),
						Total:     len(searchResult.Properties),
					},
				}

				delete(resultBuffer, nextIndex)
				nextIndex++
			} else {
				break
			}
		}
	}

	o.logger.Info("streaming search completed",
		"location", fmt.Sprintf("%s, %s", params.City, params.State),
		"duration", time.Since(startTime),
		"total", len(searchResult.Properties),
		"enriched", enriched,
		"failed", failed,
		"noData", noData,
	)

	return nil
}

// getCachedEnrichedProperty retrieves a cached property if it exists and is enriched
func (o *Orchestrator) getCachedEnrichedProperty(ctx context.Context, providerName, propertyID string) (*providers.Property, bool) {
	if propertyID == "" {
		return nil, false
	}

	// Try PropertyCache first (size-based FIFO, ADR-061)
	if o.propertyCache != nil {
		prop, err := o.propertyCache.Get(ctx, providerName, propertyID)
		if err == nil && prop != nil {
			// If property is in cache, it's already enriched (Rule 2: all properties are
			// enriched via Property API before caching)
			return prop, true
		}
		return nil, false
	}

	// Fallback to HybridCache
	if o.cache == nil {
		return nil, false
	}

	cacheKey := o.buildPropertyReadCacheKey(providerName, propertyID)
	cached, err := o.cache.Get(ctx, "", cacheKey)
	if err != nil || cached == nil {
		return nil, false
	}

	var prop providers.Property
	if err := json.Unmarshal(cached, &prop); err != nil {
		return nil, false
	}

	// If property is in cache, it's already enriched (Rule 2: all properties are
	// enriched via Property API before caching)
	return &prop, true
}

// applyEnrichedData applies cached enrichment data to a property
func (o *Orchestrator) applyEnrichedData(prop *providers.Property, cached *providers.Property) {
	if cached.YearBuilt > 0 && prop.YearBuilt == 0 {
		prop.YearBuilt = cached.YearBuilt
	}
	if cached.Sqft > 0 && prop.Sqft == 0 {
		prop.Sqft = cached.Sqft
	}
	if cached.LotSize > 0 && prop.LotSize == 0 {
		prop.LotSize = cached.LotSize
	}
	if cached.EstimatedRent > 0 && prop.EstimatedRent == 0 {
		prop.EstimatedRent = cached.EstimatedRent
	}
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

// buildPropertyReadCacheKey creates a cache key for a single property read.
func (o *Orchestrator) buildPropertyReadCacheKey(providerName, propertyID string) string {
	if providerName == "" {
		providerName = "any"
	}
	return propertyReadCacheKeyPrefix + normalizeString(providerName) + ":" + propertyID
}

// cachePropertyRead stores a single property read result.
// Uses PropertyCache (size-based FIFO, ADR-061) if available, otherwise falls back to HybridCache with TTL.
func (o *Orchestrator) cachePropertyRead(ctx context.Context, providerName string, property *providers.Property) {
	if property == nil || property.ID == "" {
		return
	}

	if providerName == "" && property.ProviderName != "" {
		providerName = property.ProviderName
	}

	// Prefer PropertyCache (size-based) if available
	if o.propertyCache != nil {
		if err := o.propertyCache.Set(ctx, providerName, property.ID, property); err != nil {
			o.logger.Warn("failed to cache property read (PropertyCache)", "error", err)
		}
		return
	}

	// Fallback to HybridCache with TTL
	if o.cache != nil {
		cacheKey := o.buildPropertyReadCacheKey(providerName, property.ID)
		if err := o.cache.Set(ctx, "", cacheKey, "property_read", property, propertyReadCacheTTL); err != nil {
			o.logger.Warn("failed to cache property read (HybridCache)", "error", err)
		}
	}
}

// cachePropertyReads stores multiple property reads.
// Uses PropertyCache (size-based FIFO, ADR-061) if available for batch efficiency.
func (o *Orchestrator) cachePropertyReads(ctx context.Context, providerName string, properties []providers.Property) {
	if len(properties) == 0 {
		return
	}

	// Prefer PropertyCache batch method if available
	if o.propertyCache != nil {
		if err := o.propertyCache.SetBatch(ctx, providerName, properties); err != nil {
			o.logger.Warn("failed to batch cache property reads", "error", err, "count", len(properties))
		}
		return
	}

	// Fallback to HybridCache (individual calls)
	if o.cache != nil {
		for i := range properties {
			o.cachePropertyRead(ctx, providerName, &properties[i])
		}
	}
}

// GetProperty retrieves a single property by ID
func (o *Orchestrator) GetProperty(ctx context.Context, providerName, propertyID string) (*providers.Property, error) {
	// Try PropertyCache first (size-based FIFO, ADR-061)
	if o.propertyCache != nil {
		prop, err := o.propertyCache.Get(ctx, providerName, propertyID)
		if err == nil && prop != nil {
			return prop, nil
		}
	} else if o.cache != nil {
		// Fallback to HybridCache
		cacheKey := o.buildPropertyReadCacheKey(providerName, propertyID)
		if cached, err := o.cache.Get(ctx, "", cacheKey); err == nil && cached != nil {
			var property providers.Property
			if err := json.Unmarshal(cached, &property); err == nil {
				return &property, nil
			}
		}
	}

	// Find the specific provider
	for _, p := range o.providers {
		if p.Name() == providerName && p.IsEnabled() {
			property, err := p.GetProperty(ctx, propertyID)
			if err == nil && property != nil {
				o.cachePropertyRead(ctx, p.Name(), property)
			}
			return property, err
		}
	}

	// If no specific provider, try all enabled providers
	for _, p := range o.getEnabledProviders() {
		property, err := p.GetProperty(ctx, propertyID)
		if err == nil {
			o.cachePropertyRead(ctx, p.Name(), property)
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

	deleted, err := o.cache.DeleteByKeyPattern(ctx, pattern)
	if err != nil {
		o.logger.Warn("failed to invalidate property cache", "pattern", pattern, "error", err)
		return err
	}

	o.logger.Info("property cache invalidated", "pattern", pattern, "deleted", deleted)
	return nil
}

// InvalidateCacheByKey clears a specific property cache entry by full cache key
func (o *Orchestrator) InvalidateCacheByKey(ctx context.Context, cacheKey string) error {
	if o.cache == nil {
		return nil
	}

	// Delete the specific key (empty userID since property cache is shared)
	if err := o.cache.Delete(ctx, "", cacheKey); err != nil {
		o.logger.Warn("failed to invalidate property cache by key", "key", cacheKey, "error", err)
		return err
	}

	o.logger.Info("property cache key invalidated", "key", cacheKey)
	return nil
}

// enrichPropertiesWithYearBuilt enriches properties missing yearBuilt by calling
// the HasData Property API. The Listing API no longer returns yearBuilt as of Jan 2026.
// Uses parallel workers for performance (configured via EnrichmentConcurrency).
func (o *Orchestrator) enrichPropertiesWithYearBuilt(ctx context.Context, properties []providers.Property) []providers.Property {
	// Find properties missing yearBuilt that have Zillow URLs
	var propertiesToEnrich []int
	var skippedNoURL, skippedNonZillow, skippedHasYearBuilt int
	for i, p := range properties {
		if p.YearBuilt > 0 {
			skippedHasYearBuilt++
			continue
		}
		if p.ListingURL == "" {
			skippedNoURL++
			continue
		}
		if !strings.Contains(p.ListingURL, "zillow.com") {
			skippedNonZillow++
			continue
		}
		propertiesToEnrich = append(propertiesToEnrich, i)
	}

	if len(propertiesToEnrich) == 0 {
		o.logger.Debug("no properties need enrichment",
			"total", len(properties),
			"skippedHasYearBuilt", skippedHasYearBuilt,
			"skippedNoURL", skippedNoURL,
			"skippedNonZillow", skippedNonZillow,
		)
		return properties
	}

	o.logger.Info("enriching properties via Property API",
		"toEnrich", len(propertiesToEnrich),
		"total", len(properties),
		"concurrency", o.enrichmentConcurrency,
		"skippedHasYearBuilt", skippedHasYearBuilt,
		"skippedNoURL", skippedNoURL,
		"skippedNonZillow", skippedNonZillow,
	)

	// Find HasData provider
	var hasDataProvider *providers.HasDataProvider
	for _, p := range o.providers {
		if hp, ok := p.(*providers.HasDataProvider); ok && hp.IsEnabled() {
			hasDataProvider = hp
			break
		}
	}

	if hasDataProvider == nil {
		o.logger.Warn("HasData provider not available for yearBuilt enrichment")
		return properties
	}

	// Create enrichment context with timeout
	enrichCtx, cancel := context.WithTimeout(ctx, enrichmentTimeout)
	defer cancel()

	// Use semaphore pattern for concurrency control
	sem := make(chan struct{}, o.enrichmentConcurrency)
	var wg sync.WaitGroup

	// Counters for detailed stats
	var enrichedCount int32    // Properties that got at least one field enriched
	var apiErrorCount int32    // API call failures
	var noDataCount int32      // API succeeded but returned no useful data
	var timedOutCount int32    // Couldn't acquire semaphore before timeout
	var yearBuiltCount int32   // YearBuilt field enriched
	var sqftCount int32        // Sqft field enriched
	var lotSizeCount int32     // LotSize field enriched
	var rentEstCount int32     // RentEstimate field enriched

	// Process properties in parallel
	for _, idx := range propertiesToEnrich {
		wg.Add(1)
		go func(propertyIdx int) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-enrichCtx.Done():
				atomic.AddInt32(&timedOutCount, 1)
				return
			}

			prop := &properties[propertyIdx]

			propData, err := hasDataProvider.GetPropertyByURL(enrichCtx, prop.ListingURL)
			if err != nil {
				atomic.AddInt32(&apiErrorCount, 1)
				o.logger.Debug("enrichment API error",
					"address", prop.Address,
					"error", err,
				)
				return
			}

			// Enrich multiple fields like www_v1
			enriched := false

			if propData.YearBuilt > 0 {
				prop.YearBuilt = propData.YearBuilt
				atomic.AddInt32(&yearBuiltCount, 1)
				enriched = true
			}

			// SquareFeet - try LivingArea first, then Area
			if prop.Sqft == 0 {
				if propData.LivingArea > 0 {
					prop.Sqft = propData.LivingArea
					atomic.AddInt32(&sqftCount, 1)
					enriched = true
				} else if propData.Area > 0 {
					prop.Sqft = propData.Area
					atomic.AddInt32(&sqftCount, 1)
					enriched = true
				}
			}

			// LotSize
			if prop.LotSize == 0 && propData.LotSize > 0 {
				prop.LotSize = propData.LotSize
				atomic.AddInt32(&lotSizeCount, 1)
				enriched = true
			}

			// RentEstimate
			if prop.EstimatedRent == 0 && propData.RentZestimate > 0 {
				prop.EstimatedRent = propData.RentZestimate
				atomic.AddInt32(&rentEstCount, 1)
				enriched = true
			}

			if enriched {
				atomic.AddInt32(&enrichedCount, 1)
			} else {
				// API succeeded but no useful data returned
				atomic.AddInt32(&noDataCount, 1)
				o.logger.Debug("enrichment returned no data",
					"address", prop.Address,
					"apiYearBuilt", propData.YearBuilt,
					"apiLivingArea", propData.LivingArea,
					"apiLotSize", propData.LotSize,
					"apiRentZestimate", propData.RentZestimate,
					"propAlreadyHadSqft", prop.Sqft > 0,
					"propAlreadyHadLotSize", prop.LotSize > 0,
					"propAlreadyHadRent", prop.EstimatedRent > 0,
				)
			}
		}(idx)
	}

	// Wait for all enrichments to complete or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All enrichments completed
	case <-enrichCtx.Done():
		o.logger.Warn("yearBuilt enrichment timed out",
			"timeout", enrichmentTimeout,
			"attempted", len(propertiesToEnrich),
		)
	}

	o.logger.Info("property enrichment completed",
		"enriched", enrichedCount,
		"attempted", len(propertiesToEnrich),
		"apiErrors", apiErrorCount,
		"noDataReturned", noDataCount,
		"timedOut", timedOutCount,
		"fieldsEnriched", map[string]int32{
			"yearBuilt":    yearBuiltCount,
			"sqft":         sqftCount,
			"lotSize":      lotSizeCount,
			"rentEstimate": rentEstCount,
		},
	)

	return properties
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
