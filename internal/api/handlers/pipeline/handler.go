// Package pipeline provides handlers for the Investment Pipeline feature (ADR-101).
package pipeline

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/unified"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles pipeline-related HTTP requests.
type Handler struct {
	store         *dbstore.Store
	cfg           *config.Config
	aggregator    *aggregator.Aggregator
	marketService *unified.Service
	logger        *slog.Logger
}

// NewHandler creates a new pipeline handler.
func NewHandler(store *dbstore.Store, cfg *config.Config) *Handler {
	return &Handler{
		store:  store,
		cfg:    cfg,
		logger: slog.Default().With("component", "pipeline_handler"),
	}
}

// SetAggregator injects the market aggregator for rent estimate lookups.
func (h *Handler) SetAggregator(a *aggregator.Aggregator) {
	h.aggregator = a
}

// SetMarketService injects the unified market data service for memo generation.
func (h *Handler) SetMarketService(ms *unified.Service) {
	h.marketService = ms
}

// ---------------------------------------------------------------------------
// Request / Response types
// ---------------------------------------------------------------------------

type createDealRequest struct {
	Name              string `json:"name"`
	Source            string `json:"source"`
	Notes             string `json:"notes"`
	PortfolioExcluded bool   `json:"portfolioExcluded"`
}

type updateDealRequest struct {
	Name              *string `json:"name"`
	Source            *string `json:"source"`
	Status            *string `json:"status"`
	Notes             *string `json:"notes"`
	PortfolioExcluded *bool   `json:"portfolioExcluded"`
	ClosedOutcome     *string `json:"closedOutcome"` // ADR-104: acquired | rejected | other
	InputComplete     *bool   `json:"inputComplete"` // ADR-107: deal ready for analysis
}

type createPropertyRequest struct {
	Address             string          `json:"address"`
	City                string          `json:"city"`
	State               string          `json:"state"`
	Zip                 string          `json:"zip"`
	PropertyType        string          `json:"propertyType"`
	Beds                *float64        `json:"beds"`
	Baths               *float64        `json:"baths"`
	Sqft                *int32          `json:"sqft"`
	YearBuilt           *int32          `json:"yearBuilt"`
	Units               *int32          `json:"units"`
	AskingPrice         *float64        `json:"askingPrice"`
	TargetPrice         *float64        `json:"targetPrice"`
	DownPaymentPct      *float64        `json:"downPaymentPct"`
	FinancingType       string          `json:"financingType"`
	InterestRate        *float64        `json:"interestRate"`
	BrokerRent          *float64        `json:"brokerRent"`
	SystemRent          *float64        `json:"systemRent"`
	CurrentOccupancy    *float64        `json:"currentOccupancy"`
	ExpenseOverrides    json.RawMessage `json:"expenseOverrides"`
	UnitMix             json.RawMessage `json:"unitMix"`
	LotSqft             *int32          `json:"lotSqft"`
	BuildingCount       *int32          `json:"buildingCount"`
	Notes               string          `json:"notes"`
	SourceType          string          `json:"sourceType"`
	// ADR-107 Phase 2: commercial property fields
	LeaseType           string          `json:"leaseType"`
	TenantCount         *int32          `json:"tenantCount"`
	AnchorTenant        string          `json:"anchorTenant"`
	WeightedAvgLeaseYrs *float64        `json:"weightedAvgLeaseYrs"`
	CommercialSqft      *int32          `json:"commercialSqft"`
	CommercialMix       json.RawMessage `json:"commercialMix"`
}

