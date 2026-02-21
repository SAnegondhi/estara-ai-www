package ai

// ============================================================
// ADR-088 Phase 13: Saved Analyses CRUD + Share Tokens
// ============================================================
// GET    /api/ai/frontier/runs           → ListFrontierRuns
// POST   /api/ai/frontier/runs           → SaveFrontierRun
// GET    /api/ai/frontier/runs/{id}      → GetFrontierRun
// DELETE /api/ai/frontier/runs/{id}      → DeleteFrontierRun
// POST   /api/ai/frontier/runs/{id}/share → EnableShare
// DELETE /api/ai/frontier/runs/{id}/share → RevokeShare
// GET    /api/share/{token}              → GetSharedRun (no auth)

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/pkg/httputil"
)

// ----------------------------------------------------------------
// Request / Response types
// ----------------------------------------------------------------

// SaveFrontierRunRequest is the body for POST /api/ai/frontier/runs.
type SaveFrontierRunRequest struct {
	Name           string                     `json:"name"`
	Criteria       json.RawMessage            `json:"criteria"`
	FrontierPoints []investment.FrontierPoint `json:"frontierPoints"`
	PropertyCount  int                        `json:"propertyCount"`
}

// FrontierRunSummaryResponse is a single row in the list response.
type FrontierRunSummaryResponse struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Locations     []string `json:"locations"`
	PropertyCount int      `json:"propertyCount"`
	ShareEnabled  bool     `json:"shareEnabled"`
	ShareURL      string   `json:"shareUrl,omitempty"`
	CreatedAt     string   `json:"createdAt"`
}

// FrontierRunDetailResponse is returned by GET /runs/{id} and GET /api/share/{token}.
type FrontierRunDetailResponse struct {
	FrontierRunSummaryResponse
	Criteria       json.RawMessage            `json:"criteria"`
	FrontierPoints []investment.FrontierPoint `json:"frontierPoints"`
	Watermark      string                     `json:"watermark,omitempty"` // public only
}

// ----------------------------------------------------------------
// ListFrontierRuns — GET /api/ai/frontier/runs
// ----------------------------------------------------------------

func (h *Handler) ListFrontierRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// Parse optional pagination query params (?limit=N&offset=M)
	limit := int32(10)
	offset := int32(0)
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.ParseInt(l, 10, 32); err == nil && v > 0 && v <= 50 {
			limit = int32(v)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.ParseInt(o, 10, 32); err == nil && v >= 0 {
			offset = int32(v)
		}
	}

	rows, err := h.store.Q().ListFrontierRuns(ctx, queries.ListFrontierRunsParams{
		UserID: user.ID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		h.logger.Error("list frontier runs failed", "error", err, "userId", user.ID)
		httputil.Error(w, http.StatusInternalServerError, "failed to list runs")
		return
	}

	runs := make([]FrontierRunSummaryResponse, 0, len(rows))
	for _, row := range rows {
		r := frontierRunSummaryFromRow(row, h.cfg.Server.ClientURL)
		runs = append(runs, r)
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{"runs": runs})
}

// ----------------------------------------------------------------
// SaveFrontierRun — POST /api/ai/frontier/runs
// ----------------------------------------------------------------

