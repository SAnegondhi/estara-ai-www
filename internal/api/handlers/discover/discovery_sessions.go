package discover

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/google/uuid"
)

// DiscoverySessionResponse represents a discovery session for API responses
type DiscoverySessionResponse struct {
	ID               string          `json:"id"`
	Location         string          `json:"location"`
	PropertyCount    int             `json:"propertyCount"`
	SearchCriteria   json.RawMessage `json:"searchCriteria"`
	Name             *string         `json:"name,omitempty"`
	Notes            *string         `json:"notes,omitempty"`
	Status           string          `json:"status"`
	ChatSessionCount int             `json:"chatSessionCount"`
	EvaluationCount  int             `json:"evaluationCount"`
	CreatedAt        string          `json:"createdAt"`
	LastAccessedAt   string          `json:"lastAccessedAt"`
	ArchivedAt       *string         `json:"archivedAt,omitempty"`
	ExpiresAt        string          `json:"expiresAt"`
}

// DiscoverySessionDetailResponse includes full session details with properties and activities
type DiscoverySessionDetailResponse struct {
	DiscoverySessionResponse
	CachedPropertyIds []string                    `json:"cachedPropertyIds"`
	Properties        []V2PropertyResult          `json:"properties"`
	Activities        []DiscoveryActivityResponse `json:"activities"`
	Evaluations       []SessionEvaluationResponse `json:"evaluations,omitempty"`
}

// DiscoveryActivityResponse represents an activity linked to a session
type DiscoveryActivityResponse struct {
	ID           string `json:"id"`
	ActivityType string `json:"activityType"`
	ActivityId   string `json:"activityId"`
	CreatedAt    string `json:"createdAt"`
}

// ListDiscoverySessionsResponse wraps the list response with pagination
type ListDiscoverySessionsResponse struct {
	Success    bool                       `json:"success"`
	Sessions   []DiscoverySessionResponse `json:"sessions"`
	Pagination PaginationInfo             `json:"pagination"`
}

// PaginationInfo contains pagination metadata
type PaginationInfo struct {
	Total   int64 `json:"total"`
	Limit   int   `json:"limit"`
	Offset  int   `json:"offset"`
	HasMore bool  `json:"hasMore"`
}

// CreateDiscoverySessionRequest is the request body for creating a session
type CreateDiscoverySessionRequest struct {
	SearchCriteria    json.RawMessage `json:"searchCriteria"`
	Location          string          `json:"location"`
	PropertyCount     int             `json:"propertyCount"`
	CachedPropertyIds []string        `json:"cachedPropertyIds"`
}

// LinkActivityRequest is the request body for linking an activity
type LinkActivityRequest struct {
	ActivityType string `json:"activityType"` // CHAT_SESSION, EVALUATION
	ActivityId   string `json:"activityId"`
}

// SaveEvaluationsRequest is the request body for saving evaluations to a session
type SaveEvaluationsRequest struct {
	Evaluations []SessionEvaluationInput `json:"evaluations" validate:"required,min=1"`
}

// SessionEvaluationInput represents an evaluation to save
type SessionEvaluationInput struct {
	PropertyID     string          `json:"propertyId" validate:"required"`
	Address        string          `json:"address" validate:"required"`
	Price          int             `json:"price" validate:"required"`
	EstimatedRent  int             `json:"estimatedRent,omitempty"`
	Scenarios      json.RawMessage `json:"scenarios" validate:"required"`
	Recommendation string          `json:"recommendation,omitempty"`
	RiskLevel      string          `json:"riskLevel,omitempty"`
	Score          int             `json:"score,omitempty"`
	Status         string          `json:"status,omitempty"`
}

// SessionEvaluationResponse represents a saved evaluation in responses
type SessionEvaluationResponse struct {
	ID             string          `json:"id"`
	PropertyID     string          `json:"propertyId"`
	Address        string          `json:"address"`
	Price          int             `json:"price"`
	EstimatedRent  *int            `json:"estimatedRent,omitempty"`
	Scenarios      json.RawMessage `json:"scenarios"`
	Recommendation *string         `json:"recommendation,omitempty"`
	RiskLevel      *string         `json:"riskLevel,omitempty"`
	Score          *int            `json:"score,omitempty"`
	Status         string          `json:"status"`
	CreatedAt      string          `json:"createdAt"`
}