type updatePropertyRequest struct {
	Address             *string          `json:"address"`
	City                *string          `json:"city"`
	State               *string          `json:"state"`
	Zip                 *string          `json:"zip"`
	PropertyType        *string          `json:"propertyType"`
	Beds                *float64         `json:"beds"`
	Baths               *float64         `json:"baths"`
	Sqft                *int32           `json:"sqft"`
	YearBuilt           *int32           `json:"yearBuilt"`
	Units               *int32           `json:"units"`
	AskingPrice         *float64         `json:"askingPrice"`
	TargetPrice         *float64         `json:"targetPrice"`
	DownPaymentPct      *float64         `json:"downPaymentPct"`
	FinancingType       *string          `json:"financingType"`
	InterestRate        *float64         `json:"interestRate"`
	BrokerRent          *float64         `json:"brokerRent"`
	SystemRent          *float64         `json:"systemRent"`
	CurrentOccupancy    *float64         `json:"currentOccupancy"`
	ExpenseOverrides    *json.RawMessage `json:"expenseOverrides"`
	UnitMix             *json.RawMessage `json:"unitMix"`
	LotSqft             *int32           `json:"lotSqft"`
	BuildingCount       *int32           `json:"buildingCount"`
	Notes               *string          `json:"notes"`
	// ADR-107 Phase 2: commercial property fields
	LeaseType           *string          `json:"leaseType"`
	TenantCount         *int32           `json:"tenantCount"`
	AnchorTenant        *string          `json:"anchorTenant"`
	WeightedAvgLeaseYrs *float64         `json:"weightedAvgLeaseYrs"`
	CommercialSqft      *int32           `json:"commercialSqft"`
	CommercialMix       *json.RawMessage `json:"commercialMix"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func textVal(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}

func numericFromFloat(f *float64) pgtype.Numeric {
	if f == nil {
		return pgtype.Numeric{Valid: false}
	}
	s := strconv.FormatFloat(*f, 'f', -1, 64)
	var n pgtype.Numeric
	_ = n.Scan(s)
	return n
}

func int4FromPtr(i *int32) pgtype.Int4 {
	if i == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: *i, Valid: true}
}

func textFromPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{Valid: false}
	}
	return textVal(*s)
}

func numericFromFloatPtr(f *float64) pgtype.Numeric {
	return numericFromFloat(f)
}

// int4FromFloat64Ptr converts *float64 → pgtype.Int4 (truncates decimal).
func int4FromFloat64Ptr(f *float64) pgtype.Int4 {
	if f == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: int32(*f), Valid: true}
}

// omCoalesceFloat returns the first non-nil *float64, or nil if both are nil.
func omCoalesceFloat(a, b *float64) *float64 {
	if a != nil {
		return a
	}
	return b
}

// int4FromGoIntPtr converts *int → pgtype.Int4.
func int4FromGoIntPtr(i *int) pgtype.Int4 {
	if i == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: int32(*i), Valid: true}
}

// ---------------------------------------------------------------------------
// Response mappers — camelCase JSON for TypeScript client.
// sqlc generates snake_case json tags; these structs provide camelCase.
// []byte JSONB fields are wrapped in json.RawMessage (not base64).
// ---------------------------------------------------------------------------

// numericToFloat converts pgtype.Numeric to *float64.
func numericToFloat(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	b, err := n.MarshalJSON()
	if err != nil || string(b) == "null" {
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return nil
	}
	return &f
}

// int4ToPtr converts pgtype.Int4 to *int32.
func int4ToPtr(n pgtype.Int4) *int32 {
	if !n.Valid {
		return nil
	}
	return &n.Int32
}

// textToPtr converts pgtype.Text to *string.
func textToPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := t.String
	return &s
}

// rawJSONOrNil returns nil for empty/null JSONB, else json.RawMessage.
func rawJSONOrNil(b []byte) json.RawMessage {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return json.RawMessage(b)
}

// pipelineDealResponse is the camelCase JSON response for deal endpoints.
type pipelineDealResponse struct {
	ID                string     `json:"id"`
	UserID            string     `json:"userId"`
	Name              string     `json:"name"`
	Source            string     `json:"source"`
	Status            string     `json:"status"`
	Notes             *string    `json:"notes"`
	PropertyCount     int32      `json:"propertyCount"`
	MemoCount         int32      `json:"memoCount"`
	PortfolioExcluded bool       `json:"portfolioExcluded"`
	LastActivityAt    *time.Time `json:"lastActivityAt"`
	ClosedOutcome     *string    `json:"closedOutcome"`
	InputComplete     bool       `json:"inputComplete"`
	MemoText          *string    `json:"memoText"`  // ADR-109: persisted generated decision memo
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

func mapDealToResponse(d queries.PipelineDeal) pipelineDealResponse {
	var lastActivityAt *time.Time
	if d.LastActivityAt.Valid {
		t := d.LastActivityAt.Time
		lastActivityAt = &t
	}
	return pipelineDealResponse{
		ID:                d.ID.String(),
		UserID:            d.UserID,
		Name:              d.Name,
		Source:            d.Source,
		Status:            d.Status,
		Notes:             textToPtr(d.Notes),
		PropertyCount:     d.PropertyCount,
		MemoCount:         d.MemoCount,
		PortfolioExcluded: d.PortfolioExcluded,
		LastActivityAt:    lastActivityAt,
		ClosedOutcome:     textToPtr(d.ClosedOutcome),
		InputComplete:     d.InputComplete,
		MemoText:          textToPtr(d.MemoText),
		CreatedAt:         d.CreatedAt,
		UpdatedAt:         d.UpdatedAt,
	}
}

// pipelinePropertyResponse is the camelCase JSON response for property endpoints.
// OmFileData is excluded (binary blob, never sent to client).
type pipelinePropertyResponse struct {
	ID                   string          `json:"id"`
	PipelineDealID       string          `json:"pipelineDealId"`
	Address              string          `json:"address"`
	City                 *string         `json:"city"`
	State                *string         `json:"state"`
	Zip                  *string         `json:"zip"`
	PropertyType         *string         `json:"propertyType"`
	Beds                 *float64        `json:"beds"`
	Baths                *float64        `json:"baths"`
	Sqft                 *int32          `json:"sqft"`
	YearBuilt            *int32          `json:"yearBuilt"`
	Units                *int32          `json:"units"`
	AskingPrice          *float64        `json:"askingPrice"`
	TargetPrice          *float64        `json:"targetPrice"`
	DownPaymentPct       *float64        `json:"downPaymentPct"`
	FinancingType        *string         `json:"financingType"`
	InterestRate         *float64        `json:"interestRate"`
	BrokerRent           *float64        `json:"brokerRent"`
	SystemRent           *float64        `json:"systemRent"`
	CurrentOccupancy     *float64        `json:"currentOccupancy"`
	ExpenseOverrides     json.RawMessage `json:"expenseOverrides"`
	UnitMix              json.RawMessage `json:"unitMix"`
	LotSqft              *int32          `json:"lotSqft"`
	BuildingCount        *int32          `json:"buildingCount"`
	Notes                *string         `json:"notes"`
	SourceType           string          `json:"sourceType"`
	OmData               json.RawMessage `json:"omData"`
	BrokerCapRate        *float64        `json:"brokerCapRate"`
	OmFilePath           *string         `json:"omFilePath"`
	OmFileName           *string         `json:"omFileName"`
	OmFileType           *string         `json:"omFileType"`
	OmValidationStatus   string          `json:"omValidationStatus"`
	OmQuestions          json.RawMessage `json:"omQuestions"`
	OmValidatedData      json.RawMessage `json:"omValidatedData"`
	PropertyCompleteness string          `json:"propertyCompleteness"`
	LeaseType            *string         `json:"leaseType"`
	TenantCount          *int32          `json:"tenantCount"`
	AnchorTenant         *string         `json:"anchorTenant"`
	WeightedAvgLeaseYrs  *float64        `json:"weightedAvgLeaseYrs"`
	CommercialSqft       *int32          `json:"commercialSqft"`
	CommercialMix        json.RawMessage `json:"commercialMix"`
	// ADR-108: physical detail columns from type-aware OM extraction
	YearRenovated        *int32          `json:"yearRenovated"`
	Stories              *int32          `json:"stories"`
	Zoning               *string         `json:"zoning"`
	Construction         *string         `json:"construction"`
	ParkingSpaces        *int32          `json:"parkingSpaces"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
}

