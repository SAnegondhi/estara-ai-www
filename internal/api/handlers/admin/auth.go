package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// Admin Auth Types
// ===============================

type adminLoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
	TOTPCode string `json:"totpCode,omitempty"`
}

type adminLoginResponse struct {
	Success      bool   `json:"success"`
	Token        string `json:"token"`
	RequiresTOTP bool   `json:"requiresTOTP,omitempty"`
	SessionID    string `json:"sessionId"`
	ExpiresAt    string `json:"expiresAt"`
	Admin        adminUserInfo `json:"admin"`
}

type adminUserInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type setup2FAResponse struct {
	Secret      string   `json:"secret"`
	QRCodeURL   string   `json:"qrCodeUrl"`
	BackupCodes []string `json:"backupCodes"`
}

type verify2FARequest struct {
	Code string `json:"code" validate:"required,len=6"`
}

type disable2FARequest struct {
	Code string `json:"code" validate:"required,len=6"`
}

type twoFactorStatus struct {
	Enabled   bool    `json:"enabled"`
	SetupAt   *string `json:"setupAt,omitempty"`
}

type sessionResponse struct {
	ID         string  `json:"id"`
	IPAddress  string  `json:"ipAddress"`
	UserAgent  string  `json:"userAgent"`
	DeviceName *string `json:"deviceName,omitempty"`
	Location   *string `json:"location,omitempty"`
	CreatedAt  string  `json:"createdAt"`
	LastActive string  `json:"lastActive"`
	ExpiresAt  string  `json:"expiresAt"`
	IsCurrent  bool    `json:"isCurrent"`
}

// ===============================
// Admin Login
// ===============================

