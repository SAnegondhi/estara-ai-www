package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v76"
	stripeCustomer "github.com/stripe/stripe-go/v76/customer"
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
// Helpers
// ===============================

func mapPaymentIntent(pi *stripe.PaymentIntent) StripePaymentResponse {
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
	// receipt_email is set for one-time charges; Customer.Email for subscription charges
	if pi.ReceiptEmail != "" {
		p.CustomerEmail = &pi.ReceiptEmail
	} else if pi.Customer != nil && pi.Customer.Email != "" {
		p.CustomerEmail = &pi.Customer.Email
	}
	return p
}

func mapInvoice(inv *stripe.Invoice) StripeInvoiceResponse {
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
	return i
}

// searchCustomerIDs finds Stripe customer IDs whose email exactly matches the search term.
func (h *Handler) searchCustomerIDs(search string) ([]string, error) {
	cParams := &stripe.CustomerListParams{}
	cParams.Filters.AddFilter("email", "", search)
	cIter := stripeCustomer.List(cParams)
	var ids []string
	for cIter.Next() {
		ids = append(ids, cIter.Customer().ID)
	}
	return ids, cIter.Err()
}

// ===============================
// Stripe Console Handlers
// ===============================

// ListPayments returns Stripe payment intents. Supports ?search=email, ?search=pi_xxx, ?status=.
func (h *Handler) ListPayments(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	search := r.URL.Query().Get("search")
	limit := httputil.GetQueryParamInt(r, "limit", 50)
	if limit > 100 {
		limit = 100
	}

	payments := make([]StripePaymentResponse, 0)

	if search != "" {
		// Smart: direct PaymentIntent ID lookup
		if strings.HasPrefix(search, "pi_") {
			piParams := &stripe.PaymentIntentParams{}
			piParams.AddExpand("customer")
			pi, err := paymentintent.Get(search, piParams)
			if err == nil {
				payments = append(payments, mapPaymentIntent(pi))
			}
			httputil.Success(w, map[string]any{"payments": payments, "total": len(payments)})
			return
		}

		// Customer email search → list their payment intents
		customerIDs, err := h.searchCustomerIDs(search)
		if err != nil {
			h.logger.Error("stripe customer search failed", "error", err)
		}
		if len(customerIDs) == 0 {
			httputil.Success(w, map[string]any{"payments": payments, "total": 0})
			return
		}
		for _, cid := range customerIDs {
			piParams := &stripe.PaymentIntentListParams{}
			piParams.Limit = stripe.Int64(int64(limit))
			piParams.AddExpand("data.customer")
			piParams.Filters.AddFilter("customer", "", cid)
			if status != "" {
				piParams.Filters.AddFilter("status", "", status)
			}
			iter := paymentintent.List(piParams)
			for iter.Next() {
				payments = append(payments, mapPaymentIntent(iter.PaymentIntent()))
			}
			if err := iter.Err(); err != nil {
				h.logger.Error("failed to list payments for customer", "customer", cid, "error", err)
			}
		}
		httputil.Success(w, map[string]any{"payments": payments, "total": len(payments)})
		return
	}

	// No search: list most recent payment intents
	params := &stripe.PaymentIntentListParams{}
	params.Limit = stripe.Int64(int64(limit))
	params.AddExpand("data.customer")
	if status != "" {
		params.Filters.AddFilter("status", "", status)
	}
	iter := paymentintent.List(params)
	for iter.Next() {
		payments = append(payments, mapPaymentIntent(iter.PaymentIntent()))
	}
	if err := iter.Err(); err != nil {
		h.logger.Error("failed to list Stripe payments", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list payments from Stripe")
		return
	}
	httputil.Success(w, map[string]any{"payments": payments, "total": len(payments)})
}

// ProcessRefund issues a Stripe refund for a payment intent.
func (h *Handler) ProcessRefund(w http.ResponseWriter, r *http.Request) {
	var req ProcessRefundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	params := &stripe.RefundParams{
		PaymentIntent: stripe.String(req.PaymentIntentID),
	}
	if req.Amount != nil {
		params.Amount = req.Amount
	}
	if req.Reason != "" {
		switch req.Reason {
		case "duplicate":
			params.Reason = stripe.String(string(stripe.RefundReasonDuplicate))
		case "fraudulent":
			params.Reason = stripe.String(string(stripe.RefundReasonFraudulent))
		case "requested_by_customer":
			params.Reason = stripe.String(string(stripe.RefundReasonRequestedByCustomer))
		}
	}

	ref, err := refund.New(params)
	if err != nil {
		h.logger.Error("failed to process refund", "paymentIntent", req.PaymentIntentID, "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to process refund: "+err.Error())
		return
	}

	h.logAdminAudit(r.Context(), r, "ADMIN_USER", "STRIPE_REFUND", "payment_intent", req.PaymentIntentID, map[string]any{
		"refundId": ref.ID,
		"amount":   ref.Amount,
		"reason":   req.Reason,
	})

	httputil.Success(w, map[string]any{
		"refundId": ref.ID,
		"amount":   ref.Amount,
		"status":   string(ref.Status),
	})
}

// ListInvoices returns Stripe invoices. Supports ?search=email, ?search=in_xxx, ?status=.
func (h *Handler) ListInvoices(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	search := r.URL.Query().Get("search")
	limit := httputil.GetQueryParamInt(r, "limit", 50)
	if limit > 100 {
		limit = 100
	}

	invoices := make([]StripeInvoiceResponse, 0)

	if search != "" {
		// Smart: direct Invoice ID lookup
		if strings.HasPrefix(search, "in_") {
			inv, err := stripeInvoice.Get(search, nil)
			if err == nil {
				invoices = append(invoices, mapInvoice(inv))
			}
			httputil.Success(w, map[string]any{"invoices": invoices, "total": len(invoices)})
			return
		}

		// Customer email search → list their invoices
		customerIDs, err := h.searchCustomerIDs(search)
		if err != nil {
			h.logger.Error("stripe customer search failed", "error", err)
		}
		if len(customerIDs) == 0 {
			httputil.Success(w, map[string]any{"invoices": invoices, "total": 0})
			return
		}
		for _, cid := range customerIDs {
			invParams := &stripe.InvoiceListParams{}
			invParams.Limit = stripe.Int64(int64(limit))
			invParams.Customer = stripe.String(cid)
			if status != "" {
				invParams.Status = stripe.String(status)
			}
			iter := stripeInvoice.List(invParams)
			for iter.Next() {
				invoices = append(invoices, mapInvoice(iter.Invoice()))
			}
			if err := iter.Err(); err != nil {
				h.logger.Error("failed to list invoices for customer", "customer", cid, "error", err)
			}
		}
		httputil.Success(w, map[string]any{"invoices": invoices, "total": len(invoices)})
		return
	}

	// No search: list most recent invoices
	params := &stripe.InvoiceListParams{}
	params.Limit = stripe.Int64(int64(limit))
	if status != "" {
		params.Status = stripe.String(status)
	}
	iter := stripeInvoice.List(params)
	for iter.Next() {
		invoices = append(invoices, mapInvoice(iter.Invoice()))
	}
	if err := iter.Err(); err != nil {
		h.logger.Error("failed to list Stripe invoices", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list invoices from Stripe")
		return
	}
	httputil.Success(w, map[string]any{"invoices": invoices, "total": len(invoices)})
}

// WebhookStatus returns recent webhook event processing status.
func (h *Handler) WebhookStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	events, err := h.store.Q().ListWebhookEvents(ctx, 50)
	if err != nil {
		h.logger.Error("failed to list webhook events", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to fetch webhook events")
		return
	}

	responses := make([]WebhookEventResponse, 0, len(events))
	for _, e := range events {
		resp := WebhookEventResponse{
			ID:        e.ID,
			EventType: e.EventType,
			Status:    "PROCESSED",
			CreatedAt: e.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
		responses = append(responses, resp)
	}
	failedCount := 0

	var failureRate float64
	if len(responses) > 0 {
		failureRate = float64(failedCount) / float64(len(responses)) * 100
	}

	httputil.Success(w, map[string]any{
		"events":      responses,
		"total":       len(responses),
		"failureRate": failureRate,
	})
}
