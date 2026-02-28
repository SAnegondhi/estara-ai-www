// Package pipeline provides handlers for the Investment Pipeline feature (ADR-101).
package pipeline

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles pipeline-related HTTP requests.
type Handler struct {
	store      *dbstore.Store
	cfg        *config.Config
	aggregator *aggregator.Aggregator
	logger     *slog.Logger
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
}

type createPropertyRequest struct {
	Address          string          `json:"address"`
	City             string          `json:"city"`
	State            string          `json:"state"`
	Zip              string          `json:"zip"`
	PropertyType     string          `json:"propertyType"`
	Beds             *float64        `json:"beds"`
	Baths            *float64        `json:"baths"`
	Sqft             *int32          `json:"sqft"`
	YearBuilt        *int32          `json:"yearBuilt"`
	Units            *int32          `json:"units"`
	AskingPrice      *float64        `json:"askingPrice"`
	TargetPrice      *float64        `json:"targetPrice"`
	DownPaymentPct   *float64        `json:"downPaymentPct"`
	FinancingType    string          `json:"financingType"`
	InterestRate     *float64        `json:"interestRate"`
	BrokerRent       *float64        `json:"brokerRent"`
	SystemRent       *float64        `json:"systemRent"`
	CurrentOccupancy *float64        `json:"currentOccupancy"`
	ExpenseOverrides json.RawMessage `json:"expenseOverrides"`
	SourceType       string          `json:"sourceType"`
}

type updatePropertyRequest struct {
	Address          *string          `json:"address"`
	City             *string          `json:"city"`
	State            *string          `json:"state"`
	Zip              *string          `json:"zip"`
	PropertyType     *string          `json:"propertyType"`
	Beds             *float64         `json:"beds"`
	Baths            *float64         `json:"baths"`
	Sqft             *int32           `json:"sqft"`
	YearBuilt        *int32           `json:"yearBuilt"`
	Units            *int32           `json:"units"`
	AskingPrice      *float64         `json:"askingPrice"`
	TargetPrice      *float64         `json:"targetPrice"`
	DownPaymentPct   *float64         `json:"downPaymentPct"`
	FinancingType    *string          `json:"financingType"`
	InterestRate     *float64         `json:"interestRate"`
	BrokerRent       *float64         `json:"brokerRent"`
	SystemRent       *float64         `json:"systemRent"`
	CurrentOccupancy *float64         `json:"currentOccupancy"`
	ExpenseOverrides *json.RawMessage `json:"expenseOverrides"`
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

	httputil.Success(w, deals)
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

	httputil.Created(w, deal)
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

	httputil.Success(w, map[string]any{
		"deal":       deal,
		"properties": props,
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

	deal, err := h.store.Q().UpdatePipelineDeal(r.Context(), queries.UpdatePipelineDealParams{
		ID:                dealID,
		UserID:            userID,
		Name:              textFromPtr(req.Name),
		Source:            textFromPtr(req.Source),
		Status:            textFromPtr(req.Status),
		Notes:             textFromPtr(req.Notes),
		PortfolioExcluded: portfolioExcluded,
		ClosedOutcome:     textFromPtr(req.ClosedOutcome),
	})
	if err != nil {
		httputil.NotFound(w, "deal not found")
		return
	}

	httputil.Success(w, deal)
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
		PipelineDealID:   dealID,
		Address:          req.Address,
		City:             textVal(req.City),
		State:            textVal(req.State),
		Zip:              textVal(req.Zip),
		PropertyType:     textVal(req.PropertyType),
		Beds:             numericFromFloat(req.Beds),
		Baths:            numericFromFloat(req.Baths),
		Sqft:             int4FromPtr(req.Sqft),
		YearBuilt:        int4FromPtr(req.YearBuilt),
		Units:            int4FromPtr(req.Units),
		AskingPrice:      numericFromFloat(req.AskingPrice),
		TargetPrice:      numericFromFloat(req.TargetPrice),
		DownPaymentPct:   numericFromFloat(req.DownPaymentPct),
		FinancingType:    textVal(req.FinancingType),
		InterestRate:     numericFromFloat(req.InterestRate),
		BrokerRent:       numericFromFloat(req.BrokerRent),
		SystemRent:       numericFromFloat(req.SystemRent),
		CurrentOccupancy: numericFromFloat(req.CurrentOccupancy),
		ExpenseOverrides: req.ExpenseOverrides,
		SourceType:       req.SourceType,
	})
	if err != nil {
		h.logger.Error("CreatePipelineProperty failed", "error", err)
		httputil.InternalError(w, err)
		return
	}

	// Update deal's property count.
	_ = h.store.Q().BumpPipelineDealActivity(r.Context(), dealID)

	httputil.Created(w, prop)
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

	httputil.Success(w, props)
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

	httputil.Success(w, prop)
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

	prop, err := h.store.Q().UpdatePipelineProperty(r.Context(), queries.UpdatePipelinePropertyParams{
		ID:               propID,
		Address:          textFromPtr(req.Address),
		City:             textFromPtr(req.City),
		State:            textFromPtr(req.State),
		Zip:              textFromPtr(req.Zip),
		PropertyType:     textFromPtr(req.PropertyType),
		Beds:             numericFromFloatPtr(req.Beds),
		Baths:            numericFromFloatPtr(req.Baths),
		Sqft:             int4FromPtr(req.Sqft),
		YearBuilt:        int4FromPtr(req.YearBuilt),
		Units:            int4FromPtr(req.Units),
		AskingPrice:      numericFromFloatPtr(req.AskingPrice),
		TargetPrice:      numericFromFloatPtr(req.TargetPrice),
		DownPaymentPct:   numericFromFloatPtr(req.DownPaymentPct),
		FinancingType:    textFromPtr(req.FinancingType),
		InterestRate:     numericFromFloatPtr(req.InterestRate),
		BrokerRent:       numericFromFloatPtr(req.BrokerRent),
		SystemRent:       numericFromFloatPtr(req.SystemRent),
		CurrentOccupancy: numericFromFloatPtr(req.CurrentOccupancy),
		ExpenseOverrides: expenseOverrides,
	})
	if err != nil {
		h.logger.Error("UpdatePipelineProperty failed", "error", err)
		httputil.InternalError(w, err)
		return
	}

	httputil.Success(w, prop)
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
