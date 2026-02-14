package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// IAP (In-App Purchase) Monitoring
// ===============================

// IAPRenewalStatusResponse represents IAP subscription overview
type IAPRenewalStatusResponse struct {
	TotalIAP      int64 `json:"totalIap"`
	ActiveIAP     int64 `json:"activeIap"`
	ExpiringSoon  int64 `json:"expiringSoon"`
	Expired       int64 `json:"expired"`
	AppleCount    int64 `json:"appleCount"`
	GoogleCount   int64 `json:"googleCount"`
}

// IAPWebhookEventResponse represents an IAP webhook event from audit logs
type IAPWebhookEventResponse struct {
	ID        string                 `json:"id"`
	Action    string                 `json:"action"`
	Resource  string                 `json:"resource"`
	Details   map[string]interface{} `json:"details,omitempty"`
	CreatedAt time.Time              `json:"createdAt"`
}

// GetIAPRenewalStatus returns IAP subscription status summary
func (h *Handler) GetIAPRenewalStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var resp IAPRenewalStatusResponse

	err := h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE "iapPlatform" IS NOT NULL) as total_iap,
			COUNT(*) FILTER (WHERE "iapPlatform" IS NOT NULL AND "iapExpiresAt" > NOW()) as active_iap,
			COUNT(*) FILTER (WHERE "iapPlatform" IS NOT NULL AND "iapExpiresAt" > NOW() AND "iapExpiresAt" < NOW() + INTERVAL '7 days') as expiring_soon,
			COUNT(*) FILTER (WHERE "iapPlatform" IS NOT NULL AND "iapExpiresAt" <= NOW()) as expired,
			COUNT(*) FILTER (WHERE "iapPlatform" = 'apple') as apple_count,
			COUNT(*) FILTER (WHERE "iapPlatform" = 'google') as google_count
		FROM users
	`).Scan(
		&resp.TotalIAP, &resp.ActiveIAP, &resp.ExpiringSoon,
		&resp.Expired, &resp.AppleCount, &resp.GoogleCount,
	)
	if err != nil {
		h.logger.Error("failed to get IAP renewal status", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get IAP renewal status")
		return
	}

	httputil.Success(w, resp)
}

// GetIAPWebhookEvents returns recent IAP webhook events from audit logs
func (h *Handler) GetIAPWebhookEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	// Query audit logs for IAP-related events
	rows, err := h.db.Main.Query(ctx, `
		SELECT id, action, resource, details, "createdAt"
		FROM admin_audit_log
		WHERE action LIKE 'IAP_%' OR resource = 'iap'
		ORDER BY "createdAt" DESC
		LIMIT $1 OFFSET $2
	`, pageSize, offset)
	if err != nil {
		h.logger.Warn("failed to get IAP webhook events", "error", err)
		httputil.Success(w, map[string]interface{}{
			"events": []IAPWebhookEventResponse{},
			"total":  0,
		})
		return
	}
	defer rows.Close()

	var events []IAPWebhookEventResponse
	for rows.Next() {
		var e IAPWebhookEventResponse
		var detailsJSON []byte
		if err := rows.Scan(&e.ID, &e.Action, &e.Resource, &detailsJSON, &e.CreatedAt); err != nil {
			continue
		}
		if detailsJSON != nil {
			_ = json.Unmarshal(detailsJSON, &e.Details)
		}
		events = append(events, e)
	}

	if events == nil {
		events = []IAPWebhookEventResponse{}
	}

	var total int64
	_ = h.db.Main.QueryRow(ctx, `
		SELECT COUNT(*) FROM admin_audit_log
		WHERE action LIKE 'IAP_%' OR resource = 'iap'
	`).Scan(&total)

	httputil.Success(w, map[string]interface{}{
		"events": events,
		"total":  total,
		"pagination": map[string]interface{}{
			"page":     page,
			"pageSize": pageSize,
			"total":    total,
		},
	})
}

// DowngradeIAPSubscription downgrades an IAP user to free tier
func (h *Handler) DowngradeIAPSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	// Verify user has IAP subscription
	var iapPlatform *string
	err := h.db.Main.QueryRow(ctx, `
		SELECT "iapPlatform" FROM users WHERE id = $1
	`, userID).Scan(&iapPlatform)
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "user not found")
		return
	}
	if iapPlatform == nil {
		httputil.BadRequest(w, "user does not have an IAP subscription")
		return
	}

	// Downgrade to free tier
	_, err = h.db.Main.Exec(ctx, `
		UPDATE users SET
			"subscriptionTier" = 'free',
			"iapPlatform" = NULL,
			"iapExpiresAt" = NULL,
			"updatedAt" = NOW()
		WHERE id = $1
	`, userID)
	if err != nil {
		h.logger.Error("failed to downgrade IAP subscription", "error", err, "user_id", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to downgrade subscription")
		return
	}

	// Also update subscription record if exists
	_, _ = h.db.Main.Exec(ctx, `
		UPDATE subscriptions SET
			status = 'CANCELED',
			tier = 'FREE',
			"updatedAt" = NOW()
		WHERE "userId" = $1 AND status IN ('ACTIVE', 'TRIALING')
	`, userID)

	h.logAdminAudit(ctx, r, "IAP_DOWNGRADE", "iap", userID, map[string]interface{}{
		"previousPlatform": *iapPlatform,
	})

	httputil.Success(w, map[string]interface{}{
		"userId":  userID,
		"message": "IAP subscription downgraded to free tier",
	})
}
