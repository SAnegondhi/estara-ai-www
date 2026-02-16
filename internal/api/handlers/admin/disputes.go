package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	billingService "github.com/estara-ai/www/internal/services/billing"
	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// Dispute Management
// ===============================

// DisputeResponse represents a dispute for admin display
type DisputeResponse struct {
	ID               string  `json:"id"`
	Amount           int64   `json:"amount"`
	Currency         string  `json:"currency"`
	Reason           string  `json:"reason"`
	Status           string  `json:"status"`
	PaymentIntentID  string  `json:"paymentIntentId,omitempty"`
	ChargeID         string  `json:"chargeId,omitempty"`
	CustomerEmail    string  `json:"customerEmail,omitempty"`
	EvidenceDueBy    *string `json:"evidenceDueBy,omitempty"`
	HasEvidence      bool    `json:"hasEvidence"`
	Created          string  `json:"created"`
	IsChargeRefundable bool  `json:"isChargeRefundable"`
}

// DisputeMetricsResponse represents dispute metrics
type DisputeMetricsResponse struct {
	OpenDisputes     int     `json:"openDisputes"`
	TotalDisputes    int     `json:"totalDisputes"`
	WonDisputes      int     `json:"wonDisputes"`
	LostDisputes     int     `json:"lostDisputes"`
	AmountAtRisk     int64   `json:"amountAtRisk"`
	ChargebackRate   float64 `json:"chargebackRate"`
	WinRate          float64 `json:"winRate"`
	EvidenceDueSoon  int     `json:"evidenceDueSoon"`
}

// SubmitEvidenceRequest represents the request to submit dispute evidence
type SubmitEvidenceRequest struct {
	Evidence billingService.ChargebackEvidence `json:"evidence" validate:"required"`
}

// ListDisputes returns a list of Stripe disputes
func (h *Handler) ListDisputes(w http.ResponseWriter, r *http.Request) {
	if h.billing == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "billing service not configured")
		return
	}

	startingAfter := r.URL.Query().Get("startingAfter")
	limit := int64(httputil.GetQueryParamInt(r, "limit", 25))
	if limit > 100 {
		limit = 100
	}

	disputes, hasMore, err := h.billing.ListDisputes(limit, startingAfter)
	if err != nil {
		h.logger.Error("failed to list disputes", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list disputes")
		return
	}

	var results []DisputeResponse
	for _, d := range disputes {
		resp := DisputeResponse{
			ID:       d.ID,
			Amount:   d.Amount,
			Currency: string(d.Currency),
			Reason:   string(d.Reason),
			Status:   string(d.Status),
			Created:  time.Unix(d.Created, 0).Format(time.RFC3339),
		}
		if d.Charge != nil {
			resp.ChargeID = d.Charge.ID
			resp.IsChargeRefundable = d.IsChargeRefundable
		}
		if d.PaymentIntent != nil {
			resp.PaymentIntentID = d.PaymentIntent.ID
		}
		if d.EvidenceDetails != nil && d.EvidenceDetails.DueBy > 0 {
			due := time.Unix(d.EvidenceDetails.DueBy, 0).Format(time.RFC3339)
			resp.EvidenceDueBy = &due
		}
		if d.Evidence != nil {
			resp.HasEvidence = d.Evidence.ProductDescription != "" || d.Evidence.CustomerName != ""
		}
		results = append(results, resp)
	}

	if results == nil {
		results = []DisputeResponse{}
	}

	httputil.Success(w, map[string]interface{}{
		"disputes": results,
		"hasMore":  hasMore,
	})
}

