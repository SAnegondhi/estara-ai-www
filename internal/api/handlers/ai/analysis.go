package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/estara-ai/www/internal/api/handlers/util"
	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/services/ai/agents"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/estara-ai/www/pkg/sse"
)

// AnalysisHandler handles market analysis endpoints
type AnalysisHandler struct {
	store    *db.Store
	queue    *queue.Queue
	registry *sse.ConnectionRegistry
	cache    *cache.HybridCache
	logger   *slog.Logger
}

// NewAnalysisHandler creates a new analysis handler
func NewAnalysisHandler(
	store *db.Store,
	q *queue.Queue,
	registry *sse.ConnectionRegistry,
	cache *cache.HybridCache,
) *AnalysisHandler {
	return &AnalysisHandler{
		store:    store,
		queue:    q,
		registry: registry,
		cache:    cache,
		logger:   slog.Default().With("component", "analysis_handler"),
	}
}

// QueueAnalysisRequest holds the request body for queue endpoint
type QueueAnalysisRequest struct {
	Location     string                 `json:"location" validate:"required"`
	CacheKey     string                 `json:"cacheKey" validate:"required"`
	MarketData   map[string]interface{} `json:"marketData,omitempty"`
	ForceRefresh bool                   `json:"forceRefresh,omitempty"`
}

// QueueAnalysis handles POST /api/ai/analysis/queue
func (h *AnalysisHandler) QueueAnalysis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Parse request body
	var req QueueAnalysisRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate required fields
	if req.Location == "" {
		httputil.Error(w, http.StatusBadRequest, "location is required")
		return
	}
	if req.CacheKey == "" {
		httputil.Error(w, http.StatusBadRequest, "cacheKey is required")
		return
	}

	h.logger.Info("queueing market analysis",
		"user_id", user.UserID,
		"location", req.Location,
		"cache_key", req.CacheKey,
	)

	// Pre-queue cache check: if a valid cached result exists, return immediately
	// so the client can fetch the report directly — avoids the SSE race condition
	// where the job completes before the client opens the stream.
	if !req.ForceRefresh && h.cache != nil && req.CacheKey != "" {
		if cached, err := h.cache.Get(ctx, user.UserID, req.CacheKey); err == nil && len(cached) > 0 {
			h.logger.Info("returning cached analysis (pre-queue)",
				"user_id", user.UserID,
				"cache_key", req.CacheKey,
			)
			httputil.JSON(w, http.StatusOK, agents.AnalysisJobResponse{
				Success:  true,
				Cached:   true,
				CacheKey: req.CacheKey,
			})
			return
		}
	}

	// Create job
	job := queue.NewJob(queue.JobTypeMarketAnalysis, user.UserID, map[string]interface{}{
		"location":     req.Location,
		"cacheKey":     req.CacheKey,
		"marketData":   req.MarketData,
		"forceRefresh": req.ForceRefresh,
	})

	// Enqueue job
	jobID, err := h.queue.Enqueue(job)
	if err != nil {
		h.logger.Error("failed to enqueue job", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to queue analysis")
		return
	}

	// Generate stream token (simplified - in production use JWT)
	streamToken := fmt.Sprintf("%s:%s", user.UserID, jobID)

	// Log feature usage for customer support and chargeback defense
	_ = util.LogFeatureUsage(ctx, h.store, r, user.UserID, "FEATURE_MARKET_ANALYSIS",
		fmt.Sprintf("Market analysis: %s", req.Location),
		map[string]any{
			"jobId":        jobID,
			"location":     req.Location,
			"cacheKey":     req.CacheKey,
			"forceRefresh": req.ForceRefresh,
		},
	)

	response := agents.AnalysisJobResponse{
		Success:   true,
		JobID:     jobID,
		StreamURL: fmt.Sprintf("/api/ai/analysis/stream?token=%s&jobId=%s", streamToken, jobID),
	}

	httputil.JSON(w, http.StatusAccepted, response)
}

// StreamAnalysis handles GET /api/ai/analysis/stream
func (h *AnalysisHandler) StreamAnalysis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get token and jobId from query params (EventSource doesn't support headers)
	token := r.URL.Query().Get("token")
	jobID := r.URL.Query().Get("jobId")

	if token == "" || jobID == "" {
		httputil.Error(w, http.StatusBadRequest, "token and jobId required")
		return
	}

	// Validate token (simplified - extract userID)
	// In production, this would be a proper JWT validation
	var userID string
	if _, err := fmt.Sscanf(token, "%s:", &userID); err != nil {
		// Try to extract before colon
		for i, c := range token {
			if c == ':' {
				userID = token[:i]
				break
			}
		}
	}

	if userID == "" {
		httputil.Error(w, http.StatusUnauthorized, "invalid token")
		return
	}

	h.logger.Info("starting analysis stream",
		"user_id", userID,
		"job_id", jobID,
	)

	// Set up SSE
	sseWriter, err := sse.NewWriter(w)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Register connection using context cancellation
	connID := fmt.Sprintf("analysis_%s_%d", jobID, time.Now().UnixNano())
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	_, err = h.registry.Add(userID, connID, jobID, connCancel)
	if err != nil {
		h.logger.Warn("failed to register connection", "error", err)
		// Continue anyway - connection limit reached is not fatal
	}
	defer h.registry.Remove(userID, connID)

	// Use connCtx for monitoring cancellation
	_ = connCtx

	// Subscribe to job progress
	progressCh := h.queue.Subscribe(jobID)
	if progressCh == nil {
		// Job may already be complete, try to get result
		result, err := h.queue.GetResult(jobID)
		if err == nil && result != nil {
			sseWriter.WriteEventJSON("complete", map[string]interface{}{
				"jobId":  jobID,
				"result": result.Data,
			})
			return
		}
		httputil.Error(w, http.StatusNotFound, "job not found")
		return
	}

	// Start heartbeat
	stopHeartbeat := sseWriter.StartHeartbeat(15 * time.Second)
	defer close(stopHeartbeat)

	// Stream events
	for {
		select {
		case <-ctx.Done():
			h.logger.Info("stream context cancelled", "job_id", jobID)
			return

		case event, ok := <-progressCh:
			if !ok {
				// Channel closed, job complete
				result, err := h.queue.GetResult(jobID)
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

			// Send progress event
			sseWriter.WriteEventJSON("progress", map[string]interface{}{
				"jobId":    event.JobID,
				"progress": event.Progress,
				"message":  event.Message,
			})
		}
	}
}

// GetAnalysisJob handles GET /api/ai/analysis/{jobId}
func (h *AnalysisHandler) GetAnalysisJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "jobId")

	if jobID == "" {
		httputil.Error(w, http.StatusBadRequest, "jobId required")
		return
	}

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Get job result
	result, err := h.queue.GetResult(jobID)
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "job not found")
		return
	}

	// Check job ownership
	job, _ := h.queue.GetJob(jobID)
	if job != nil && job.UserID != user.UserID {
		httputil.Error(w, http.StatusForbidden, "access denied")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"job": map[string]interface{}{
			"id":          jobID,
			"status":      getJobStatus(result),
			"result":      result.Data,
			"error":       result.Error,
			"completedAt": result.CompletedAt,
		},
	})
}

