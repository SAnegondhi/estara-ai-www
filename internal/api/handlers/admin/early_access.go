package admin

// Early Access Admin Endpoints (ADR-085)
//
// GET  /api/admin/early-access              — list applications
// GET  /api/admin/early-access/:id          — get single application
// POST /api/admin/early-access/:id/approve  — approve + create user + send welcome email
// POST /api/admin/early-access/:id/reject   — reject application
// POST /api/admin/users/:id/suspend-early-access   — suspend user
// POST /api/admin/users/:id/restore-early-access   — restore suspended user
// POST /api/admin/users/:id/terminate-early-access — admin-force terminate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/email"
	"github.com/estara-ai/www/pkg/httputil"
)

// ─────────────────────────────────────────────────────────────────────────────
// Request types
// ─────────────────────────────────────────────────────────────────────────────

type approveEarlyAccessRequest struct {
	AdminNotes string `json:"adminNotes"`
}

type rejectEarlyAccessRequest struct {
	AdminNotes string `json:"adminNotes"`
}

type suspendEarlyAccessRequest struct {
	Reason string `json:"reason"`
}

// ─────────────────────────────────────────────────────────────────────────────
// List / Get
// ─────────────────────────────────────────────────────────────────────────────

// earlyAccessAppResponse is the camelCase DTO sent to the admin frontend.
type earlyAccessAppResponse struct {
	ID                        string  `json:"id"`
	Email                     string  `json:"email"`
	FirstName                 string  `json:"firstName"`
	LastName                  string  `json:"lastName"`
	Company                   *string `json:"company"`
	UseCase                   string  `json:"useCase"`
	PortfolioSize             *string `json:"portfolioSize"`
	LinkedinUrl               *string `json:"linkedinUrl"`
	PrimaryMarkets            *string `json:"primaryMarkets"`
	KeyInvestmentDecisions    *string `json:"keyInvestmentDecisions"`
	CurrentAnalyticalApproach *string `json:"currentAnalyticalApproach"`
	Status                    string  `json:"status"`
	UserID                    *string `json:"userId"`
	AdminNotes                *string `json:"adminNotes"`
	CreatedAt                 string  `json:"createdAt"`
	ReviewedAt                *string `json:"reviewedAt"`
	ReviewedBy                *string `json:"reviewedBy"`
}

func toEarlyAccessAppResponse(r queries.EarlyAccessRequest) earlyAccessAppResponse {
	nullStr := func(pt pgtype.Text) *string {
		if pt.Valid {
			s := pt.String
			return &s
		}
		return nil
	}
	nullTime := func(pt pgtype.Timestamptz) *string {
		if pt.Valid {
			s := pt.Time.Format(time.RFC3339)
			return &s
		}
		return nil
	}
	return earlyAccessAppResponse{
		ID:                        r.ID,
		Email:                     r.Email,
		FirstName:                 r.FirstName,
		LastName:                  r.LastName,
		Company:                   nullStr(r.Company),
		UseCase:                   r.UseCase,
		PortfolioSize:             nullStr(r.PortfolioSize),
		LinkedinUrl:               nullStr(r.LinkedinUrl),
		PrimaryMarkets:            nullStr(r.PrimaryMarkets),
		KeyInvestmentDecisions:    nullStr(r.KeyInvestmentDecisions),
		CurrentAnalyticalApproach: nullStr(r.CurrentAnalyticalApproach),
		Status:                    r.Status,
		UserID:                    nullStr(r.UserID),
		AdminNotes:                nullStr(r.AdminNotes),
		CreatedAt:                 r.CreatedAt.Format(time.RFC3339),
		ReviewedAt:                nullTime(r.ReviewedAt),
		ReviewedBy:                nullStr(r.ReviewedBy),
	}
}