func mapPropertyToResponse(p queries.PipelineProperty) pipelinePropertyResponse {
	return pipelinePropertyResponse{
		ID:                   p.ID.String(),
		PipelineDealID:       p.PipelineDealID.String(),
		Address:              p.Address,
		City:                 textToPtr(p.City),
		State:                textToPtr(p.State),
		Zip:                  textToPtr(p.Zip),
		PropertyType:         textToPtr(p.PropertyType),
		Beds:                 numericToFloat(p.Beds),
		Baths:                numericToFloat(p.Baths),
		Sqft:                 int4ToPtr(p.Sqft),
		YearBuilt:            int4ToPtr(p.YearBuilt),
		Units:                int4ToPtr(p.Units),
		AskingPrice:          numericToFloat(p.AskingPrice),
		TargetPrice:          numericToFloat(p.TargetPrice),
		DownPaymentPct:       numericToFloat(p.DownPaymentPct),
		FinancingType:        textToPtr(p.FinancingType),
		InterestRate:         numericToFloat(p.InterestRate),
		BrokerRent:           numericToFloat(p.BrokerRent),
		SystemRent:           numericToFloat(p.SystemRent),
		CurrentOccupancy:     numericToFloat(p.CurrentOccupancy),
		ExpenseOverrides:     rawJSONOrNil(p.ExpenseOverrides),
		UnitMix:              rawJSONOrNil(p.UnitMix),
		LotSqft:              int4ToPtr(p.LotSqft),
		BuildingCount:        int4ToPtr(p.BuildingCount),
		Notes:                textToPtr(p.Notes),
		SourceType:           p.SourceType,
		OmData:               rawJSONOrNil(p.OmData),
		BrokerCapRate:        numericToFloat(p.BrokerCapRate),
		OmFilePath:           textToPtr(p.OmFilePath),
		OmFileName:           textToPtr(p.OmFileName),
		OmFileType:           textToPtr(p.OmFileType),
		OmValidationStatus:   p.OmValidationStatus,
		OmQuestions:          rawJSONOrNil(p.OmQuestions),
		OmValidatedData:      rawJSONOrNil(p.OmValidatedData),
		PropertyCompleteness: p.PropertyCompleteness,
		LeaseType:            textToPtr(p.LeaseType),
		TenantCount:          int4ToPtr(p.TenantCount),
		AnchorTenant:         textToPtr(p.AnchorTenant),
		WeightedAvgLeaseYrs:  numericToFloat(p.WeightedAvgLeaseYrs),
		CommercialSqft:       int4ToPtr(p.CommercialSqft),
		CommercialMix:        rawJSONOrNil(p.CommercialMix),
		YearRenovated:        int4ToPtr(p.YearRenovated),
		Stories:              int4ToPtr(p.Stories),
		Zoning:               textToPtr(p.Zoning),
		Construction:         textToPtr(p.Construction),
		ParkingSpaces:        int4ToPtr(p.ParkingSpaces),
		CreatedAt:            p.CreatedAt,
		UpdatedAt:            p.UpdatedAt,
	}
}

