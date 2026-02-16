package website

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	db "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/services/website"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles website-related HTTP requests
type Handler struct {
	service *website.Service
	logger  *slog.Logger
}

// NewHandler creates a new website handler
func NewHandler(store *db.Store, cfg *config.Config) *Handler {
	return &Handler{
		service: website.NewService(store, cfg),
		logger:  slog.Default().With("component", "website_handler"),
	}
}

// GenerateReport initiates report generation
// POST /api/website/generate-report
func (h *Handler) GenerateReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req website.GenerateReportRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate required fields
	if req.Email == "" {
		httputil.BadRequest(w, "email is required")
		return
	}
	if req.ReportType == "" {
		httputil.BadRequest(w, "reportType is required")
		return
	}

	result, err := h.service.GenerateReport(ctx, &req)
	if err != nil {
		h.logger.Error("failed to generate report", "error", err)
		if err == website.ErrInvalidReportType {
			httputil.BadRequest(w, "invalid report type")
			return
		}
		httputil.Error(w, http.StatusInternalServerError, "Failed to generate report")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// CreateCheckout creates a Stripe checkout session
// POST /api/website/checkout
func (h *Handler) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req website.CheckoutRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate required fields
	if req.PriceID == "" {
		httputil.BadRequest(w, "priceId is required")
		return
	}
	if req.SuccessURL == "" || req.CancelURL == "" {
		httputil.BadRequest(w, "successUrl and cancelUrl are required")
		return
	}

	result, err := h.service.CreateCheckoutSession(ctx, &req)
	if err != nil {
		h.logger.Error("failed to create checkout session", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to create checkout session")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// CreateFreeSnapshot creates a free snapshot request
// POST /api/website/free-snapshot
func (h *Handler) CreateFreeSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req website.FreeSnapshotRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate required fields
	if req.Email == "" {
		httputil.BadRequest(w, "email is required")
		return
	}
	if req.Address == "" || req.City == "" || req.State == "" || req.ZipCode == "" {
		httputil.BadRequest(w, "address, city, state, and zipCode are required")
		return
	}

	// Get IP and User-Agent from request
	req.IPAddress = r.RemoteAddr
	req.UserAgent = r.UserAgent()

	result, err := h.service.CreateFreeSnapshot(ctx, &req)
	if err != nil {
		h.logger.Error("failed to create free snapshot", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to create snapshot request")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// GetOrderStatus retrieves the status of an order
// GET /api/website/order-status?orderId=xxx
func (h *Handler) GetOrderStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	orderID := r.URL.Query().Get("orderId")
	if orderID == "" {
		httputil.BadRequest(w, "orderId is required")
		return
	}

	result, err := h.service.GetOrderStatus(ctx, orderID)
	if err != nil {
		h.logger.Error("failed to get order status", "error", err, "order_id", orderID)
		if err == website.ErrOrderNotFound {
			httputil.NotFound(w, "Order not found")
			return
		}
		httputil.Error(w, http.StatusInternalServerError, "Failed to get order status")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// GetPricingConfig returns the public pricing configuration
// GET /api/website/pricing
func (h *Handler) GetPricingConfig(w http.ResponseWriter, r *http.Request) {
	result := h.service.GetPricingConfig()
	httputil.JSON(w, http.StatusOK, result)
}

// GetInsightAccessStatus checks insight access status for authenticated user
// GET /api/website/insight-access/status
func (h *Handler) GetInsightAccessStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context (set by auth middleware)
	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	result, err := h.service.GetInsightAccessStatus(ctx, claims.UserID)
	if err != nil {
		h.logger.Error("failed to get insight access status", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to get insight access status")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// CreateGuestSession creates a new guest session
// POST /api/website/guest-session
func (h *Handler) CreateGuestSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := h.service.CreateGuestSession(ctx)
	if err != nil {
		h.logger.Error("failed to create guest session", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to create guest session")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// GetReport retrieves a completed report
// GET /api/website/reports/{id}
func (h *Handler) GetReport(w http.ResponseWriter, r *http.Request) {
	reportID := chi.URLParam(r, "id")
	if reportID == "" {
		httputil.BadRequest(w, "report id is required")
		return
	}

	// TODO: Implement report retrieval
	httputil.Error(w, http.StatusNotImplemented, "Not implemented")
}