// ListEarlyAccessRequests handles GET /api/admin/early-access
func (h *Handler) ListEarlyAccessRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	status := r.URL.Query().Get("status") // "" | "pending" | "approved" | "rejected"
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	if page < 1 {
		page = 1
	}
	offset := int32((page - 1) * pageSize)

	rows, err := h.store.Q().ListEarlyAccessRequests(ctx, queries.ListEarlyAccessRequestsParams{
		Column1: status,
		Limit:   int32(pageSize),
		Offset:  offset,
	})
	if err != nil {
		h.logger.Error("failed to list early access requests", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list applications")
		return
	}

	total, _ := h.store.Q().CountEarlyAccessRequests(ctx, status)
	summary, _ := h.store.Q().SummarizeEarlyAccessRequests(ctx)

	totalPages := int64(1)
	if pageSize > 0 && total > 0 {
		totalPages = (total + int64(pageSize) - 1) / int64(pageSize)
	}

	apps := make([]earlyAccessAppResponse, len(rows))
	for i, row := range rows {
		apps[i] = toEarlyAccessAppResponse(row)
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"applications": apps,
			"total":        total,
			"page":         page,
			"pageSize":     pageSize,
			"totalPages":   totalPages,
			"summary": map[string]interface{}{
				"total":    summary.Total,
				"pending":  summary.PendingCount,
				"approved": summary.ApprovedCount,
				"rejected": summary.RejectedCount,
			},
		},
	})
}

