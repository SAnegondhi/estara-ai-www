// Package webhooks provides HTTP handlers for webhook endpoints
package webhooks

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/services/billing"
	"github.com/estara-ai/www/pkg/httputil"
)

// StripeHandler handles Stripe webhook requests
type StripeHandler struct {
	webhookService *billing.WebhookService
	cfg            *config.Config
	logger         *slog.Logger
}

// NewStripeHandler creates a new Stripe webhook handler
func NewStripeHandler(db *postgres.DB, cfg *config.Config) *StripeHandler {
	return &StripeHandler{
		webhookService: billing.NewWebhookService(db, cfg),
		cfg:            cfg,
		logger:         slog.Default().With("component", "stripe_webhook_handler"),
	}
}

// HandleWebhook processes incoming Stripe webhook events
// POST /api/webhooks/stripe
func (h *StripeHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Read the request body
	body, err := io.ReadAll(io.LimitReader(r.Body, 65536)) // 64KB limit
	if err != nil {
		h.logger.Error("failed to read webhook body", "error", err)
		httputil.Error(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	defer r.Body.Close()

	// Get the Stripe signature header
	signature := r.Header.Get("Stripe-Signature")
	if signature == "" {
		h.logger.Warn("missing Stripe-Signature header")
		httputil.Error(w, http.StatusBadRequest, "Missing Stripe-Signature header")
		return
	}

	// Verify and parse the event
	event, err := h.webhookService.VerifyAndParseEvent(body, signature)
	if err != nil {
		h.logger.Error("webhook verification failed", "error", err)
		httputil.Error(w, http.StatusBadRequest, "Webhook verification failed")
		return
	}

	// Get request metadata for audit logging
	ipAddress := r.Header.Get("X-Forwarded-For")
	if ipAddress == "" {
		ipAddress = r.RemoteAddr
	}
	userAgent := r.Header.Get("User-Agent")

	// Process the event
	if err := h.webhookService.HandleEvent(ctx, event, ipAddress, userAgent); err != nil {
		h.logger.Error("failed to process webhook event",
			"error", err,
			"event_id", event.ID,
			"event_type", event.Type,
		)
		// Return 200 to prevent Stripe retries for processing errors
		// The audit log will capture the failure
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"received": true,
			"error":    err.Error(),
		})
		return
	}

	h.logger.Info("webhook event processed",
		"event_id", event.ID,
		"event_type", event.Type,
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"received": true,
	})
}
