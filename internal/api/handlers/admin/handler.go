package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles admin-related HTTP requests
type Handler struct {
	db       *postgres.DB
	redis    *redisClient.Client
	cfg      *config.Config
	auth     *middleware.AuthMiddleware
	validate *validator.Validate
	logger   *slog.Logger
}

// NewHandler creates a new admin handler
func NewHandler(db *postgres.DB, redis *redisClient.Client, cfg *config.Config, auth *middleware.AuthMiddleware) *Handler {
	return &Handler{
		db:       db,
		redis:    redis,
		cfg:      cfg,
		auth:     auth,
		validate: validator.New(),
		logger:   slog.Default().With("component", "admin_handler"),
	}
}

// ===============================
// Types
// ===============================

// User represents a user from the database
type User struct {
	ID                 string     `json:"id"`
	ClerkUserID        *string    `json:"clerkUserId,omitempty"`
	Email              string     `json:"email"`
	Name               *string    `json:"name,omitempty"`
	Role               string     `json:"role"`
	SubscriptionPlan   string     `json:"subscriptionPlan"`
	SubscriptionStatus string     `json:"subscriptionStatus"`
	MonthlyUsage       any        `json:"monthlyUsage,omitempty"`
	UsageResetAt       *time.Time `json:"usageResetAt,omitempty"`
	LastLoginAt        *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// UpdateUserRequest represents the request to update a user
type UpdateUserRequest struct {
	Name               *string `json:"name,omitempty"`
	Email              *string `json:"email,omitempty" validate:"omitempty,email"`
	Role               *string `json:"role,omitempty" validate:"omitempty,oneof=user admin"`
	SubscriptionPlan   *string `json:"subscriptionPlan,omitempty" validate:"omitempty,oneof=free pro enterprise"`
	SubscriptionStatus *string `json:"subscriptionStatus,omitempty" validate:"omitempty,oneof=active inactive trialing past_due canceled"`
}

// InvalidateCacheRequest represents the request to invalidate cache
type InvalidateCacheRequest struct {
	Strategy string  `json:"strategy" validate:"required,oneof=all expired type user key"`
	Type     *string `json:"type,omitempty"`     // Required if strategy=type
	UserID   *string `json:"userId,omitempty"`   // Required if strategy=user
	CacheKey *string `json:"cacheKey,omitempty"` // Required if strategy=key
}

// Vendor represents a configured vendor
type Vendor struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Enabled     bool      `json:"enabled"`
	HealthURL   string    `json:"healthUrl,omitempty"`
	LastChecked *time.Time `json:"lastChecked,omitempty"`
	Status      string    `json:"status"`
}

// VendorHealthResult represents the health check result
type VendorHealthResult struct {
	Vendor      string    `json:"vendor"`
	Status      string    `json:"status"`
	Latency     int64     `json:"latencyMs"`
	Message     string    `json:"message,omitempty"`
	CheckedAt   time.Time `json:"checkedAt"`
}

// UserStats represents aggregate user statistics
type UserStats struct {
	TotalUsers      int64 `json:"totalUsers"`
	AdminCount      int64 `json:"adminCount"`
	UserCount       int64 `json:"userCount"`
	FreeCount       int64 `json:"freeCount"`
	ProCount        int64 `json:"proCount"`
	EnterpriseCount int64 `json:"enterpriseCount"`
	ActiveLastWeek  int64 `json:"activeLastWeek"`
}

// AIMetrics represents AI usage metrics
type AIMetrics struct {
	TotalRequests      int64   `json:"totalRequests"`
	TotalInputTokens   int64   `json:"totalInputTokens"`
	TotalOutputTokens  int64   `json:"totalOutputTokens"`
	TotalCost          float64 `json:"totalCost"`
	CacheHitRate       float64 `json:"cacheHitRate"`
	AverageLatencyMs   float64 `json:"averageLatencyMs"`
	RequestsToday      int64   `json:"requestsToday"`
	CostToday          float64 `json:"costToday"`
	RequestsThisMonth  int64   `json:"requestsThisMonth"`
	CostThisMonth      float64 `json:"costThisMonth"`
}

// AIUsageRecord represents a single AI usage record
type AIUsageRecord struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId"`
	Email        *string   `json:"email,omitempty"`
	CacheType    string    `json:"cacheType"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"inputTokens"`
	OutputTokens int       `json:"outputTokens"`
	Cost         float64   `json:"cost"`
	CreatedAt    time.Time `json:"createdAt"`
}

// AuditLogEntry represents an audit log entry
type AuditLogEntry struct {
	ID         string                 `json:"id"`
	UserID     *string                `json:"userId,omitempty"`
	Action     string                 `json:"action"`
	Resource   string                 `json:"resource"`
	ResourceID *string                `json:"resourceId,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
	IPAddress  *string                `json:"ipAddress,omitempty"`
	UserAgent  *string                `json:"userAgent,omitempty"`
	CreatedAt  time.Time              `json:"createdAt"`
}

// SystemAlert represents a system alert
type SystemAlert struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Severity   string    `json:"severity"`
	Title      string    `json:"title"`
	Message    string    `json:"message"`
	Resolved   bool      `json:"resolved"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
	ResolvedBy *string   `json:"resolvedBy,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Analytics represents application analytics
type Analytics struct {
	Users     UserAnalytics     `json:"users"`
	Reports   ReportAnalytics   `json:"reports"`
	API       APIAnalytics      `json:"api"`
	Revenue   RevenueAnalytics  `json:"revenue"`
	Timestamp string            `json:"timestamp"`
}

// UserAnalytics represents user-related analytics
type UserAnalytics struct {
	Total           int64   `json:"total"`
	ActiveThisWeek  int64   `json:"activeThisWeek"`
	ActiveThisMonth int64   `json:"activeThisMonth"`
	NewThisWeek     int64   `json:"newThisWeek"`
	NewThisMonth    int64   `json:"newThisMonth"`
	ChurnRate       float64 `json:"churnRate"`
}

// ReportAnalytics represents report-related analytics
type ReportAnalytics struct {
	TotalGenerated int64 `json:"totalGenerated"`
	ThisWeek       int64 `json:"thisWeek"`
	ThisMonth      int64 `json:"thisMonth"`
	FailedThisWeek int64 `json:"failedThisWeek"`
}

// APIAnalytics represents API usage analytics
type APIAnalytics struct {
	TotalRequests int64   `json:"totalRequests"`
	RequestsToday int64   `json:"requestsToday"`
	AvgLatencyMs  float64 `json:"avgLatencyMs"`
	ErrorRate     float64 `json:"errorRate"`
}

// RevenueAnalytics represents revenue analytics
type RevenueAnalytics struct {
	TotalMRR         float64 `json:"totalMrr"`
	NewSubscriptions int64   `json:"newSubscriptions"`
	Churned          int64   `json:"churned"`
}

// VendorCostRecord represents vendor usage costs
type VendorCostRecord struct {
	VendorID    string    `json:"vendorId"`
	VendorName  string    `json:"vendorName"`
	Category    string    `json:"category"`
	TotalCost   float64   `json:"totalCost"`
	RequestCount int64    `json:"requestCount"`
	Period      string    `json:"period"`
	RecordedAt  time.Time `json:"recordedAt"`
}

// InvestorReportAdmin represents an investor report for admin view
type InvestorReportAdmin struct {
	ID           string     `json:"id"`
	UserID       string     `json:"userId"`
	Email        *string    `json:"email,omitempty"`
	ReportType   string     `json:"reportType"`
	Status       string     `json:"status"`
	Address      *string    `json:"address,omitempty"`
	City         *string    `json:"city,omitempty"`
	State        *string    `json:"state,omitempty"`
	ErrorMessage *string    `json:"errorMessage,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
}

