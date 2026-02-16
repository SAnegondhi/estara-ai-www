package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// User Management Types
// ===============================

type suspendRequest struct {
	Reason string `json:"reason" validate:"required,min=5"`
}

type createUserRequest struct {
	Email     string `json:"email" validate:"required,email"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Password  string `json:"password" validate:"required,min=8"`
	Role      string `json:"role" validate:"required,oneof=USER ADMIN"`
}

// ===============================
// Suspend / Unsuspend
// ===============================

// SuspendUser suspends a user account.
// POST /api/admin/users/{id}/suspend
func (h *Handler) SuspendUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "user ID required")
		return
	}

	var req suspendRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	q := h.store.Q()

	// Check user exists
	_, err := q.GetUserByID(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.NotFound(w, "user not found")
		} else {
			httputil.InternalError(w, fmt.Errorf("failed to get user"))
		}
		return
	}

	admin := middleware.GetUserFromContext(ctx)
	adminID := ""
	if admin != nil {
		adminID = admin.UserID
	}

	err = q.SuspendUser(ctx, queries.SuspendUserParams{
		ID:            userID,
		SuspendedBy:   pgtype.Text{String: adminID, Valid: true},
		SuspendReason: pgtype.Text{String: req.Reason, Valid: true},
	})
	if err != nil {
		h.logger.Error("suspend user failed", "error", err, "user_id", userID)
		httputil.InternalError(w, fmt.Errorf("failed to suspend user"))
		return
	}

	h.logAdminAudit(ctx, r, "USER_SUSPEND", "user", userID, map[string]any{
		"reason": req.Reason,
	})

	h.logger.Info("user suspended", "user_id", userID, "admin_id", adminID)
	httputil.Success(w, map[string]any{"suspended": true})
}

// UnsuspendUser removes a suspension from a user account.
// POST /api/admin/users/{id}/unsuspend
func (h *Handler) UnsuspendUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "user ID required")
		return
	}

	q := h.store.Q()

	// Check user exists
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.NotFound(w, "user not found")
		} else {
			httputil.InternalError(w, fmt.Errorf("failed to get user"))
		}
		return
	}

	if !user.SuspendedAt.Valid {
		httputil.BadRequest(w, "user is not suspended")
		return
	}

	err = q.UnsuspendUser(ctx, userID)
	if err != nil {
		h.logger.Error("unsuspend user failed", "error", err, "user_id", userID)
		httputil.InternalError(w, fmt.Errorf("failed to unsuspend user"))
		return
	}

	h.logAdminAudit(ctx, r, "USER_UNSUSPEND", "user", userID, map[string]any{})

	h.logger.Info("user unsuspended", "user_id", userID)
	httputil.Success(w, map[string]any{"suspended": false})
}

// ===============================
// Create User
// ===============================

// CreateUser creates a new user account.
// POST /api/admin/users
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createUserRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	q := h.store.Q()

	// Check if email already exists
	_, err := q.GetUserByEmail(ctx, req.Email)
	if err == nil {
		httputil.BadRequest(w, "email already in use")
		return
	}
	if err != pgx.ErrNoRows {
		httputil.InternalError(w, fmt.Errorf("failed to check email"))
		return
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("failed to hash password"))
		return
	}

	userID := uuid.New().String()
	_, err = q.CreateUserWithPassword(ctx, queries.CreateUserWithPasswordParams{
		ID:               userID,
		Email:            req.Email,
		Password:         pgtype.Text{String: string(hash), Valid: true},
		FirstName:        pgtype.Text{String: req.FirstName, Valid: req.FirstName != ""},
		LastName:         pgtype.Text{String: req.LastName, Valid: req.LastName != ""},
		Role:             req.Role,
		SubscriptionTier: pgtype.Text{String: "FREE", Valid: true},
		StripeCustomerId: pgtype.Text{},
	})
	if err != nil {
		h.logger.Error("create user failed", "error", err, "email", req.Email)
		httputil.InternalError(w, fmt.Errorf("failed to create user"))
		return
	}

	h.logAdminAudit(ctx, r, "USER_CREATE", "user", userID, map[string]any{
		"email": req.Email,
		"role":  req.Role,
	})

	h.logger.Info("user created", "user_id", userID, "email", req.Email)
	httputil.Created(w, map[string]any{
		"id":    userID,
		"email": req.Email,
		"role":  req.Role,
	})
}

// ===============================
// User Activity
// ===============================

// GetUserActivity returns audit log entries for a specific user.
// GET /api/admin/users/{id}/activity
func (h *Handler) GetUserActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "user ID required")
		return
	}

	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	q := h.store.Q()

	// Count audit_logs for this user
	userIDFilter := pgtype.Text{String: userID, Valid: true}
	total, err := q.CountAuditLogsFiltered(ctx, queries.CountAuditLogsFilteredParams{
		UserID:         userIDFilter,
		ActionFilter:   pgtype.Text{},
		ResourceFilter: pgtype.Text{},
	})
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("failed to count activity"))
		return
	}

	auditRows, err := q.ListAuditLogsFiltered(ctx, queries.ListAuditLogsFilteredParams{
		UserID:         userIDFilter,
		ActionFilter:   pgtype.Text{},
		ResourceFilter: pgtype.Text{},
		Offset:         int32(offset),
		Limit:          int32(pageSize),
	})
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("failed to query activity"))
		return
	}

	type activityEntry struct {
		ID          string  `json:"id"`
		EventType   string  `json:"eventType"`
		Description *string `json:"description"`
		IPAddress   *string `json:"ipAddress"`
		Timestamp   string  `json:"timestamp"`
		Success     bool    `json:"success"`
		Action      *string `json:"action"`
	}

	entries := make([]activityEntry, 0, len(auditRows))
	for _, r := range auditRows {
		e := activityEntry{
			ID:        r.ID,
			EventType: r.Event,
		}
		if r.Action != "" {
			a := r.Action
			e.Action = &a
		}
		if r.IpAddress.Valid {
			e.IPAddress = &r.IpAddress.String
		}
		if r.CreatedAt.Valid {
			e.Timestamp = r.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		}
		// Note: ListAuditLogsFiltered doesn't return description or success fields,
		// map from metadata if available
		e.Success = true // default to true for audit log entries
		entries = append(entries, e)
	}

	httputil.Success(w, map[string]any{
		"entries": entries,
		"pagination": map[string]any{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// ===============================
// Financial Profile
// ===============================

// GetUserFinancialProfile returns subscription and billing details for a user.
// GET /api/admin/users/{id}/financial-profile
func (h *Handler) GetUserFinancialProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "user ID required")
		return
	}

	q := h.store.Q()

	// Get user basic info
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.NotFound(w, "user not found")
		} else {
			httputil.InternalError(w, fmt.Errorf("failed to get user"))
		}
		return
	}

	// Get subscription info
	type subscriptionInfo struct {
		Tier      string  `json:"tier"`
		Status    string  `json:"status"`
		StartDate *string `json:"startDate"`
		EndDate   *string `json:"endDate"`
	}

	var sub subscriptionInfo
	subRow, subErr := q.GetUserSubscriptionInfo(ctx, userID)
	if subErr != nil && subErr != pgx.ErrNoRows {
		h.logger.Warn("failed to get subscription", "error", subErr, "user_id", userID)
	}
	if subErr == nil {
		sub.Tier = subRow.Tier
		sub.Status = subRow.Status
		if sd, ok := subRow.StartDate.(string); ok && sd != "" {
			sub.StartDate = &sd
		}
		if ed, ok := subRow.EndDate.(string); ok && ed != "" {
			sub.EndDate = &ed
		}
	}

	// Get IAP info
	type iapInfo struct {
		Platform *string `json:"platform"`
		Product  *string `json:"productId"`
		Expires  *string `json:"expiresAt"`
	}
	var iap iapInfo
	if user.IapPlatform.Valid {
		iap.Platform = &user.IapPlatform.String
	}
	if user.IapProductId.Valid {
		iap.Product = &user.IapProductId.String
	}
	if user.IapExpiresAt.Valid {
		t := user.IapExpiresAt.Time.Format("2006-01-02T15:04:05Z07:00")
		iap.Expires = &t
	}

	// Stripe info
	var stripeID *string
	if user.StripeCustomerId.Valid {
		stripeID = &user.StripeCustomerId.String
	}

	httputil.Success(w, map[string]any{
		"subscription":     sub,
		"iap":              iap,
		"stripeCustomerId": stripeID,
		"subscriptionTier": func() string { if user.SubscriptionTier.Valid { return user.SubscriptionTier.String }; return "FREE" }(),
	})
}

// ===============================
// GDPR
// ===============================

// ExportUserData exports all user data for GDPR compliance.
// POST /api/admin/users/{id}/export
func (h *Handler) ExportUserData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "user ID required")
		return
	}

	q := h.store.Q()

	// Get user
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.NotFound(w, "user not found")
		} else {
			httputil.InternalError(w, fmt.Errorf("failed to get user"))
		}
		return
	}

	// Get audit logs
	auditRows, err := q.ListAuditLogsFiltered(ctx, queries.ListAuditLogsFilteredParams{
		UserID:         pgtype.Text{String: userID, Valid: true},
		ActionFilter:   pgtype.Text{},
		ResourceFilter: pgtype.Text{},
		Offset:         0,
		Limit:          1000,
	})
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("failed to export audit logs"))
		return
	}

	auditLogs := make([]map[string]any, 0, len(auditRows))
	for _, r := range auditRows {
		entry := map[string]any{
			"id":        r.ID,
			"eventType": r.Event,
		}
		if r.CreatedAt.Valid {
			entry["timestamp"] = r.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		}
		if r.IpAddress.Valid {
			entry["ipAddress"] = r.IpAddress.String
		}
		auditLogs = append(auditLogs, entry)
	}

	// Build export
	export := map[string]any{
		"user": map[string]any{
			"id":    user.ID,
			"email": user.Email,
			"firstName": func() *string { if user.FirstName.Valid { return &user.FirstName.String }; return nil }(),
			"lastName":  func() *string { if user.LastName.Valid { return &user.LastName.String }; return nil }(),
			"role":      fmt.Sprintf("%v", user.Role),
			"createdAt": func() string { if user.CreatedAt.Valid { return user.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00") }; return "" }(),
		},
		"auditLogs": auditLogs,
	}

	h.logAdminAudit(ctx, r, "USER_DATA_EXPORT", "user", userID, map[string]any{
		"email": user.Email,
	})

	// Return as JSON download
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="user-export-%s.json"`, userID[:8]))
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(export)
}

// DeleteUserData permanently deletes all user data for GDPR compliance.
// DELETE /api/admin/users/{id}/data
func (h *Handler) DeleteUserData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "user ID required")
		return
	}

	q := h.store.Q()

	// Get user first (for audit log)
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.NotFound(w, "user not found")
		} else {
			httputil.InternalError(w, fmt.Errorf("failed to get user"))
		}
		return
	}

	// Delete user (cascades to related tables via FK constraints)
	err = q.DeleteUser(ctx, userID)
	if err != nil {
		h.logger.Error("delete user data failed", "error", err, "user_id", userID)
		httputil.InternalError(w, fmt.Errorf("failed to delete user data"))
		return
	}

	h.logAdminAudit(ctx, r, "USER_DATA_DELETE", "user", userID, map[string]any{
		"email": user.Email,
	})

	h.logger.Info("user data deleted (GDPR)", "user_id", userID, "email", user.Email)
	httputil.Success(w, map[string]any{"deleted": true})
}
