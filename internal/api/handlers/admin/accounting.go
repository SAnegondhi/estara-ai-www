package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// Accounting & Profitability
// ===============================

// JournalEntryResponse represents a double-entry journal entry
type JournalEntryResponse struct {
	Month         string  `json:"month"`
	Description   string  `json:"description"`
	DebitAccount  string  `json:"debitAccount"`
	CreditAccount string  `json:"creditAccount"`
	Amount        float64 `json:"amount"`
}

// DeferredRevenueItem represents deferred revenue for an annual subscription
type DeferredRevenueItem struct {
	SubscriptionID   string  `json:"subscriptionId"`
	Email            string  `json:"email"`
	Tier             string  `json:"tier"`
	TotalAmount      float64 `json:"totalAmount"`
	RecognizedAmount float64 `json:"recognizedAmount"`
	DeferredAmount   float64 `json:"deferredAmount"`
	StartDate        string  `json:"startDate"`
	EndDate          string  `json:"endDate"`
	MonthsTotal      int     `json:"monthsTotal"`
	MonthsRecognized int     `json:"monthsRecognized"`
}

// VendorExpenseItem represents a vendor expense line item
type VendorExpenseItem struct {
	VendorID    string  `json:"vendorId"`
	VendorName  string  `json:"vendorName"`
	Category    string  `json:"category"`
	MonthlyCost float64 `json:"monthlyCost"`
	AnnualCost  float64 `json:"annualCost"`
}

// ProfitabilityResponse represents profitability data
type ProfitabilityResponse struct {
	GrossRevenue  float64          `json:"grossRevenue"`
	TotalCosts    float64          `json:"totalCosts"`
	GrossProfit   float64          `json:"grossProfit"`
	GrossMargin   float64          `json:"grossMargin"`
	RevenueByMonth []MonthAmount   `json:"revenueByMonth"`
	CostsByMonth   []MonthAmount   `json:"costsByMonth"`
}

// MonthAmount represents a monthly amount
type MonthAmount struct {
	Month  string  `json:"month"`
	Amount float64 `json:"amount"`
}

// GetRevenueJournal returns monthly double-entry journal entries
func (h *Handler) GetRevenueJournal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	months := httputil.GetQueryParamInt(r, "months", 6)
	if months > 24 {
		months = 24
	}

	rows, err := h.db.Main.Query(ctx, `
		SELECT
			DATE_TRUNC('month', "createdAt") as month,
			COALESCE(SUM("amountPaid"), 0) as total_paid,
			COALESCE(SUM("taxAmount"), 0) as total_tax
		FROM invoices
		WHERE status = 'paid'
		AND "createdAt" >= DATE_TRUNC('month', CURRENT_DATE - ($1 || ' months')::interval)
		GROUP BY DATE_TRUNC('month', "createdAt")
		ORDER BY month DESC
	`, months)
	if err != nil {
		h.logger.Error("failed to get revenue journal", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue journal")
		return
	}
	defer rows.Close()

	var entries []JournalEntryResponse
	for rows.Next() {
		var month time.Time
		var totalPaid, totalTax int64
		if err := rows.Scan(&month, &totalPaid, &totalTax); err != nil {
			continue
		}
		monthStr := month.Format("2006-01")
		revenue := float64(totalPaid) / 100
		tax := float64(totalTax) / 100

		// Revenue recognition entry
		entries = append(entries, JournalEntryResponse{
			Month:         monthStr,
			Description:   "Subscription revenue recognized",
			DebitAccount:  "Accounts Receivable",
			CreditAccount: "Subscription Revenue",
			Amount:        revenue - tax,
		})

		// Tax entry
		if tax > 0 {
			entries = append(entries, JournalEntryResponse{
				Month:         monthStr,
				Description:   "Sales tax collected",
				DebitAccount:  "Accounts Receivable",
				CreditAccount: "Sales Tax Payable",
				Amount:        tax,
			})
		}
	}

	if entries == nil {
		entries = []JournalEntryResponse{}
	}

	httputil.Success(w, map[string]interface{}{
		"entries": entries,
	})
}