// CacheStats represents cache statistics
type CacheStats struct {
	TotalEntries    int64   `json:"totalEntries"`
	ExpiredEntries  int64   `json:"expiredEntries"`
	TotalInputTokens  int64   `json:"totalInputTokens"`
	TotalOutputTokens int64   `json:"totalOutputTokens"`
	TotalCost       float64 `json:"totalCost"`
	UniqueUsers     int64   `json:"uniqueUsers"`
	CacheTypes      int64   `json:"cacheTypes"`
}

// ===============================
// User Management
// ===============================

// ListUsers returns a paginated list of users
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 20)
	search := r.URL.Query().Get("search")

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	var users []User
	var total int64
	var err error

	if search != "" {
		// Search by email
		users, total, err = h.searchUsersByEmail(ctx, search, pageSize, offset)
	} else {
		// List all users
		users, total, err = h.listAllUsers(ctx, pageSize, offset)
	}

	if err != nil {
		h.logger.Error("failed to list users", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	httputil.Success(w, map[string]interface{}{
		"users": users,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// GetUser returns a specific user
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	user, err := h.getUserByID(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("failed to get user", "error", err, "user_id", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get user")
		return
	}

	httputil.Success(w, user)
}

// UpdateUser updates a user
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	// Check user exists
	_, err := h.getUserByID(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("failed to get user", "error", err, "user_id", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get user")
		return
	}

	// Update user
	if err := h.updateUserProfile(ctx, userID, &req); err != nil {
		h.logger.Error("failed to update user", "error", err, "user_id", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Get updated user
	user, err := h.getUserByID(ctx, userID)
	if err != nil {
		h.logger.Error("failed to get updated user", "error", err, "user_id", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get updated user")
		return
	}

	h.logAdminAudit(ctx, r, "USER_UPDATE", "user", userID, map[string]interface{}{
		"changes": req,
	})
	h.logger.Info("user updated", "user_id", userID)
	httputil.Success(w, user)
}

// ImpersonateUser generates a token to impersonate a user
func (h *Handler) ImpersonateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	// Get the target user
	user, err := h.getUserByID(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("failed to get user", "error", err, "user_id", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get user")
		return
	}

	// Generate impersonation token (1 hour expiry)
	token, err := h.generateImpersonationToken(user, time.Hour)
	if err != nil {
		h.logger.Error("failed to generate impersonation token", "error", err, "user_id", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	h.logAdminAudit(ctx, r, "USER_IMPERSONATE", "user", userID, map[string]interface{}{
		"targetEmail": user.Email,
		"expiresIn":   3600,
	})
	h.logger.Info("impersonation token generated", "target_user_id", userID)
	httputil.Success(w, map[string]interface{}{
		"token":     token,
		"expiresIn": 3600,
		"userId":    userID,
		"email":     user.Email,
	})
}

// ===============================
// Cache Management
// ===============================

// CacheStats returns cache statistics
func (h *Handler) CacheStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stats := make(map[string]interface{})

	// Get Redis stats
	if h.redis != nil {
		stats["redis"] = h.redis.Stats()
	}

	// Get database pool stats
	stats["database"] = h.db.AllStats()

	// Get cache entry stats from PostgreSQL
	cacheStats, err := h.getCacheStats(ctx)
	if err != nil {
		h.logger.Warn("failed to get cache stats", "error", err)
	} else {
		stats["cache"] = cacheStats
	}

	httputil.Success(w, stats)
}

// InvalidateCache invalidates cache entries
func (h *Handler) InvalidateCache(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req InvalidateCacheRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	var deleted int64
	var err error

	switch req.Strategy {
	case "all":
		// Clear all cache (dangerous - require confirmation)
		deleted, err = h.deleteAllCache(ctx)
		if h.redis != nil {
			_ = h.redis.FlushDB(ctx)
		}

	case "expired":
		// Clear only expired entries
		deleted, err = h.deleteExpiredCache(ctx)

	case "type":
		if req.Type == nil || *req.Type == "" {
			httputil.BadRequest(w, "type is required for strategy=type")
			return
		}
		deleted, err = h.deleteCacheByType(ctx, *req.Type)

	case "user":
		if req.UserID == nil || *req.UserID == "" {
			httputil.BadRequest(w, "userId is required for strategy=user")
			return
		}
		deleted, err = h.deleteCacheByUser(ctx, *req.UserID)

	case "key":
		if req.CacheKey == nil || *req.CacheKey == "" {
			httputil.BadRequest(w, "cacheKey is required for strategy=key")
			return
		}
		deleted, err = h.deleteCacheByKey(ctx, req.UserID, *req.CacheKey)
		// Also clear from Redis
		if h.redis != nil {
			_ = h.redis.Del(ctx, *req.CacheKey)
		}

	default:
		httputil.BadRequest(w, "invalid strategy")
		return
	}

	if err != nil {
		h.logger.Error("failed to invalidate cache", "error", err, "strategy", req.Strategy)
		httputil.Error(w, http.StatusInternalServerError, "failed to invalidate cache")
		return
	}

	h.logAdminAudit(ctx, r, "CACHE_INVALIDATE", "cache", "", map[string]interface{}{
		"strategy": req.Strategy,
		"deleted":  deleted,
	})
	h.logger.Info("cache invalidated", "strategy", req.Strategy, "deleted", deleted)
	httputil.Success(w, map[string]interface{}{
		"strategy": req.Strategy,
		"deleted":  deleted,
	})
}

// ===============================
// Vendor Management
// ===============================

// ListVendors returns all configured vendors
func (h *Handler) ListVendors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vendors, err := h.getVendorConfigs(ctx)
	if err != nil {
		h.logger.Error("failed to list vendors", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list vendors")
		return
	}

	httputil.Success(w, map[string]interface{}{
		"vendors": vendors,
		"total":   len(vendors),
	})
}

// VendorHealth returns health status for a vendor
func (h *Handler) VendorHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vendorID := chi.URLParam(r, "id")
	if vendorID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	// Get vendor config
	vendor, err := h.getVendorByID(ctx, vendorID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "vendor not found")
			return
		}
		h.logger.Error("failed to get vendor", "error", err, "vendor_id", vendorID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get vendor")
		return
	}

	// Perform health check
	result := h.checkVendorHealth(ctx, vendor)

	httputil.Success(w, result)
}

// ToggleVendor enables or disables a vendor
func (h *Handler) ToggleVendor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vendorID := chi.URLParam(r, "id")
	if vendorID == "" {
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

	// Update vendor status
	if err := h.updateVendorEnabled(ctx, vendorID, req.Enabled); err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "vendor not found")
			return
		}
		h.logger.Error("failed to toggle vendor", "error", err, "vendor_id", vendorID)
		httputil.Error(w, http.StatusInternalServerError, "failed to toggle vendor")
		return
	}

	h.logAdminAudit(ctx, r, "WHITELIST_TOGGLE", "vendor", vendorID, map[string]interface{}{
		"enabled": req.Enabled,
	})
	h.logger.Info("vendor toggled", "vendor_id", vendorID, "enabled", req.Enabled)
	httputil.Success(w, map[string]interface{}{
		"vendorId": vendorID,
		"enabled":  req.Enabled,
	})
}

// ===============================
// Monitoring
// ===============================

// Metrics returns application metrics
func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	metrics := make(map[string]interface{})

	// User statistics
	userStats, err := h.getUserStats(ctx)
	if err != nil {
		h.logger.Warn("failed to get user stats", "error", err)
	} else {
		metrics["users"] = userStats
	}

	// Cache statistics
	cacheStats, err := h.getCacheStats(ctx)
	if err != nil {
		h.logger.Warn("failed to get cache stats", "error", err)
	} else {
		metrics["cache"] = cacheStats
	}

	// Database pool stats
	metrics["database"] = h.db.AllStats()

	// Redis stats
	if h.redis != nil {
		metrics["redis"] = h.redis.Stats()
	}

	// Timestamp
	metrics["timestamp"] = time.Now().UTC().Format(time.RFC3339)

	httputil.Success(w, metrics)
}

// HealthCheck returns detailed health status
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	status := "healthy"
	details := make(map[string]interface{})

	// Check database connections
	dbHealth := h.db.Health(ctx)
	dbDetails := make(map[string]string)
	for name, err := range dbHealth {
		if err != nil {
			status = "degraded"
			dbDetails[name] = err.Error()
		} else {
			dbDetails[name] = "healthy"
		}
	}
	details["database"] = dbDetails

	// Check Redis
	if h.redis != nil {
		if err := h.redis.Health(ctx); err != nil {
			status = "degraded"
			details["redis"] = err.Error()
		} else {
			details["redis"] = "healthy"
		}
	}

	response := map[string]interface{}{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"version":   "2.0.0",
		"details":   details,
	}

	if status == "healthy" {
		httputil.JSON(w, http.StatusOK, response)
	} else {
		httputil.JSON(w, http.StatusServiceUnavailable, response)
	}
}

// ===============================
// AI Metrics
// ===============================

// GetAIMetrics returns aggregated AI usage metrics
func (h *Handler) GetAIMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	metrics, err := h.getAIMetrics(ctx)
	if err != nil {
		h.logger.Error("failed to get AI metrics", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get AI metrics")
		return
	}

	httputil.Success(w, metrics)
}

// GetAIUsage returns detailed AI usage records
func (h *Handler) GetAIUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	userID := r.URL.Query().Get("userId")
	cacheType := r.URL.Query().Get("cacheType")

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	records, total, err := h.getAIUsageRecords(ctx, userID, cacheType, pageSize, offset)
	if err != nil {
		h.logger.Error("failed to get AI usage", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get AI usage")
		return
	}

	httputil.Success(w, map[string]interface{}{
		"records": records,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// GetAICacheStatus returns AI cache statistics and status
func (h *Handler) GetAICacheStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cacheStats, err := h.getCacheStats(ctx)
	if err != nil {
		h.logger.Error("failed to get AI cache status", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get AI cache status")
		return
	}

	// Get cache type breakdown
	typeBreakdown, err := h.getCacheTypeBreakdown(ctx)
	if err != nil {
		h.logger.Warn("failed to get cache type breakdown", "error", err)
		typeBreakdown = []map[string]interface{}{}
	}

	httputil.Success(w, map[string]interface{}{
		"stats":         cacheStats,
		"typeBreakdown": typeBreakdown,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
	})
}

// ===============================
// Investor Reports Management
// ===============================

// ListInvestorReports returns a list of investor reports for admin
func (h *Handler) ListInvestorReports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 20)
	status := r.URL.Query().Get("status")
	userID := r.URL.Query().Get("userId")

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	reports, total, err := h.getInvestorReports(ctx, status, userID, pageSize, offset)
	if err != nil {
		h.logger.Error("failed to list investor reports", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list reports")
		return
	}

	httputil.Success(w, map[string]interface{}{
		"reports": reports,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// RetryInvestorReport retries a failed investor report
func (h *Handler) RetryInvestorReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reportID := chi.URLParam(r, "id")
	if reportID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	// Update report status to PENDING to trigger retry
	err := h.updateReportStatus(ctx, reportID, "PENDING", nil)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "report not found")
			return
		}
		h.logger.Error("failed to retry report", "error", err, "report_id", reportID)
		httputil.Error(w, http.StatusInternalServerError, "failed to retry report")
		return
	}

	h.logger.Info("investor report retry queued", "report_id", reportID)
	httputil.Success(w, map[string]interface{}{
		"reportId": reportID,
		"status":   "PENDING",
		"message":  "Report retry queued",
	})
}

// ===============================
// Audit Log
// ===============================

// GetAuditLog returns audit log entries
func (h *Handler) GetAuditLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	userID := r.URL.Query().Get("userId")
	action := r.URL.Query().Get("action")
	resource := r.URL.Query().Get("resource")

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	entries, total, err := h.getAuditLogEntries(ctx, userID, action, resource, pageSize, offset)
	if err != nil {
		h.logger.Error("failed to get audit log", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get audit log")
		return
	}

	httputil.Success(w, map[string]interface{}{
		"entries": entries,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// ===============================
// Analytics
// ===============================

// GetAnalytics returns application analytics
func (h *Handler) GetAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	analytics, err := h.getAnalytics(ctx)
	if err != nil {
		h.logger.Error("failed to get analytics", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get analytics")
		return
	}

	httputil.Success(w, analytics)
}

// ===============================
// System Alerts
// ===============================

// GetSystemAlerts returns system alerts
func (h *Handler) GetSystemAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	showResolved := r.URL.Query().Get("showResolved") == "true"
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	alerts, total, err := h.getSystemAlerts(ctx, showResolved, pageSize, offset)
	if err != nil {
		h.logger.Error("failed to get system alerts", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get system alerts")
		return
	}

	httputil.Success(w, map[string]interface{}{
		"alerts": alerts,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// ResolveSystemAlert marks a system alert as resolved
func (h *Handler) ResolveSystemAlert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	alertID := chi.URLParam(r, "id")
	if alertID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	// Get admin user ID from context (would need middleware support)
	resolvedBy := "admin" // Placeholder - should get from auth context

	err := h.resolveSystemAlert(ctx, alertID, resolvedBy)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "alert not found")
			return
		}
		h.logger.Error("failed to resolve alert", "error", err, "alert_id", alertID)
		httputil.Error(w, http.StatusInternalServerError, "failed to resolve alert")
		return
	}

	h.logAdminAudit(ctx, r, "ALERT_DISMISS", "system_alert", alertID, nil)
	h.logger.Info("system alert resolved", "alert_id", alertID)
	httputil.Success(w, map[string]interface{}{
		"alertId":  alertID,
		"resolved": true,
	})
}

// ===============================
// Vendor Costs
// ===============================

// GetVendorCosts returns vendor usage and costs
func (h *Handler) GetVendorCosts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	period := r.URL.Query().Get("period") // "day", "week", "month"
	if period == "" {
		period = "month"
	}

	costs, err := h.getVendorCosts(ctx, period)
	if err != nil {
		h.logger.Error("failed to get vendor costs", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get vendor costs")
		return
	}

	// Calculate totals
	var totalCost float64
	var totalRequests int64
	for _, c := range costs {
		totalCost += c.TotalCost
		totalRequests += c.RequestCount
	}

	httputil.Success(w, map[string]interface{}{
		"costs":         costs,
		"totalCost":     totalCost,
		"totalRequests": totalRequests,
		"period":        period,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
	})
}

// ===============================
// Audit Logging
// ===============================

// logAdminAudit writes an admin audit log entry for admin operations.
// action must be a valid AdminAction enum value.
func (h *Handler) logAdminAudit(ctx context.Context, r *http.Request, action, resource, resourceID string, details map[string]interface{}) {
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	clientIP := r.RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			clientIP = xff[:idx]
		} else {
			clientIP = xff
		}
	} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
		clientIP = xri
	}

	// Extract admin identity from JWT claims in context
	adminID := "unknown"
	adminEmail := "unknown"
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		// Parse without validation to extract claims (token was already validated by middleware)
		parser := jwt.NewParser(jwt.WithoutClaimsValidation())
		claims := jwt.MapClaims{}
		_, _, _ = parser.ParseUnverified(tokenStr, claims)
		if uid, ok := claims["userId"].(string); ok {
			adminID = uid
		}
		if email, ok := claims["email"].(string); ok {
			adminEmail = email
		}
	}

	var detailsJSON json.RawMessage
	if details != nil {
		detailsJSON, _ = json.Marshal(details)
	}

	q := queries.New(h.db.Main)
	_, err := q.CreateAdminAuditLog(ctx, queries.CreateAdminAuditLogParams{
		ID:         id,
		AdminId:    adminID,
		AdminEmail: adminEmail,
		Action:     action,
		Resource:   resource,
		ResourceId: pgtype.Text{String: resourceID, Valid: resourceID != ""},
		Details:    detailsJSON,
		IpAddress:  clientIP,
		UserAgent:  r.UserAgent(),
	})
	if err != nil {
		h.logger.Warn("failed to write admin audit log", "error", err, "action", action)
	}
}