// computePropertyCompleteness returns the completeness level for a property.
// ADR-107: 'incomplete' | 'sufficient' | 'complete'
// ADR-108: uses om_data fallback chains when structured columns are null (OM-sourced properties).
// Computed server-side on every create/update so the client always has fresh status.
func computePropertyCompleteness(prop queries.PipelineProperty) string {
	// Parse om_data for fallback values (ADR-108).
	var om struct {
		AskingPrice            *float64 `json:"askingPrice"`
		CapRate                *float64 `json:"capRate"`
		GrossPotentialRentCurrent *float64 `json:"grossPotentialRentCurrent"`
		GrossPotentialRent     *float64 `json:"grossPotentialRent"`
		NOICurrent             *float64 `json:"noiCurrent"`
		BrokerNOI              *float64 `json:"brokerNOI"`
		RentableSF             *float64 `json:"rentableSF"`
		BuildingSqft           *float64 `json:"buildingSqft"`
		VacancyPct             *float64 `json:"vacancyPct"`
		PropertyType           *string  `json:"propertyType"`
		RentByUnitType         []struct {
			Count *int `json:"count"`
		} `json:"rentByUnitType"`
		TenantSchedule []struct {
			TenantName string `json:"tenantName"`
		} `json:"tenantSchedule"`
	}
	hasOMData := false
	if len(prop.OmData) > 2 {
		if jerr := json.Unmarshal(prop.OmData, &om); jerr == nil {
			hasOMData = true
		}
	}

	// Asking price: structured column OR om fallback.
	hasAskingPrice := prop.AskingPrice.Valid || (hasOMData && om.AskingPrice != nil)

	// Broker rent: structured column OR om.GrossPotentialRentCurrent OR om.GrossPotentialRent.
	hasBrokerRent := prop.BrokerRent.Valid ||
		(hasOMData && (om.GrossPotentialRentCurrent != nil || om.GrossPotentialRent != nil))

	// Cap rate / NOI proxy: structured brokerCapRate OR om.CapRate OR om.NOICurrent OR om.BrokerNOI.
	hasBrokerCapRate := prop.BrokerCapRate.Valid ||
		(hasOMData && (om.CapRate != nil || om.NOICurrent != nil || om.BrokerNOI != nil))

	// Units: structured column OR sum count from rent roll.
	hasUnits := prop.Units.Valid
	if !hasUnits && hasOMData {
		for _, row := range om.RentByUnitType {
			if row.Count != nil && *row.Count > 0 {
				hasUnits = true
				break
			}
		}
	}

	// Sqft: structured column OR om.RentableSF OR om.BuildingSqft.
	hasSqft := prop.Sqft.Valid || (hasOMData && (om.RentableSF != nil || om.BuildingSqft != nil))

	// Unit mix rent check.
	hasUnitMixRent := string(prop.UnitMix) != "null" && len(prop.UnitMix) > 2
	if !hasUnitMixRent && hasOMData {
		hasUnitMixRent = len(om.RentByUnitType) > 0
	}

	// Commercial mix rows from manual entry (ADR-109).
	type commercialTenantRow struct {
		TenantName string   `json:"tenantName"`
		AnnualRent *float64 `json:"annualRent"`
	}
	hasCommercialMixRows := false
	if len(prop.CommercialMix) > 2 && string(prop.CommercialMix) != "null" {
		var cmRows []commercialTenantRow
		if jerr := json.Unmarshal(prop.CommercialMix, &cmRows); jerr == nil {
			hasCommercialMixRows = len(cmRows) > 0
		}
	}

	// Tenant schedule for NNN/retail/office: manual entry OR om_data.
	hasTenants := hasCommercialMixRows || (hasOMData && len(om.TenantSchedule) > 0)

	// Property type: structured column OR om.PropertyType (from Pass 1 classification).
	pType := ""
	if prop.PropertyType.Valid {
		pType = prop.PropertyType.String
	} else if hasOMData && om.PropertyType != nil {
		pType = *om.PropertyType
	}

	hasNOIProxy := hasBrokerCapRate

	var sufficient bool
	switch pType {
	case "sfh", "condo", "townhouse", "":
		sufficient = hasAskingPrice && (hasBrokerRent || hasNOIProxy)
	case "multifamily", "student_housing", "senior_housing":
		sufficient = hasAskingPrice && hasUnits && (hasBrokerRent || hasUnitMixRent)
	case "office", "commercial", "nnn", "retail":
		sufficient = hasAskingPrice && (hasBrokerRent || hasNOIProxy || hasTenants)
	case "warehouse", "industrial":
		sufficient = hasAskingPrice && hasSqft
	case "mixed_use":
		sufficient = hasAskingPrice && (hasUnits || hasSqft || hasUnitMixRent || hasCommercialMixRows || hasBrokerRent)
	case "self_storage":
		sufficient = hasAskingPrice && (hasSqft || hasNOIProxy)
	case "portfolio":
		sufficient = hasAskingPrice && hasNOIProxy
	default:
		sufficient = hasAskingPrice
	}

	if !sufficient {
		return "incomplete"
	}

	// 'complete' = sufficient + financing + occupancy + expense data
	hasFinancing := prop.DownPaymentPct.Valid && prop.InterestRate.Valid
	hasOccupancy := prop.CurrentOccupancy.Valid ||
		(hasOMData && om.VacancyPct != nil) // 1 - vacancyPct gives occupancy
	hasExpenses := len(prop.ExpenseOverrides) > 2 // non-null JSONB
	if hasFinancing && hasOccupancy && hasExpenses {
		return "complete"
	}
	return "sufficient"
}

// computeWALR calculates the Weighted Average Lease Remaining (in years) from a
// commercial_mix JSON array. Returns 0 if no parseable data.
// Formula: Σ(annualRent_i × remainingYears_i) / Σ(annualRent_i)
func computeWALR(commercialMixJSON []byte) float64 {
	if len(commercialMixJSON) <= 2 || string(commercialMixJSON) == "null" {
		return 0
	}
	type cmTenant struct {
		AnnualRent  *float64 `json:"annualRent"`
		LeaseExpiry *string  `json:"leaseExpiry"`
	}
	var tenants []cmTenant
	if err := json.Unmarshal(commercialMixJSON, &tenants); err != nil {
		return 0
	}
	now := time.Now()
	var weightedSum, rentSum float64
	for _, t := range tenants {
		if t.AnnualRent == nil || *t.AnnualRent <= 0 || t.LeaseExpiry == nil || *t.LeaseExpiry == "" {
			continue
		}
		// Parse MM/YYYY or YYYY-MM-DD or YYYY-MM
		expiry, err := parseLeaseExpiry(*t.LeaseExpiry)
		if err != nil {
			continue
		}
		remainingYears := expiry.Sub(now).Hours() / (365.25 * 24)
		if remainingYears < 0 {
			remainingYears = 0
		}
		weightedSum += *t.AnnualRent * remainingYears
		rentSum += *t.AnnualRent
	}
	if rentSum == 0 {
		return 0
	}
	return weightedSum / rentSum
}

