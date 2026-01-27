package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildKey(t *testing.T) {
	cache := &HybridCache{}

	tests := []struct {
		userID   string
		key      string
		expected string
	}{
		{
			userID:   "user-123",
			key:      "analysis:austin_tx",
			expected: "cache:user-123:analysis:austin_tx",
		},
		{
			userID:   "admin",
			key:      "market_defaults",
			expected: "cache:admin:market_defaults",
		},
		{
			userID:   "test-user",
			key:      "*",
			expected: "cache:test-user:*",
		},
	}

	for _, tt := range tests {
		result := cache.buildKey(tt.userID, tt.key)
		assert.Equal(t, tt.expected, result)
	}
}

func TestNewHybridCache(t *testing.T) {
	// Test with nil dependencies (graceful handling)
	cache := NewHybridCache(nil, nil)
	assert.NotNil(t, cache)
	assert.Nil(t, cache.redis)
	assert.Nil(t, cache.db)
	assert.NotNil(t, cache.logger)
}

// NOTE: Get, Set, Delete, and DeleteByUser require database connections
// These are tested in integration tests with real Redis and PostgreSQL
// See hybrid_integration_test.go

// NOTE: Stats requires database connection and is tested in integration tests

func TestCacheEntry(t *testing.T) {
	entry := CacheEntry{
		Key:   "test-key",
		Value: []byte(`{"test": "value"}`),
		Type:  "market_analysis",
	}

	assert.Equal(t, "test-key", entry.Key)
	assert.Equal(t, "market_analysis", entry.Type)
	assert.NotEmpty(t, entry.Value)
}

func TestErrors(t *testing.T) {
	// Verify error values are set correctly
	assert.Equal(t, "cache miss", ErrCacheMiss.Error())
	assert.Equal(t, "not found", ErrNotFound.Error())
}

// Integration tests with real Redis and PostgreSQL would go in a separate file
// e.g., hybrid_integration_test.go with build tag: //go:build integration
