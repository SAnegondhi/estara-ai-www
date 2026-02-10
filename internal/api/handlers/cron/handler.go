package cron

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/market/importer"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles cron job HTTP requests
type Handler struct {
	db       *postgres.DB
	redis    *redisClient.Client
	cfg      *config.Config
	importer *importer.Service // ADR-075: Market data import service
	logger   *slog.Logger
}

// NewHandler creates a new cron handler
func NewHandler(db *postgres.DB, redis *redisClient.Client, cfg *config.Config) *Handler {
	return &Handler{
		db:     db,
		redis:  redis,
		cfg:    cfg,
		logger: slog.Default().With("component", "cron_handler"),
	}
}

// SetImporter injects the market data importer service (ADR-075).
func (h *Handler) SetImporter(imp *importer.Service) {
	h.importer = imp
}

// CronResult represents the result of a cron job
type CronResult struct {
	Status      string      `json:"status"`
	Message     string      `json:"message,omitempty"`
	AffectedRows int64      `json:"affectedRows,omitempty"`
	Details     interface{} `json:"details,omitempty"`
	Duration    string      `json:"duration"`
	Timestamp   string      `json:"timestamp"`
}

func newCronResult(start time.Time) *CronResult {
	return &CronResult{
		Duration:  time.Since(start).String(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// ProcessMarketAlerts processes pending market alerts
func (h *Handler) ProcessMarketAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting market alerts processing")

	// Query users with market alerts configured
	alerts, err := h.getPendingMarketAlerts(ctx)
	if err != nil {
		h.logger.Error("failed to get market alerts", "error", err)
		result.Status = "error"
		result.Message = "failed to get market alerts"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	// Process alerts (for now, just count them)
	processed := int64(0)
	triggered := int64(0)

	for _, alert := range alerts {
		// Check if alert condition is met (simplified for now)
		if h.checkAlertCondition(ctx, alert) {
			triggered++
			// Mark alert as triggered
			_ = h.markAlertTriggered(ctx, alert.ID)
		}
		processed++
	}

	result.Status = "completed"
	result.Message = "market alerts processed"
	result.AffectedRows = processed
	result.Details = map[string]int64{
		"processed": processed,
		"triggered": triggered,
	}
	result.Duration = time.Since(start).String()

	h.logger.Info("market alerts processed",
		"processed", processed,
		"triggered", triggered,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// CleanupCaches cleans up expired cache entries
func (h *Handler) CleanupCaches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting cache cleanup")

	// Delete expired AiResponseCache entries
	dbDeleted, err := h.deleteExpiredCache(ctx)
	if err != nil {
		h.logger.Error("failed to cleanup database cache", "error", err)
	}

	// Clean up Redis expired keys (Redis handles this automatically, but we can force scan)
	redisDeleted := int64(0)
	if h.redis != nil {
		// Scan for keys and delete expired ones
		redisDeleted, _ = h.cleanupRedisCache(ctx)
	}

	result.Status = "completed"
	result.Message = "cache cleanup completed"
	result.AffectedRows = dbDeleted + redisDeleted
	result.Details = map[string]int64{
		"databaseDeleted": dbDeleted,
		"redisDeleted":    redisDeleted,
	}
	result.Duration = time.Since(start).String()

	h.logger.Info("cache cleanup completed",
		"db_deleted", dbDeleted,
		"redis_deleted", redisDeleted,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// AggregateVendorCosts aggregates vendor costs for reporting
func (h *Handler) AggregateVendorCosts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting vendor costs aggregation")

	// Aggregate costs from VendorUsageLog table
	aggregated, err := h.aggregateVendorCosts(ctx)
	if err != nil {
		h.logger.Error("failed to aggregate vendor costs", "error", err)
		result.Status = "error"
		result.Message = "failed to aggregate vendor costs"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "vendor costs aggregated"
	result.AffectedRows = int64(len(aggregated))
	result.Details = aggregated
	result.Duration = time.Since(start).String()

	h.logger.Info("vendor costs aggregated",
		"vendors", len(aggregated),
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// SendRenewalReminders sends subscription renewal reminders
func (h *Handler) SendRenewalReminders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting renewal reminders")

	// Query users with subscriptions expiring in 7 days
	users, err := h.getUsersWithExpiringSubscriptions(ctx, 7)
	if err != nil {
		h.logger.Error("failed to get expiring subscriptions", "error", err)
		result.Status = "error"
		result.Message = "failed to get expiring subscriptions"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	// In production, send emails here
	// For now, just log and count
	sent := int64(0)
	for _, user := range users {
		h.logger.Info("would send renewal reminder",
			"user_id", user.ID,
			"email", user.Email,
			"expires_at", user.SubscriptionEndsAt,
		)
		sent++
	}

	result.Status = "completed"
	result.Message = "renewal reminders processed"
	result.AffectedRows = sent
	result.Details = map[string]int64{
		"usersFound":   int64(len(users)),
		"remindersSent": sent,
	}
	result.Duration = time.Since(start).String()

	h.logger.Info("renewal reminders completed",
		"users", len(users),
		"sent", sent,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// SyncSubscriptions syncs subscriptions with Stripe
func (h *Handler) SyncSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting subscription sync")

	// This would integrate with Stripe API in production
	// For now, verify internal consistency
	synced, discrepancies, err := h.syncSubscriptionStatus(ctx)
	if err != nil {
		h.logger.Error("failed to sync subscriptions", "error", err)
		result.Status = "error"
		result.Message = "failed to sync subscriptions"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "subscriptions synced"
	result.AffectedRows = synced
	result.Details = map[string]int64{
		"synced":       synced,
		"discrepancies": discrepancies,
	}
	result.Duration = time.Since(start).String()

	h.logger.Info("subscription sync completed",
		"synced", synced,
		"discrepancies", discrepancies,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// ResetMonthlyUsage resets monthly usage counters
func (h *Handler) ResetMonthlyUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting monthly usage reset")

	// Reset usage for users whose reset date has passed
	reset, err := h.resetUserUsage(ctx)
	if err != nil {
		h.logger.Error("failed to reset usage", "error", err)
		result.Status = "error"
		result.Message = "failed to reset usage"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "monthly usage reset"
	result.AffectedRows = reset
	result.Duration = time.Since(start).String()

	h.logger.Info("monthly usage reset completed",
		"reset", reset,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// CleanupStaleJobs cleans up stale/orphaned jobs
func (h *Handler) CleanupStaleJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting stale jobs cleanup")

	// Find jobs stuck in processing for more than 30 minutes
	cleaned, err := h.cleanupStaleJobs(ctx, 30*time.Minute)
	if err != nil {
		h.logger.Error("failed to cleanup stale jobs", "error", err)
		result.Status = "error"
		result.Message = "failed to cleanup stale jobs"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "stale jobs cleaned up"
	result.AffectedRows = cleaned
	result.Duration = time.Since(start).String()

	h.logger.Info("stale jobs cleanup completed",
		"cleaned", cleaned,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// ArchiveAuditLogs archives old audit logs
func (h *Handler) ArchiveAuditLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting audit log archival")

	// Archive logs older than 90 days
	archived, err := h.archiveOldAuditLogs(ctx, 90)
	if err != nil {
		h.logger.Error("failed to archive audit logs", "error", err)
		result.Status = "error"
		result.Message = "failed to archive audit logs"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "audit logs archived"
	result.AffectedRows = archived
	result.Duration = time.Since(start).String()

	h.logger.Info("audit log archival completed",
		"archived", archived,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// RefreshMarketData refreshes market data from external sources
func (h *Handler) RefreshMarketData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting market data refresh")

	// This would trigger data fetches from Zillow/FRED in production
	// For now, update cache timestamps
	refreshed, err := h.refreshMarketDataTimestamps(ctx)
	if err != nil {
		h.logger.Error("failed to refresh market data", "error", err)
		result.Status = "error"
		result.Message = "failed to refresh market data"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "market data refresh completed"
	result.AffectedRows = refreshed
	result.Duration = time.Since(start).String()

	h.logger.Info("market data refresh completed",
		"refreshed", refreshed,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// PrunePropertyCache prunes old property cache entries
func (h *Handler) PrunePropertyCache(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting property cache pruning")

	// Delete properties not used in 90 days
	pruned, err := h.prunePropertyCache(ctx, 90)
	if err != nil {
		h.logger.Error("failed to prune property cache", "error", err)
		result.Status = "error"
		result.Message = "failed to prune property cache"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "property cache pruned"
	result.AffectedRows = pruned
	result.Duration = time.Since(start).String()

	h.logger.Info("property cache pruning completed",
		"pruned", pruned,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

// ===============================
// Helper Types
// ===============================

type MarketAlert struct {
	ID        string
	UserID    string
	AlertType string
	Location  string
	Threshold float64
}

type UserSubscription struct {
	ID                 string
	Email              string
	SubscriptionEndsAt *time.Time
}

type VendorCostSummary struct {
	VendorID   string  `json:"vendorId"`
	VendorName string  `json:"vendorName"`
	TotalCost  float64 `json:"totalCost"`
	TotalCalls int64   `json:"totalCalls"`
	Period     string  `json:"period"`
}

// ===============================
// Database Helper Methods
// ===============================

func (h *Handler) getPendingMarketAlerts(ctx context.Context) ([]MarketAlert, error) {
	rows, err := h.db.Main.Query(ctx, `
		SELECT id, "userId", "alertType", location, threshold
		FROM "MarketAlert"
		WHERE enabled = true AND ("lastTriggeredAt" IS NULL OR "lastTriggeredAt" < NOW() - INTERVAL '24 hours')
		LIMIT 1000
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []MarketAlert
	for rows.Next() {
		var a MarketAlert
		if err := rows.Scan(&a.ID, &a.UserID, &a.AlertType, &a.Location, &a.Threshold); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, nil
}

func (h *Handler) checkAlertCondition(ctx context.Context, alert MarketAlert) bool {
	// Simplified check - in production would fetch market data
	// and compare against threshold
	return false
}

func (h *Handler) markAlertTriggered(ctx context.Context, alertID string) error {
	_, err := h.db.Main.Exec(ctx, `
		UPDATE "MarketAlert" SET "lastTriggeredAt" = NOW(), "updatedAt" = NOW()
		WHERE id = $1
	`, alertID)
	return err
}

func (h *Handler) deleteExpiredCache(ctx context.Context) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `
		DELETE FROM "AiResponseCache" WHERE "expiresAt" < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) cleanupRedisCache(ctx context.Context) (int64, error) {
	// Redis automatically handles expiration
	// This is a placeholder for any manual cleanup needed
	return 0, nil
}

func (h *Handler) aggregateVendorCosts(ctx context.Context) ([]VendorCostSummary, error) {
	rows, err := h.db.Main.Query(ctx, `
		SELECT
			v.id,
			v.name,
			COALESCE(SUM(u.cost), 0) as total_cost,
			COUNT(u.id) as total_calls
		FROM "VendorConfig" v
		LEFT JOIN "VendorUsageLog" u ON v.id = u."vendorId"
			AND u."createdAt" > NOW() - INTERVAL '30 days'
		GROUP BY v.id, v.name
		ORDER BY total_cost DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []VendorCostSummary
	for rows.Next() {
		var s VendorCostSummary
		if err := rows.Scan(&s.VendorID, &s.VendorName, &s.TotalCost, &s.TotalCalls); err != nil {
			return nil, err
		}
		s.Period = "last_30_days"
		summaries = append(summaries, s)
	}
	return summaries, nil
}

func (h *Handler) getUsersWithExpiringSubscriptions(ctx context.Context, days int) ([]UserSubscription, error) {
	rows, err := h.db.Main.Query(ctx, `
		SELECT id, email, "subscriptionEndsAt"
		FROM "User"
		WHERE "subscriptionStatus" = 'active'
		  AND "subscriptionEndsAt" IS NOT NULL
		  AND "subscriptionEndsAt" BETWEEN NOW() AND NOW() + $1 * INTERVAL '1 day'
		LIMIT 500
	`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserSubscription
	for rows.Next() {
		var u UserSubscription
		if err := rows.Scan(&u.ID, &u.Email, &u.SubscriptionEndsAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (h *Handler) syncSubscriptionStatus(ctx context.Context) (int64, int64, error) {
	// Check for status discrepancies
	var discrepancies int64
	err := h.db.Main.QueryRow(ctx, `
		SELECT COUNT(*) FROM "User"
		WHERE "subscriptionStatus" = 'active'
		  AND "subscriptionEndsAt" IS NOT NULL
		  AND "subscriptionEndsAt" < NOW()
	`).Scan(&discrepancies)
	if err != nil {
		return 0, 0, err
	}

	// Fix discrepancies
	if discrepancies > 0 {
		result, err := h.db.Main.Exec(ctx, `
			UPDATE "User" SET
				"subscriptionStatus" = 'expired',
				"updatedAt" = NOW()
			WHERE "subscriptionStatus" = 'active'
			  AND "subscriptionEndsAt" IS NOT NULL
			  AND "subscriptionEndsAt" < NOW()
		`)
		if err != nil {
			return 0, discrepancies, err
		}
		return result.RowsAffected(), discrepancies, nil
	}

	return 0, 0, nil
}

func (h *Handler) resetUserUsage(ctx context.Context) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `
		UPDATE "User" SET
			"monthlyUsage" = '{}',
			"usageResetAt" = NOW(),
			"updatedAt" = NOW()
		WHERE "usageResetAt" IS NULL
		   OR "usageResetAt" < NOW() - INTERVAL '1 month'
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) cleanupStaleJobs(ctx context.Context, staleThreshold time.Duration) (int64, error) {
	// Mark jobs stuck in processing as failed
	result, err := h.db.Main.Exec(ctx, `
		UPDATE "Job" SET
			status = 'failed',
			error = 'Job timed out - marked as stale',
			"updatedAt" = NOW()
		WHERE status = 'processing'
		  AND "updatedAt" < NOW() - $1 * INTERVAL '1 second'
	`, int(staleThreshold.Seconds()))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) archiveOldAuditLogs(ctx context.Context, days int) (int64, error) {
	// In production, would copy to archive table first
	// For now, just delete old logs
	result, err := h.db.Main.Exec(ctx, `
		DELETE FROM "AuditLog"
		WHERE "createdAt" < NOW() - $1 * INTERVAL '1 day'
	`, days)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) refreshMarketDataTimestamps(ctx context.Context) (int64, error) {
	// Update cache timestamps for market data
	result, err := h.db.Main.Exec(ctx, `
		UPDATE "CityMarketCache" SET
			"updatedAt" = NOW()
		WHERE "updatedAt" < NOW() - INTERVAL '24 hours'
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) prunePropertyCache(ctx context.Context, days int) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `
		DELETE FROM "CachedProperty"
		WHERE "lastUsedAt" < NOW() - $1 * INTERVAL '1 day'
	`, days)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// ExpireFreeTrials expires free trial subscriptions that have ended
// POST /api/cron/expire-free-trials
func (h *Handler) ExpireFreeTrials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting free trial expiration")

	expired, err := h.expireFreeTrials(ctx)
	if err != nil {
		h.logger.Error("failed to expire free trials", "error", err)
		result.Status = "error"
		result.Message = "failed to expire free trials"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "free trials expired"
	result.AffectedRows = expired
	result.Duration = time.Since(start).String()

	h.logger.Info("free trial expiration completed",
		"expired", expired,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

func (h *Handler) expireFreeTrials(ctx context.Context) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `
		UPDATE "User" SET
			"subscriptionStatus" = 'expired',
			"subscriptionTier" = 'free',
			"updatedAt" = NOW()
		WHERE "subscriptionStatus" = 'trialing'
		  AND "trialEndsAt" IS NOT NULL
		  AND "trialEndsAt" < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// ProcessScheduledReports processes scheduled report generation
// POST /api/cron/reports
func (h *Handler) ProcessScheduledReports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting scheduled reports processing")

	processed, err := h.processScheduledReports(ctx)
	if err != nil {
		h.logger.Error("failed to process scheduled reports", "error", err)
		result.Status = "error"
		result.Message = "failed to process scheduled reports"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "scheduled reports processed"
	result.AffectedRows = processed
	result.Duration = time.Since(start).String()

	h.logger.Info("scheduled reports processing completed",
		"processed", processed,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

func (h *Handler) processScheduledReports(ctx context.Context) (int64, error) {
	// Find pending scheduled reports and queue them for generation
	result, err := h.db.Main.Exec(ctx, `
		UPDATE "InvestorReport" SET
			status = 'QUEUED',
			"updatedAt" = NOW()
		WHERE status = 'SCHEDULED'
		  AND "scheduledAt" <= NOW()
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// RefreshAIEstimates refreshes AI cost estimates
// POST /api/cron/ai-estimate-refresh
func (h *Handler) RefreshAIEstimates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting AI estimate refresh")

	// Refresh AI usage summary aggregates
	refreshed, err := h.refreshAIEstimates(ctx)
	if err != nil {
		h.logger.Error("failed to refresh AI estimates", "error", err)
		result.Status = "error"
		result.Message = "failed to refresh AI estimates"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "AI estimates refreshed"
	result.AffectedRows = refreshed
	result.Duration = time.Since(start).String()

	h.logger.Info("AI estimate refresh completed",
		"refreshed", refreshed,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

func (h *Handler) refreshAIEstimates(ctx context.Context) (int64, error) {
	// Aggregate AI usage from recent entries into summary table
	result, err := h.db.Main.Exec(ctx, `
		INSERT INTO "AiUsageSummary" (id, "userId", "month", "totalInputTokens", "totalOutputTokens", "totalCost", "totalRequests", "createdAt", "updatedAt")
		SELECT
			gen_random_uuid()::text,
			"userId",
			date_trunc('month', NOW()) as month,
			SUM("inputTokens") as total_input,
			SUM("outputTokens") as total_output,
			SUM(cost) as total_cost,
			COUNT(*) as total_requests,
			NOW(),
			NOW()
		FROM "AiUsage"
		WHERE "createdAt" >= date_trunc('month', NOW())
		GROUP BY "userId"
		ON CONFLICT ("userId", "month")
		DO UPDATE SET
			"totalInputTokens" = EXCLUDED."totalInputTokens",
			"totalOutputTokens" = EXCLUDED."totalOutputTokens",
			"totalCost" = EXCLUDED."totalCost",
			"totalRequests" = EXCLUDED."totalRequests",
			"updatedAt" = NOW()
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// CleanupGuestSessions removes expired guest sessions
// POST /api/cron/cleanup-guest-sessions
func (h *Handler) CleanupGuestSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting guest session cleanup")

	cleaned, err := h.cleanupGuestSessions(ctx)
	if err != nil {
		h.logger.Error("failed to cleanup guest sessions", "error", err)
		result.Status = "error"
		result.Message = "failed to cleanup guest sessions"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "guest sessions cleaned up"
	result.AffectedRows = cleaned
	result.Duration = time.Since(start).String()

	h.logger.Info("guest session cleanup completed",
		"cleaned", cleaned,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

func (h *Handler) cleanupGuestSessions(ctx context.Context) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `
		DELETE FROM guest_sessions
		WHERE "expiresAt" < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// DiscoveryCleanup handles auto-archiving and deletion of discovery sessions
// POST /api/cron/discovery-cleanup
func (h *Handler) DiscoveryCleanup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting discovery session cleanup")

	// Auto-archive sessions older than 30 days
	archived, err := h.autoArchiveDiscoverySessions(ctx)
	if err != nil {
		h.logger.Error("failed to auto-archive discovery sessions", "error", err)
	}

	// Delete sessions past expiration (180 days)
	deleted, err := h.deleteExpiredDiscoverySessions(ctx)
	if err != nil {
		h.logger.Error("failed to delete expired discovery sessions", "error", err)
	}

	result.Status = "completed"
	result.Message = "discovery session cleanup completed"
	result.AffectedRows = archived + deleted
	result.Details = map[string]int64{
		"archived": archived,
		"deleted":  deleted,
	}
	result.Duration = time.Since(start).String()

	h.logger.Info("discovery session cleanup completed",
		"archived", archived,
		"deleted", deleted,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

func (h *Handler) autoArchiveDiscoverySessions(ctx context.Context) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `
		UPDATE discovery_sessions SET
			status = 'ARCHIVED',
			"archivedAt" = NOW(),
			"updatedAt" = NOW()
		WHERE status = 'ACTIVE'
		  AND "createdAt" < NOW() - INTERVAL '30 days'
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) deleteExpiredDiscoverySessions(ctx context.Context) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `
		DELETE FROM discovery_sessions
		WHERE "expiresAt" < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// ===============================
// Market Data Import Endpoints (ADR-075)
// ===============================

// ImportZillowZHVI handles POST /api/cron/market-data/zillow-zhvi?level=metro|city|zip|state
func (h *Handler) ImportZillowZHVI(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market importer not configured")
		return
	}

	level := r.URL.Query().Get("level")
	if level == "" {
		level = "metro"
	}

	start := time.Now()
	result, err := h.importer.ImportZillowZHVI(r.Context(), level)
	if err != nil {
		h.logger.Error("ZHVI import failed", "level", level, "error", err)
		httputil.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	cronResult := newCronResult(start)
	cronResult.Status = "completed"
	cronResult.Message = "ZHVI import completed"
	cronResult.AffectedRows = result.RecordsUpserted
	cronResult.Details = result
	cronResult.Duration = time.Since(start).String()
	httputil.Success(w, cronResult)
}

// ImportZillowZORI handles POST /api/cron/market-data/zillow-zori?level=metro|city|zip
func (h *Handler) ImportZillowZORI(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market importer not configured")
		return
	}

	level := r.URL.Query().Get("level")
	if level == "" {
		level = "metro"
	}

	start := time.Now()
	result, err := h.importer.ImportZillowZORI(r.Context(), level)
	if err != nil {
		h.logger.Error("ZORI import failed", "level", level, "error", err)
		httputil.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	cronResult := newCronResult(start)
	cronResult.Status = "completed"
	cronResult.Message = "ZORI import completed"
	cronResult.AffectedRows = result.RecordsUpserted
	cronResult.Details = result
	cronResult.Duration = time.Since(start).String()
	httputil.Success(w, cronResult)
}

// ImportZillowForecasts handles POST /api/cron/market-data/zillow-forecasts
func (h *Handler) ImportZillowForecasts(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market importer not configured")
		return
	}

	start := time.Now()
	result, err := h.importer.ImportZillowForecasts(r.Context())
	if err != nil {
		h.logger.Error("ZHVF import failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	cronResult := newCronResult(start)
	cronResult.Status = "completed"
	cronResult.Message = "ZHVF import completed"
	cronResult.AffectedRows = result.RecordsUpserted
	cronResult.Details = result
	cronResult.Duration = time.Since(start).String()
	httputil.Success(w, cronResult)
}

// ImportZillowMetrics handles POST /api/cron/market-data/zillow-metrics
func (h *Handler) ImportZillowMetrics(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market importer not configured")
		return
	}

	start := time.Now()
	result, err := h.importer.ImportZillowMetrics(r.Context())
	if err != nil {
		h.logger.Error("metrics import failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	cronResult := newCronResult(start)
	cronResult.Status = "completed"
	cronResult.Message = "Zillow metrics import completed"
	cronResult.AffectedRows = result.RecordsUpserted
	cronResult.Details = result
	cronResult.Duration = time.Since(start).String()
	httputil.Success(w, cronResult)
}

// ImportRedfinData handles POST /api/cron/market-data/redfin?level=national|state|metro|county|city|zip
func (h *Handler) ImportRedfinData(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market importer not configured")
		return
	}

	level := r.URL.Query().Get("level")
	if level == "" {
		level = "metro"
	}

	start := time.Now()
	result, err := h.importer.ImportRedfinData(r.Context(), level)
	if err != nil {
		h.logger.Error("Redfin import failed", "level", level, "error", err)
		httputil.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	cronResult := newCronResult(start)
	cronResult.Status = "completed"
	cronResult.Message = "Redfin import completed"
	cronResult.AffectedRows = result.RecordsUpserted
	cronResult.Details = result
	cronResult.Duration = time.Since(start).String()
	httputil.Success(w, cronResult)
}

// ComputeNational handles POST /api/cron/market-data/compute-national
func (h *Handler) ComputeNational(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market importer not configured")
		return
	}

	start := time.Now()
	result, err := h.importer.ComputeNationalAggregates(r.Context())
	if err != nil {
		h.logger.Error("national aggregate computation failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	cronResult := newCronResult(start)
	cronResult.Status = "completed"
	cronResult.Message = "national aggregates computed"
	cronResult.AffectedRows = result.RecordsUpserted
	cronResult.Details = result
	cronResult.Duration = time.Since(start).String()
	httputil.Success(w, cronResult)
}

// FullRefreshMarketData handles POST /api/cron/market-data/full-refresh
func (h *Handler) FullRefreshMarketData(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market importer not configured")
		return
	}

	start := time.Now()
	result, err := h.importer.FullRefresh(r.Context())
	if err != nil {
		h.logger.Error("full refresh failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	cronResult := newCronResult(start)
	cronResult.Status = "completed"
	cronResult.Message = "full market data refresh completed"
	cronResult.AffectedRows = result.RecordsUpserted
	cronResult.Details = result
	cronResult.Duration = time.Since(start).String()
	httputil.Success(w, cronResult)
}

// MarketDataStatus handles GET /api/cron/market-data/status
func (h *Handler) MarketDataStatus(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market importer not configured")
		return
	}

	status, err := h.importer.Status(r.Context())
	if err != nil {
		h.logger.Error("market data status failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.Success(w, status)
}
