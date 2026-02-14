package admin

import (
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// Revenue Analytics
// ===============================

// RevenueSummaryResponse represents the revenue summary
type RevenueSummaryResponse struct {
	MRR           float64 `json:"mrr"`
	ARR           float64 `json:"arr"`
	ARPU          float64 `json:"arpu"`
	LTV           float64 `json:"ltv"`
	ChurnRate     float64 `json:"churnRate"`
	GrowthRate    float64 `json:"growthRate"`
	ActiveSubs    int64   `json:"activeSubscriptions"`
	NewThisMonth  int64   `json:"newThisMonth"`
	ChurnedMonth  int64   `json:"churnedThisMonth"`
}

// RevenueTrendPoint represents a point in the revenue trend
type RevenueTrendPoint struct {
	Month    string  `json:"month"`
	MRR      float64 `json:"mrr"`
	NewSubs  int64   `json:"newSubs"`
	Churned  int64   `json:"churned"`
	NetGrowth int64  `json:"netGrowth"`
}

// RevenueByTierItem represents revenue breakdown by tier
type RevenueByTierItem struct {
	Tier       string  `json:"tier"`
	Count      int64   `json:"count"`
	MRR        float64 `json:"mrr"`
	Percentage float64 `json:"percentage"`
}

// AtRiskCustomerResponse represents an at-risk customer
type AtRiskCustomerResponse struct {
	UserID          string  `json:"userId"`
	Email           string  `json:"email"`
	Tier            string  `json:"tier"`
	RiskLevel       string  `json:"riskLevel"`
	RiskScore       int     `json:"riskScore"`
	Reason          string  `json:"reason"`
	LastActive      *string `json:"lastActive,omitempty"`
	DaysSinceActive *int    `json:"daysSinceActive,omitempty"`
	MRR             float64 `json:"mrr"`
}

// RevenueLeakageResponse represents revenue leakage
type RevenueLeakageResponse struct {
	TotalLeakage    float64 `json:"totalLeakage"`
	Refunds         float64 `json:"refunds"`
	RefundCount     int64   `json:"refundCount"`
	Chargebacks     float64 `json:"chargebacks"`
	ChargebackCount int64   `json:"chargebackCount"`
	FailedPayments  float64 `json:"failedPayments"`
	FailedCount     int64   `json:"failedCount"`
	Period          string  `json:"period"`
}

// ChargebackRateResponse represents the chargeback rate
type ChargebackRateResponse struct {
	Rate              float64 `json:"rate"`
	DisputeCount      int64   `json:"disputeCount"`
	TransactionCount  int64   `json:"transactionCount"`
	VisaThreshold     float64 `json:"visaThreshold"`
	MastercardThreshold float64 `json:"mastercardThreshold"`
	Status            string  `json:"status"`
	Period            string  `json:"period"`
}

// RevenueSegmentResponse represents revenue segmentation data
type RevenueSegmentResponse struct {
	ByTier   []RevenueByTierItem  `json:"byTier"`
	ByCohort []CohortSegment      `json:"byCohort"`
}

// CohortSegment represents a signup month cohort
type CohortSegment struct {
	Month       string  `json:"month"`
	SignupCount int64   `json:"signupCount"`
	ActiveNow   int64   `json:"activeNow"`
	Retention   float64 `json:"retention"`
	MRR         float64 `json:"mrr"`
}

// tierMRR returns the monthly revenue for a subscription tier
func tierMRR(tier string) float64 {
	switch tier {
	case "ANNUAL_ACCESS":
		return 99.99
	case "PROFESSIONAL_ALLOCATOR":
		return 149.99
	case "AAPI_INVESTOR", "API_INVESTOR":
		return 79.99
	case "AAPI_ALLOCATOR", "API_ALLOCATOR":
		return 199.99
	case "INVESTOR":
		return 29.99
	case "PROFESSIONAL":
		return 49.99
	default:
		return 0
	}
}

// GetRevenueSummary returns MRR, ARR, ARPU, LTV, churn, growth
func (h *Handler) GetRevenueSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var summary RevenueSummaryResponse

	// Calculate MRR from active subscriptions
	rows, err := h.db.Main.Query(ctx, `
		SELECT tier, COUNT(*) as cnt
		FROM subscriptions
		WHERE status IN ('ACTIVE', 'TRIALING')
		GROUP BY tier
	`)
	if err != nil {
		h.logger.Error("failed to get revenue summary", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue summary")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var tier string
		var count int64
		if err := rows.Scan(&tier, &count); err != nil {
			continue
		}
		summary.MRR += tierMRR(tier) * float64(count)
		summary.ActiveSubs += count
	}

	summary.ARR = summary.MRR * 12
	if summary.ActiveSubs > 0 {
		summary.ARPU = summary.MRR / float64(summary.ActiveSubs)
	}

	// Churn: cancellations this month / active at start of month
	err = h.db.Main.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'CANCELED' AND "updatedAt" >= DATE_TRUNC('month', CURRENT_DATE)) as churned,
			COUNT(*) FILTER (WHERE "createdAt" >= DATE_TRUNC('month', CURRENT_DATE) AND status IN ('ACTIVE', 'TRIALING')) as new_this_month
		FROM subscriptions
	`).Scan(&summary.ChurnedMonth, &summary.NewThisMonth)
	if err != nil {
		h.logger.Warn("failed to get churn metrics", "error", err)
	}

	// Active at start of month = current active + churned - new
	startOfMonth := summary.ActiveSubs + summary.ChurnedMonth - summary.NewThisMonth
	if startOfMonth > 0 {
		summary.ChurnRate = float64(summary.ChurnedMonth) / float64(startOfMonth) * 100
	}

	// LTV = ARPU / monthly churn rate (as decimal)
	monthlyChurn := summary.ChurnRate / 100
	if monthlyChurn > 0 {
		summary.LTV = summary.ARPU / monthlyChurn
	}

	// Growth rate (month over month)
	if startOfMonth > 0 {
		summary.GrowthRate = float64(summary.NewThisMonth-summary.ChurnedMonth) / float64(startOfMonth) * 100
	}

	httputil.Success(w, summary)
}

// GetRevenueTrend returns monthly revenue time series (12 months)
func (h *Handler) GetRevenueTrend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Main.Query(ctx, `
		WITH months AS (
			SELECT generate_series(
				DATE_TRUNC('month', CURRENT_DATE - INTERVAL '11 months'),
				DATE_TRUNC('month', CURRENT_DATE),
				'1 month'::interval
			) AS month
		)
		SELECT
			m.month,
			COUNT(*) FILTER (WHERE s."createdAt" < m.month + INTERVAL '1 month' AND (s.status IN ('ACTIVE','TRIALING') OR (s.status = 'CANCELED' AND s."updatedAt" >= m.month + INTERVAL '1 month'))) as active_count,
			COUNT(*) FILTER (WHERE s."createdAt" >= m.month AND s."createdAt" < m.month + INTERVAL '1 month') as new_subs,
			COUNT(*) FILTER (WHERE s.status = 'CANCELED' AND s."updatedAt" >= m.month AND s."updatedAt" < m.month + INTERVAL '1 month') as churned
		FROM months m
		LEFT JOIN subscriptions s ON TRUE
		GROUP BY m.month
		ORDER BY m.month
	`)
	if err != nil {
		h.logger.Error("failed to get revenue trend", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue trend")
		return
	}
	defer rows.Close()

	var trend []RevenueTrendPoint
	for rows.Next() {
		var month time.Time
		var activeCount, newSubs, churned int64
		if err := rows.Scan(&month, &activeCount, &newSubs, &churned); err != nil {
			continue
		}
		trend = append(trend, RevenueTrendPoint{
			Month:     month.Format("2006-01"),
			MRR:       float64(activeCount) * 99.99, // simplified average
			NewSubs:   newSubs,
			Churned:   churned,
			NetGrowth: newSubs - churned,
		})
	}

	if trend == nil {
		trend = []RevenueTrendPoint{}
	}

	httputil.Success(w, map[string]interface{}{
		"trend": trend,
	})
}

// GetRevenueByTier returns revenue breakdown by subscription tier
func (h *Handler) GetRevenueByTier(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Main.Query(ctx, `
		SELECT tier, COUNT(*) as cnt
		FROM subscriptions
		WHERE status IN ('ACTIVE', 'TRIALING')
		GROUP BY tier
		ORDER BY cnt DESC
	`)
	if err != nil {
		h.logger.Error("failed to get revenue by tier", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue by tier")
		return
	}
	defer rows.Close()

	var items []RevenueByTierItem
	var totalMRR float64
	for rows.Next() {
		var tier string
		var count int64
		if err := rows.Scan(&tier, &count); err != nil {
			continue
		}
		mrr := tierMRR(tier) * float64(count)
		totalMRR += mrr
		items = append(items, RevenueByTierItem{
			Tier:  tier,
			Count: count,
			MRR:   mrr,
		})
	}

	// Calculate percentages
	for i := range items {
		if totalMRR > 0 {
			items[i].Percentage = items[i].MRR / totalMRR * 100
		}
	}

	if items == nil {
		items = []RevenueByTierItem{}
	}

	httputil.Success(w, map[string]interface{}{
		"tiers":    items,
		"totalMRR": totalMRR,
	})
}

// GetAtRiskCustomers returns customers at risk of churning
func (h *Handler) GetAtRiskCustomers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Main.Query(ctx, `
		SELECT
			u.id, u.email, s.tier, u."updatedAt", s.status,
			EXTRACT(DAY FROM NOW() - u."updatedAt")::int as days_inactive
		FROM users u
		JOIN subscriptions s ON u.id = s."userId"
		WHERE s.status IN ('ACTIVE', 'TRIALING')
		ORDER BY u."updatedAt" ASC
		LIMIT 50
	`)
	if err != nil {
		h.logger.Error("failed to get at-risk customers", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get at-risk customers")
		return
	}
	defer rows.Close()

	var customers []AtRiskCustomerResponse
	for rows.Next() {
		var c AtRiskCustomerResponse
		var lastActive time.Time
		var daysInactive int
		var status string
		if err := rows.Scan(&c.UserID, &c.Email, &c.Tier, &lastActive, &status, &daysInactive); err != nil {
			continue
		}
		la := lastActive.Format(time.RFC3339)
		c.LastActive = &la
		c.DaysSinceActive = &daysInactive
		c.MRR = tierMRR(c.Tier)

		// Risk scoring heuristic
		if daysInactive > 30 {
			c.RiskLevel = "CRITICAL"
			c.RiskScore = 90
			c.Reason = "No activity for 30+ days"
		} else if daysInactive > 14 {
			c.RiskLevel = "HIGH"
			c.RiskScore = 70
			c.Reason = "No activity for 14+ days"
		} else if daysInactive > 7 {
			c.RiskLevel = "MEDIUM"
			c.RiskScore = 40
			c.Reason = "No activity for 7+ days"
		} else {
			continue // Not at risk, skip
		}

		customers = append(customers, c)
	}

	if customers == nil {
		customers = []AtRiskCustomerResponse{}
	}

	// Calculate total MRR at risk
	var mrrAtRisk float64
	for _, c := range customers {
		mrrAtRisk += c.MRR
	}

	httputil.Success(w, map[string]interface{}{
		"customers": customers,
		"total":     len(customers),
		"mrrAtRisk": mrrAtRisk,
	})
}

// GetRevenueLeakage returns revenue leakage (refunds, chargebacks, failed payments)
func (h *Handler) GetRevenueLeakage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "30d"
	}

	var interval string
	switch period {
	case "7d":
		interval = "7 days"
	case "90d":
		interval = "90 days"
	default:
		interval = "30 days"
		period = "30d"
	}

	var leakage RevenueLeakageResponse
	leakage.Period = period

	// Refunds from billing audit logs
	query := fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE action = 'STRIPE_REFUND') as refund_count,
			COUNT(*) FILTER (WHERE action = 'DISPUTE_CREATED' OR action LIKE 'DISPUTE_%%') as chargeback_count
		FROM admin_audit_log
		WHERE "createdAt" >= NOW() - INTERVAL '%s'
	`, interval)
	err := h.db.Main.QueryRow(ctx, query).Scan(&leakage.RefundCount, &leakage.ChargebackCount)
	if err != nil {
		h.logger.Warn("failed to get leakage from audit logs", "error", err)
	}

	// Failed payments from invoices
	query = fmt.Sprintf(`
		SELECT COUNT(*), COALESCE(SUM("amountDue"), 0)
		FROM invoices
		WHERE status IN ('uncollectible', 'void')
		AND "createdAt" >= NOW() - INTERVAL '%s'
	`, interval)
	err = h.db.Main.QueryRow(ctx, query).Scan(&leakage.FailedCount, &leakage.FailedPayments)
	if err != nil {
		h.logger.Warn("failed to get failed payment stats", "error", err)
	}
	leakage.FailedPayments = leakage.FailedPayments / 100 // cents to dollars

	leakage.TotalLeakage = leakage.Refunds + leakage.Chargebacks + leakage.FailedPayments

	httputil.Success(w, leakage)
}

