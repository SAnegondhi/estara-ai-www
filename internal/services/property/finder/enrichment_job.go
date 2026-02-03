package finder

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/property/providers"
	"github.com/google/uuid"
)

const (
	// Job cache settings
	enrichmentJobKeyPrefix = "enrich_job:"
	enrichmentJobTTL       = 10 * time.Minute

	// Note: propertyReadCacheKeyPrefix and propertyReadCacheTTL are defined in orchestrator.go

	// Status values
	EnrichmentStatusPending    = "PENDING"
	EnrichmentStatusProcessing = "PROCESSING"
	EnrichmentStatusCompleted  = "COMPLETED"
	EnrichmentStatusFailed     = "FAILED"
)

// EnrichmentJobStatus represents the current state of an enrichment job
type EnrichmentJobStatus struct {
	JobID       string              `json:"jobId"`
	Status      string              `json:"status"`
	Total       int                 `json:"total"`       // Total properties to enrich
	Completed   int                 `json:"completed"`   // Properties processed (success or fail)
	Enriched    int                 `json:"enriched"`    // Properties actually enriched with data
	Failed      int                 `json:"failed"`      // Properties that failed API call
	NoData      int                 `json:"noData"`      // Properties with no data returned
	StartedAt   time.Time           `json:"startedAt"`
	CompletedAt time.Time           `json:"completedAt,omitempty"`
	Error       string              `json:"error,omitempty"`
	Updates     []EnrichmentUpdate  `json:"updates,omitempty"` // All successful updates (for late subscribers)
}

// EnrichmentUpdate represents an SSE update for a single property
type EnrichmentUpdate struct {
	PropertyID    string `json:"propertyId"`
	PropertyIndex int    `json:"propertyIndex"`
	Success       bool   `json:"success"`
	YearBuilt     int    `json:"yearBuilt,omitempty"`
	Sqft          int    `json:"sqft,omitempty"`
	LotSize       int    `json:"lotSize,omitempty"`
	RentEstimate  int    `json:"rentEstimate,omitempty"`
	Error         string `json:"error,omitempty"`
}

// EnrichmentJobManager manages background enrichment jobs
type EnrichmentJobManager struct {
	cache         *cache.HybridCache   // Used for job status caching
	propertyCache *cache.PropertyCache // Used for property read caching (size-based FIFO, ADR-061)
	providers     []providers.Provider
	concurrency   int
	logger        *slog.Logger

	// Track active jobs for SSE subscribers
	mu          sync.RWMutex
	subscribers map[string][]chan EnrichmentUpdate
}

// NewEnrichmentJobManager creates a new job manager
func NewEnrichmentJobManager(hybridCache *cache.HybridCache, propertyCache *cache.PropertyCache, providerList []providers.Provider, concurrency int) *EnrichmentJobManager {
	if concurrency <= 0 {
		concurrency = defaultEnrichmentConcurrency
	}

	return &EnrichmentJobManager{
		cache:         hybridCache,
		propertyCache: propertyCache,
		providers:     providerList,
		concurrency:   concurrency,
		logger:        slog.Default().With("component", "enrichment_job_manager"),
		subscribers:   make(map[string][]chan EnrichmentUpdate),
	}
}

// buildPropertyCacheKey creates a cache key for property read data
func (m *EnrichmentJobManager) buildPropertyCacheKey(providerName, propertyID string) string {
	if providerName == "" {
		providerName = "any"
	}
	return propertyReadCacheKeyPrefix + strings.ToLower(strings.ReplaceAll(providerName, " ", "_")) + ":" + propertyID
}

// getCachedProperty retrieves a property from cache if it exists and is enriched
func (m *EnrichmentJobManager) getCachedProperty(ctx context.Context, providerName, propertyID string) (*providers.Property, bool) {
	if propertyID == "" {
		return nil, false
	}

	// Try PropertyCache first (size-based FIFO, ADR-061)
	if m.propertyCache != nil {
		prop, err := m.propertyCache.Get(ctx, providerName, propertyID)
		if err == nil && prop != nil {
			// Check if property is enriched (has yearBuilt or other enrichment data)
			isEnriched := prop.YearBuilt > 0 || prop.EstimatedRent > 0 || prop.LotSize > 0
			return prop, isEnriched
		}
		return nil, false
	}

	// Fallback to HybridCache
	if m.cache == nil {
		return nil, false
	}

	cacheKey := m.buildPropertyCacheKey(providerName, propertyID)
	cached, err := m.cache.Get(ctx, "", cacheKey)
	if err != nil || cached == nil {
		return nil, false
	}

	var prop providers.Property
	if err := json.Unmarshal(cached, &prop); err != nil {
		return nil, false
	}

	// Check if property is enriched (has yearBuilt or other enrichment data)
	isEnriched := prop.YearBuilt > 0 || prop.EstimatedRent > 0 || prop.LotSize > 0
	return &prop, isEnriched
}

