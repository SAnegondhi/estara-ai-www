package discover

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/estara-ai/www/internal/api/handlers/util"
	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/internal/services/memo"
	"github.com/estara-ai/www/internal/services/pdf"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/estara-ai/www/pkg/sse"
)

// memoService is set via SetMemoService after handler construction.
var memoJobQueue *queue.Queue
var memoPDFBuilder *pdf.DecisionMemoBuilder

// SetMemoService injects the memo service dependencies into the discover handler.
func (h *Handler) SetMemoService(q *queue.Queue) {
	memoJobQueue = q
}

// SetMemoPDFBuilder injects the PDF builder.
func (h *Handler) SetMemoPDFBuilder(b *pdf.DecisionMemoBuilder) {
	memoPDFBuilder = b
}

// DecisionMemoRequest is the request body for queuing memo generation.
type DecisionMemoRequest struct {
	Properties   []memo.BatchPropertyInput `json:"properties" validate:"required,min=1,max=10,dive"`
	Strategy     string                    `json:"strategy"`
	ForceRefresh bool                      `json:"forceRefresh"`
}

// DecisionMemoResponse is returned when a memo job is queued.
type DecisionMemoResponse struct {
	Success bool   `json:"success"`
	JobID   string `json:"jobId,omitempty"`
	Cached  bool   `json:"cached"`
}

// QueueDecisionMemos handles POST /api/v2/discover/decision-memo
// Queues a background job for memo generation.
func (h *Handler) QueueDecisionMemos(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "unauthorized")
		return
	}

	var req DecisionMemoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	if len(req.Properties) == 0 {
		httputil.BadRequest(w, "at least one property is required")
		return
	}
	if len(req.Properties) > 10 {
		httputil.BadRequest(w, "maximum 10 properties per memo request")
		return
	}

	if req.Strategy == "" {
		req.Strategy = "balanced"
	}

	h.logger.Info("queueing decision memo",
		"user_id", user.UserID,
		"properties", len(req.Properties),
		"strategy", req.Strategy,
	)

	// Serialize properties for job payload
	propsJSON, err := json.Marshal(req.Properties)
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("failed to serialize properties: %w", err))
		return
	}

	job := queue.NewJob(queue.JobTypeDecisionMemo, user.UserID, map[string]interface{}{
		"properties":   json.RawMessage(propsJSON),
		"strategy":     req.Strategy,
		"forceRefresh": req.ForceRefresh,
		"userId":       user.UserID,
	})

	if h.redis == nil || memoJobQueue == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "memo service not available")
		return
	}

	jobID, err := memoJobQueue.Enqueue(job)
	if err != nil {
		h.logger.Error("failed to enqueue memo job", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to queue memo generation")
		return
	}

	// Log feature usage for customer support and chargeback defense
	_ = util.LogFeatureUsage(ctx, h.store, r, user.UserID, "FEATURE_DECISION_MEMO",
		fmt.Sprintf("Decision memo queued: %d properties", len(req.Properties)),
		map[string]any{
			"jobId":         jobID,
			"propertyCount": len(req.Properties),
			"strategy":      req.Strategy,
			"forceRefresh":  req.ForceRefresh,
		},
	)

	httputil.JSON(w, http.StatusAccepted, map[string]interface{}{
		"success": true,
		"jobId":   jobID,
		"cached":  false,
	})
}

// StreamDecisionMemoProgress handles GET /api/v2/discover/decision-memo/{jobId}/stream
// SSE endpoint for memo generation progress.
func (h *Handler) StreamDecisionMemoProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "jobId")

	if jobID == "" {
		httputil.BadRequest(w, "jobId required")
		return
	}

	// Auth handled by middleware — user is already validated
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "unauthorized")
		return
	}

	logger := slog.Default().With("component", "memo_stream", "job_id", jobID)
	logger.Info("starting memo stream", "user_id", user.UserID)

	sseWriter, err := sse.NewWriter(w)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	if memoJobQueue == nil {
		sseWriter.WriteError("memo service not available")
		return
	}

	// Subscribe to job progress
	progressCh := memoJobQueue.Subscribe(jobID)
	if progressCh == nil {
		// Job may already be complete
		result, err := memoJobQueue.GetResult(jobID)
		if err == nil && result != nil {
			sseWriter.WriteEventJSON("complete", map[string]interface{}{
				"jobId":  jobID,
				"result": result.Data,
			})
			return
		}
		sseWriter.WriteError("job not found")
		return
	}

	// Start heartbeat
	stopHeartbeat := sseWriter.StartHeartbeat(15 * time.Second)
	defer close(stopHeartbeat)

	for {
		select {
		case <-ctx.Done():
			logger.Info("memo stream context cancelled")
			return

		case event, ok := <-progressCh:
			if !ok {
				result, err := memoJobQueue.GetResult(jobID)
				if err != nil {
					sseWriter.WriteEventJSON("error", map[string]interface{}{
						"jobId": jobID,
						"error": "failed to get result",
					})
				} else if result.Status == queue.JobStatusCompleted {
					sseWriter.WriteEventJSON("complete", map[string]interface{}{
						"jobId":  jobID,
						"result": result.Data,
					})
				} else {
					sseWriter.WriteEventJSON("error", map[string]interface{}{
						"jobId": jobID,
						"error": result.Error,
					})
				}
				return
			}

			sseWriter.WriteEventJSON("progress", map[string]interface{}{
				"jobId":    event.JobID,
				"progress": event.Progress,
				"message":  event.Message,
			})
		}
	}
}

