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
// Raw pool access is not exposed except for transaction support.
type Store struct {
	q          *queries.Queries
	mq         *marketqueries.Queries
	pool       *postgres.Pool // main DB pool — transactions + gradual migration
	marketPool *postgres.Pool // market DB pool — gradual migration only
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
// This should ONLY be used by migration runners, the Store constructor,
// and as a transitional shim during the raw-SQL-to-sqlc migration.
// New code must NOT use this — use Q() instead.
func (s *Store) Pool() *postgres.Pool { return s.pool }

// MarketPool returns the underlying market database pool.
// This is a transitional shim during the raw-SQL-to-sqlc migration.
// New code must NOT use this — use MQ() instead.
func (s *Store) MarketPool() *postgres.Pool { return s.marketPool }