// GetDeferredRevenue returns deferred revenue schedule for annual subscriptions
func (h *Handler) GetDeferredRevenue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Main.Query(ctx, `
		SELECT
			s.id, u.email, s.tier,
			s."currentPeriodStart", s."currentPeriodEnd"
		FROM subscriptions s
		JOIN users u ON s."userId" = u.id
		WHERE s.status = 'ACTIVE'
		AND s."currentPeriodStart" IS NOT NULL
		AND s."currentPeriodEnd" IS NOT NULL
		ORDER BY s."currentPeriodEnd" DESC
	`)
	if err != nil {
		h.logger.Error("failed to get deferred revenue", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get deferred revenue")
		return
	}
	defer rows.Close()

	var items []DeferredRevenueItem
	var totalDeferred, totalRecognized float64
	now := time.Now()

	for rows.Next() {
		var item DeferredRevenueItem
		var startDate, endDate time.Time
		if err := rows.Scan(&item.SubscriptionID, &item.Email, &item.Tier, &startDate, &endDate); err != nil {
			continue
		}

		// Calculate total months and recognized months
		totalMonths := int(endDate.Sub(startDate).Hours() / (24 * 30))
		if totalMonths <= 0 {
			totalMonths = 1
		}
		elapsedMonths := int(now.Sub(startDate).Hours() / (24 * 30))
		if elapsedMonths < 0 {
			elapsedMonths = 0
		}
		if elapsedMonths > totalMonths {
			elapsedMonths = totalMonths
		}

		// Calculate amounts
		annualPrice := tierMRR(item.Tier) * 12
		item.TotalAmount = annualPrice
		item.MonthsTotal = totalMonths
		item.MonthsRecognized = elapsedMonths
		item.RecognizedAmount = annualPrice * float64(elapsedMonths) / float64(totalMonths)
		item.DeferredAmount = annualPrice - item.RecognizedAmount
		item.StartDate = startDate.Format("2006-01-02")
		item.EndDate = endDate.Format("2006-01-02")

		totalDeferred += item.DeferredAmount
		totalRecognized += item.RecognizedAmount

		items = append(items, item)
	}

	if items == nil {
		items = []DeferredRevenueItem{}
	}

	httputil.Success(w, map[string]interface{}{
		"schedule":        items,
		"totalDeferred":   totalDeferred,
		"totalRecognized": totalRecognized,
	})
}

// GetVendorExpenses returns monthly vendor cost breakdown
func (h *Handler) GetVendorExpenses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Main.Query(ctx, `
		SELECT id, name, category, COALESCE("monthlyCost", 0) as monthly_cost
		FROM "VendorConfig"
		WHERE enabled = true
		ORDER BY category, name
	`)
	if err != nil {
		h.logger.Error("failed to get vendor expenses", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get vendor expenses")
		return
	}
	defer rows.Close()

	var items []VendorExpenseItem
	var totalMonthly float64
	for rows.Next() {
		var item VendorExpenseItem
		if err := rows.Scan(&item.VendorID, &item.VendorName, &item.Category, &item.MonthlyCost); err != nil {
			continue
		}
		item.AnnualCost = item.MonthlyCost * 12
		totalMonthly += item.MonthlyCost
		items = append(items, item)
	}

	if items == nil {
		items = []VendorExpenseItem{}
	}

	// Get contract costs
	contractRows, err := h.db.Main.Query(ctx, `
		SELECT vc.id, vc.name, vc.category, COALESCE(c.notes, '') as notes
		FROM "VendorConfig" vc
		JOIN vendor_contracts c ON vc.id = c.vendor_id
		WHERE vc.enabled = true AND c.end_date > NOW()
	`)
	var activeContracts int64
	if err == nil {
		defer contractRows.Close()
		for contractRows.Next() {
			var id, name, category, notes string
			_ = contractRows.Scan(&id, &name, &category, &notes)
			activeContracts++
		}
	}

	httputil.Success(w, map[string]interface{}{
		"expenses":        items,
		"totalMonthly":    totalMonthly,
		"totalAnnual":     totalMonthly * 12,
		"activeContracts": activeContracts,
	})
}

