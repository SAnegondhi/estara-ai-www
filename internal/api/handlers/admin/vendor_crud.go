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

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// ===============================
// Vendor CRUD Types
// ===============================

// CreateVendorRequest represents the request to create a vendor.
type CreateVendorRequest struct {
	Name         string  `json:"name" validate:"required"`
	DisplayName  string  `json:"displayName" validate:"required"`
	Category     string  `json:"category" validate:"required,oneof=AI_PROVIDER DATA_PROVIDER INFRASTRUCTURE PAYMENT EMAIL MONITORING"`
	BillingModel string  `json:"billingModel" validate:"required,oneof=PAYGO MONTHLY ANNUAL FREE_TIER USAGE_BASED"`
	MonthlyCost  float64 `json:"monthlyCost"`
	ApiKeyEnvVar string  `json:"apiKeyEnvVar"`
	HealthUrl    string  `json:"healthUrl"`
	IsPrimary    bool    `json:"isPrimary"`
	Notes        string  `json:"notes"`
}

// UpdateVendorRequest represents the request to update a vendor.
type UpdateVendorRequest struct {
	DisplayName  *string  `json:"displayName,omitempty"`
	Category     *string  `json:"category,omitempty" validate:"omitempty,oneof=AI_PROVIDER DATA_PROVIDER INFRASTRUCTURE PAYMENT EMAIL MONITORING"`
	BillingModel *string  `json:"billingModel,omitempty" validate:"omitempty,oneof=PAYGO MONTHLY ANNUAL FREE_TIER USAGE_BASED"`
	MonthlyCost  *float64 `json:"monthlyCost,omitempty"`
	ApiKeyEnvVar *string  `json:"apiKeyEnvVar,omitempty"`
	HealthUrl    *string  `json:"healthUrl,omitempty"`
	IsPrimary    *bool    `json:"isPrimary,omitempty"`
	Notes        *string  `json:"notes,omitempty"`
}

