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
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	billingService "github.com/estara-ai/www/internal/services/billing"
	"github.com/estara-ai/www/internal/services/market/importer"
	"github.com/estara-ai/www/internal/services/whitelist"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles admin-related HTTP requests
type Handler struct {
	store     *dbstore.Store
	redis     *redisClient.Client
	cfg       *config.Config
	auth      *middleware.AuthMiddleware
	validate  *validator.Validate
	logger    *slog.Logger
	importer  *importer.Service
	billing   *billingService.StripeClient
	whitelist *whitelist.Service
}

// SetBilling injects the Stripe billing client into the handler.
func (h *Handler) SetBilling(b *billingService.StripeClient) {
	h.billing = b
}

// SetWhitelist injects the whitelist service for admin management.
func (h *Handler) SetWhitelist(wl *whitelist.Service) {
	h.whitelist = wl
}

// NewHandler creates a new admin handler
func NewHandler(store *dbstore.Store, redis *redisClient.Client, cfg *config.Config, auth *middleware.AuthMiddleware) *Handler {
	return &Handler{
		store:    store,
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
	Email              string     `json:"email"`
	FirstName          *string    `json:"firstName,omitempty"`
	LastName           *string    `json:"lastName,omitempty"`
	Role               string     `json:"role"`
	SubscriptionTier   *string    `json:"subscriptionTier,omitempty"`
	StripeCustomerID   *string    `json:"stripeCustomerId,omitempty"`
	SuspendedAt        *time.Time `json:"suspendedAt,omitempty"`
	SuspendedBy        *string    `json:"suspendedBy,omitempty"`
	SuspendReason      *string    `json:"suspendReason,omitempty"`
	EarlyAccessStatus  *string    `json:"earlyAccessStatus,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// UpdateUserRequest represents the request to update a user
type UpdateUserRequest struct {
	FirstName        *string `json:"firstName,omitempty"`
	LastName         *string `json:"lastName,omitempty"`
	Email            *string `json:"email,omitempty" validate:"omitempty,email"`
	Role             *string `json:"role,omitempty" validate:"omitempty,oneof=USER ADMIN SUPER_ADMIN"`
	SubscriptionTier *string `json:"subscriptionTier,omitempty"`
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

// SystemAlert represents a system alert (matches system_alerts table schema)
type SystemAlert struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Severity        string     `json:"severity"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	AlertKey        string     `json:"alertKey"`
	Metadata        string     `json:"metadata"`
	FirstSeen       time.Time  `json:"firstSeen"`
	LastSeen        time.Time  `json:"lastSeen"`
	OccurrenceCount int        `json:"occurrenceCount"`
	Dismissed       bool       `json:"dismissed"`
	ActionRequired  bool       `json:"actionRequired"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
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
	TotalMRR            float64 `json:"totalMrr"`
	ActiveSubscriptions int64   `json:"activeSubscriptions"`
	NewSubscriptions    int64   `json:"newSubscriptions"`
	Churned             int64   `json:"churned"`
}

// PlatformCounts represents counts of key entities in the system
type PlatformCounts struct {
	Users               int64 `json:"users"`
	ActiveSubscriptions int64 `json:"activeSubscriptions"`
	Vendors             int64 `json:"vendors"`
	CronJobs            int64 `json:"cronJobs"`
	MarketDataCities    int64 `json:"marketDataCities"`
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

	h.logAdminAudit(ctx, r, "ADMIN_USER", "USER_UPDATE", "user", userID, map[string]interface{}{
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

	h.logAdminAudit(ctx, r, "ADMIN_USER", "USER_IMPERSONATE", "user", userID, map[string]interface{}{
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
	dbStats := map[string]interface{}{}
	if p := h.store.Pool(); p != nil {
		s := p.Stat()
		dbStats["main"] = map[string]interface{}{
			"total_conns":   s.TotalConns(),
			"idle_conns":    s.IdleConns(),
			"acquired_conns": s.AcquiredConns(),
		}
	}
	if p := h.store.MarketPool(); p != nil {
		s := p.Stat()
		dbStats["market"] = map[string]interface{}{
			"total_conns":   s.TotalConns(),
			"idle_conns":    s.IdleConns(),
			"acquired_conns": s.AcquiredConns(),
		}
	}
	stats["database"] = dbStats

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

	h.logAdminAudit(ctx, r, "ADMIN_USER", "CACHE_INVALIDATE", "cache", "", map[string]interface{}{
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

	h.logAdminAudit(ctx, r, "ADMIN_USER", "WHITELIST_TOGGLE", "vendor", vendorID, map[string]interface{}{
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
	dbStats := map[string]interface{}{}
	if p := h.store.Pool(); p != nil {
		s := p.Stat()
		dbStats["main"] = map[string]interface{}{
			"total_conns":   s.TotalConns(),
			"idle_conns":    s.IdleConns(),
			"acquired_conns": s.AcquiredConns(),
		}
	}
	if p := h.store.MarketPool(); p != nil {
		s := p.Stat()
		dbStats["market"] = map[string]interface{}{
			"total_conns":   s.TotalConns(),
			"idle_conns":    s.IdleConns(),
			"acquired_conns": s.AcquiredConns(),
		}
	}
	metrics["database"] = dbStats

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
	dbDetails := make(map[string]string)
	if p := h.store.Pool(); p != nil {
		if err := p.Ping(ctx); err != nil {
			status = "degraded"
			dbDetails["main"] = err.Error()
		} else {
			dbDetails["main"] = "healthy"
		}
	}
	if p := h.store.MarketPool(); p != nil {
		if err := p.Ping(ctx); err != nil {
			status = "degraded"
			dbDetails["market"] = err.Error()
		} else {
			dbDetails["market"] = "healthy"
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

	// Gather platform counts
	counts := map[string]int64{}
	q := h.store.Q()
	if c, err := q.CountUsers(ctx); err == nil {
		counts["users"] = c
	}
	if c, err := q.CountActiveSubscriptions(ctx); err == nil {
		counts["activeSubscriptions"] = c
	}
	if c, err := q.CountVendorConfigs(ctx); err == nil {
		counts["vendors"] = c
	}
	if c, err := q.CountCronJobConfigs(ctx); err == nil {
		counts["cronJobs"] = c
	}
	// Market data cities from market DB
	if mq := h.store.MQ(); mq != nil {
		if c, err := mq.GetCityCount(ctx); err == nil {
			counts["marketDataCities"] = c
		}
	}

	response := map[string]interface{}{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"version":   "2.0.0",
		"details":   details,
		"counts":    counts,
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

// AdminAuditLogEntry represents an admin audit log entry for API responses
type AdminAuditLogEntry struct {
	ID          string                 `json:"id"`
	AdminID     string                 `json:"adminId"`
	AdminEmail  string                 `json:"adminEmail"`
	ActorType   string                 `json:"actorType"`
	Action      string                 `json:"action"`
	Resource    string                 `json:"resource"`
	ResourceID  *string                `json:"resourceId,omitempty"`
	Details     map[string]interface{} `json:"details,omitempty"`
	IPAddress   string                 `json:"ipAddress"`
	UserAgent   string                 `json:"userAgent"`
	CreatedAt   time.Time              `json:"createdAt"`
}

// GetAuditLog returns unified audit log (now all in audit_logs table)
func (h *Handler) GetAuditLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	action := r.URL.Query().Get("action")
	resource := r.URL.Query().Get("resource")
	actorType := r.URL.Query().Get("actorType")
	detailsSearch := r.URL.Query().Get("detailsSearch")

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	var entries []AdminAuditLogEntry
	var total int64
	var err error

	// Use search if detailsSearch parameter is provided
	if detailsSearch != "" {
		entries, total, err = h.searchAdminAuditLogs(ctx, detailsSearch, actorType, action, resource, pageSize, offset)
	} else {
		entries, total, err = h.listAdminAuditLogs(ctx, actorType, action, resource, pageSize, offset)
	}

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

	h.logAdminAudit(ctx, r, "ADMIN_USER", "ALERT_DISMISS", "system_alert", alertID, nil)
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

// buildAuditDetails constructs standardized audit details with before/after tracking.
// ADR-086: Enhanced Admin Audit Logging
func buildAuditDetails(reason string, before, after, additional map[string]interface{}) map[string]interface{} {
	details := make(map[string]interface{})

	if reason != "" {
		details["reason"] = reason
	}
	if before != nil {
		details["before"] = before
	}
	if after != nil {
		details["after"] = after
	}

	// Merge additional context
	for k, v := range additional {
		details[k] = v
	}

	return details
}

// logAdminAudit writes an admin audit log entry for admin operations.
// action must be a valid AdminAction enum value.
// actorType must be a valid AuditActorType enum value ("ADMIN_USER", "SYSTEM", "CRON_JOB", etc.)
// ADR-086: Enhanced with actor type classification
func (h *Handler) logAdminAudit(ctx context.Context, r *http.Request, actorType, action, resource, resourceID string, details map[string]interface{}) {
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

	q := h.store.Q()
	err := q.CreateAdminAuditLog(ctx, queries.CreateAdminAuditLogParams{
		ID:         id,
		AdminID:    pgtype.Text{String: adminID, Valid: true},
		AdminEmail: pgtype.Text{String: adminEmail, Valid: true},
		ActorType:  actorType,
		Action:     pgtype.Text{String: action, Valid: true},
		Resource:   pgtype.Text{String: resource, Valid: true},
		ResourceID: pgtype.Text{String: resourceID, Valid: resourceID != ""},
		Details:    detailsJSON,
		IpAddress:  pgtype.Text{String: clientIP, Valid: true},
		UserAgent:  pgtype.Text{String: r.UserAgent(), Valid: true},
	})
	if err != nil {
		h.logger.Warn("failed to write admin audit log", "error", err, "action", action)
	}
}

// logSystemAudit writes an audit log entry for system processes (cron, background workers).
// Bypasses HTTP request context since these operations don't originate from HTTP requests.
// ADR-086: System process audit logging
func (h *Handler) logSystemAudit(ctx context.Context, actorType, action, resource, resourceID string, details map[string]interface{}) {
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	var detailsJSON json.RawMessage
	if details != nil {
		detailsJSON, _ = json.Marshal(details)
	}

	q := h.store.Q()
	err := q.CreateAdminAuditLog(ctx, queries.CreateAdminAuditLogParams{
		ID:         id,
		AdminID:    pgtype.Text{String: "system", Valid: true},
		AdminEmail: pgtype.Text{String: "system@estara-ai.com", Valid: true},
		ActorType:  actorType,
		Action:     pgtype.Text{String: action, Valid: true},
		Resource:   pgtype.Text{String: resource, Valid: true},
		ResourceID: pgtype.Text{String: resourceID, Valid: resourceID != ""},
		Details:    detailsJSON,
		IpAddress:  pgtype.Text{String: "0.0.0.0", Valid: true}, // No IP for system processes
		UserAgent:  pgtype.Text{String: "System Process", Valid: true},
	})
	if err != nil {
		h.logger.Warn("failed to write system audit log", "error", err, "action", action)
	}
}

// ===============================
// Database Helper Methods
// ===============================

// mapQueryUserToLocal converts a sqlc-generated queries.User to the local admin User type.
func mapQueryUserToLocal(qu queries.User) User {
	u := User{
		ID:    qu.ID,
		Email: qu.Email,
		Role:  qu.Role,
	}
	if qu.FirstName.Valid {
		u.FirstName = &qu.FirstName.String
	}
	if qu.LastName.Valid {
		u.LastName = &qu.LastName.String
	}
	if qu.SubscriptionTier.Valid {
		u.SubscriptionTier = &qu.SubscriptionTier.String
	}
	if qu.StripeCustomerId.Valid {
		u.StripeCustomerID = &qu.StripeCustomerId.String
	}
	if qu.SuspendedAt.Valid {
		u.SuspendedAt = &qu.SuspendedAt.Time
	}
	if qu.SuspendedBy.Valid {
		u.SuspendedBy = &qu.SuspendedBy.String
	}
	if qu.SuspendReason.Valid {
		u.SuspendReason = &qu.SuspendReason.String
	}
	if qu.EarlyAccessStatus.Valid {
		u.EarlyAccessStatus = &qu.EarlyAccessStatus.String
	}
	if qu.CreatedAt.Valid {
		u.CreatedAt = qu.CreatedAt.Time
	}
	if qu.UpdatedAt.Valid {
		u.UpdatedAt = qu.UpdatedAt.Time
	}
	return u
}

// pgtextFromPtr converts a *string to pgtype.Text for sqlc narg parameters.
func pgtextFromPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *s, Valid: true}
}

// pgtextFromString converts a non-empty string to a valid pgtype.Text, or invalid if empty.
func pgtextFromString(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}

func (h *Handler) listAllUsers(ctx context.Context, limit, offset int) ([]User, int64, error) {
	q := h.store.Q()

	total, err := q.CountUsers(ctx)
	if err != nil {
		return nil, 0, err
	}

	rows, err := q.ListUsers(ctx, queries.ListUsersParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, 0, err
	}

	users := make([]User, 0, len(rows))
	for _, qu := range rows {
		users = append(users, mapQueryUserToLocal(qu))
	}

	return users, total, nil
}

func (h *Handler) searchUsersByEmail(ctx context.Context, search string, limit, offset int) ([]User, int64, error) {
	q := h.store.Q()
	searchPattern := "%" + strings.ToLower(search) + "%"

	total, err := q.CountSearchUsersByEmail(ctx, searchPattern)
	if err != nil {
		return nil, 0, err
	}

	rows, err := q.SearchUsersByEmail(ctx, queries.SearchUsersByEmailParams{
		Column1: pgtype.Text{String: search, Valid: true},
		Limit:   int32(limit),
		Offset:  int32(offset),
	})
	if err != nil {
		return nil, 0, err
	}

	users := make([]User, 0, len(rows))
	for _, qu := range rows {
		users = append(users, mapQueryUserToLocal(qu))
	}

	return users, total, nil
}

func (h *Handler) getUserByID(ctx context.Context, id string) (*User, error) {
	qu, err := h.store.Q().GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	u := mapQueryUserToLocal(qu)
	return &u, nil
}

func (h *Handler) updateUserProfile(ctx context.Context, id string, req *UpdateUserRequest) error {
	return h.store.Q().AdminUpdateUserProfile(ctx, queries.AdminUpdateUserProfileParams{
		ID:               id,
		FirstName:        pgtextFromPtr(req.FirstName),
		LastName:         pgtextFromPtr(req.LastName),
		Email:            pgtextFromPtr(req.Email),
		Role:             pgtextFromPtr(req.Role),
		SubscriptionTier: pgtextFromPtr(req.SubscriptionTier),
	})
}

func (h *Handler) getUserStats(ctx context.Context) (*UserStats, error) {
	row, err := h.store.Q().GetAdminUserStats(ctx)
	if err != nil {
		return nil, err
	}
	return &UserStats{
		TotalUsers:      row.TotalUsers,
		AdminCount:      row.AdminCount,
		UserCount:       row.UserCount,
		FreeCount:       row.FreeCount,
		ProCount:        row.ProCount,
		EnterpriseCount: 0,
		ActiveLastWeek:  row.ActiveLastWeek,
	}, nil
}

func (h *Handler) generateImpersonationToken(user *User, expiry time.Duration) (string, error) {
	now := time.Now()

	claims := jwt.MapClaims{
		"userId":      user.ID,
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
	q := h.store.Q()

	cacheRow, err := q.GetCacheStatsAdmin(ctx)
	if err != nil {
		return nil, err
	}

	stats := &CacheStats{
		TotalEntries:   cacheRow.TotalEntries,
		ExpiredEntries: cacheRow.ExpiredEntries,
		UniqueUsers:    cacheRow.UniqueUsers,
		CacheTypes:     cacheRow.CacheTypes,
	}

	// Token/cost stats from ai_usage
	costRow, costErr := q.GetAIUsageTotalCost(ctx)
	if costErr == nil {
		stats.TotalInputTokens = costRow.TotalInputTokens
		stats.TotalOutputTokens = costRow.TotalOutputTokens
		if v, ok := costRow.TotalCost.(float64); ok {
			stats.TotalCost = v
		}
	}

	return stats, nil
}

func (h *Handler) deleteAllCache(ctx context.Context) (int64, error) {
	return h.store.Q().DeleteAllCacheRows(ctx)
}

func (h *Handler) deleteExpiredCache(ctx context.Context) (int64, error) {
	return h.store.Q().DeleteExpiredCache(ctx)
}

func (h *Handler) deleteCacheByType(ctx context.Context, cacheType string) (int64, error) {
	return h.store.Q().DeleteCacheByFeature(ctx, cacheType)
}

func (h *Handler) deleteCacheByUser(ctx context.Context, userID string) (int64, error) {
	return h.store.Q().DeleteCacheByUserIDRows(ctx, userID)
}

func (h *Handler) deleteCacheByKey(ctx context.Context, userID *string, cacheKey string) (int64, error) {
	q := h.store.Q()
	if userID != nil && *userID != "" {
		return q.DeleteCacheByUserAndKeyRows(ctx, queries.DeleteCacheByUserAndKeyRowsParams{
			UserId: *userID,
			Key:    cacheKey,
		})
	}
	return q.DeleteCacheByKeyRows(ctx, cacheKey)
}

// ===============================
// Vendor Helper Methods
// ===============================

func (h *Handler) getVendorConfigs(ctx context.Context) ([]Vendor, error) {
	rows, err := h.store.Q().ListVendorConfigsAdmin(ctx)
	if err != nil {
		return nil, err
	}

	vendors := make([]Vendor, 0, len(rows))
	for _, r := range rows {
		v := Vendor{
			ID:       r.ID,
			Name:     r.Name,
			Category: r.Category,
			Enabled:  r.IsActive,
			Status:   r.HealthStatus,
		}
		if r.HealthCheckUrl.Valid {
			v.HealthURL = r.HealthCheckUrl.String
		}
		if r.LastHealthCheck.Valid {
			v.LastChecked = &r.LastHealthCheck.Time
		}
		if v.Status == "" {
			v.Status = "unknown"
		}
		vendors = append(vendors, v)
	}

	return vendors, nil
}

func (h *Handler) getVendorByID(ctx context.Context, id string) (*Vendor, error) {
	r, err := h.store.Q().GetVendorConfigAdmin(ctx, id)
	if err != nil {
		return nil, err
	}
	v := &Vendor{
		ID:       r.ID,
		Name:     r.Name,
		Category: r.Category,
		Enabled:  r.IsActive,
		Status:   r.HealthStatus,
	}
	if r.HealthCheckUrl.Valid {
		v.HealthURL = r.HealthCheckUrl.String
	}
	if r.LastHealthCheck.Valid {
		v.LastChecked = &r.LastHealthCheck.Time
	}
	if v.Status == "" {
		v.Status = "unknown"
	}
	return v, nil
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
	q := h.store.Q()
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
	err := h.store.Q().UpdateVendorHealthCheck(ctx, queries.UpdateVendorHealthCheckParams{
		ID:      id,
		Column2: status,
	})
	if err != nil {
		h.logger.Warn("failed to update vendor status", "error", err, "vendor_id", id)
	}
}

func (h *Handler) updateVendorEnabled(ctx context.Context, id string, enabled bool) error {
	rows, err := h.store.Q().ToggleVendorActiveRows(ctx, queries.ToggleVendorActiveRowsParams{
		ID:       id,
		IsActive: enabled,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ===============================
// AI Metrics Helper Methods
// ===============================

func (h *Handler) getAIMetrics(ctx context.Context) (*AIMetrics, error) {
	q := h.store.Q()
	var metrics AIMetrics

	// Get overall metrics from ai_usage table
	allTime, err := q.GetAIUsageStatsAllTime(ctx)
	if err != nil {
		return nil, err
	}
	metrics.TotalRequests = allTime.TotalRequests
	metrics.TotalInputTokens = allTime.TotalInputTokens
	metrics.TotalOutputTokens = allTime.TotalOutputTokens
	if v, ok := allTime.TotalCost.(float64); ok {
		metrics.TotalCost = v
	}

	// Get today's metrics
	today, err := q.GetAIUsageStatsToday(ctx)
	if err != nil {
		h.logger.Warn("failed to get today's metrics", "error", err)
	} else {
		metrics.RequestsToday = today.RequestsToday
		if v, ok := today.CostToday.(float64); ok {
			metrics.CostToday = v
		}
	}

	// Get this month's metrics
	monthly, err := q.GetAIUsageStatsThisMonth(ctx)
	if err != nil {
		h.logger.Warn("failed to get monthly metrics", "error", err)
	} else {
		metrics.RequestsThisMonth = monthly.RequestsThisMonth
		if v, ok := monthly.CostThisMonth.(float64); ok {
			metrics.CostThisMonth = v
		}
	}

	// Calculate cache hit rate
	hitRate, err := q.GetAIUsageCacheHitRate(ctx)
	if err == nil && hitRate.TotalCount > 0 {
		metrics.CacheHitRate = float64(hitRate.CachedCount) / float64(hitRate.TotalCount) * 100
	}

	return &metrics, nil
}

func (h *Handler) getAIUsageRecords(ctx context.Context, userID, cacheType string, limit, offset int) ([]AIUsageRecord, int64, error) {
	q := h.store.Q()

	countParams := queries.CountAIUsageRecordsParams{
		UserID:  pgtextFromString(userID),
		Feature: pgtextFromString(cacheType),
	}
	total, err := q.CountAIUsageRecords(ctx, countParams)
	if err != nil {
		return nil, 0, err
	}

	rows, err := q.ListAIUsageRecords(ctx, queries.ListAIUsageRecordsParams{
		UserID:  pgtextFromString(userID),
		Feature: pgtextFromString(cacheType),
		Offset:  int32(offset),
		Limit:   int32(limit),
	})
	if err != nil {
		return nil, 0, err
	}

	records := make([]AIUsageRecord, 0, len(rows))
	for _, r := range rows {
		rec := AIUsageRecord{
			ID:           r.ID,
			UserID:       r.UserId,
			CacheType:    r.Feature,
			Model:        r.Model,
			InputTokens:  int(r.InputTokens),
			OutputTokens: int(r.OutputTokens),
		}
		if r.Email.Valid {
			rec.Email = &r.Email.String
		}
		if r.TotalCost.Valid {
			f, _ := r.TotalCost.Float64Value()
			rec.Cost = f.Float64
		}
		if r.CreatedAt.Valid {
			rec.CreatedAt = r.CreatedAt.Time
		}
		records = append(records, rec)
	}

	return records, total, nil
}

func (h *Handler) getCacheTypeBreakdown(ctx context.Context) ([]map[string]interface{}, error) {
	rows, err := h.store.Q().GetAIUsageByFeature(ctx)
	if err != nil {
		return nil, err
	}

	breakdown := make([]map[string]interface{}, 0, len(rows))
	for _, r := range rows {
		breakdown = append(breakdown, map[string]interface{}{
			"cacheType":    r.Feature,
			"count":        r.Count,
			"inputTokens":  r.InputTokens,
			"outputTokens": r.OutputTokens,
			"cost":         r.Cost,
		})
	}

	return breakdown, nil
}

// ===============================
// Investor Reports Helper Methods
// ===============================

func (h *Handler) getInvestorReports(ctx context.Context, status, userID string, limit, offset int) ([]InvestorReportAdmin, int64, error) {
	q := h.store.Q()

	total, err := q.CountInvestorReportsFiltered(ctx, queries.CountInvestorReportsFilteredParams{
		StatusFilter: pgtextFromString(status),
		UserID:       pgtextFromString(userID),
	})
	if err != nil {
		return nil, 0, err
	}

	rows, err := q.ListInvestorReportsFiltered(ctx, queries.ListInvestorReportsFilteredParams{
		StatusFilter: pgtextFromString(status),
		UserID:       pgtextFromString(userID),
		Offset:       int32(offset),
		Limit:        int32(limit),
	})
	if err != nil {
		return nil, 0, err
	}

	reports := make([]InvestorReportAdmin, 0, len(rows))
	for _, r := range rows {
		rpt := InvestorReportAdmin{
			ID:         r.ID,
			ReportType: r.ReportType,
			Status:     r.RStatus,
		}
		if r.UserId.Valid {
			rpt.UserID = r.UserId.String
		}
		if r.Email.Valid {
			rpt.Email = &r.Email.String
		}
		if r.PropertyAddress.Valid {
			rpt.Address = &r.PropertyAddress.String
		}
		if r.CreatedAt.Valid {
			rpt.CreatedAt = r.CreatedAt.Time
		}
		if r.CompletedAt.Valid {
			rpt.CompletedAt = &r.CompletedAt.Time
		}
		reports = append(reports, rpt)
	}

	return reports, total, nil
}

func (h *Handler) updateReportStatus(ctx context.Context, reportID, status string, _ *string) error {
	q := h.store.Q()

	if status == "PENDING" {
		rows, err := q.ResetInvestorReportForRetry(ctx, reportID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}

	return q.UpdateInvestorReportStatusAdmin(ctx, queries.UpdateInvestorReportStatusAdminParams{
		ID:      reportID,
		Column2: status,
	})
}

// ===============================
// Audit Log Helper Methods
// ===============================

func (h *Handler) getAuditLogEntries(ctx context.Context, userID, action, resource string, limit, offset int) ([]AuditLogEntry, int64, error) {
	q := h.store.Q()

	filterParams := queries.CountAuditLogsFilteredParams{
		UserID:         pgtextFromString(userID),
		ActionFilter:   pgtextFromString(action),
		ResourceFilter: pgtextFromString(resource),
	}

	total, err := q.CountAuditLogsFiltered(ctx, filterParams)
	if err != nil {
		h.logger.Warn("failed to count audit logs", "error", err)
		return []AuditLogEntry{}, 0, nil
	}

	rows, err := q.ListAuditLogsFiltered(ctx, queries.ListAuditLogsFilteredParams{
		UserID:         pgtextFromString(userID),
		ActionFilter:   pgtextFromString(action),
		ResourceFilter: pgtextFromString(resource),
		Offset:         int32(offset),
		Limit:          int32(limit),
	})
	if err != nil {
		h.logger.Warn("failed to query audit logs", "error", err)
		return []AuditLogEntry{}, 0, nil
	}

	entries := make([]AuditLogEntry, 0, len(rows))
	for _, r := range rows {
		e := AuditLogEntry{
			ID:       r.ID,
			Action:   r.Action,
			Resource: r.Resource,
		}
		if r.UserId.Valid {
			e.UserID = &r.UserId.String
		}
		if e.Action == "" {
			e.Action = r.Event // Use event type as action if action not set
		}
		if r.IpAddress.Valid {
			e.IPAddress = &r.IpAddress.String
		}
		if r.UserAgent.Valid {
			e.UserAgent = &r.UserAgent.String
		}
		if r.CreatedAt.Valid {
			e.CreatedAt = r.CreatedAt.Time
		}
		if r.Metadata != nil {
			_ = json.Unmarshal(r.Metadata, &e.Details)
		}
		entries = append(entries, e)
	}

	return entries, total, nil
}

// ===============================
// Analytics Helper Methods
// ===============================

func (h *Handler) getAnalytics(ctx context.Context) (*Analytics, error) {
	q := h.store.Q()
	analytics := &Analytics{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// User analytics
	userRow, err := q.GetUserAnalytics(ctx)
	if err != nil {
		h.logger.Warn("failed to get user analytics", "error", err)
	} else {
		analytics.Users.Total = userRow.Total
		analytics.Users.ActiveThisWeek = userRow.ActiveThisWeek
		analytics.Users.ActiveThisMonth = userRow.ActiveThisMonth
		analytics.Users.NewThisWeek = userRow.NewThisWeek
		analytics.Users.NewThisMonth = userRow.NewThisMonth
	}

	// Report analytics
	reportRow, err := q.GetReportAnalytics(ctx)
	if err != nil {
		h.logger.Warn("failed to get report analytics", "error", err)
	} else {
		analytics.Reports.TotalGenerated = reportRow.TotalGenerated
		analytics.Reports.ThisWeek = reportRow.ThisWeek
		analytics.Reports.ThisMonth = reportRow.ThisMonth
		analytics.Reports.FailedThisWeek = reportRow.FailedThisWeek
	}

	// API analytics
	apiRow, err := q.GetAIRequestAnalytics(ctx)
	if err != nil {
		h.logger.Warn("failed to get API analytics", "error", err)
	} else {
		analytics.API.TotalRequests = apiRow.TotalRequests
		analytics.API.RequestsToday = apiRow.RequestsToday
	}

	// Revenue analytics
	revRow, err := q.GetSubscriptionRevenueSummary(ctx)
	if err != nil {
		h.logger.Warn("failed to get revenue analytics", "error", err)
	} else {
		if v, ok := revRow.Mrr.(float64); ok {
			analytics.Revenue.TotalMRR = v
		}
		analytics.Revenue.ActiveSubscriptions = revRow.ActiveSubs
		analytics.Revenue.NewSubscriptions = revRow.NewSubs
		analytics.Revenue.Churned = revRow.Churned
	}

	return analytics, nil
}

// ===============================
// System Alerts Helper Methods
// ===============================

func (h *Handler) getSystemAlerts(ctx context.Context, showResolved bool, limit, offset int) ([]SystemAlert, int64, error) {
	q := h.store.Q()

	// For showResolved=true, pass NULL (no filter); for false, filter dismissed=false
	dismissedFilter := pgtype.Bool{Valid: false} // NULL = no filter (show all)
	if !showResolved {
		dismissedFilter = pgtype.Bool{Bool: false, Valid: true} // dismissed = false
	}

	total, err := q.CountSystemAlertsFiltered(ctx, dismissedFilter)
	if err != nil {
		h.logger.Warn("failed to count system alerts", "error", err)
		return []SystemAlert{}, 0, nil
	}

	rows, err := q.ListSystemAlertsFiltered(ctx, queries.ListSystemAlertsFilteredParams{
		DismissedFilter: dismissedFilter,
		Offset:          int32(offset),
		Limit:           int32(limit),
	})
	if err != nil {
		h.logger.Warn("failed to query system alerts", "error", err)
		return []SystemAlert{}, 0, nil
	}

	alerts := make([]SystemAlert, 0, len(rows))
	for _, r := range rows {
		a := SystemAlert{
			ID:              r.ID,
			Type:            r.Type,
			Severity:        r.Severity,
			Title:           r.Title,
			Description:     r.Description,
			AlertKey:        r.AlertKey,
			Metadata:        r.Metadata,
			OccurrenceCount: int(r.OccurrenceCount),
			Dismissed:       r.Dismissed,
			ActionRequired:  r.ActionRequired,
		}
		if r.FirstSeen.Valid {
			a.FirstSeen = r.FirstSeen.Time
		}
		if r.LastSeen.Valid {
			a.LastSeen = r.LastSeen.Time
		}
		if r.ExpiresAt.Valid {
			a.ExpiresAt = &r.ExpiresAt.Time
		}
		if r.CreatedAt.Valid {
			a.CreatedAt = r.CreatedAt.Time
		}
		alerts = append(alerts, a)
	}

	return alerts, total, nil
}

func (h *Handler) resolveSystemAlert(ctx context.Context, alertID, _ string) error {
	rows, err := h.store.Q().DismissSystemAlertRows(ctx, alertID)
	if err != nil {
		return err
	}
	if rows == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ===============================
// Vendor Costs Helper Methods
// ===============================

func (h *Handler) getVendorCosts(ctx context.Context, period string) ([]VendorCostRecord, error) {
	now := time.Now()

	rows, err := h.store.Q().GetVendorCostSummary(ctx, queries.GetVendorCostSummaryParams{
		Month: int32(now.Month()),
		Year:  int32(now.Year()),
	})
	if err != nil {
		// Table might not exist - return basic vendor info
		h.logger.Warn("failed to query vendor costs, falling back to basic info", "error", err)
		return h.getBasicVendorCosts(ctx, period)
	}

	costs := make([]VendorCostRecord, 0, len(rows))
	for _, r := range rows {
		c := VendorCostRecord{
			VendorID:     r.VendorID,
			VendorName:   r.VendorName,
			Category:     r.VCategory,
			RequestCount: r.RequestCount,
			Period:       period,
			RecordedAt:   now,
		}
		if v, ok := r.TotalCost.(float64); ok {
			c.TotalCost = v
		}
		if t, ok := r.RecordedAt.(time.Time); ok {
			c.RecordedAt = t
		}
		costs = append(costs, c)
	}

	return costs, nil
}

func (h *Handler) getBasicVendorCosts(ctx context.Context, period string) ([]VendorCostRecord, error) {
	rows, err := h.store.Q().ListVendorConfigsAdmin(ctx)
	if err != nil {
		return []VendorCostRecord{}, nil
	}

	costs := make([]VendorCostRecord, 0, len(rows))
	for _, r := range rows {
		if !r.IsActive {
			continue
		}
		costs = append(costs, VendorCostRecord{
			VendorID:   r.ID,
			VendorName: r.Name,
			Category:   r.Category,
			Period:     period,
			RecordedAt: time.Now(),
		})
	}

	return costs, nil
}

// listAdminAuditLogs retrieves admin audit logs with optional filters
func (h *Handler) listAdminAuditLogs(ctx context.Context, actorType, action, resource string, limit, offset int) ([]AdminAuditLogEntry, int64, error) {
	q := h.store.Q()

	// Count total
	countParams := queries.CountAdminAuditLogsParams{
		ActorType:      pgtextFromString(actorType),
		ActionFilter:   pgtextFromString(action),
		ResourceFilter: pgtextFromString(resource),
		AdminID:        pgtype.Text{Valid: false}, // Not filtering by admin ID
	}
	total, err := q.CountAdminAuditLogs(ctx, countParams)
	if err != nil {
		return nil, 0, err
	}

	// List entries
	listParams := queries.ListAdminAuditLogsParams{
		ActorType:      pgtextFromString(actorType),
		ActionFilter:   pgtextFromString(action),
		ResourceFilter: pgtextFromString(resource),
		AdminID:        pgtype.Text{Valid: false},
		Limit:          int32(limit),
		Offset:         int32(offset),
	}
	rows, err := q.ListAdminAuditLogs(ctx, listParams)
	if err != nil {
		return nil, 0, err
	}

	return h.convertAdminAuditListRows(rows), total, nil
}

// searchAdminAuditLogs searches admin audit logs by details text
func (h *Handler) searchAdminAuditLogs(ctx context.Context, searchText, actorType, action, resource string, limit, offset int) ([]AdminAuditLogEntry, int64, error) {
	q := h.store.Q()

	// Count total
	countParams := queries.CountAdminAuditLogsByDetailsTextParams{
		SearchText: searchText,
		ActorType:  pgtextFromString(actorType),
		Action:     pgtextFromString(action),
		Resource:   pgtextFromString(resource),
	}
	total, err := q.CountAdminAuditLogsByDetailsText(ctx, countParams)
	if err != nil {
		return nil, 0, err
	}

	// Search entries
	searchParams := queries.SearchAdminAuditLogsByDetailsTextParams{
		SearchText: searchText,
		ActorType:  pgtextFromString(actorType),
		Action:     pgtextFromString(action),
		Resource:   pgtextFromString(resource),
		Limit:      int32(limit),
		Offset:     int32(offset),
	}
	rows, err := q.SearchAdminAuditLogsByDetailsText(ctx, searchParams)
	if err != nil {
		return nil, 0, err
	}

	return h.convertAdminAuditSearchRows(rows), total, nil
}

// convertAdminAuditListRows converts ListAdminAuditLogsRow to AdminAuditLogEntry
func (h *Handler) convertAdminAuditListRows(rows []queries.ListAdminAuditLogsRow) []AdminAuditLogEntry {
	entries := make([]AdminAuditLogEntry, 0, len(rows))
	for _, r := range rows {
		entry := AdminAuditLogEntry{
			ID:         r.ID,
			AdminID:    r.AdminId.String,      // pgtype.Text
			AdminEmail: r.AdminEmail,           // string
			ActorType:  r.ActorType,            // string
			Resource:   r.Resource.String,      // pgtype.Text
			IPAddress:  r.IpAddress.String,     // pgtype.Text
			UserAgent:  r.UserAgent.String,     // pgtype.Text
			CreatedAt:  r.CreatedAt.Time,
		}
		// Handle Action which is interface{}
		if actionStr, ok := r.Action.(string); ok {
			entry.Action = actionStr
		}
		// Handle ResourceID
		if r.ResourceId.Valid {
			entry.ResourceID = &r.ResourceId.String
		}
		// Unmarshal Details
		if r.Details != nil {
			_ = json.Unmarshal(r.Details, &entry.Details)
		}
		entries = append(entries, entry)
	}
	return entries
}

// convertAdminAuditSearchRows converts SearchAdminAuditLogsByDetailsTextRow to AdminAuditLogEntry
func (h *Handler) convertAdminAuditSearchRows(rows []queries.SearchAdminAuditLogsByDetailsTextRow) []AdminAuditLogEntry {
	entries := make([]AdminAuditLogEntry, 0, len(rows))
	for _, r := range rows {
		entry := AdminAuditLogEntry{
			ID:         r.ID,
			AdminID:    r.AdminId.String,
			AdminEmail: r.AdminEmail.String,
			ActorType:  r.ActorType,
			Action:     r.Action,              // string
			Resource:   r.Resource.String,
			IPAddress:  r.IpAddress.String,
			UserAgent:  r.UserAgent.String,
			CreatedAt:  r.CreatedAt.Time,
		}
		if r.ResourceId.Valid {
			entry.ResourceID = &r.ResourceId.String
		}
		if r.Details != nil {
			_ = json.Unmarshal(r.Details, &entry.Details)
		}
		entries = append(entries, entry)
	}
	return entries
}
