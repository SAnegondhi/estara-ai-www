package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/cronjoborgclient"
	"github.com/estara-ai/www/pkg/httputil"
)

// generateCronID returns a random hex ID for new cron job configs.
func generateCronID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

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
	// cron-job.org integration (ADR-099)
	ExternalJobID  *int64 `json:"externalJobId"`
	ExternalSynced bool   `json:"externalSynced"`
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
	if c.ExternalJobID.Valid {
		v := c.ExternalJobID.Int64
		resp.ExternalJobID = &v
		resp.ExternalSynced = true
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

	h.logAdminAudit(ctx, r, "ADMIN_USER", "CRON_TOGGLE", "cron_job", jobID, map[string]interface{}{
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

	h.logAdminAudit(ctx, r, "ADMIN_USER", "CRON_TRIGGER", "cron_job", jobID, map[string]interface{}{
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

// cronOrgClient returns a cron-job.org API client using the configured CRON_SECRET.
func (h *Handler) cronOrgClient() *cronjoborgclient.Client {
	return cronjoborgclient.New(h.cfg.Cron.Secret)
}

// ─────────────────────────────────────────────────────────────────────────────
// cron-job.org integration handlers (ADR-099)
// ─────────────────────────────────────────────────────────────────────────────

// cronSyncStatusJob is a per-job sync status entry.
type cronSyncStatusJob struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Synced          bool    `json:"synced"`
	ExternalJobID   *int64  `json:"externalJobId"`
	ExternalEnabled *bool   `json:"externalEnabled"`
}

// GetSyncStatus returns how many local cron jobs are synced to cron-job.org.
// GET /api/admin/cron-jobs/sync-status
func (h *Handler) GetSyncStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := h.store.Q()

	localJobs, err := q.ListCronJobConfigs(ctx)
	if err != nil {
		h.logger.Error("sync-status: list local jobs", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list cron jobs")
		return
	}

	client := h.cronOrgClient()
	remoteJobs, err := client.ListJobs()
	if err != nil {
		h.logger.Warn("sync-status: list remote jobs failed", "error", err)
		// Return local status even if remote is unreachable — surface as unsynced.
		remoteJobs = nil
	}

	// Index remote jobs by jobId for O(1) lookup.
	remoteByID := make(map[int64]cronjoborgclient.CronOrgJob, len(remoteJobs))
	for _, rj := range remoteJobs {
		remoteByID[rj.JobID] = rj
	}

	var jobStatuses []cronSyncStatusJob
	synced := 0

	for _, lj := range localJobs {
		entry := cronSyncStatusJob{
			ID:   lj.ID,
			Name: lj.Name,
		}
		if lj.ExternalJobID.Valid {
			v := lj.ExternalJobID.Int64
			entry.ExternalJobID = &v
			if rj, ok := remoteByID[v]; ok {
				entry.Synced = true
				entry.ExternalEnabled = &rj.Enabled
				synced++
			}
		}
		jobStatuses = append(jobStatuses, entry)
	}

	httputil.Success(w, map[string]any{
		"total":   len(localJobs),
		"synced":  synced,
		"unsynced": len(localJobs) - synced,
		"jobs":    jobStatuses,
	})
}

// SyncCronJobs creates missing cron-job.org entries for unsynced local jobs.
// POST /api/admin/cron-jobs/sync
func (h *Handler) SyncCronJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := h.store.Q()

	localJobs, err := q.ListCronJobConfigs(ctx)
	if err != nil {
		h.logger.Error("sync: list local jobs", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list cron jobs")
		return
	}

	client := h.cronOrgClient()
	secret := h.cfg.Cron.Secret

	port := h.cfg.Server.Port
	if port == 0 {
		port = 3000
	}

	created := 0
	alreadySynced := 0
	var failures []string

	for _, lj := range localJobs {
		if lj.ExternalJobID.Valid {
			alreadySynced++
			continue
		}

		schedule, err := cronjoborgclient.ParseCronExpression(lj.Schedule)
		if err != nil {
			h.logger.Warn("sync: unparseable schedule", "job", lj.Name, "schedule", lj.Schedule, "error", err)
			failures = append(failures, fmt.Sprintf("%s: unparseable schedule %q", lj.Name, lj.Schedule))
			continue
		}

		endpointURL := fmt.Sprintf("https://api.estara-ai.com%s", lj.Endpoint)
		jobID, err := client.CreateJob(endpointURL, schedule, lj.Name, secret)
		if err != nil {
			h.logger.Warn("sync: create job failed", "job", lj.Name, "error", err)
			failures = append(failures, fmt.Sprintf("%s: %v", lj.Name, err))
			continue
		}

		if err := q.UpdateCronJobExternalID(ctx, queries.UpdateCronJobExternalIDParams{
			ID:            lj.ID,
			ExternalJobID: pgtype.Int8{Int64: jobID, Valid: true},
		}); err != nil {
			h.logger.Error("sync: store external job id", "job", lj.Name, "jobId", jobID, "error", err)
			failures = append(failures, fmt.Sprintf("%s: failed to store external id", lj.Name))
			continue
		}
		created++
	}

	h.logAdminAudit(ctx, r, "ADMIN_USER", "CRON_SYNC", "cron_jobs", "", map[string]any{
		"created":      created,
		"alreadySynced": alreadySynced,
		"failures":     len(failures),
	})

	httputil.Success(w, map[string]any{
		"created":      created,
		"alreadySynced": alreadySynced,
		"failures":     failures,
	})
}

// BulkToggleCronJobs enables or disables a set of cron jobs in both local DB
// and cron-job.org.
// POST /api/admin/cron-jobs/bulk-toggle
func (h *Handler) BulkToggleCronJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		IDs     []string `json:"ids"`
		Enabled bool     `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		httputil.BadRequest(w, "ids must not be empty")
		return
	}

	q := h.store.Q()

	updated, err := q.BulkToggleCronJobsByIDs(ctx, queries.BulkToggleCronJobsByIDsParams{
		Column1:   req.IDs,
		IsEnabled: req.Enabled,
	})
	if err != nil {
		h.logger.Error("bulk-toggle: update local db", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to toggle cron jobs")
		return
	}

	// Propagate to cron-job.org for jobs that have an external ID.
	client := h.cronOrgClient()
	var extFailures []string
	for _, job := range updated {
		if !job.ExternalJobID.Valid {
			continue
		}
		if err := client.UpdateJob(job.ExternalJobID.Int64, req.Enabled); err != nil {
			h.logger.Warn("bulk-toggle: update remote job", "job", job.Name, "error", err)
			extFailures = append(extFailures, job.Name)
		}
	}

	h.logAdminAudit(ctx, r, "ADMIN_USER", "CRON_BULK_TOGGLE", "cron_jobs", "", map[string]any{
		"ids":         req.IDs,
		"enabled":     req.Enabled,
		"extFailures": extFailures,
	})

	jobs := make([]CronJobResponse, len(updated))
	for i, c := range updated {
		jobs[i] = cronJobToResponse(c)
	}

	httputil.Success(w, map[string]any{
		"jobs":            jobs,
		"remoteFailures":  extFailures,
	})
}

// cronEndpointResult is the health check result for a single endpoint.
type cronEndpointResult struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Endpoint   string  `json:"endpoint"`
	Status     string  `json:"status"`     // "ok" | "not_found" | "error"
	HTTPStatus *int    `json:"httpStatus"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 2: CRUD + server discovery (ADR-099)
// ─────────────────────────────────────────────────────────────────────────────

// CronJobFormRequest is the body for create/update cron job config.
type CronJobFormRequest struct {
	Name        string `json:"name" validate:"required,max=100"`
	Description string `json:"description"`
	Schedule    string `json:"schedule" validate:"required"`
	Endpoint    string `json:"endpoint" validate:"required"`
	IsEnabled   bool   `json:"isEnabled"`
	TimeoutMs   int32  `json:"timeoutMs"`
	MaxFailures int32  `json:"maxFailures"`
}

// cronDiscoveredRoute is a single route found by chi.Walk.
type cronDiscoveredRoute struct {
	Method   string `json:"method"`
	Pattern  string `json:"pattern"`
	InDB     bool   `json:"inDb"`
	JobID    string `json:"jobId,omitempty"`
	JobName  string `json:"jobName,omitempty"`
}

// DiscoverCronJobs walks the chi router to return all /api/cron/* routes,
// annotated with whether each exists in the local DB.
// GET /api/admin/cron-jobs/discover
func (h *Handler) DiscoverCronJobs(w http.ResponseWriter, r *http.Request) {
	if h.router == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "router not available")
		return
	}

	ctx := r.Context()
	q := h.store.Q()

	localJobs, err := q.ListCronJobConfigs(ctx)
	if err != nil {
		h.logger.Error("discover: list local jobs", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list cron jobs")
		return
	}

	// Index local jobs by endpoint for O(1) lookup.
	localByEndpoint := make(map[string]queries.CronJobConfig, len(localJobs))
	for _, lj := range localJobs {
		localByEndpoint[lj.Endpoint] = lj
	}

	discovered := make([]cronDiscoveredRoute, 0)
	seenPaths := make(map[string]bool)

	_ = chi.Walk(h.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/cron") {
			return nil
		}
		// Deduplicate (chi may emit same route with multiple methods)
		key := method + ":" + route
		if seenPaths[key] {
			return nil
		}
		seenPaths[key] = true

		entry := cronDiscoveredRoute{
			Method:  method,
			Pattern: route,
		}
		if lj, ok := localByEndpoint[route]; ok {
			entry.InDB = true
			entry.JobID = lj.ID
			entry.JobName = lj.Name
		}
		discovered = append(discovered, entry)
		return nil
	})

	// Build reverse: DB entries whose endpoint doesn't match any server route.
	serverRoutes := make(map[string]bool)
	for _, d := range discovered {
		serverRoutes[d.Pattern] = true
	}

	orphaned := make([]CronJobResponse, 0)
	for _, lj := range localJobs {
		if !serverRoutes[lj.Endpoint] {
			orphaned = append(orphaned, cronJobToResponse(lj))
		}
	}

	httputil.Success(w, map[string]any{
		"routes":   discovered,
		"orphaned": orphaned,
	})
}

// CreateCronJob creates a new cron job config entry.
// POST /api/admin/cron-jobs/
func (h *Handler) CreateCronJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req CronJobFormRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if req.TimeoutMs <= 0 {
		req.TimeoutMs = 60000
	}
	if req.MaxFailures <= 0 {
		req.MaxFailures = 3
	}

	// Soft-check: probe the endpoint (404 = warning, not block).
	endpointStatus := h.probeEndpoint(req.Endpoint)

	id := generateCronID()
	q := h.store.Q()
	config, err := q.CreateCronJobConfig(ctx, queries.CreateCronJobConfigParams{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Schedule:    req.Schedule,
		Endpoint:    req.Endpoint,
		IsEnabled:   req.IsEnabled,
		TimeoutMs:   req.TimeoutMs,
		MaxFailures: req.MaxFailures,
	})
	if err != nil {
		h.logger.Error("create cron job: insert", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to create cron job")
		return
	}

	h.logAdminAudit(ctx, r, "ADMIN_USER", "CRON_CREATE", "cron_job", id, map[string]any{
		"name":           req.Name,
		"endpoint":       req.Endpoint,
		"endpointStatus": endpointStatus,
	})

	httputil.Success(w, map[string]any{
		"job":            cronJobToResponse(config),
		"endpointStatus": endpointStatus,
	})
}

// UpdateCronJob updates an existing cron job config.
// PUT /api/admin/cron-jobs/{id}
func (h *Handler) UpdateCronJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "id")
	if jobID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req CronJobFormRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if req.TimeoutMs <= 0 {
		req.TimeoutMs = 60000
	}
	if req.MaxFailures <= 0 {
		req.MaxFailures = 3
	}

	// Soft-check: probe the endpoint.
	endpointStatus := h.probeEndpoint(req.Endpoint)

	q := h.store.Q()
	config, err := q.UpdateCronJobConfig(ctx, queries.UpdateCronJobConfigParams{
		ID:          jobID,
		Name:        req.Name,
		Description: req.Description,
		Schedule:    req.Schedule,
		Endpoint:    req.Endpoint,
		IsEnabled:   req.IsEnabled,
		TimeoutMs:   req.TimeoutMs,
		MaxFailures: req.MaxFailures,
	})
	if err != nil {
		h.logger.Error("update cron job: update", "error", err, "job_id", jobID)
		httputil.Error(w, http.StatusInternalServerError, "failed to update cron job")
		return
	}

	h.logAdminAudit(ctx, r, "ADMIN_USER", "CRON_UPDATE", "cron_job", jobID, map[string]any{
		"name":           req.Name,
		"endpoint":       req.Endpoint,
		"endpointStatus": endpointStatus,
	})

	httputil.Success(w, map[string]any{
		"job":            cronJobToResponse(config),
		"endpointStatus": endpointStatus,
	})
}

