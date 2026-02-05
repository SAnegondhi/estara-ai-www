package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles auth-related HTTP requests
type Handler struct {
	auth     *middleware.AuthMiddleware
	db       *postgres.DB
	redis    *redisClient.Client
	cfg      *config.Config
	validate *validator.Validate
	logger   *slog.Logger
}

// NewHandler creates a new auth handler
func NewHandler(auth *middleware.AuthMiddleware, db *postgres.DB, redis *redisClient.Client, cfg *config.Config) *Handler {
	return &Handler{
		auth:     auth,
		db:       db,
		redis:    redis,
		cfg:      cfg,
		validate: validator.New(),
		logger:   slog.Default().With("component", "auth_handler"),
	}
}

// LoginRequest represents a login request
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	Success      bool          `json:"success"`
	Token        string        `json:"token"`                   // Kept for backward compatibility during migration
	RefreshToken string        `json:"refreshToken,omitempty"`  // Kept for backward compatibility
	User         UserResponse  `json:"user"`
	Entitlements *Entitlements `json:"entitlements"`
	CSRFToken    string        `json:"csrfToken,omitempty"`     // ADR-066: CSRF token for cookie-based auth
}

// UserResponse represents user data in responses
// Note: firstName, lastName, and investorProfile use pointers and no omitempty
// to match www_v1 behavior of returning null instead of omitting the field
type UserResponse struct {
	ID               string                 `json:"id"`
	Email            string                 `json:"email"`
	FirstName        *string                `json:"firstName"`
	LastName         *string                `json:"lastName"`
	SubscriptionTier string                 `json:"subscriptionTier"`
	Theme            string                 `json:"theme"`
	InvestorProfile  map[string]interface{} `json:"investorProfile"`
}

// Entitlements represents user entitlements
// Matches www_v1 response format for API contract compatibility
type Entitlements struct {
	HasActiveSubscription        bool            `json:"hasActiveSubscription"`
	SubscriptionTier             string          `json:"subscriptionTier"`
	HasDataTier                  string          `json:"hasDataTier"`
	AnalysisSessionsRemaining    int             `json:"analysisSessionsRemaining"`    // -1 for unlimited
	MaxAnalysisSessionsPerMonth  int             `json:"maxAnalysisSessionsPerMonth"`  // -1 for unlimited
	Features                     map[string]bool `json:"features"`
	V2Quota                      *V2QuotaInfo    `json:"v2Quota,omitempty"`
}

// V2QuotaInfo represents V2 evaluation quota information
// Matches www_v1 response format for API contract compatibility
type V2QuotaInfo struct {
	Tier             string          `json:"tier"`
	DisplayName      string          `json:"displayName"`
	Used             int             `json:"used"`
	Limit            int             `json:"limit"`
	Remaining        interface{}     `json:"remaining"` // Can be "unlimited" string or integer
	IsUnlimited      bool            `json:"isUnlimited"`
	PeriodStart      string          `json:"periodStart"` // ISO 8601 string to match www_v1
	PeriodEnd        string          `json:"periodEnd"`   // ISO 8601 string to match www_v1
	WarningThreshold bool            `json:"warningThreshold"`
	TierFeatures     map[string]bool `json:"tierFeatures,omitempty"`
	PricePerYear     int             `json:"pricePerYear,omitempty"`
}

// RefreshRequest represents a token refresh request
type RefreshRequest struct {
	RefreshToken string `json:"refreshToken" validate:"required"`
}

// RefreshResponse represents a token refresh response
type RefreshResponse struct {
	Success      bool          `json:"success"`
	Token        string        `json:"token"`           // Kept for backward compatibility
	User         *UserResponse `json:"user,omitempty"`  // ADR-066: Include user data
	Entitlements *Entitlements `json:"entitlements"`
	CSRFToken    string        `json:"csrfToken,omitempty"` // ADR-066: CSRF token
}