// AdminLogin authenticates an admin user and returns a short-lived admin JWT.
// POST /api/auth/admin-login
func (h *Handler) AdminLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req adminLoginRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))

	// Look up user by email
	q := queries.New(h.db.Main)
	user, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		h.logger.Warn("admin login: user not found", "email", email, "error", err.Error())
		httputil.Unauthorized(w, "Invalid credentials")
		return
	}

	// Verify user is an admin
	isAdmin := strings.EqualFold(user.Role, "ADMIN") || strings.EqualFold(user.Role, "SUPER_ADMIN") || h.auth.IsAdmin(user.ID)
	if !isAdmin {
		h.logger.Warn("admin login: non-admin user attempted admin login", "email", email, "user_id", user.ID)
		httputil.Forbidden(w, "admin access required")
		return
	}

	// Verify password
	if !user.Password.Valid || user.Password.String == "" {
		h.logger.Warn("admin login: no password set", "email", email)
		httputil.Unauthorized(w, "Invalid credentials")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password.String), []byte(req.Password)); err != nil {
		h.logger.Warn("admin login: invalid password", "email", email)
		httputil.Unauthorized(w, "Invalid credentials")
		return
	}

	// Check 2FA if enabled
	twoFactor, err := q.GetAdminTwoFactorByUserID(ctx, user.ID)
	if err == nil && twoFactor.Enabled {
		if req.TOTPCode == "" {
			// 2FA required but no code provided
			httputil.JSON(w, http.StatusOK, map[string]any{
				"success":      false,
				"requiresTOTP": true,
				"message":      "Two-factor authentication code required",
			})
			return
		}

		// Validate TOTP code
		valid := totp.Validate(req.TOTPCode, twoFactor.Secret)
		if !valid {
			// Also check backup codes
			if !h.consumeBackupCode(ctx, q, user.ID, req.TOTPCode, twoFactor.BackupCodes) {
				h.logger.Warn("admin login: invalid TOTP code", "email", email)
				httputil.Unauthorized(w, "Invalid two-factor code")
				return
			}
		}
	}

	// Generate admin token (8h expiry)
	token, err := h.auth.GenerateAdminToken(user.ID, email)
	if err != nil {
		h.logger.Error("admin login: failed to generate token", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to generate token"))
		return
	}

	// Create admin session
	expiresAt := time.Now().Add(8 * time.Hour)
	tokenHash := hashToken(token)
	sessionID := uuid.New().String()

	_, err = q.CreateAdminSession(ctx, queries.CreateAdminSessionParams{
		ID:        sessionID,
		UserId:    user.ID,
		TokenHash: tokenHash,
		IpAddress: getClientIP(r),
		UserAgent: truncate(r.UserAgent(), 500),
		DeviceName: pgtype.Text{
			String: parseDeviceName(r.UserAgent()),
			Valid:  true,
		},
		ExpiresAt: pgtype.Timestamp{
			Time:  expiresAt,
			Valid: true,
		},
	})
	if err != nil {
		h.logger.Error("admin login: failed to create session", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to create session"))
		return
	}

	h.logger.Info("admin login successful", "user_id", user.ID, "email", email, "session_id", sessionID)

	httputil.JSON(w, http.StatusOK, adminLoginResponse{
		Success:   true,
		Token:     token,
		SessionID: sessionID,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Admin: adminUserInfo{
			ID:    user.ID,
			Email: email,
			Role:  "admin",
		},
	})
}

// ===============================
// 2FA Endpoints
// ===============================

// Setup2FA generates a new TOTP secret and backup codes.
// POST /api/admin/2fa/setup
func (h *Handler) Setup2FA(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "")
		return
	}

	q := queries.New(h.db.Main)

	// Check if 2FA already enabled
	existing, existErr := q.GetAdminTwoFactorByUserID(ctx, user.UserID)
	if existErr == nil && existing.Enabled {
		httputil.BadRequest(w, "Two-factor authentication is already enabled")
		return
	}

	// Generate TOTP key
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Estara Admin",
		AccountName: user.Email,
	})
	if err != nil {
		h.logger.Error("2FA setup: failed to generate key", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to generate 2FA key"))
		return
	}

	// Generate backup codes
	backupCodes := generateBackupCodes(8)

	// Delete existing (unenabled) record if any
	if existErr == nil {
		_ = q.DeleteAdminTwoFactor(ctx, user.UserID)
	}

	// Store TOTP secret (not yet enabled)
	_, err = q.CreateAdminTwoFactor(ctx, queries.CreateAdminTwoFactorParams{
		ID:          uuid.New().String(),
		UserId:      user.UserID,
		Secret:      key.Secret(),
		BackupCodes: backupCodes,
	})
	if err != nil {
		h.logger.Error("2FA setup: failed to save secret", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to save 2FA configuration"))
		return
	}

	httputil.Success(w, setup2FAResponse{
		Secret:      key.Secret(),
		QRCodeURL:   key.URL(),
		BackupCodes: backupCodes,
	})
}

// Verify2FA confirms a TOTP code and enables 2FA.
// POST /api/admin/2fa/verify
func (h *Handler) Verify2FA(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "")
		return
	}

	var req verify2FARequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	q := queries.New(h.db.Main)

	// Get the pending 2FA record
	twoFactor, err := q.GetAdminTwoFactorByUserID(ctx, user.UserID)
	if err != nil {
		httputil.BadRequest(w, "Two-factor authentication not set up")
		return
	}

	if twoFactor.Enabled {
		httputil.BadRequest(w, "Two-factor authentication is already enabled")
		return
	}

	// Validate the TOTP code against the secret
	valid := totp.Validate(req.Code, twoFactor.Secret)
	if !valid {
		httputil.BadRequest(w, "Invalid verification code")
		return
	}

	// Enable 2FA
	if err := q.EnableAdminTwoFactor(ctx, user.UserID); err != nil {
		h.logger.Error("2FA verify: failed to enable", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to enable 2FA"))
		return
	}

	h.logger.Info("2FA enabled", "user_id", user.UserID)
	httputil.Success(w, map[string]any{"enabled": true})
}

// Disable2FA disables two-factor authentication.
// POST /api/admin/2fa/disable
func (h *Handler) Disable2FA(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "")
		return
	}

	var req disable2FARequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	q := queries.New(h.db.Main)

	// Get the 2FA record
	twoFactor, err := q.GetAdminTwoFactorByUserID(ctx, user.UserID)
	if err != nil {
		httputil.BadRequest(w, "Two-factor authentication not configured")
		return
	}

	if !twoFactor.Enabled {
		httputil.BadRequest(w, "Two-factor authentication is not enabled")
		return
	}

	// Validate code before disabling
	valid := totp.Validate(req.Code, twoFactor.Secret)
	if !valid {
		httputil.BadRequest(w, "Invalid verification code")
		return
	}

	// Disable and delete
	if err := q.DeleteAdminTwoFactor(ctx, user.UserID); err != nil {
		h.logger.Error("2FA disable: failed to delete", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to disable 2FA"))
		return
	}

	h.logger.Info("2FA disabled", "user_id", user.UserID)
	httputil.Success(w, map[string]any{"enabled": false})
}