// discoverySessionRow represents a row from the discovery_sessions table
type discoverySessionRow struct {
	ID               string
	UserID           string
	SearchCriteria   json.RawMessage
	Location         string
	PropertyCount    int
	CachedPropertyIds []string
	Name             *string
	Notes            *string
	Status           string
	ChatSessionCount int
	EvaluationCount  int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LastAccessedAt   time.Time
	ArchivedAt       *time.Time
	ExpiresAt        time.Time
}

// discoveryActivityRow represents a row from the discovery_session_activities table
type discoveryActivityRow struct {
	ID           string
	SessionID    string
	ActivityType string
	ActivityId   string
	CreatedAt    time.Time
}

// ListDiscoverySessions handles GET /api/v2/discover/sessions
func (h *Handler) ListDiscoverySessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}
	userID := user.UserID

	// Parse query parameters
	status := httputil.GetQueryParam(r, "status", "ACTIVE")
	if status != "ACTIVE" && status != "ARCHIVED" {
		status = "ACTIVE"
	}

	limit, _ := strconv.Atoi(httputil.GetQueryParam(r, "limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	offset, _ := strconv.Atoi(httputil.GetQueryParam(r, "offset", "0"))
	if offset < 0 {
		offset = 0
	}

	// Get sessions
	rows, err := h.db.Main.Query(ctx, `
		SELECT id, "userId", "searchCriteria", location, "propertyCount",
		       "cachedPropertyIds", name, notes, status, "chatSessionCount",
		       "evaluationCount", "createdAt", "updatedAt", "lastAccessedAt",
		       "archivedAt", "expiresAt"
		FROM discovery_sessions
		WHERE "userId" = $1 AND status = $2
		ORDER BY "lastAccessedAt" DESC
		LIMIT $3 OFFSET $4
	`, userID, status, limit, offset)
	if err != nil {
		h.logger.Error("failed to list discovery sessions", "error", err, "userId", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	defer rows.Close()

	var sessions []discoverySessionRow
	for rows.Next() {
		var s discoverySessionRow
		err := rows.Scan(
			&s.ID, &s.UserID, &s.SearchCriteria, &s.Location, &s.PropertyCount,
			&s.CachedPropertyIds, &s.Name, &s.Notes, &s.Status, &s.ChatSessionCount,
			&s.EvaluationCount, &s.CreatedAt, &s.UpdatedAt, &s.LastAccessedAt,
			&s.ArchivedAt, &s.ExpiresAt,
		)
		if err != nil {
			h.logger.Warn("failed to scan discovery session row", "error", err)
			continue
		}
		sessions = append(sessions, s)
	}

	// Get total count
	var total int64
	err = h.db.Main.QueryRow(ctx, `
		SELECT COUNT(*) FROM discovery_sessions
		WHERE "userId" = $1 AND status = $2
	`, userID, status).Scan(&total)
	if err != nil {
		h.logger.Error("failed to count discovery sessions", "error", err, "userId", userID)
		total = 0
	}

	// Transform to response
	responseSessions := make([]DiscoverySessionResponse, len(sessions))
	for i, s := range sessions {
		responseSessions[i] = sessionRowToResponse(s)
	}

	response := ListDiscoverySessionsResponse{
		Success:  true,
		Sessions: responseSessions,
		Pagination: PaginationInfo{
			Total:   total,
			Limit:   limit,
			Offset:  offset,
			HasMore: int64(offset+len(sessions)) < total,
		},
	}

	httputil.JSON(w, http.StatusOK, response)
}

// GetDiscoverySession handles GET /api/v2/discover/sessions/{id}
func (h *Handler) GetDiscoverySession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}
	userID := user.UserID

	sessionID := r.PathValue("id")
	if sessionID == "" {
		httputil.BadRequest(w, "session id is required")
		return
	}

	// Get session
	var s discoverySessionRow
	err := h.db.Main.QueryRow(ctx, `
		SELECT id, "userId", "searchCriteria", location, "propertyCount",
		       "cachedPropertyIds", name, notes, status, "chatSessionCount",
		       "evaluationCount", "createdAt", "updatedAt", "lastAccessedAt",
		       "archivedAt", "expiresAt"
		FROM discovery_sessions
		WHERE id = $1 AND "userId" = $2
	`, sessionID, userID).Scan(
		&s.ID, &s.UserID, &s.SearchCriteria, &s.Location, &s.PropertyCount,
		&s.CachedPropertyIds, &s.Name, &s.Notes, &s.Status, &s.ChatSessionCount,
		&s.EvaluationCount, &s.CreatedAt, &s.UpdatedAt, &s.LastAccessedAt,
		&s.ArchivedAt, &s.ExpiresAt,
	)
	if err != nil {
		httputil.NotFound(w, "session not found")
		return
	}

	// Update last accessed time
	_, _ = h.db.Main.Exec(ctx, `
		UPDATE discovery_sessions SET
			"lastAccessedAt" = NOW(),
			"updatedAt" = NOW()
		WHERE id = $1
	`, sessionID)

	// Get activities
	activityRows, err := h.db.Main.Query(ctx, `
		SELECT id, "discoverySessionId", "activityType", "activityId", "createdAt"
		FROM discovery_session_activities
		WHERE "discoverySessionId" = $1
		ORDER BY "createdAt" DESC
	`, sessionID)
	if err != nil {
		h.logger.Warn("failed to list session activities", "error", err, "sessionId", sessionID)
	}

	var activities []DiscoveryActivityResponse
	if activityRows != nil {
		defer activityRows.Close()
		for activityRows.Next() {
			var a discoveryActivityRow
			if err := activityRows.Scan(&a.ID, &a.SessionID, &a.ActivityType, &a.ActivityId, &a.CreatedAt); err != nil {
				continue
			}
			activities = append(activities, DiscoveryActivityResponse{
				ID:           a.ID,
				ActivityType: a.ActivityType,
				ActivityId:   a.ActivityId,
				CreatedAt:    a.CreatedAt.Format(time.RFC3339),
			})
		}
	}

	// Get properties from separate table
	propRows, err := h.db.Main.Query(ctx, `
		SELECT "listingId", address, city, state, "zipCode", price, "estimatedRent",
		       "capRateMin", "capRateMax", beds, baths, sqft, "yearBuilt", "propertyType",
		       "listingDate", "daysOnMarket", "imageUrl", "listingSearchUrl", "googleSearchUrl",
		       latitude, longitude
		FROM discovery_session_properties
		WHERE "discoverySessionId" = $1
		ORDER BY price ASC
	`, sessionID)
	if err != nil {
		h.logger.Warn("failed to list session properties", "error", err, "sessionId", sessionID)
	}

	var properties []V2PropertyResult
	if propRows != nil {
		defer propRows.Close()
		for propRows.Next() {
			var p V2PropertyResult
			var capRateMin, capRateMax *float64
			err := propRows.Scan(
				&p.ID, &p.Address, &p.City, &p.State, &p.ZipCode, &p.Price, &p.EstimatedRent,
				&capRateMin, &capRateMax, &p.Beds, &p.Baths, &p.Sqft, &p.YearBuilt, &p.PropertyType,
				&p.ListingDate, &p.DaysOnMarket, &p.ImageUrl, &p.ListingSearchUrl, &p.GoogleSearchUrl,
				&p.Latitude, &p.Longitude,
			)
			if err != nil {
				h.logger.Warn("failed to scan property row", "error", err)
				continue
			}
			// Reconstruct cap rate range
			if capRateMin != nil && capRateMax != nil {
				p.CapRateRange = &CapRateRange{Min: *capRateMin, Max: *capRateMax}
			}
			properties = append(properties, p)
		}
	}

	// Get evaluations
	evalRows, err := h.db.Main.Query(ctx, `
		SELECT id, "propertyId", address, price, "estimatedRent", scenarios,
		       recommendation, "riskLevel", score, status, "createdAt"
		FROM discovery_session_evaluations
		WHERE "discoverySessionId" = $1
		ORDER BY "createdAt" DESC
	`, sessionID)
	if err != nil {
		h.logger.Warn("failed to list session evaluations", "error", err, "sessionId", sessionID)
	}

	var evaluations []SessionEvaluationResponse
	if evalRows != nil {
		defer evalRows.Close()
		for evalRows.Next() {
			var e SessionEvaluationResponse
			var estimatedRent *int
			var recommendation, riskLevel *string
			var score *int
			var createdAt time.Time
			err := evalRows.Scan(
				&e.ID, &e.PropertyID, &e.Address, &e.Price, &estimatedRent, &e.Scenarios,
				&recommendation, &riskLevel, &score, &e.Status, &createdAt,
			)
			if err != nil {
				h.logger.Warn("failed to scan evaluation row", "error", err)
				continue
			}
			e.EstimatedRent = estimatedRent
			e.Recommendation = recommendation
			e.RiskLevel = riskLevel
			e.Score = score
			e.CreatedAt = createdAt.Format(time.RFC3339)
			evaluations = append(evaluations, e)
		}
	}

	response := struct {
		Success bool                           `json:"success"`
		Session DiscoverySessionDetailResponse `json:"session"`
	}{
		Success: true,
		Session: DiscoverySessionDetailResponse{
			DiscoverySessionResponse: sessionRowToResponse(s),
			CachedPropertyIds:        s.CachedPropertyIds,
			Properties:               properties,
			Activities:               activities,
			Evaluations:              evaluations,
		},
	}

	httputil.JSON(w, http.StatusOK, response)
}

// CreateDiscoverySession handles POST /api/v2/discover/sessions
func (h *Handler) CreateDiscoverySession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}
	userID := user.UserID

	var req CreateDiscoverySessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if req.Location == "" {
		httputil.BadRequest(w, "location is required")
		return
	}

	sessionID := uuid.New().String()

	var s discoverySessionRow
	err := h.db.Main.QueryRow(ctx, `
		INSERT INTO discovery_sessions (
			id, "userId", "searchCriteria", location, "propertyCount",
			"cachedPropertyIds", status, "createdAt", "updatedAt", "lastAccessedAt", "expiresAt"
		) VALUES (
			$1, $2, $3, $4, $5, $6, 'ACTIVE', NOW(), NOW(), NOW(), NOW() + INTERVAL '180 days'
		) RETURNING id, "userId", "searchCriteria", location, "propertyCount",
		           "cachedPropertyIds", name, notes, status, "chatSessionCount",
		           "evaluationCount", "createdAt", "updatedAt", "lastAccessedAt",
		           "archivedAt", "expiresAt"
	`, sessionID, userID, req.SearchCriteria, req.Location, req.PropertyCount, req.CachedPropertyIds).Scan(
		&s.ID, &s.UserID, &s.SearchCriteria, &s.Location, &s.PropertyCount,
		&s.CachedPropertyIds, &s.Name, &s.Notes, &s.Status, &s.ChatSessionCount,
		&s.EvaluationCount, &s.CreatedAt, &s.UpdatedAt, &s.LastAccessedAt,
		&s.ArchivedAt, &s.ExpiresAt,
	)
	if err != nil {
		h.logger.Error("failed to create discovery session", "error", err, "userId", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	h.logger.Info("created discovery session",
		"sessionId", sessionID,
		"userId", userID,
		"location", req.Location,
		"propertyCount", req.PropertyCount,
	)

	response := struct {
		Success bool                     `json:"success"`
		Session DiscoverySessionResponse `json:"session"`
	}{
		Success: true,
		Session: sessionRowToResponse(s),
	}

	httputil.JSON(w, http.StatusCreated, response)
}

// ArchiveDiscoverySession handles DELETE /api/v2/discover/sessions/{id}
func (h *Handler) ArchiveDiscoverySession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}
	userID := user.UserID

	sessionID := r.PathValue("id")
	if sessionID == "" {
		httputil.BadRequest(w, "session id is required")
		return
	}

	var s discoverySessionRow
	err := h.db.Main.QueryRow(ctx, `
		UPDATE discovery_sessions SET
			status = 'ARCHIVED',
			"archivedAt" = NOW(),
			"updatedAt" = NOW()
		WHERE id = $1 AND "userId" = $2
		RETURNING id, "userId", "searchCriteria", location, "propertyCount",
		          "cachedPropertyIds", name, notes, status, "chatSessionCount",
		          "evaluationCount", "createdAt", "updatedAt", "lastAccessedAt",
		          "archivedAt", "expiresAt"
	`, sessionID, userID).Scan(
		&s.ID, &s.UserID, &s.SearchCriteria, &s.Location, &s.PropertyCount,
		&s.CachedPropertyIds, &s.Name, &s.Notes, &s.Status, &s.ChatSessionCount,
		&s.EvaluationCount, &s.CreatedAt, &s.UpdatedAt, &s.LastAccessedAt,
		&s.ArchivedAt, &s.ExpiresAt,
	)
	if err != nil {
		httputil.NotFound(w, "session not found")
		return
	}

	h.logger.Info("archived discovery session", "sessionId", sessionID, "userId", userID)

	response := struct {
		Success bool                     `json:"success"`
		Session DiscoverySessionResponse `json:"session"`
	}{
		Success: true,
		Session: sessionRowToResponse(s),
	}

	httputil.JSON(w, http.StatusOK, response)
}

