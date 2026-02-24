package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/pkg/httputil"
)

// EnrichmentJobManager manages background enrichment jobs
// Must be set via SetEnrichmentJobManager before using enrichment endpoints
var enrichmentJobManager *finder.EnrichmentJobManager

// SetEnrichmentJobManager sets the global enrichment job manager
func SetEnrichmentJobManager(manager *finder.EnrichmentJobManager) {
	enrichmentJobManager = manager
}

// AsyncSearchResponse extends the normal search response with enrichment job info
type AsyncSearchResponse struct {
	Success          bool               `json:"success"`
	Properties       []V2PropertyResult `json:"properties"`
	TotalCount       int                `json:"totalCount"`
	EnrichmentJobID  string             `json:"enrichmentJobId,omitempty"`
	EnrichmentStatus string             `json:"enrichmentStatus,omitempty"` // PENDING, PROCESSING, COMPLETED
}

// EnrichmentStatusResponse represents job status for polling
type EnrichmentStatusResponse struct {
	Success     bool                       `json:"success"`
	JobID       string                     `json:"jobId"`
	Status      string                     `json:"status"`
	Total       int                        `json:"total"`
	Completed   int                        `json:"completed"`
	Enriched    int                        `json:"enriched"`
	Failed      int                        `json:"failed"`
	NoData      int                        `json:"noData"`
	StartedAt   time.Time                  `json:"startedAt"`
	CompletedAt *time.Time                 `json:"completedAt,omitempty"`
	Error       string                     `json:"error,omitempty"`
}

// GetEnrichmentStatus returns the status of an enrichment job (for polling)
// GET /api/v2/discover/enrich/{jobId}
func (h *Handler) GetEnrichmentStatus(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId is required")
		return
	}

	if enrichmentJobManager == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "enrichment service not available")
		return
	}

	status, err := enrichmentJobManager.GetJobStatus(r.Context(), jobID)
	if err != nil {
		httputil.NotFound(w, "job not found")
		return
	}

	response := EnrichmentStatusResponse{
		Success:   true,
		JobID:     status.JobID,
		Status:    status.Status,
		Total:     status.Total,
		Completed: status.Completed,
		Enriched:  status.Enriched,
		Failed:    status.Failed,
		NoData:    status.NoData,
		StartedAt: status.StartedAt,
	}
	if !status.CompletedAt.IsZero() {
		response.CompletedAt = &status.CompletedAt
	}
	if status.Error != "" {
		response.Error = status.Error
	}

	httputil.JSON(w, http.StatusOK, response)
}

// StreamEnrichmentUpdates provides SSE stream for real-time enrichment updates
// GET /api/v2/discover/enrich/{jobId}/stream
func (h *Handler) StreamEnrichmentUpdates(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId is required")
		return
	}

	if enrichmentJobManager == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "enrichment service not available")
		return
	}

	// Check job exists
	status, err := enrichmentJobManager.GetJobStatus(r.Context(), jobID)
	if err != nil {
		httputil.NotFound(w, "job not found")
		return
	}

	// Set up SSE headers BEFORE any response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// If already completed, return current status and close
	if status.Status == finder.EnrichmentStatusCompleted || status.Status == finder.EnrichmentStatusFailed {
		h.sendSSEEvent(w, "complete", status)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		httputil.Error(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Disable write deadline and start fixed-interval heartbeat (same fix as streaming_search.go)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	heartbeatTicker := time.NewTicker(sseHeartbeatInterval)
	defer heartbeatTicker.Stop()

	// Subscribe to updates
	updates, unsubscribe := enrichmentJobManager.Subscribe(jobID)
	defer unsubscribe()

	// Create context for cleanup
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Send initial status
	h.sendSSEEvent(w, "status", status)
	flusher.Flush()

	// Stream updates
	logger := slog.Default().With("component", "enrichment_sse", "jobId", jobID)
	logger.Debug("SSE stream started")

	for {
		select {
		case update, ok := <-updates:
			if !ok {
				// Channel closed, job complete
				finalStatus, _ := enrichmentJobManager.GetJobStatus(ctx, jobID)
				if finalStatus != nil {
					h.sendSSEEvent(w, "complete", finalStatus)
					flusher.Flush()
				}
				logger.Debug("SSE stream completed")
				return
			}

			// Send property update
			h.sendSSEEvent(w, "update", update)
			flusher.Flush()

		case <-ctx.Done():
			logger.Debug("SSE client disconnected")
			return

		case <-heartbeatTicker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// sendSSEEvent writes a server-sent event
func (h *Handler) sendSSEEvent(w http.ResponseWriter, eventType string, data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(jsonData))
}

// InvalidateCacheRequest is the request body for cache invalidation
type InvalidateCacheRequest struct {
	City     string `json:"city"`
	State    string `json:"state"`
	CacheKey string `json:"cacheKey,omitempty"` // Optional: invalidate specific key
}

// InvalidateCacheResponse is the response for cache invalidation
type InvalidateCacheResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// InvalidateSearchCache invalidates property search cache for a location
// POST /api/v2/discover/cache/invalidate
func (h *Handler) InvalidateSearchCache(w http.ResponseWriter, r *http.Request) {
	if h.orchestrator == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "property service not available")
		return
	}

	var req InvalidateCacheRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	ctx := r.Context()

	// If specific cache key provided, invalidate just that key
	if req.CacheKey != "" {
		if err := h.orchestrator.InvalidateCacheByKey(ctx, req.CacheKey); err != nil {
			h.logger.Warn("cache invalidation failed", "key", req.CacheKey, "error", err)
			// Still return success - cache invalidation is best-effort
		}
		httputil.JSON(w, http.StatusOK, InvalidateCacheResponse{
			Success: true,
			Message: "cache key invalidated",
		})
		return
	}

	// Invalidate by location pattern
	if req.City == "" || req.State == "" {
		httputil.BadRequest(w, "city and state are required (or provide cacheKey)")
		return
	}

	if err := h.orchestrator.InvalidateCache(ctx, req.City, req.State); err != nil {
		h.logger.Warn("cache invalidation failed", "city", req.City, "state", req.State, "error", err)
		// Still return success - cache invalidation is best-effort
	}

	httputil.JSON(w, http.StatusOK, InvalidateCacheResponse{
		Success: true,
		Message: fmt.Sprintf("cache invalidated for %s, %s", req.City, req.State),
	})
}
