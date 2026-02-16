package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// ContactSubmission represents a contact form submission for admin view
type ContactSubmission struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Email           string     `json:"email"`
	Company         *string    `json:"company,omitempty"`
	Phone           *string    `json:"phone,omitempty"`
	Subject         *string    `json:"subject,omitempty"`
	Message         string     `json:"message"`
	Category        string     `json:"category"`
	Source          *string    `json:"source,omitempty"`
	IPAddress       *string    `json:"ipAddress,omitempty"`
	UserAgent       *string    `json:"userAgent,omitempty"`
	Status          string     `json:"status"`
	AssignedTo      *string    `json:"assignedTo,omitempty"`
	Notes           *string    `json:"notes,omitempty"`
	FirstResponseAt *time.Time `json:"firstResponseAt,omitempty"`
	ResolvedAt      *time.Time `json:"resolvedAt,omitempty"`
	ResponseCount   int        `json:"responseCount"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// UpdateContactRequest represents a request to update a contact submission
type UpdateContactRequest struct {
	Status *string `json:"status,omitempty" validate:"omitempty,oneof=NEW ASSIGNED IN_PROGRESS AWAITING_RESPONSE RESOLVED CLOSED"`
	Notes  *string `json:"notes,omitempty"`
}

// ContactSummary represents aggregate contact statistics
type ContactSummary struct {
	Total            int64 `json:"total"`
	New              int64 `json:"new"`
	Assigned         int64 `json:"assigned"`
	InProgress       int64 `json:"inProgress"`
	AwaitingResponse int64 `json:"awaitingResponse"`
	Resolved         int64 `json:"resolved"`
	Closed           int64 `json:"closed"`
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

	adminEmail := h.getAdminEmailFromRequest(r)

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

	// Check existence first since DeleteContactSubmission is :exec (no RowsAffected)
	_, err := h.store.Q().GetContactByID(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "contact not found")
			return
		}
		h.logger.Error("failed to check contact", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete contact")
		return
	}

	if err := h.store.Q().DeleteContactSubmission(ctx, id); err != nil {
		h.logger.Error("failed to delete contact", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete contact")
		return
	}

	h.logAdminAudit(ctx, r, "CONTACT_DELETE", "contact", id, nil)
	httputil.Success(w, map[string]interface{}{"deleted": true})
}

// ===============================
// Contact Helper Methods
// ===============================

func (h *Handler) listContacts(ctx context.Context, status, search string, limit, offset int) ([]ContactSubmission, int64, error) {
	q := h.store.Q()
	filterParams := queries.CountContactsFilteredParams{
		StatusFilter: pgtype.Text{String: status, Valid: status != ""},
		Search:       pgtype.Text{String: search, Valid: search != ""},
	}

	total, err := q.CountContactsFiltered(ctx, filterParams)
	if err != nil {
		return nil, 0, err
	}

	rows, err := q.ListContactsFiltered(ctx, queries.ListContactsFilteredParams{
		StatusFilter: filterParams.StatusFilter,
		Search:       filterParams.Search,
		Offset:       int32(offset),
		Limit:        int32(limit),
	})
	if err != nil {
		return nil, 0, err
	}

	contacts := make([]ContactSubmission, 0, len(rows))
	for _, r := range rows {
		contacts = append(contacts, mapContactRow(r))
	}
	return contacts, total, nil
}

func (h *Handler) getContactByID(ctx context.Context, id string) (*ContactSubmission, error) {
	r, err := h.store.Q().GetContactByID(ctx, id)
	if err != nil {
		return nil, err
	}
	c := mapContactByIDRow(r)
	return &c, nil
}

func (h *Handler) updateContact(ctx context.Context, id string, status, notes *string, assignedTo string) (*ContactSubmission, error) {
	var statusParam, notesParam pgtype.Text
	if status != nil {
		statusParam = pgtype.Text{String: *status, Valid: true}
	}
	if notes != nil {
		notesParam = pgtype.Text{String: *notes, Valid: true}
	}

	r, err := h.store.Q().UpdateContactAdmin(ctx, queries.UpdateContactAdminParams{
		ID:         id,
		Status:     statusParam,
		Notes:      notesParam,
		AssignedTo: pgtype.Text{String: assignedTo, Valid: assignedTo != ""},
	})
	if err != nil {
		return nil, err
	}
	c := mapUpdateContactRow(r)
	return &c, nil
}

func (h *Handler) getContactSummary(ctx context.Context) (*ContactSummary, error) {
	r, err := h.store.Q().GetContactSummary(ctx)
	if err != nil {
		return nil, err
	}
	return &ContactSummary{
		Total:            r.Total,
		New:              r.NewCount,
		Assigned:         r.AssignedCount,
		InProgress:       r.InProgressCount,
		AwaitingResponse: r.AwaitingResponseCount,
		Resolved:         r.ResolvedCount,
		Closed:           r.ClosedCount,
	}, nil
}

// mapContactRow converts a sqlc ListContactsFilteredRow to ContactSubmission.
func mapContactRow(r queries.ListContactsFilteredRow) ContactSubmission {
	c := ContactSubmission{
		ID:            r.ID,
		Name:          r.Name,
		Email:         r.Email,
		Message:       r.Message,
		Category:      r.Category,
		Status:        r.Status,
		ResponseCount: int(r.ResponseCount),
	}
	if r.Company.Valid {
		c.Company = &r.Company.String
	}
	if r.Phone.Valid {
		c.Phone = &r.Phone.String
	}
	if r.Subject.Valid {
		c.Subject = &r.Subject.String
	}
	if r.Source.Valid {
		c.Source = &r.Source.String
	}
	if r.IpAddress.Valid {
		c.IPAddress = &r.IpAddress.String
	}
	if r.UserAgent.Valid {
		c.UserAgent = &r.UserAgent.String
	}
	if r.AssignedTo.Valid {
		c.AssignedTo = &r.AssignedTo.String
	}
	if r.Notes.Valid {
		c.Notes = &r.Notes.String
	}
	if r.FirstResponseAt.Valid {
		c.FirstResponseAt = &r.FirstResponseAt.Time
	}
	if r.ResolvedAt.Valid {
		c.ResolvedAt = &r.ResolvedAt.Time
	}
	if r.CreatedAt.Valid {
		c.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		c.UpdatedAt = r.UpdatedAt.Time
	}
	return c
}

// mapContactByIDRow converts a sqlc GetContactByIDRow to ContactSubmission.
func mapContactByIDRow(r queries.GetContactByIDRow) ContactSubmission {
	c := ContactSubmission{
		ID:            r.ID,
		Name:          r.Name,
		Email:         r.Email,
		Message:       r.Message,
		Category:      r.Category,
		Status:        r.Status,
		ResponseCount: int(r.ResponseCount),
	}
	if r.Company.Valid {
		c.Company = &r.Company.String
	}
	if r.Phone.Valid {
		c.Phone = &r.Phone.String
	}
	if r.Subject.Valid {
		c.Subject = &r.Subject.String
	}
	if r.Source.Valid {
		c.Source = &r.Source.String
	}
	if r.IpAddress.Valid {
		c.IPAddress = &r.IpAddress.String
	}
	if r.UserAgent.Valid {
		c.UserAgent = &r.UserAgent.String
	}
	if r.AssignedTo.Valid {
		c.AssignedTo = &r.AssignedTo.String
	}
	if r.Notes.Valid {
		c.Notes = &r.Notes.String
	}
	if r.FirstResponseAt.Valid {
		c.FirstResponseAt = &r.FirstResponseAt.Time
	}
	if r.ResolvedAt.Valid {
		c.ResolvedAt = &r.ResolvedAt.Time
	}
	if r.CreatedAt.Valid {
		c.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		c.UpdatedAt = r.UpdatedAt.Time
	}
	return c
}

// mapUpdateContactRow converts a sqlc UpdateContactAdminRow to ContactSubmission.
func mapUpdateContactRow(r queries.UpdateContactAdminRow) ContactSubmission {
	c := ContactSubmission{
		ID:            r.ID,
		Name:          r.Name,
		Email:         r.Email,
		Message:       r.Message,
		Category:      r.Category,
		Status:        r.Status,
		ResponseCount: int(r.ResponseCount),
	}
	if r.Company.Valid {
		c.Company = &r.Company.String
	}
	if r.Phone.Valid {
		c.Phone = &r.Phone.String
	}
	if r.Subject.Valid {
		c.Subject = &r.Subject.String
	}
	if r.Source.Valid {
		c.Source = &r.Source.String
	}
	if r.IpAddress.Valid {
		c.IPAddress = &r.IpAddress.String
	}
	if r.UserAgent.Valid {
		c.UserAgent = &r.UserAgent.String
	}
	if r.AssignedTo.Valid {
		c.AssignedTo = &r.AssignedTo.String
	}
	if r.Notes.Valid {
		c.Notes = &r.Notes.String
	}
	if r.FirstResponseAt.Valid {
		c.FirstResponseAt = &r.FirstResponseAt.Time
	}
	if r.ResolvedAt.Valid {
		c.ResolvedAt = &r.ResolvedAt.Time
	}
	if r.CreatedAt.Valid {
		c.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		c.UpdatedAt = r.UpdatedAt.Time
	}
	return c
}