func (h *Handler) SaveFrontierRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req SaveFrontierRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	if len(req.FrontierPoints) == 0 {
		httputil.BadRequest(w, "frontierPoints required")
		return
	}

	// Auto-generate name from criteria if not supplied
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Analysis " + time.Now().Format("Jan 2, 2006")
	}

	// Extract locations from criteria for display / filtering
	locations := extractLocationsFromCriteria(req.Criteria)

	// Serialize frontier points
	pointsJSON, err := json.Marshal(req.FrontierPoints)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to serialize frontier points")
		return
	}

	id := uuid.New().String()
	run, err := h.store.Q().InsertFrontierRun(ctx, queries.InsertFrontierRunParams{
		ID:             id,
		UserID:         user.ID,
		Name:           name,
		Locations:      locations,
		Criteria:       req.Criteria,
		FrontierPoints: json.RawMessage(pointsJSON),
		PropertyCount:  int32(req.PropertyCount),
	})
	if err != nil {
		h.logger.Error("save frontier run failed", "error", err, "userId", user.ID)
		httputil.Error(w, http.StatusInternalServerError, "failed to save run")
		return
	}

	httputil.JSON(w, http.StatusCreated, map[string]interface{}{
		"id":        run.ID,
		"name":      run.Name,
		"createdAt": run.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// ----------------------------------------------------------------
// GetFrontierRun — GET /api/ai/frontier/runs/{id}
// ----------------------------------------------------------------

func (h *Handler) GetFrontierRunByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	id := chi.URLParam(r, "id")
	run, err := h.store.Q().GetFrontierRun(ctx, queries.GetFrontierRunParams{
		ID:     id,
		UserID: user.ID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "run not found")
			return
		}
		h.logger.Error("get frontier run failed", "error", err, "userId", user.ID, "runId", id)
		httputil.Error(w, http.StatusInternalServerError, "failed to get run")
		return
	}

	// Touch accessed_at async — use Background context so it survives response send.
	go func() {
		if err := h.store.Q().TouchFrontierRun(context.Background(), run.ID); err != nil {
			h.logger.Warn("touch frontier run failed", "error", err, "runId", run.ID)
		}
	}()

	detail, err := frontierRunDetail(run, h.cfg.Server.ClientURL, "")
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to decode run")
		return
	}

	httputil.JSON(w, http.StatusOK, detail)
}

// ----------------------------------------------------------------
// DeleteFrontierRun — DELETE /api/ai/frontier/runs/{id}
// ----------------------------------------------------------------

func (h *Handler) DeleteFrontierRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	id := chi.URLParam(r, "id")
	if err := h.store.Q().DeleteFrontierRun(ctx, queries.DeleteFrontierRunParams{
		ID:     id,
		UserID: user.ID,
	}); err != nil {
		h.logger.Error("delete frontier run failed", "error", err, "userId", user.ID, "runId", id)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete run")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------
// EnableShare — POST /api/ai/frontier/runs/{id}/share
// ----------------------------------------------------------------

func (h *Handler) EnableShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	id := chi.URLParam(r, "id")

	// Generate cryptographically random share token, retrying on the extremely rare
	// UNIQUE constraint collision (pgx error code 23505).
	// 16 random bytes → base64url (no padding) → exactly 22 URL-safe characters.
	const maxTokenAttempts = 3
	var shareToken string
	var tokenErr error

	for attempt := range maxTokenAttempts {
		tokenBytes := make([]byte, 16)
		if _, readErr := rand.Read(tokenBytes); readErr != nil {
			h.logger.Error("failed to generate share token", "error", readErr)
			httputil.Error(w, http.StatusInternalServerError, "failed to generate share token")
			return
		}
		shareToken = base64.RawURLEncoding.EncodeToString(tokenBytes)

		tokenErr = h.store.Q().SetFrontierRunShareToken(ctx, queries.SetFrontierRunShareTokenParams{
			ID:         id,
			UserID:     user.ID,
			ShareToken: pgtype.Text{String: shareToken, Valid: true},
		})
		if tokenErr == nil {
			break // success
		}

		// Retry only on UNIQUE violation (collision); surface all other errors immediately.
		var pgErr *pgconn.PgError
		if errors.As(tokenErr, &pgErr) && pgErr.Code == "23505" {
			h.logger.Warn("share token collision, retrying", "attempt", attempt+1, "runId", id)
			continue
		}
		break // non-collision error — exit retry loop
	}

	if tokenErr != nil {
		h.logger.Error("enable share failed", "error", tokenErr, "userId", user.ID, "runId", id)
		httputil.Error(w, http.StatusInternalServerError, "failed to enable sharing")
		return
	}

	shareURL := fmt.Sprintf("%s/s/%s", h.cfg.Server.ClientURL, shareToken)
	httputil.JSON(w, http.StatusOK, map[string]string{"shareUrl": shareURL})
}

