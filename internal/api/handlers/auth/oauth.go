package auth

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/pkg/httputil"
)

// OAuthLoginRequest represents a social login request (ADR-082).
type OAuthLoginRequest struct {
	Provider string `json:"provider" validate:"required,oneof=google microsoft apple"`
	IDToken  string `json:"idToken" validate:"required"`
	Name     string `json:"name,omitempty"` // Apple sends name only on first auth
}

// OAuthLogin authenticates a user via social login (Google, Microsoft, Apple).
// ADR-082: Token-based flow — frontend SDKs obtain ID tokens, backend verifies via JWKS.
func (h *Handler) OAuthLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req OAuthLoginRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Rate limit by IP (email not yet known)
	clientIP := extractClientIP(r)
	allowed, retryAfter := h.checkRateLimit(ctx, "oauth_ip:"+clientIP)
	if !allowed {
		h.logger.Warn("rate limited OAuth attempt", "ip", clientIP, "provider", req.Provider)
		h.logAuthAudit(ctx, r, "", "RATE_LIMIT_EXCEEDED", fmt.Sprintf("OAuth login blocked: too many attempts from IP (provider=%s)", req.Provider), false, "rate limited")
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		httputil.JSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"error":      "Too many sign-in attempts. Please try again later.",
			"code":       "ACCOUNT_LOCKED",
			"retryAfter": retryAfter,
		})
		return
	}

	// Verify ID token with the appropriate provider
	var identity *OAuthIdentity
	var verifyErr error

	switch req.Provider {
	case "google":
		if h.cfg.OAuth.GoogleClientID == "" {
			httputil.BadRequest(w, "Google sign-in is not configured")
			return
		}
		identity, verifyErr = VerifyGoogleToken(ctx, req.IDToken, h.cfg.OAuth.GoogleClientID)

	case "microsoft":
		if h.cfg.OAuth.AzureClientID == "" {
			httputil.BadRequest(w, "Microsoft sign-in is not configured")
			return
		}
		identity, verifyErr = VerifyMicrosoftIDToken(ctx, req.IDToken, h.cfg.OAuth.AzureClientID)

	case "apple":
		if h.cfg.OAuth.AppleClientID == "" {
			httputil.BadRequest(w, "Apple sign-in is not configured")
			return
		}
		identity, verifyErr = VerifyAppleIDToken(ctx, req.IDToken, h.cfg.OAuth.AppleClientID)

	default:
		httputil.BadRequest(w, "unsupported provider")
		return
	}

	if verifyErr != nil {
		h.recordFailedLogin(ctx, "oauth_ip:"+clientIP)
		h.logAuthAudit(ctx, r, "", "OAUTH_LOGIN", fmt.Sprintf("OAuth verification failed: provider=%s, error=%s", req.Provider, verifyErr.Error()), false, verifyErr.Error())
		h.logger.Warn("OAuth token verification failed", "provider", req.Provider, "error", verifyErr.Error())
		httputil.Unauthorized(w, "Invalid credentials")
		return
	}

	email := strings.ToLower(strings.TrimSpace(identity.Email))

	// Look up user by email — existing users only (ADR-082)
	user, err := h.getUserByEmail(ctx, email)
	if err != nil {
		h.recordFailedLogin(ctx, "oauth_ip:"+clientIP)
		h.logAuthAudit(ctx, r, "", "OAUTH_LOGIN", fmt.Sprintf("OAuth login failed: no account for email (provider=%s)", req.Provider), false, "user not found")
		h.logger.Warn("OAuth login: user not found", "email", email, "provider", req.Provider)
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":   "No account found for this email. Sign up at estara-ai.com.",
			"code":    "NO_ACCOUNT",
			"message": "No account found for this email. Sign up at estara-ai.com.",
		})
		return
	}

	// Clear rate limit on successful token verification + user found
	h.clearLoginAttempts(ctx, "oauth_ip:"+clientIP)

	// From here, follow the same flow as Login()
	// Check whitelist
	if h.whitelist != nil && !h.whitelist.IsAllowed(ctx, email) {
		h.logAuthAudit(ctx, r, user.ID, "OAUTH_LOGIN", fmt.Sprintf("OAuth login blocked: not whitelisted (provider=%s)", req.Provider), false, "not whitelisted")
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":   "access_denied",
			"code":    "NOT_WHITELISTED",
			"message": "Your account is not approved for beta access. Please contact support.",
		})
		return
	}

	// Check V2 subscription quota
	v2Quota, err := h.getV2Quota(ctx, user.ID)
	if err != nil || v2Quota == nil || v2Quota.PeriodEndDate.Before(time.Now()) {
		h.logger.Warn("no valid V2 subscription (OAuth)", "user_id", user.ID, "provider", req.Provider)
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":       "no_insight_access",
			"code":        "NO_V2_SUBSCRIPTION",
			"message":     "You do not have an active Estara Insight subscription.",
			"canPurchase": true,
			"websiteUrl":  h.cfg.Server.MarketingURL,
		})
		return
	}

	// Build entitlements
	entitlements := h.buildEntitlements(user, v2Quota)

	// Generate tokens
	accessToken, err := h.auth.GenerateToken(user.ID, user.ClerkUserID, email, user.Role, 24*time.Hour)
	if err != nil {
		h.logger.Error("failed to generate access token (OAuth)", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	refreshToken, err := h.auth.GenerateRefreshToken(user.ID, 7*24*time.Hour)
	if err != nil {
		h.logger.Error("failed to generate refresh token (OAuth)", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	// Update last login
	_ = h.updateLastLogin(ctx, user.ID)

	// Set cookies (ADR-066)
	secure := !h.cfg.IsDevelopment()
	middleware.SetAuthCookies(w, accessToken, refreshToken, secure)
	csrfToken := middleware.SetCSRFCookie(w, secure, h.cfg.Security.CSRFTokenLength)

	// Build response (identical to Login response)
	tier := strings.TrimPrefix(v2Quota.Tier, "V2_")
	response := LoginResponse{
		Success:      true,
		Token:        accessToken,
		RefreshToken: refreshToken,
		User: UserResponse{
			ID:               user.ID,
			Email:            email,
			FirstName:        user.FirstName,
			LastName:         user.LastName,
			SubscriptionTier: strings.ToLower(tier),
			Theme:            getStringOrDefault(user.Theme, "estara"),
		},
		Entitlements: entitlements,
		CSRFToken:    csrfToken,
	}

	h.logAuthAudit(ctx, r, user.ID, "OAUTH_LOGIN", fmt.Sprintf("OAuth login successful (provider=%s)", req.Provider), true, "")
	h.logger.Info("OAuth login successful", "user_id", user.ID, "email", email, "provider", req.Provider)
	httputil.JSON(w, http.StatusOK, response)
}

// extractClientIP extracts the client IP from the request.
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	return r.RemoteAddr
}