// GetDisputeDetail returns detailed information about a specific dispute
func (h *Handler) GetDisputeDetail(w http.ResponseWriter, r *http.Request) {
	if h.billing == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "billing service not configured")
		return
	}

	disputeID := chi.URLParam(r, "id")
	if disputeID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	d, err := h.billing.GetDispute(disputeID)
	if err != nil {
		h.logger.Error("failed to get dispute", "error", err, "dispute_id", disputeID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get dispute")
		return
	}

	detail := map[string]interface{}{
		"id":       d.ID,
		"amount":   d.Amount,
		"currency": string(d.Currency),
		"reason":   string(d.Reason),
		"status":   string(d.Status),
		"created":  time.Unix(d.Created, 0).Format(time.RFC3339),
	}
	if d.Charge != nil {
		detail["chargeId"] = d.Charge.ID
	}
	if d.PaymentIntent != nil {
		detail["paymentIntentId"] = d.PaymentIntent.ID
	}
	if d.EvidenceDetails != nil {
		detail["evidenceDetails"] = map[string]interface{}{
			"dueBy":                time.Unix(d.EvidenceDetails.DueBy, 0).Format(time.RFC3339),
			"hasEvidence":          d.EvidenceDetails.HasEvidence,
			"pastDue":              d.EvidenceDetails.PastDue,
			"submissionCount":      d.EvidenceDetails.SubmissionCount,
		}
	}
	if d.Evidence != nil {
		detail["evidence"] = map[string]interface{}{
			"customerName":       d.Evidence.CustomerName,
			"customerEmail":      d.Evidence.CustomerEmailAddress,
			"productDescription": d.Evidence.ProductDescription,
			"billingAddress":     d.Evidence.BillingAddress,
			"serviceDate":        d.Evidence.ServiceDate,
			"cancellationPolicy": d.Evidence.CancellationPolicy,
			"refundPolicy":       d.Evidence.RefundPolicy,
		}
	}

	// Get related audit logs
	ctx := r.Context()
	auditEntries, _, _ := h.getAuditLogEntries(ctx, "", "DISPUTE_", "", 10, 0)
	detail["auditLog"] = auditEntries

	httputil.Success(w, detail)
}

// SubmitEvidence submits evidence for a dispute
func (h *Handler) SubmitEvidence(w http.ResponseWriter, r *http.Request) {
	if h.billing == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "billing service not configured")
		return
	}

	disputeID := chi.URLParam(r, "id")
	if disputeID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req SubmitEvidenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	d, err := h.billing.UpdateDispute(disputeID, &req.Evidence)
	if err != nil {
		h.logger.Error("failed to submit evidence", "error", err, "dispute_id", disputeID)
		httputil.Error(w, http.StatusInternalServerError, "failed to submit evidence")
		return
	}

	h.logAdminAudit(r.Context(), r, "DISPUTE_EVIDENCE_SUBMIT", "dispute", disputeID, map[string]interface{}{
		"amount": d.Amount,
		"reason": string(d.Reason),
	})

	httputil.Success(w, map[string]interface{}{
		"disputeId": d.ID,
		"status":    string(d.Status),
		"message":   "Evidence submitted successfully",
	})
}

