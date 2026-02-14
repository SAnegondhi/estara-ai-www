package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/estara-ai/www/pkg/httputil"
)

// ContactSubmission represents a contact form submission for admin view
type ContactSubmission struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	Phone       *string    `json:"phone,omitempty"`
	Subject     *string    `json:"subject,omitempty"`
	Message     string     `json:"message"`
	Source      *string    `json:"source,omitempty"`
	IPAddress   *string    `json:"ipAddress,omitempty"`
	UserAgent   *string    `json:"userAgent,omitempty"`
	Status      string     `json:"status"`
	Notes       *string    `json:"notes,omitempty"`
	RespondedAt *time.Time `json:"respondedAt,omitempty"`
	RespondedBy *string    `json:"respondedBy,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// UpdateContactRequest represents a request to update a contact submission
type UpdateContactRequest struct {
	Status *string `json:"status,omitempty" validate:"omitempty,oneof=NEW IN_PROGRESS RESPONDED RESOLVED CLOSED"`
	Notes  *string `json:"notes,omitempty"`
}

// ContactSummary represents aggregate contact statistics
type ContactSummary struct {
	Total       int64 `json:"total"`
	New         int64 `json:"new"`
	InProgress  int64 `json:"inProgress"`
	Responded   int64 `json:"responded"`
	Resolved    int64 `json:"resolved"`
	Closed      int64 `json:"closed"`
}

// ===============================
// Contact Management
// ===============================

// ListContacts returns a paginated list of contact submissions
func (h *Handler) ListContacts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	status := r.URL.Query().Get("status")
	search := r.URL.Query().Get("search")

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	contacts, total, err := h.listContacts(ctx, status, search, pageSize, offset)
	if err != nil {
		h.logger.Error("failed to list contacts", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list contacts")
		return
	}

	summary, _ := h.getContactSummary(ctx)

	httputil.Success(w, map[string]interface{}{
		"contacts": contacts,
		"summary":  summary,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// GetContact returns a specific contact submission
func (h *Handler) GetContact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	contact, err := h.getContactByID(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "contact not found")
			return
		}
		h.logger.Error("failed to get contact", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get contact")
		return
	}

	httputil.Success(w, contact)
}

// UpdateContact updates a contact submission status/notes
func (h *Handler) UpdateContact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req UpdateContactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	adminEmail := "admin"

	contact, err := h.updateContact(ctx, id, req.Status, req.Notes, adminEmail)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "contact not found")
			return
		}
		h.logger.Error("failed to update contact", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to update contact")
		return
	}

	h.logAdminAudit(ctx, r, "CONTACT_UPDATE", "contact", id, map[string]interface{}{
		"status": req.Status,
	})
	httputil.Success(w, contact)
}

// DeleteContact removes a contact submission
func (h *Handler) DeleteContact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	result, err := h.db.Main.Exec(ctx, `DELETE FROM contact_submissions WHERE id = $1`, id)
	if err != nil {
		h.logger.Error("failed to delete contact", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete contact")
		return
	}
	if result.RowsAffected() == 0 {
		httputil.Error(w, http.StatusNotFound, "contact not found")
		return
	}

	h.logAdminAudit(ctx, r, "CONTACT_DELETE", "contact", id, nil)
	httputil.Success(w, map[string]interface{}{"deleted": true})
}

// ===============================
// Contact Helper Methods
// ===============================

func (h *Handler) listContacts(ctx context.Context, status, search string, limit, offset int) ([]ContactSubmission, int64, error) {
	whereClause := "1=1"
	args := make([]interface{}, 0)
	argIndex := 1

	if status != "" {
		whereClause += fmt.Sprintf(` AND status = $%d`, argIndex)
		args = append(args, status)
		argIndex++
	}
	if search != "" {
		pattern := "%" + strings.ToLower(search) + "%"
		whereClause += fmt.Sprintf(` AND (LOWER(email) LIKE $%d OR LOWER(name) LIKE $%d OR LOWER(COALESCE(subject, '')) LIKE $%d)`, argIndex, argIndex, argIndex)
		args = append(args, pattern)
		argIndex++
	}

	var total int64
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM contact_submissions WHERE %s`, whereClause)
	err := h.db.Main.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT id, name, email, phone, subject, message, source, "ipAddress", "userAgent",
		       status, notes, "respondedAt", "respondedBy", "createdAt", "updatedAt"
		FROM contact_submissions
		WHERE %s
		ORDER BY "createdAt" DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	rows, err := h.db.Main.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	contacts := []ContactSubmission{}
	for rows.Next() {
		var c ContactSubmission
		err := rows.Scan(
			&c.ID, &c.Name, &c.Email, &c.Phone, &c.Subject, &c.Message,
			&c.Source, &c.IPAddress, &c.UserAgent, &c.Status, &c.Notes,
			&c.RespondedAt, &c.RespondedBy, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		contacts = append(contacts, c)
	}
	return contacts, total, nil
}

func (h *Handler) getContactByID(ctx context.Context, id string) (*ContactSubmission, error) {
	var c ContactSubmission
	err := h.db.Main.QueryRow(ctx, `
		SELECT id, name, email, phone, subject, message, source, "ipAddress", "userAgent",
		       status, notes, "respondedAt", "respondedBy", "createdAt", "updatedAt"
		FROM contact_submissions WHERE id = $1
	`, id).Scan(
		&c.ID, &c.Name, &c.Email, &c.Phone, &c.Subject, &c.Message,
		&c.Source, &c.IPAddress, &c.UserAgent, &c.Status, &c.Notes,
		&c.RespondedAt, &c.RespondedBy, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (h *Handler) updateContact(ctx context.Context, id string, status, notes *string, respondedBy string) (*ContactSubmission, error) {
	var c ContactSubmission
	err := h.db.Main.QueryRow(ctx, `
		UPDATE contact_submissions SET
			status = COALESCE($2, status),
			notes = COALESCE($3, notes),
			"respondedAt" = CASE WHEN $2 = 'RESPONDED' THEN NOW() ELSE "respondedAt" END,
			"respondedBy" = CASE WHEN $2 = 'RESPONDED' THEN $4 ELSE "respondedBy" END,
			"updatedAt" = NOW()
		WHERE id = $1
		RETURNING id, name, email, phone, subject, message, source, "ipAddress", "userAgent",
		          status, notes, "respondedAt", "respondedBy", "createdAt", "updatedAt"
	`, id, status, notes, respondedBy).Scan(
		&c.ID, &c.Name, &c.Email, &c.Phone, &c.Subject, &c.Message,
		&c.Source, &c.IPAddress, &c.UserAgent, &c.Status, &c.Notes,
		&c.RespondedAt, &c.RespondedBy, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (h *Handler) getContactSummary(ctx context.Context) (*ContactSummary, error) {
	var s ContactSummary
	err := h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'NEW') as new,
			COUNT(*) FILTER (WHERE status = 'IN_PROGRESS') as in_progress,
			COUNT(*) FILTER (WHERE status = 'RESPONDED') as responded,
			COUNT(*) FILTER (WHERE status = 'RESOLVED') as resolved,
			COUNT(*) FILTER (WHERE status = 'CLOSED') as closed
		FROM contact_submissions
	`).Scan(&s.Total, &s.New, &s.InProgress, &s.Responded, &s.Resolved, &s.Closed)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
