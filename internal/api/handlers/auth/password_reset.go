package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/estara-ai/www/internal/api/handlers/util"
	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/email"
	"github.com/estara-ai/www/pkg/httputil"
)

const (
	tokenExpiryMinutes = 30
)

// ForgotPasswordRequest represents a forgot password request
type ForgotPasswordRequest struct {
	Email       string `json:"email" validate:"required,email"`
	RedirectURL string `json:"redirectUrl"`
}

// ForgotPasswordResponse represents a forgot password response
type ForgotPasswordResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	RedirectURL string `json:"redirectUrl,omitempty"`
}

// ResetPasswordRequest represents a password reset confirmation request
type ResetPasswordRequest struct {
	Token       string `json:"token" validate:"required"`
	NewPassword string `json:"newPassword" validate:"required,min=8"`
}

// ResetPasswordResponse represents a password reset response
type ResetPasswordResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// UpdatePasswordRequest represents an authenticated password update request
type UpdatePasswordRequest struct {
	CurrentPassword string `json:"currentPassword" validate:"required"`
	NewPassword     string `json:"newPassword" validate:"required,min=8"`
}

// ClientForgotPassword handles password reset requests
// POST /api/auth/client-forgot-password
func (h *Handler) ClientForgotPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ForgotPasswordRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if req.Email == "" {
		httputil.BadRequest(w, "Email is required")
		return
	}

	// Validate email format
	if !isValidEmail(req.Email) {
		httputil.BadRequest(w, "Invalid email format")
		return
	}

	normalizedEmail := strings.ToLower(strings.TrimSpace(req.Email))

	// For security, always return success even if user doesn't exist
	successResponse := ForgotPasswordResponse{
		Success:     true,
		Message:     "If this email is registered, you will receive password reset instructions shortly.",
		RedirectURL: h.cfg.Server.MarketingURL + "/sign-in?reset=true",
	}

	// Look up user
	user, err := h.getUserByEmail(ctx, normalizedEmail)
	if err != nil {
		h.logger.Info("password reset requested for non-existent email", "email", normalizedEmail)
		h.logPasswordResetAudit(ctx, r, "", "AUTH_FAILURE", "Password reset for non-existent email", false, "email not found")
		httputil.JSON(w, http.StatusOK, successResponse)
		return
	}

	// Generate and store reset token
	token, err := h.createPasswordResetToken(ctx, user.ID, normalizedEmail)
	if err != nil {
		h.logger.Error("failed to create password reset token", "error", err)
		httputil.JSON(w, http.StatusOK, successResponse)
		return
	}

	// Send password reset email
	emailSvc := email.NewService(h.cfg)
	firstName := "User"
	if user.FirstName != nil && *user.FirstName != "" {
		firstName = *user.FirstName
	}

	result, err := emailSvc.SendPasswordReset(normalizedEmail, token, firstName)
	if err != nil || !result.Success {
		h.logger.Error("failed to send password reset email", "error", err)
		errMsg := "email send failure"
		if err != nil {
			errMsg = err.Error()
		}
		h.logPasswordResetAudit(ctx, r, user.ID, "AUTH_FAILURE", "Password reset email failed to send", false, errMsg)
		// Still return success for security
	} else {
		h.logger.Info("password reset email sent", "email", normalizedEmail)
		h.logPasswordResetAudit(ctx, r, user.ID, "AUTH_SUCCESS", "Password reset requested", true, "")
	}

	httputil.JSON(w, http.StatusOK, successResponse)
}

// ResetPassword handles password reset confirmation
// POST /api/auth/reset-password
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ResetPasswordRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Validate password strength
	if !isStrongPassword(req.NewPassword) {
		h.logPasswordResetAudit(ctx, r, "", "AUTH_FAILURE", "Password reset with weak password", false, "password does not meet strength requirements")
		httputil.BadRequest(w, "Password must be at least 8 characters with uppercase, lowercase, number, and special character")
		return
	}

	// Validate token and get user info
	tokenInfo, err := h.validatePasswordResetToken(ctx, req.Token)
	if err != nil {
		h.logger.Warn("invalid password reset token", "error", err)
		h.logPasswordResetAudit(ctx, r, "", "AUTH_FAILURE", "Password reset with invalid token", false, err.Error())
		httputil.JSON(w, http.StatusBadRequest, ResetPasswordResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	// Hash new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		h.logger.Error("failed to hash password", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to process password")
		return
	}

	// Update user's password
	err = h.updateUserPassword(ctx, tokenInfo.UserID, string(hashedPassword))
	if err != nil {
		h.logger.Error("failed to update password", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to update password")
		return
	}

	// Mark token as used
	err = h.markPasswordResetTokenUsed(ctx, req.Token)
	if err != nil {
		h.logger.Error("failed to mark token as used", "error", err)
		// Non-critical error, continue
	}

	// Invalidate all other password reset tokens for this user
	_ = h.invalidateUserPasswordResetTokens(ctx, tokenInfo.UserID)

	h.logger.Info("password reset successful", "user_id", tokenInfo.UserID)
	h.logPasswordResetAudit(ctx, r, tokenInfo.UserID, "AUTH_SUCCESS", "Password reset completed", true, "")
	httputil.JSON(w, http.StatusOK, ResetPasswordResponse{
		Success: true,
		Message: "Password has been reset successfully. You can now sign in with your new password.",
	})
}

// UpdatePassword handles authenticated password updates
// POST /api/auth/update-password (protected)
func (h *Handler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	// This endpoint requires authentication - user must be logged in
	// The router should apply auth middleware before this handler
	ctx := r.Context()

	var req UpdatePasswordRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Get user from context (set by auth middleware)
	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}
	userID := claims.UserID

	// Get user and verify current password
	user, err := h.getUserByID(ctx, userID)
	if err != nil {
		httputil.NotFound(w, "User not found")
		return
	}

	if user.Password == nil || *user.Password == "" {
		httputil.BadRequest(w, "Password not set for this account")
		return
	}

	// Verify current password
	if err := bcrypt.CompareHashAndPassword([]byte(*user.Password), []byte(req.CurrentPassword)); err != nil {
		httputil.BadRequest(w, "Current password is incorrect")
		return
	}

	// Validate new password strength
	if !isStrongPassword(req.NewPassword) {
		httputil.BadRequest(w, "Password must be at least 8 characters with uppercase, lowercase, number, and special character")
		return
	}

	// Hash new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		h.logger.Error("failed to hash password", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to process password")
		return
	}

	// Update password
	err = h.updateUserPassword(ctx, userID, string(hashedPassword))
	if err != nil {
		h.logger.Error("failed to update password", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to update password")
		return
	}

	h.logger.Info("password updated", "user_id", userID)

	// Log feature usage for customer support and chargeback defense
	_ = util.LogFeatureUsage(ctx, h.store, r, userID, "FEATURE_PASSWORD_CHANGE",
		"Password changed by user",
		map[string]any{
			"method": "authenticated_change",
		},
	)

	httputil.Success(w, map[string]interface{}{
		"success": true,
		"message": "Password updated successfully",
	})
}