// GetChargebackRate returns the rolling chargeback rate
func (h *Handler) GetChargebackRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var resp ChargebackRateResponse
	resp.Period = "90d"
	resp.VisaThreshold = 0.90
	resp.MastercardThreshold = 1.00

	// Count disputes in last 90 days
	err := h.db.Main.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM admin_audit_log
		WHERE action LIKE 'DISPUTE_%'
		AND "createdAt" >= NOW() - INTERVAL '90 days'
	`).Scan(&resp.DisputeCount)
	if err != nil {
		h.logger.Warn("failed to count disputes", "error", err)
	}

	// Count total paid transactions in last 90 days
	err = h.db.Main.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM invoices
		WHERE status = 'paid'
		AND "createdAt" >= NOW() - INTERVAL '90 days'
	`).Scan(&resp.TransactionCount)
	if err != nil {
		h.logger.Warn("failed to count transactions", "error", err)
	}

	if resp.TransactionCount > 0 {
		resp.Rate = float64(resp.DisputeCount) / float64(resp.TransactionCount) * 100
	}

	// Status based on network thresholds
	resp.Rate = math.Round(resp.Rate*100) / 100
	if resp.Rate >= resp.VisaThreshold {
		resp.Status = "critical"
	} else if resp.Rate >= resp.VisaThreshold*0.75 {
		resp.Status = "warning"
	} else {
		resp.Status = "healthy"
	}

	httputil.Success(w, resp)
}