// cacheProperty stores an enriched property in cache
func (m *EnrichmentJobManager) cacheProperty(ctx context.Context, prop *providers.Property) {
	if prop == nil || prop.ID == "" {
		return
	}

	providerName := prop.ProviderName
	if providerName == "" {
		providerName = "hasdata"
	}

	// Prefer PropertyCache (size-based FIFO, ADR-061) if available
	if m.propertyCache != nil {
		if err := m.propertyCache.Set(ctx, providerName, prop.ID, prop); err != nil {
			m.logger.Warn("failed to cache enriched property (PropertyCache)", "propertyId", prop.ID, "error", err)
		}
		return
	}

	// Fallback to HybridCache with TTL
	if m.cache != nil {
		cacheKey := m.buildPropertyCacheKey(providerName, prop.ID)
		if err := m.cache.Set(ctx, "", cacheKey, "property_read", prop, propertyReadCacheTTL); err != nil {
			m.logger.Warn("failed to cache enriched property (HybridCache)", "propertyId", prop.ID, "error", err)
		}
	}
}

// StartEnrichmentJob starts a background enrichment job and returns the job ID
func (m *EnrichmentJobManager) StartEnrichmentJob(ctx context.Context, properties []providers.Property) (string, error) {
	jobID := uuid.New().String()

	// Find properties that need enrichment
	var toEnrich []int
	var skippedHasYearBuilt, skippedNoURL, skippedNotZillow int
	for i, p := range properties {
		if p.YearBuilt > 0 {
			skippedHasYearBuilt++
			continue
		}
		if p.ListingURL == "" {
			skippedNoURL++
			m.logger.Debug("skipping property - no URL",
				"propertyId", p.ID,
				"address", p.Address,
			)
			continue
		}
		if !strings.Contains(p.ListingURL, "zillow.com") {
			skippedNotZillow++
			m.logger.Debug("skipping property - not zillow URL",
				"propertyId", p.ID,
				"listingURL", p.ListingURL,
			)
			continue
		}
		toEnrich = append(toEnrich, i)
	}

	m.logger.Info("enrichment job property analysis",
		"jobId", jobID,
		"total", len(properties),
		"toEnrich", len(toEnrich),
		"skippedHasYearBuilt", skippedHasYearBuilt,
		"skippedNoURL", skippedNoURL,
		"skippedNotZillow", skippedNotZillow,
	)

	if len(toEnrich) == 0 {
		// No enrichment needed, return completed status
		status := &EnrichmentJobStatus{
			JobID:       jobID,
			Status:      EnrichmentStatusCompleted,
			Total:       0,
			Completed:   0,
			StartedAt:   time.Now(),
			CompletedAt: time.Now(),
		}
		m.saveJobStatus(ctx, status)
		return jobID, nil
	}

	// Initialize job status
	status := &EnrichmentJobStatus{
		JobID:     jobID,
		Status:    EnrichmentStatusPending,
		Total:     len(toEnrich),
		StartedAt: time.Now(),
	}
	if err := m.saveJobStatus(ctx, status); err != nil {
		return "", fmt.Errorf("failed to save job status: %w", err)
	}

	// Start background enrichment
	go m.runEnrichment(jobID, properties, toEnrich)

	m.logger.Info("started enrichment job",
		"jobId", jobID,
		"toEnrich", len(toEnrich),
		"total", len(properties),
	)

	return jobID, nil
}

