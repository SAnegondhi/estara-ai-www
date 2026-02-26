package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/estara-ai/www/internal/config"
)

// Pool wraps a pgxpool.Pool with additional functionality
type Pool struct {
	*pgxpool.Pool
	name string
}

// NewPool creates a new PostgreSQL connection pool
func NewPool(ctx context.Context, cfg config.DatabaseConfig, name string) (*Pool, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("%s: database URL is required", name)
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to parse database URL: %w", name, err)
	}

	// Configure pool settings
	poolConfig.MaxConns = int32(cfg.MaxConnections)
	poolConfig.MinConns = int32(cfg.MinConnections)
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	// Add health check interval
	poolConfig.HealthCheckPeriod = 30 * time.Second

	// Create the pool
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to create connection pool: %w", name, err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: failed to ping database: %w", name, err)
	}

	slog.Info("database pool created",
		"name", name,
		"max_conns", poolConfig.MaxConns,
		"min_conns", poolConfig.MinConns,
	)

	return &Pool{
		Pool: pool,
		name: name,
	}, nil
}

// Close closes the connection pool gracefully
func (p *Pool) Close() {
	if p.Pool != nil {
		p.Pool.Close()
		slog.Info("database pool closed", "name", p.name)
	}
}

// Health checks if the database connection is healthy
func (p *Pool) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := p.Ping(ctx); err != nil {
		return fmt.Errorf("%s: health check failed: %w", p.name, err)
	}

	return nil
}

// Stats returns pool statistics
func (p *Pool) Stats() *PoolStats {
	stat := p.Stat()
	return &PoolStats{
		Name:              p.name,
		AcquireCount:      stat.AcquireCount(),
		AcquiredConns:     stat.AcquiredConns(),
		CanceledAcquires:  stat.CanceledAcquireCount(),
		ConstructingConns: stat.ConstructingConns(),
		EmptyAcquires:     stat.EmptyAcquireCount(),
		IdleConns:         stat.IdleConns(),
		MaxConns:          stat.MaxConns(),
		TotalConns:        stat.TotalConns(),
	}
}

// PoolStats holds connection pool statistics
type PoolStats struct {
	Name              string `json:"name"`
	AcquireCount      int64  `json:"acquireCount"`
	AcquiredConns     int32  `json:"acquiredConns"`
	CanceledAcquires  int64  `json:"canceledAcquires"`
	ConstructingConns int32  `json:"constructingConns"`
	EmptyAcquires     int64  `json:"emptyAcquires"`
	IdleConns         int32  `json:"idleConns"`
	MaxConns          int32  `json:"maxConns"`
	TotalConns        int32  `json:"totalConns"`
}

// DB provides access to multiple database pools
type DB struct {
	Main   *Pool // Main application database
	Market *Pool // Market data time series database
}

// NewDB creates database connections for all configured databases
func NewDB(ctx context.Context, cfg *config.Config) (*DB, error) {
	db := &DB{}

	// Connect to main database (required)
	mainPool, err := NewPool(ctx, cfg.Database, "main")
	if err != nil {
		return nil, err
	}
	db.Main = mainPool

	// Run pending migrations on main database
	if err := RunMigrations(ctx, mainPool); err != nil {
		slog.Warn("failed to run migrations", "error", err)
		// Don't fail startup - migrations might already be applied
	}

	// Connect to market database (optional)
	if cfg.MarketDB.URL != "" {
		marketPool, err := NewPool(ctx, cfg.MarketDB, "market")
		if err != nil {
			// Log warning but don't fail - market data is optional
			slog.Warn("failed to connect to market database", "error", err)
		} else {
			db.Market = marketPool
			// Ensure market schema exists (idempotent — uses CREATE IF NOT EXISTS)
			if err := RunMarketSchema(ctx, marketPool); err != nil {
				slog.Warn("market schema init warning", "error", err)
			}
		}
	}

	return db, nil
}

// Close closes all database connections
func (db *DB) Close() {
	if db.Main != nil {
		db.Main.Close()
	}
	if db.Market != nil {
		db.Market.Close()
	}
}

// Health checks all database connections
func (db *DB) Health(ctx context.Context) map[string]error {
	results := make(map[string]error)

	if db.Main != nil {
		results["main"] = db.Main.Health(ctx)
	}
	if db.Market != nil {
		results["market"] = db.Market.Health(ctx)
	}

	return results
}

// AllStats returns statistics for all pools
func (db *DB) AllStats() map[string]*PoolStats {
	stats := make(map[string]*PoolStats)

	if db.Main != nil {
		stats["main"] = db.Main.Stats()
	}
	if db.Market != nil {
		stats["market"] = db.Market.Stats()
	}

	return stats
}