// DeleteCronJob removes a cron job config from the local DB (does NOT delete
// from cron-job.org — admin should disable/delete there separately).
// DELETE /api/admin/cron-jobs/{id}
func (h *Handler) DeleteCronJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := chi.URLParam(r, "id")
	if jobID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	q := h.store.Q()
	// Fetch before delete for audit log.
	existing, err := q.GetCronJobConfigByID(ctx, jobID)
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "cron job not found")
		return
	}

	if err := q.DeleteCronJobConfig(ctx, jobID); err != nil {
		h.logger.Error("delete cron job: delete", "error", err, "job_id", jobID)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete cron job")
		return
	}

	h.logAdminAudit(ctx, r, "ADMIN_USER", "CRON_DELETE", "cron_job", jobID, map[string]any{
		"name":     existing.Name,
		"endpoint": existing.Endpoint,
	})

	httputil.Success(w, map[string]any{"deleted": true, "id": jobID})
}

// probeEndpoint sends a GET to the local server endpoint and returns a status string.
// Returns "ok", "not_found", or "error". Hard 5-second timeout, non-blocking.
func (h *Handler) probeEndpoint(endpoint string) string {
	port := h.cfg.Server.Port
	if port == 0 {
		port = 3000
	}
	url := fmt.Sprintf("http://localhost:%d%s", port, endpoint)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "error"
	}
	req.Header.Set("X-Cron-Secret", h.cfg.Cron.Secret)
	resp, err := client.Do(req)
	if err != nil {
		return "error"
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "not_found"
	}
	if resp.StatusCode >= 500 {
		return "error"
	}
	return "ok"
}