// parseLeaseExpiry attempts to parse lease expiry in formats: MM/YYYY, YYYY-MM-DD, YYYY-MM.
func parseLeaseExpiry(s string) (time.Time, error) {
	// MM/YYYY
	if len(s) == 7 && s[2] == '/' {
		return time.Parse("01/2006", s)
	}
	// YYYY-MM-DD
	if len(s) == 10 {
		return time.Parse("2006-01-02", s)
	}
	// YYYY-MM
	if len(s) == 7 && s[4] == '-' {
		return time.Parse("2006-01", s)
	}
	return time.Time{}, fmt.Errorf("unparseable lease expiry: %s", s)
}

// getUserID extracts the authenticated user ID from the request context.
func getUserID(r *http.Request) (string, bool) {
	claims := middleware.GetUserFromContext(r.Context())
	if claims == nil {
		return "", false
	}
	return claims.UserID, true
}

// ---------------------------------------------------------------------------
// Deal CRUD
// ---------------------------------------------------------------------------

// ListDeals handles GET /api/pipeline/deals
func (h *Handler) ListDeals(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	includeArchived := r.URL.Query().Get("includeArchived") == "true"

	deals, err := h.store.Q().ListPipelineDeals(r.Context(), queries.ListPipelineDealsParams{
		UserID:          userID,
		IncludeArchived: includeArchived,
	})
	if err != nil {
		h.logger.Error("ListPipelineDeals failed", "error", err, "user_id", userID)
		httputil.InternalError(w, err)
		return
	}

	mapped := make([]pipelineDealResponse, len(deals))
	for i, d := range deals {
		mapped[i] = mapDealToResponse(d)
	}
	httputil.Success(w, mapped)
}

// CreateDeal handles POST /api/pipeline/deals
func (h *Handler) CreateDeal(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var req createDealRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}
	if req.Source == "" {
		req.Source = "broker"
	}

	deal, err := h.store.Q().CreatePipelineDeal(r.Context(), queries.CreatePipelineDealParams{
		UserID:            userID,
		Name:              req.Name,
		Source:            req.Source,
		Notes:             textVal(req.Notes),
		PortfolioExcluded: req.PortfolioExcluded,
	})
	if err != nil {
		h.logger.Error("CreatePipelineDeal failed", "error", err, "user_id", userID)
		httputil.InternalError(w, err)
		return
	}

	httputil.Created(w, mapDealToResponse(deal))
}

// pipelineStatsResponse is the camelCase JSON response for GET /api/pipeline/deals/stats.
// The sqlc-generated row uses snake_case json tags; this struct provides the camelCase
// format expected by the client.
type pipelineStatsResponse struct {
	ActiveDeals                     int            `json:"activeDeals"`
	PropertiesInPipeline            int            `json:"propertiesInPipeline"`
	DecisionMemosGenerated          int            `json:"decisionMemosGenerated"`
	DealsPendingDecision            int            `json:"dealsPendingDecision"`
	QualifiedCount                  int            `json:"qualifiedCount"`
	PendingCount                    int            `json:"pendingCount"`
	PassedCount                     int            `json:"passedCount"`
	TotalPipelineValue              float64        `json:"totalPipelineValue"`
	WeightedAvgCapRate              *float64       `json:"weightedAvgCapRate"`
	WeightedAvgCapRatePropertyCount int            `json:"weightedAvgCapRatePropertyCount"`
	TotalPropertiesWithRentData     int            `json:"totalPropertiesWithRentData"`
	SourceBreakdown                 map[string]int `json:"sourceBreakdown"`
}

// GetStats handles GET /api/pipeline/deals/stats
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	row, err := h.store.Q().GetPipelineStats(r.Context(), userID)
	if err != nil {
		h.logger.Error("GetPipelineStats failed", "error", err, "user_id", userID)
		httputil.InternalError(w, err)
		return
	}

	// WeightedAvgCapRate is null when no properties have system_rent.
	var weightedAvgCapRate *float64
	if row.WeightedAvgCapRatePropertyCount > 0 && row.WeightedAvgCapRate != 0 {
		v := row.WeightedAvgCapRate
		weightedAvgCapRate = &v
	}

	httputil.Success(w, pipelineStatsResponse{
		ActiveDeals:                     int(row.ActiveDeals),
		PropertiesInPipeline:            int(row.PropertiesInPipeline),
		DecisionMemosGenerated:          int(row.DecisionMemosGenerated),
		DealsPendingDecision:            int(row.DealsPendingDecision),
		QualifiedCount:                  int(row.QualifiedCount),
		PendingCount:                    int(row.PendingCount),
		PassedCount:                     int(row.PassedCount),
		TotalPipelineValue:              row.TotalPipelineValue,
		WeightedAvgCapRate:              weightedAvgCapRate,
		WeightedAvgCapRatePropertyCount: int(row.WeightedAvgCapRatePropertyCount),
		TotalPropertiesWithRentData:     int(row.TotalPropertiesWithRentData),
		SourceBreakdown: map[string]int{
			"broker":      int(row.SourceBroker),
			"off-market":  int(row.SourceOffMarket),
			"syndication": int(row.SourceSyndication),
			"jv":          int(row.SourceJv),
			"auction":     int(row.SourceAuction),
			"direct":      int(row.SourceDirect),
			"other":       int(row.SourceOther),
		},
	})
}