// GetJobStatus retrieves the current status of an enrichment job
func (m *EnrichmentJobManager) GetJobStatus(ctx context.Context, jobID string) (*EnrichmentJobStatus, error) {
	cacheKey := enrichmentJobKeyPrefix + jobID
	cached, err := m.cache.Get(ctx, "", cacheKey)
	if err != nil {
		m.logger.Warn("failed to get job status from cache",
			"jobId", jobID,
			"cacheKey", cacheKey,
			"error", err,
		)
		return nil, fmt.Errorf("job not found: %s", jobID)
	}

	var status EnrichmentJobStatus
	if err := json.Unmarshal(cached, &status); err != nil {
		return nil, fmt.Errorf("failed to parse job status: %w", err)
	}

	return &status, nil
}

// Subscribe returns a channel for receiving enrichment updates
func (m *EnrichmentJobManager) Subscribe(jobID string) (<-chan EnrichmentUpdate, func()) {
	ch := make(chan EnrichmentUpdate, 100)

	m.mu.Lock()
	m.subscribers[jobID] = append(m.subscribers[jobID], ch)
	m.mu.Unlock()

	// Return unsubscribe function
	unsubscribe := func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		subs := m.subscribers[jobID]
		for i, sub := range subs {
			if sub == ch {
				m.subscribers[jobID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		close(ch)
	}

	return ch, unsubscribe
}

// runEnrichment performs the actual enrichment in background
func (m *EnrichmentJobManager) runEnrichment(jobID string, properties []providers.Property, indicesToEnrich []int) {
	ctx := context.Background()

	// Update status to processing
	status := &EnrichmentJobStatus{
		JobID:     jobID,
		Status:    EnrichmentStatusProcessing,
		Total:     len(indicesToEnrich),
		StartedAt: time.Now(),
	}
	m.saveJobStatus(ctx, status)

	// Find HasData provider
	var hasDataProvider *providers.HasDataProvider
	for _, p := range m.providers {
		if hp, ok := p.(*providers.HasDataProvider); ok && hp.IsEnabled() {
			hasDataProvider = hp
			break
		}
	}

	if hasDataProvider == nil {
		status.Status = EnrichmentStatusFailed
		status.Error = "HasData provider not available"
		status.CompletedAt = time.Now()
		m.saveJobStatus(ctx, status)
		m.notifyComplete(jobID)
		return
	}

	// Create enrichment context with timeout
	enrichCtx, cancel := context.WithTimeout(ctx, enrichmentTimeout)
	defer cancel()

	// Use semaphore for concurrency control
	sem := make(chan struct{}, m.concurrency)
	var wg sync.WaitGroup

	var completed, enriched, failed, noData int32

	// Track all successful updates for late subscribers
	var updatesMu sync.Mutex
	var allUpdates []EnrichmentUpdate

	for _, idx := range indicesToEnrich {
		wg.Add(1)
		go func(propertyIdx int) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-enrichCtx.Done():
				atomic.AddInt32(&failed, 1)
				return
			}

			prop := &properties[propertyIdx]
			update := EnrichmentUpdate{
				PropertyID:    prop.ID,
				PropertyIndex: propertyIdx,
			}

			// Check cache first for already-enriched property data
			if cachedProp, isEnriched := m.getCachedProperty(enrichCtx, prop.ProviderName, prop.ID); isEnriched {
				// Use cached enriched data - no API call needed
				wasEnriched := false

				if cachedProp.YearBuilt > 0 && prop.YearBuilt == 0 {
					prop.YearBuilt = cachedProp.YearBuilt
					update.YearBuilt = cachedProp.YearBuilt
					wasEnriched = true
				}

				if prop.Sqft == 0 && cachedProp.Sqft > 0 {
					prop.Sqft = cachedProp.Sqft
					update.Sqft = cachedProp.Sqft
					wasEnriched = true
				}

				if prop.LotSize == 0 && cachedProp.LotSize > 0 {
					prop.LotSize = cachedProp.LotSize
					update.LotSize = cachedProp.LotSize
					wasEnriched = true
				}

				if prop.EstimatedRent == 0 && cachedProp.EstimatedRent > 0 {
					prop.EstimatedRent = cachedProp.EstimatedRent
					update.RentEstimate = cachedProp.EstimatedRent
					wasEnriched = true
				}

				if wasEnriched {
					atomic.AddInt32(&enriched, 1)
					update.Success = true
					m.logger.Debug("used cached enriched property", "propertyId", prop.ID)
					// Track for late subscribers
					updatesMu.Lock()
					allUpdates = append(allUpdates, update)
					updatesMu.Unlock()
				} else {
					atomic.AddInt32(&noData, 1)
				}

				m.broadcast(jobID, update)
				atomic.AddInt32(&completed, 1)
				return
			}

			// Not in cache or not enriched - call API
			propData, err := hasDataProvider.GetPropertyByURL(enrichCtx, prop.ListingURL)
			if err != nil {
				atomic.AddInt32(&failed, 1)
				update.Error = err.Error()
				m.broadcast(jobID, update)
				atomic.AddInt32(&completed, 1)
				return
			}

			// Enrich fields from API response
			wasEnriched := false

			if propData.YearBuilt > 0 {
				prop.YearBuilt = propData.YearBuilt
				update.YearBuilt = propData.YearBuilt
				wasEnriched = true
			}

			if prop.Sqft == 0 {
				if propData.LivingArea > 0 {
					prop.Sqft = propData.LivingArea
					update.Sqft = propData.LivingArea
					wasEnriched = true
				} else if propData.Area > 0 {
					prop.Sqft = propData.Area
					update.Sqft = propData.Area
					wasEnriched = true
				}
			}

			if prop.LotSize == 0 && propData.LotSize > 0 {
				prop.LotSize = propData.LotSize
				update.LotSize = propData.LotSize
				wasEnriched = true
			}

			if prop.EstimatedRent == 0 && propData.RentZestimate > 0 {
				prop.EstimatedRent = propData.RentZestimate
				update.RentEstimate = propData.RentZestimate
				wasEnriched = true
			}

			// Cache the enriched property for future use
			if wasEnriched {
				atomic.AddInt32(&enriched, 1)
				update.Success = true
				m.cacheProperty(enrichCtx, prop)
				// Track for late subscribers
				updatesMu.Lock()
				allUpdates = append(allUpdates, update)
				updatesMu.Unlock()
			} else {
				atomic.AddInt32(&noData, 1)
			}

			m.broadcast(jobID, update)
			atomic.AddInt32(&completed, 1)

			// Update status periodically
			if atomic.LoadInt32(&completed)%5 == 0 {
				status := &EnrichmentJobStatus{
					JobID:     jobID,
					Status:    EnrichmentStatusProcessing,
					Total:     len(indicesToEnrich),
					Completed: int(atomic.LoadInt32(&completed)),
					Enriched:  int(atomic.LoadInt32(&enriched)),
					Failed:    int(atomic.LoadInt32(&failed)),
					NoData:    int(atomic.LoadInt32(&noData)),
					StartedAt: time.Now(),
				}
				m.saveJobStatus(ctx, status)
			}
		}(idx)
	}

	// Wait for completion
	wg.Wait()

	// Final status update with all successful updates for late subscribers
	finalStatus := &EnrichmentJobStatus{
		JobID:       jobID,
		Status:      EnrichmentStatusCompleted,
		Total:       len(indicesToEnrich),
		Completed:   int(completed),
		Enriched:    int(enriched),
		Failed:      int(failed),
		NoData:      int(noData),
		StartedAt:   status.StartedAt,
		CompletedAt: time.Now(),
		Updates:     allUpdates, // Include all successful updates
	}
	m.saveJobStatus(ctx, finalStatus)
	m.notifyComplete(jobID)

	m.logger.Info("enrichment job completed",
		"jobId", jobID,
		"total", len(indicesToEnrich),
		"enriched", enriched,
		"failed", failed,
		"noData", noData,
	)
}

// broadcast sends an update to all subscribers for a job
func (m *EnrichmentJobManager) broadcast(jobID string, update EnrichmentUpdate) {
	m.mu.RLock()
	subs := m.subscribers[jobID]
	m.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- update:
		default:
			// Channel full, skip
		}
	}
}

// notifyComplete notifies subscribers that the job is done
func (m *EnrichmentJobManager) notifyComplete(jobID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Close all subscriber channels for this job
	for _, ch := range m.subscribers[jobID] {
		close(ch)
	}
	delete(m.subscribers, jobID)
}

// saveJobStatus persists job status to cache
func (m *EnrichmentJobManager) saveJobStatus(ctx context.Context, status *EnrichmentJobStatus) error {
	cacheKey := enrichmentJobKeyPrefix + status.JobID
	// Pass status directly - cache.Set will marshal it
	// (passing pre-marshaled bytes would cause double-encoding)
	return m.cache.Set(ctx, "", cacheKey, "enrichment_job", status, enrichmentJobTTL)
}