// ===============================
// Database Helper Methods
// ===============================

func (h *Handler) listAllUsers(ctx context.Context, limit, offset int) ([]User, int64, error) {
	// Get total count
	var total int64
	err := h.db.Main.QueryRow(ctx, `SELECT COUNT(*) FROM "User"`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// Get users with pagination
	rows, err := h.db.Main.Query(ctx, `
		SELECT id, "clerkUserId", email, name, role, "subscriptionPlan", "subscriptionStatus",
		       "monthlyUsage", "usageResetAt", "lastLoginAt", "createdAt", "updatedAt"
		FROM "User"
		ORDER BY "createdAt" DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		err := rows.Scan(
			&u.ID, &u.ClerkUserID, &u.Email, &u.Name, &u.Role,
			&u.SubscriptionPlan, &u.SubscriptionStatus, &u.MonthlyUsage,
			&u.UsageResetAt, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}

	return users, total, nil
}

func (h *Handler) searchUsersByEmail(ctx context.Context, search string, limit, offset int) ([]User, int64, error) {
	searchPattern := "%" + strings.ToLower(search) + "%"

	// Get total count
	var total int64
	err := h.db.Main.QueryRow(ctx, `
		SELECT COUNT(*) FROM "User" WHERE LOWER(email) LIKE $1
	`, searchPattern).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// Get users with pagination
	rows, err := h.db.Main.Query(ctx, `
		SELECT id, "clerkUserId", email, name, role, "subscriptionPlan", "subscriptionStatus",
		       "monthlyUsage", "usageResetAt", "lastLoginAt", "createdAt", "updatedAt"
		FROM "User"
		WHERE LOWER(email) LIKE $1
		ORDER BY "createdAt" DESC
		LIMIT $2 OFFSET $3
	`, searchPattern, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		err := rows.Scan(
			&u.ID, &u.ClerkUserID, &u.Email, &u.Name, &u.Role,
			&u.SubscriptionPlan, &u.SubscriptionStatus, &u.MonthlyUsage,
			&u.UsageResetAt, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}

	return users, total, nil
}

func (h *Handler) getUserByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := h.db.Main.QueryRow(ctx, `
		SELECT id, "clerkUserId", email, name, role, "subscriptionPlan", "subscriptionStatus",
		       "monthlyUsage", "usageResetAt", "lastLoginAt", "createdAt", "updatedAt"
		FROM "User"
		WHERE id = $1
	`, id).Scan(
		&u.ID, &u.ClerkUserID, &u.Email, &u.Name, &u.Role,
		&u.SubscriptionPlan, &u.SubscriptionStatus, &u.MonthlyUsage,
		&u.UsageResetAt, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (h *Handler) updateUserProfile(ctx context.Context, id string, req *UpdateUserRequest) error {
	_, err := h.db.Main.Exec(ctx, `
		UPDATE "User" SET
			name = COALESCE($2, name),
			email = COALESCE($3, email),
			role = COALESCE($4, role),
			"subscriptionPlan" = COALESCE($5, "subscriptionPlan"),
			"subscriptionStatus" = COALESCE($6, "subscriptionStatus"),
			"updatedAt" = NOW()
		WHERE id = $1
	`, id, req.Name, req.Email, req.Role, req.SubscriptionPlan, req.SubscriptionStatus)
	return err
}

func (h *Handler) getUserStats(ctx context.Context) (*UserStats, error) {
	var stats UserStats
	err := h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as total_users,
			COUNT(*) FILTER (WHERE role = 'admin') as admin_count,
			COUNT(*) FILTER (WHERE role = 'user') as user_count,
			COUNT(*) FILTER (WHERE "subscriptionPlan" = 'free') as free_count,
			COUNT(*) FILTER (WHERE "subscriptionPlan" = 'pro') as pro_count,
			COUNT(*) FILTER (WHERE "subscriptionPlan" = 'enterprise') as enterprise_count,
			COUNT(*) FILTER (WHERE "lastLoginAt" > NOW() - INTERVAL '7 days') as active_last_week
		FROM "User"
	`).Scan(
		&stats.TotalUsers, &stats.AdminCount, &stats.UserCount,
		&stats.FreeCount, &stats.ProCount, &stats.EnterpriseCount,
		&stats.ActiveLastWeek,
	)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}

func (h *Handler) generateImpersonationToken(user *User, expiry time.Duration) (string, error) {
	now := time.Now()

	clerkUserID := ""
	if user.ClerkUserID != nil {
		clerkUserID = *user.ClerkUserID
	}

	claims := jwt.MapClaims{
		"userId":      user.ID,
		"clerkUserId": clerkUserID,
		"email":       user.Email,
		"role":        user.Role,
		"exp":         now.Add(expiry).Unix(),
		"iat":         now.Unix(),
		"nbf":         now.Unix(),
		"iss":         "estara-ai",
		"sub":         user.ID,
		"impersonated": true,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.cfg.JWT.Secret))
}