// RestoreDiscoverySession handles POST /api/v2/discover/sessions/{id}/restore
func (h *Handler) RestoreDiscoverySession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}
	userID := user.UserID

	sessionID := r.PathValue("id")
	if sessionID == "" {
		httputil.BadRequest(w, "session id is required")
		return
	}

	var s discoverySessionRow
	err := h.db.Main.QueryRow(ctx, `
		UPDATE discovery_sessions SET
			status = 'ACTIVE',
			"archivedAt" = NULL,
			"updatedAt" = NOW()
		WHERE id = $1 AND "userId" = $2
		RETURNING id, "userId", "searchCriteria", location, "propertyCount",
		          "cachedPropertyIds", name, notes, status, "chatSessionCount",
		          "evaluationCount", "createdAt", "updatedAt", "lastAccessedAt",
		          "archivedAt", "expiresAt"
	`, sessionID, userID).Scan(
		&s.ID, &s.UserID, &s.SearchCriteria, &s.Location, &s.PropertyCount,
		&s.CachedPropertyIds, &s.Name, &s.Notes, &s.Status, &s.ChatSessionCount,
		&s.EvaluationCount, &s.CreatedAt, &s.UpdatedAt, &s.LastAccessedAt,
		&s.ArchivedAt, &s.ExpiresAt,
	)
	if err != nil {
		httputil.NotFound(w, "session not found")
		return
	}

	h.logger.Info("restored discovery session", "sessionId", sessionID, "userId", userID)

	response := struct {
		Success bool                     `json:"success"`
		Session DiscoverySessionResponse `json:"session"`
	}{
		Success: true,
		Session: sessionRowToResponse(s),
	}

	httputil.JSON(w, http.StatusOK, response)
}

