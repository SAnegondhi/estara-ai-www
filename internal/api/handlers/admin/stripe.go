package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stripe/stripe-go/v76"
	stripeInvoice "github.com/stripe/stripe-go/v76/invoice"
	"github.com/stripe/stripe-go/v76/paymentintent"
	"github.com/stripe/stripe-go/v76/refund"

	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// Stripe Console Types
// ===============================

// StripePaymentResponse is a JSON-safe Stripe payment intent for admin views.
type StripePaymentResponse struct {
	ID            string  `json:"id"`
	Amount        int64   `json:"amount"`
	Currency      string  `json:"currency"`
	Status        string  `json:"status"`
	CustomerEmail *string `json:"customerEmail"`
	Description   *string `json:"description"`
	Created       string  `json:"created"`
}

// StripeInvoiceResponse is a JSON-safe Stripe invoice for admin views.
type StripeInvoiceResponse struct {
	ID               string  `json:"id"`
	Number           *string `json:"number"`
	CustomerEmail    *string `json:"customerEmail"`
	Status           string  `json:"status"`
	AmountDue        int64   `json:"amountDue"`
	AmountPaid       int64   `json:"amountPaid"`
	Currency         string  `json:"currency"`
	HostedInvoiceURL *string `json:"hostedInvoiceUrl"`
	InvoicePDFURL    *string `json:"invoicePdfUrl"`
	Created          string  `json:"created"`
}

// ProcessRefundRequest represents a refund request.
type ProcessRefundRequest struct {
	PaymentIntentID string `json:"paymentIntentId" validate:"required"`
	Amount          *int64 `json:"amount,omitempty"` // nil = full refund
	Reason          string `json:"reason,omitempty"`
}

// WebhookEventResponse is a JSON-safe webhook event summary.
type WebhookEventResponse struct {
	ID        string  `json:"id"`
	EventType string  `json:"eventType"`
	Status    string  `json:"status"`
	Error     *string `json:"error"`
	CreatedAt string  `json:"createdAt"`
}

// ===============================
// Stripe Console Handlers
// ===============================

// ListPayments returns recent Stripe payment intents.
func (h *Handler) ListPayments(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit := httputil.GetQueryParamInt(r, "limit", 25)
	if limit > 100 {
		limit = 100
	}

	params := &stripe.PaymentIntentListParams{}
	params.Limit = stripe.Int64(int64(limit))
	if status != "" {
		params.Filters.AddFilter("status", "", status)
	}

	iter := paymentintent.List(params)
	payments := make([]StripePaymentResponse, 0)
	for iter.Next() {
		pi := iter.PaymentIntent()
		p := StripePaymentResponse{
			ID:       pi.ID,
			Amount:   pi.Amount,
			Currency: string(pi.Currency),
			Status:   string(pi.Status),
			Created:  time.Unix(pi.Created, 0).UTC().Format(time.RFC3339),
		}
		if pi.Description != "" {
			p.Description = &pi.Description
		}
		if pi.ReceiptEmail != "" {
			p.CustomerEmail = &pi.ReceiptEmail
		} else if pi.Customer != nil && pi.Customer.Email != "" {
			p.CustomerEmail = &pi.Customer.Email
		}
		payments = append(payments, p)
	}
	if err := iter.Err(); err != nil {
		h.logger.Error("failed to list Stripe payments", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list payments from Stripe")
		return
	}

	httputil.Success(w, map[string]any{
		"payments": payments,
		"total":    len(payments),
	})
}

// ProcessRefund creates a refund for a payment intent.
func (h *Handler) ProcessRefund(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ProcessRefundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	params := &stripe.RefundParams{
		PaymentIntent: stripe.String(req.PaymentIntentID),
	}
	if req.Amount != nil {
		params.Amount = req.Amount
	}
	if req.Reason != "" {
		params.Reason = stripe.String(req.Reason)
	}

	ref, err := refund.New(params)
	if err != nil {
		h.logger.Error("failed to process refund", "error", err, "payment_intent", req.PaymentIntentID)
		httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("failed to process refund: %v", err))
		return
	}

	h.logAdminAudit(ctx, r, "STRIPE_REFUND", "payment", req.PaymentIntentID, map[string]any{
		"refundId": ref.ID,
		"amount":   ref.Amount,
		"reason":   req.Reason,
	})
	h.logger.Info("refund processed", "refund_id", ref.ID, "payment_intent", req.PaymentIntentID, "amount", ref.Amount)
	httputil.Success(w, map[string]any{
		"refundId":        ref.ID,
		"paymentIntentId": req.PaymentIntentID,
		"amount":          ref.Amount,
		"status":          string(ref.Status),
	})
}

// ListInvoices returns recent Stripe invoices.
func (h *Handler) ListInvoices(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit := httputil.GetQueryParamInt(r, "limit", 25)
	if limit > 100 {
		limit = 100
	}

	params := &stripe.InvoiceListParams{}
	params.Limit = stripe.Int64(int64(limit))
	if status != "" {
		params.Status = stripe.String(status)
	}

	iter := stripeInvoice.List(params)
	invoices := make([]StripeInvoiceResponse, 0)
	for iter.Next() {
		inv := iter.Invoice()
		i := StripeInvoiceResponse{
			ID:        inv.ID,
			Status:    string(inv.Status),
			AmountDue: inv.AmountDue,
			Currency:  string(inv.Currency),
			Created:   time.Unix(inv.Created, 0).UTC().Format(time.RFC3339),
		}
		if inv.AmountPaid > 0 {
			i.AmountPaid = inv.AmountPaid
		}
		if inv.Number != "" {
			i.Number = &inv.Number
		}
		if inv.CustomerEmail != "" {
			i.CustomerEmail = &inv.CustomerEmail
		}
		if inv.HostedInvoiceURL != "" {
			i.HostedInvoiceURL = &inv.HostedInvoiceURL
		}
		if inv.InvoicePDF != "" {
			i.InvoicePDFURL = &inv.InvoicePDF
		}
		invoices = append(invoices, i)
	}
	if err := iter.Err(); err != nil {
		h.logger.Error("failed to list Stripe invoices", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list invoices from Stripe")
		return
	}

	httputil.Success(w, map[string]any{
		"invoices": invoices,
		"total":    len(invoices),
	})
}

// WebhookStatus returns recent webhook event processing status.
func (h *Handler) WebhookStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Main.Query(ctx, `
		SELECT id, event_type, status, error_message, created_at
		FROM billing_audit_logs
		WHERE event_type IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 50
	`)
	if err != nil {
		h.logger.Warn("failed to query webhook events", "error", err)
		httputil.Success(w, map[string]any{
			"events":      []WebhookEventResponse{},
			"total":       0,
			"failureRate": 0,
		})
		return
	}
	defer rows.Close()

	var events []WebhookEventResponse
	var totalCount, failedCount int
	for rows.Next() {
		var e WebhookEventResponse
		if err := rows.Scan(&e.ID, &e.EventType, &e.Status, &e.Error, &e.CreatedAt); err != nil {
			h.logger.Warn("failed to scan webhook event", "error", err)
			continue
		}
		events = append(events, e)
		totalCount++
		if e.Status == "FAILED" || e.Status == "ERROR" {
			failedCount++
		}
	}
	if events == nil {
		events = []WebhookEventResponse{}
	}

	var failureRate float64
	if totalCount > 0 {
		failureRate = float64(failedCount) / float64(totalCount) * 100
	}

	httputil.Success(w, map[string]any{
		"events":      events,
		"total":       totalCount,
		"failureRate": failureRate,
	})
}
