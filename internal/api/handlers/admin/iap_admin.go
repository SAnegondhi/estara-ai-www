package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/estara-ai/www/internal/db/queries"
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

	stats, err := h.store.Q().GetIAPStats(ctx)
	if err != nil {
		h.logger.Error("failed to get IAP renewal status", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get IAP renewal status")
		return
	}

	resp := IAPRenewalStatusResponse{
		TotalIAP:     stats.TotalIap,
		ActiveIAP:    stats.ActiveIap,
		ExpiringSoon: stats.ExpiringSoon,
		Expired:      stats.Expired,
		AppleCount:   stats.AppleCount,
		GoogleCount:  stats.GoogleCount,
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

	q := h.store.Q()

	// Query audit logs for IAP-related events
	iapRows, err := q.ListIAPAuditLogs(ctx, queries.ListIAPAuditLogsParams{
		Limit:  int32(pageSize),
		Offset: int32(offset),
	})
	if err != nil {
		h.logger.Warn("failed to get IAP webhook events", "error", err)
		httputil.Success(w, map[string]interface{}{
			"events": []IAPWebhookEventResponse{},
			"total":  0,
		})
		return
	}

	events := make([]IAPWebhookEventResponse, 0, len(iapRows))
	for _, r := range iapRows {
		e := IAPWebhookEventResponse{
			ID: r.ID,
		}
		if r.Action.Valid {
			e.Action = r.Action.String
		}
		if r.Resource.Valid {
			e.Resource = r.Resource.String
		}
		if r.Metadata != nil {
			_ = json.Unmarshal(r.Metadata, &e.Details)
		}
		if r.CreatedAt.Valid {
			e.CreatedAt = r.CreatedAt.Time
		}
		events = append(events, e)
	}

	total, _ := q.CountIAPAuditLogs(ctx)

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

	q := h.store.Q()

	// Verify user has IAP subscription
	iapPlatformVal, err := q.GetUserIAPPlatform(ctx, userID)
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "user not found")
		return
	}
	if !iapPlatformVal.Valid {
		httputil.BadRequest(w, "user does not have an IAP subscription")
		return
	}

	// Downgrade to free tier
	err = q.DowngradeIAPUser(ctx, userID)
	if err != nil {
		h.logger.Error("failed to downgrade IAP subscription", "error", err, "user_id", userID)
		httputil.Error(w, http.StatusInternalServerError, "failed to downgrade subscription")
		return
	}

	// Also update subscription record if exists
	_ = q.CancelIAPSubscriptions(ctx, userID)

	h.logAdminAudit(ctx, r, "ADMIN_USER", "IAP_DOWNGRADE", "iap", userID, map[string]interface{}{
		"previousPlatform": iapPlatformVal.String,
	})

	httputil.Success(w, map[string]interface{}{
		"userId":  userID,
		"message": "IAP subscription downgraded to free tier",
	})
}
