package ai

import (
	"net/http"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// UserActivityResponse represents the user's location activity history
type UserActivityResponse struct {
	Success          bool     `json:"success"`
	TopLocations     []string `json:"topLocations"`     // Most viewed locations
	RecentSearches   []string `json:"recentSearches"`   // Recent location searches
	LastActivityDate *string  `json:"lastActivity"`     // Most recent activity timestamp
}

// GetUserActivity returns the user's location activity for intelligent preloading
// GET /api/user/activity/locations
func (h *Handler) GetUserActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract user ID from context (set by auth middleware)
	userID, ok := ctx.Value("userId").(string)
	if !ok || userID == "" {
		httputil.Unauthorized(w, "User not authenticated")
		return
	}

	// Get query parameter for limit (default 10)
	limit := int32(10)
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l := httputil.GetQueryParamInt(r, "limit", 10); l > 0 && l <= 50 {
			limit = int32(l)
		}
	}

	// Fetch top locations by view count
	topLocs, err := h.store.Q().GetUserTopLocations(ctx, queries.GetUserTopLocationsParams{
		UserID: userID,
		Limit:  limit,
	})
	if err != nil {
		h.logger.Error("failed to get user top locations", "error", err, "userID", userID)
		topLocs = []queries.GetUserTopLocationsRow{} // Empty result on error
	}

	// Fetch recent activity
	recentActivity, err := h.store.Q().GetUserRecentActivity(ctx, queries.GetUserRecentActivityParams{
		UserID: userID,
		Limit:  limit,
	})
	if err != nil {
		h.logger.Error("failed to get user recent activity", "error", err, "userID", userID)
		recentActivity = []queries.GetUserRecentActivityRow{} // Empty result on error
	}

	// Extract location names
	topLocations := make([]string, 0, len(topLocs))
	for _, loc := range topLocs {
		topLocations = append(topLocations, loc.Location)
	}

	// Extract recent search locations (filter by search activity type)
	recentSearches := make([]string, 0)
	seenLocations := make(map[string]bool)
	for _, activity := range recentActivity {
		if activity.ActivityType == "search" && !seenLocations[activity.Location] {
			recentSearches = append(recentSearches, activity.Location)
			seenLocations[activity.Location] = true
		}
	}

	// Get last activity timestamp
	var lastActivity *string
	if len(recentActivity) > 0 {
		ts := recentActivity[0].CreatedAt.Format("2006-01-02T15:04:05Z07:00")
		lastActivity = &ts
	}

	httputil.JSON(w, http.StatusOK, UserActivityResponse{
		Success:          true,
		TopLocations:     topLocations,
		RecentSearches:   recentSearches,
		LastActivityDate: lastActivity,
	})
}
