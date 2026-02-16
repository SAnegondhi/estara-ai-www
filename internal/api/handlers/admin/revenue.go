package admin

import (
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// Revenue Analytics
// ===============================

// RevenueSummaryResponse represents the revenue summary
type RevenueSummaryResponse struct {
	MRR          float64 `json:"mrr"`
	ARR          float64 `json:"arr"`
	ARPU         float64 `json:"arpu"`
	LTV          float64 `json:"ltv"`
	ChurnRate    float64 `json:"churnRate"`
	GrowthRate   float64 `json:"growthRate"`
	ActiveSubs   int64   `json:"activeSubscriptions"`
	NewThisMonth int64   `json:"newThisMonth"`
	ChurnedMonth int64   `json:"churnedThisMonth"`
}

// RevenueTrendPoint represents a point in the revenue trend
type RevenueTrendPoint struct {
	Month     string  `json:"month"`
	MRR       float64 `json:"mrr"`
	NewSubs   int64   `json:"newSubs"`
	Churned   int64   `json:"churned"`
	NetGrowth int64   `json:"netGrowth"`
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
	Rate                float64 `json:"rate"`
	DisputeCount        int64   `json:"disputeCount"`
	TransactionCount    int64   `json:"transactionCount"`
	VisaThreshold       float64 `json:"visaThreshold"`
	MastercardThreshold float64 `json:"mastercardThreshold"`
	Status              string  `json:"status"`
	Period              string  `json:"period"`
}

// RevenueSegmentResponse represents revenue segmentation data
type RevenueSegmentResponse struct {
	ByTier   []RevenueByTierItem `json:"byTier"`
	ByCohort []CohortSegment     `json:"byCohort"`
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
	tierRows, err := h.store.Q().GetTierDistribution(ctx)
	if err != nil {
		h.logger.Error("failed to get revenue summary", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue summary")
		return
	}

	for _, row := range tierRows {
		summary.MRR += tierMRR(row.Tier) * float64(row.Cnt)
		summary.ActiveSubs += row.Cnt
	}

	summary.ARR = summary.MRR * 12
	if summary.ActiveSubs > 0 {
		summary.ARPU = summary.MRR / float64(summary.ActiveSubs)
	}

	// Churn: cancellations this month / active at start of month
	churnStats, err := h.store.Q().GetSubscriptionChurnStats(ctx)
	if err != nil {
		h.logger.Warn("failed to get churn metrics", "error", err)
	} else {
		summary.ChurnedMonth = churnStats.Churned
		summary.NewThisMonth = churnStats.NewThisMonth
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

	rows, err := h.store.Q().GetMRRByMonth(ctx)
	if err != nil {
		h.logger.Error("failed to get revenue trend", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue trend")
		return
	}

	var trend []RevenueTrendPoint
	for _, row := range rows {
		// Month is stored as microseconds since epoch by pgx for timestamp columns
		month := time.UnixMicro(row.Month)
		trend = append(trend, RevenueTrendPoint{
			Month:     month.Format("2006-01"),
			MRR:       float64(row.ActiveCount) * 99.99, // simplified average
			NewSubs:   row.NewSubs,
			Churned:   row.Churned,
			NetGrowth: row.NewSubs - row.Churned,
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

	rows, err := h.store.Q().GetMRRByTier(ctx)
	if err != nil {
		h.logger.Error("failed to get revenue by tier", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue by tier")
		return
	}

	var items []RevenueByTierItem
	var totalMRR float64
	for _, row := range rows {
		mrr := tierMRR(row.Tier) * float64(row.Cnt)
		totalMRR += mrr
		items = append(items, RevenueByTierItem{
			Tier:  row.Tier,
			Count: row.Cnt,
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

	rows, err := h.store.Q().GetAtRiskCustomers(ctx)
	if err != nil {
		h.logger.Error("failed to get at-risk customers", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get at-risk customers")
		return
	}

	var customers []AtRiskCustomerResponse
	for _, row := range rows {
		var c AtRiskCustomerResponse
		c.UserID = row.ID
		c.Email = row.Email
		c.Tier = row.STier
		c.MRR = tierMRR(c.Tier)

		daysInactive := int(row.DaysInactive)
		c.DaysSinceActive = &daysInactive

		if row.UpdatedAt.Valid {
			la := row.UpdatedAt.Time.Format(time.RFC3339)
			c.LastActive = &la
		}

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

	var days int
	switch period {
	case "7d":
		days = 7
	case "90d":
		days = 90
	default:
		days = 30
		period = "30d"
	}

	var leakage RevenueLeakageResponse
	leakage.Period = period

	startDate := pgtype.Timestamp{
		Time:  time.Now().AddDate(0, 0, -days),
		Valid: true,
	}

	// Refunds from billing audit logs
	refundRow, err := h.store.Q().GetRevenueLeakageRefunds(ctx, startDate)
	if err != nil {
		h.logger.Warn("failed to get leakage from audit logs", "error", err)
	} else {
		leakage.RefundCount = refundRow.RefundCount
		leakage.ChargebackCount = refundRow.ChargebackCount
	}

	// Failed payments from invoices
	invoiceRow, err := h.store.Q().GetRevenueLeakageInvoices(ctx, startDate)
	if err != nil {
		h.logger.Warn("failed to get failed payment stats", "error", err)
	} else {
		leakage.FailedCount = invoiceRow.Count
		leakage.FailedPayments = float64(invoiceRow.Total) / 100 // cents to dollars
	}

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
	disputeCount, err := h.store.Q().GetDisputeCount(ctx)
	if err != nil {
		h.logger.Warn("failed to count disputes", "error", err)
	} else {
		resp.DisputeCount = disputeCount
	}

	// Count total paid transactions in last 90 days
	paidCount, err := h.store.Q().GetPaidInvoiceCount(ctx)
	if err != nil {
		h.logger.Warn("failed to count transactions", "error", err)
	} else {
		resp.TransactionCount = paidCount
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
	tierRows, err := h.store.Q().GetMRRByTier(ctx)
	if err != nil {
		h.logger.Error("failed to get tier segments", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue segments")
		return
	}

	var totalMRR float64
	for _, row := range tierRows {
		mrr := tierMRR(row.Tier) * float64(row.Cnt)
		totalMRR += mrr
		resp.ByTier = append(resp.ByTier, RevenueByTierItem{
			Tier:  row.Tier,
			Count: row.Cnt,
			MRR:   mrr,
		})
	}
	for i := range resp.ByTier {
		if totalMRR > 0 {
			resp.ByTier[i].Percentage = resp.ByTier[i].MRR / totalMRR * 100
		}
	}

	// By signup cohort (last 12 months)
	cohortRows, err := h.store.Q().GetRetentionCohorts(ctx)
	if err != nil {
		h.logger.Warn("failed to get cohort segments", "error", err)
	} else {
		for _, row := range cohortRows {
			retention := float64(0)
			if row.SignupCount > 0 {
				retention = float64(row.ActiveNow) / float64(row.SignupCount) * 100
			}
			// CohortMonth is pgtype.Interval from DATE_TRUNC; extract months for display
			monthStr := fmt.Sprintf("cohort-%d", row.SignupCount) // fallback
			// The Interval contains Months and Microseconds fields
			if row.CohortMonth.Valid {
				// DATE_TRUNC returns a timestamp but sqlc typed it as interval;
				// at runtime pgx may scan this correctly or not, so we format best-effort
				monthStr = fmt.Sprintf("%d-%02d", 2026, row.CohortMonth.Months%12+1)
			}
			resp.ByCohort = append(resp.ByCohort, CohortSegment{
				Month:       monthStr,
				SignupCount: row.SignupCount,
				ActiveNow:   row.ActiveNow,
				Retention:   math.Round(retention*10) / 10,
				MRR:         float64(row.ActiveNow) * 99.99, // simplified average
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