// ===============================
// Cache Helper Methods
// ===============================

func (h *Handler) getCacheStats(ctx context.Context) (*CacheStats, error) {
	var stats CacheStats
	err := h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as total_entries,
			COUNT(*) FILTER (WHERE "expiresAt" < NOW()) as expired_entries,
			COALESCE(SUM("inputTokens"), 0) as total_input_tokens,
			COALESCE(SUM("outputTokens"), 0) as total_output_tokens,
			COALESCE(SUM("totalCost"), 0) as total_cost,
			COUNT(DISTINCT "userId") as unique_users,
			COUNT(DISTINCT "cacheType") as cache_types
		FROM "AiResponseCache"
	`).Scan(
		&stats.TotalEntries, &stats.ExpiredEntries,
		&stats.TotalInputTokens, &stats.TotalOutputTokens,
		&stats.TotalCost, &stats.UniqueUsers, &stats.CacheTypes,
	)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}

func (h *Handler) deleteAllCache(ctx context.Context) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `DELETE FROM "AiResponseCache"`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) deleteExpiredCache(ctx context.Context) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `DELETE FROM "AiResponseCache" WHERE "expiresAt" < NOW()`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) deleteCacheByType(ctx context.Context, cacheType string) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `DELETE FROM "AiResponseCache" WHERE "cacheType" = $1`, cacheType)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) deleteCacheByUser(ctx context.Context, userID string) (int64, error) {
	result, err := h.db.Main.Exec(ctx, `DELETE FROM "AiResponseCache" WHERE "userId" = $1`, userID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (h *Handler) deleteCacheByKey(ctx context.Context, userID *string, cacheKey string) (int64, error) {
	if userID != nil && *userID != "" {
		result, err := h.db.Main.Exec(ctx, `DELETE FROM "AiResponseCache" WHERE "userId" = $1 AND "cacheKey" = $2`, *userID, cacheKey)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected(), nil
	}
	// Delete by key only
	result, err := h.db.Main.Exec(ctx, `DELETE FROM "AiResponseCache" WHERE "cacheKey" = $1`, cacheKey)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// ===============================
// Vendor Helper Methods
// ===============================

func (h *Handler) getVendorConfigs(ctx context.Context) ([]Vendor, error) {
	rows, err := h.db.Main.Query(ctx, `
		SELECT id, name, category, enabled, "healthUrl", "lastChecked", status
		FROM "VendorConfig"
		ORDER BY category, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vendors []Vendor
	for rows.Next() {
		var v Vendor
		var healthURL, status *string
		err := rows.Scan(&v.ID, &v.Name, &v.Category, &v.Enabled, &healthURL, &v.LastChecked, &status)
		if err != nil {
			return nil, err
		}
		if healthURL != nil {
			v.HealthURL = *healthURL
		}
		if status != nil {
			v.Status = *status
		} else {
			v.Status = "unknown"
		}
		vendors = append(vendors, v)
	}

	return vendors, nil
}