// GetDeal handles GET /api/pipeline/deals/{dealId}
func (h *Handler) GetDeal(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	dealID, err := uuid.Parse(chi.URLParam(r, "dealId"))
	if err != nil {
		httputil.BadRequest(w, "invalid deal ID")
		return
	}

	deal, err := h.store.Q().GetPipelineDeal(r.Context(), queries.GetPipelineDealParams{
		ID:     dealID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "deal not found")
		return
	}

	props, err := h.store.Q().ListPipelineProperties(r.Context(), queries.ListPipelinePropertiesParams{
		PipelineDealID: dealID,
		UserID:         userID,
	})
	if err != nil {
		h.logger.Error("ListPipelineProperties failed", "error", err)
		httputil.InternalError(w, err)
		return
	}

	mappedProps := make([]pipelinePropertyResponse, len(props))
	for i, p := range props {
		mappedProps[i] = mapPropertyToResponse(p)
	}
	httputil.Success(w, map[string]any{
		"deal":       mapDealToResponse(deal),
		"properties": mappedProps,
	})
}

// UpdateDeal handles PUT /api/pipeline/deals/{dealId}
func (h *Handler) UpdateDeal(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	dealID, err := uuid.Parse(chi.URLParam(r, "dealId"))
	if err != nil {
		httputil.BadRequest(w, "invalid deal ID")
		return
	}

	var req updateDealRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	var portfolioExcluded pgtype.Bool
	if req.PortfolioExcluded != nil {
		portfolioExcluded = pgtype.Bool{Bool: *req.PortfolioExcluded, Valid: true}
	}

	var inputComplete pgtype.Bool
	if req.InputComplete != nil {
		inputComplete = pgtype.Bool{Bool: *req.InputComplete, Valid: true}
	}

	deal, err := h.store.Q().UpdatePipelineDeal(r.Context(), queries.UpdatePipelineDealParams{
		ID:                dealID,
		UserID:            userID,
		Name:              textFromPtr(req.Name),
		Source:            textFromPtr(req.Source),
		Status:            textFromPtr(req.Status),
		Notes:             textFromPtr(req.Notes),
		PortfolioExcluded: portfolioExcluded,
		ClosedOutcome:     textFromPtr(req.ClosedOutcome),
		InputComplete:     inputComplete,
	})
	if err != nil {
		httputil.NotFound(w, "deal not found")
		return
	}

	httputil.Success(w, mapDealToResponse(deal))
}

// DeleteDeal handles DELETE /api/pipeline/deals/{dealId}
func (h *Handler) DeleteDeal(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	dealID, err := uuid.Parse(chi.URLParam(r, "dealId"))
	if err != nil {
		httputil.BadRequest(w, "invalid deal ID")
		return
	}

	rows, err := h.store.Q().DeletePipelineDeal(r.Context(), queries.DeletePipelineDealParams{
		ID:     dealID,
		UserID: userID,
	})
	if err != nil || rows == 0 {
		httputil.NotFound(w, "deal not found")
		return
	}

	httputil.NoContent(w)
}

// ---------------------------------------------------------------------------
// Property CRUD
// ---------------------------------------------------------------------------

// AddProperty handles POST /api/pipeline/deals/{dealId}/properties
func (h *Handler) AddProperty(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	dealID, err := uuid.Parse(chi.URLParam(r, "dealId"))
	if err != nil {
		httputil.BadRequest(w, "invalid deal ID")
		return
	}

	// Verify the deal belongs to this user.
	if _, err := h.store.Q().GetPipelineDeal(r.Context(), queries.GetPipelineDealParams{
		ID: dealID, UserID: userID,
	}); err != nil {
		httputil.NotFound(w, "deal not found")
		return
	}

	var req createPropertyRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Address == "" {
		httputil.BadRequest(w, "address is required")
		return
	}
	if req.SourceType == "" {
		req.SourceType = "manual"
	}

	prop, err := h.store.Q().CreatePipelineProperty(r.Context(), queries.CreatePipelinePropertyParams{
		PipelineDealID:      dealID,
		Address:             req.Address,
		City:                textVal(req.City),
		State:               textVal(req.State),
		Zip:                 textVal(req.Zip),
		PropertyType:        textVal(req.PropertyType),
		Beds:                numericFromFloat(req.Beds),
		Baths:               numericFromFloat(req.Baths),
		Sqft:                int4FromPtr(req.Sqft),
		YearBuilt:           int4FromPtr(req.YearBuilt),
		Units:               int4FromPtr(req.Units),
		AskingPrice:         numericFromFloat(req.AskingPrice),
		TargetPrice:         numericFromFloat(req.TargetPrice),
		DownPaymentPct:      numericFromFloat(req.DownPaymentPct),
		FinancingType:       textVal(req.FinancingType),
		InterestRate:        numericFromFloat(req.InterestRate),
		BrokerRent:          numericFromFloat(req.BrokerRent),
		SystemRent:          numericFromFloat(req.SystemRent),
		CurrentOccupancy:    numericFromFloat(req.CurrentOccupancy),
		ExpenseOverrides:    req.ExpenseOverrides,
		UnitMix:             req.UnitMix,
		LotSqft:             int4FromPtr(req.LotSqft),
		BuildingCount:       int4FromPtr(req.BuildingCount),
		Notes:               textVal(req.Notes),
		SourceType:          req.SourceType,
		LeaseType:           textVal(req.LeaseType),
		TenantCount:         int4FromPtr(req.TenantCount),
		AnchorTenant:        textVal(req.AnchorTenant),
		WeightedAvgLeaseYrs: numericFromFloat(req.WeightedAvgLeaseYrs),
		CommercialSqft:      int4FromPtr(req.CommercialSqft),
		CommercialMix:       req.CommercialMix,
	})
	if err != nil {
		h.logger.Error("CreatePipelineProperty failed", "error", err)
		httputil.InternalError(w, err)
		return
	}

	// Compute and store completeness (ADR-107).
	completeness := computePropertyCompleteness(prop)
	_ = h.store.Q().UpdatePropertyCompleteness(r.Context(), queries.UpdatePropertyCompletenessParams{
		ID:                   prop.ID,
		PropertyCompleteness: completeness,
	})
	prop.PropertyCompleteness = completeness

	// Update deal's property count.
	_ = h.store.Q().BumpPipelineDealActivity(r.Context(), dealID)

	httputil.Created(w, mapPropertyToResponse(prop))
}