// Rate limiting constants
const (
	maxLoginAttempts    = 5
	loginLockoutMinutes = 15
	rateLimitKeyPrefix  = "login_attempts:"
)

// Health returns server health status
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	status := "healthy"
	details := make(map[string]interface{})

	// Check database
	dbHealth := h.db.Health(ctx)
	details["database"] = make(map[string]string)
	for name, err := range dbHealth {
		if err != nil {
			status = "degraded"
			details["database"].(map[string]string)[name] = err.Error()
		} else {
			details["database"].(map[string]string)[name] = "healthy"
		}
	}

	// Check Redis
	if h.redis != nil {
		if err := h.redis.Ping(ctx).Err(); err != nil {
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

// Login authenticates a user and returns tokens
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req LoginRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))

	// Check rate limiting
	allowed, retryAfter := h.checkRateLimit(ctx, email)
	if !allowed {
		h.logger.Warn("rate limited login attempt", "email", email)
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		httputil.JSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"error":       "Account temporarily locked due to too many failed attempts",
			"code":        "ACCOUNT_LOCKED",
			"retryAfter":  retryAfter,
		})
		return
	}

	// Look up user by email
	h.logger.Info("looking up user", "email", email)
	user, err := h.getUserByEmail(ctx, email)
	if err != nil {
		h.recordFailedLogin(ctx, email)
		h.logger.Warn("user not found", "email", email, "error", err.Error())
		httputil.Unauthorized(w, "Invalid credentials")
		return
	}
	h.logger.Info("user found", "id", user.ID, "email", user.Email, "hasPassword", user.Password != nil)

	// Verify password
	if user.Password == nil || *user.Password == "" {
		h.recordFailedLogin(ctx, email)
		h.logger.Warn("user has no password", "email", email)
		httputil.Unauthorized(w, "Invalid credentials")
		return
	}

	h.logger.Info("comparing password", "hashLen", len(*user.Password), "inputLen", len(req.Password))
	if err := bcrypt.CompareHashAndPassword([]byte(*user.Password), []byte(req.Password)); err != nil {
		h.recordFailedLogin(ctx, email)
		h.logger.Warn("invalid password", "email", email, "bcryptError", err.Error())
		httputil.Unauthorized(w, "Invalid credentials")
		return
	}
	h.logger.Info("password verified successfully")

	// Clear failed login attempts on success
	h.clearLoginAttempts(ctx, email)

	// Check V2 subscription quota
	v2Quota, err := h.getV2Quota(ctx, user.ID)
	if err != nil || v2Quota == nil || v2Quota.PeriodEndDate.Before(time.Now()) {
		h.logger.Warn("no valid V2 subscription", "user_id", user.ID)
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":      "no_insight_access",
			"code":       "NO_V2_SUBSCRIPTION",
			"message":    "You do not have an active Estara Insight subscription.",
			"canPurchase": true,
			"websiteUrl": h.cfg.Server.MarketingURL,
		})
		return
	}

	// Calculate entitlements
	entitlements := h.buildEntitlements(user, v2Quota)

	// Generate tokens
	accessToken, err := h.auth.GenerateToken(
		user.ID,
		user.ClerkUserID,
		email,
		user.Role,
		24*time.Hour,
	)
	if err != nil {
		h.logger.Error("failed to generate access token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	refreshToken, err := h.auth.GenerateRefreshToken(user.ID, 7*24*time.Hour)
	if err != nil {
		h.logger.Error("failed to generate refresh token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	// Update last login
	_ = h.updateLastLogin(ctx, user.ID)

	// ADR-066: Set httpOnly cookies for authentication
	secure := !h.cfg.IsDevelopment()
	middleware.SetAuthCookies(w, accessToken, refreshToken, secure)

	// ADR-066: Set CSRF cookie and get token for response
	csrfToken := middleware.SetCSRFCookie(w, secure, h.cfg.Security.CSRFTokenLength)

	// Build response
	tier := strings.TrimPrefix(v2Quota.Tier, "V2_")
	response := LoginResponse{
		Success:      true,
		Token:        accessToken,    // Kept for backward compatibility during migration
		RefreshToken: refreshToken,   // Kept for backward compatibility during migration
		User: UserResponse{
			ID:              user.ID,
			Email:           email,
			FirstName:       user.FirstName,
			LastName:        user.LastName,
			SubscriptionTier: strings.ToLower(tier),
			Theme:           getStringOrDefault(user.Theme, "estara"),
		},
		Entitlements: entitlements,
		CSRFToken:    csrfToken,      // ADR-066: CSRF token for cookie-based auth
	}

	h.logger.Info("login successful", "user_id", user.ID, "email", email)
	// Return response directly without wrapping in data object to match www_v1 format
	httputil.JSON(w, http.StatusOK, response)
}

// RefreshToken refreshes an access token using a refresh token
func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// ADR-066: Try cookie first, fall back to Authorization header
	token := middleware.GetAccessToken(r)
	if token == "" {
		// Fall back to Authorization header for backward compatibility
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			httputil.Unauthorized(w, "Authorization required")
			return
		}
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}

	// Decode token to get user ID (even if expired)
	claims, err := h.auth.DecodeTokenWithoutValidation(token)
	if err != nil || claims == nil || claims.UserID == "" {
		httputil.Unauthorized(w, "Invalid token")
		return
	}

	// Look up user
	user, err := h.getUserByID(ctx, claims.UserID)
	if err != nil {
		h.logger.Warn("user not found for refresh", "user_id", claims.UserID)
		httputil.NotFound(w, "User not found")
		return
	}

	// Check V2 subscription quota
	v2Quota, err := h.getV2Quota(ctx, user.ID)
	if err != nil || v2Quota == nil || v2Quota.PeriodEndDate.Before(time.Now()) {
		h.logger.Warn("no valid V2 subscription for refresh", "user_id", user.ID)
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":      "no_insight_access",
			"code":       "NO_V2_SUBSCRIPTION",
			"message":    "Your Estara Insight subscription has expired.",
			"canPurchase": true,
			"websiteUrl": h.cfg.Server.MarketingURL,
		})
		return
	}

	// Calculate entitlements
	entitlements := h.buildEntitlements(user, v2Quota)

	// Generate new access token
	newToken, err := h.auth.GenerateToken(
		user.ID,
		user.ClerkUserID,
		user.Email,
		user.Role,
		24*time.Hour,
	)
	if err != nil {
		h.logger.Error("failed to generate access token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	// ADR-066: Set httpOnly cookie with new access token
	secure := !h.cfg.IsDevelopment()
	middleware.SetAccessCookie(w, newToken, secure)

	// ADR-066: Refresh CSRF token
	csrfToken := middleware.SetCSRFCookie(w, secure, h.cfg.Security.CSRFTokenLength)

	// Build user response for ADR-066
	tier := strings.TrimPrefix(v2Quota.Tier, "V2_")
	userResponse := &UserResponse{
		ID:               user.ID,
		Email:            user.Email,
		FirstName:        user.FirstName,
		LastName:         user.LastName,
		SubscriptionTier: strings.ToLower(tier),
		Theme:            getStringOrDefault(user.Theme, "estara"),
	}

	response := RefreshResponse{
		Success:      true,
		Token:        newToken,      // Kept for backward compatibility
		User:         userResponse,  // ADR-066: Include user data
		Entitlements: entitlements,
		CSRFToken:    csrfToken,     // ADR-066: CSRF token
	}

	h.logger.Info("token refreshed", "user_id", user.ID)
	// Return response directly without wrapping in data object to match www_v1 format
	httputil.JSON(w, http.StatusOK, response)
}

// GetCurrentUser returns the current authenticated user
func (h *Handler) GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	// Look up full user details from database
	user, err := h.getUserByID(ctx, claims.UserID)
	if err != nil {
		h.logger.Warn("user not found", "user_id", claims.UserID)
		httputil.NotFound(w, "User not found")
		return
	}

	// Get V2 quota for entitlements
	v2Quota, _ := h.getV2Quota(ctx, user.ID)

	tier := "free"
	if v2Quota != nil {
		tier = strings.ToLower(strings.TrimPrefix(v2Quota.Tier, "V2_"))
	}

	response := UserResponse{
		ID:              user.ID,
		Email:           user.Email,
		FirstName:       user.FirstName,
		LastName:        user.LastName,
		SubscriptionTier: tier,
		Theme:           getStringOrDefault(user.Theme, "estara"),
	}

	httputil.Success(w, response)
}