func (h *Handler) getVendorByID(ctx context.Context, id string) (*Vendor, error) {
	var v Vendor
	var healthURL, status *string
	err := h.db.Main.QueryRow(ctx, `
		SELECT id, name, category, enabled, "healthUrl", "lastChecked", status
		FROM "VendorConfig"
		WHERE id = $1
	`, id).Scan(&v.ID, &v.Name, &v.Category, &v.Enabled, &healthURL, &v.LastChecked, &status)
	if err != nil {
		return nil, err
	}
	if healthURL != nil {
		v.HealthURL = *healthURL
	}
	if status != nil {
		v.Status = *status
	} else {
		v.Status = "unknown"
	}
	return &v, nil
}

func (h *Handler) checkVendorHealth(ctx context.Context, vendor *Vendor) *VendorHealthResult {
	result := &VendorHealthResult{
		Vendor:    vendor.Name,
		CheckedAt: time.Now().UTC(),
	}

	if !vendor.Enabled {
		result.Status = "disabled"
		result.Message = "vendor is disabled"
		return result
	}

	if vendor.HealthURL == "" {
		result.Status = "unknown"
		result.Message = "no health URL configured"
		return result
	}

	// Perform HTTP health check
	start := time.Now()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(vendor.HealthURL)
	result.Latency = time.Since(start).Milliseconds()

	if err != nil {
		result.Status = "unhealthy"
		result.Message = fmt.Sprintf("health check failed: %v", err)
		h.updateVendorStatus(ctx, vendor.ID, "unhealthy")
		h.createVendorHealthAlert(ctx, vendor, result.Message)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Status = "healthy"
		result.Message = fmt.Sprintf("status code: %d", resp.StatusCode)
		h.updateVendorStatus(ctx, vendor.ID, "healthy")
	} else {
		result.Status = "unhealthy"
		result.Message = fmt.Sprintf("unexpected status code: %d", resp.StatusCode)
		h.updateVendorStatus(ctx, vendor.ID, "unhealthy")
		h.createVendorHealthAlert(ctx, vendor, result.Message)
	}

	return result
}