// Token info returned from validation
type tokenInfo struct {
	UserID string
	Email  string
}

// createPasswordResetToken creates a new password reset token using sqlc-generated queries
func (h *Handler) createPasswordResetToken(ctx context.Context, userID, emailAddr string) (string, error) {
	// Generate cryptographically secure random token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)

	// Calculate expiration
	expiresAt := time.Now().Add(time.Duration(tokenExpiryMinutes) * time.Minute)

	// Delete any existing reset tokens for this user
	err := h.store.Q().DeletePasswordResetTokensByUser(ctx, userID)
	if err != nil {
		h.logger.Warn("failed to delete existing tokens", "error", err)
		// Continue anyway
	}

	// Generate a unique ID for the token record
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	// Insert new token using sqlc
	_, err = h.store.Q().CreatePasswordResetToken(ctx, queries.CreatePasswordResetTokenParams{
		ID:        id,
		Token:     token,
		UserId:    userID,
		Email:     emailAddr,
		ExpiresAt: pgtype.Timestamp{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return "", err
	}

	return token, nil
}

// validatePasswordResetToken validates a password reset token
func (h *Handler) validatePasswordResetToken(ctx context.Context, token string) (*tokenInfo, error) {
	row, err := h.store.Q().GetPasswordResetTokenRaw(ctx, token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("Token not found")
		}
		return nil, err
	}

	if row.Used {
		return nil, errors.New("Token has already been used")
	}

	if row.ExpiresAt.Valid && time.Now().After(row.ExpiresAt.Time) {
		// Clean up expired token
		_ = h.store.Q().DeletePasswordResetTokenByToken(ctx, token)
		return nil, errors.New("Token has expired")
	}

	return &tokenInfo{UserID: row.UserId, Email: row.Email}, nil
}

// markPasswordResetTokenUsed marks a token as used using sqlc-generated query
func (h *Handler) markPasswordResetTokenUsed(ctx context.Context, token string) error {
	return h.store.Q().MarkPasswordResetTokenUsedByToken(ctx, token)
}

// invalidateUserPasswordResetTokens invalidates all tokens for a user using sqlc-generated query
func (h *Handler) invalidateUserPasswordResetTokens(ctx context.Context, userID string) error {
	return h.store.Q().InvalidateUserPasswordResetTokens(ctx, userID)
}

// updateUserPassword updates the user's password in the database using sqlc-generated query
func (h *Handler) updateUserPassword(ctx context.Context, userID, hashedPassword string) error {
	return h.store.Q().UpdateUserPassword(ctx, queries.UpdateUserPasswordParams{
		ID:       userID,
		Password: pgtype.Text{String: hashedPassword, Valid: true},
	})
}

// logPasswordResetAudit logs a password reset audit event
func (h *Handler) logPasswordResetAudit(ctx context.Context, r *http.Request, userID, eventType, description string, success bool, errMsg string) {
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	// Extract client IP from headers
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

	q := h.store.Q()
	err := q.CreateAuditLog(ctx, queries.CreateAuditLogParams{
		ID:        id,
		UserId:    pgtype.Text{String: userID, Valid: userID != ""},
		Event: eventType,
		Description: pgtype.Text{String: description, Valid: true},
		IpAddress:   pgtype.Text{String: clientIP, Valid: true},
		UserAgent:   pgtype.Text{String: r.UserAgent(), Valid: true},
		Success:     success,
		Error:       pgtype.Text{String: errMsg, Valid: errMsg != ""},
		Endpoint:    pgtype.Text{String: r.URL.Path, Valid: true},
		Severity:    "info",
	})
	if err != nil {
		h.logger.Warn("failed to write audit log", "error", err)
	}
}

// isValidEmail validates email format
func isValidEmail(email string) bool {
	emailRegex := regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	return emailRegex.MatchString(email)
}

// isStrongPassword validates password strength
func isStrongPassword(password string) bool {
	if len(password) < 8 {
		return false
	}
	if len(password) > 128 {
		return false
	}

	hasUpper := regexp.MustCompile(`[A-Z]`).MatchString(password)
	hasLower := regexp.MustCompile(`[a-z]`).MatchString(password)
	hasDigit := regexp.MustCompile(`\d`).MatchString(password)
	hasSpecial := regexp.MustCompile(`[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]`).MatchString(password)

	return hasUpper && hasLower && hasDigit && hasSpecial
}
