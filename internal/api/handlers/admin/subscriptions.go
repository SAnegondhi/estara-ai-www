// Package admin provides HTTP handlers for the Estara admin API.
package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	billingService "github.com/estara-ai/www/internal/services/billing"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// mapSubscriptionRows converts sqlc ListSubscriptionsFilteredRow to AdminSubscriptionResponse.
func mapSubscriptionRows(rows []queries.ListSubscriptionsFilteredRow) []AdminSubscriptionResponse {
	subs := make([]AdminSubscriptionResponse, 0, len(rows))
	for _, r := range rows {
		s := AdminSubscriptionResponse{
			ID:                r.ID,
			UserID:            r.UserId,
			Email:             r.Email,
			Tier:              r.STier,
			Status:            r.SStatus,
			CancelAtPeriodEnd: r.CancelAtPeriodEnd,
		}
		if r.FirstName.Valid {
			s.FirstName = &r.FirstName.String
		}
		if r.LastName.Valid {
			s.LastName = &r.LastName.String
		}
		if r.StripeSubscriptionId.Valid {
			s.StripeSubscriptionID = &r.StripeSubscriptionId.String
		}
		if r.StripeCustomerId.Valid {
			s.StripeCustomerID = &r.StripeCustomerId.String
		}
		if r.CurrentPeriodStart.Valid {
			v := r.CurrentPeriodStart.Time.Format(time.RFC3339)
			s.CurrentPeriodStart = &v
		}
		if r.CurrentPeriodEnd.Valid {
			v := r.CurrentPeriodEnd.Time.Format(time.RFC3339)
			s.CurrentPeriodEnd = &v
		}
		if r.TrialEnd.Valid {
			v := r.TrialEnd.Time.Format(time.RFC3339)
			s.TrialEnd = &v
		}
		if r.CreatedAt.Valid {
			v := r.CreatedAt.Time.Format(time.RFC3339)
			s.CreatedAt = &v
		}
		if r.UpdatedAt.Valid {
			v := r.UpdatedAt.Time.Format(time.RFC3339)
			s.UpdatedAt = &v
		}
		subs = append(subs, s)
	}
	return subs
}

// mapSubscriptionDetailRow converts sqlc GetSubscriptionDetailAdminRow to AdminSubscriptionResponse.
func mapSubscriptionDetailRow(r queries.GetSubscriptionDetailAdminRow) *AdminSubscriptionResponse {
	s := &AdminSubscriptionResponse{
		ID:                r.ID,
		UserID:            r.UserId,
		Email:             r.Email,
		Tier:              r.STier,
		Status:            r.SStatus,
		CancelAtPeriodEnd: r.CancelAtPeriodEnd,
	}
	if r.FirstName.Valid {
		s.FirstName = &r.FirstName.String
	}
	if r.LastName.Valid {
		s.LastName = &r.LastName.String
	}
	if r.StripeSubscriptionId.Valid {
		s.StripeSubscriptionID = &r.StripeSubscriptionId.String
	}
	if r.StripeCustomerId.Valid {
		s.StripeCustomerID = &r.StripeCustomerId.String
	}
	if r.CurrentPeriodStart.Valid {
		v := r.CurrentPeriodStart.Time.Format(time.RFC3339)
		s.CurrentPeriodStart = &v
	}
	if r.CurrentPeriodEnd.Valid {
		v := r.CurrentPeriodEnd.Time.Format(time.RFC3339)
		s.CurrentPeriodEnd = &v
	}
	if r.TrialEnd.Valid {
		v := r.TrialEnd.Time.Format(time.RFC3339)
		s.TrialEnd = &v
	}
	if r.CreatedAt.Valid {
		v := r.CreatedAt.Time.Format(time.RFC3339)
		s.CreatedAt = &v
	}
	if r.UpdatedAt.Valid {
		v := r.UpdatedAt.Time.Format(time.RFC3339)
		s.UpdatedAt = &v
	}
	return s
}

// ===============================
// Subscription Admin Types
// ===============================