// LinkActivity handles POST /api/v2/discover/sessions/{id}/link
func (h *Handler) LinkActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}
	userID := user.UserID

	sessionID := r.PathValue("id")
	if sessionID == "" {
		httputil.BadRequest(w, "session id is required")
		return
	}

	var req LinkActivityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if req.ActivityType == "" || req.ActivityId == "" {
		httputil.BadRequest(w, "activityType and activityId are required")
		return
	}

	if req.ActivityType != "CHAT_SESSION" && req.ActivityType != "EVALUATION" {
		httputil.BadRequest(w, "activityType must be CHAT_SESSION or EVALUATION")
		return
	}

	// Verify session belongs to user
	var sessionExists bool
	err := h.db.Main.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM discovery_sessions WHERE id = $1 AND "userId" = $2)
	`, sessionID, userID).Scan(&sessionExists)
	if err != nil || !sessionExists {
		httputil.NotFound(w, "session not found")
		return
	}

	// Create activity link
	linkID := uuid.New().String()
	_, err = h.db.Main.Exec(ctx, `
		INSERT INTO discovery_session_activities (
			id, "discoverySessionId", "activityType", "activityId", "createdAt"
		) VALUES (
			$1, $2, $3, $4, NOW()
		)
		ON CONFLICT ("discoverySessionId", "activityType", "activityId") DO NOTHING
	`, linkID, sessionID, req.ActivityType, req.ActivityId)
	if err != nil {
		h.logger.Warn("failed to create activity link", "error", err)
		// Don't return error - may already exist due to ON CONFLICT DO NOTHING
	}

	// Increment counter
	if req.ActivityType == "CHAT_SESSION" {
		_, _ = h.db.Main.Exec(ctx, `
			UPDATE discovery_sessions SET
				"chatSessionCount" = "chatSessionCount" + 1,
				"updatedAt" = NOW()
			WHERE id = $1
		`, sessionID)
	} else if req.ActivityType == "EVALUATION" {
		_, _ = h.db.Main.Exec(ctx, `
			UPDATE discovery_sessions SET
				"evaluationCount" = "evaluationCount" + 1,
				"updatedAt" = NOW()
			WHERE id = $1
		`, sessionID)
	}

	h.logger.Info("linked activity to discovery session",
		"sessionId", sessionID,
		"activityType", req.ActivityType,
		"activityId", req.ActivityId,
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// SaveEvaluations handles POST /api/v2/discover/sessions/{id}/evaluations
// Saves evaluation results to the discovery session for later retrieval
func (h *Handler) SaveEvaluations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}
	userID := user.UserID

	sessionID := r.PathValue("id")
	if sessionID == "" {
		httputil.BadRequest(w, "session id is required")
		return
	}

	var req SaveEvaluationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if len(req.Evaluations) == 0 {
		httputil.BadRequest(w, "at least one evaluation is required")
		return
	}

	// Verify session belongs to user
	var sessionExists bool
	err := h.db.Main.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM discovery_sessions WHERE id = $1 AND "userId" = $2)
	`, sessionID, userID).Scan(&sessionExists)
	if err != nil || !sessionExists {
		httputil.NotFound(w, "session not found")
		return
	}

	// Save evaluations (upsert)
	savedCount := 0
	for _, eval := range req.Evaluations {
		evalID := uuid.New().String()
		status := eval.Status
		if status == "" {
			status = "COMPLETED"
		}

		_, err := h.db.Main.Exec(ctx, `
			INSERT INTO discovery_session_evaluations (
				id, "discoverySessionId", "propertyId", address, price, "estimatedRent",
				scenarios, recommendation, "riskLevel", score, status, "createdAt"
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW()
			)
			ON CONFLICT ("discoverySessionId", "propertyId") DO UPDATE SET
				scenarios = EXCLUDED.scenarios,
				recommendation = EXCLUDED.recommendation,
				"riskLevel" = EXCLUDED."riskLevel",
				score = EXCLUDED.score,
				status = EXCLUDED.status
		`, evalID, sessionID, eval.PropertyID, eval.Address, eval.Price, eval.EstimatedRent,
			eval.Scenarios, eval.Recommendation, eval.RiskLevel, eval.Score, status)
		if err != nil {
			h.logger.Warn("failed to save evaluation", "error", err, "propertyId", eval.PropertyID)
			continue
		}
		savedCount++
	}

	// Update evaluation count on session
	_, _ = h.db.Main.Exec(ctx, `
		UPDATE discovery_sessions SET
			"evaluationCount" = (SELECT COUNT(*) FROM discovery_session_evaluations WHERE "discoverySessionId" = $1),
			"updatedAt" = NOW()
		WHERE id = $1
	`, sessionID)

	h.logger.Info("saved evaluations to discovery session",
		"sessionId", sessionID,
		"savedCount", savedCount,
		"totalCount", len(req.Evaluations),
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"savedCount": savedCount,
	})
}

