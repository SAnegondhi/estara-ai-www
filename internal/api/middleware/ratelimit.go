package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/pkg/httputil"
)

// RateLimiter implements rate limiting using Redis
type RateLimiter struct {
	redis    *redisClient.Client
	requests int
	window   time.Duration
	logger   *slog.Logger
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(redis *redisClient.Client, requests int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		redis:    redis,
		requests: requests,
		window:   window,
		logger:   slog.Default(),
	}
}

// Limit applies rate limiting to requests
func (rl *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.getKey(r)

		// Try to increment counter
		count, err := rl.redis.Incr(r.Context(), key)
		if err != nil {
			// Fail open but log error
			rl.logger.Warn("rate limit redis error", "error", err, "key", key)
			next.ServeHTTP(w, r)
			return
		}

		// Set expiration on first request
		if count == 1 {
			if err := rl.redis.Expire(r.Context(), key, rl.window); err != nil {
				rl.logger.Warn("rate limit expire error", "error", err)
			}
		}

		remaining := max(0, rl.requests-int(count))

		// Set rate limit headers
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.requests))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(rl.window).Unix(), 10))

		// Check if over limit
		if count > int64(rl.requests) {
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
			rl.logger.Warn("rate limit exceeded",
				"key", key,
				"count", count,
				"limit", rl.requests,
			)
			httputil.Error(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// LimitByUser applies per-user rate limiting
func (rl *RateLimiter) LimitByUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user == nil {
			// Fall back to IP-based limiting
			rl.Limit(next).ServeHTTP(w, r)
			return
		}

		key := "ratelimit:user:" + user.UserID

		// Same logic as Limit but with user key
		count, err := rl.redis.Incr(r.Context(), key)
		if err != nil {
			rl.logger.Warn("rate limit redis error", "error", err, "user_id", user.UserID)
			next.ServeHTTP(w, r)
			return
		}

		if count == 1 {
			_ = rl.redis.Expire(r.Context(), key, rl.window)
		}

		remaining := max(0, rl.requests-int(count))

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.requests))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(rl.window).Unix(), 10))

		if count > int64(rl.requests) {
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
			rl.logger.Warn("user rate limit exceeded",
				"user_id", user.UserID,
				"count", count,
				"limit", rl.requests,
			)
			httputil.Error(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// getKey generates the rate limit key for a request
func (rl *RateLimiter) getKey(r *http.Request) string {
	// Try to get user ID first
	if user := GetUserFromContext(r.Context()); user != nil {
		return "ratelimit:user:" + user.UserID
	}

	// Fall back to IP address
	ip := getClientIP(r)
	return "ratelimit:ip:" + ip
}

// getClientIP extracts the client IP from the request
func getClientIP(r *http.Request) string {
	// Check for forwarded headers (behind proxy/load balancer)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to remote address
	return r.RemoteAddr
}

// max returns the larger of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// NoRedisRateLimiter is a fallback rate limiter when Redis is unavailable
// It uses an in-memory map (not suitable for distributed systems)
type NoRedisRateLimiter struct {
	requests int
	window   time.Duration
	logger   *slog.Logger
}

// NewNoRedisRateLimiter creates a rate limiter without Redis
func NewNoRedisRateLimiter(requests int, window time.Duration) *NoRedisRateLimiter {
	return &NoRedisRateLimiter{
		requests: requests,
		window:   window,
		logger:   slog.Default(),
	}
}

// Limit is a no-op when Redis is unavailable (fail open)
func (rl *NoRedisRateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Log that we're not rate limiting
		rl.logger.Debug("rate limiting disabled - no redis")
		next.ServeHTTP(w, r)
	})
}

// Limiter interface for both implementations
type Limiter interface {
	Limit(next http.Handler) http.Handler
}

// NewLimiter creates the appropriate rate limiter based on Redis availability
func NewLimiter(ctx context.Context, redis *redisClient.Client, requests int, window time.Duration) Limiter {
	if redis == nil {
		return NewNoRedisRateLimiter(requests, window)
	}

	// Verify Redis is healthy
	if err := redis.Health(ctx); err != nil {
		slog.Warn("redis unavailable for rate limiting", "error", err)
		return NewNoRedisRateLimiter(requests, window)
	}

	return NewRateLimiter(redis, requests, window)
}
