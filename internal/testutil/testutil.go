//go:build integration

// Package testutil provides utilities for integration tests.
// These tests require real database connections (Neon PostgreSQL + Redis).
package testutil

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/redis"
)

// TestDB provides database connections for integration tests.
type TestDB struct {
	MainPool   *postgres.Pool
	MarketPool *postgres.Pool
	Redis      *redis.Client
}

// SetupTestDB creates test database connections.
// Skips the test if required environment variables are not set.
func SetupTestDB(t *testing.T) *TestDB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Main PostgreSQL (Neon)
	mainURL := os.Getenv("DATABASE_URL")
	if mainURL == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	mainPool, err := postgres.NewPool(ctx, config.DatabaseConfig{
		URL:             mainURL,
		MaxConnections:  5,
		MinConnections:  1,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}, "test_main")
	if err != nil {
		t.Fatalf("failed to connect to Neon main database: %v", err)
	}

	// Market PostgreSQL (Neon)
	marketURL := os.Getenv("MARKET_DATABASE_URL")
	if marketURL == "" {
		mainPool.Close()
		t.Skip("MARKET_DATABASE_URL not set, skipping integration test")
	}

	marketPool, err := postgres.NewPool(ctx, config.DatabaseConfig{
		URL:             marketURL,
		MaxConnections:  5,
		MinConnections:  1,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}, "test_market")
	if err != nil {
		mainPool.Close()
		t.Fatalf("failed to connect to Neon market database: %v", err)
	}

	// Redis (local)
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379" // Default port
	}

	redisClient, err := redis.NewClient(ctx, config.RedisConfig{
		URL: redisURL,
	})
	if err != nil {
		mainPool.Close()
		marketPool.Close()
		t.Fatalf("failed to connect to redis: %v", err)
	}

	return &TestDB{
		MainPool:   mainPool,
		MarketPool: marketPool,
		Redis:      redisClient,
	}
}

// Cleanup closes all connections and cleans test data.
// Uses test-specific prefixes to avoid affecting real data.
func (db *TestDB) Cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Clean Redis test data (delete test: prefixed keys)
	if _, err := db.Redis.DeletePattern(ctx, "cache:*:test:*"); err != nil {
		t.Logf("warning: failed to clean redis test keys: %v", err)
	}
	db.Redis.Close()

	// Clean Neon test data using test-specific prefixes
	// This avoids accidentally deleting real data
	_, err := db.MainPool.Exec(ctx, `DELETE FROM analysis_cache WHERE key LIKE 'test:%'`)
	if err != nil {
		t.Logf("warning: failed to clean analysis_cache: %v", err)
	}

	// Delete test users created by CreateTestUser/CreateTestAdminUser (test_* prefix)
	_, err = db.MainPool.Exec(ctx, `DELETE FROM users WHERE email LIKE 'test_%@estara-ai.com'`)
	if err != nil {
		t.Logf("warning: failed to clean test users: %v", err)
	}

	db.MainPool.Close()
	db.MarketPool.Close()
}

// GetExistingUser retrieves an existing user by email and returns the ID.
// Use this for integration tests that should use real users.
func (db *TestDB) GetExistingUser(t *testing.T, email string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var id string
	err := db.MainPool.QueryRow(ctx, `
		SELECT id FROM users WHERE email = $1
	`, email).Scan(&id)

	if err != nil {
		t.Fatalf("failed to get existing user %s: %v", email, err)
	}
	return id
}

// GetDefaultTestUser returns the default test user (sudhindra@estara-ai.com).
// Preferred for integration tests to avoid creating new users.
func (db *TestDB) GetDefaultTestUser(t *testing.T) string {
	return db.GetExistingUser(t, "sudhindra@estara-ai.com")
}

// CreateTestUser inserts a test user and returns the ID.
// Uses @estara-ai.com domain for test users.
func (db *TestDB) CreateTestUser(t *testing.T, name string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use estara-ai.com email domain with test_ prefix
	testEmail := "test_" + name + "@estara-ai.com"

	var id string
	err := db.MainPool.QueryRow(ctx, `
		INSERT INTO users (id, email, role, "createdAt", "updatedAt")
		VALUES (gen_random_uuid(), $1, 'USER', NOW(), NOW())
		ON CONFLICT (email) DO UPDATE SET "updatedAt" = NOW()
		RETURNING id
	`, testEmail).Scan(&id)

	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return id
}

// CreateTestAdminUser inserts a test admin user and returns the ID.
// Uses @estara-ai.com domain for test users.
func (db *TestDB) CreateTestAdminUser(t *testing.T, name string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	testEmail := "test_admin_" + name + "@estara-ai.com"

	var id string
	err := db.MainPool.QueryRow(ctx, `
		INSERT INTO users (id, email, role, "createdAt", "updatedAt")
		VALUES (gen_random_uuid(), $1, 'ADMIN', NOW(), NOW())
		ON CONFLICT (email) DO UPDATE SET role = 'ADMIN', "updatedAt" = NOW()
		RETURNING id
	`, testEmail).Scan(&id)

	if err != nil {
		t.Fatalf("failed to create test admin user: %v", err)
	}
	return id
}

// TableExists checks if a table exists in the database.
func (db *TestDB) TableExists(t *testing.T, tableName string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var exists bool
	err := db.MainPool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_name = $1
		)
	`, tableName).Scan(&exists)

	if err != nil {
		t.Fatalf("failed to check table existence: %v", err)
	}
	return exists
}

// GetTestContext returns a context with a reasonable timeout for tests.
func GetTestContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}
