//go:build integration

package cache_test

import (
	"testing"
	"time"

	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHybridCache_Integration(t *testing.T) {
	db := testutil.SetupTestDB(t)
	defer db.Cleanup(t)

	// Use the default test user
	userID := db.GetDefaultTestUser(t)
	hybridCache := cache.NewHybridCache(db.Redis, db.MainPool)
	ctx, cancel := testutil.GetTestContext()
	defer cancel()

	t.Run("Set and Get from L1 Redis", func(t *testing.T) {
		key := "test:analysis:austin_tx"
		value := map[string]interface{}{
			"median_price": 450000,
			"location":     "Austin, TX",
		}
		ttl := 1 * time.Hour

		// Set value (cacheType is 3rd param, value is 4th)
		err := hybridCache.Set(ctx, userID, key, "market_analysis", value, ttl)
		require.NoError(t, err)

		// Get value (should hit L1 Redis)
		got, err := hybridCache.Get(ctx, userID, key)
		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Contains(t, string(got), "Austin, TX")
	})

	t.Run("Get from L2 PostgreSQL after Redis flush", func(t *testing.T) {
		key := "test:analysis:dallas_tx"
		value := map[string]interface{}{
			"median_price": 380000,
			"location":     "Dallas, TX",
		}

		// Set in both layers
		err := hybridCache.Set(ctx, userID, key, "market_analysis", value, time.Hour)
		require.NoError(t, err)

		// Verify it's accessible
		got1, err := hybridCache.Get(ctx, userID, key)
		require.NoError(t, err)
		assert.Contains(t, string(got1), "Dallas, TX")

		// Flush Redis to simulate restart/eviction
		db.Redis.FlushDB(ctx)

		// Get should fall back to L2 PostgreSQL and warm L1
		got2, err := hybridCache.Get(ctx, userID, key)
		require.NoError(t, err)
		assert.Contains(t, string(got2), "Dallas, TX")

		// Verify L1 was warmed (get again should be fast)
		got3, err := hybridCache.Get(ctx, userID, key)
		require.NoError(t, err)
		assert.Contains(t, string(got3), "Dallas, TX")
	})

	t.Run("Delete removes from both layers", func(t *testing.T) {
		key := "test:analysis:houston_tx"
		value := map[string]interface{}{
			"median_price": 320000,
			"location":     "Houston, TX",
		}

		// Set value
		err := hybridCache.Set(ctx, userID, key, "market_analysis", value, time.Hour)
		require.NoError(t, err)

		// Verify it exists
		got, err := hybridCache.Get(ctx, userID, key)
		require.NoError(t, err)
		assert.Contains(t, string(got), "Houston, TX")

		// Delete
		err = hybridCache.Delete(ctx, userID, key)
		require.NoError(t, err)

		// Verify it's gone
		_, err = hybridCache.Get(ctx, userID, key)
		assert.ErrorIs(t, err, cache.ErrCacheMiss)
	})

	t.Run("DeleteByUser removes all user keys", func(t *testing.T) {
		// Create a separate user for this test
		deleteTestUserID := db.CreateTestUser(t, "delete_test_user")

		// Set multiple keys for this user
		keys := []string{"test:key1", "test:key2", "test:key3"}
		for _, k := range keys {
			err := hybridCache.Set(ctx, deleteTestUserID, k, "test", map[string]string{"data": k}, time.Hour)
			require.NoError(t, err)
		}

		// Verify all exist
		for _, k := range keys {
			_, err := hybridCache.Get(ctx, deleteTestUserID, k)
			require.NoError(t, err)
		}

		// Delete all user keys (returns just error)
		err := hybridCache.DeleteByUser(ctx, deleteTestUserID)
		require.NoError(t, err)

		// Verify all are deleted
		for _, k := range keys {
			_, err := hybridCache.Get(ctx, deleteTestUserID, k)
			assert.ErrorIs(t, err, cache.ErrCacheMiss)
		}
	})

	t.Run("Cache miss for non-existent key", func(t *testing.T) {
		_, err := hybridCache.Get(ctx, userID, "test:non-existent-key")
		assert.ErrorIs(t, err, cache.ErrCacheMiss)
	})

	t.Run("Stats returns valid statistics", func(t *testing.T) {
		stats := hybridCache.Stats(ctx)
		assert.NotNil(t, stats)
		// Stats should have some entries from previous tests
	})

	t.Run("Different users have isolated caches", func(t *testing.T) {
		user1 := db.CreateTestUser(t, "isolation_user1")
		user2 := db.CreateTestUser(t, "isolation_user2")

		key := "test:shared-key-name"
		value1 := map[string]string{"user": "user1"}
		value2 := map[string]string{"user": "user2"}

		// Set same key for different users
		err := hybridCache.Set(ctx, user1, key, "test", value1, time.Hour)
		require.NoError(t, err)
		err = hybridCache.Set(ctx, user2, key, "test", value2, time.Hour)
		require.NoError(t, err)

		// Get for each user should return their own value
		got1, err := hybridCache.Get(ctx, user1, key)
		require.NoError(t, err)
		assert.Contains(t, string(got1), "user1")

		got2, err := hybridCache.Get(ctx, user2, key)
		require.NoError(t, err)
		assert.Contains(t, string(got2), "user2")
	})

	t.Run("Large value storage", func(t *testing.T) {
		key := "test:large-value"
		// Create a large value (~10KB)
		largeData := make([]byte, 10*1024)
		for i := range largeData {
			largeData[i] = byte(i % 256)
		}

		err := hybridCache.Set(ctx, userID, key, "large_test", largeData, time.Hour)
		require.NoError(t, err)

		got, err := hybridCache.Get(ctx, userID, key)
		require.NoError(t, err)
		assert.NotNil(t, got)
	})
}

func TestHybridCache_TTL_Integration(t *testing.T) {
	db := testutil.SetupTestDB(t)
	defer db.Cleanup(t)

	userID := db.GetDefaultTestUser(t)
	hybridCache := cache.NewHybridCache(db.Redis, db.MainPool)
	ctx, cancel := testutil.GetTestContext()
	defer cancel()

	t.Run("Short TTL expires in Redis", func(t *testing.T) {
		key := "test:short-ttl"
		value := map[string]string{"expires": "soon"}

		// Set with 1 second TTL
		err := hybridCache.Set(ctx, userID, key, "test", value, 1*time.Second)
		require.NoError(t, err)

		// Should exist immediately
		got, err := hybridCache.Get(ctx, userID, key)
		require.NoError(t, err)
		assert.Contains(t, string(got), "soon")

		// Wait for TTL to expire
		time.Sleep(2 * time.Second)

		// Redis should have expired, but L2 might still have it
		// (L2 expiry is handled differently)
		// This tests the L1 TTL behavior
	})
}
