// Package cache provides caching services for the application.
// PropertyCache implements a size-based FIFO cache for individual property reads.
// See ADR-061 for design rationale.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/property/providers"
)

const (
	// Default cache sizes
	defaultL1MaxSize  = 50000  // Redis max entries
	defaultL2MaxSize  = 500000 // Postgres max entries
	defaultEvictBatch = 1000   // Entries to evict when full

	// Cache key prefix
	propertyCacheKeyPrefix = "prop_cache:"
)

// PropertyCacheConfig holds configuration for the property cache
type PropertyCacheConfig struct {
	L1MaxSize  int // Redis max entries (default: 50,000)
	L2MaxSize  int // Postgres max entries (default: 500,000)
	EvictBatch int // Entries to evict when L2 is full (default: 1,000)
}

// PropertyCache implements a two-tier size-based FIFO cache for property reads.
// L1 (Redis): Fast, volatile, relies on Redis LRU eviction
// L2 (Postgres): Persistent, size-limited with explicit FIFO eviction
type PropertyCache struct {
	redis      *redisClient.Client
	queries    *queries.Queries
	logger     *slog.Logger
	l1MaxSize  int
	l2MaxSize  int
	evictBatch int

	// Mutex for eviction coordination
	evictMu sync.Mutex
}

// PropertyCacheStats holds cache statistics for monitoring
type PropertyCacheStats struct {
	TotalEntries  int64     `json:"totalEntries"`
	OldestEntry   time.Time `json:"oldestEntry,omitempty"`
	NewestEntry   time.Time `json:"newestEntry,omitempty"`
	TotalAccesses int64     `json:"totalAccesses"`
}

// NewPropertyCache creates a new PropertyCache instance
func NewPropertyCache(redis *redisClient.Client, q *queries.Queries, logger *slog.Logger, config PropertyCacheConfig) *PropertyCache {
	if logger == nil {
		logger = slog.Default()
	}

	// Apply defaults
	l1MaxSize := config.L1MaxSize
	if l1MaxSize <= 0 {
		l1MaxSize = defaultL1MaxSize
	}

	l2MaxSize := config.L2MaxSize
	if l2MaxSize <= 0 {
		l2MaxSize = defaultL2MaxSize
	}

	evictBatch := config.EvictBatch
	if evictBatch <= 0 {
		evictBatch = defaultEvictBatch
	}

	return &PropertyCache{
		redis:      redis,
		queries:    q,
		logger:     logger.With("component", "property_cache"),
		l1MaxSize:  l1MaxSize,
		l2MaxSize:  l2MaxSize,
		evictBatch: evictBatch,
	}
}

// buildCacheKey creates a cache key for a property
func (c *PropertyCache) buildCacheKey(provider, propertyID string) string {
	// Normalize provider name
	provider = strings.ToLower(strings.ReplaceAll(provider, " ", "_"))
	if provider == "" {
		provider = "unknown"
	}
	return propertyCacheKeyPrefix + provider + ":" + propertyID
}

// Get retrieves a property from the cache
// Returns nil, nil if not found (cache miss)
func (c *PropertyCache) Get(ctx context.Context, provider, propertyID string) (*providers.Property, error) {
	if propertyID == "" {
		return nil, nil
	}

	cacheKey := c.buildCacheKey(provider, propertyID)

	// L1: Try Redis first
	if c.redis != nil {
		data, err := c.redis.GetJSON(ctx, cacheKey)
		if err != nil {
			c.logger.Debug("L1 cache error", "error", err, "key", cacheKey)
		} else if data != nil {
			var prop providers.Property
			if err := json.Unmarshal(data, &prop); err == nil {
				c.logger.Debug("L1 cache hit", "key", cacheKey)
				return &prop, nil
			}
		}
	}

	// L2: Try Postgres
	if c.queries != nil {
		record, err := c.queries.GetPropertyFromCache(ctx, cacheKey)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.logger.Debug("L2 cache miss", "key", cacheKey)
				return nil, nil // Cache miss
			}
			return nil, fmt.Errorf("L2 cache error: %w", err)
		}

		// Parse property from JSONB
		var prop providers.Property
		if err := json.Unmarshal(record.Content, &prop); err != nil {
			c.logger.Warn("failed to unmarshal cached property", "error", err, "key", cacheKey)
			return nil, nil
		}

		c.logger.Debug("L2 cache hit", "key", cacheKey)

		// Warm L1 cache (no TTL - relies on Redis LRU eviction)
		if c.redis != nil {
			data, _ := json.Marshal(prop)
			if err := c.redis.Set(ctx, cacheKey, data, 0).Err(); err != nil {
				c.logger.Debug("failed to warm L1 cache", "error", err)
			}
		}

		// Update access count asynchronously
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = c.queries.UpdatePropertyCacheAccess(bgCtx, cacheKey)
		}()

		return &prop, nil
	}

	return nil, nil // No cache configured
}