// AutoCompileEvidence automatically compiles evidence for a dispute
func (h *Handler) AutoCompileEvidence(w http.ResponseWriter, r *http.Request) {
	if h.billing == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "billing service not configured")
		return
	}

	disputeID := chi.URLParam(r, "id")
	if disputeID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	ctx := r.Context()

	// Get dispute details
	d, err := h.billing.GetDispute(disputeID)
	if err != nil {
		h.logger.Error("failed to get dispute for auto-evidence", "error", err, "dispute_id", disputeID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get dispute")
		return
	}

	// Auto-compile evidence from billing audit logs and system data
	evidence := &billingService.ChargebackEvidence{
		ProductDescription: "Estara AI - Real estate investment intelligence platform providing market analysis, investment planning, and portfolio optimization tools.",
		CancellationPolicy: "Subscriptions can be canceled at any time from account settings. Cancellation takes effect at the end of the billing period.",
		RefundPolicy:       "Refunds are available within 14 days of purchase for unused services. Pro-rated refunds available for annual subscriptions.",
	}

	// Try to find customer info from related charge
	if d.Charge != nil && d.Charge.BillingDetails != nil {
		if d.Charge.BillingDetails.Name != "" {
			evidence.CustomerName = d.Charge.BillingDetails.Name
		}
		if d.Charge.BillingDetails.Email != "" {
			evidence.CustomerEmail = d.Charge.BillingDetails.Email
		}
	}

	// Look up terms acceptance from audit logs
	var termsNote string
	if d.PaymentIntent != nil {
		// Search for terms acceptance records
		entries, _, _ := h.getAuditLogEntries(ctx, "", "TERMS_ACCEPT", "", 1, 0)
		if len(entries) > 0 {
			termsNote = fmt.Sprintf("Customer accepted Terms of Service at %s", entries[0].CreatedAt.Format(time.RFC3339))
		}
	}
	if termsNote != "" {
		evidence.UncategorizedText = termsNote
	}

	// Submit compiled evidence
	updated, err := h.billing.UpdateDispute(disputeID, evidence)
	if err != nil {
		h.logger.Error("failed to submit auto-compiled evidence", "error", err, "dispute_id", disputeID)
		httputil.Error(w, http.StatusInternalServerError, "failed to submit evidence")
		return
	}

	h.logAdminAudit(ctx, r, "DISPUTE_AUTO_EVIDENCE", "dispute", disputeID, map[string]interface{}{
		"amount":         d.Amount,
		"reason":         string(d.Reason),
		"hasCustomerInfo": evidence.CustomerName != "",
		"hasTermsRecord": termsNote != "",
	})

	httputil.Success(w, map[string]interface{}{
		"disputeId": updated.ID,
		"status":    string(updated.Status),
		"compiled": map[string]interface{}{
			"customerName":       evidence.CustomerName,
			"customerEmail":      evidence.CustomerEmail,
			"productDescription": evidence.ProductDescription != "",
			"cancellationPolicy": evidence.CancellationPolicy != "",
			"refundPolicy":       evidence.RefundPolicy != "",
			"termsRecord":        termsNote != "",
		},
		"message": "Evidence auto-compiled and submitted",
	})
}

// GetDisputeMetrics returns dispute metrics (chargeback rate, win rate, etc.)
func (h *Handler) GetDisputeMetrics(w http.ResponseWriter, r *http.Request) {
	if h.billing == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "billing service not configured")
		return
	}

	// Fetch all disputes to calculate metrics
	disputes, _, err := h.billing.ListDisputes(100, "")
	if err != nil {
		h.logger.Error("failed to get disputes for metrics", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get dispute metrics")
		return
	}

	metrics := DisputeMetricsResponse{}
	now := time.Now()
	ninetyDaysAgo := now.AddDate(0, 0, -90)
	fortyEightHours := now.Add(48 * time.Hour)

	for _, d := range disputes {
		created := time.Unix(d.Created, 0)
		if created.Before(ninetyDaysAgo) {
			continue
		}
		metrics.TotalDisputes++

		switch d.Status {
		case "needs_response", "warning_needs_response":
			metrics.OpenDisputes++
			metrics.AmountAtRisk += d.Amount
			// Check if evidence is due soon
			if d.EvidenceDetails != nil && d.EvidenceDetails.DueBy > 0 {
				dueBy := time.Unix(d.EvidenceDetails.DueBy, 0)
				if dueBy.Before(fortyEightHours) {
					metrics.EvidenceDueSoon++
				}
			}
		case "won":
			metrics.WonDisputes++
		case "lost":
			metrics.LostDisputes++
		case "under_review", "warning_under_review":
			metrics.AmountAtRisk += d.Amount
		}
	}

	// Calculate win rate
	settled := metrics.WonDisputes + metrics.LostDisputes
	if settled > 0 {
		metrics.WinRate = float64(metrics.WonDisputes) / float64(settled) * 100
	}

	// Get total charges in last 90 days to calculate chargeback rate
	ctx := r.Context()
	totalCharges, countErr := h.store.Q().CountPaidInvoicesAfterDate(ctx, pgtype.Timestamp{Time: ninetyDaysAgo, Valid: true})
	if countErr != nil {
		h.logger.Warn("failed to count charges for chargeback rate", "error", countErr)
	}
	if totalCharges > 0 {
		metrics.ChargebackRate = float64(metrics.TotalDisputes) / float64(totalCharges) * 100
	}

	httputil.Success(w, metrics)
}
