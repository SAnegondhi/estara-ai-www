package admin

import (
	"fmt"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// Compliance Management
// ===============================

// ConsentResponse represents a user consent record
type ConsentResponse struct {
	ID          string  `json:"id"`
	UserID      string  `json:"userId"`
	Email       *string `json:"email,omitempty"`
	ConsentType string  `json:"consentType"`
	Version     string  `json:"version"`
	GrantedAt   string  `json:"grantedAt"`
	RevokedAt   *string `json:"revokedAt,omitempty"`
	IPAddress   *string `json:"ipAddress,omitempty"`
	CreatedAt   string  `json:"createdAt"`
}

// ConsentSummaryResponse represents consent adoption summary
type ConsentSummaryResponse struct {
	ConsentType  string  `json:"consentType"`
	ActiveCount  int64   `json:"activeCount"`
	RevokedCount int64   `json:"revokedCount"`
	TotalCount   int64   `json:"totalCount"`
	AdoptionRate float64 `json:"adoptionRate"`
}

// TermsLogEntry represents a terms acceptance record
type TermsLogEntry struct {
	ID         string  `json:"id"`
	UserID     string  `json:"userId"`
	Email      *string `json:"email,omitempty"`
	Version    string  `json:"version"`
	AcceptedAt string  `json:"acceptedAt"`
	IPAddress  *string `json:"ipAddress,omitempty"`
}

// TaxSummaryItem represents tax aggregate by state
type TaxSummaryItem struct {
	State        string  `json:"state"`
	TotalTax     float64 `json:"totalTax"`
	InvoiceCount int64   `json:"invoiceCount"`
	TotalRevenue float64 `json:"totalRevenue"`
}

// GetConsents returns user consent records
func (h *Handler) GetConsents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	consentType := r.URL.Query().Get("type")
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	q := queries.New(h.db.Main)

	// Get consent records
	var consentRows []queries.UserConsent
	var err error
	if consentType != "" {
		consentRows, err = q.ListUserConsentsByType(ctx, queries.ListUserConsentsByTypeParams{
			ConsentType: consentType,
			Limit:       int32(pageSize),
			Offset:      int32(offset),
		})
	} else {
		consentRows, err = q.ListUserConsents(ctx, queries.ListUserConsentsParams{
			Limit:  int32(pageSize),
			Offset: int32(offset),
		})
	}
	if err != nil {
		h.logger.Error("failed to list consents", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list consents")
		return
	}

	// Map to response
	var results []ConsentResponse
	for _, c := range consentRows {
		resp := ConsentResponse{
			ID:          c.ID.String(),
			UserID:      c.UserID.String(),
			ConsentType: c.ConsentType,
			Version:     c.Version,
			GrantedAt:   c.GrantedAt.Format(time.RFC3339),
			CreatedAt:   c.CreatedAt.Format(time.RFC3339),
		}
		if c.RevokedAt.Valid {
			ra := c.RevokedAt.Time.Format(time.RFC3339)
			resp.RevokedAt = &ra
		}
		if c.IpAddress.Valid {
			resp.IPAddress = &c.IpAddress.String
		}
		results = append(results, resp)
	}

	if results == nil {
		results = []ConsentResponse{}
	}

	// Get total count
	var total int64
	if consentType != "" {
		total, _ = q.CountUserConsentsByType(ctx, consentType)
	} else {
		total, _ = q.CountUserConsents(ctx)
	}

	// Get consent summary
	summaryRows, _ := q.GetConsentSummary(ctx)
	var summaries []ConsentSummaryResponse
	var totalUsers int64
	_ = h.db.Main.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&totalUsers)

	for _, s := range summaryRows {
		adoptionRate := float64(0)
		if totalUsers > 0 {
			adoptionRate = float64(s.ActiveCount) / float64(totalUsers) * 100
		}
		summaries = append(summaries, ConsentSummaryResponse{
			ConsentType:  s.ConsentType,
			ActiveCount:  s.ActiveCount,
			RevokedCount: s.RevokedCount,
			TotalCount:   s.TotalCount,
			AdoptionRate: adoptionRate,
		})
	}

	if summaries == nil {
		summaries = []ConsentSummaryResponse{}
	}

	httputil.Success(w, map[string]interface{}{
		"consents": results,
		"summary":  summaries,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// GetTermsLog returns terms acceptance records
func (h *Handler) GetTermsLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := httputil.GetQueryParamInt(r, "page", 1)
	pageSize := httputil.GetQueryParamInt(r, "pageSize", 50)
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	rows, err := h.db.Main.Query(ctx, `
		SELECT t.id, t."userId", u.email, t.version, t."acceptedAt", t."ipAddress"
		FROM terms_acceptances t
		LEFT JOIN users u ON t."userId" = u.id
		ORDER BY t."acceptedAt" DESC
		LIMIT $1 OFFSET $2
	`, pageSize, offset)
	if err != nil {
		h.logger.Warn("failed to get terms log", "error", err)
		httputil.Success(w, map[string]interface{}{
			"entries": []TermsLogEntry{},
			"total":   0,
		})
		return
	}
	defer rows.Close()

	var entries []TermsLogEntry
	for rows.Next() {
		var e TermsLogEntry
		var acceptedAt time.Time
		if err := rows.Scan(&e.ID, &e.UserID, &e.Email, &e.Version, &acceptedAt, &e.IPAddress); err != nil {
			continue
		}
		e.AcceptedAt = acceptedAt.Format(time.RFC3339)
		entries = append(entries, e)
	}

	if entries == nil {
		entries = []TermsLogEntry{}
	}

	// Get total count
	var total int64
	_ = h.db.Main.QueryRow(ctx, "SELECT COUNT(*) FROM terms_acceptances").Scan(&total)

	// Get version adoption rates
	versionRows, err := h.db.Main.Query(ctx, `
		SELECT version, COUNT(*) as cnt
		FROM terms_acceptances
		GROUP BY version
		ORDER BY version DESC
	`)
	var versionAdoption []map[string]interface{}
	if err == nil {
		defer versionRows.Close()
		for versionRows.Next() {
			var version string
			var count int64
			if err := versionRows.Scan(&version, &count); err != nil {
				continue
			}
			versionAdoption = append(versionAdoption, map[string]interface{}{
				"version": version,
				"count":   count,
			})
		}
	}
	if versionAdoption == nil {
		versionAdoption = []map[string]interface{}{}
	}

	httputil.Success(w, map[string]interface{}{
		"entries":         entries,
		"versionAdoption": versionAdoption,
		"total":           total,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

// GetTaxSummary returns tax collected by state
func (h *Handler) GetTaxSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Aggregate tax from invoices joined with user state data
	rows, err := h.db.Main.Query(ctx, `
		SELECT
			COALESCE(u."state", 'Unknown') as state,
			COALESCE(SUM(i."taxAmount"), 0) as total_tax,
			COUNT(*) as invoice_count,
			COALESCE(SUM(i.total), 0) as total_revenue
		FROM invoices i
		LEFT JOIN users u ON i."userId" = u.id
		WHERE i.status = 'paid'
		GROUP BY u."state"
		ORDER BY total_tax DESC
	`)
	if err != nil {
		h.logger.Warn("failed to get tax summary", "error", err)
		httputil.Success(w, map[string]interface{}{
			"summary":      []TaxSummaryItem{},
			"totalTax":     0,
			"totalRevenue": 0,
		})
		return
	}
	defer rows.Close()

	var items []TaxSummaryItem
	var totalTax, totalRevenue float64
	for rows.Next() {
		var item TaxSummaryItem
		var tax, revenue int64
		if err := rows.Scan(&item.State, &tax, &item.InvoiceCount, &revenue); err != nil {
			continue
		}
		item.TotalTax = float64(tax) / 100
		item.TotalRevenue = float64(revenue) / 100
		totalTax += item.TotalTax
		totalRevenue += item.TotalRevenue
		items = append(items, item)
	}

	if items == nil {
		items = []TaxSummaryItem{}
	}

	httputil.Success(w, map[string]interface{}{
		"summary":      items,
		"totalTax":     fmt.Sprintf("%.2f", totalTax),
		"totalRevenue": fmt.Sprintf("%.2f", totalRevenue),
	})
}