// Me returns the current authenticated user with entitlements and refreshes CSRF token
// ADR-066: Used for checking auth status on page load with cookie-based authentication
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	// Look up full user details from database
	user, err := h.getUserByID(ctx, claims.UserID)
	if err != nil {
		h.logger.Warn("user not found", "user_id", claims.UserID)
		httputil.NotFound(w, "User not found")
		return
	}

	// Get V2 quota for entitlements
	v2Quota, _ := h.getV2Quota(ctx, user.ID)
	var entitlements *Entitlements
	tier := "free"
	if v2Quota != nil {
		tier = strings.ToLower(strings.TrimPrefix(v2Quota.Tier, "V2_"))
		entitlements = h.buildEntitlements(user, v2Quota)
	}

	// ADR-066: Refresh CSRF token
	secure := !h.cfg.IsDevelopment()
	csrfToken := middleware.SetCSRFCookie(w, secure, h.cfg.Security.CSRFTokenLength)

	response := struct {
		User         UserResponse  `json:"user"`
		Entitlements *Entitlements `json:"entitlements,omitempty"`
		CSRFToken    string        `json:"csrfToken"`
	}{
		User: UserResponse{
			ID:               user.ID,
			Email:            user.Email,
			FirstName:        user.FirstName,
			LastName:         user.LastName,
			SubscriptionTier: tier,
			Theme:            getStringOrDefault(user.Theme, "estara"),
		},
		Entitlements: entitlements,
		CSRFToken:    csrfToken,
	}

	httputil.JSON(w, http.StatusOK, response)
}

