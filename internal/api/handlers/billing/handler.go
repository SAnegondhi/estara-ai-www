// Package billing provides HTTP handlers for billing endpoints
package billing

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/services/billing"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles billing HTTP requests
type Handler struct {
	service *billing.Service
	cfg     *config.Config
	logger  *slog.Logger
}

// NewHandler creates a new billing handler
func NewHandler(db *postgres.DB, cfg *config.Config) *Handler {
	return &Handler{
		service: billing.NewService(db, cfg),
		cfg:     cfg,
		logger:  slog.Default().With("component", "billing_handler"),
	}
}

// CreateCheckoutSessionRequest represents the request body for creating a checkout session
type CreateCheckoutSessionRequest struct {
	PriceID        string            `json:"priceId"`
	ProductType    string            `json:"productType"`
	SuccessURL     string            `json:"successUrl"`
	CancelURL      string            `json:"cancelUrl"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	AllowPromoCode bool              `json:"allowPromoCode,omitempty"`
}

// CreateCheckout creates a new Stripe checkout session
// POST /api/create-checkout-session
func (h *Handler) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var req CreateCheckoutSessionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "Invalid request body")
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

	// Map product type
	productType := billing.ProductSubscription
	switch req.ProductType {
	case "SINGLE_REPORT":
		productType = billing.ProductSingleReport
	case "REPORT_PACK":
		productType = billing.ProductReportPack
	case "OVERAGE_REPORT":
		productType = billing.ProductOverageReport
	case "INSIGHT_ACCESS":
		productType = billing.ProductInsightAccess
	}

	checkoutReq := &billing.CheckoutSessionRequest{
		PriceID:        req.PriceID,
		ProductType:    productType,
		SuccessURL:     req.SuccessURL,
		CancelURL:      req.CancelURL,
		CustomerEmail:  claims.Email,
		Metadata:       req.Metadata,
		AllowPromoCode: req.AllowPromoCode,
	}

	resp, err := h.service.CreateCheckoutSession(ctx, claims.UserID, claims.Email, checkoutReq)
	if err != nil {
		h.logger.Error("failed to create checkout session", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to create checkout session")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":        true,
		"sessionId":      resp.SessionID,
		"sessionUrl":     resp.SessionURL,
		"publishableKey": resp.PublishableKey,
	})
}

// GetCheckoutSession retrieves a checkout session status
// GET /api/checkout-session/{id}
func (h *Handler) GetCheckoutSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sessionID := r.PathValue("id")
	if sessionID == "" {
		httputil.BadRequest(w, "Session ID is required")
		return
	}

	resp, err := h.service.GetCheckoutSessionStatus(ctx, sessionID)
	if err != nil {
		h.logger.Error("failed to get checkout session", "error", err, "session_id", sessionID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to get checkout session")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":         resp.Success,
		"status":          resp.Status,
		"paymentIntentId": resp.PaymentIntentID,
		"subscriptionId":  resp.SubscriptionID,
		"customerId":      resp.CustomerID,
	})
}

// CheckPaymentStatus checks the payment status of a checkout session
// GET /api/check-payment-status?session_id=xxx
func (h *Handler) CheckPaymentStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		httputil.BadRequest(w, "session_id is required")
		return
	}

	resp, err := h.service.GetCheckoutSessionStatus(ctx, sessionID)
	if err != nil {
		h.logger.Error("failed to check payment status", "error", err, "session_id", sessionID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to check payment status")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"paid":    resp.Success,
		"status":  resp.Status,
	})
}

// GetSubscription retrieves the current user's subscription
// GET /api/subscription
func (h *Handler) GetSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	sub, err := h.service.GetSubscription(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, billing.ErrSubscriptionNotFound) {
			httputil.JSON(w, http.StatusOK, map[string]interface{}{
				"success":      true,
				"subscription": nil,
			})
			return
		}
		h.logger.Error("failed to get subscription", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to get subscription")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"subscription": sub,
	})
}

// CancelSubscriptionRequest represents the request body for canceling a subscription
type CancelSubscriptionRequest struct {
	CancelAtPeriodEnd bool `json:"cancelAtPeriodEnd"`
}

// CancelSubscription cancels the current user's subscription
// POST /api/cancel-subscription
func (h *Handler) CancelSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var req CancelSubscriptionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		// Default to cancel at period end
		req.CancelAtPeriodEnd = true
	}

	err := h.service.CancelSubscription(ctx, claims.UserID, req.CancelAtPeriodEnd)
	if err != nil {
		if errors.Is(err, billing.ErrSubscriptionNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "Subscription not found",
			})
			return
		}
		h.logger.Error("failed to cancel subscription", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to cancel subscription")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Subscription canceled successfully",
	})
}

// ReactivateSubscription reactivates a canceled subscription
// POST /api/reactivate-subscription
func (h *Handler) ReactivateSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	err := h.service.ReactivateSubscription(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, billing.ErrSubscriptionNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "Subscription not found",
			})
			return
		}
		h.logger.Error("failed to reactivate subscription", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to reactivate subscription")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Subscription reactivated successfully",
	})
}

// RenewSubscription is an alias for ReactivateSubscription
// POST /api/renew-subscription
func (h *Handler) RenewSubscription(w http.ResponseWriter, r *http.Request) {
	h.ReactivateSubscription(w, r)
}

// CreateFreeSubscription creates a free tier subscription
// POST /api/create-free-subscription
func (h *Handler) CreateFreeSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	sub, err := h.service.CreateFreeSubscription(ctx, claims.UserID)
	if err != nil {
		h.logger.Error("failed to create free subscription", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to create subscription")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"subscription": sub,
	})
}

// UpgradeSubscriptionRequest represents the request body for upgrading a subscription
type UpgradeSubscriptionRequest struct {
	NewTier string `json:"newTier"`
}

// UpgradeSubscription upgrades the subscription to a new tier
// POST /api/upgrade-subscription
func (h *Handler) UpgradeSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var req UpgradeSubscriptionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "Invalid request body")
		return
	}

	if req.NewTier == "" {
		httputil.BadRequest(w, "newTier is required")
		return
	}

	// Map tier string
	var tier billing.SubscriptionTier
	switch req.NewTier {
	case "ANNUAL_ACCESS", "INVESTOR":
		tier = billing.TierAnnualAccess
	case "PROFESSIONAL_ALLOCATOR", "PROFESSIONAL":
		tier = billing.TierProfessionalAllocator
	case "API_INVESTOR":
		tier = billing.TierAPIInvestor
	case "API_ALLOCATOR":
		tier = billing.TierAPIAllocator
	default:
		httputil.BadRequest(w, "Invalid tier")
		return
	}

	err := h.service.UpgradeSubscription(ctx, claims.UserID, tier)
	if err != nil {
		if errors.Is(err, billing.ErrSubscriptionNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "Subscription not found",
			})
			return
		}
		if errors.Is(err, billing.ErrInvalidTier) {
			httputil.BadRequest(w, "Invalid tier")
			return
		}
		h.logger.Error("failed to upgrade subscription", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to upgrade subscription")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Subscription upgraded successfully",
	})
}

// CreatePortalSessionRequest represents the request body for creating a billing portal session
type CreatePortalSessionRequest struct {
	ReturnURL string `json:"returnUrl"`
}

// CreatePortalSession creates a Stripe billing portal session
// POST /api/billing/portal
func (h *Handler) CreatePortalSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var req CreatePortalSessionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "Invalid request body")
		return
	}

	if req.ReturnURL == "" {
		req.ReturnURL = h.cfg.Server.ClientURL + "/account"
	}

	resp, err := h.service.CreateBillingPortalSession(ctx, claims.UserID, req.ReturnURL)
	if err != nil {
		if errors.Is(err, billing.ErrSubscriptionNotFound) || errors.Is(err, billing.ErrCustomerNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "No billing account found",
			})
			return
		}
		h.logger.Error("failed to create portal session", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to create portal session")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"url":     resp.URL,
	})
}

// GetInvoices retrieves invoices for the current user
// GET /api/billing/invoices
func (h *Handler) GetInvoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	// Parse pagination
	limit := int32(20)
	offset := int32(0)
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = int32(parsed)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}

	invoices, err := h.service.GetUserInvoices(ctx, claims.UserID, limit, offset)
	if err != nil {
		h.logger.Error("failed to get invoices", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to get invoices")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"invoices": invoices,
	})
}

// GetReceipts retrieves receipts for the current user
// GET /api/billing/receipts
func (h *Handler) GetReceipts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	// Parse pagination
	limit := int32(20)
	offset := int32(0)
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = int32(parsed)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}

	receipts, err := h.service.GetUserReceipts(ctx, claims.UserID, limit, offset)
	if err != nil {
		h.logger.Error("failed to get receipts", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to get receipts")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"receipts": receipts,
	})
}
