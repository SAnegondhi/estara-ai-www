package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/email"
	"github.com/estara-ai/www/pkg/httputil"
)

const (
	codeExpiryMinutes = 10
	maxCodesPerHour   = 3
	maxAttempts       = 5
)

// SendVerificationCodeRequest represents a verification code request
type SendVerificationCodeRequest struct {
	Email     string `json:"email" validate:"required,email"`
	FirstName string `json:"firstName"`
}

// SendVerificationCodeResponse represents the response for sending a code
type SendVerificationCodeResponse struct {
	Success          bool   `json:"success"`
	Message          string `json:"message,omitempty"`
	Error            string `json:"error,omitempty"`
	Code             string `json:"code,omitempty"` // Error code
	ExpiresInMinutes int    `json:"expiresInMinutes,omitempty"`
}

// VerifyCodeRequest represents a code verification request
type VerifyCodeRequest struct {
	Email string `json:"email" validate:"required,email"`
	Code  string `json:"code" validate:"required"`
}

// VerifyCodeResponse represents the response for code verification
type VerifyCodeResponse struct {
	Success           bool   `json:"success"`
	Message           string `json:"message,omitempty"`
	Error             string `json:"error,omitempty"`
	Code              string `json:"code,omitempty"` // Error code
	Verified          bool   `json:"verified,omitempty"`
	RemainingAttempts int    `json:"remainingAttempts,omitempty"`
}

// SendVerificationCode sends a verification code to the provided email
// POST /api/auth/send-verification-code
func (h *Handler) SendVerificationCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req SendVerificationCodeRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.JSON(w, http.StatusBadRequest, SendVerificationCodeResponse{
			Success: false,
			Error:   "invalid request body",
		})
		return
	}

	if req.Email == "" {
		httputil.JSON(w, http.StatusBadRequest, SendVerificationCodeResponse{
			Success: false,
			Error:   "Email is required",
		})
		return
	}

	normalizedEmail := strings.ToLower(strings.TrimSpace(req.Email))

	// Validate email format
	if !isValidEmail(normalizedEmail) {
		httputil.JSON(w, http.StatusBadRequest, SendVerificationCodeResponse{
			Success: false,
			Error:   "Invalid email format",
		})
		return
	}

	// Check if email already exists as a registered user
	user, err := h.getUserByEmail(ctx, normalizedEmail)
	if err == nil && user != nil {
		httputil.JSON(w, http.StatusConflict, SendVerificationCodeResponse{
			Success: false,
			Error:   "An account with this email already exists",
			Code:    "EMAIL_EXISTS",
		})
		return
	}

	// Rate limiting: Check codes sent in the last hour
	recentCount, err := h.countRecentVerificationCodes(ctx, normalizedEmail)
	if err != nil {
		h.logger.Error("failed to check rate limit", "error", err)
	}

	if recentCount >= maxCodesPerHour {
		httputil.JSON(w, http.StatusTooManyRequests, SendVerificationCodeResponse{
			Success: false,
			Error:   "Too many verification requests. Please try again later.",
			Code:    "RATE_LIMITED",
		})
		return
	}

	// Generate 6-digit code
	code := generateVerificationCode()
	expiresAt := time.Now().Add(time.Duration(codeExpiryMinutes) * time.Minute)

	// Delete any existing unverified codes for this email
	err = h.store.Q().DeleteUnverifiedEmailCodesByEmail(ctx, normalizedEmail)
	if err != nil {
		h.logger.Warn("failed to delete existing codes", "error", err)
	}

	// Create new verification code
	err = h.createEmailVerificationCode(ctx, normalizedEmail, code, expiresAt)
	if err != nil {
		h.logger.Error("failed to create verification code", "error", err)
		httputil.JSON(w, http.StatusInternalServerError, SendVerificationCodeResponse{
			Success: false,
			Error:   "Failed to create verification code",
		})
		return
	}

	// Log code for E2E testing in non-production
	if !h.cfg.IsProduction() {
		h.logger.Info("[E2E-VERIFY]", "email", normalizedEmail, "code", code)
	}

	// Send verification email
	emailSvc := email.NewService(h.cfg)
	firstName := req.FirstName
	if firstName == "" {
		firstName = "there"
	}

	result, err := emailSvc.SendVerificationCode(normalizedEmail, code, firstName)
	if err != nil || !result.Success {
		h.logger.Error("failed to send verification email", "error", err)
		httputil.JSON(w, http.StatusInternalServerError, SendVerificationCodeResponse{
			Success: false,
			Error:   "Failed to send verification email. Please try again.",
		})
		return
	}

	h.logger.Info("verification code sent", "email", normalizedEmail)
	httputil.JSON(w, http.StatusOK, SendVerificationCodeResponse{
		Success:          true,
		Message:          "Verification code sent",
		ExpiresInMinutes: codeExpiryMinutes,
	})
}

