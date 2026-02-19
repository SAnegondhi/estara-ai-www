package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

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
	GrossRevenue   float64       `json:"grossRevenue"`
	TotalCosts     float64       `json:"totalCosts"`
	GrossProfit    float64       `json:"grossProfit"`
	GrossMargin    float64       `json:"grossMargin"`
	RevenueByMonth []MonthAmount `json:"revenueByMonth"`
	CostsByMonth   []MonthAmount `json:"costsByMonth"`
}

// MonthAmount represents a monthly amount
type MonthAmount struct {
	Month  string  `json:"month"`
	Amount float64 `json:"amount"`
}

// numericToFloat64 converts a pgtype.Numeric to float64
func numericToFloat64(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, _ := n.Float64Value()
	return f.Float64
}

// GetRevenueJournal returns monthly double-entry journal entries
func (h *Handler) GetRevenueJournal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	months := httputil.GetQueryParamInt(r, "months", 6)
	if months > 24 {
		months = 24
	}

	rows, err := h.store.Q().GetMonthlyRevenue(ctx, pgtype.Text{
		String: fmt.Sprintf("%d", months),
		Valid:  true,
	})
	if err != nil {
		h.logger.Error("failed to get revenue journal", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get revenue journal")
		return
	}

	var entries []JournalEntryResponse
	for _, row := range rows {
		// Month is pgtype.Interval from DATE_TRUNC; extract microseconds as timestamp
		monthStr := "unknown"
		if row.Month.Valid {
			// pgtype.Interval stores Microseconds - interpret as Unix microseconds for timestamp
			month := time.UnixMicro(row.Month.Microseconds)
			monthStr = month.Format("2006-01")
		}
		revenue := float64(row.TotalPaid) / 100
		tax := float64(row.TotalTax) / 100

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

	rows, err := h.store.Q().ListActiveSubscriptionsWithUsers(ctx)
	if err != nil {
		h.logger.Error("failed to get deferred revenue", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get deferred revenue")
		return
	}

	var items []DeferredRevenueItem
	var totalDeferred, totalRecognized float64
	now := time.Now()

	for _, row := range rows {
		if !row.CurrentPeriodStart.Valid || !row.CurrentPeriodEnd.Valid {
			continue
		}

		startDate := row.CurrentPeriodStart.Time
		endDate := row.CurrentPeriodEnd.Time

		var item DeferredRevenueItem
		item.SubscriptionID = row.ID
		item.Email = row.Email
		item.Tier = row.STier

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

	rows, err := h.store.Q().ListActiveVendorConfigs(ctx)
	if err != nil {
		h.logger.Error("failed to get vendor expenses", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get vendor expenses")
		return
	}

	var items []VendorExpenseItem
	var totalMonthly float64
	for _, row := range rows {
		monthlyCost := numericToFloat64(row.MonthlyCost)
		item := VendorExpenseItem{
			VendorID:    row.ID,
			VendorName:  row.Name,
			Category:    row.Category,
			MonthlyCost: monthlyCost,
			AnnualCost:  monthlyCost * 12,
		}
		totalMonthly += monthlyCost
		items = append(items, item)
	}

	if items == nil {
		items = []VendorExpenseItem{}
	}

	// Get active contract count
	activeContracts, err := h.store.Q().GetActiveVendorContractCount(ctx)
	if err != nil {
		activeContracts = 0
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
	revenueRows, err := h.store.Q().GetQuarterlyExpenses(ctx)
	if err != nil {
		h.logger.Error("failed to get profitability", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get profitability")
		return
	}

	for _, row := range revenueRows {
		monthStr := "unknown"
		if row.Month.Valid {
			month := time.UnixMicro(row.Month.Microseconds)
			monthStr = month.Format("2006-01")
		}
		amount := float64(row.Total) / 100
		resp.GrossRevenue += amount
		resp.RevenueByMonth = append(resp.RevenueByMonth, MonthAmount{
			Month:  monthStr,
			Amount: amount,
		})
	}

	// Get vendor costs (use monthly cost * months)
	totalCostResult, err := h.store.Q().GetTotalActiveVendorCost(ctx)
	var monthlyVendorCost float64
	if err == nil && totalCostResult != nil {
		// GetTotalActiveVendorCost returns interface{} — try numeric string conversion
		switch v := totalCostResult.(type) {
		case float64:
			monthlyVendorCost = v
		case int64:
			monthlyVendorCost = float64(v)
		case string:
			fmt.Sscanf(v, "%f", &monthlyVendorCost)
		case pgtype.Numeric:
			monthlyVendorCost = numericToFloat64(v)
		}
	}

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
		rows, err := h.store.Q().ListActiveVendorConfigs(ctx)
		if err != nil {
			httputil.Error(w, http.StatusInternalServerError, "failed to export")
			return
		}

		if req.Format == "csv" {
			headers := []string{"Vendor", "Category", "Monthly Cost", "Annual Cost"}
			var csvRows [][]string
			for _, row := range rows {
				monthlyCost := numericToFloat64(row.MonthlyCost)
				csvRows = append(csvRows, []string{
					row.Name, row.Category,
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
			for _, row := range rows {
				monthlyCost := numericToFloat64(row.MonthlyCost)
				expenses = append(expenses, expense{
					Name:        row.Name,
					Category:    row.Category,
					MonthlyCost: monthlyCost,
					AnnualCost:  monthlyCost * 12,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", "attachment; filename=vendor_expenses.json")
			_ = json.NewEncoder(w).Encode(expenses)
		}

	default:
		httputil.BadRequest(w, "export not supported for section: "+req.Section)
	}

	h.logAdminAudit(ctx, r, "ADMIN_USER", "ACCOUNTING_EXPORT", "accounting", "", map[string]interface{}{
		"format":  req.Format,
		"section": req.Section,
	})
}