// CheckCronEndpoints probes each /api/cron/* route with a GET request to verify
// it is registered (405 = exists, wrong method; 404 = missing route; 5xx = error).
// POST /api/admin/cron-jobs/check-endpoints
func (h *Handler) CheckCronEndpoints(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := h.store.Q()

	jobs, err := q.ListCronJobConfigs(ctx)
	if err != nil {
		h.logger.Error("check-endpoints: list jobs", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list cron jobs")
		return
	}

	port := h.cfg.Server.Port
	if port == 0 {
		port = 3000
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	results := make([]cronEndpointResult, 0, len(jobs))

	for _, job := range jobs {
		url := fmt.Sprintf("http://localhost:%d%s", port, job.Endpoint)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			results = append(results, cronEndpointResult{
				ID: job.ID, Name: job.Name, Endpoint: job.Endpoint,
				Status: "error",
			})
			continue
		}
		req.Header.Set("X-Cron-Secret", h.cfg.Cron.Secret)

		resp, err := httpClient.Do(req)
		if err != nil {
			results = append(results, cronEndpointResult{
				ID: job.ID, Name: job.Name, Endpoint: job.Endpoint,
				Status: "error",
			})
			continue
		}
		resp.Body.Close()

		code := resp.StatusCode
		status := "ok"
		if code == http.StatusNotFound {
			status = "not_found"
		} else if code >= 500 {
			status = "error"
		}
		results = append(results, cronEndpointResult{
			ID: job.ID, Name: job.Name, Endpoint: job.Endpoint,
			Status:     status,
			HTTPStatus: &code,
		})
	}

	httputil.Success(w, results)
}