// ListProperties handles GET /api/pipeline/deals/{dealId}/properties
func (h *Handler) ListProperties(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	dealID, err := uuid.Parse(chi.URLParam(r, "dealId"))
	if err != nil {
		httputil.BadRequest(w, "invalid deal ID")
		return
	}

	props, err := h.store.Q().ListPipelineProperties(r.Context(), queries.ListPipelinePropertiesParams{
		PipelineDealID: dealID,
		UserID:         userID,
	})
	if err != nil {
		h.logger.Error("ListPipelineProperties failed", "error", err)
		httputil.InternalError(w, err)
		return
	}

	mapped := make([]pipelinePropertyResponse, len(props))
	for i, p := range props {
		mapped[i] = mapPropertyToResponse(p)
	}
	httputil.Success(w, mapped)
}

// GetProperty handles GET /api/pipeline/deals/{dealId}/properties/{propId}
func (h *Handler) GetProperty(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	propID, err := uuid.Parse(chi.URLParam(r, "propId"))
	if err != nil {
		httputil.BadRequest(w, "invalid property ID")
		return
	}

	prop, err := h.store.Q().GetPipelineProperty(r.Context(), queries.GetPipelinePropertyParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "property not found")
		return
	}

	httputil.Success(w, mapPropertyToResponse(prop))
}

// UpdateProperty handles PUT /api/pipeline/deals/{dealId}/properties/{propId}
func (h *Handler) UpdateProperty(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	propID, err := uuid.Parse(chi.URLParam(r, "propId"))
	if err != nil {
		httputil.BadRequest(w, "invalid property ID")
		return
	}

	// Verify ownership.
	if _, err := h.store.Q().GetPipelineProperty(r.Context(), queries.GetPipelinePropertyParams{
		ID: propID, UserID: userID,
	}); err != nil {
		httputil.NotFound(w, "property not found")
		return
	}

	var req updatePropertyRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	var expenseOverrides []byte
	if req.ExpenseOverrides != nil {
		expenseOverrides = *req.ExpenseOverrides
	}
	var unitMix []byte
	if req.UnitMix != nil {
		unitMix = *req.UnitMix
	}
	var commercialMix []byte
	if req.CommercialMix != nil {
		commercialMix = *req.CommercialMix
	}

	prop, err := h.store.Q().UpdatePipelineProperty(r.Context(), queries.UpdatePipelinePropertyParams{
		ID:                  propID,
		Address:             textFromPtr(req.Address),
		City:                textFromPtr(req.City),
		State:               textFromPtr(req.State),
		Zip:                 textFromPtr(req.Zip),
		PropertyType:        textFromPtr(req.PropertyType),
		Beds:                numericFromFloatPtr(req.Beds),
		Baths:               numericFromFloatPtr(req.Baths),
		Sqft:                int4FromPtr(req.Sqft),
		YearBuilt:           int4FromPtr(req.YearBuilt),
		Units:               int4FromPtr(req.Units),
		AskingPrice:         numericFromFloatPtr(req.AskingPrice),
		TargetPrice:         numericFromFloatPtr(req.TargetPrice),
		DownPaymentPct:      numericFromFloatPtr(req.DownPaymentPct),
		FinancingType:       textFromPtr(req.FinancingType),
		InterestRate:        numericFromFloatPtr(req.InterestRate),
		BrokerRent:          numericFromFloatPtr(req.BrokerRent),
		SystemRent:          numericFromFloatPtr(req.SystemRent),
		CurrentOccupancy:    numericFromFloatPtr(req.CurrentOccupancy),
		ExpenseOverrides:    expenseOverrides,
		UnitMix:             unitMix,
		LotSqft:             int4FromPtr(req.LotSqft),
		BuildingCount:       int4FromPtr(req.BuildingCount),
		Notes:               textFromPtr(req.Notes),
		LeaseType:           textFromPtr(req.LeaseType),
		TenantCount:         int4FromPtr(req.TenantCount),
		AnchorTenant:        textFromPtr(req.AnchorTenant),
		WeightedAvgLeaseYrs: numericFromFloatPtr(req.WeightedAvgLeaseYrs),
		CommercialSqft:      int4FromPtr(req.CommercialSqft),
		CommercialMix:       commercialMix,
	})
	if err != nil {
		h.logger.Error("UpdatePipelineProperty failed", "error", err)
		httputil.InternalError(w, err)
		return
	}

	// Recompute completeness after update (ADR-107).
	completeness := computePropertyCompleteness(prop)
	_ = h.store.Q().UpdatePropertyCompleteness(r.Context(), queries.UpdatePropertyCompletenessParams{
		ID:                   prop.ID,
		PropertyCompleteness: completeness,
	})
	prop.PropertyCompleteness = completeness

	httputil.Success(w, mapPropertyToResponse(prop))
}