// GetProfitability returns revenue minus costs = profit
func (h *Handler) GetProfitability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var resp ProfitabilityResponse

	// Get revenue by month (last 6 months)
	revenueRows, err := h.db.Main.Query(ctx, `
		SELECT
			DATE_TRUNC('month', "createdAt") as month,
			COALESCE(SUM("amountPaid"), 0) as total
		FROM invoices
		WHERE status = 'paid'
		AND "createdAt" >= DATE_TRUNC('month', CURRENT_DATE - INTERVAL '5 months')
		GROUP BY DATE_TRUNC('month', "createdAt")
		ORDER BY month
	`)
	if err != nil {
		h.logger.Error("failed to get profitability", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get profitability")
		return
	}
	defer revenueRows.Close()

	for revenueRows.Next() {
		var month time.Time
		var total int64
		if err := revenueRows.Scan(&month, &total); err != nil {
			continue
		}
		amount := float64(total) / 100
		resp.GrossRevenue += amount
		resp.RevenueByMonth = append(resp.RevenueByMonth, MonthAmount{
			Month:  month.Format("2006-01"),
			Amount: amount,
		})
	}

	// Get vendor costs (use monthly cost * months)
	var monthlyVendorCost float64
	_ = h.db.Main.QueryRow(ctx, `
		SELECT COALESCE(SUM("monthlyCost"), 0)
		FROM "VendorConfig"
		WHERE enabled = true
	`).Scan(&monthlyVendorCost)

	// Create cost entries for each month
	for _, rev := range resp.RevenueByMonth {
		resp.CostsByMonth = append(resp.CostsByMonth, MonthAmount{
			Month:  rev.Month,
			Amount: monthlyVendorCost,
		})
		resp.TotalCosts += monthlyVendorCost
	}

	resp.GrossProfit = resp.GrossRevenue - resp.TotalCosts
	if resp.GrossRevenue > 0 {
		resp.GrossMargin = resp.GrossProfit / resp.GrossRevenue * 100
	}

	if resp.RevenueByMonth == nil {
		resp.RevenueByMonth = []MonthAmount{}
	}
	if resp.CostsByMonth == nil {
		resp.CostsByMonth = []MonthAmount{}
	}

	httputil.Success(w, resp)
}

// ExportAccounting exports accounting data in CSV or JSON format
func (h *Handler) ExportAccounting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Format  string `json:"format" validate:"required,oneof=csv json"`
		Section string `json:"section" validate:"required,oneof=journal deferred expenses profitability"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	ctx := r.Context()

	switch req.Section {
	case "expenses":
		rows, err := h.db.Main.Query(ctx, `
			SELECT name, category, COALESCE("monthlyCost", 0)
			FROM "VendorConfig"
			WHERE enabled = true
			ORDER BY category, name
		`)
		if err != nil {
			httputil.Error(w, http.StatusInternalServerError, "failed to export")
			return
		}
		defer rows.Close()

		if req.Format == "csv" {
			headers := []string{"Vendor", "Category", "Monthly Cost", "Annual Cost"}
			var csvRows [][]string
			for rows.Next() {
				var name, category string
				var monthlyCost float64
				if err := rows.Scan(&name, &category, &monthlyCost); err != nil {
					continue
				}
				csvRows = append(csvRows, []string{
					name, category,
					fmt.Sprintf("%.2f", monthlyCost),
					fmt.Sprintf("%.2f", monthlyCost*12),
				})
			}
			exportCSV(w, "vendor_expenses.csv", headers, csvRows)
		} else {
			type expense struct {
				Name        string  `json:"name"`
				Category    string  `json:"category"`
				MonthlyCost float64 `json:"monthlyCost"`
				AnnualCost  float64 `json:"annualCost"`
			}
			var expenses []expense
			for rows.Next() {
				var e expense
				if err := rows.Scan(&e.Name, &e.Category, &e.MonthlyCost); err != nil {
					continue
				}
				e.AnnualCost = e.MonthlyCost * 12
				expenses = append(expenses, e)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", "attachment; filename=vendor_expenses.json")
			_ = json.NewEncoder(w).Encode(expenses)
		}

	default:
		httputil.BadRequest(w, "export not supported for section: "+req.Section)
	}

	h.logAdminAudit(ctx, r, "ACCOUNTING_EXPORT", "accounting", "", map[string]interface{}{
		"format":  req.Format,
		"section": req.Section,
	})
}