// VerifyCode verifies an email verification code
// POST /api/auth/verify-code
func (h *Handler) VerifyCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req VerifyCodeRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.JSON(w, http.StatusBadRequest, VerifyCodeResponse{
			Success: false,
			Error:   "invalid request body",
		})
		return
	}

	if req.Email == "" || req.Code == "" {
		httputil.JSON(w, http.StatusBadRequest, VerifyCodeResponse{
			Success: false,
			Error:   "Email and code are required",
		})
		return
	}

	normalizedEmail := strings.ToLower(strings.TrimSpace(req.Email))
	normalizedCode := strings.TrimSpace(req.Code)

	// Validate email format
	if !isValidEmail(normalizedEmail) {
		httputil.JSON(w, http.StatusBadRequest, VerifyCodeResponse{
			Success: false,
			Error:   "Invalid email format",
		})
		return
	}

	// Validate code format (6 digits)
	if len(normalizedCode) != 6 || !isNumeric(normalizedCode) {
		httputil.JSON(w, http.StatusBadRequest, VerifyCodeResponse{
			Success: false,
			Error:   "Invalid code format. Please enter a 6-digit code.",
		})
		return
	}

	// Find the most recent unverified code for this email
	codeRecord, err := h.getLatestVerificationCode(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.JSON(w, http.StatusBadRequest, VerifyCodeResponse{
				Success: false,
				Error:   "No verification code found. Please request a new code.",
				Code:    "NO_CODE",
			})
			return
		}
		h.logger.Error("failed to get verification code", "error", err)
		httputil.JSON(w, http.StatusInternalServerError, VerifyCodeResponse{
			Success: false,
			Error:   "Failed to verify code",
		})
		return
	}

	// Check if code has expired
	if time.Now().After(codeRecord.ExpiresAt) {
		// Clean up expired code
		_ = h.store.Q().DeleteEmailVerificationCodeByID(ctx, codeRecord.ID)

		httputil.JSON(w, http.StatusBadRequest, VerifyCodeResponse{
			Success: false,
			Error:   "Verification code has expired. Please request a new code.",
			Code:    "EXPIRED",
		})
		return
	}

	// Check if max attempts exceeded
	if codeRecord.Attempts >= maxAttempts {
		// Clean up code after max attempts
		_ = h.store.Q().DeleteEmailVerificationCodeByID(ctx, codeRecord.ID)

		httputil.JSON(w, http.StatusBadRequest, VerifyCodeResponse{
			Success: false,
			Error:   "Too many incorrect attempts. Please request a new code.",
			Code:    "MAX_ATTEMPTS",
		})
		return
	}

	// Check if code matches
	if codeRecord.Code != normalizedCode {
		// Increment attempts
		err = h.store.Q().IncrementEmailVerificationAttempts(ctx, codeRecord.ID)
		if err != nil {
			h.logger.Warn("failed to increment attempts", "error", err)
		}

		remainingAttempts := maxAttempts - codeRecord.Attempts - 1
		attemptWord := "attempts"
		if remainingAttempts == 1 {
			attemptWord = "attempt"
		}

		httputil.JSON(w, http.StatusBadRequest, VerifyCodeResponse{
			Success:           false,
			Error:             fmt.Sprintf("Incorrect code. %d %s remaining.", remainingAttempts, attemptWord),
			Code:              "INCORRECT",
			RemainingAttempts: remainingAttempts,
		})
		return
	}

	// Code is valid - mark as verified
	err = h.store.Q().MarkEmailVerificationCodeVerified(ctx, codeRecord.ID)
	if err != nil {
		h.logger.Error("failed to mark code as verified", "error", err)
	}

	h.logger.Info("email verified", "email", normalizedEmail)
	httputil.JSON(w, http.StatusOK, VerifyCodeResponse{
		Success:  true,
		Message:  "Email verified successfully",
		Verified: true,
	})
}

// VerificationCodeRecord represents a verification code record from the database
type VerificationCodeRecord struct {
	ID        string
	Email     string
	Code      string
	ExpiresAt time.Time
	Verified  bool
	Attempts  int
}

// countRecentVerificationCodes counts codes sent in the last hour
func (h *Handler) countRecentVerificationCodes(ctx context.Context, email string) (int, error) {
	oneHourAgo := time.Now().Add(-1 * time.Hour)

	count, err := h.store.Q().CountRecentEmailVerificationCodes(ctx, queries.CountRecentEmailVerificationCodesParams{
		Email:     email,
		CreatedAt: pgtype.Timestamp{Time: oneHourAgo, Valid: true},
	})
	return int(count), err
}

// createEmailVerificationCode creates a new verification code record
func (h *Handler) createEmailVerificationCode(ctx context.Context, email, code string, expiresAt time.Time) error {
	// Generate a unique ID
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := fmt.Sprintf("%x", idBytes)

	_, err := h.store.Q().CreateEmailVerificationCode(ctx, queries.CreateEmailVerificationCodeParams{
		ID:        id,
		Email:     email,
		Code:      code,
		ExpiresAt: pgtype.Timestamp{Time: expiresAt, Valid: true},
	})
	return err
}

// getLatestVerificationCode gets the most recent unverified code for an email
func (h *Handler) getLatestVerificationCode(ctx context.Context, email string) (*VerificationCodeRecord, error) {
	row, err := h.store.Q().GetLatestEmailVerificationCode(ctx, email)
	if err != nil {
		return nil, err
	}

	record := &VerificationCodeRecord{
		ID:       row.ID,
		Email:    row.Email,
		Code:     row.Code,
		Verified: row.Verified,
		Attempts: int(row.Attempts),
	}
	if row.ExpiresAt.Valid {
		record.ExpiresAt = row.ExpiresAt.Time
	}

	return record, nil
}

// generateVerificationCode generates a cryptographically secure 6-digit code
func generateVerificationCode() string {
	// Generate a random number between 100000 and 999999
	max := big.NewInt(900000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		// Fallback (should never happen)
		return "123456"
	}
	return fmt.Sprintf("%06d", n.Int64()+100000)
}

// isNumeric checks if a string contains only digits
func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