// CreateDiscoverySessionForSearch creates a session as part of the search flow
// Returns the session ID or empty string on error
func (h *Handler) CreateDiscoverySessionForSearch(ctx context.Context, userID string, criteria json.RawMessage, location string, propertyCount int, propertyIds []string, properties []V2PropertyResult) string {
	if userID == "" {
		return ""
	}

	sessionID := uuid.New().String()

	_, err := h.db.Main.Exec(ctx, `
		INSERT INTO discovery_sessions (
			id, "userId", "searchCriteria", location, "propertyCount",
			"cachedPropertyIds", status, "createdAt", "updatedAt", "lastAccessedAt", "expiresAt"
		) VALUES (
			$1, $2, $3, $4, $5, $6, 'ACTIVE', NOW(), NOW(), NOW(), NOW() + INTERVAL '180 days'
		)
	`, sessionID, userID, criteria, location, propertyCount, propertyIds)
	if err != nil {
		h.logger.Error("failed to create discovery session for search", "error", err, "userId", userID)
		return ""
	}

	// Store properties in separate table
	for _, p := range properties {
		propID := uuid.New().String()
		var capRateMin, capRateMax *float64
		if p.CapRateRange != nil {
			capRateMin = &p.CapRateRange.Min
			capRateMax = &p.CapRateRange.Max
		}

		_, err := h.db.Main.Exec(ctx, `
			INSERT INTO discovery_session_properties (
				id, "discoverySessionId", "listingId", address, city, state, "zipCode",
				price, "estimatedRent", "capRateMin", "capRateMax", beds, baths, sqft,
				"yearBuilt", "propertyType", "listingDate", "daysOnMarket", "imageUrl",
				"listingSearchUrl", "googleSearchUrl", latitude, longitude, "createdAt"
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, NOW()
			)
			ON CONFLICT ("discoverySessionId", "listingId") DO NOTHING
		`, propID, sessionID, p.ID, p.Address, p.City, p.State, p.ZipCode,
			p.Price, p.EstimatedRent, capRateMin, capRateMax, p.Beds, p.Baths, p.Sqft,
			p.YearBuilt, p.PropertyType, p.ListingDate, p.DaysOnMarket, p.ImageUrl,
			p.ListingSearchUrl, p.GoogleSearchUrl, p.Latitude, p.Longitude)
		if err != nil {
			h.logger.Warn("failed to store discovery property", "error", err, "listingId", p.ID)
		}
	}

	h.logger.Info("created discovery session for search",
		"sessionId", sessionID,
		"userId", userID,
		"location", location,
		"propertyCount", propertyCount,
	)

	return sessionID
}

