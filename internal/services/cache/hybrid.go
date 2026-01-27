package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
)

var (
	ErrCacheMiss = errors.New("cache miss")
	ErrNotFound  = errors.New("not found")
)

// CacheEntry represents a cached item with metadata
type CacheEntry struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Type      string          `json:"type"`
	ExpiresAt time.Time       `json:"expiresAt"`
	CreatedAt time.Time       `json:"createdAt"`
}

// HybridCache provides two-layer caching (Redis L1, PostgreSQL L2)
type HybridCache struct {
	redis   *redisClient.Client
	db      *postgres.Pool
	queries *queries.Queries
	logger  *slog.Logger
}

// NewHybridCache creates a new hybrid cache
func NewHybridCache(redis *redisClient.Client, db *postgres.Pool) *HybridCache {
	return &HybridCache{
		redis:   redis,
		db:      db,
		queries: queries.New(db),
		logger:  slog.Default().With("component", "hybrid_cache"),
	}
}

// Get retrieves a value from cache (L1 first, then L2)
func (c *HybridCache) Get(ctx context.Context, userID, key string) ([]byte, error) {
	fullKey := c.buildKey(userID, key)

	// L1: Try Redis first
	if c.redis != nil {
		data, err := c.redis.GetJSON(ctx, fullKey)
		if err == nil && data != nil {
			c.logger.Debug("cache hit (L1)", "key", key)
			return data, nil
		}
	}

	// L2: Try PostgreSQL using generated queries
	record, err := c.queries.GetCacheByUserAndKey(ctx, queries.GetCacheByUserAndKeyParams{
		UserId: userID,
		Key:    key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCacheMiss
		}
		return nil, err
	}

	c.logger.Debug("cache hit (L2)", "key", key)

	data := []byte(record.Content)

	// Warm L1 cache on L2 hit
	if c.redis != nil {
		_ = c.redis.Set(ctx, fullKey, data, time.Hour)
	}

	// Update access count
	_ = c.queries.UpdateCacheAccess(ctx, key)

	return data, nil
}

// Set stores a value in both cache layers
func (c *HybridCache) Set(ctx context.Context, userID, key, cacheType string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	fullKey := c.buildKey(userID, key)
	expiresAt := time.Now().Add(ttl)

	// L1: Store in Redis
	if c.redis != nil {
		if err := c.redis.Set(ctx, fullKey, data, ttl).Err(); err != nil {
			c.logger.Warn("failed to set L1 cache", "key", key, "error", err)
		}
	}

	// L2: Store in PostgreSQL using generated queries
	_, err = c.queries.UpsertCache(ctx, queries.UpsertCacheParams{
		ID:            uuid.New().String(),
		Key:           key,
		UserId:        userID,
		Location:      "", // Empty location for generic cache
		Feature:       cacheType,
		Content:       string(data),
		MetricsData:   nil,
		NarrativeData: nil,
		Metadata:      json.RawMessage("{}"),
		ExpiresAt:     pgtype.Timestamp{Time: expiresAt, Valid: true},
	})
	if err != nil {
		c.logger.Warn("failed to set L2 cache", "key", key, "error", err)
		// Continue even if L2 fails - L1 is still valid
	}

	c.logger.Debug("cache set", "key", key, "ttl", ttl)
	return nil
}

// Delete removes a value from both cache layers
func (c *HybridCache) Delete(ctx context.Context, userID, key string) error {
	fullKey := c.buildKey(userID, key)

	// L1: Delete from Redis
	if c.redis != nil {
		if err := c.redis.Delete(ctx, fullKey); err != nil {
			c.logger.Warn("failed to delete from L1 cache", "key", key, "error", err)
		}
	}

	// L2: Delete from PostgreSQL using generated queries
	if err := c.queries.DeleteCacheByUserAndKey(ctx, queries.DeleteCacheByUserAndKeyParams{
		UserId: userID,
		Key:    key,
	}); err != nil {
		c.logger.Warn("failed to delete from L2 cache", "key", key, "error", err)
	}

	return nil
}

// DeleteByUser removes all cache entries for a user
func (c *HybridCache) DeleteByUser(ctx context.Context, userID string) error {
	// L1: Delete pattern from Redis
	if c.redis != nil {
		pattern := c.buildKey(userID, "*")
		if _, err := c.redis.DeletePattern(ctx, pattern); err != nil {
			c.logger.Warn("failed to delete user cache from L1", "user_id", userID, "error", err)
		}
	}

	// L2: Delete from PostgreSQL using generated queries
	if err := c.queries.DeleteCacheByUserID(ctx, userID); err != nil {
		c.logger.Warn("failed to delete user cache from L2", "user_id", userID, "error", err)
	}

	return nil
}

// CleanupExpired removes expired entries from L2 cache
func (c *HybridCache) CleanupExpired(ctx context.Context) (int64, error) {
	return c.queries.DeleteExpiredCache(ctx)
}

// buildKey creates a cache key with user prefix
func (c *HybridCache) buildKey(userID, key string) string {
	return "cache:" + userID + ":" + key
}

// GetWithMetadata retrieves cache entry with metadata
func (c *HybridCache) GetWithMetadata(ctx context.Context, userID, key string) (*CacheEntry, error) {
	record, err := c.queries.GetCacheByUserAndKey(ctx, queries.GetCacheByUserAndKeyParams{
		UserId: userID,
		Key:    key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &CacheEntry{
		Key:       record.Key,
		Value:     json.RawMessage(record.Content),
		Type:      record.Feature,
		ExpiresAt: record.ExpiresAt.Time,
		CreatedAt: record.CreatedAt.Time,
	}, nil
}

// Stats returns cache statistics
func (c *HybridCache) Stats(ctx context.Context) map[string]interface{} {
	stats := make(map[string]interface{})

	// Redis stats
	if c.redis != nil {
		stats["redis"] = c.redis.Stats()
	}

	// L2 stats using generated queries
	cacheStats, err := c.queries.GetCacheStats(ctx)
	if err == nil {
		stats["l2_entries"] = cacheStats.TotalEntries
		stats["l2_expired"] = cacheStats.ExpiredEntries
		stats["l2_unique_users"] = cacheStats.UniqueUsers
		stats["l2_feature_count"] = cacheStats.FeatureCount
	}

	return stats
}
