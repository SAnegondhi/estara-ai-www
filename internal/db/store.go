package db

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/estara-ai/www/internal/db/marketqueries"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
)

// Store is the ONLY database interface for handlers and services.
// All DB access MUST go through Store methods (sqlc-generated).
//
// ADR-083: Single Auditable Database Interface
// - All database queries use sqlc-generated code (100% compile-time validated)
// - Pool access restricted to monitoring (Ping/Stat) only
// - Transactions use WithTx() helper
type Store struct {
	q          *queries.Queries
	mq         *marketqueries.Queries
	pool       *postgres.Pool // main DB pool — WithTx() + monitoring only
	marketPool *postgres.Pool // market DB pool — monitoring only
}

// NewStore creates a new Store from a postgres.DB.
func NewStore(db *postgres.DB) *Store {
	s := &Store{
		q:    queries.New(db.Main),
		pool: db.Main,
	}
	if db.Market != nil {
		s.mq = marketqueries.New(db.Market)
		s.marketPool = db.Market
	}
	return s
}

// Q returns the main database querier.
func (s *Store) Q() *queries.Queries { return s.q }

// MQ returns the market database querier. May be nil if market DB is not configured.
func (s *Store) MQ() *marketqueries.Queries { return s.mq }

// WithTx executes fn within a transaction on the main database.
func (s *Store) WithTx(ctx context.Context, fn func(q *queries.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WithTxOptions executes fn within a transaction with the given options.
func (s *Store) WithTxOptions(ctx context.Context, opts pgx.TxOptions, fn func(q *queries.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Pool returns the underlying main database pool.
//
// ⚠️ RESTRICTED USE ONLY ⚠️
// This method may ONLY be used for:
//   - Monitoring: Ping(), Stat()
//   - Transactions: WithTx() helper (internal use)
//
// ❌ FORBIDDEN: Query(), QueryRow(), Exec()
// ✅ REQUIRED: Use Q() for all database queries
//
// ADR-083: All 43 database query Pool() calls have been migrated to sqlc.
// Only monitoring calls remain (health checks, connection stats).
func (s *Store) Pool() *postgres.Pool { return s.pool }

// MarketPool returns the underlying market database pool.
//
// ⚠️ RESTRICTED USE ONLY ⚠️
// This method may ONLY be used for:
//   - Monitoring: Ping(), Stat()
//   - Nil checks: Verify market DB is configured
//
// ❌ FORBIDDEN: Query(), QueryRow(), Exec()
// ✅ REQUIRED: Use MQ() for all database queries
//
// ADR-083: All database query MarketPool() calls have been migrated to sqlc.
// Only monitoring calls remain (health checks, connection stats).
func (s *Store) MarketPool() *postgres.Pool { return s.marketPool }