// DeleteProperty handles DELETE /api/pipeline/deals/{dealId}/properties/{propId}
func (h *Handler) DeleteProperty(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	propID, err := uuid.Parse(chi.URLParam(r, "propId"))
	if err != nil {
		httputil.BadRequest(w, "invalid property ID")
		return
	}

	dealID, err := uuid.Parse(chi.URLParam(r, "dealId"))
	if err != nil {
		httputil.BadRequest(w, "invalid deal ID")
		return
	}

	rows, err := h.store.Q().DeletePipelineProperty(r.Context(), queries.DeletePipelinePropertyParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil || rows == 0 {
		httputil.NotFound(w, "property not found")
		return
	}

	// Refresh the deal's property count.
	_ = h.store.Q().BumpPipelineDealActivity(r.Context(), dealID)

	httputil.NoContent(w)
}

// UpdateOMValidated handles PATCH /api/pipeline/deals/{dealId}/properties/{propId}/om-validated
// Saves user-edited OM fields to om_validated_data.
// ?confirm=true transitions om_validation_status to 'validated'.
// ADR-108: blur-save pattern — each field edit calls this endpoint; "Save & Confirm" adds ?confirm=true.
func (h *Handler) UpdateOMValidated(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	propID, err := uuid.Parse(chi.URLParam(r, "propId"))
	if err != nil {
		httputil.BadRequest(w, "invalid property ID")
		return
	}

	// Verify ownership.
	if _, err := h.store.Q().GetPipelineProperty(r.Context(), queries.GetPipelinePropertyParams{
		ID: propID, UserID: userID,
	}); err != nil {
		httputil.NotFound(w, "property not found")
		return
	}

	// Parse body — must be valid JSON (the omValidatedData object).
	var body json.RawMessage
	if err := httputil.DecodeJSON(r, &body); err != nil {
		httputil.BadRequest(w, "invalid JSON body")
		return
	}

	confirm := r.URL.Query().Get("confirm") == "true"

	prop, err := h.store.Q().UpdateOMValidatedData(r.Context(), queries.UpdateOMValidatedDataParams{
		ID:              propID,
		OmValidatedData: []byte(body),
		Column3:         confirm,
	})
	if err != nil {
		h.logger.Error("UpdateOMValidatedData failed", "propId", propID, "error", err)
		httputil.InternalError(w, err)
		return
	}

	httputil.Success(w, mapPropertyToResponse(prop))
}

// GetRetrospective handles GET /api/pipeline/retrospective?days=90
// Returns pipeline activity analytics for the given period (0 = all time).
func (h *Handler) GetRetrospective(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	daysStr := r.URL.Query().Get("days")
	days := 90 // default
	if daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil && d >= 0 {
			days = d
		}
	}

	row, err := h.store.Q().GetPipelineRetrospective(r.Context(), queries.GetPipelineRetrospectiveParams{
		UserID:  userID,
		Column2: int32(days),
	})
	if err != nil {
		h.logger.Error("GetPipelineRetrospective failed", "error", err, "user_id", userID)
		httputil.InternalError(w, err)
		return
	}

	period := "90d"
	switch days {
	case 0:
		period = "all"
	case 30:
		period = "30d"
	case 365:
		period = "1y"
	default:
		period = strconv.Itoa(days) + "d"
	}

	httputil.Success(w, map[string]any{
		"period":               period,
		"totalEvaluations":     row.TotalEvaluations,
		"discoveryEvaluations": row.DiscoveryEvaluations,
		"pipelineEvaluations":  row.PipelineEvaluations,
		"pipelineBySource": map[string]any{
			"broker":      row.SourceBroker,
			"off-market":  row.SourceOffMarket,
			"syndication": row.SourceSyndication,
			"jv":          row.SourceJv,
			"auction":     row.SourceAuction,
			"direct":      row.SourceDirect,
			"other":       row.SourceOther,
		},
		"proceededDeals":    row.ProceededDeals,
		"totalPipelineDeals": row.TotalPipelineDeals,
		"activePipelineDeals": row.ActivePipelineDeals,
	})
}

// GetRentEstimate handles GET /api/pipeline/deals/{dealId}/properties/{propId}/rent-estimate
// Returns system rent estimate from the market aggregator for the property's city/state.
func (h *Handler) GetRentEstimate(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	propID, err := uuid.Parse(chi.URLParam(r, "propId"))
	if err != nil {
		httputil.BadRequest(w, "invalid property ID")
		return
	}

	prop, err := h.store.Q().GetPipelineProperty(r.Context(), queries.GetPipelinePropertyParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "property not found")
		return
	}

	if !prop.City.Valid || !prop.State.Valid {
		httputil.BadRequest(w, "property city and state are required for rent estimate")
		return
	}

	if h.aggregator == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market data unavailable")
		return
	}

	marketData, err := h.aggregator.GetMarketData(r.Context(), prop.City.String, prop.State.String)
	if err != nil || marketData == nil {
		// Non-fatal — return empty estimate rather than an error.
		httputil.Success(w, map[string]any{
			"systemRent": nil,
			"marketData": nil,
		})
		return
	}

	httputil.Success(w, map[string]any{
		"systemRent": marketData.MedianRent,
		"marketData": map[string]any{
			"medianRent":   marketData.MedianRent,
			"vacancyRate":  marketData.VacancyRate,
			"city":         prop.City.String,
			"state":        prop.State.String,
		},
	})
}
