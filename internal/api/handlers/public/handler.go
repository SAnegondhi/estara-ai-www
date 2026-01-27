package public

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles public (no-auth) HTTP requests
type Handler struct {
	db     *postgres.DB
	cfg    *config.Config
	logger *slog.Logger
}

// NewHandler creates a new public handler
func NewHandler(db *postgres.DB, cfg *config.Config) *Handler {
	return &Handler{
		db:     db,
		cfg:    cfg,
		logger: slog.Default().With("component", "public_handler"),
	}
}

// ContactSubmissionRequest represents a contact form submission
type ContactSubmissionRequest struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	Phone     string `json:"phone,omitempty"`
	Subject   string `json:"subject,omitempty"`
	Message   string `json:"message"`
	Source    string `json:"source,omitempty"`
	IPAddress string `json:"-"`
	UserAgent string `json:"-"`
}

// ContactSubmissionResponse represents the response to a contact submission
type ContactSubmissionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	ID      string `json:"id,omitempty"`
}

// SubmitContact handles contact form submissions
// POST /api/public/contact
func (h *Handler) SubmitContact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ContactSubmissionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate required fields
	if req.Name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}
	if req.Email == "" {
		httputil.BadRequest(w, "email is required")
		return
	}
	if req.Message == "" {
		httputil.BadRequest(w, "message is required")
		return
	}

	// Get IP and User-Agent from request
	req.IPAddress = r.RemoteAddr
	req.UserAgent = r.UserAgent()

	q := queries.New(h.db.Main)

	// Create contact submission
	submissionID := uuid.New().String()
	_, err := q.CreateContactSubmission(ctx, queries.CreateContactSubmissionParams{
		ID:        submissionID,
		Name:      req.Name,
		Email:     req.Email,
		Phone:     pgtype.Text{String: req.Phone, Valid: req.Phone != ""},
		Subject:   pgtype.Text{String: req.Subject, Valid: req.Subject != ""},
		Message:   req.Message,
		Source:    pgtype.Text{String: req.Source, Valid: req.Source != ""},
		IpAddress: pgtype.Text{String: req.IPAddress, Valid: req.IPAddress != ""},
		UserAgent: pgtype.Text{String: req.UserAgent, Valid: req.UserAgent != ""},
		Status:    "NEW",
	})
	if err != nil {
		h.logger.Error("failed to create contact submission", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to submit contact form")
		return
	}

	h.logger.Info("contact form submitted",
		"submission_id", submissionID,
		"email", req.Email,
	)

	httputil.JSON(w, http.StatusOK, ContactSubmissionResponse{
		Success: true,
		Message: "Thank you for contacting us. We will get back to you shortly.",
		ID:      submissionID,
	})
}

// EarlyAccessRequest represents an early access signup request
type EarlyAccessRequest struct {
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	Source    string `json:"source,omitempty"`
	IPAddress string `json:"-"`
	UserAgent string `json:"-"`
}

// EarlyAccessResponse represents the response to early access signup
type EarlyAccessResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Position int    `json:"position,omitempty"`
}

// SignupEarlyAccess handles early access waitlist signups
// POST /api/public/early-access
func (h *Handler) SignupEarlyAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req EarlyAccessRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate required fields
	if req.Email == "" {
		httputil.BadRequest(w, "email is required")
		return
	}

	// Get IP and User-Agent from request
	req.IPAddress = r.RemoteAddr
	req.UserAgent = r.UserAgent()

	q := queries.New(h.db.Main)

	// Check if email already exists in early_access
	existing, err := q.GetEarlyAccessByEmail(ctx, req.Email)
	if err == nil && existing.ID != "" {
		// Already signed up
		httputil.JSON(w, http.StatusOK, EarlyAccessResponse{
			Success: true,
			Message: "You are already on the waitlist!",
		})
		return
	}

	// Get current position (count of existing entries + 1)
	count, err := q.CountEarlyAccess(ctx)
	if err != nil {
		h.logger.Error("failed to count early access entries", "error", err)
		count = 0
	}

	// Create early access entry
	entryID := uuid.New().String()
	_, err = q.CreateEarlyAccess(ctx, queries.CreateEarlyAccessParams{
		ID:        entryID,
		Email:     req.Email,
		Name:      pgtype.Text{String: req.Name, Valid: req.Name != ""},
		Source:    pgtype.Text{String: req.Source, Valid: req.Source != ""},
		IpAddress: pgtype.Text{String: req.IPAddress, Valid: req.IPAddress != ""},
	})
	if err != nil {
		h.logger.Error("failed to create early access entry", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to sign up for early access")
		return
	}

	h.logger.Info("early access signup",
		"entry_id", entryID,
		"email", req.Email,
		"position", count+1,
	)

	httputil.JSON(w, http.StatusOK, EarlyAccessResponse{
		Success:  true,
		Message:  "You have been added to the waitlist!",
		Position: int(count + 1),
	})
}

// GetEarlyAccessStatus checks early access status for an email
// GET /api/public/early-access?email=xxx
func (h *Handler) GetEarlyAccessStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	email := r.URL.Query().Get("email")
	if email == "" {
		httputil.BadRequest(w, "email is required")
		return
	}

	q := queries.New(h.db.Main)

	entry, err := q.GetEarlyAccessByEmail(ctx, email)
	if err != nil {
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"registered": false,
		})
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"registered": true,
		"signedUpAt": entry.CreatedAt.Time.Format(time.RFC3339),
	})
}
