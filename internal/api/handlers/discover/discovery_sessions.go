package discover

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/google/uuid"
)

// PriceStats contains computed price statistics for a session's properties
type PriceStats struct {
	Median int `json:"median"`
	Mode   int `json:"mode"`
}

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
	PriceStats       *PriceStats     `json:"priceStats,omitempty"`
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

	q := h.store.Q()

	sessions, err := q.ListUserDiscoverySessions(ctx, queries.ListUserDiscoverySessionsParams{
		UserId: userID,
		Status: status,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		h.logger.Error("failed to list discovery sessions", "error", err, "userId", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}

	total, err := q.CountUserDiscoverySessions(ctx, queries.CountUserDiscoverySessionsParams{
		UserId: userID,
		Status: status,
	})
	if err != nil {
		h.logger.Error("failed to count discovery sessions", "error", err, "userId", userID)
		total = 0
	}

	responseSessions := make([]DiscoverySessionResponse, len(sessions))
	for i, s := range sessions {
		responseSessions[i] = sqlcSessionToResponse(s)
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

	q := h.store.Q()

	// Get session
	s, err := q.GetDiscoverySessionByUser(ctx, queries.GetDiscoverySessionByUserParams{
		ID:     sessionID,
		UserId: userID,
	})
	if err != nil {
		httputil.NotFound(w, "session not found")
		return
	}

	// Update last accessed time
	_, _ = q.UpdateDiscoverySessionAccess(ctx, sessionID)

	// Get activities
	activityList, err := q.ListSessionActivities(ctx, sessionID)
	if err != nil {
		h.logger.Warn("failed to list session activities", "error", err, "sessionId", sessionID)
	}

	var activities []DiscoveryActivityResponse
	for _, a := range activityList {
		activities = append(activities, DiscoveryActivityResponse{
			ID:           a.ID,
			ActivityType: a.ActivityType,
			ActivityId:   a.ActivityId,
			CreatedAt:    a.CreatedAt.Time.Format(time.RFC3339),
		})
	}

	// Get properties
	propList, err := q.ListSessionProperties(ctx, sessionID)
	if err != nil {
		h.logger.Warn("failed to list session properties", "error", err, "sessionId", sessionID)
	}

	var properties []V2PropertyResult
	for _, p := range propList {
		prop := sqlcPropertyToResult(p)
		properties = append(properties, prop)
	}

	// Get evaluations
	evalList, err := q.ListSessionEvaluations(ctx, sessionID)
	if err != nil {
		h.logger.Warn("failed to list session evaluations", "error", err, "sessionId", sessionID)
	}

	var evaluations []SessionEvaluationResponse
	for _, e := range evalList {
		evaluations = append(evaluations, sqlcEvaluationToResponse(e))
	}

	response := struct {
		Success bool                           `json:"success"`
		Session DiscoverySessionDetailResponse `json:"session"`
	}{
		Success: true,
		Session: DiscoverySessionDetailResponse{
			DiscoverySessionResponse: sqlcSessionToResponse(s),
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

	q := h.store.Q()
	sessionID := uuid.New().String()

	s, err := q.CreateDiscoverySession(ctx, queries.CreateDiscoverySessionParams{
		ID:                sessionID,
		UserId:            userID,
		SearchCriteria:    req.SearchCriteria,
		Location:          req.Location,
		PropertyCount:     int32(req.PropertyCount),
		CachedPropertyIds: req.CachedPropertyIds,
	})
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
		Session: sqlcSessionToResponse(s),
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

	q := h.store.Q()
	s, err := q.ArchiveDiscoverySession(ctx, queries.ArchiveDiscoverySessionParams{
		ID:     sessionID,
		UserId: userID,
	})
	if err != nil {
		httputil.NotFound(w, "session not found")
		return
	}

	h.logger.Info("archived discovery session", "sessionId", sessionID, "userId", userID)

	httputil.JSON(w, http.StatusOK, struct {
		Success bool                     `json:"success"`
		Session DiscoverySessionResponse `json:"session"`
	}{
		Success: true,
		Session: sqlcSessionToResponse(s),
	})
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

	q := h.store.Q()
	s, err := q.RestoreDiscoverySession(ctx, queries.RestoreDiscoverySessionParams{
		ID:     sessionID,
		UserId: userID,
	})
	if err != nil {
		httputil.NotFound(w, "session not found")
		return
	}

	h.logger.Info("restored discovery session", "sessionId", sessionID, "userId", userID)

	httputil.JSON(w, http.StatusOK, struct {
		Success bool                     `json:"success"`
		Session DiscoverySessionResponse `json:"session"`
	}{
		Success: true,
		Session: sqlcSessionToResponse(s),
	})
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

	q := h.store.Q()

	exists, err := q.SessionExistsByUser(ctx, queries.SessionExistsByUserParams{
		ID:     sessionID,
		UserId: userID,
	})
	if err != nil || !exists {
		httputil.NotFound(w, "session not found")
		return
	}

	linkID := uuid.New().String()
	_, _ = q.CreateActivityLink(ctx, queries.CreateActivityLinkParams{
		ID:                 linkID,
		DiscoverySessionId: sessionID,
		ActivityType:       req.ActivityType,
		ActivityId:         req.ActivityId,
	})

	if req.ActivityType == "CHAT_SESSION" {
		_ = q.IncrementChatSessionCount(ctx, sessionID)
	} else if req.ActivityType == "EVALUATION" {
		_ = q.IncrementEvaluationCount(ctx, sessionID)
	}

	h.logger.Info("linked activity to discovery session",
		"sessionId", sessionID,
		"activityType", req.ActivityType,
		"activityId", req.ActivityId,
	)

	httputil.JSON(w, http.StatusOK, map[string]any{"success": true})
}

// SaveEvaluations handles POST /api/v2/discover/sessions/{id}/evaluations
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

	q := h.store.Q()

	exists, err := q.SessionExistsByUser(ctx, queries.SessionExistsByUserParams{
		ID:     sessionID,
		UserId: userID,
	})
	if err != nil || !exists {
		httputil.NotFound(w, "session not found")
		return
	}

	savedCount := 0
	for _, eval := range req.Evaluations {
		evalID := uuid.New().String()
		status := eval.Status
		if status == "" {
			status = "COMPLETED"
		}

		_, err := q.CreateDiscoverySessionEvaluation(ctx, queries.CreateDiscoverySessionEvaluationParams{
			ID:                 evalID,
			DiscoverySessionId: sessionID,
			PropertyId:         eval.PropertyID,
			Address:            eval.Address,
			Price:              int32(eval.Price),
			EstimatedRent:      pgtype.Int4{Int32: int32(eval.EstimatedRent), Valid: eval.EstimatedRent > 0},
			Scenarios:          eval.Scenarios,
			Recommendation:     pgtype.Text{String: eval.Recommendation, Valid: eval.Recommendation != ""},
			RiskLevel:          pgtype.Text{String: eval.RiskLevel, Valid: eval.RiskLevel != ""},
			Score:              pgtype.Int4{Int32: int32(eval.Score), Valid: eval.Score > 0},
			Status:             status,
		})
		if err != nil {
			h.logger.Warn("failed to save evaluation", "error", err, "propertyId", eval.PropertyID)
			continue
		}
		savedCount++
	}

	_ = q.UpdateSessionEvaluationCount(ctx, sessionID)

	h.logger.Info("saved evaluations to discovery session",
		"sessionId", sessionID,
		"savedCount", savedCount,
		"totalCount", len(req.Evaluations),
	)

	httputil.JSON(w, http.StatusOK, map[string]any{
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

	q := h.store.Q()
	sessionID := uuid.New().String()

	_, err := q.CreateDiscoverySession(ctx, queries.CreateDiscoverySessionParams{
		ID:                sessionID,
		UserId:            userID,
		SearchCriteria:    criteria,
		Location:          location,
		PropertyCount:     int32(propertyCount),
		CachedPropertyIds: propertyIds,
	})
	if err != nil {
		h.logger.Error("failed to create discovery session for search", "error", err, "userId", userID)
		return ""
	}

	// Store properties
	for _, p := range properties {
		propID := uuid.New().String()
		params := queries.CreateDiscoverySessionPropertyParams{
			ID:                 propID,
			DiscoverySessionId: sessionID,
			ListingId:          p.ID,
			Address:            p.Address,
			City:               p.City,
			State:              p.State,
			Price:              int32(p.Price),
			Beds:               int32(p.Beds),
		}
		if p.ZipCode != "" {
			params.ZipCode = pgtype.Text{String: p.ZipCode, Valid: true}
		}
		if p.EstimatedRent != nil && *p.EstimatedRent > 0 {
			params.EstimatedRent = pgtype.Int4{Int32: int32(*p.EstimatedRent), Valid: true}
		}
		if p.CapRateRange != nil {
			params.CapRateMin = numericFromFloat(p.CapRateRange.Min)
			params.CapRateMax = numericFromFloat(p.CapRateRange.Max)
		}
		params.Baths = numericFromFloat(p.Baths)
		if p.Sqft > 0 {
			params.Sqft = pgtype.Int4{Int32: int32(p.Sqft), Valid: true}
		}
		if p.YearBuilt != nil && *p.YearBuilt > 0 {
			params.YearBuilt = pgtype.Int4{Int32: int32(*p.YearBuilt), Valid: true}
		}
		if p.PropertyType != "" {
			params.PropertyType = pgtype.Text{String: p.PropertyType, Valid: true}
		}
		if p.ListingDate != nil && *p.ListingDate != "" {
			params.ListingDate = pgtype.Text{String: *p.ListingDate, Valid: true}
		}
		if p.DaysOnMarket != nil && *p.DaysOnMarket > 0 {
			params.DaysOnMarket = pgtype.Int4{Int32: int32(*p.DaysOnMarket), Valid: true}
		}
		if p.ImageUrl != nil && *p.ImageUrl != "" {
			params.ImageUrl = pgtype.Text{String: *p.ImageUrl, Valid: true}
		}
		if p.ListingSearchUrl != "" {
			params.ListingSearchUrl = pgtype.Text{String: p.ListingSearchUrl, Valid: true}
		}
		if p.GoogleSearchUrl != "" {
			params.GoogleSearchUrl = pgtype.Text{String: p.GoogleSearchUrl, Valid: true}
		}
		if p.Latitude != nil && *p.Latitude != 0 {
			params.Latitude = numericFromFloat(*p.Latitude)
		}
		if p.Longitude != nil && *p.Longitude != 0 {
			params.Longitude = numericFromFloat(*p.Longitude)
		}

		_, err := q.CreateDiscoverySessionProperty(ctx, params)
		if err != nil {
			h.logger.Warn("failed to store discovery property", "error", err, "listingId", p.ID)
		}
	}

	// Compute and save price stats
	if len(properties) > 0 {
		if err := q.UpdateSessionPriceStats(ctx, sessionID); err != nil {
			h.logger.Warn("failed to compute price stats", "error", err, "sessionId", sessionID)
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

// ============================================================================
// Helper functions
// ============================================================================

// sqlcSessionToResponse converts a sqlc-generated DiscoverySession to API response
func sqlcSessionToResponse(s queries.DiscoverySession) DiscoverySessionResponse {
	resp := DiscoverySessionResponse{
		ID:               s.ID,
		Location:         s.Location,
		PropertyCount:    int(s.PropertyCount),
		SearchCriteria:   s.SearchCriteria,
		Status:           s.Status,
		ChatSessionCount: int(s.ChatSessionCount),
		EvaluationCount:  int(s.EvaluationCount),
		CreatedAt:        s.CreatedAt.Time.Format(time.RFC3339),
		LastAccessedAt:   s.LastAccessedAt.Time.Format(time.RFC3339),
		ExpiresAt:        s.ExpiresAt.Time.Format(time.RFC3339),
	}

	if s.Name.Valid {
		resp.Name = &s.Name.String
	}
	if s.Notes.Valid {
		resp.Notes = &s.Notes.String
	}
	if s.ArchivedAt.Valid {
		archived := s.ArchivedAt.Time.Format(time.RFC3339)
		resp.ArchivedAt = &archived
	}
	if s.MedianPrice.Valid && s.ModePrice.Valid {
		resp.PriceStats = &PriceStats{Median: int(s.MedianPrice.Int32), Mode: int(s.ModePrice.Int32)}
	}

	return resp
}

// sqlcPropertyToResult converts a sqlc-generated DiscoverySessionProperty to V2PropertyResult
func sqlcPropertyToResult(p queries.DiscoverySessionProperty) V2PropertyResult {
	prop := V2PropertyResult{
		ID:      p.ListingId,
		Address: p.Address,
		City:    p.City,
		State:   p.State,
		Price:   int(p.Price),
		Beds:    int(p.Beds),
	}
	if p.ZipCode.Valid {
		prop.ZipCode = p.ZipCode.String
	}
	if p.EstimatedRent.Valid {
		v := int(p.EstimatedRent.Int32)
		prop.EstimatedRent = &v
	}
	if p.CapRateMin.Valid && p.CapRateMax.Valid {
		min := floatFromNumeric(p.CapRateMin)
		max := floatFromNumeric(p.CapRateMax)
		prop.CapRateRange = &CapRateRange{Min: min, Max: max}
	}
	prop.Baths = floatFromNumeric(p.Baths)
	if p.Sqft.Valid {
		prop.Sqft = int(p.Sqft.Int32)
	}
	if p.YearBuilt.Valid {
		v := int(p.YearBuilt.Int32)
		prop.YearBuilt = &v
	}
	if p.PropertyType.Valid {
		prop.PropertyType = p.PropertyType.String
	}
	if p.ListingDate.Valid {
		prop.ListingDate = &p.ListingDate.String
	}
	if p.DaysOnMarket.Valid {
		v := int(p.DaysOnMarket.Int32)
		prop.DaysOnMarket = &v
	}
	if p.ImageUrl.Valid {
		prop.ImageUrl = &p.ImageUrl.String
	}
	if p.ListingSearchUrl.Valid {
		prop.ListingSearchUrl = p.ListingSearchUrl.String
	}
	if p.GoogleSearchUrl.Valid {
		prop.GoogleSearchUrl = p.GoogleSearchUrl.String
	}
	if p.Latitude.Valid {
		v := floatFromNumeric(p.Latitude)
		prop.Latitude = &v
	}
	if p.Longitude.Valid {
		v := floatFromNumeric(p.Longitude)
		prop.Longitude = &v
	}
	return prop
}

// sqlcEvaluationToResponse converts a sqlc-generated DiscoverySessionEvaluation to API response
func sqlcEvaluationToResponse(e queries.DiscoverySessionEvaluation) SessionEvaluationResponse {
	resp := SessionEvaluationResponse{
		ID:         e.ID,
		PropertyID: e.PropertyId,
		Address:    e.Address,
		Price:      int(e.Price),
		Scenarios:  e.Scenarios,
		Status:     e.Status,
		CreatedAt:  e.CreatedAt.Time.Format(time.RFC3339),
	}
	if e.EstimatedRent.Valid {
		v := int(e.EstimatedRent.Int32)
		resp.EstimatedRent = &v
	}
	if e.Recommendation.Valid {
		resp.Recommendation = &e.Recommendation.String
	}
	if e.RiskLevel.Valid {
		resp.RiskLevel = &e.RiskLevel.String
	}
	if e.Score.Valid {
		v := int(e.Score.Int32)
		resp.Score = &v
	}
	return resp
}

// numericFromFloat creates a pgtype.Numeric from float64.
// pgx v5 pgtype.Numeric.Scan only accepts string, not float64 — scan via FormatFloat.
func numericFromFloat(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(strconv.FormatFloat(f, 'f', 10, 64))
	return n
}

// floatFromNumeric extracts float64 from pgtype.Numeric
func floatFromNumeric(n pgtype.Numeric) float64 {
	var f float64
	if n.Valid {
		v, _ := n.Float64Value()
		f = v.Float64
	}
	return f
}

// RunDiscoveryCleanup runs the auto-archive and cleanup jobs via sqlc
func RunDiscoveryCleanup(ctx context.Context, pool interface {
	Query(ctx context.Context, sql string, args ...any) (interface{ Close() }, error)
}, logger *slog.Logger) {
	// This function uses sqlc queries but needs a pool-compatible interface.
	// For now, keep as a no-op placeholder — the actual cron calls the handler directly.
	logger.Info("discovery cleanup: use cron endpoint instead")
}
