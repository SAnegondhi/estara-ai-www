package discover

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/estara-ai/www/internal/api/middleware"
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

	streamToken := fmt.Sprintf("%s:%s", user.UserID, jobID)
	httputil.JSON(w, http.StatusAccepted, map[string]interface{}{
		"success":   true,
		"jobId":     jobID,
		"cached":    false,
		"streamUrl": fmt.Sprintf("/api/v2/discover/decision-memo/%s/stream?token=%s", jobID, streamToken),
	})
}

// StreamDecisionMemoProgress handles GET /api/v2/discover/decision-memo/{jobId}/stream
// SSE endpoint for memo generation progress.
func (h *Handler) StreamDecisionMemoProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "jobId")
	token := r.URL.Query().Get("token")

	if jobID == "" {
		httputil.BadRequest(w, "jobId required")
		return
	}

	// Validate token
	var userID string
	for i, c := range token {
		if c == ':' {
			userID = token[:i]
			break
		}
	}
	if userID == "" {
		httputil.Unauthorized(w, "invalid token")
		return
	}

	logger := slog.Default().With("component", "memo_stream", "job_id", jobID)
	logger.Info("starting memo stream", "user_id", userID)

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
