package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/google/uuid"
)

// TrackLocationActivity middleware tracks when users interact with locations
// Used for intelligent preloading to prioritize reports based on user activity
func TrackLocationActivity(store *db.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Always call next handler first
			next.ServeHTTP(w, r)

			// Track in background (non-blocking)
			go func() {
				// Extract location from query params or request body
				location := extractLocation(r)
				if location == "" {
					return
				}

				// Extract user ID from context (set by auth middleware)
				userID := getUserIDFromContext(r.Context())
				if userID == "" {
					return
				}

				// Determine activity type from path
				activityType := determineActivityType(r)

				// Track activity
				ctx := context.Background()
				id := uuid.New().String()
				_ = store.Q().TrackUserLocationActivity(ctx, queries.TrackUserLocationActivityParams{
					ID:           id,
					UserID:       userID,
					Location:     location,
					ActivityType: activityType,
				})

				// Update location preferences (aggregate)
				prefID := uuid.New().String()
				_ = store.Q().UpsertUserLocationPreference(ctx, queries.UpsertUserLocationPreferenceParams{
					ID:       prefID,
					UserID:   userID,
					Location: location,
				})
			}()
		})
	}
}

// extractLocation extracts location from request (query param or body)
func extractLocation(r *http.Request) string {
	// Try query parameter first
	if location := r.URL.Query().Get("location"); location != "" {
		return location
	}

	// Try different query parameter names
	if city := r.URL.Query().Get("city"); city != "" {
		state := r.URL.Query().Get("state")
		if state != "" {
			return city + ", " + state
		}
		return city
	}

	return ""
}

// determineActivityType determines the type of activity based on the request path
func determineActivityType(r *http.Request) string {
	path := r.URL.Path

	if strings.Contains(path, "/analysis/queue") || strings.Contains(path, "/analysis/stream") {
		return "generate"
	}
	if strings.Contains(path, "/analysis/") {
		return "view_analysis"
	}
	if strings.Contains(path, "/trends/") {
		return "view_trends"
	}
	if strings.Contains(path, "/discover") || strings.Contains(path, "/search") {
		return "search"
	}

	return "view"
}

// getUserIDFromContext extracts user ID from context
// The auth middleware should set this value
func getUserIDFromContext(ctx context.Context) string {
	if userID := ctx.Value("userId"); userID != nil {
		if uid, ok := userID.(string); ok {
			return uid
		}
	}
	return ""
}
