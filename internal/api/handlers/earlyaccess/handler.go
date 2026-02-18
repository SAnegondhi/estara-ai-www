// Package earlyaccess handles the early access program endpoints (ADR-085).
//
// Public endpoints (no auth):
//   POST /api/early-access/apply          — submit application
//   GET  /api/auth/verify-setup-token     — validate one-time token
//   POST /api/auth/setup-password         — set initial password
//
// Authenticated user endpoints:
//   POST /api/auth/request-termination    — user self-terminates early access account
package earlyaccess

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/email"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles early access program HTTP requests.
type Handler struct {
	store  *dbstore.Store
	redis  *redisClient.Client
	cfg    *config.Config
	auth   *middleware.AuthMiddleware
	logger *slog.Logger
}

// NewHandler creates a new early access handler.
func NewHandler(store *dbstore.Store, redis *redisClient.Client, cfg *config.Config, auth *middleware.AuthMiddleware) *Handler {
	return &Handler{
		store:  store,
		redis:  redis,
		cfg:    cfg,
		auth:   auth,
		logger: slog.Default().With("component", "earlyaccess_handler"),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Request / Response types
// ─────────────────────────────────────────────────────────────────────────────

// ApplyRequest is the body for POST /api/early-access/apply.
type ApplyRequest struct {
	FirstName                  string `json:"firstName"`
	LastName                   string `json:"lastName"`
	Email                      string `json:"email"`
	Company                    string `json:"company"`
	UseCase                    string `json:"useCase"`
	PortfolioSize              string `json:"portfolioSize"`
	LinkedInURL                string `json:"linkedinUrl"`
	PrimaryMarkets             string `json:"primaryMarkets"`
	KeyInvestmentDecisions     string `json:"keyInvestmentDecisions"`
	CurrentAnalyticalApproach  string `json:"currentAnalyticalApproach"`
}

// SetupPasswordRequest is the body for POST /api/auth/setup-password.
type SetupPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Public Endpoints
// ─────────────────────────────────────────────────────────────────────────────

// Apply handles POST /api/early-access/apply.
// Always returns 202 Accepted to prevent email enumeration.
func (h *Handler) Apply(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate required fields
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.UseCase = strings.TrimSpace(req.UseCase)

	if req.FirstName == "" || req.LastName == "" || req.Email == "" || req.UseCase == "" {
		httputil.BadRequest(w, "firstName, lastName, email, and useCase are required")
		return
	}
	if !strings.Contains(req.Email, "@") {
		httputil.BadRequest(w, "invalid email address")
		return
	}

	// Check for duplicate — always respond 202 regardless (privacy)
	existing, err := h.store.Q().GetEarlyAccessRequestByEmail(ctx, req.Email)
	if err == nil && existing.ID != "" {
		h.logger.Info("early access apply: duplicate email, silently accepted", "email", req.Email)
		httputil.JSON(w, http.StatusAccepted, map[string]interface{}{
			"success": true,
			"message": "Application received. We'll be in touch within 2–3 business days.",
		})
		return
	}

	// Generate ID
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		h.logger.Error("failed to generate request ID", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	id := hex.EncodeToString(idBytes)

	// Insert request
	companyNull := pgtype.Text{String: req.Company, Valid: req.Company != ""}
	portfolioNull := pgtype.Text{String: req.PortfolioSize, Valid: req.PortfolioSize != ""}
	linkedinNull := pgtype.Text{String: req.LinkedInURL, Valid: req.LinkedInURL != ""}
	marketsNull := pgtype.Text{String: req.PrimaryMarkets, Valid: req.PrimaryMarkets != ""}
	investDecNull := pgtype.Text{String: req.KeyInvestmentDecisions, Valid: req.KeyInvestmentDecisions != ""}
	analyticalNull := pgtype.Text{String: req.CurrentAnalyticalApproach, Valid: req.CurrentAnalyticalApproach != ""}

	_, err = h.store.Q().CreateEarlyAccessRequest(ctx, queries.CreateEarlyAccessRequestParams{
		ID:                         id,
		Email:                      req.Email,
		FirstName:                  req.FirstName,
		LastName:                   req.LastName,
		Company:                    companyNull,
		UseCase:                    req.UseCase,
		PortfolioSize:              portfolioNull,
		LinkedinUrl:                linkedinNull,
		PrimaryMarkets:             marketsNull,
		KeyInvestmentDecisions:     investDecNull,
		CurrentAnalyticalApproach:  analyticalNull,
	})
	if err != nil {
		h.logger.Error("failed to create early access request", "error", err)
		// Still return 202 — don't leak internal errors
		httputil.JSON(w, http.StatusAccepted, map[string]interface{}{
			"success": true,
			"message": "Application received. We'll be in touch within 2–3 business days.",
		})
		return
	}

	// Send acknowledgement email (best-effort)
	emailSvc := email.NewService(h.cfg)
	if _, err := emailSvc.SendEarlyAccessReceived(req.Email, req.FirstName); err != nil {
		h.logger.Warn("failed to send early access received email", "error", err, "email", req.Email)
	}

	// Write audit log (best-effort)
	h.logAudit(ctx, r, "system", "EARLY_ACCESS_APPLIED", "early_access_requests", id, map[string]interface{}{
		"email":     req.Email,
		"firstName": req.FirstName,
		"lastName":  req.LastName,
	})

	h.logger.Info("early access application submitted", "id", id, "email", req.Email)
	httputil.JSON(w, http.StatusAccepted, map[string]interface{}{
		"success": true,
		"message": "Application received. We'll be in touch within 2–3 business days.",
	})
}

// VerifySetupToken handles GET /api/auth/verify-setup-token?token=.
func (h *Handler) VerifySetupToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	token := r.URL.Query().Get("token")
	if token == "" {
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"valid":  false,
			"reason": "not_found",
		})
		return
	}

	row, err := h.store.Q().GetPasswordSetupToken(ctx, token)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.JSON(w, http.StatusOK, map[string]interface{}{
				"valid":  false,
				"reason": "not_found",
			})
			return
		}
		h.logger.Error("failed to get setup token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Check used
	if row.UsedAt.Valid {
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"valid":  false,
			"reason": "used",
		})
		return
	}

	// Check expired
	if row.ExpiresAt.Before(time.Now()) {
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"valid":  false,
			"reason": "expired",
		})
		return
	}

	// Look up user to return email
	user, err := h.store.Q().GetUserByID(ctx, row.UserID)
	if err != nil {
		h.logger.Error("failed to get user for setup token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"valid": true,
		"email": user.Email,
	})
}