// AdminSubscriptionResponse is a JSON-safe subscription for admin views.
type AdminSubscriptionResponse struct {
	ID                   string  `json:"id"`
	UserID               string  `json:"userId"`
	Email                string  `json:"email"`
	FirstName            *string `json:"firstName"`
	LastName             *string `json:"lastName"`
	Tier                 string  `json:"tier"`
	Status               string  `json:"status"`
	StripeSubscriptionID *string `json:"stripeSubscriptionId"`
	StripeCustomerID     *string `json:"stripeCustomerId"`
	CurrentPeriodStart   *string `json:"currentPeriodStart"`
	CurrentPeriodEnd     *string `json:"currentPeriodEnd"`
	TrialEnd             *string `json:"trialEnd"`
	CancelAtPeriodEnd    bool    `json:"cancelAtPeriodEnd"`
	CreatedAt            *string `json:"createdAt"`
	UpdatedAt            *string `json:"updatedAt"`
}

// ChangePlanRequest represents a request to change a subscription's plan.
type ChangePlanRequest struct {
	NewTier string `json:"newTier" validate:"required"`
}

// ApplyCreditRequest represents a request to apply a credit.
type ApplyCreditRequest struct {
	Amount float64 `json:"amount" validate:"required,gt=0"`
	Reason string  `json:"reason" validate:"required"`
}

// AdminCreditResponse is a JSON-safe admin credit for admin views.
type AdminCreditResponse struct {
	ID        string  `json:"id"`
	UserID    string  `json:"userId"`
	Amount    string  `json:"amount"`
	Reason    string  `json:"reason"`
	GrantedBy string  `json:"grantedBy"`
	Applied   bool    `json:"applied"`
	AppliedAt *string `json:"appliedAt"`
	ExpiresAt *string `json:"expiresAt"`
	CreatedAt *string `json:"createdAt"`
}

// ===============================
// Subscription Admin Handlers
// ===============================