// MemoHistoryEntry is one item in the memo history list.
type MemoHistoryEntry struct {
	ID            string    `json:"id"`
	Key           string    `json:"key"`
	Location      string    `json:"location"`
	CreatedAt     time.Time `json:"createdAt"`
	PropertyCount int       `json:"propertyCount"`
	Preview       string    `json:"preview"`
}

// GetMemoHistory handles GET /api/v2/discover/decision-memo/history
// Returns cached memos for the current user.
func (h *Handler) GetMemoHistory(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUserFromContext(r.Context())
	if user == nil {
		httputil.Unauthorized(w, "unauthorized")
		return
	}

	if h.store == nil {
		httputil.JSON(w, http.StatusOK, map[string]interface{}{"entries": []MemoHistoryEntry{}})
		return
	}

	q := h.store.Q()
	rows, err := q.ListCacheByUserAndFeature(r.Context(), queries.ListCacheByUserAndFeatureParams{
		UserId:  user.UserID,
		Feature: "decision_memo",
		Limit:   20,
		Offset:  0,
	})
	if err != nil {
		h.logger.Error("failed to list memo history", "error", err)
		httputil.JSON(w, http.StatusOK, map[string]interface{}{"entries": []MemoHistoryEntry{}})
		return
	}

	entries := make([]MemoHistoryEntry, 0, len(rows))
	for _, row := range rows {
		entry := MemoHistoryEntry{
			ID:       row.ID,
			Key:      row.Key,
			Location: row.Location,
		}
		if row.CreatedAt.Valid {
			entry.CreatedAt = row.CreatedAt.Time
		}
		// Extract propertyCount and preview from metadata
		if len(row.Metadata) > 0 {
			var meta map[string]interface{}
			if err := json.Unmarshal(row.Metadata, &meta); err == nil {
				if cnt, ok := meta["propertyCount"].(float64); ok {
					entry.PropertyCount = int(cnt)
				}
				if prev, ok := meta["preview"].(string); ok {
					entry.Preview = prev
				}
			}
		}
		entries = append(entries, entry)
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{"entries": entries})
}

// GetCachedMemo handles GET /api/v2/discover/decision-memo/cached/{key}
// Returns full memo data from cache.
func (h *Handler) GetCachedMemo(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUserFromContext(r.Context())
	if user == nil {
		httputil.Unauthorized(w, "unauthorized")
		return
	}

	key := chi.URLParam(r, "key")
	if key == "" {
		httputil.BadRequest(w, "cache key required")
		return
	}

	if h.store == nil {
		httputil.Error(w, http.StatusNotFound, "memo not found")
		return
	}

	q := h.store.Q()
	row, err := q.GetCacheByUserAndKey(r.Context(), queries.GetCacheByUserAndKeyParams{
		UserId: user.UserID,
		Key:    key,
	})
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "memo not found")
		return
	}

	// Update access time
	_ = q.UpdateCacheAccess(r.Context(), key)

	// Parse cached content and return the memos
	var cached map[string]interface{}
	if err := json.Unmarshal([]byte(row.Content), &cached); err != nil {
		httputil.InternalError(w, fmt.Errorf("failed to parse cached memo: %w", err))
		return
	}

	httputil.JSON(w, http.StatusOK, cached)
}

// DeleteCachedMemo handles DELETE /api/v2/discover/decision-memo/cached/{key}
// Deletes a memo from cache.
func (h *Handler) DeleteCachedMemo(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUserFromContext(r.Context())
	if user == nil {
		httputil.Unauthorized(w, "unauthorized")
		return
	}

	key := chi.URLParam(r, "key")
	if key == "" {
		httputil.BadRequest(w, "cache key required")
		return
	}

	if h.store == nil {
		httputil.Error(w, http.StatusNotFound, "memo not found")
		return
	}

	q := h.store.Q()
	err := q.DeleteCacheByUserAndKey(r.Context(), queries.DeleteCacheByUserAndKeyParams{
		UserId: user.UserID,
		Key:    key,
	})
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("failed to delete memo: %w", err))
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// ExportDecisionMemoPDF handles POST /api/v2/discover/decision-memo/export
// Generates a PDF from memo data.
func (h *Handler) ExportDecisionMemoPDF(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUserFromContext(r.Context())
	if user == nil {
		httputil.Unauthorized(w, "unauthorized")
		return
	}

	var req struct {
		Memos []memo.MemoData `json:"memos" validate:"required,min=1"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if len(req.Memos) == 0 {
		httputil.BadRequest(w, "at least one memo is required")
		return
	}

	if memoPDFBuilder == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "PDF export not available")
		return
	}

	pdfBytes, err := memoPDFBuilder.Build(req.Memos)
	if err != nil {
		h.logger.Error("failed to build decision memo PDF", "error", err)
		httputil.InternalError(w, fmt.Errorf("PDF generation failed: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=decision-memo.pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfBytes)))
	w.WriteHeader(http.StatusOK)
	w.Write(pdfBytes)
}
