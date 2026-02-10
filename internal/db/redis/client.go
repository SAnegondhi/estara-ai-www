package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/estara-ai/www/internal/config"
)

// Client wraps the Redis client with additional functionality
type Client struct {
	*redis.Client
}

// NewClient creates a new Redis client
func NewClient(ctx context.Context, cfg config.RedisConfig) (*Client, error) {
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	// Override options if provided
	if cfg.Password != "" {
		opts.Password = cfg.Password
	}
	if cfg.DB != 0 {
		opts.DB = cfg.DB
	}

	// Configure pool settings
	opts.PoolSize = 10
	opts.MinIdleConns = 2
	opts.MaxRetries = 3
	opts.MinRetryBackoff = 8 * time.Millisecond
	opts.MaxRetryBackoff = 512 * time.Millisecond

	client := redis.NewClient(opts)

	// Verify connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	slog.Info("redis client connected",
		"addr", opts.Addr,
		"db", opts.DB,
	)

	return &Client{Client: client}, nil
}

// Close closes the Redis connection
func (c *Client) Close() error {
	if c.Client != nil {
		if err := c.Client.Close(); err != nil {
			return err
		}
		slog.Info("redis client closed")
	}
	return nil
}

// Health checks if Redis is healthy
func (c *Client) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := c.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis health check failed: %w", err)
	}
	return nil
}

// SetJSON stores a value as JSON with optional TTL
func (c *Client) SetJSON(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value for Redis: %w", err)
	}
	return c.Set(ctx, key, data, ttl).Err()
}

// GetJSON retrieves a JSON value
func (c *Client) GetJSON(ctx context.Context, key string) ([]byte, error) {
	result, err := c.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil // Key not found
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Delete removes keys
func (c *Client) Delete(ctx context.Context, keys ...string) error {
	return c.Del(ctx, keys...).Err()
}

// DeletePattern removes keys matching a pattern
func (c *Client) DeletePattern(ctx context.Context, pattern string) (int64, error) {
	var deleted int64
	iter := c.Scan(ctx, 0, pattern, 100).Iterator()

	for iter.Next(ctx) {
		if err := c.Del(ctx, iter.Val()).Err(); err != nil {
			return deleted, err
		}
		deleted++
	}

	if err := iter.Err(); err != nil {
		return deleted, err
	}

	return deleted, nil
}

// Exists checks if a key exists
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	result, err := c.Client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

// Incr increments a counter and returns the new value
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	return c.Client.Incr(ctx, key).Result()
}

// Expire sets the expiration for a key
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.Client.Expire(ctx, key, ttl).Err()
}

// Stats returns Redis statistics
func (c *Client) Stats() *ClientStats {
	poolStats := c.PoolStats()
	return &ClientStats{
		Hits:       poolStats.Hits,
		Misses:     poolStats.Misses,
		Timeouts:   poolStats.Timeouts,
		TotalConns: poolStats.TotalConns,
		IdleConns:  poolStats.IdleConns,
		StaleConns: poolStats.StaleConns,
	}
}

// ClientStats holds Redis client statistics
type ClientStats struct {
	Hits       uint32 `json:"hits"`
	Misses     uint32 `json:"misses"`
	Timeouts   uint32 `json:"timeouts"`
	TotalConns uint32 `json:"totalConns"`
	IdleConns  uint32 `json:"idleConns"`
	StaleConns uint32 `json:"staleConns"`
}
