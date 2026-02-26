package postgres

import (
	"context"
	_ "embed"
	"log/slog"
	"strings"
)

//go:embed market_schema.sql
var marketSchemaSQL string

// RunMarketSchema creates all market database tables if they don't already exist.
// Uses CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS so it is safe
// to call on every startup against both fresh and already-initialised databases.
func RunMarketSchema(ctx context.Context, pool *Pool) error {
	logger := slog.Default().With("component", "market_schema")

	stmts := splitStatements(marketSchemaSQL)
	ok, failed := 0, 0
	for _, stmt := range stmts {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		if _, err := pool.Exec(ctx, trimmed); err != nil {
			logger.Warn("market schema statement failed",
				"error", err,
				"stmt_prefix", truncate(trimmed, 80))
			failed++
		} else {
			ok++
		}
	}

	if failed > 0 {
		logger.Warn("market schema init completed with errors",
			"ok", ok, "failed", failed)
	} else {
		logger.Info("market schema initialised", "statements", ok)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
