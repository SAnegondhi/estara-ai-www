package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/email"
	"github.com/estara-ai/www/pkg/httputil"
)

// WhitelistedEmail represents a whitelisted email/domain entry
type WhitelistedEmail struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      *string   `json:"name,omitempty"`
	Type      string    `json:"type"` // EMAIL or DOMAIN
	AddedBy   *string   `json:"addedBy,omitempty"`
	Reason    *string   `json:"reason,omitempty"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CreateWhitelistRequest represents a request to add a whitelist entry
type CreateWhitelistRequest struct {
	Email  string  `json:"email" validate:"required"`
	Name   *string `json:"name,omitempty"`
	Type   string  `json:"type" validate:"required,oneof=EMAIL DOMAIN"`
	Reason *string `json:"reason,omitempty"`
}

// UpdateWhitelistRequest represents a request to update a whitelist entry
type UpdateWhitelistRequest struct {
	Name   *string `json:"name,omitempty"`
	Reason *string `json:"reason,omitempty"`
}

// WhitelistSummary represents aggregate whitelist statistics
type WhitelistSummary struct {
	Total       int64 `json:"total"`
	Active      int64 `json:"active"`
	EmailCount  int64 `json:"emailCount"`
	DomainCount int64 `json:"domainCount"`
}

// ===============================
// Whitelist Management
// ===============================

// ListWhitelist returns a paginated list of whitelisted emails/domains
func (h *Handler) ListWhitelist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	search := r.URL.Query().Get("search")

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	var entries []WhitelistedEmail
	var total int64
	var err error

	if search != "" {
		entries, total, err = h.searchWhitelist(ctx, search, pageSize, offset)
	} else {
		entries, total, err = h.listWhitelist(ctx, pageSize, offset)
	}

	if err != nil {
		h.logger.Error("failed to list whitelist", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list whitelist")
		return
	}

	// Get summary
	summary, _ := h.getWhitelistSummary(ctx)

	httputil.Success(w, map[string]interface{}{
		"entries": entries,
		"summary": summary,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// GetWhitelistEntry returns a specific whitelist entry
func (h *Handler) GetWhitelistEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	entry, err := h.getWhitelistByID(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "entry not found")
			return
		}
		h.logger.Error("failed to get whitelist entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get whitelist entry")
		return
	}

	httputil.Success(w, entry)
}

// CreateWhitelistEntry adds a new email/domain to the whitelist
func (h *Handler) CreateWhitelistEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req CreateWhitelistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	// Normalize email
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	// Check for duplicate
	_, err := h.getWhitelistByEmail(ctx, req.Email)
	if err == nil {
		httputil.JSON(w, http.StatusConflict, map[string]interface{}{
			"error": "entry already exists",
			"code":  "DUPLICATE",
		})
		return
	}

	// Generate ID
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	// Get admin email from context
	adminEmail := h.getAdminEmailFromRequest(r)

	entry, err := h.createWhitelistEntry(ctx, id, req.Email, req.Name, req.Type, adminEmail, req.Reason)
	if err != nil {
		h.logger.Error("failed to create whitelist entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to create whitelist entry")
		return
	}

	// Invalidate whitelist cache
	if h.whitelist != nil {
		h.whitelist.InvalidateCache()
	}

	// Send invite email for EMAIL type entries (not domains)
	var emailSent bool
	var emailError string
	if req.Type == "EMAIL" {
		emailSent, emailError = h.sendWhitelistInviteEmail(req.Email, req.Name)
	}

	h.logAdminAudit(ctx, r, "WHITELIST_CREATE", "whitelist", id, map[string]interface{}{
		"email": req.Email,
		"type":  req.Type,
	})
	h.logger.Info("whitelist entry created", "email", req.Email, "type", req.Type)

	resp := map[string]interface{}{
		"success":   true,
		"data":      entry,
		"emailSent": emailSent,
	}
	if emailError != "" {
		resp["emailError"] = emailError
	}
	httputil.JSON(w, http.StatusCreated, resp)
}

// UpdateWhitelistEntry updates a whitelist entry
func (h *Handler) UpdateWhitelistEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req UpdateWhitelistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	entry, err := h.updateWhitelistEntry(ctx, id, req.Name, req.Reason)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "entry not found")
			return
		}
		h.logger.Error("failed to update whitelist entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to update whitelist entry")
		return
	}

	h.logAdminAudit(ctx, r, "WHITELIST_UPDATE", "whitelist", id, nil)
	httputil.Success(w, entry)
}

// ToggleWhitelistEntry enables or disables a whitelist entry
func (h *Handler) ToggleWhitelistEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	entry, err := h.toggleWhitelistEntry(ctx, id, req.Active)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "entry not found")
			return
		}
		h.logger.Error("failed to toggle whitelist entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to toggle whitelist entry")
		return
	}

	// Invalidate whitelist cache
	if h.whitelist != nil {
		h.whitelist.InvalidateCache()
	}

	// Send notification email for EMAIL type entries
	var emailSent bool
	var emailError string
	if entry.Type == "EMAIL" {
		emailSent, emailError = h.sendWhitelistToggleEmail(entry.Email, entry.Name, req.Active)
	}

	h.logAdminAudit(ctx, r, "WHITELIST_TOGGLE", "whitelist", id, map[string]interface{}{
		"active": req.Active,
	})

	resp := map[string]interface{}{
		"success":   true,
		"data":      entry,
		"emailSent": emailSent,
	}
	if emailError != "" {
		resp["emailError"] = emailError
	}
	httputil.JSON(w, http.StatusOK, resp)
}

// DeleteWhitelistEntry removes a whitelist entry
func (h *Handler) DeleteWhitelistEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	// Get entry before deleting for audit
	entry, err := h.getWhitelistByID(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "entry not found")
			return
		}
		h.logger.Error("failed to get whitelist entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete whitelist entry")
		return
	}

	if err := h.deleteWhitelistEntry(ctx, id); err != nil {
		h.logger.Error("failed to delete whitelist entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete whitelist entry")
		return
	}

	// Invalidate whitelist cache
	if h.whitelist != nil {
		h.whitelist.InvalidateCache()
	}

	// Cascade: revoke the corresponding waitlist entry if one exists
	if entry.Type == "EMAIL" {
		h.revokeWaitlistByEmail(ctx, entry.Email)
	}

	// Send removal notification for EMAIL type entries
	var emailSent bool
	var emailError string
	if entry.Type == "EMAIL" {
		emailSent, emailError = h.sendWhitelistRemovedEmail(entry.Email, entry.Name)
	}

	h.logAdminAudit(ctx, r, "WHITELIST_DELETE", "whitelist", id, map[string]interface{}{
		"email": entry.Email,
	})

	resp := map[string]interface{}{
		"success":   true,
		"deleted":   true,
		"emailSent": emailSent,
	}
	if emailError != "" {
		resp["emailError"] = emailError
	}
	httputil.JSON(w, http.StatusOK, resp)
}

// ===============================
// Whitelist Helper Methods
// ===============================

// mapWhitelistedEmailRow converts a sqlc row type to the handler's WhitelistedEmail type
func mapWhitelistedEmailRow(id, emailAddr string, name, addedBy, reason pgtype.Text, typ string, active bool, createdAt, updatedAt pgtype.Timestamp) WhitelistedEmail {
	e := WhitelistedEmail{
		ID:     id,
		Email:  emailAddr,
		Type:   typ,
		Active: active,
	}
	if name.Valid {
		e.Name = &name.String
	}
	if addedBy.Valid {
		e.AddedBy = &addedBy.String
	}
	if reason.Valid {
		e.Reason = &reason.String
	}
	if createdAt.Valid {
		e.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		e.UpdatedAt = updatedAt.Time
	}
	return e
}

func (h *Handler) listWhitelist(ctx context.Context, limit, offset int) ([]WhitelistedEmail, int64, error) {
	total, err := h.store.Q().CountWhitelistedEmails(ctx)
	if err != nil {
		return nil, 0, err
	}

	rows, err := h.store.Q().ListWhitelistedEmails(ctx, queries.ListWhitelistedEmailsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, 0, err
	}

	entries := []WhitelistedEmail{}
	for _, r := range rows {
		entries = append(entries, mapWhitelistedEmailRow(r.ID, r.Email, r.Name, r.AddedBy, r.Reason, r.Type, r.Active, r.CreatedAt, r.UpdatedAt))
	}
	return entries, total, nil
}

func (h *Handler) searchWhitelist(ctx context.Context, search string, limit, offset int) ([]WhitelistedEmail, int64, error) {
	pattern := "%" + strings.ToLower(search) + "%"

	total, err := h.store.Q().CountSearchWhitelistedEmails(ctx, pattern)
	if err != nil {
		return nil, 0, err
	}

	rows, err := h.store.Q().SearchWhitelistedEmails(ctx, queries.SearchWhitelistedEmailsParams{
		Email:  pattern,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, 0, err
	}

	entries := []WhitelistedEmail{}
	for _, r := range rows {
		entries = append(entries, mapWhitelistedEmailRow(r.ID, r.Email, r.Name, r.AddedBy, r.Reason, r.Type, r.Active, r.CreatedAt, r.UpdatedAt))
	}
	return entries, total, nil
}

func (h *Handler) getWhitelistByID(ctx context.Context, id string) (*WhitelistedEmail, error) {
	row, err := h.store.Q().GetWhitelistedEmailByID(ctx, id)
	if err != nil {
		return nil, err
	}
	e := mapWhitelistedEmailRow(row.ID, row.Email, row.Name, row.AddedBy, row.Reason, row.Type, row.Active, row.CreatedAt, row.UpdatedAt)
	return &e, nil
}

func (h *Handler) getWhitelistByEmail(ctx context.Context, emailAddr string) (*WhitelistedEmail, error) {
	row, err := h.store.Q().GetWhitelistedEmailByEmail(ctx, emailAddr)
	if err != nil {
		return nil, err
	}
	e := mapWhitelistedEmailRow(row.ID, row.Email, row.Name, row.AddedBy, row.Reason, row.Type, row.Active, row.CreatedAt, row.UpdatedAt)
	return &e, nil
}

func (h *Handler) createWhitelistEntry(ctx context.Context, id, emailAddr string, name *string, entryType, addedBy string, reason *string) (*WhitelistedEmail, error) {
	params := queries.CreateWhitelistedEmailParams{
		ID:      id,
		Email:   emailAddr,
		Column4: entryType,
	}
	if name != nil {
		params.Name = pgtype.Text{String: *name, Valid: true}
	}
	params.AddedBy = pgtype.Text{String: addedBy, Valid: true}
	if reason != nil {
		params.Reason = pgtype.Text{String: *reason, Valid: true}
	}

	row, err := h.store.Q().CreateWhitelistedEmail(ctx, params)
	if err != nil {
		return nil, err
	}
	e := mapWhitelistedEmailRow(row.ID, row.Email, row.Name, row.AddedBy, row.Reason, row.Type, row.Active, row.CreatedAt, row.UpdatedAt)
	return &e, nil
}

func (h *Handler) updateWhitelistEntry(ctx context.Context, id string, name, reason *string) (*WhitelistedEmail, error) {
	params := queries.UpdateWhitelistedEmailParams{
		ID: id,
	}
	if name != nil {
		params.Name = pgtype.Text{String: *name, Valid: true}
	}
	if reason != nil {
		params.Reason = pgtype.Text{String: *reason, Valid: true}
	}

	row, err := h.store.Q().UpdateWhitelistedEmail(ctx, params)
	if err != nil {
		return nil, err
	}
	e := mapWhitelistedEmailRow(row.ID, row.Email, row.Name, row.AddedBy, row.Reason, row.Type, row.Active, row.CreatedAt, row.UpdatedAt)
	return &e, nil
}

func (h *Handler) toggleWhitelistEntry(ctx context.Context, id string, active bool) (*WhitelistedEmail, error) {
	row, err := h.store.Q().ToggleWhitelistedEmail(ctx, queries.ToggleWhitelistedEmailParams{
		ID:     id,
		Active: active,
	})
	if err != nil {
		return nil, err
	}
	e := mapWhitelistedEmailRow(row.ID, row.Email, row.Name, row.AddedBy, row.Reason, row.Type, row.Active, row.CreatedAt, row.UpdatedAt)
	return &e, nil
}

func (h *Handler) deleteWhitelistEntry(ctx context.Context, id string) error {
	return h.store.Q().DeleteWhitelistedEmail(ctx, id)
}

func (h *Handler) getWhitelistSummary(ctx context.Context) (*WhitelistSummary, error) {
	row, err := h.store.Q().GetWhitelistSummary(ctx)
	if err != nil {
		return nil, err
	}
	return &WhitelistSummary{
		Total:       row.Total,
		Active:      row.Active,
		EmailCount:  row.EmailCount,
		DomainCount: row.DomainCount,
	}, nil
}

func (h *Handler) getAdminEmailFromRequest(r *http.Request) string {
	// Extract admin email from JWT claims — token already validated by middleware
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		if c, err := r.Cookie("access_token"); err == nil {
			authHeader = "Bearer " + c.Value
		}
	}
	if authHeader == "" {
		return "admin"
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	claims := jwt.MapClaims{}
	_, _, _ = parser.ParseUnverified(tokenStr, claims)
	if emailVal, ok := claims["email"].(string); ok {
		return emailVal
	}
	return "admin"
}

// sendWhitelistInviteEmail sends an invite email when an admin whitelists an email address.
// Returns (sent bool, errorMsg string).
func (h *Handler) sendWhitelistInviteEmail(emailAddr string, name *string) (bool, string) {
	emailSvc := email.NewService(h.cfg)

	firstName := ""
	if name != nil && *name != "" {
		firstName = *name
	}

	result, err := emailSvc.SendWhitelistInvite(emailAddr, firstName)
	if err != nil {
		h.logger.Error("failed to send whitelist invite email", "email", emailAddr, "error", err)
		return false, err.Error()
	}
	if !result.Success {
		h.logger.Warn("whitelist invite email not sent", "email", emailAddr, "error", result.Error)
		return false, result.Error
	}
	h.logger.Info("whitelist invite email sent", "email", emailAddr)
	return true, ""
}

// sendWhitelistToggleEmail sends a suspend or restore notification.
// Returns (sent bool, errorMsg string).
func (h *Handler) sendWhitelistToggleEmail(emailAddr string, name *string, active bool) (bool, string) {
	emailSvc := email.NewService(h.cfg)

	firstName := ""
	if name != nil && *name != "" {
		firstName = *name
	}

	var result *email.Result
	var err error
	if active {
		result, err = emailSvc.SendWhitelistRestored(emailAddr, firstName)
	} else {
		result, err = emailSvc.SendWhitelistSuspended(emailAddr, firstName)
	}

	action := "suspended"
	if active {
		action = "restored"
	}

	if err != nil {
		h.logger.Error("failed to send whitelist "+action+" email", "email", emailAddr, "error", err)
		return false, err.Error()
	}
	if !result.Success {
		h.logger.Warn("whitelist "+action+" email not sent", "email", emailAddr, "error", result.Error)
		return false, result.Error
	}
	h.logger.Info("whitelist "+action+" email sent", "email", emailAddr)
	return true, ""
}

// sendWhitelistRemovedEmail sends a removal notification.
// Returns (sent bool, errorMsg string).
func (h *Handler) sendWhitelistRemovedEmail(emailAddr string, name *string) (bool, string) {
	emailSvc := email.NewService(h.cfg)

	firstName := ""
	if name != nil && *name != "" {
		firstName = *name
	}

	result, err := emailSvc.SendWhitelistRemoved(emailAddr, firstName)
	if err != nil {
		h.logger.Error("failed to send whitelist removed email", "email", emailAddr, "error", err)
		return false, err.Error()
	}
	if !result.Success {
		h.logger.Warn("whitelist removed email not sent", "email", emailAddr, "error", result.Error)
		return false, result.Error
	}
	h.logger.Info("whitelist removed email sent", "email", emailAddr)
	return true, ""
}
