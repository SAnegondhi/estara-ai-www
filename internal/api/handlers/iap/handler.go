package iap

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	db "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/services/iap"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles IAP-related HTTP requests
type Handler struct {
	service *iap.Service
	logger  *slog.Logger
}

// NewHandler creates a new IAP handler
func NewHandler(ctx context.Context, store *db.Store, cfg *config.Config) *Handler {
	service, _ := iap.NewService(ctx, store, cfg)
	return &Handler{
		service: service,
		logger:  slog.Default().With("component", "iap_handler"),
	}
}

// ValidateIOSReceiptRequest represents the iOS receipt validation request
type ValidateIOSReceiptRequest struct {
	ReceiptData string `json:"receiptData"`
}

// ValidateIOSReceipt validates an iOS receipt
// POST /api/mobile/iap/validate-receipt/ios
func (h *Handler) ValidateIOSReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ValidateIOSReceiptRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if req.ReceiptData == "" {
		httputil.BadRequest(w, "receiptData is required")
		return
	}

	result, err := h.service.ValidateIOSReceipt(ctx, req.ReceiptData)
	if err != nil {
		h.logger.Error("iOS receipt validation failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Receipt validation failed")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// ValidateAndroidReceipt validates an Android receipt
// POST /api/mobile/iap/validate-receipt/android
func (h *Handler) ValidateAndroidReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req iap.GoogleReceiptValidationRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if req.ProductID == "" || req.PurchaseToken == "" {
		httputil.BadRequest(w, "productId and purchaseToken are required")
		return
	}

	result, err := h.service.ValidateAndroidReceipt(ctx, &req)
	if err != nil {
		h.logger.Error("Android receipt validation failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Receipt validation failed")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// SyncEntitlements syncs IAP with user entitlements
// POST /api/mobile/iap/sync-entitlements
func (h *Handler) SyncEntitlements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context (set by auth middleware)
	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var req iap.SyncEntitlementsRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	result, err := h.service.SyncEntitlements(ctx, claims.UserID, &req)
	if err != nil {
		h.logger.Error("failed to sync entitlements", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to sync entitlements")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// GetSubscriptionPlans returns available subscription plans
// GET /api/mobile/subscription-plans
func (h *Handler) GetSubscriptionPlans(w http.ResponseWriter, r *http.Request) {
	result := h.service.GetSubscriptionPlans()
	httputil.JSON(w, http.StatusOK, result)
}