// createVendorHealthAlert creates a system alert when a vendor health check fails
func (h *Handler) createVendorHealthAlert(ctx context.Context, vendor *Vendor, message string) {
	alertKey := fmt.Sprintf("vendor_unhealthy_%s", vendor.ID)
	q := queries.New(h.db.Main)
	_, err := q.UpsertSystemAlert(ctx, queries.UpsertSystemAlertParams{
		ID:             alertKey,
		Type:           "vendor_error",
		Severity:       "warning",
		Title:          fmt.Sprintf("Vendor Unhealthy: %s", vendor.Name),
		Description:    fmt.Sprintf("Health check failed for %s (%s): %s", vendor.Name, vendor.Category, message),
		AlertKey:       alertKey,
		Metadata:       fmt.Sprintf(`{"vendor_id":"%s","vendor_name":"%s","category":"%s"}`, vendor.ID, vendor.Name, vendor.Category),
		ActionRequired: true,
	})
	if err != nil {
		h.logger.Warn("failed to create vendor health alert", "error", err)
	}
}

func (h *Handler) updateVendorStatus(ctx context.Context, id, status string) {
	_, err := h.db.Main.Exec(ctx, `
		UPDATE "VendorConfig"
		SET status = $2, "lastChecked" = NOW(), "updatedAt" = NOW()
		WHERE id = $1
	`, id, status)
	if err != nil {
		h.logger.Warn("failed to update vendor status", "error", err, "vendor_id", id)
	}
}

func (h *Handler) updateVendorEnabled(ctx context.Context, id string, enabled bool) error {
	result, err := h.db.Main.Exec(ctx, `
		UPDATE "VendorConfig"
		SET enabled = $2, "updatedAt" = NOW()
		WHERE id = $1
	`, id, enabled)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ===============================
// AI Metrics Helper Methods
// ===============================

func (h *Handler) getAIMetrics(ctx context.Context) (*AIMetrics, error) {
	var metrics AIMetrics

	// Get overall metrics from cache table
	err := h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as total_requests,
			COALESCE(SUM("inputTokens"), 0) as total_input_tokens,
			COALESCE(SUM("outputTokens"), 0) as total_output_tokens,
			COALESCE(SUM("totalCost"), 0) as total_cost
		FROM "AiResponseCache"
	`).Scan(
		&metrics.TotalRequests,
		&metrics.TotalInputTokens,
		&metrics.TotalOutputTokens,
		&metrics.TotalCost,
	)
	if err != nil {
		return nil, err
	}

	// Get today's metrics
	err = h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as requests_today,
			COALESCE(SUM("totalCost"), 0) as cost_today
		FROM "AiResponseCache"
		WHERE "createdAt" >= CURRENT_DATE
	`).Scan(&metrics.RequestsToday, &metrics.CostToday)
	if err != nil {
		h.logger.Warn("failed to get today's metrics", "error", err)
	}

	// Get this month's metrics
	err = h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as requests_this_month,
			COALESCE(SUM("totalCost"), 0) as cost_this_month
		FROM "AiResponseCache"
		WHERE "createdAt" >= DATE_TRUNC('month', CURRENT_DATE)
	`).Scan(&metrics.RequestsThisMonth, &metrics.CostThisMonth)
	if err != nil {
		h.logger.Warn("failed to get monthly metrics", "error", err)
	}

	// Calculate cache hit rate (simplified - based on cached responses with hits > 0)
	var cachedCount, totalCount int64
	err = h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE "hitCount" > 0) as cached_count,
			COUNT(*) as total_count
		FROM "AiResponseCache"
	`).Scan(&cachedCount, &totalCount)
	if err == nil && totalCount > 0 {
		metrics.CacheHitRate = float64(cachedCount) / float64(totalCount) * 100
	}

	return &metrics, nil
}

func (h *Handler) getAIUsageRecords(ctx context.Context, userID, cacheType string, limit, offset int) ([]AIUsageRecord, int64, error) {
	// Build query with optional filters
	whereClause := "1=1"
	args := make([]interface{}, 0)
	argIndex := 1

	if userID != "" {
		whereClause += fmt.Sprintf(` AND c."userId" = $%d`, argIndex)
		args = append(args, userID)
		argIndex++
	}
	if cacheType != "" {
		whereClause += fmt.Sprintf(` AND c."cacheType" = $%d`, argIndex)
		args = append(args, cacheType)
		argIndex++
	}

	// Get total count
	var total int64
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "AiResponseCache" c WHERE %s`, whereClause)
	err := h.db.Main.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// Get records with pagination
	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT c.id, c."userId", u.email, c."cacheType", c.model, c."inputTokens", c."outputTokens", c."totalCost", c."createdAt"
		FROM "AiResponseCache" c
		LEFT JOIN "User" u ON c."userId" = u.id
		WHERE %s
		ORDER BY c."createdAt" DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	rows, err := h.db.Main.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var records []AIUsageRecord
	for rows.Next() {
		var r AIUsageRecord
		err := rows.Scan(
			&r.ID, &r.UserID, &r.Email, &r.CacheType, &r.Model,
			&r.InputTokens, &r.OutputTokens, &r.Cost, &r.CreatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, r)
	}

	if records == nil {
		records = []AIUsageRecord{}
	}

	return records, total, nil
}

func (h *Handler) getCacheTypeBreakdown(ctx context.Context) ([]map[string]interface{}, error) {
	rows, err := h.db.Main.Query(ctx, `
		SELECT
			"cacheType",
			COUNT(*) as count,
			COALESCE(SUM("inputTokens"), 0) as input_tokens,
			COALESCE(SUM("outputTokens"), 0) as output_tokens,
			COALESCE(SUM("totalCost"), 0) as cost
		FROM "AiResponseCache"
		GROUP BY "cacheType"
		ORDER BY count DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var breakdown []map[string]interface{}
	for rows.Next() {
		var cacheType string
		var count, inputTokens, outputTokens int64
		var cost float64
		if err := rows.Scan(&cacheType, &count, &inputTokens, &outputTokens, &cost); err != nil {
			return nil, err
		}
		breakdown = append(breakdown, map[string]interface{}{
			"cacheType":    cacheType,
			"count":        count,
			"inputTokens":  inputTokens,
			"outputTokens": outputTokens,
			"cost":         cost,
		})
	}

	if breakdown == nil {
		breakdown = []map[string]interface{}{}
	}

	return breakdown, nil
}

