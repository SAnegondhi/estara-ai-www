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
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	ConsentType string `json:"consentType"`
	Version     string `json:"version"`
	Granted     bool   `json:"granted"`
	IPAddress   string `json:"ipAddress"`
	UserAgent   string `json:"userAgent"`
	Timestamp   string `json:"timestamp"`
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

	q := h.store.Q()

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
			ID:        c.ID,
			UserID:    c.UserId,
			Version:   c.Version,
			Granted:   c.Granted,
			IPAddress: c.IpAddress,
			UserAgent: c.UserAgent,
		}
		if c.ConsentType != nil {
			resp.ConsentType = fmt.Sprintf("%v", c.ConsentType)
		}
		if c.Timestamp.Valid {
			resp.Timestamp = c.Timestamp.Time.Format(time.RFC3339)
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
	totalUsers, _ := q.CountUsers(ctx)

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

	q := h.store.Q()

	termsRows, err := q.GetTermsLogWithUsers(ctx, queries.GetTermsLogWithUsersParams{
		Limit:  int32(pageSize),
		Offset: int32(offset),
	})
	if err != nil {
		h.logger.Warn("failed to get terms log", "error", err)
		httputil.Success(w, map[string]interface{}{
			"entries": []TermsLogEntry{},
			"total":   0,
		})
		return
	}

	entries := make([]TermsLogEntry, 0, len(termsRows))
	for _, r := range termsRows {
		e := TermsLogEntry{
			ID:     r.ID,
			UserID: r.UserId,
		}
		if r.Email.Valid {
			e.Email = &r.Email.String
		}
		e.Version = r.Version
		if r.AcceptedAt.Valid {
			e.AcceptedAt = r.AcceptedAt.Time.Format(time.RFC3339)
		}
		if r.IpAddress.Valid {
			e.IPAddress = &r.IpAddress.String
		}
		entries = append(entries, e)
	}

	// Get total count
	total, _ := q.CountTermsAcceptances(ctx)

	// Get version adoption rates
	versionData, _ := q.GetTermsAcceptancesByVersion(ctx)
	versionAdoption := make([]map[string]interface{}, 0, len(versionData))
	for _, v := range versionData {
		versionAdoption = append(versionAdoption, map[string]interface{}{
			"version": v.Version,
			"count":   v.Cnt,
		})
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
	taxRows, err := h.store.Q().GetTaxSummary(ctx)
	if err != nil {
		h.logger.Warn("failed to get tax summary", "error", err)
		httputil.Success(w, map[string]interface{}{
			"summary":      []TaxSummaryItem{},
			"totalTax":     0,
			"totalRevenue": 0,
		})
		return
	}

	items := make([]TaxSummaryItem, 0, len(taxRows))
	var totalTax, totalRevenue float64
	for _, r := range taxRows {
		item := TaxSummaryItem{
			State:        r.State,
			TotalTax:     float64(r.TotalTax) / 100,
			InvoiceCount: r.InvoiceCount,
			TotalRevenue: float64(r.TotalRevenue) / 100,
		}
		totalTax += item.TotalTax
		totalRevenue += item.TotalRevenue
		items = append(items, item)
	}

	httputil.Success(w, map[string]interface{}{
		"summary":      items,
		"totalTax":     fmt.Sprintf("%.2f", totalTax),
		"totalRevenue": fmt.Sprintf("%.2f", totalRevenue),
	})
}
