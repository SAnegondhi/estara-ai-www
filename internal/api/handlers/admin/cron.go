package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// CronJobResponse is a JSON-safe representation of a cron job config.
type CronJobResponse struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Description         string  `json:"description"`
	Schedule            string  `json:"schedule"`
	Endpoint            string  `json:"endpoint"`
	IsRequired          bool    `json:"isRequired"`
	IsConfigured        bool    `json:"isConfigured"`
	IsEnabled           bool    `json:"isEnabled"`
	TimeoutMs           int32   `json:"timeoutMs"`
	MaxFailures         int32   `json:"maxFailures"`
	ConsecutiveFailures int32   `json:"consecutiveFailures"`
	AlertOnFailure      bool    `json:"alertOnFailure"`
	TotalRuns           int32   `json:"totalRuns"`
	SuccessfulRuns      int32   `json:"successfulRuns"`
	FailedRuns          int32   `json:"failedRuns"`
	LastRun             *string `json:"lastRun"`
	LastRunStatus       *string `json:"lastRunStatus"`
	LastRunDuration     *int32  `json:"lastRunDuration"`
	LastRunError        *string `json:"lastRunError"`
	CreatedAt           string  `json:"createdAt"`
	UpdatedAt           *string `json:"updatedAt"`
}

// CronJobRunResponse is a JSON-safe representation of a cron job run.
type CronJobRunResponse struct {
	ID          string  `json:"id"`
	JobID       string  `json:"jobId"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"startedAt"`
	CompletedAt *string `json:"completedAt"`
	Duration    *int32  `json:"duration"`
	Error       *string `json:"error"`
	TriggeredBy *string `json:"triggeredBy"`
}

func cronJobToResponse(c queries.CronJobConfig) CronJobResponse {
	resp := CronJobResponse{
		ID:                  c.ID,
		Name:                c.Name,
		Description:         c.Description,
		Schedule:            c.Schedule,
		Endpoint:            c.Endpoint,
		IsRequired:          c.IsRequired,
		IsConfigured:        c.IsConfigured,
		IsEnabled:           c.IsEnabled,
		TimeoutMs:           c.TimeoutMs,
		MaxFailures:         c.MaxFailures,
		ConsecutiveFailures: c.ConsecutiveFailures,
		AlertOnFailure:      c.AlertOnFailure,
		TotalRuns:           c.TotalRuns,
		SuccessfulRuns:      c.SuccessfulRuns,
		FailedRuns:          c.FailedRuns,
	}
	if c.LastRun.Valid {
		s := c.LastRun.Time.Format(time.RFC3339)
		resp.LastRun = &s
	}
	if c.LastRunStatus != nil {
		s := fmt.Sprintf("%v", c.LastRunStatus)
		resp.LastRunStatus = &s
	}
	if c.LastRunDuration.Valid {
		resp.LastRunDuration = &c.LastRunDuration.Int32
	}
	if c.LastRunError.Valid {
		resp.LastRunError = &c.LastRunError.String
	}
	if c.CreatedAt.Valid {
		resp.CreatedAt = c.CreatedAt.Time.Format(time.RFC3339)
	}
	if c.UpdatedAt.Valid {
		s := c.UpdatedAt.Time.Format(time.RFC3339)
		resp.UpdatedAt = &s
	}
	return resp
}

func cronRunToResponse(r queries.CronJobRun) CronJobRunResponse {
	resp := CronJobRunResponse{
		ID:    r.ID,
		JobID: r.CronJobId,
	}
	if r.Status != nil {
		resp.Status = fmt.Sprintf("%v", r.Status)
	}
	if r.StartedAt.Valid {
		resp.StartedAt = r.StartedAt.Time.Format(time.RFC3339)
	}
	if r.CompletedAt.Valid {
		s := r.CompletedAt.Time.Format(time.RFC3339)
		resp.CompletedAt = &s
	}
	if r.Duration.Valid {
		resp.Duration = &r.Duration.Int32
	}
	if r.Error.Valid {
		resp.Error = &r.Error.String
	}
	if r.TriggeredBy.Valid {
		resp.TriggeredBy = &r.TriggeredBy.String
	}
	return resp
}