// ===============================
// Investor Reports Helper Methods
// ===============================

func (h *Handler) getInvestorReports(ctx context.Context, status, userID string, limit, offset int) ([]InvestorReportAdmin, int64, error) {
	// Build query with optional filters
	whereClause := "1=1"
	args := make([]interface{}, 0)
	argIndex := 1

	if status != "" {
		whereClause += fmt.Sprintf(` AND r.status = $%d`, argIndex)
		args = append(args, status)
		argIndex++
	}
	if userID != "" {
		whereClause += fmt.Sprintf(` AND r."userId" = $%d`, argIndex)
		args = append(args, userID)
		argIndex++
	}

	// Get total count
	var total int64
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM investor_reports r WHERE %s`, whereClause)
	err := h.db.Main.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// Get reports with pagination
	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT r.id, r."userId", u.email, r.report_type, r.status, r.address, r.city, r.state,
		       r.error_message, r.created_at, r.completed_at
		FROM investor_reports r
		LEFT JOIN "User" u ON r."userId" = u.id
		WHERE %s
		ORDER BY r.created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	rows, err := h.db.Main.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var reports []InvestorReportAdmin
	for rows.Next() {
		var r InvestorReportAdmin
		err := rows.Scan(
			&r.ID, &r.UserID, &r.Email, &r.ReportType, &r.Status,
			&r.Address, &r.City, &r.State, &r.ErrorMessage,
			&r.CreatedAt, &r.CompletedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		reports = append(reports, r)
	}

	if reports == nil {
		reports = []InvestorReportAdmin{}
	}

	return reports, total, nil
}

func (h *Handler) updateReportStatus(ctx context.Context, reportID, status string, errorMsg *string) error {
	var query string
	var args []interface{}

	if status == "PENDING" {
		// Reset for retry
		query = `
			UPDATE investor_reports
			SET status = $2, error_message = NULL, updated_at = NOW()
			WHERE id = $1
		`
		args = []interface{}{reportID, status}
	} else {
		query = `
			UPDATE investor_reports
			SET status = $2, error_message = $3, updated_at = NOW()
			WHERE id = $1
		`
		args = []interface{}{reportID, status, errorMsg}
	}

	result, err := h.db.Main.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ===============================
// Audit Log Helper Methods
// ===============================

func (h *Handler) getAuditLogEntries(ctx context.Context, userID, action, resource string, limit, offset int) ([]AuditLogEntry, int64, error) {
	// Build query with optional filters
	whereClause := "1=1"
	args := make([]interface{}, 0)
	argIndex := 1

	if userID != "" {
		whereClause += fmt.Sprintf(` AND "userId" = $%d`, argIndex)
		args = append(args, userID)
		argIndex++
	}
	if action != "" {
		whereClause += fmt.Sprintf(` AND action = $%d`, argIndex)
		args = append(args, action)
		argIndex++
	}
	if resource != "" {
		whereClause += fmt.Sprintf(` AND resource = $%d`, argIndex)
		args = append(args, resource)
		argIndex++
	}

	// Get total count
	var total int64
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM audit_logs WHERE %s`, whereClause)
	err := h.db.Main.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		// Table might not exist, return empty results
		h.logger.Warn("failed to count audit logs", "error", err)
		return []AuditLogEntry{}, 0, nil
	}

	// Get entries with pagination
	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT id, "userId", action, resource, "resourceId", details, "ipAddress", "userAgent", "createdAt"
		FROM audit_logs
		WHERE %s
		ORDER BY "createdAt" DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	rows, err := h.db.Main.Query(ctx, query, args...)
	if err != nil {
		h.logger.Warn("failed to query audit logs", "error", err)
		return []AuditLogEntry{}, 0, nil
	}
	defer rows.Close()

	var entries []AuditLogEntry
	for rows.Next() {
		var e AuditLogEntry
		var detailsJSON []byte
		err := rows.Scan(
			&e.ID, &e.UserID, &e.Action, &e.Resource, &e.ResourceID,
			&detailsJSON, &e.IPAddress, &e.UserAgent, &e.CreatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		if detailsJSON != nil {
			_ = json.Unmarshal(detailsJSON, &e.Details)
		}
		entries = append(entries, e)
	}

	if entries == nil {
		entries = []AuditLogEntry{}
	}

	return entries, total, nil
}

// ===============================
// Analytics Helper Methods
// ===============================