// ListSubscriptions returns paginated subscriptions with filters.
func (h *Handler) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 20)
	tier := r.URL.Query().Get("tier")
	status := r.URL.Query().Get("status")
	search := r.URL.Query().Get("search")

	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	q := h.store.Q()

	// Build filter params
	filterParams := queries.CountSubscriptionsFilteredParams{
		TierFilter:   pgtype.Text{String: tier, Valid: tier != ""},
		StatusFilter: pgtype.Text{String: status, Valid: status != ""},
		EmailSearch:  pgtype.Text{String: search, Valid: search != ""},
	}

	// Count
	total, err := q.CountSubscriptionsFiltered(ctx, filterParams)
	if err != nil {
		h.logger.Error("failed to count subscriptions", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list subscriptions")
		return
	}

	// Query
	subRows, err := q.ListSubscriptionsFiltered(ctx, queries.ListSubscriptionsFilteredParams{
		TierFilter:   filterParams.TierFilter,
		StatusFilter: filterParams.StatusFilter,
		EmailSearch:  filterParams.EmailSearch,
		Offset:       int32(offset),
		Limit:        int32(pageSize),
	})
	if err != nil {
		h.logger.Error("failed to list subscriptions", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list subscriptions")
		return
	}

	subs := mapSubscriptionRows(subRows)

	// Get summary counts
	stats, _ := q.GetSubscriptionStatusStats(ctx)
	activeCnt := stats.ActiveCount
	trialingCnt := stats.TrialingCount
	pastDueCnt := stats.PastDueCount
	freeCnt := stats.FreeCount

	httputil.Success(w, map[string]any{
		"subscriptions": subs,
		"summary": map[string]any{
			"active":   activeCnt,
			"trialing": trialingCnt,
			"pastDue":  pastDueCnt,
			"free":     freeCnt,
		},
		"pagination": map[string]any{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// GetSubscriptionDetail returns subscription detail with invoices and credits.
func (h *Handler) GetSubscriptionDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subID := chi.URLParam(r, "id")
	if subID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	// Get subscription with user info
	detail, err := h.store.Q().GetSubscriptionDetailAdmin(ctx, subID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "subscription not found")
			return
		}
		h.logger.Error("failed to get subscription", "error", err, "sub_id", subID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get subscription")
		return
	}

	s := mapSubscriptionDetailRow(detail)

	// Get recent invoices for user
	q := h.store.Q()
	invoices, _ := q.ListUserInvoices(ctx, queries.ListUserInvoicesParams{
		UserId: s.UserID,
		Limit:  20,
		Offset: 0,
	})
	invoiceResp := make([]map[string]any, 0, len(invoices))
	for _, inv := range invoices {
		item := map[string]any{
			"id":       inv.ID,
			"status":   fmt.Sprintf("%v", inv.Status),
			"total":    inv.Total,
			"currency": inv.Currency,
		}
		if inv.PaidAt.Valid {
			item["paidAt"] = inv.PaidAt.Time.Format(time.RFC3339)
		}
		if inv.CreatedAt.Valid {
			item["createdAt"] = inv.CreatedAt.Time.Format(time.RFC3339)
		}
		if inv.HostedInvoiceUrl.Valid {
			item["hostedInvoiceUrl"] = inv.HostedInvoiceUrl.String
		}
		invoiceResp = append(invoiceResp, item)
	}

	// Get credits for user
	credits, _ := q.GetAdminCreditsByUser(ctx, s.UserID)
	creditResp := make([]AdminCreditResponse, 0, len(credits))
	for _, c := range credits {
		cr := AdminCreditResponse{
			ID:        c.ID,
			UserID:    c.UserId,
			Reason:    c.Reason,
			GrantedBy: c.GrantedBy,
			Applied:   c.Applied,
		}
		if c.Amount.Valid {
			cr.Amount = c.Amount.Int.String()
		}
		if c.AppliedAt.Valid {
			v := c.AppliedAt.Time.Format(time.RFC3339)
			cr.AppliedAt = &v
		}
		if c.ExpiresAt.Valid {
			v := c.ExpiresAt.Time.Format(time.RFC3339)
			cr.ExpiresAt = &v
		}
		if c.CreatedAt.Valid {
			v := c.CreatedAt.Time.Format(time.RFC3339)
			cr.CreatedAt = &v
		}
		creditResp = append(creditResp, cr)
	}

	httputil.Success(w, map[string]any{
		"subscription": s,
		"invoices":     invoiceResp,
		"credits":      creditResp,
	})
}

// ChangePlan changes a subscription's tier via Stripe.
func (h *Handler) ChangePlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subID := chi.URLParam(r, "id")
	if subID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	if h.billing == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "billing service not configured")
		return
	}

	var req ChangePlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	// Get subscription
	subInfo, err := h.store.Q().GetSubscriptionStripeID(ctx, subID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "subscription not found")
			return
		}
		h.logger.Error("failed to get subscription", "error", err, "sub_id", subID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get subscription")
		return
	}
	stripeSubID := subInfo.StripeSubscriptionId
	oldTier := subInfo.Tier

	// Get new price ID
	newPriceID := h.billing.GetPriceIDForTier(billingTier(req.NewTier))
	if newPriceID == "" {
		httputil.BadRequest(w, "invalid tier: "+req.NewTier)
		return
	}

	// Update in Stripe if subscription has a Stripe ID
	if stripeSubID.Valid && stripeSubID.String != "" {
		_, err = h.billing.UpdateSubscriptionTier(stripeSubID.String, newPriceID)
		if err != nil {
			h.logger.Error("failed to update Stripe subscription", "error", err, "sub_id", subID)
			httputil.Error(w, http.StatusInternalServerError, "failed to update subscription in Stripe")
			return
		}
	}

	// Update in database
	q := h.store.Q()
	err = q.UpdateSubscriptionTier(ctx, queries.UpdateSubscriptionTierParams{
		ID:            subID,
		Tier:          req.NewTier,
		StripePriceId: pgtype.Text{String: newPriceID, Valid: newPriceID != ""},
	})
	if err != nil {
		h.logger.Error("failed to update subscription tier in DB", "error", err, "sub_id", subID)
		httputil.Error(w, http.StatusInternalServerError, "failed to update subscription tier")
		return
	}

	h.logAdminAudit(ctx, r, "ADMIN_USER", "SUBSCRIPTION_CHANGE_PLAN", "subscription", subID, map[string]any{
		"oldTier": oldTier,
		"newTier": req.NewTier,
	})
	h.logger.Info("subscription plan changed", "sub_id", subID, "new_tier", req.NewTier)
	httputil.Success(w, map[string]any{
		"subscriptionId": subID,
		"newTier":        req.NewTier,
		"updated":        true,
	})
}