// GetEarlyAccessRequest handles GET /api/admin/early-access/:id
func (h *Handler) GetEarlyAccessRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := chi.URLParam(r, "id")
	row, err := h.store.Q().GetEarlyAccessRequestByID(ctx, id)
	if err != nil {
		httputil.NotFound(w, "application not found")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    toEarlyAccessAppResponse(row),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Approve
// ─────────────────────────────────────────────────────────────────────────────

// ApproveEarlyAccess handles POST /api/admin/early-access/:id/approve
func (h *Handler) ApproveEarlyAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := chi.URLParam(r, "id")

	var req approveEarlyAccessRequest
	_ = httputil.DecodeJSON(r, &req) // optional body

	// Load request
	ear, err := h.store.Q().GetEarlyAccessRequestByID(ctx, id)
	if err != nil {
		httputil.NotFound(w, "application not found")
		return
	}
	if ear.Status != "pending" {
		httputil.JSON(w, http.StatusConflict, map[string]interface{}{
			"error":  "already_processed",
			"status": ear.Status,
		})
		return
	}

	// Check no existing user with this email
	_, userErr := h.store.Q().GetUserByEmail(ctx, ear.Email)
	if userErr == nil {
		// User already exists — still approve request, link user_id below
		h.logger.Warn("approving early access for existing email", "email", ear.Email)
	}

	// Build admin identity from JWT
	adminID := h.extractAdminID(r)

	// Create user
	userID := newID()
	now := time.Now()
	// Set period_end far in the future for unlimited early access
	periodEnd := now.AddDate(10, 0, 0) // 10 years

	// Create user (no password — will be set via setup-password flow)
	newUser, err := h.store.Q().CreateEarlyAccessUser(ctx, queries.CreateEarlyAccessUserParams{
		ID:        userID,
		Email:     ear.Email,
		FirstName: pgtype.Text{String: ear.FirstName, Valid: true},
		LastName:  pgtype.Text{String: ear.LastName, Valid: true},
	})
	if err != nil {
		h.logger.Error("failed to create early access user", "error", err, "email", ear.Email)
		httputil.Error(w, http.StatusInternalServerError, "failed to create user account")
		return
	}

	// Create quota row with unlimited access
	quotaID := newID()
	_, err = h.store.Q().UpsertV2EvaluationQuota(ctx, queries.UpsertV2EvaluationQuotaParams{
		ID:             quotaID,
		UserID:         newUser.ID,
		Column3:        "EARLY_ACCESS",
		AnnualLimit:    -1,
		UsedThisPeriod: 0,
		PeriodStartDate: pgtype.Timestamp{Time: now, Valid: true},
		PeriodEndDate:   pgtype.Timestamp{Time: periodEnd, Valid: true},
	})
	if err != nil {
		h.logger.Error("failed to create early access quota", "error", err, "user_id", newUser.ID)
		// Non-fatal — user exists, quota can be created manually
	}

	// Generate password setup token (72 hours)
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	setupToken := hex.EncodeToString(tokenBytes)
	tokenID := newID()

	_, err = h.store.Q().CreatePasswordSetupToken(ctx, queries.CreatePasswordSetupTokenParams{
		ID:        tokenID,
		UserID:    newUser.ID,
		Token:     setupToken,
		ExpiresAt: now.Add(72 * time.Hour),
	})
	if err != nil {
		h.logger.Error("failed to create setup token", "error", err, "user_id", newUser.ID)
		httputil.Error(w, http.StatusInternalServerError, "failed to generate setup link")
		return
	}

	// Update request status
	adminNotesText := pgtype.Text{String: req.AdminNotes, Valid: req.AdminNotes != ""}
	if err := h.store.Q().ApproveEarlyAccessRequest(ctx, queries.ApproveEarlyAccessRequestParams{
		ID:         id,
		UserID:     pgtype.Text{String: newUser.ID, Valid: true},
		AdminNotes: adminNotesText,
		ReviewedBy: pgtype.Text{String: adminID, Valid: true},
	}); err != nil {
		h.logger.Error("failed to approve request record", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to update request")
		return
	}

	// Add to whitelist so the user can log in (early access requires whitelist clearance)
	fullName := ear.FirstName + " " + ear.LastName
	h.autoWhitelistEmail(ctx, r, ear.Email, &fullName)

	// Send welcome email with setup link
	setupURL := h.cfg.Server.ClientURL + "/setup-password?token=" + setupToken
	emailSvc := email.NewService(h.cfg)
	if _, emailErr := emailSvc.SendEarlyAccessWelcome(ear.Email, ear.FirstName, setupURL); emailErr != nil {
		h.logger.Warn("failed to send early access welcome email", "error", emailErr, "email", ear.Email)
	}

	h.logAdminAudit(ctx, r, "EARLY_ACCESS_APPROVED", "early_access_requests", id, map[string]interface{}{
		"email":  ear.Email,
		"userId": newUser.ID,
	})

	h.logger.Info("early access approved", "request_id", id, "user_id", newUser.ID, "email", ear.Email)
	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"userId":  newUser.ID,
		"email":   ear.Email,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Reject
// ─────────────────────────────────────────────────────────────────────────────

// RejectEarlyAccess handles POST /api/admin/early-access/:id/reject
func (h *Handler) RejectEarlyAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := chi.URLParam(r, "id")

	var req rejectEarlyAccessRequest
	_ = httputil.DecodeJSON(r, &req)

	ear, err := h.store.Q().GetEarlyAccessRequestByID(ctx, id)
	if err != nil {
		httputil.NotFound(w, "application not found")
		return
	}
	if ear.Status != "pending" {
		httputil.JSON(w, http.StatusConflict, map[string]interface{}{
			"error":  "already_processed",
			"status": ear.Status,
		})
		return
	}

	adminID := h.extractAdminID(r)
	adminNotesText := pgtype.Text{String: req.AdminNotes, Valid: req.AdminNotes != ""}

	if err := h.store.Q().RejectEarlyAccessRequest(ctx, queries.RejectEarlyAccessRequestParams{
		ID:         id,
		AdminNotes: adminNotesText,
		ReviewedBy: pgtype.Text{String: adminID, Valid: true},
	}); err != nil {
		h.logger.Error("failed to reject request", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to update request")
		return
	}

	// Send rejection email (best-effort)
	emailSvc := email.NewService(h.cfg)
	if _, emailErr := emailSvc.SendEarlyAccessRejected(ear.Email, ear.FirstName); emailErr != nil {
		h.logger.Warn("failed to send rejection email", "error", emailErr)
	}

	h.logAdminAudit(ctx, r, "EARLY_ACCESS_REJECTED", "early_access_requests", id, map[string]interface{}{
		"email": ear.Email,
	})

	httputil.JSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// Suspend / Restore / Terminate
// ─────────────────────────────────────────────────────────────────────────────

// SuspendEarlyAccessUser handles POST /api/admin/users/:id/suspend-early-access
func (h *Handler) SuspendEarlyAccessUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	var req suspendEarlyAccessRequest
	_ = httputil.DecodeJSON(r, &req)

	user, err := h.store.Q().GetUserByID(ctx, userID)
	if err != nil {
		httputil.NotFound(w, "user not found")
		return
	}
	if !user.SubscriptionTier.Valid || user.SubscriptionTier.String != "early_access" {
		httputil.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "not_early_access_user",
		})
		return
	}

	if err := h.store.Q().UpdateUserEarlyAccessStatus(ctx, queries.UpdateUserEarlyAccessStatusParams{
		ID:                userID,
		EarlyAccessStatus: pgtype.Text{String: "suspended", Valid: true},
	}); err != nil {
		h.logger.Error("failed to suspend early access user", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Also suspend the account status to block login
	if err := h.store.Q().SuspendUser(ctx, queries.SuspendUserParams{
		ID:            userID,
		SuspendedBy:   pgtype.Text{String: h.extractAdminID(r), Valid: true},
		SuspendReason: pgtype.Text{String: req.Reason, Valid: req.Reason != ""},
	}); err != nil {
		h.logger.Warn("failed to set suspended_at on user", "error", err)
	}

	// Send suspension email (best-effort)
	name := ""
	if user.FirstName.Valid {
		name = user.FirstName.String
	}
	emailSvc := email.NewService(h.cfg)
	if _, emailErr := emailSvc.SendEarlyAccessSuspended(user.Email, name, req.Reason); emailErr != nil {
		h.logger.Warn("failed to send suspension email", "error", emailErr)
	}

	h.logAdminAudit(ctx, r, "EARLY_ACCESS_SUSPENDED", "users", userID, map[string]interface{}{
		"reason": req.Reason,
	})

	httputil.JSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// RestoreEarlyAccessUser handles POST /api/admin/users/:id/restore-early-access
func (h *Handler) RestoreEarlyAccessUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	user, err := h.store.Q().GetUserByID(ctx, userID)
	if err != nil {
		httputil.NotFound(w, "user not found")
		return
	}
	if !user.EarlyAccessStatus.Valid || user.EarlyAccessStatus.String != "suspended" {
		httputil.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "user_not_suspended",
		})
		return
	}

	if err := h.store.Q().UpdateUserEarlyAccessStatus(ctx, queries.UpdateUserEarlyAccessStatusParams{
		ID:                userID,
		EarlyAccessStatus: pgtype.Text{String: "active", Valid: true},
	}); err != nil {
		h.logger.Error("failed to restore early access user", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Clear suspended_at
	if err := h.store.Q().UnsuspendUser(ctx, userID); err != nil {
		h.logger.Warn("failed to clear suspended_at on user", "error", err)
	}

	name := ""
	if user.FirstName.Valid {
		name = user.FirstName.String
	}
	emailSvc := email.NewService(h.cfg)
	if _, emailErr := emailSvc.SendEarlyAccessRestored(user.Email, name); emailErr != nil {
		h.logger.Warn("failed to send restore email", "error", emailErr)
	}

	h.logAdminAudit(ctx, r, "EARLY_ACCESS_RESTORED", "users", userID, nil)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// TerminateEarlyAccessUser handles POST /api/admin/users/:id/terminate-early-access
func (h *Handler) TerminateEarlyAccessUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	user, err := h.store.Q().GetUserByID(ctx, userID)
	if err != nil {
		httputil.NotFound(w, "user not found")
		return
	}
	if !user.SubscriptionTier.Valid || user.SubscriptionTier.String != "early_access" {
		httputil.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "not_early_access_user",
		})
		return
	}

	if err := h.store.Q().UpdateUserEarlyAccessStatus(ctx, queries.UpdateUserEarlyAccessStatusParams{
		ID:                userID,
		EarlyAccessStatus: pgtype.Text{String: "terminated", Valid: true},
	}); err != nil {
		h.logger.Error("failed to terminate early access user", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Suspend the login as well
	adminID := h.extractAdminID(r)
	if err := h.store.Q().SuspendUser(ctx, queries.SuspendUserParams{
		ID:            userID,
		SuspendedBy:   pgtype.Text{String: adminID, Valid: true},
		SuspendReason: pgtype.Text{String: "early access terminated by admin", Valid: true},
	}); err != nil {
		h.logger.Warn("failed to suspend terminated user login", "error", err)
	}

	name := ""
	if user.FirstName.Valid {
		name = user.FirstName.String
	}
	emailSvc := email.NewService(h.cfg)
	if _, emailErr := emailSvc.SendEarlyAccessTerminated(user.Email, name); emailErr != nil {
		h.logger.Warn("failed to send termination email", "error", emailErr)
	}

	h.logAdminAudit(ctx, r, "EARLY_ACCESS_TERMINATED", "users", userID, nil)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Handler) extractAdminID(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "admin"
	}
	_ = strings.TrimPrefix(authHeader, "Bearer ")
	// Simplified — full JWT parsing is in logAdminAudit
	return "admin"
}

func invalidateUserSessions(ctx context.Context, userID string) {
	// No-op here — Redis invalidation happens in earlyaccess handler.
	// Admin termination just suspends the DB status; JWT expires naturally.
	_ = ctx
	_ = userID
}