// Logout invalidates the user's session
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	// ADR-066: Clear all authentication cookies
	middleware.ClearAuthCookies(w)

	// For stateless JWT, logout is handled by clearing cookies
	// If we need to blacklist tokens, we would add them to Redis here
	httputil.Success(w, map[string]string{"message": "logged out successfully"})
}

// ClientLogin is an alias for Login to match the www_v1 API contract at /api/auth/client-login
func (h *Handler) ClientLogin(w http.ResponseWriter, r *http.Request) {
	h.Login(w, r)
}

// ClientRefresh is an alias for RefreshToken to match the www_v1 API contract at /api/auth/client-refresh
func (h *Handler) ClientRefresh(w http.ResponseWriter, r *http.Request) {
	h.RefreshToken(w, r)
}

// User represents a database user record
type User struct {
	ID              string
	Email           string
	ClerkUserID     string
	FirstName       *string
	LastName        *string
	Password        *string
	Role            string
	Theme           *string
	HasDataTier     *string
	InvestorProfile map[string]interface{}
}

// V2Quota represents a V2 evaluation quota record
type V2Quota struct {
	ID              string
	UserID          string
	Tier            string
	AnnualLimit     int
	UsedThisPeriod  int
	PeriodStartDate time.Time
	PeriodEndDate   time.Time
}

