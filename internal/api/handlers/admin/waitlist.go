package admin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/estara-ai/www/pkg/httputil"
)

// EarlyAccessEntry represents a waitlist entry for admin view
type EarlyAccessEntry struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       *string    `json:"name,omitempty"`
	Source     *string    `json:"source,omitempty"`
	IPAddress  *string    `json:"ipAddress,omitempty"`
	InvitedAt  *time.Time `json:"invitedAt,omitempty"`
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// WaitlistSummary represents aggregate waitlist statistics
type WaitlistSummary struct {
	Total    int64 `json:"total"`
	Pending  int64 `json:"pending"`
	Invited  int64 `json:"invited"`
	Accepted int64 `json:"accepted"`
}

// ===============================
// Waitlist (Early Access) Management
// ===============================

// ListWaitlist returns a paginated list of early access signups
func (h *Handler) ListWaitlist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	filter := r.URL.Query().Get("filter") // pending, invited, accepted, all
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

// InviteWaitlistEntry marks a waitlist entry as invited
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

	h.logAdminAudit(ctx, r, "WAITLIST_INVITE", "early_access", id, map[string]interface{}{
		"email": entry.Email,
	})
	httputil.Success(w, entry)
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
		whereClause += ` AND "invitedAt" IS NULL`
	case "invited":
		whereClause += ` AND "invitedAt" IS NOT NULL AND "acceptedAt" IS NULL`
	case "accepted":
		whereClause += ` AND "acceptedAt" IS NOT NULL`
	}

	if search != "" {
		pattern := "%" + strings.ToLower(search) + "%"
		whereClause += fmt.Sprintf(` AND (LOWER(email) LIKE $%d OR LOWER(COALESCE(name, '')) LIKE $%d)`, argIndex, argIndex)
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
		SELECT id, email, name, source, "ipAddress", "invitedAt", "acceptedAt", "createdAt"
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
		err := rows.Scan(&e.ID, &e.Email, &e.Name, &e.Source, &e.IPAddress, &e.InvitedAt, &e.AcceptedAt, &e.CreatedAt)
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
		SELECT id, email, name, source, "ipAddress", "invitedAt", "acceptedAt", "createdAt"
		FROM early_access WHERE id = $1
	`, id).Scan(&e.ID, &e.Email, &e.Name, &e.Source, &e.IPAddress, &e.InvitedAt, &e.AcceptedAt, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (h *Handler) inviteWaitlistEntry(ctx context.Context, id string) (*EarlyAccessEntry, error) {
	var e EarlyAccessEntry
	err := h.db.Main.QueryRow(ctx, `
		UPDATE early_access SET "invitedAt" = NOW()
		WHERE id = $1
		RETURNING id, email, name, source, "ipAddress", "invitedAt", "acceptedAt", "createdAt"
	`, id).Scan(&e.ID, &e.Email, &e.Name, &e.Source, &e.IPAddress, &e.InvitedAt, &e.AcceptedAt, &e.CreatedAt)
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
			COUNT(*) FILTER (WHERE "invitedAt" IS NULL) as pending,
			COUNT(*) FILTER (WHERE "invitedAt" IS NOT NULL AND "acceptedAt" IS NULL) as invited,
			COUNT(*) FILTER (WHERE "acceptedAt" IS NOT NULL) as accepted
		FROM early_access
	`).Scan(&s.Total, &s.Pending, &s.Invited, &s.Accepted)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