// GetRevenueSegments returns revenue segmentation data (by tier and by signup cohort)
func (h *Handler) GetRevenueSegments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var resp RevenueSegmentResponse

	// By tier (reuse logic)
	tierRows, err := h.db.Main.Query(ctx, `
		SELECT tier, COUNT(*) as cnt
		FROM subscriptions
		WHERE status IN ('ACTIVE', 'TRIALING')
		GROUP BY tier
		ORDER BY cnt DESC
	`)
	if err != nil {
		h.logger.Error("failed to get tier segments", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue segments")
		return
	}
	defer tierRows.Close()

	var totalMRR float64
	for tierRows.Next() {
		var tier string
		var count int64
		if err := tierRows.Scan(&tier, &count); err != nil {
			continue
		}
		mrr := tierMRR(tier) * float64(count)
		totalMRR += mrr
		resp.ByTier = append(resp.ByTier, RevenueByTierItem{
			Tier:  tier,
			Count: count,
			MRR:   mrr,
		})
	}
	for i := range resp.ByTier {
		if totalMRR > 0 {
			resp.ByTier[i].Percentage = resp.ByTier[i].MRR / totalMRR * 100
		}
	}

	// By signup cohort (last 12 months)
	cohortRows, err := h.db.Main.Query(ctx, `
		WITH cohorts AS (
			SELECT
				DATE_TRUNC('month', "createdAt") as cohort_month,
				COUNT(*) as signup_count,
				COUNT(*) FILTER (WHERE status IN ('ACTIVE', 'TRIALING')) as active_now
			FROM subscriptions
			WHERE "createdAt" >= DATE_TRUNC('month', CURRENT_DATE - INTERVAL '11 months')
			GROUP BY DATE_TRUNC('month', "createdAt")
			ORDER BY cohort_month
		)
		SELECT cohort_month, signup_count, active_now FROM cohorts
	`)
	if err != nil {
		h.logger.Warn("failed to get cohort segments", "error", err)
	} else {
		defer cohortRows.Close()
		for cohortRows.Next() {
			var month time.Time
			var signupCount, activeNow int64
			if err := cohortRows.Scan(&month, &signupCount, &activeNow); err != nil {
				continue
			}
			retention := float64(0)
			if signupCount > 0 {
				retention = float64(activeNow) / float64(signupCount) * 100
			}
			resp.ByCohort = append(resp.ByCohort, CohortSegment{
				Month:       month.Format("2006-01"),
				SignupCount: signupCount,
				ActiveNow:   activeNow,
				Retention:   math.Round(retention*10) / 10,
				MRR:         float64(activeNow) * 99.99, // simplified average
			})
		}
	}

	if resp.ByTier == nil {
		resp.ByTier = []RevenueByTierItem{}
	}
	if resp.ByCohort == nil {
		resp.ByCohort = []CohortSegment{}
	}

	httputil.Success(w, resp)
}

// exportCSV is a helper to write CSV data to the response
func exportCSV(w http.ResponseWriter, filename string, headers []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

	writer := csv.NewWriter(w)
	_ = writer.Write(headers)
	for _, row := range rows {
		_ = writer.Write(row)
	}
	writer.Flush()
}