// VendorConfigResponse is a JSON-safe vendor config for admin views.
type VendorConfigResponse struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	DisplayName  string     `json:"displayName"`
	Category     string     `json:"category"`
	BillingModel string     `json:"billingModel"`
	MonthlyCost  *float64   `json:"monthlyCost"`
	ApiKeyEnvVar *string    `json:"apiKeyEnvVar"`
	HealthUrl    *string    `json:"healthUrl"`
	IsPrimary    bool       `json:"isPrimary"`
	IsActive     bool       `json:"isActive"`
	Enabled      bool       `json:"enabled"`
	Notes        *string    `json:"notes"`
	Status       *string    `json:"status"`
	LastChecked  *time.Time `json:"lastChecked"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// VendorContractResponse is a JSON-safe vendor contract for admin views.
type VendorContractResponse struct {
	ID                    string  `json:"id"`
	VendorID              string  `json:"vendorId"`
	StartDate             *string `json:"startDate"`
	EndDate               *string `json:"endDate"`
	AutoRenew             bool    `json:"autoRenew"`
	RenewalTermMonths     *int32  `json:"renewalTermMonths"`
	TerminationNoticeDays *int32  `json:"terminationNoticeDays"`
	PricingTiers          any     `json:"pricingTiers"`
	UsageLimits           any     `json:"usageLimits"`
	SlaTerms              any     `json:"slaTerms"`
	LegalRestrictions     *string `json:"legalRestrictions"`
	ContactName           *string `json:"contactName"`
	ContactEmail          *string `json:"contactEmail"`
	Notes                 *string `json:"notes"`
	CreatedAt             *string `json:"createdAt"`
	UpdatedAt             *string `json:"updatedAt"`
}

// CreateContractRequest represents the request to create or update a vendor contract.
type CreateContractRequest struct {
	StartDate             string `json:"startDate" validate:"required"`
	EndDate               string `json:"endDate,omitempty"`
	AutoRenew             bool   `json:"autoRenew"`
	RenewalTermMonths     *int32 `json:"renewalTermMonths,omitempty"`
	TerminationNoticeDays *int32 `json:"terminationNoticeDays,omitempty"`
	PricingTiers          any    `json:"pricingTiers,omitempty"`
	UsageLimits           any    `json:"usageLimits,omitempty"`
	SlaTerms              any    `json:"slaTerms,omitempty"`
	LegalRestrictions     string `json:"legalRestrictions,omitempty"`
	ContactName           string `json:"contactName,omitempty"`
	ContactEmail          string `json:"contactEmail,omitempty"`
	Notes                 string `json:"notes,omitempty"`
}

// ===============================
// Vendor CRUD Handlers
// ===============================

// CreateVendor creates a new vendor config entry.
func (h *Handler) CreateVendor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req CreateVendorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	_, err := h.db.Main.Exec(ctx, `
		INSERT INTO "VendorConfig" (
			id, name, "displayName", category, "billingModel",
			"monthlyCost", "apiKeyEnvVar", "healthUrl", "isPrimary", notes,
			enabled, "isActive", "createdAt", "updatedAt"
		) VALUES ($1, $2, $3, $4::text::"VendorCategory", $5::text::"VendorBillingModel",
			$6, $7, $8, $9, $10,
			true, true, NOW(), NOW())
	`, id, req.Name, req.DisplayName, req.Category, req.BillingModel,
		req.MonthlyCost, nilIfEmpty(req.ApiKeyEnvVar), nilIfEmpty(req.HealthUrl), req.IsPrimary, nilIfEmpty(req.Notes))
	if err != nil {
		h.logger.Error("failed to create vendor", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to create vendor")
		return
	}

	h.logAdminAudit(ctx, r, "VENDOR_CREATE", "vendor", id, map[string]any{
		"name":     req.Name,
		"category": req.Category,
	})
	h.logger.Info("vendor created", "vendor_id", id, "name", req.Name)
	httputil.Success(w, map[string]any{
		"id":      id,
		"name":    req.Name,
		"created": true,
	})
}

// UpdateVendor updates an existing vendor config entry.
func (h *Handler) UpdateVendor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vendorID := chi.URLParam(r, "id")
	if vendorID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req UpdateVendorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	// Build dynamic update
	setClauses := []string{`"updatedAt" = NOW()`}
	args := []any{vendorID}
	argIdx := 2

	if req.DisplayName != nil {
		setClauses = append(setClauses, fmt.Sprintf(`"displayName" = $%d`, argIdx))
		args = append(args, *req.DisplayName)
		argIdx++
	}
	if req.Category != nil {
		setClauses = append(setClauses, fmt.Sprintf(`category = $%d::text::"VendorCategory"`, argIdx))
		args = append(args, *req.Category)
		argIdx++
	}
	if req.BillingModel != nil {
		setClauses = append(setClauses, fmt.Sprintf(`"billingModel" = $%d::text::"VendorBillingModel"`, argIdx))
		args = append(args, *req.BillingModel)
		argIdx++
	}
	if req.MonthlyCost != nil {
		setClauses = append(setClauses, fmt.Sprintf(`"monthlyCost" = $%d`, argIdx))
		args = append(args, *req.MonthlyCost)
		argIdx++
	}
	if req.ApiKeyEnvVar != nil {
		setClauses = append(setClauses, fmt.Sprintf(`"apiKeyEnvVar" = $%d`, argIdx))
		args = append(args, *req.ApiKeyEnvVar)
		argIdx++
	}
	if req.HealthUrl != nil {
		setClauses = append(setClauses, fmt.Sprintf(`"healthUrl" = $%d`, argIdx))
		args = append(args, *req.HealthUrl)
		argIdx++
	}
	if req.IsPrimary != nil {
		setClauses = append(setClauses, fmt.Sprintf(`"isPrimary" = $%d`, argIdx))
		args = append(args, *req.IsPrimary)
		argIdx++
	}
	if req.Notes != nil {
		setClauses = append(setClauses, fmt.Sprintf(`notes = $%d`, argIdx))
		args = append(args, *req.Notes)
		argIdx++
	}

	query := fmt.Sprintf(`UPDATE "VendorConfig" SET %s WHERE id = $1`, joinStrings(setClauses, ", "))
	result, err := h.db.Main.Exec(ctx, query, args...)
	if err != nil {
		h.logger.Error("failed to update vendor", "error", err, "vendor_id", vendorID)
		httputil.Error(w, http.StatusInternalServerError, "failed to update vendor")
		return
	}
	if result.RowsAffected() == 0 {
		httputil.Error(w, http.StatusNotFound, "vendor not found")
		return
	}

	h.logAdminAudit(ctx, r, "VENDOR_UPDATE", "vendor", vendorID, map[string]any{
		"changes": req,
	})
	h.logger.Info("vendor updated", "vendor_id", vendorID)
	httputil.Success(w, map[string]any{
		"vendorId": vendorID,
		"updated":  true,
	})
}

// DeleteVendor soft-deletes a vendor (sets enabled=false, isActive=false).
func (h *Handler) DeleteVendor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vendorID := chi.URLParam(r, "id")
	if vendorID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	result, err := h.db.Main.Exec(ctx, `
		UPDATE "VendorConfig"
		SET enabled = false, "isActive" = false, "updatedAt" = NOW()
		WHERE id = $1
	`, vendorID)
	if err != nil {
		h.logger.Error("failed to delete vendor", "error", err, "vendor_id", vendorID)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete vendor")
		return
	}
	if result.RowsAffected() == 0 {
		httputil.Error(w, http.StatusNotFound, "vendor not found")
		return
	}

	h.logAdminAudit(ctx, r, "VENDOR_DELETE", "vendor", vendorID, nil)
	h.logger.Info("vendor soft-deleted", "vendor_id", vendorID)
	httputil.Success(w, map[string]any{
		"vendorId": vendorID,
		"deleted":  true,
	})
}

// GetVendorContract returns the contract for a vendor.
func (h *Handler) GetVendorContract(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vendorID := chi.URLParam(r, "id")
	if vendorID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	q := queries.New(h.db.Main)
	contract, err := q.GetVendorContract(ctx, vendorID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httputil.Success(w, map[string]any{"contract": nil})
			return
		}
		h.logger.Error("failed to get vendor contract", "error", err, "vendor_id", vendorID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get vendor contract")
		return
	}

	httputil.Success(w, map[string]any{
		"contract": contractToResponse(contract),
	})
}

// CreateOrUpdateContract creates or updates a vendor contract.
func (h *Handler) CreateOrUpdateContract(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vendorID := chi.URLParam(r, "id")
	if vendorID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	var req CreateContractRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	startDate, err := time.Parse(time.RFC3339, req.StartDate)
	if err != nil {
		httputil.BadRequest(w, "invalid startDate format, use RFC3339")
		return
	}

	var endDate pgtype.Timestamp
	if req.EndDate != "" {
		t, err := time.Parse(time.RFC3339, req.EndDate)
		if err != nil {
			httputil.BadRequest(w, "invalid endDate format, use RFC3339")
			return
		}
		endDate = pgtype.Timestamp{Time: t, Valid: true}
	}

	var pricingTiers, usageLimits, slaTerms []byte
	if req.PricingTiers != nil {
		pricingTiers, _ = json.Marshal(req.PricingTiers)
	}
	if req.UsageLimits != nil {
		usageLimits, _ = json.Marshal(req.UsageLimits)
	}
	if req.SlaTerms != nil {
		slaTerms, _ = json.Marshal(req.SlaTerms)
	}

	q := queries.New(h.db.Main)

	// Check if contract exists
	existing, existErr := q.GetVendorContract(ctx, vendorID)
	if existErr != nil && existErr != pgx.ErrNoRows {
		h.logger.Error("failed to check existing contract", "error", existErr)
		httputil.Error(w, http.StatusInternalServerError, "failed to check existing contract")
		return
	}

	var contract queries.VendorContract
	if existErr == pgx.ErrNoRows {
		// Create new contract
		idBytes := make([]byte, 12)
		_, _ = rand.Read(idBytes)
		contractID := hex.EncodeToString(idBytes)

		contract, err = q.CreateVendorContract(ctx, queries.CreateVendorContractParams{
			ID:                    contractID,
			VendorId:              vendorID,
			StartDate:             pgtype.Timestamp{Time: startDate, Valid: true},
			EndDate:               endDate,
			AutoRenew:             req.AutoRenew,
			RenewalTermMonths:     pgtypeInt4(req.RenewalTermMonths),
			TerminationNoticeDays: pgtypeInt4(req.TerminationNoticeDays),
			PricingTiers:          pricingTiers,
			UsageLimits:           usageLimits,
			SlaTerms:              slaTerms,
			LegalRestrictions:     pgtypeText(req.LegalRestrictions),
			ContactName:           pgtypeText(req.ContactName),
			ContactEmail:          pgtypeText(req.ContactEmail),
			Notes:                 pgtypeText(req.Notes),
		})
	} else {
		// Update existing contract
		contract, err = q.UpdateVendorContract(ctx, queries.UpdateVendorContractParams{
			ID:                    existing.ID,
			StartDate:             pgtype.Timestamp{Time: startDate, Valid: true},
			EndDate:               endDate,
			AutoRenew:             req.AutoRenew,
			RenewalTermMonths:     pgtypeInt4(req.RenewalTermMonths),
			TerminationNoticeDays: pgtypeInt4(req.TerminationNoticeDays),
			PricingTiers:          pricingTiers,
			UsageLimits:           usageLimits,
			SlaTerms:              slaTerms,
			LegalRestrictions:     pgtypeText(req.LegalRestrictions),
			ContactName:           pgtypeText(req.ContactName),
			ContactEmail:          pgtypeText(req.ContactEmail),
			Notes:                 pgtypeText(req.Notes),
		})
	}
	if err != nil {
		h.logger.Error("failed to save vendor contract", "error", err, "vendor_id", vendorID)
		httputil.Error(w, http.StatusInternalServerError, "failed to save vendor contract")
		return
	}

	h.logAdminAudit(ctx, r, "VENDOR_CONTRACT_UPDATE", "vendor_contract", contract.ID, map[string]any{
		"vendorId": vendorID,
	})
	httputil.Success(w, map[string]any{
		"contract": contractToResponse(contract),
	})
}

// GetVendorAlerts returns system alerts of type vendor_*.
func (h *Handler) GetVendorAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Main.Query(ctx, `
		SELECT id, type, severity, title, description, "alertKey", metadata,
			"firstSeen", "lastSeen", "occurrenceCount", dismissed, "actionRequired",
			"expiresAt", "createdAt"
		FROM system_alerts
		WHERE type LIKE 'vendor_%' AND dismissed = false
		ORDER BY "createdAt" DESC
		LIMIT 50
	`)
	if err != nil {
		h.logger.Warn("failed to get vendor alerts", "error", err)
		httputil.Success(w, map[string]any{"alerts": []SystemAlert{}})
		return
	}
	defer rows.Close()

	var alerts []SystemAlert
	for rows.Next() {
		var a SystemAlert
		if err := rows.Scan(&a.ID, &a.Type, &a.Severity, &a.Title, &a.Description,
			&a.AlertKey, &a.Metadata, &a.FirstSeen, &a.LastSeen,
			&a.OccurrenceCount, &a.Dismissed, &a.ActionRequired,
			&a.ExpiresAt, &a.CreatedAt); err != nil {
			h.logger.Warn("failed to scan vendor alert", "error", err)
			continue
		}
		alerts = append(alerts, a)
	}
	if alerts == nil {
		alerts = []SystemAlert{}
	}

	httputil.Success(w, map[string]any{
		"alerts": alerts,
		"total":  len(alerts),
	})
}

// ===============================
// Vendor CRUD Helpers
// ===============================

func contractToResponse(c queries.VendorContract) VendorContractResponse {
	resp := VendorContractResponse{
		ID:        c.ID,
		VendorID:  c.VendorId,
		AutoRenew: c.AutoRenew,
	}
	if c.StartDate.Valid {
		s := c.StartDate.Time.Format(time.RFC3339)
		resp.StartDate = &s
	}
	if c.EndDate.Valid {
		s := c.EndDate.Time.Format(time.RFC3339)
		resp.EndDate = &s
	}
	if c.RenewalTermMonths.Valid {
		resp.RenewalTermMonths = &c.RenewalTermMonths.Int32
	}
	if c.TerminationNoticeDays.Valid {
		resp.TerminationNoticeDays = &c.TerminationNoticeDays.Int32
	}
	if len(c.PricingTiers) > 0 {
		var v any
		_ = json.Unmarshal(c.PricingTiers, &v)
		resp.PricingTiers = v
	}
	if len(c.UsageLimits) > 0 {
		var v any
		_ = json.Unmarshal(c.UsageLimits, &v)
		resp.UsageLimits = v
	}
	if len(c.SlaTerms) > 0 {
		var v any
		_ = json.Unmarshal(c.SlaTerms, &v)
		resp.SlaTerms = v
	}
	if c.LegalRestrictions.Valid {
		resp.LegalRestrictions = &c.LegalRestrictions.String
	}
	if c.ContactName.Valid {
		resp.ContactName = &c.ContactName.String
	}
	if c.ContactEmail.Valid {
		resp.ContactEmail = &c.ContactEmail.String
	}
	if c.Notes.Valid {
		resp.Notes = &c.Notes.String
	}
	if c.CreatedAt.Valid {
		s := c.CreatedAt.Time.Format(time.RFC3339)
		resp.CreatedAt = &s
	}
	if c.UpdatedAt.Valid {
		s := c.UpdatedAt.Time.Format(time.RFC3339)
		resp.UpdatedAt = &s
	}
	return resp
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func pgtypeText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func pgtypeInt4(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