// getUserByEmail looks up a user by email
func (h *Handler) getUserByEmail(ctx context.Context, email string) (*User, error) {
	query := `
		SELECT id, email, "firstName", "lastName", password, role, theme, "hasDataTier"
		FROM users
		WHERE email = $1
	`

	var user User
	err := h.db.Main.QueryRow(ctx, query, email).Scan(
		&user.ID,
		&user.Email,
		&user.FirstName,
		&user.LastName,
		&user.Password,
		&user.Role,
		&user.Theme,
		&user.HasDataTier,
	)
	if err != nil {
		return nil, err
	}
	// ClerkUserID is no longer used - set to empty string for backwards compatibility
	user.ClerkUserID = ""

	return &user, nil
}

// getUserByID looks up a user by ID
func (h *Handler) getUserByID(ctx context.Context, userID string) (*User, error) {
	query := `
		SELECT id, email, "firstName", "lastName", password, role, theme, "hasDataTier"
		FROM users
		WHERE id = $1
	`

	var user User
	err := h.db.Main.QueryRow(ctx, query, userID).Scan(
		&user.ID,
		&user.Email,
		&user.FirstName,
		&user.LastName,
		&user.Password,
		&user.Role,
		&user.Theme,
		&user.HasDataTier,
	)
	if err != nil {
		return nil, err
	}
	// ClerkUserID is no longer used - set to empty string for backwards compatibility
	user.ClerkUserID = ""

	return &user, nil
}