// SetupPassword handles POST /api/auth/setup-password.
// Validates the one-time token, hashes the password, logs the user in.
func (h *Handler) SetupPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req SetupPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	req.Token = strings.TrimSpace(req.Token)
	req.Password = strings.TrimSpace(req.Password)

	if req.Token == "" || req.Password == "" {
		httputil.BadRequest(w, "token and password are required")
		return
	}
	if len(req.Password) < 8 {
		httputil.BadRequest(w, "password must be at least 8 characters")
		return
	}

	// Load token
	row, err := h.store.Q().GetPasswordSetupToken(ctx, req.Token)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.JSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"error":  "invalid_token",
				"reason": "not_found",
			})
			return
		}
		h.logger.Error("failed to get setup token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if row.UsedAt.Valid {
		httputil.JSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error":  "invalid_token",
			"reason": "used",
		})
		return
	}
	if row.ExpiresAt.Before(time.Now()) {
		httputil.JSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error":  "invalid_token",
			"reason": "expired",
		})
		return
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("failed to hash password", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Set password and mark early_access_status active
	hashStr := string(hash)
	if err := h.store.Q().SetUserPasswordAndStatus(ctx, queries.SetUserPasswordAndStatusParams{
		ID:       row.UserID,
		Password: pgtype.Text{String: hashStr, Valid: true},
	}); err != nil {
		h.logger.Error("failed to set user password", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Mark token used
	if err := h.store.Q().MarkPasswordSetupTokenUsed(ctx, row.ID); err != nil {
		h.logger.Warn("failed to mark setup token used", "error", err, "token_id", row.ID)
	}

	// Load user to build JWT
	user, err := h.store.Q().GetUserByID(ctx, row.UserID)
	if err != nil {
		h.logger.Error("failed to load user after password set", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Generate JWT tokens
	accessToken, err := h.auth.GenerateToken(
		user.ID,
		"", // no clerk ID for early access users
		user.Email,
		string(user.Role),
		24*time.Hour,
	)
	if err != nil {
		h.logger.Error("failed to generate access token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	refreshToken, err := h.auth.GenerateRefreshToken(user.ID, 7*24*time.Hour)
	if err != nil {
		h.logger.Error("failed to generate refresh token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Set auth cookies (ADR-066)
	secure := !h.cfg.IsDevelopment()
	middleware.SetAuthCookies(w, accessToken, refreshToken, secure)
	csrfToken := middleware.SetCSRFCookie(w, secure, h.cfg.Security.CSRFTokenLength)

	h.logAudit(ctx, r, user.ID, "USER_SIGNIN", "users", user.ID, map[string]interface{}{
		"method": "setup_password",
	})

	h.logger.Info("early access password setup complete", "user_id", user.ID)
	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"token":        accessToken,
		"refreshToken": refreshToken,
		"csrfToken":    csrfToken,
		"user": map[string]interface{}{
			"id":               user.ID,
			"email":            user.Email,
			"firstName":        nilableString(user.FirstName),
			"lastName":         nilableString(user.LastName),
			"subscriptionTier": "early_access",
		},
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Authenticated User Endpoints
// ─────────────────────────────────────────────────────────────────────────────

// RequestTermination handles POST /api/auth/request-termination.
// Authenticated early access users can self-terminate their account.
func (h *Handler) RequestTermination(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	authUser := middleware.GetUserFromContext(ctx)
	if authUser == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	// Load user to check tier
	user, err := h.store.Q().GetUserByID(ctx, authUser.UserID)
	if err != nil {
		h.logger.Error("failed to load user for termination", "error", err, "user_id", authUser.UserID)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	tier := ""
	if user.SubscriptionTier.Valid {
		tier = user.SubscriptionTier.String
	}
	if tier != "early_access" {
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":   "not_early_access",
			"message": "This endpoint is only available to early access users.",
		})
		return
	}

	// Set user to terminated + inactive
	if err := h.store.Q().UpdateUserEarlyAccessStatus(ctx, queries.UpdateUserEarlyAccessStatusParams{
		ID:                user.ID,
		EarlyAccessStatus: pgtype.Text{String: "terminated", Valid: true},
	}); err != nil {
		h.logger.Error("failed to update early access status", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Invalidate refresh tokens via Redis (best-effort)
	h.invalidateUserSessions(ctx, user.ID)

	// Send termination confirmation email (best-effort)
	emailSvc := email.NewService(h.cfg)
	name := ""
	if user.FirstName.Valid {
		name = user.FirstName.String
	}
	if _, err := emailSvc.SendEarlyAccessTerminated(user.Email, name); err != nil {
		h.logger.Warn("failed to send termination email", "error", err, "user_id", user.ID)
	}

	h.logAudit(ctx, r, user.ID, "EARLY_ACCESS_TERMINATION_REQUESTED", "users", user.ID, nil)
	h.logger.Info("early access termination requested", "user_id", user.ID)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Account closure confirmed. Check your email.",
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) invalidateUserSessions(ctx context.Context, userID string) {
	if h.redis == nil {
		return
	}
	key := "refresh_token:user:" + userID
	if err := h.redis.Client.Del(ctx, key).Err(); err != nil {
		h.logger.Warn("failed to invalidate user sessions", "error", err, "user_id", userID)
	}
}

func (h *Handler) logAudit(ctx context.Context, r *http.Request, actorID, action, resource, resourceID string, details map[string]interface{}) {
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

	var detailsJSON json.RawMessage
	if details != nil {
		detailsJSON, _ = json.Marshal(details)
	}

	err := h.store.Q().CreateAdminAuditLog(ctx, queries.CreateAdminAuditLogParams{
		ID:         id,
		AdminId:    actorID,
		AdminEmail: actorID,
		Action:     action,
		Resource:   resource,
		ResourceId: pgtype.Text{String: resourceID, Valid: resourceID != ""},
		Details:    detailsJSON,
		IpAddress:  clientIP,
		UserAgent:  r.UserAgent(),
	})
	if err != nil {
		h.logger.Warn("failed to write audit log", "error", err, "action", action)
	}
}

func nilableString(pt pgtype.Text) *string {
	if pt.Valid {
		return &pt.String
	}
	return nil
}
