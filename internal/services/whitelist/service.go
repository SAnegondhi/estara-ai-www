// Package whitelist provides email whitelist enforcement for beta access control.
// Ported from www_v1's emailWhitelist.ts — supports EMAIL and DOMAIN types
// with a 5-minute in-memory cache to avoid hitting the database on every login.
package whitelist

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

const cacheTTL = 5 * time.Minute

// Querier is the minimal interface for database queries.
type Querier interface {
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
}

// entry represents a whitelisted email or domain
type entry struct {
	Email  string
	Type   string // "EMAIL" or "DOMAIN"
	Active bool
}

// Service provides whitelist checking with an in-memory cache.
type Service struct {
	db     Querier
	logger *slog.Logger

	mu        sync.RWMutex
	cache     []entry
	cacheTime time.Time
}

// NewService creates a new whitelist service.
func NewService(db Querier) *Service {
	return &Service{
		db:     db,
		logger: slog.Default().With("component", "whitelist"),
	}
}

// IsEnabled returns true if the whitelist table has any active entries.
// If no active entries exist, the whitelist is effectively disabled (all emails pass).
func (s *Service) IsEnabled(ctx context.Context) bool {
	entries, err := s.getEntries(ctx)
	if err != nil {
		s.logger.Warn("failed to check whitelist, allowing access", "error", err)
		return false
	}
	return len(entries) > 0
}

// IsAllowed checks whether a given email is allowed by the whitelist.
// Returns true if:
//   - the whitelist is empty (disabled), OR
//   - the exact email is whitelisted, OR
//   - the email's domain matches a whitelisted domain
func (s *Service) IsAllowed(ctx context.Context, email string) bool {
	entries, err := s.getEntries(ctx)
	if err != nil {
		s.logger.Warn("failed to check whitelist, allowing access", "error", err)
		return true
	}

	// If no active entries, whitelist is disabled — allow all
	if len(entries) == 0 {
		return true
	}

	email = strings.ToLower(strings.TrimSpace(email))
	domain := ""
	if at := strings.LastIndex(email, "@"); at >= 0 {
		domain = email[at+1:]
	}

	for _, e := range entries {
		if !e.Active {
			continue
		}
		target := strings.ToLower(strings.TrimSpace(e.Email))
		switch e.Type {
		case "EMAIL":
			if target == email {
				return true
			}
		case "DOMAIN":
			// Strip leading @ if present (Prisma stored domains as @domain.com)
			target = strings.TrimPrefix(target, "@")
			if domain != "" && target == domain {
				return true
			}
		}
	}

	return false
}

// InvalidateCache forces the next check to re-read from the database.
func (s *Service) InvalidateCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheTime = time.Time{}
	s.cache = nil
}

// getEntries returns cached whitelist entries, refreshing from DB if stale.
func (s *Service) getEntries(ctx context.Context) ([]entry, error) {
	s.mu.RLock()
	if time.Since(s.cacheTime) < cacheTTL && s.cache != nil {
		entries := s.cache
		s.mu.RUnlock()
		return entries, nil
	}
	s.mu.RUnlock()

	// Refresh from database
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if time.Since(s.cacheTime) < cacheTTL && s.cache != nil {
		return s.cache, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT email, type, active
		FROM whitelisted_emails
		WHERE active = true
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Email, &e.Type, &e.Active); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []entry{}
	}

	s.cache = entries
	s.cacheTime = time.Now()
	s.logger.Info("whitelist cache refreshed", "entries", len(entries))

	return entries, nil
}