// ListAnalysisJobs handles GET /api/ai/analysis/jobs
func (h *AnalysisHandler) ListAnalysisJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Get user's jobs from queue (nil status returns all jobs)
	jobs := h.queue.GetUserJobs(user.UserID, nil)

	// Filter to only market analysis jobs and build response
	jobDetails := make([]agents.AnalysisJobDetail, 0)
	for _, job := range jobs {
		// Only include market analysis jobs
		if job.Type != queue.JobTypeMarketAnalysis {
			continue
		}

		detail := agents.AnalysisJobDetail{
			ID:        job.ID,
			Status:    string(job.Status),
			CreatedAt: job.CreatedAt,
		}

		if location, ok := job.Payload["location"].(string); ok {
			detail.Location = location
		}

		// Get result if completed
		if job.Status == queue.JobStatusCompleted {
			if result, err := h.queue.GetResult(job.ID); err == nil {
				// Result.Data is already map[string]interface{}
				detail.ResultData = result.Data
				detail.CompletedAt = &result.CompletedAt
			}
		} else if job.Status == queue.JobStatusFailed {
			if result, err := h.queue.GetResult(job.ID); err == nil {
				detail.Error = result.Error
			}
		}

		jobDetails = append(jobDetails, detail)
	}

	httputil.JSON(w, http.StatusOK, agents.AnalysisJobListResponse{
		Success: true,
		Jobs:    jobDetails,
		Total:   len(jobDetails),
	})
}

// RetryAnalysis handles POST /api/ai/analysis/retry/{jobId}
func (h *AnalysisHandler) RetryAnalysis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "jobId")

	if jobID == "" {
		httputil.Error(w, http.StatusBadRequest, "jobId required")
		return
	}

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Get original job
	job, err := h.queue.GetJob(jobID)
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "job not found")
		return
	}

	// Check ownership
	if job.UserID != user.UserID {
		httputil.Error(w, http.StatusForbidden, "access denied")
		return
	}

	// Check if job can be retried
	if job.Status != queue.JobStatusFailed {
		httputil.Error(w, http.StatusBadRequest, "job cannot be retried")
		return
	}

	h.logger.Info("retrying analysis job",
		"user_id", user.UserID,
		"job_id", jobID,
	)

	// Create new job with same parameters
	newJob := &queue.Job{
		Type:      job.Type,
		UserID:    job.UserID,
		Payload:   job.Payload,
		Priority:  job.Priority,
		CreatedAt: time.Now(),
	}

	// Force refresh on retry
	newJob.Payload["forceRefresh"] = true

	// Enqueue new job
	newJobID, err := h.queue.Enqueue(newJob)
	if err != nil {
		h.logger.Error("failed to enqueue retry job", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to retry analysis")
		return
	}

	// Generate stream token
	streamToken := fmt.Sprintf("%s:%s", user.UserID, newJobID)

	httputil.JSON(w, http.StatusAccepted, agents.AnalysisJobResponse{
		Success:   true,
		JobID:     newJobID,
		StreamURL: fmt.Sprintf("/api/ai/analysis/stream?token=%s&jobId=%s", streamToken, newJobID),
	})
}

// CancelAnalysis handles POST /api/ai/analysis/cancel/{jobId}
func (h *AnalysisHandler) CancelAnalysis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "jobId")

	if jobID == "" {
		httputil.Error(w, http.StatusBadRequest, "jobId required")
		return
	}

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Get job
	job, err := h.queue.GetJob(jobID)
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "job not found")
		return
	}

	// Check ownership
	if job.UserID != user.UserID {
		httputil.Error(w, http.StatusForbidden, "access denied")
		return
	}

	// Check if job can be cancelled
	if job.Status != queue.JobStatusPending && job.Status != queue.JobStatusProcessing {
		httputil.Error(w, http.StatusBadRequest, "job cannot be cancelled")
		return
	}

	h.logger.Info("cancelling analysis job",
		"user_id", user.UserID,
		"job_id", jobID,
	)

	// Cancel job
	if err := h.queue.Cancel(jobID); err != nil {
		h.logger.Error("failed to cancel job", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to cancel job")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "job cancelled",
	})
}

// Helper function to get job status string
func getJobStatus(result *queue.JobResult) string {
	if result == nil {
		return "pending"
	}
	return string(result.Status)
}