// ListCronJobs returns all cron job configurations.
func (h *Handler) ListCronJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := h.store.Q()

	configs, err := q.ListCronJobConfigs(ctx)
	if err != nil {
		h.logger.Error("failed to list cron jobs", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list cron jobs")
		return
	}

	jobs := make([]CronJobResponse, len(configs))
	for i, c := range configs {
		jobs[i] = cronJobToResponse(c)
	}

	httputil.Success(w, map[string]interface{}{
		"jobs":  jobs,
		"total": len(jobs),
	})
}

// GetCronJobRuns returns paginated run history for a cron job.
func (h *Handler) GetCronJobRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "id")
	if jobID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	q := h.store.Q()

	runs, err := q.ListCronJobRuns(ctx, queries.ListCronJobRunsParams{
		CronJobId: jobID,
		Limit:     int32(pageSize),
		Offset:    int32(offset),
	})
	if err != nil {
		h.logger.Error("failed to list cron job runs", "error", err, "job_id", jobID)
		httputil.Error(w, http.StatusInternalServerError, "failed to list cron job runs")
		return
	}

	// Get total count by status
	counts, err := q.CountCronJobRunsByStatus(ctx, jobID)
	if err != nil {
		h.logger.Warn("failed to count cron job runs", "error", err)
	}
	total := counts.SuccessCount + counts.FailedCount + counts.TimeoutCount + counts.RunningCount

	items := make([]CronJobRunResponse, len(runs))
	for i, r := range runs {
		items[i] = cronRunToResponse(r)
	}

	httputil.Success(w, map[string]interface{}{
		"runs": items,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// ToggleCronJob enables or disables a cron job.
func (h *Handler) ToggleCronJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "id")
	if jobID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	q := h.store.Q()
	config, err := q.ToggleCronJobConfig(ctx, queries.ToggleCronJobConfigParams{
		ID:        jobID,
		IsEnabled: req.Enabled,
	})
	if err != nil {
		h.logger.Error("failed to toggle cron job", "error", err, "job_id", jobID)
		httputil.Error(w, http.StatusInternalServerError, "failed to toggle cron job")
		return
	}

	h.logAdminAudit(ctx, r, "CRON_TOGGLE", "cron_job", jobID, map[string]interface{}{
		"enabled": req.Enabled,
		"name":    config.Name,
	})
	httputil.Success(w, cronJobToResponse(config))
}

// TriggerCronJob triggers an immediate execution of a cron job.
func (h *Handler) TriggerCronJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "id")
	if jobID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	q := h.store.Q()
	config, err := q.GetCronJobConfigByID(ctx, jobID)
	if err != nil {
		h.logger.Error("failed to get cron job config", "error", err, "job_id", jobID)
		httputil.Error(w, http.StatusNotFound, "cron job not found")
		return
	}

	// Make internal HTTP POST to the cron endpoint
	port := h.cfg.Server.Port
	if port == 0 {
		port = 3000
	}
	url := fmt.Sprintf("http://localhost:%d%s", port, config.Endpoint)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		h.logger.Error("failed to create trigger request", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to create request")
		return
	}
	req.Header.Set("X-Cron-Secret", h.cfg.Cron.Secret)

	resp, err := client.Do(req)
	if err != nil {
		h.logger.Error("failed to trigger cron job", "error", err, "job_id", jobID, "endpoint", config.Endpoint)
		httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("failed to trigger cron job: %v", err))
		return
	}
	defer resp.Body.Close()

	h.logAdminAudit(ctx, r, "CRON_TRIGGER", "cron_job", jobID, map[string]interface{}{
		"name":       config.Name,
		"endpoint":   config.Endpoint,
		"statusCode": resp.StatusCode,
	})

	httputil.Success(w, map[string]interface{}{
		"jobId":      jobID,
		"name":       config.Name,
		"triggered":  true,
		"statusCode": resp.StatusCode,
	})
}