func (h *Handler) getAnalytics(ctx context.Context) (*Analytics, error) {
	analytics := &Analytics{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// User analytics
	err := h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE "updatedAt" > NOW() - INTERVAL '7 days') as active_this_week,
			COUNT(*) FILTER (WHERE "updatedAt" > NOW() - INTERVAL '30 days') as active_this_month,
			COUNT(*) FILTER (WHERE "createdAt" > NOW() - INTERVAL '7 days') as new_this_week,
			COUNT(*) FILTER (WHERE "createdAt" > NOW() - INTERVAL '30 days') as new_this_month
		FROM users
	`).Scan(
		&analytics.Users.Total,
		&analytics.Users.ActiveThisWeek,
		&analytics.Users.ActiveThisMonth,
		&analytics.Users.NewThisWeek,
		&analytics.Users.NewThisMonth,
	)
	if err != nil {
		h.logger.Warn("failed to get user analytics", "error", err)
	}

	// Report analytics
	err = h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as total_generated,
			COUNT(*) FILTER (WHERE "createdAt" > NOW() - INTERVAL '7 days') as this_week,
			COUNT(*) FILTER (WHERE "createdAt" > NOW() - INTERVAL '30 days') as this_month,
			COUNT(*) FILTER (WHERE status = 'FAILED' AND "createdAt" > NOW() - INTERVAL '7 days') as failed_this_week
		FROM investor_reports
	`).Scan(
		&analytics.Reports.TotalGenerated,
		&analytics.Reports.ThisWeek,
		&analytics.Reports.ThisMonth,
		&analytics.Reports.FailedThisWeek,
	)
	if err != nil {
		h.logger.Warn("failed to get report analytics", "error", err)
	}

	// API analytics from cache table
	err = h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as total_requests,
			COUNT(*) FILTER (WHERE "createdAt" >= CURRENT_DATE) as requests_today
		FROM "AiResponseCache"
	`).Scan(
		&analytics.API.TotalRequests,
		&analytics.API.RequestsToday,
	)
	if err != nil {
		h.logger.Warn("failed to get API analytics", "error", err)
	}

	// Revenue analytics from subscriptions
	err = h.db.Main.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE
				WHEN status = 'ACTIVE' AND tier = 'INVESTOR' THEN 29.99
				WHEN status = 'ACTIVE' AND tier = 'PROFESSIONAL' THEN 49.99
				WHEN status = 'ACTIVE' AND tier = 'ANNUAL_ACCESS' THEN 99.99
				WHEN status = 'ACTIVE' AND tier = 'PROFESSIONAL_ALLOCATOR' THEN 149.99
				WHEN status = 'ACTIVE' AND tier = 'AAPI_INVESTOR' THEN 79.99
				WHEN status = 'ACTIVE' AND tier = 'AAPI_ALLOCATOR' THEN 199.99
				ELSE 0
			END), 0) as mrr,
			COUNT(*) FILTER (WHERE status = 'ACTIVE' AND "createdAt" > NOW() - INTERVAL '30 days') as new_subs,
			COUNT(*) FILTER (WHERE status = 'CANCELED' AND "updatedAt" > NOW() - INTERVAL '30 days') as churned
		FROM subscriptions
	`).Scan(
		&analytics.Revenue.TotalMRR,
		&analytics.Revenue.NewSubscriptions,
		&analytics.Revenue.Churned,
	)
	if err != nil {
		h.logger.Warn("failed to get revenue analytics", "error", err)
	}

	return analytics, nil
}

// ===============================
// System Alerts Helper Methods
// ===============================

func (h *Handler) getSystemAlerts(ctx context.Context, showResolved bool, limit, offset int) ([]SystemAlert, int64, error) {
	whereClause := "1=1"
	if !showResolved {
		whereClause = "resolved = false"
	}

	// Get total count
	var total int64
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM system_alerts WHERE %s`, whereClause)
	err := h.db.Main.QueryRow(ctx, countQuery).Scan(&total)
	if err != nil {
		// Table might not exist, return empty results
		h.logger.Warn("failed to count system alerts", "error", err)
		return []SystemAlert{}, 0, nil
	}

	// Get alerts with pagination
	query := fmt.Sprintf(`
		SELECT id, type, severity, title, message, resolved, "resolvedAt", "resolvedBy", "createdAt"
		FROM system_alerts
		WHERE %s
		ORDER BY
			CASE severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END,
			"createdAt" DESC
		LIMIT $1 OFFSET $2
	`, whereClause)

	rows, err := h.db.Main.Query(ctx, query, limit, offset)
	if err != nil {
		h.logger.Warn("failed to query system alerts", "error", err)
		return []SystemAlert{}, 0, nil
	}
	defer rows.Close()

	var alerts []SystemAlert
	for rows.Next() {
		var a SystemAlert
		err := rows.Scan(
			&a.ID, &a.Type, &a.Severity, &a.Title, &a.Message,
			&a.Resolved, &a.ResolvedAt, &a.ResolvedBy, &a.CreatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		alerts = append(alerts, a)
	}

	if alerts == nil {
		alerts = []SystemAlert{}
	}

	return alerts, total, nil
}

func (h *Handler) resolveSystemAlert(ctx context.Context, alertID, resolvedBy string) error {
	result, err := h.db.Main.Exec(ctx, `
		UPDATE system_alerts
		SET resolved = true, "resolvedAt" = NOW(), "resolvedBy" = $2
		WHERE id = $1 AND resolved = false
	`, alertID, resolvedBy)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ===============================
// Vendor Costs Helper Methods
// ===============================

func (h *Handler) getVendorCosts(ctx context.Context, period string) ([]VendorCostRecord, error) {
	var interval string
	switch period {
	case "day":
		interval = "1 day"
	case "week":
		interval = "7 days"
	default:
		interval = "30 days"
	}

	// Try to get vendor usage summaries
	query := fmt.Sprintf(`
		SELECT
			v.id as vendor_id,
			v.name as vendor_name,
			v.category,
			COALESCE(SUM(s.total_cost), 0) as total_cost,
			COALESCE(SUM(s.request_count), 0) as request_count,
			$1 as period,
			MAX(COALESCE(s.recorded_at, NOW())) as recorded_at
		FROM "VendorConfig" v
		LEFT JOIN vendor_usage_summaries s ON v.id = s.vendor_id
			AND s.recorded_at > NOW() - INTERVAL '%s'
		GROUP BY v.id, v.name, v.category
		ORDER BY total_cost DESC
	`, interval)

	rows, err := h.db.Main.Query(ctx, query, period)
	if err != nil {
		// Table might not exist - return basic vendor info
		h.logger.Warn("failed to query vendor costs, falling back to basic info", "error", err)
		return h.getBasicVendorCosts(ctx, period)
	}
	defer rows.Close()

	var costs []VendorCostRecord
	for rows.Next() {
		var c VendorCostRecord
		err := rows.Scan(
			&c.VendorID, &c.VendorName, &c.Category,
			&c.TotalCost, &c.RequestCount, &c.Period, &c.RecordedAt,
		)
		if err != nil {
			return nil, err
		}
		costs = append(costs, c)
	}

	if costs == nil {
		costs = []VendorCostRecord{}
	}

	return costs, nil
}

func (h *Handler) getBasicVendorCosts(ctx context.Context, period string) ([]VendorCostRecord, error) {
	rows, err := h.db.Main.Query(ctx, `
		SELECT id, name, category
		FROM "VendorConfig"
		WHERE enabled = true
		ORDER BY name
	`)
	if err != nil {
		return []VendorCostRecord{}, nil
	}
	defer rows.Close()

	var costs []VendorCostRecord
	for rows.Next() {
		var c VendorCostRecord
		err := rows.Scan(&c.VendorID, &c.VendorName, &c.Category)
		if err != nil {
			continue
		}
		c.Period = period
		c.RecordedAt = time.Now()
		costs = append(costs, c)
	}

	if costs == nil {
		costs = []VendorCostRecord{}
	}

	return costs, nil
}