// Set stores a property in the cache
func (c *PropertyCache) Set(ctx context.Context, provider, propertyID string, property *providers.Property) error {
	if property == nil || propertyID == "" {
		return nil
	}

	cacheKey := c.buildCacheKey(provider, propertyID)
	data, err := json.Marshal(property)
	if err != nil {
		return fmt.Errorf("failed to marshal property: %w", err)
	}

	// L1: Store in Redis (no TTL - relies on Redis LRU eviction)
	if c.redis != nil {
		if err := c.redis.Set(ctx, cacheKey, data, 0).Err(); err != nil {
			c.logger.Warn("failed to set L1 cache", "error", err, "key", cacheKey)
		}
	}

	// L2: Store in Postgres with size-based eviction
	if c.queries != nil {
		// Check if eviction is needed
		if err := c.maybeEvictL2(ctx); err != nil {
			c.logger.Warn("L2 eviction failed", "error", err)
		}

		// Upsert the property
		err := c.queries.UpsertPropertyCache(ctx, queries.UpsertPropertyCacheParams{
			CacheKey:   cacheKey,
			Provider:   provider,
			PropertyID: propertyID,
			Content:    data,
		})
		if err != nil {
			c.logger.Warn("failed to set L2 cache", "error", err, "key", cacheKey)
			return fmt.Errorf("failed to set L2 cache: %w", err)
		}
	}

	c.logger.Debug("property cached", "key", cacheKey, "provider", provider)
	return nil
}

// SetBatch stores multiple properties in the cache
func (c *PropertyCache) SetBatch(ctx context.Context, provider string, properties []providers.Property) error {
	if len(properties) == 0 {
		return nil
	}

	// Check eviction once for the batch
	if c.queries != nil {
		if err := c.maybeEvictL2(ctx); err != nil {
			c.logger.Warn("L2 eviction failed", "error", err)
		}
	}

	var errs []error
	for i := range properties {
		prop := &properties[i]
		if prop.ID == "" {
			continue
		}
		if err := c.Set(ctx, provider, prop.ID, prop); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to cache %d/%d properties", len(errs), len(properties))
	}

	c.logger.Debug("batch cached", "count", len(properties), "provider", provider)
	return nil
}

// maybeEvictL2 checks if L2 cache needs eviction and evicts if necessary
func (c *PropertyCache) maybeEvictL2(ctx context.Context) error {
	if c.queries == nil {
		return nil
	}

	// Use mutex to prevent concurrent evictions
	c.evictMu.Lock()
	defer c.evictMu.Unlock()

	count, err := c.queries.CountPropertyCache(ctx)
	if err != nil {
		return fmt.Errorf("failed to count cache entries: %w", err)
	}

	if count >= int64(c.l2MaxSize) {
		// Evict oldest entries
		deleted, err := c.queries.DeleteOldestPropertyCache(ctx, int32(c.evictBatch))
		if err != nil {
			return fmt.Errorf("failed to evict old entries: %w", err)
		}
		c.logger.Info("L2 cache eviction",
			"deleted", deleted,
			"count_before", count,
			"max_size", c.l2MaxSize,
		)
	}

	return nil
}

// Delete removes a property from the cache
func (c *PropertyCache) Delete(ctx context.Context, provider, propertyID string) error {
	cacheKey := c.buildCacheKey(provider, propertyID)

	// L1: Delete from Redis
	if c.redis != nil {
		if err := c.redis.Delete(ctx, cacheKey); err != nil {
			c.logger.Warn("failed to delete from L1", "error", err, "key", cacheKey)
		}
	}

	// L2: Delete from Postgres
	if c.queries != nil {
		if err := c.queries.DeletePropertyFromCache(ctx, cacheKey); err != nil {
			c.logger.Warn("failed to delete from L2", "error", err, "key", cacheKey)
			return err
		}
	}

	return nil
}

// DeleteByProvider removes all cached properties for a provider
func (c *PropertyCache) DeleteByProvider(ctx context.Context, provider string) (int64, error) {
	var deleted int64

	// L1: Delete pattern from Redis
	if c.redis != nil {
		pattern := propertyCacheKeyPrefix + strings.ToLower(provider) + ":*"
		count, err := c.redis.DeletePattern(ctx, pattern)
		if err != nil {
			c.logger.Warn("failed to delete L1 pattern", "error", err, "pattern", pattern)
		} else {
			deleted += count
		}
	}

	// L2: Delete from Postgres
	if c.queries != nil {
		count, err := c.queries.DeletePropertyCacheByProvider(ctx, provider)
		if err != nil {
			return deleted, fmt.Errorf("failed to delete from L2: %w", err)
		}
		deleted += count
	}

	c.logger.Info("deleted cache entries by provider", "provider", provider, "deleted", deleted)
	return deleted, nil
}

// Stats returns cache statistics
func (c *PropertyCache) Stats(ctx context.Context) (*PropertyCacheStats, error) {
	if c.queries == nil {
		return &PropertyCacheStats{}, nil
	}

	row, err := c.queries.GetPropertyCacheStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache stats: %w", err)
	}

	stats := &PropertyCacheStats{
		TotalEntries:  row.TotalEntries,
		TotalAccesses: row.TotalAccesses,
	}

	// Parse timestamps if available
	if oldest, ok := row.OldestEntry.(time.Time); ok {
		stats.OldestEntry = oldest
	}
	if newest, ok := row.NewestEntry.(time.Time); ok {
		stats.NewestEntry = newest
	}

	return stats, nil
}
