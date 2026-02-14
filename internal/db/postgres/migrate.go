package postgres

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations executes all pending migrations on the main database
// Migrations are embedded SQL files in the migrations/ directory
// Each migration is tracked in schema_migrations table to prevent re-running
func RunMigrations(ctx context.Context, pool *Pool) error {
	logger := slog.Default().With("component", "migrations")

	// Ensure migrations table exists
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Get list of migration files
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Sort migrations by filename (date-prefixed)
	var migrationFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			migrationFiles = append(migrationFiles, entry.Name())
		}
	}
	sort.Strings(migrationFiles)

	// Get already applied migrations
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("failed to query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("failed to scan migration version: %w", err)
		}
		applied[version] = true
	}

	// Apply pending migrations
	appliedCount := 0
	for _, filename := range migrationFiles {
		version := strings.TrimSuffix(filename, ".sql")
		if applied[version] {
			continue
		}

		// Read migration SQL
		content, err := migrationsFS.ReadFile(filepath.Join("migrations", filename))
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", filename, err)
		}

		sql := string(content)

		// Migrations containing ALTER TYPE ... ADD VALUE cannot run inside a
		// transaction in PostgreSQL. Detect this and execute statements individually.
		if strings.Contains(sql, "ALTER TYPE") && strings.Contains(sql, "ADD VALUE") {
			// Execute each statement outside a transaction
			stmts := splitStatements(sql)
			for _, stmt := range stmts {
				if _, err := pool.Exec(ctx, stmt); err != nil {
					return fmt.Errorf("failed to execute migration %s: %w", filename, err)
				}
			}
			// Record migration
			if _, err := pool.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
				return fmt.Errorf("failed to record migration %s: %w", filename, err)
			}
		} else {
			// Execute migration in a transaction
			tx, err := pool.Begin(ctx)
			if err != nil {
				return fmt.Errorf("failed to begin transaction for %s: %w", filename, err)
			}

			if _, err := tx.Exec(ctx, sql); err != nil {
				tx.Rollback(ctx)
				return fmt.Errorf("failed to execute migration %s: %w", filename, err)
			}

			if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
				tx.Rollback(ctx)
				return fmt.Errorf("failed to record migration %s: %w", filename, err)
			}

			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("failed to commit migration %s: %w", filename, err)
			}
		}

		logger.Info("applied migration", "version", version)
		appliedCount++
	}

	if appliedCount > 0 {
		logger.Info("migrations complete", "applied", appliedCount, "total", len(migrationFiles))
	} else {
		logger.Debug("no pending migrations")
	}

	return nil
}

// splitStatements splits a SQL string into individual statements on semicolons,
// skipping comments and empty lines.
func splitStatements(sql string) []string {
	var stmts []string
	for _, part := range strings.Split(sql, ";") {
		stmt := strings.TrimSpace(part)
		if stmt == "" {
			continue
		}
		// Skip comment-only blocks
		lines := strings.Split(stmt, "\n")
		hasCode := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "--") {
				hasCode = true
				break
			}
		}
		if hasCode {
			stmts = append(stmts, stmt)
		}
	}
	return stmts
}