// getV2Quota retrieves the V2 evaluation quota for a user
func (h *Handler) getV2Quota(ctx context.Context, userID string) (*V2Quota, error) {
	query := `
		SELECT id, user_id, tier, annual_limit, used_this_period,
		       period_start_date, period_end_date
		FROM v2_evaluation_quotas
		WHERE user_id = $1
	`

	var quota V2Quota
	err := h.db.Main.QueryRow(ctx, query, userID).Scan(
		&quota.ID,
		&quota.UserID,
		&quota.Tier,
		&quota.AnnualLimit,
		&quota.UsedThisPeriod,
		&quota.PeriodStartDate,
		&quota.PeriodEndDate,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &quota, nil
}

// updateLastLogin updates the user's last login timestamp
func (h *Handler) updateLastLogin(ctx context.Context, userID string) error {
	query := `UPDATE users SET "lastLoginAt" = NOW(), "updatedAt" = NOW() WHERE id = $1`
	_, err := h.db.Main.Exec(ctx, query, userID)
	return err
}

// buildEntitlements builds the entitlements object for a user
// Matches www_v1 response format for API contract compatibility
func (h *Handler) buildEntitlements(user *User, quota *V2Quota) *Entitlements {
	tier := "free"
	if quota != nil {
		tier = strings.ToLower(strings.TrimPrefix(quota.Tier, "V2_"))
	}

	// All V2 subscribers get full features
	features := map[string]bool{
		"basicAnalysis":       true,
		"advancedAnalysis":    true,
		"sensitivityAnalysis": true,
		"exportToPdf":         true,
		"apiAccess":           false, // Reserved for enterprise
		"bulkAnalysis":        false, // Reserved for enterprise
		"aiMarketAnalysis":    true,
		"aiQueryHistory":      true,
	}

	entitlements := &Entitlements{
		HasActiveSubscription:       quota != nil && quota.PeriodEndDate.After(time.Now()),
		SubscriptionTier:            tier,
		HasDataTier:                 getStringOrDefault(user.HasDataTier, "free"),
		AnalysisSessionsRemaining:   -1, // Unlimited for V2 subscribers
		MaxAnalysisSessionsPerMonth: -1, // Unlimited for V2 subscribers
		Features:                    features,
	}

	if quota != nil {
		remaining := quota.AnnualLimit - quota.UsedThisPeriod
		if remaining < 0 {
			remaining = 0
		}

		isUnlimited := strings.Contains(quota.Tier, "PRIVATE") || quota.AnnualLimit < 0

		// remaining can be "unlimited" string or an integer
		var remainingVal interface{}
		if isUnlimited {
			remainingVal = "unlimited"
		} else {
			remainingVal = remaining
		}

		warningThreshold := !isUnlimited && quota.AnnualLimit > 0 &&
			float64(quota.UsedThisPeriod)/float64(quota.AnnualLimit) >= 0.95

		// Get tier-specific features and pricing
		tierFeatures, pricePerYear := getTierFeaturesAndPrice(quota.Tier)

		entitlements.V2Quota = &V2QuotaInfo{
			Tier:             quota.Tier,
			DisplayName:      getTierDisplayName(quota.Tier),
			Used:             quota.UsedThisPeriod,
			Limit:            quota.AnnualLimit,
			Remaining:        remainingVal,
			IsUnlimited:      isUnlimited,
			PeriodStart:      quota.PeriodStartDate.Format(time.RFC3339), // ISO 8601 string
			PeriodEnd:        quota.PeriodEndDate.Format(time.RFC3339),   // ISO 8601 string
			WarningThreshold: warningThreshold,
			TierFeatures:     tierFeatures,
			PricePerYear:     pricePerYear,
		}
	}

	return entitlements
}

// Rate limiting helpers

func (h *Handler) checkRateLimit(ctx context.Context, email string) (allowed bool, retryAfter int) {
	if h.redis == nil {
		return true, 0
	}

	key := rateLimitKeyPrefix + email
	countStr, err := h.redis.Get(ctx, key).Result()
	if err != nil {
		// Key doesn't exist or Redis error - allow the request
		return true, 0
	}

	var count int
	fmt.Sscanf(countStr, "%d", &count)

	if count >= maxLoginAttempts {
		return false, loginLockoutMinutes * 60
	}

	return true, 0
}

func (h *Handler) recordFailedLogin(ctx context.Context, email string) {
	if h.redis == nil {
		return
	}

	key := rateLimitKeyPrefix + email
	count, err := h.redis.Incr(ctx, key)
	if err != nil {
		return
	}

	// Set expiry on first failed attempt
	if count == 1 {
		_ = h.redis.Expire(ctx, key, time.Duration(loginLockoutMinutes)*time.Minute)
	}
}

func (h *Handler) clearLoginAttempts(ctx context.Context, email string) {
	if h.redis == nil {
		return
	}

	key := rateLimitKeyPrefix + email
	_ = h.redis.Delete(ctx, key)
}

// Helper functions

func getStringOrDefault(s *string, defaultVal string) string {
	if s == nil || *s == "" {
		return defaultVal
	}
	return *s
}

func getTierDisplayName(tier string) string {
	switch tier {
	case "V2_PROFESSIONAL":
		return "Professional"
	case "V2_ADVANCED":
		return "Advanced"
	case "V2_PRIVATE":
		return "Private"
	default:
		return strings.TrimPrefix(tier, "V2_")
	}
}

// getTierFeaturesAndPrice returns tier-specific features and annual price
// Matches www_v1's getV2TierConfig() for API contract compatibility
func getTierFeaturesAndPrice(tier string) (map[string]bool, int) {
	// Default features for all V2 tiers
	features := map[string]bool{
		"aiEvaluation":       true,
		"stressTests":        true,
		"portfolioAnalysis":  true,
		"marketData":         true,
		"exportPdf":          true,
		"chatHistory":        true,
		"customStressTests":  false,
		"prioritySupport":    false,
		"dedicatedAnalyst":   false,
	}

	switch tier {
	case "V2_PROFESSIONAL":
		return features, 1200

	case "V2_ADVANCED":
		features["customStressTests"] = true
		features["prioritySupport"] = true
		return features, 2400

	case "V2_PRIVATE":
		features["customStressTests"] = true
		features["prioritySupport"] = true
		features["dedicatedAnalyst"] = true
		return features, 0 // Contact for pricing

	default:
		return features, 0
	}
}