// Get2FAStatus returns whether 2FA is enabled for the current admin.
// GET /api/admin/2fa/status
func (h *Handler) Get2FAStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "")
		return
	}

	q := queries.New(h.db.Main)

	twoFactor, err := q.GetAdminTwoFactorByUserID(ctx, user.UserID)
	if err != nil {
		httputil.Success(w, twoFactorStatus{Enabled: false})
		return
	}

	status := twoFactorStatus{Enabled: twoFactor.Enabled}
	if twoFactor.Enabled && twoFactor.CreatedAt.Valid {
		t := twoFactor.CreatedAt.Time.Format(time.RFC3339)
		status.SetupAt = &t
	}

	httputil.Success(w, status)
}

// ===============================
// Session Management
// ===============================

// ListSessions returns all active admin sessions for the current user.
// GET /api/admin/sessions
func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "")
		return
	}

	q := queries.New(h.db.Main)

	sessions, err := q.ListAdminSessionsByUser(ctx, user.UserID)
	if err != nil {
		h.logger.Error("list sessions: query failed", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to list sessions"))
		return
	}

	// Get current token hash to mark the current session
	currentTokenHash := ""
	if bearerToken, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		currentTokenHash = hashToken(bearerToken)
	}

	result := make([]sessionResponse, 0, len(sessions))
	for _, s := range sessions {
		resp := sessionResponse{
			ID:        s.ID,
			IPAddress: s.IpAddress,
			UserAgent: s.UserAgent,
			IsCurrent: s.TokenHash == currentTokenHash,
		}
		if s.DeviceName.Valid {
			resp.DeviceName = &s.DeviceName.String
		}
		if s.Location.Valid {
			resp.Location = &s.Location.String
		}
		if s.CreatedAt.Valid {
			resp.CreatedAt = s.CreatedAt.Time.Format(time.RFC3339)
		}
		if s.LastActive.Valid {
			resp.LastActive = s.LastActive.Time.Format(time.RFC3339)
		}
		if s.ExpiresAt.Valid {
			resp.ExpiresAt = s.ExpiresAt.Time.Format(time.RFC3339)
		}
		result = append(result, resp)
	}

	httputil.Success(w, result)
}

// RevokeSession revokes a specific admin session.
// DELETE /api/admin/sessions/{id}
func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "")
		return
	}

	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		httputil.BadRequest(w, "session ID required")
		return
	}

	q := queries.New(h.db.Main)

	// Verify the session belongs to this user
	session, err := q.GetAdminSessionByID(ctx, sessionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.NotFound(w, "session not found")
		} else {
			httputil.InternalError(w, fmt.Errorf("failed to get session"))
		}
		return
	}

	if session.UserId != user.UserID {
		httputil.Forbidden(w, "cannot revoke another user's session")
		return
	}

	if err := q.RevokeAdminSession(ctx, sessionID); err != nil {
		h.logger.Error("revoke session: failed", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to revoke session"))
		return
	}

	h.logger.Info("admin session revoked", "session_id", sessionID, "user_id", user.UserID)
	httputil.Success(w, map[string]any{"revoked": true})
}

// ===============================
// Helpers
// ===============================

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		return xri
	}
	// Strip port from RemoteAddr
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

func parseDeviceName(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "chrome"):
		return "Chrome"
	case strings.Contains(ua, "firefox"):
		return "Firefox"
	case strings.Contains(ua, "safari"):
		return "Safari"
	case strings.Contains(ua, "edge"):
		return "Edge"
	default:
		return "Unknown Browser"
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func generateBackupCodes(count int) []string {
	codes := make([]string, count)
	for i := range codes {
		b := make([]byte, 5)
		_, _ = rand.Read(b)
		codes[i] = strings.ToUpper(hex.EncodeToString(b))
	}
	return codes
}

func (h *Handler) consumeBackupCode(ctx context.Context, q *queries.Queries, userID, code string, backupCodes []string) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	found := -1
	for i, bc := range backupCodes {
		if bc == code {
			found = i
			break
		}
	}
	if found == -1 {
		return false
	}

	// Remove used code
	remaining := make([]string, 0, len(backupCodes)-1)
	remaining = append(remaining, backupCodes[:found]...)
	remaining = append(remaining, backupCodes[found+1:]...)

	if err := q.UpdateAdminTwoFactorBackupCodes(ctx, queries.UpdateAdminTwoFactorBackupCodesParams{
		UserId:      userID,
		BackupCodes: remaining,
	}); err != nil {
		slog.Error("failed to update backup codes", "error", err)
	}

	return true
}
