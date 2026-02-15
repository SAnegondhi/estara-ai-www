package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/estara-ai/www/internal/services/email"
	"github.com/estara-ai/www/pkg/httputil"
)

// EarlyAccessEntry represents a waitlist entry for admin view
type EarlyAccessEntry struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Name        *string    `json:"name,omitempty"`
	Source      *string    `json:"source,omitempty"`
	Status      string     `json:"status"` // PENDING, INVITED, REVOKED
	Invited     bool       `json:"invited"`
	Contacted   bool       `json:"contacted"`
	Notes       *string    `json:"notes,omitempty"`
	RequestedAt *time.Time `json:"requestedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// WaitlistSummary represents aggregate waitlist statistics
type WaitlistSummary struct {
	Total   int64 `json:"total"`
	Pending int64 `json:"pending"`
	Invited int64 `json:"invited"`
	Revoked int64 `json:"revoked"`
}

// ===============================
// Waitlist (Early Access) Management
// ===============================

// ListWaitlist returns a paginated list of early access signups
func (h *Handler) ListWaitlist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	filter := r.URL.Query().Get("filter") // pending, invited, revoked, all
	search := r.URL.Query().Get("search")

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	entries, total, err := h.listWaitlist(ctx, filter, search, pageSize, offset)
	if err != nil {
		h.logger.Error("failed to list waitlist", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list waitlist")
		return
	}

	summary, _ := h.getWaitlistSummary(ctx)

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

// GetWaitlistEntry returns a specific waitlist entry
func (h *Handler) GetWaitlistEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	entry, err := h.getWaitlistByID(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "entry not found")
			return
		}
		h.logger.Error("failed to get waitlist entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get waitlist entry")
		return
	}

	httputil.Success(w, entry)
}

// InviteWaitlistEntry marks a waitlist entry as invited, sends an invite email,
// and auto-adds the email to the whitelist.
func (h *Handler) InviteWaitlistEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	entry, err := h.inviteWaitlistEntry(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "entry not found")
			return
		}
		h.logger.Error("failed to invite waitlist entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to invite waitlist entry")
		return
	}

	// Auto-add to whitelist (pass name from metadata if available)
	h.autoWhitelistEmail(ctx, r, entry.Email, entry.Name)

	// Send invite email synchronously so admin gets feedback
	emailSent, emailError := h.sendWaitlistInviteEmail(entry.Email, entry.Name)

	h.logAdminAudit(ctx, r, "WAITLIST_INVITE", "early_access", id, map[string]interface{}{
		"email": entry.Email,
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

// UpdateWaitlistEntryStatus changes the status of a waitlist entry (e.g. REVOKED → PENDING)
func (h *Handler) UpdateWaitlistEntryStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	newStatus := strings.ToUpper(req.Status)
	if newStatus != "PENDING" && newStatus != "INVITED" && newStatus != "REVOKED" {
		httputil.BadRequest(w, "status must be PENDING, INVITED, or REVOKED")
		return
	}

	invited := newStatus == "INVITED"
	var e EarlyAccessEntry
	err := h.db.Main.QueryRow(ctx, `
		UPDATE early_access SET status = $2, invited = $3, "updatedAt" = NOW()
		WHERE id = $1
		RETURNING id, email, metadata->>'name', source, status, invited, contacted, notes, "requestedAt", "createdAt", "updatedAt"
	`, id, newStatus, invited).Scan(
		&e.ID, &e.Email, &e.Name, &e.Source, &e.Status, &e.Invited, &e.Contacted, &e.Notes, &e.RequestedAt, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "entry not found")
			return
		}
		h.logger.Error("failed to update waitlist entry status", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to update waitlist entry status")
		return
	}

	h.logAdminAudit(ctx, r, "WAITLIST_STATUS_UPDATE", "early_access", id, map[string]interface{}{
		"status": newStatus,
	})
	httputil.Success(w, e)
}

// DeleteWaitlistEntry removes a waitlist entry
func (h *Handler) DeleteWaitlistEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	result, err := h.db.Main.Exec(ctx, `DELETE FROM early_access WHERE id = $1`, id)
	if err != nil {
		h.logger.Error("failed to delete waitlist entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete waitlist entry")
		return
	}
	if result.RowsAffected() == 0 {
		httputil.Error(w, http.StatusNotFound, "entry not found")
		return
	}

	h.logAdminAudit(ctx, r, "WAITLIST_DELETE", "early_access", id, nil)
	httputil.Success(w, map[string]interface{}{"deleted": true})
}

// ===============================
// Waitlist Helper Methods
// ===============================

func (h *Handler) listWaitlist(ctx context.Context, filter, search string, limit, offset int) ([]EarlyAccessEntry, int64, error) {
	whereClause := "1=1"
	args := make([]interface{}, 0)
	argIndex := 1

	switch filter {
	case "pending":
		whereClause += ` AND status = 'PENDING'`
	case "invited":
		whereClause += ` AND status = 'INVITED'`
	case "revoked":
		whereClause += ` AND status = 'REVOKED'`
	}

	if search != "" {
		pattern := "%" + strings.ToLower(search) + "%"
		whereClause += fmt.Sprintf(` AND (LOWER(email) LIKE $%d OR LOWER(COALESCE(metadata->>'name', '')) LIKE $%d)`, argIndex, argIndex)
		args = append(args, pattern)
		argIndex++
	}

	var total int64
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM early_access WHERE %s`, whereClause)
	err := h.db.Main.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT id, email, metadata->>'name', source, status, invited, contacted, notes, "requestedAt", "createdAt", "updatedAt"
		FROM early_access
		WHERE %s
		ORDER BY "createdAt" DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	rows, err := h.db.Main.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	entries := []EarlyAccessEntry{}
	for rows.Next() {
		var e EarlyAccessEntry
		err := rows.Scan(&e.ID, &e.Email, &e.Name, &e.Source, &e.Status, &e.Invited, &e.Contacted, &e.Notes, &e.RequestedAt, &e.CreatedAt, &e.UpdatedAt)
		if err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, nil
}