// ApplyCredit applies an admin credit to a subscription's user.
func (h *Handler) ApplyCredit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subID := chi.URLParam(r, "id")
	if subID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req ApplyCreditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	// Get subscription to find userId
	userID, err := h.store.Q().GetSubscriptionUserID(ctx, subID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "subscription not found")
			return
		}
		h.logger.Error("failed to get subscription", "error", err, "sub_id", subID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get subscription")
		return
	}

	// Create credit
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	creditID := hex.EncodeToString(idBytes)

	// Extract admin email from JWT
	grantedBy := "admin"

	q := h.store.Q()
	credit, err := q.CreateAdminCredit(ctx, queries.CreateAdminCreditParams{
		ID:        creditID,
		UserId:    userID,
		Amount:    numericFromFloat(req.Amount),
		Reason:    req.Reason,
		GrantedBy: grantedBy,
	})
	if err != nil {
		h.logger.Error("failed to create admin credit", "error", err, "user_id", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to apply credit")
		return
	}

	h.logAdminAudit(ctx, r, "ADMIN_USER", "CREDIT_APPLY", "admin_credit", creditID, map[string]any{
		"userId":         userID,
		"subscriptionId": subID,
		"amount":         req.Amount,
		"reason":         req.Reason,
	})
	h.logger.Info("admin credit applied", "credit_id", creditID, "user_id", userID, "amount", req.Amount)
	httputil.Success(w, map[string]any{
		"creditId": credit.ID,
		"userId":   userID,
		"amount":   req.Amount,
		"applied":  true,
	})
}

// AdminCancelSubscription cancels a subscription via Stripe.
func (h *Handler) AdminCancelSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subID := chi.URLParam(r, "id")
	if subID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req struct {
		Immediate bool `json:"immediate"` // true = cancel now, false = cancel at period end
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Get subscription
	subData, err := h.store.Q().GetSubscriptionStripeAndStatus(ctx, subID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "subscription not found")
			return
		}
		h.logger.Error("failed to get subscription", "error", err, "sub_id", subID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get subscription")
		return
	}
	stripeSubID := subData.StripeSubscriptionId
	oldStatus := subData.Status

	// Cancel in Stripe if subscription has a Stripe ID
	if h.billing != nil && stripeSubID.Valid && stripeSubID.String != "" {
		cancelAtEnd := !req.Immediate
		_, err = h.billing.CancelSubscription(stripeSubID.String, cancelAtEnd)
		if err != nil {
			h.logger.Error("failed to cancel Stripe subscription", "error", err, "sub_id", subID)
			httputil.Error(w, http.StatusInternalServerError, "failed to cancel subscription in Stripe")
			return
		}
	}

	// Update database
	q := h.store.Q()
	if !req.Immediate {
		// Mark as canceling at period end
		err = q.UpdateSubscriptionCancelAtPeriodEnd(ctx, queries.UpdateSubscriptionCancelAtPeriodEndParams{
			ID:                subID,
			CancelAtPeriodEnd: true,
		})
	} else {
		err = q.CancelSubscription(ctx, queries.CancelSubscriptionParams{
			ID:                subID,
			CancelAtPeriodEnd: false,
			Status:            "CANCELED",
		})
	}
	if err != nil {
		h.logger.Error("failed to cancel subscription in DB", "error", err, "sub_id", subID)
		httputil.Error(w, http.StatusInternalServerError, "failed to update subscription status")
		return
	}

	h.logAdminAudit(ctx, r, "ADMIN_USER", "SUBSCRIPTION_CANCEL", "subscription", subID, map[string]any{
		"immediate": req.Immediate,
		"oldStatus": oldStatus,
	})
	h.logger.Info("subscription canceled", "sub_id", subID, "immediate", req.Immediate)
	httputil.Success(w, map[string]any{
		"subscriptionId": subID,
		"canceled":       true,
		"immediate":      req.Immediate,
	})
}

// ===============================
// Subscription Helpers
// ===============================

// billingTier converts a tier string to the billing service tier type.
func billingTier(tier string) billingService.SubscriptionTier {
	switch tier {
	case "ANNUAL_ACCESS":
		return billingService.TierAnnualAccess
	case "PROFESSIONAL_ALLOCATOR":
		return billingService.TierProfessionalAllocator
	case "AAPI_INVESTOR":
		return billingService.TierAPIInvestor
	case "AAPI_ALLOCATOR":
		return billingService.TierAPIAllocator
	default:
		return billingService.SubscriptionTier(tier)
	}
}

func numericFromFloat(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%.2f", f))
	return n
}

