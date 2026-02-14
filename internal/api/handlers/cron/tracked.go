package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/queries"
)

// statusCapture wraps http.ResponseWriter to capture the status code.
type statusCapture struct {
	http.ResponseWriter
	code int
}

func (sc *statusCapture) WriteHeader(code int) {
	sc.code = code
	sc.ResponseWriter.WriteHeader(code)
}

// Tracked wraps a cron handler with job tracking. It looks up the cron job
// configuration by name, records the run in cron_job_runs, and updates
// cron_job_configs with the outcome. On 3+ consecutive failures a system
// alert is created.
func (h *Handler) Tracked(jobName string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		q := queries.New(h.db.Main)

		// Look up job config by name
		jobCfg, err := q.GetCronJobConfigByName(ctx, jobName)
		if err != nil {
			// Config not seeded yet – run the handler untracked
			h.logger.Warn("cron job config not found, running untracked",
				"job", jobName, "error", err)
			next.ServeHTTP(w, r)
			return
		}

		// Check if job is disabled
		if !jobCfg.IsEnabled {
			h.logger.Info("cron job is disabled, skipping", "job", jobName)
			http.Error(w, "job disabled", http.StatusServiceUnavailable)
			return
		}

		// Generate run ID
		idBytes := make([]byte, 12)
		_, _ = rand.Read(idBytes)
		runID := hex.EncodeToString(idBytes)

		// Record run start
		_, runErr := q.CreateCronJobRun(ctx, queries.CreateCronJobRunParams{
			ID:    runID,
			JobId: jobCfg.ID,
		})
		if runErr != nil {
			h.logger.Warn("failed to record cron run start", "job", jobName, "error", runErr)
		}

		// Apply timeout from config
		timeoutMs := int(jobCfg.TimeoutMs)
		if timeoutMs <= 0 {
			timeoutMs = 300000 // 5 minute default
		}
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()

		// Replace request context with timeout context
		r = r.WithContext(timeoutCtx)

		// Wrap ResponseWriter to capture status code
		sc := &statusCapture{ResponseWriter: w, code: http.StatusOK}

		start := time.Now()
		next.ServeHTTP(sc, r)
		durationMs := int32(time.Since(start).Milliseconds())

		// Determine outcome
		status := "SUCCESS"
		var errMsg string
		if sc.code >= 400 {
			status = "FAILED"
			errMsg = fmt.Sprintf("HTTP %d", sc.code)
		}
		if timeoutCtx.Err() == context.DeadlineExceeded {
			status = "TIMEOUT"
			errMsg = "job exceeded timeout"
		}

		// Complete the run record
		if runErr == nil {
			_, completeErr := q.CompleteCronJobRun(ctx, queries.CompleteCronJobRunParams{
				ID:         runID,
				Status:     status,
				DurationMs: pgtype.Int4{Int32: durationMs, Valid: true},
				Error:      pgtype.Text{String: errMsg, Valid: errMsg != ""},
				Output:     nil,
			})
			if completeErr != nil {
				h.logger.Warn("failed to complete cron run record", "job", jobName, "error", completeErr)
			}
		}

		// Update job config stats
		updateErr := q.UpdateCronJobLastRun(ctx, queries.UpdateCronJobLastRunParams{
			ID:               jobCfg.ID,
			LastRunAt:        pgtype.Timestamp{Time: start, Valid: true},
			LastRunStatus:    pgtype.Text{String: status, Valid: true},
			LastRunDurationMs: pgtype.Int4{Int32: durationMs, Valid: true},
		})
		if updateErr != nil {
			h.logger.Warn("failed to update cron job stats", "job", jobName, "error", updateErr)
		}

		// Check for consecutive failure threshold → system alert
		if status != "SUCCESS" {
			h.checkConsecutiveFailures(ctx, q, jobName)
		}
	}
}

// checkConsecutiveFailures creates a system alert if consecutive failures
// exceed the configured threshold.
func (h *Handler) checkConsecutiveFailures(ctx context.Context, q *queries.Queries, jobName string) {
	// Re-read config to get updated consecutiveFailures
	updated, err := q.GetCronJobConfigByName(ctx, jobName)
	if err != nil {
		return
	}

	threshold := updated.MaxConsecutiveFailures
	if threshold <= 0 {
		threshold = 3
	}

	if updated.ConsecutiveFailures >= threshold {
		alertKey := fmt.Sprintf("cron_failure_%s", jobName)
		metadataBytes, _ := json.Marshal(map[string]any{
			"job_name":              jobName,
			"endpoint":             updated.Endpoint,
			"consecutive_failures": updated.ConsecutiveFailures,
			"last_status":          updated.LastRunStatus.String,
		})

		_, alertErr := q.UpsertSystemAlert(ctx, queries.UpsertSystemAlertParams{
			ID:             alertKey,
			Type:           "cron_failure",
			Severity:       "high",
			Title:          fmt.Sprintf("Cron Job Failing: %s", jobName),
			Description:    fmt.Sprintf("Cron job %q has failed %d consecutive times (threshold: %d)", jobName, updated.ConsecutiveFailures, threshold),
			AlertKey:       alertKey,
			Metadata:       string(metadataBytes),
			ActionRequired: true,
		})
		if alertErr != nil {
			slog.Warn("failed to create cron failure alert", "job", jobName, "error", alertErr)
		}
	}
}
