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

	// Build dynamic query
	whereClause := "1=1"
	args := make([]any, 0)
	argIndex := 1

	if tier != "" {
		whereClause += fmt.Sprintf(` AND s.tier::text = $%d`, argIndex)
		args = append(args, tier)
		argIndex++
	}
	if status != "" {
		whereClause += fmt.Sprintf(` AND s.status::text = $%d`, argIndex)
		args = append(args, status)
		argIndex++
	}
	if search != "" {
		whereClause += fmt.Sprintf(` AND LOWER(u.email) LIKE $%d`, argIndex)
		args = append(args, "%"+search+"%")
		argIndex++
	}

	// Count
	var total int64
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM subscriptions s JOIN "User" u ON s."userId" = u.id WHERE %s`, whereClause)
	err := h.db.Main.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		h.logger.Error("failed to count subscriptions", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list subscriptions")
		return
	}

	// Query
	args = append(args, pageSize, offset)
	query := fmt.Sprintf(`
		SELECT s.id, s."userId", u.email, u."firstName", u."lastName",
			s.tier::text, s.status::text, s."stripeSubscriptionId", s."stripeCustomerId",
			s."currentPeriodStart", s."currentPeriodEnd", s."trialEnd",
			s."cancelAtPeriodEnd", s."createdAt", s."updatedAt"
		FROM subscriptions s
		JOIN "User" u ON s."userId" = u.id
		WHERE %s
		ORDER BY s."createdAt" DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	rows, err := h.db.Main.Query(ctx, query, args...)
	if err != nil {
		h.logger.Error("failed to list subscriptions", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list subscriptions")
		return
	}
	defer rows.Close()

	subs := scanSubscriptionRows(rows, h.logger)

	// Get summary counts
	var activeCnt, trialingCnt, pastDueCnt, freeCnt int64
	_ = h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status::text = 'ACTIVE') AS active_count,
			COUNT(*) FILTER (WHERE status::text = 'TRIALING') AS trialing_count,
			COUNT(*) FILTER (WHERE status::text = 'PAST_DUE') AS past_due_count,
			COUNT(*) FILTER (WHERE status::text = 'FREE') AS free_count
		FROM subscriptions
	`).Scan(&activeCnt, &trialingCnt, &pastDueCnt, &freeCnt)

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
	row := h.db.Main.QueryRow(ctx, `
		SELECT s.id, s."userId", u.email, u."firstName", u."lastName",
			s.tier::text, s.status::text, s."stripeSubscriptionId", s."stripeCustomerId",
			s."currentPeriodStart", s."currentPeriodEnd", s."trialEnd",
			s."cancelAtPeriodEnd", s."createdAt", s."updatedAt"
		FROM subscriptions s
		JOIN "User" u ON s."userId" = u.id
		WHERE s.id = $1
	`, subID)

	s, err := scanSubscriptionRow(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "subscription not found")
			return
		}
		h.logger.Error("failed to get subscription", "error", err, "sub_id", subID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get subscription")
		return
	}

	// Get recent invoices for user
	q := queries.New(h.db.Main)
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
	var stripeSubID pgtype.Text
	var oldTier string
	err := h.db.Main.QueryRow(ctx, `
		SELECT "stripeSubscriptionId", tier::text FROM subscriptions WHERE id = $1
	`, subID).Scan(&stripeSubID, &oldTier)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "subscription not found")
			return
		}
		h.logger.Error("failed to get subscription", "error", err, "sub_id", subID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get subscription")
		return
	}

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
	q := queries.New(h.db.Main)
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

	h.logAdminAudit(ctx, r, "SUBSCRIPTION_CHANGE_PLAN", "subscription", subID, map[string]any{
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
	var userID string
	err := h.db.Main.QueryRow(ctx, `SELECT "userId" FROM subscriptions WHERE id = $1`, subID).Scan(&userID)
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

	q := queries.New(h.db.Main)
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

	h.logAdminAudit(ctx, r, "CREDIT_APPLY", "admin_credit", creditID, map[string]any{
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
	var stripeSubID pgtype.Text
	var oldStatus string
	err := h.db.Main.QueryRow(ctx, `
		SELECT "stripeSubscriptionId", status::text FROM subscriptions WHERE id = $1
	`, subID).Scan(&stripeSubID, &oldStatus)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Error(w, http.StatusNotFound, "subscription not found")
			return
		}
		h.logger.Error("failed to get subscription", "error", err, "sub_id", subID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get subscription")
		return
	}

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
	q := queries.New(h.db.Main)
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

	h.logAdminAudit(ctx, r, "SUBSCRIPTION_CANCEL", "subscription", subID, map[string]any{
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

func scanSubscriptionRow(row pgx.Row) (*AdminSubscriptionResponse, error) {
	var s AdminSubscriptionResponse
	var periodStart, periodEnd, trialEnd, createdAt, updatedAt pgtype.Timestamp
	var stripeSub, stripeCust pgtype.Text
	err := row.Scan(
		&s.ID, &s.UserID, &s.Email, &s.FirstName, &s.LastName,
		&s.Tier, &s.Status, &stripeSub, &stripeCust,
		&periodStart, &periodEnd, &trialEnd,
		&s.CancelAtPeriodEnd, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if stripeSub.Valid {
		s.StripeSubscriptionID = &stripeSub.String
	}
	if stripeCust.Valid {
		s.StripeCustomerID = &stripeCust.String
	}
	if periodStart.Valid {
		v := periodStart.Time.Format(time.RFC3339)
		s.CurrentPeriodStart = &v
	}
	if periodEnd.Valid {
		v := periodEnd.Time.Format(time.RFC3339)
		s.CurrentPeriodEnd = &v
	}
	if trialEnd.Valid {
		v := trialEnd.Time.Format(time.RFC3339)
		s.TrialEnd = &v
	}
	if createdAt.Valid {
		v := createdAt.Time.Format(time.RFC3339)
		s.CreatedAt = &v
	}
	if updatedAt.Valid {
		v := updatedAt.Time.Format(time.RFC3339)
		s.UpdatedAt = &v
	}
	return &s, nil
}

func scanSubscriptionRows(rows pgx.Rows, logger interface{ Warn(string, ...any) }) []AdminSubscriptionResponse {
	var subs []AdminSubscriptionResponse
	for rows.Next() {
		var s AdminSubscriptionResponse
		var periodStart, periodEnd, trialEnd, createdAt, updatedAt pgtype.Timestamp
		var stripeSub, stripeCust pgtype.Text
		err := rows.Scan(
			&s.ID, &s.UserID, &s.Email, &s.FirstName, &s.LastName,
			&s.Tier, &s.Status, &stripeSub, &stripeCust,
			&periodStart, &periodEnd, &trialEnd,
			&s.CancelAtPeriodEnd, &createdAt, &updatedAt,
		)
		if err != nil {
			logger.Warn("failed to scan subscription", "error", err)
			continue
		}
		if stripeSub.Valid {
			s.StripeSubscriptionID = &stripeSub.String
		}
		if stripeCust.Valid {
			s.StripeCustomerID = &stripeCust.String
		}
		if periodStart.Valid {
			v := periodStart.Time.Format(time.RFC3339)
			s.CurrentPeriodStart = &v
		}
		if periodEnd.Valid {
			v := periodEnd.Time.Format(time.RFC3339)
			s.CurrentPeriodEnd = &v
		}
		if trialEnd.Valid {
			v := trialEnd.Time.Format(time.RFC3339)
			s.TrialEnd = &v
		}
		if createdAt.Valid {
			v := createdAt.Time.Format(time.RFC3339)
			s.CreatedAt = &v
		}
		if updatedAt.Valid {
			v := updatedAt.Time.Format(time.RFC3339)
			s.UpdatedAt = &v
		}
		subs = append(subs, s)
	}
	if subs == nil {
		subs = []AdminSubscriptionResponse{}
	}
	return subs
}
