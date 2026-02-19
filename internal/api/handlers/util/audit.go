package util

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/jackc/pgx/v5/pgtype"
)

// LogFeatureUsage logs a feature usage event to the audit_logs table.
// This is used to track all user feature usage for customer support and chargeback defense.
//
// Parameters:
//   - ctx: Request context
//   - store: Database store for query execution
//   - r: HTTP request (for IP, user agent, endpoint)
//   - userID: User ID performing the action
//   - eventType: AuditEventType enum value (e.g., "FEATURE_DISCOVER_SEARCH")
//   - description: Human-readable description of the action
//   - metadata: Additional context specific to the feature (stored as JSONB)
//
// Returns error only for logging purposes - errors are non-blocking and don't fail the request.
func LogFeatureUsage(
	ctx context.Context,
	store *db.Store,
	r *http.Request,
	userID string,
	eventType string,
	description string,
	metadata map[string]any,
) error {
	// Generate random ID for audit log entry
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	// Extract client IP from headers (respects X-Forwarded-For and X-Real-IP)
	clientIP := extractClientIP(r)

	// Serialize metadata to JSONB
	metadataJSON := []byte("{}")
	if len(metadata) > 0 {
		var err error
		metadataJSON, err = json.Marshal(metadata)
		if err != nil {
			// If metadata serialization fails, log with empty metadata
			metadataJSON = []byte("{}")
		}
	}

	// Write to audit_logs table
	q := store.Q()
	return q.CreateAuditLog(ctx, queries.CreateAuditLogParams{
		ID:          id,
		UserId:      pgtype.Text{String: userID, Valid: userID != ""},
		Event:       eventType,
		Description: pgtype.Text{String: description, Valid: description != ""},
		IpAddress:   pgtype.Text{String: clientIP, Valid: clientIP != ""},
		UserAgent:   pgtype.Text{String: r.UserAgent(), Valid: true},
		Metadata:    metadataJSON,
		Success:     true,
		Error:       pgtype.Text{Valid: false},
		Endpoint:    pgtype.Text{String: r.URL.Path, Valid: true},
		Severity:    "info",
	})
}

// extractClientIP extracts the real client IP from request headers.
// Respects reverse proxy headers (X-Forwarded-For, X-Real-IP) while falling back to RemoteAddr.
//
// Priority order:
//  1. X-Forwarded-For (first IP if comma-separated list)
//  2. X-Real-IP
//  3. RemoteAddr
func extractClientIP(r *http.Request) string {
	clientIP := r.RemoteAddr

	// Check X-Forwarded-For header (used by load balancers/proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can contain multiple IPs: "client, proxy1, proxy2"
		// Take the first one (original client)
		if firstIP, _, found := strings.Cut(xff, ","); found {
			clientIP = firstIP
		} else {
			clientIP = xff
		}
	} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
		// X-Real-IP is set by some reverse proxies (e.g., nginx)
		clientIP = xri
	}

	return strings.TrimSpace(clientIP)
}