func (h *Handler) getWaitlistByID(ctx context.Context, id string) (*EarlyAccessEntry, error) {
	var e EarlyAccessEntry
	err := h.db.Main.QueryRow(ctx, `
		SELECT id, email, metadata->>'name', source, status, invited, contacted, notes, "requestedAt", "createdAt", "updatedAt"
		FROM early_access WHERE id = $1
	`, id).Scan(&e.ID, &e.Email, &e.Name, &e.Source, &e.Status, &e.Invited, &e.Contacted, &e.Notes, &e.RequestedAt, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (h *Handler) inviteWaitlistEntry(ctx context.Context, id string) (*EarlyAccessEntry, error) {
	var e EarlyAccessEntry
	err := h.db.Main.QueryRow(ctx, `
		UPDATE early_access SET invited = true, status = 'INVITED', "updatedAt" = NOW()
		WHERE id = $1
		RETURNING id, email, metadata->>'name', source, status, invited, contacted, notes, "requestedAt", "createdAt", "updatedAt"
	`, id).Scan(&e.ID, &e.Email, &e.Name, &e.Source, &e.Status, &e.Invited, &e.Contacted, &e.Notes, &e.RequestedAt, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (h *Handler) getWaitlistSummary(ctx context.Context) (*WaitlistSummary, error) {
	var s WaitlistSummary
	err := h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'PENDING') as pending,
			COUNT(*) FILTER (WHERE status = 'INVITED') as invited,
			COUNT(*) FILTER (WHERE status = 'REVOKED') as revoked
		FROM early_access
	`).Scan(&s.Total, &s.Pending, &s.Invited, &s.Revoked)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// revokeWaitlistByEmail sets the waitlist entry for an email to REVOKED status.
// Called when a whitelist entry is deleted.
func (h *Handler) revokeWaitlistByEmail(ctx context.Context, emailAddr string) {
	emailAddr = strings.ToLower(strings.TrimSpace(emailAddr))
	_, err := h.db.Main.Exec(ctx, `
		UPDATE early_access SET status = 'REVOKED', invited = false, "updatedAt" = NOW()
		WHERE LOWER(email) = $1
	`, emailAddr)
	if err != nil {
		h.logger.Warn("failed to revoke waitlist entry for deleted whitelist email", "email", emailAddr, "error", err)
		return
	}
	h.logger.Info("revoked waitlist entry for deleted whitelist email", "email", emailAddr)
}

// autoWhitelistEmail adds an email to the whitelisted_emails table if not already present.
func (h *Handler) autoWhitelistEmail(ctx context.Context, r *http.Request, emailAddr string, name *string) {
	emailAddr = strings.ToLower(strings.TrimSpace(emailAddr))

	// Check if already whitelisted
	_, err := h.getWhitelistByEmail(ctx, emailAddr)
	if err == nil {
		return // already whitelisted
	}

	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	adminEmail := h.getAdminEmailFromRequest(r)
	reason := "Auto-whitelisted from waitlist invite"

	_, err = h.createWhitelistEntry(ctx, id, emailAddr, name, "EMAIL", adminEmail, &reason)
	if err != nil {
		h.logger.Warn("failed to auto-whitelist email from waitlist invite", "email", emailAddr, "error", err)
		return
	}

	// Invalidate whitelist cache
	if h.whitelist != nil {
		h.whitelist.InvalidateCache()
	}

	h.logger.Info("auto-whitelisted email from waitlist invite", "email", emailAddr)
}

// sendWaitlistInviteEmail sends an invite email to a waitlisted user.
// Returns (sent bool, errorMsg string).
func (h *Handler) sendWaitlistInviteEmail(emailAddr string, name *string) (bool, string) {
	emailSvc := email.NewService(h.cfg)

	firstName := ""
	if name != nil && *name != "" {
		firstName = *name
	}

	result, err := emailSvc.SendWhitelistInvite(emailAddr, firstName)
	if err != nil {
		h.logger.Error("failed to send waitlist invite email", "email", emailAddr, "error", err)
		return false, err.Error()
	}
	if !result.Success {
		h.logger.Warn("waitlist invite email not sent", "email", emailAddr, "error", result.Error)
		return false, result.Error
	}
	h.logger.Info("waitlist invite email sent", "email", emailAddr)
	return true, ""
}
