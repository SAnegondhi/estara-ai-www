package cron

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/config"
	db "github.com/estara-ai/www/internal/db"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/market/importer"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles cron job HTTP requests
type Handler struct {
	store    *db.Store
	redis    *redisClient.Client
	cfg      *config.Config
	importer *importer.Service // ADR-075: Market data import service
	logger   *slog.Logger
}

// NewHandler creates a new cron handler
func NewHandler(store *db.Store, redis *redisClient.Client, cfg *config.Config) *Handler {
	return &Handler{
		store:  store,
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
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: MarketAlert table does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// CleanupCaches cleans up expired cache entries
func (h *Handler) CleanupCaches(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: AiResponseCache table does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// AggregateVendorCosts aggregates vendor costs for reporting
func (h *Handler) AggregateVendorCosts(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: VendorConfig/VendorUsageLog tables do not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// SendRenewalReminders sends subscription renewal reminders
func (h *Handler) SendRenewalReminders(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: User table with Prisma-era columns does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// SyncSubscriptions syncs subscriptions with Stripe
func (h *Handler) SyncSubscriptions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: User table with Prisma-era columns does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// ResetMonthlyUsage resets monthly usage counters
func (h *Handler) ResetMonthlyUsage(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: User table with Prisma-era columns does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// CleanupStaleJobs cleans up stale/orphaned jobs
func (h *Handler) CleanupStaleJobs(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: Job table does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// ArchiveAuditLogs archives old audit logs
func (h *Handler) ArchiveAuditLogs(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: AuditLog table does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// RefreshMarketData refreshes market data from external sources
func (h *Handler) RefreshMarketData(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: CityMarketCache table does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// PrunePropertyCache prunes old property cache entries
func (h *Handler) PrunePropertyCache(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: CachedProperty table does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// ExpireFreeTrials expires free trial subscriptions that have ended
// POST /api/cron/expire-free-trials
func (h *Handler) ExpireFreeTrials(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: User table with Prisma-era columns does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// ProcessScheduledReports processes scheduled report generation
// POST /api/cron/reports
func (h *Handler) ProcessScheduledReports(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: InvestorReport table does not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
}

// RefreshAIEstimates refreshes AI cost estimates
// POST /api/cron/ai-estimate-refresh
func (h *Handler) RefreshAIEstimates(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := newCronResult(start)
	result.Status = "completed"
	result.Message = "no-op: AiUsageSummary/AiUsage tables do not exist in Go backend"
	result.Duration = time.Since(start).String()
	httputil.Success(w, result)
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
	return h.store.Q().DeleteExpiredGuestSessionsRows(ctx)
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
	return h.store.Q().AutoArchiveOldSessionsRows(ctx)
}

func (h *Handler) deleteExpiredDiscoverySessions(ctx context.Context) (int64, error) {
	return h.store.Q().DeleteExpiredSessionsRows(ctx)
}

// ExpireIAPSubscriptions expires IAP subscriptions past their expiry date
// POST /api/cron/expire-iap-subscriptions
func (h *Handler) ExpireIAPSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	result := newCronResult(start)

	h.logger.Info("starting IAP subscription expiration")

	expired, err := h.expireIAPSubscriptions(ctx)
	if err != nil {
		h.logger.Error("failed to expire IAP subscriptions", "error", err)
		result.Status = "error"
		result.Message = "failed to expire IAP subscriptions"
		httputil.Error(w, http.StatusInternalServerError, result.Message)
		return
	}

	result.Status = "completed"
	result.Message = "IAP subscriptions expired"
	result.AffectedRows = expired
	result.Duration = time.Since(start).String()

	h.logger.Info("IAP subscription expiration completed",
		"expired", expired,
		"duration", result.Duration,
	)

	httputil.Success(w, result)
}

func (h *Handler) expireIAPSubscriptions(ctx context.Context) (int64, error) {
	return h.store.Q().ExpireIAPSubscriptionsRows(ctx)
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