// ----------------------------------------------------------------
// RevokeShare — DELETE /api/ai/frontier/runs/{id}/share
// ----------------------------------------------------------------

func (h *Handler) RevokeShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	id := chi.URLParam(r, "id")
	if err := h.store.Q().ClearFrontierRunShareToken(ctx, queries.ClearFrontierRunShareTokenParams{
		ID:     id,
		UserID: user.ID,
	}); err != nil {
		h.logger.Error("revoke share failed", "error", err, "userId", user.ID, "runId", id)
		httputil.Error(w, http.StatusInternalServerError, "failed to revoke sharing")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------
// GetSharedRun — GET /api/share/{token} (no auth)
// ----------------------------------------------------------------

func (h *Handler) GetSharedRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")

	run, err := h.store.Q().GetFrontierRunByToken(ctx, pgtype.Text{String: token, Valid: true})
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "analysis not found or sharing has been revoked")
			return
		}
		h.logger.Error("get shared run failed", "error", err, "token", token)
		httputil.Error(w, http.StatusInternalServerError, "failed to get shared run")
		return
	}

	// Touch async — use Background context so it survives response send.
	go func() {
		if err := h.store.Q().TouchFrontierRun(context.Background(), run.ID); err != nil {
			h.logger.Warn("touch shared run failed", "error", err, "token", token)
		}
	}()

	detail, err := frontierRunDetail(run, h.cfg.Server.ClientURL, "Generated via Estara Insight")
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to decode run")
		return
	}

	httputil.JSON(w, http.StatusOK, detail)
}

// ----------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------

// frontierRunSummaryFromRow converts a ListFrontierRunsRow to a summary response.
func frontierRunSummaryFromRow(row queries.ListFrontierRunsRow, baseURL string) FrontierRunSummaryResponse {
	r := FrontierRunSummaryResponse{
		ID:            row.ID,
		Name:          row.Name,
		Locations:     row.Locations,
		PropertyCount: int(row.PropertyCount),
		ShareEnabled:  row.ShareToken.Valid,
		CreatedAt:     row.CreatedAt.UTC().Format(time.RFC3339),
	}
	if row.ShareToken.Valid && baseURL != "" {
		r.ShareURL = fmt.Sprintf("%s/s/%s", baseURL, row.ShareToken.String)
	}
	return r
}

// frontierRunDetail decodes a full FrontierRun DB row into a detail response.
func frontierRunDetail(run queries.FrontierRun, baseURL string, watermark string) (FrontierRunDetailResponse, error) {
	var points []investment.FrontierPoint
	if err := json.Unmarshal(run.FrontierPoints, &points); err != nil {
		return FrontierRunDetailResponse{}, fmt.Errorf("decode frontier points: %w", err)
	}

	summary := FrontierRunSummaryResponse{
		ID:            run.ID,
		Name:          run.Name,
		Locations:     run.Locations,
		PropertyCount: int(run.PropertyCount),
		ShareEnabled:  run.ShareToken.Valid,
		CreatedAt:     run.CreatedAt.UTC().Format(time.RFC3339),
	}
	if run.ShareToken.Valid && baseURL != "" {
		summary.ShareURL = fmt.Sprintf("%s/s/%s", baseURL, run.ShareToken.String)
	}

	return FrontierRunDetailResponse{
		FrontierRunSummaryResponse: summary,
		Criteria:                   run.Criteria,
		FrontierPoints:             points,
		Watermark:                  watermark,
	}, nil
}

// extractLocationsFromCriteria parses the criteria JSONB and extracts the locations array.
// Returns []string{} (never nil) so the JSON response always encodes as [] not null.
func extractLocationsFromCriteria(criteria json.RawMessage) []string {
	if len(criteria) == 0 {
		return []string{}
	}
	var c struct {
		Locations []string `json:"locations"`
	}
	if err := json.Unmarshal(criteria, &c); err != nil {
		return []string{}
	}
	if c.Locations == nil {
		return []string{}
	}
	return c.Locations
}