// Helper functions

func sessionRowToResponse(s discoverySessionRow) DiscoverySessionResponse {
	resp := DiscoverySessionResponse{
		ID:               s.ID,
		Location:         s.Location,
		PropertyCount:    s.PropertyCount,
		SearchCriteria:   s.SearchCriteria,
		Name:             s.Name,
		Notes:            s.Notes,
		Status:           s.Status,
		ChatSessionCount: s.ChatSessionCount,
		EvaluationCount:  s.EvaluationCount,
		CreatedAt:        s.CreatedAt.Format(time.RFC3339),
		LastAccessedAt:   s.LastAccessedAt.Format(time.RFC3339),
		ExpiresAt:        s.ExpiresAt.Format(time.RFC3339),
	}

	if s.ArchivedAt != nil {
		archived := s.ArchivedAt.Format(time.RFC3339)
		resp.ArchivedAt = &archived
	}

	return resp
}

// RunDiscoveryCleanup runs the auto-archive and cleanup jobs
// Should be called periodically (e.g., daily via cron)
func RunDiscoveryCleanup(ctx context.Context, db interface {
	Main() interface {
		Exec(ctx context.Context, sql string, args ...interface{}) (interface{}, error)
	}
}, logger *slog.Logger) {
	// Auto-archive sessions older than 30 days
	_, err := db.Main().Exec(ctx, `
		UPDATE discovery_sessions SET
			status = 'ARCHIVED',
			"archivedAt" = NOW(),
			"updatedAt" = NOW()
		WHERE status = 'ACTIVE'
		  AND "createdAt" < NOW() - INTERVAL '30 days'
	`)
	if err != nil {
		logger.Error("failed to auto-archive old sessions", "error", err)
	} else {
		logger.Info("auto-archive job completed")
	}

	// Delete expired sessions (older than 180 days)
	_, err = db.Main().Exec(ctx, `
		DELETE FROM discovery_sessions
		WHERE "expiresAt" < NOW()
	`)
	if err != nil {
		logger.Error("failed to delete expired sessions", "error", err)
	} else {
		logger.Info("delete expired sessions job completed")
	}
}
