package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetClientIP_XForwardedFor(t *testing.T) {
	tests := []struct {
		name     string
		xff      string
		expected string
	}{
		{
			name:     "single IP",
			xff:      "192.168.1.1",
			expected: "192.168.1.1",
		},
		{
			name:     "multiple IPs",
			xff:      "192.168.1.1, 10.0.0.1, 172.16.0.1",
			expected: "192.168.1.1",
		},
		{
			name:     "IPv6",
			xff:      "2001:db8::1",
			expected: "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("X-Forwarded-For", tt.xff)

			ip := getClientIP(req)
			assert.Equal(t, tt.expected, ip)
		})
	}
}

func TestGetClientIP_XRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Real-IP", "10.0.0.5")

	ip := getClientIP(req)
	assert.Equal(t, "10.0.0.5", ip)
}

func TestGetClientIP_FallbackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	// RemoteAddr is set by httptest.NewRequest to "192.0.2.1:1234"

	ip := getClientIP(req)
	assert.NotEmpty(t, ip)
}

func TestGetClientIP_PreferXFF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.1")
	req.Header.Set("X-Real-IP", "10.0.0.1")

	ip := getClientIP(req)
	assert.Equal(t, "192.168.1.1", ip)
}

func TestMax(t *testing.T) {
	tests := []struct {
		a, b     int
		expected int
	}{
		{5, 3, 5},
		{3, 5, 5},
		{5, 5, 5},
		{0, 0, 0},
		{-1, -5, -1},
		{-5, 1, 1},
	}

	for _, tt := range tests {
		result := max(tt.a, tt.b)
		assert.Equal(t, tt.expected, result)
	}
}

func TestNoRedisRateLimiter_Limit(t *testing.T) {
	rl := NewNoRedisRateLimiter(10, time.Minute)

	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	// Should always pass through when Redis is unavailable (fail open)
	rl.Limit(handler).ServeHTTP(rr, req)

	assert.True(t, handlerCalled)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestNoRedisRateLimiter_MultipleCalls(t *testing.T) {
	rl := NewNoRedisRateLimiter(2, time.Minute) // Very low limit

	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	})

	// Make 10 requests - all should pass through (fail open)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()
		rl.Limit(handler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	}

	assert.Equal(t, 10, callCount)
}

func TestNewLimiter_NilRedis(t *testing.T) {
	limiter := NewLimiter(context.Background(), nil, 100, time.Minute)

	// Should return NoRedisRateLimiter
	_, ok := limiter.(*NoRedisRateLimiter)
	assert.True(t, ok, "should return NoRedisRateLimiter when Redis is nil")
}

func TestRateLimiter_GetKey_WithUser(t *testing.T) {
	// Create rate limiter (Redis can be nil for this test)
	rl := &RateLimiter{
		redis:    nil,
		requests: 100,
		window:   time.Minute,
	}

	// Create request with user in context
	claims := &Claims{
		UserID: "user-123",
	}
	ctx := context.WithValue(context.Background(), userContextKey, claims)
	req := httptest.NewRequest(http.MethodGet, "/test", nil).WithContext(ctx)

	key := rl.getKey(req)
	assert.Equal(t, "ratelimit:user:user-123", key)
}

func TestRateLimiter_GetKey_WithoutUser(t *testing.T) {
	rl := &RateLimiter{
		redis:    nil,
		requests: 100,
		window:   time.Minute,
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.100")

	key := rl.getKey(req)
	assert.Equal(t, "ratelimit:ip:192.168.1.100", key)
}

func TestNewRateLimiter(t *testing.T) {
	// Can't test with real Redis without integration tests
	// But we can verify constructor works
	rl := NewRateLimiter(nil, 100, time.Minute)
	assert.NotNil(t, rl)
	assert.Equal(t, 100, rl.requests)
	assert.Equal(t, time.Minute, rl.window)
}

// Integration tests would require a real Redis connection or miniredis
// These will be added in the integration test suite
