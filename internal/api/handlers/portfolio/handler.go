package portfolio

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/api/handlers/util"
	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/expenses"
	"github.com/estara-ai/www/internal/services/investment/projection"
	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles portfolio-related HTTP requests
type Handler struct {
	store           *dbstore.Store
	cfg             *config.Config
	validate        *validator.Validate
	logger          *slog.Logger
	propertyFinder  *finder.Orchestrator
	expenseCalc     *expenses.Calculator
}

// NewHandler creates a new portfolio handler
func NewHandler(store *dbstore.Store, cfg *config.Config) *Handler {
	return &Handler{
		store:       store,
		cfg:         cfg,
		validate:    validator.New(),
		logger:      slog.Default().With("component", "portfolio_handler"),
		expenseCalc: expenses.NewCalculator(),
	}
}

// SetPropertyFinder sets the property finder orchestrator for address lookup
func (h *Handler) SetPropertyFinder(pf *finder.Orchestrator) {
	h.propertyFinder = pf
}

// PropertyExpenses represents the breakdown of property expenses
type PropertyExpenses struct {
	Maintenance float64 `json:"maintenance,omitempty"` // Monthly maintenance
	Tax         float64 `json:"tax,omitempty"`         // Monthly property tax
	Insurance   float64 `json:"insurance,omitempty"`   // Monthly insurance
	HOA         float64 `json:"hoa,omitempty"`         // Monthly HOA fees
	Other       float64 `json:"other,omitempty"`       // Monthly other expenses
}

// PortfolioProperty represents a property in the user's portfolio
type PortfolioProperty struct {
	ID              string     `json:"id"`
	UserID          string     `json:"userId"`
	Address         string     `json:"address"`
	City            string     `json:"city"`
	State           string     `json:"state"`
	ZipCode         *string    `json:"zipCode,omitempty"`
	PropertyType    *string    `json:"propertyType,omitempty"`
	PropertyStatus  string     `json:"propertyStatus"`
	Beds            *int       `json:"beds,omitempty"`
	Baths           *float64   `json:"baths,omitempty"`
	Sqft            *int       `json:"sqft,omitempty"`
	YearBuilt       *int       `json:"yearBuilt,omitempty"`
	PurchasePrice   float64    `json:"purchasePrice"`
	PurchaseDate    *time.Time `json:"purchaseDate,omitempty"`
	CurrentValue    float64    `json:"currentValue"`
	MonthlyRent     float64    `json:"monthlyRent"`
	VacancyRate     *float64   `json:"vacancyRate,omitempty"`  // Vacancy rate percentage
	Expenses        *PropertyExpenses `json:"expenses,omitempty"` // Expense breakdown
	MortgageBalance float64    `json:"mortgageBalance"`
	MortgageRate    float64    `json:"mortgageRate"`
	MortgagePayment float64    `json:"mortgagePayment"`
	MonthlyExpenses float64    `json:"monthlyExpenses"` // Total monthly expenses (calculated)
	Status          string     `json:"status"`
	Notes           *string    `json:"notes,omitempty"`
	ImageURL        *string    `json:"imageUrl,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	// Calculated fields
	Equity          float64 `json:"equity"`
	MonthlyCashFlow float64 `json:"monthlyCashFlow"`
	CapRate         float64 `json:"capRate"`
	CashOnCash      float64 `json:"cashOnCash"`
}

// CreatePropertyRequest represents a request to add a property
type CreatePropertyRequest struct {
	Address         string   `json:"address" validate:"required"`
	City            string   `json:"city" validate:"required"`
	State           string   `json:"state" validate:"required,len=2"`
	ZipCode         string   `json:"zipCode" validate:"required"`
	PropertyType    *string  `json:"propertyType,omitempty"`
	PropertyStatus  *string  `json:"propertyStatus,omitempty" validate:"omitempty,oneof=owner_occupied rented rental_vacant"`
	Beds            *int     `json:"beds,omitempty"`
	Baths           *float64 `json:"baths,omitempty"`
	Sqft            *int     `json:"sqft,omitempty"`
	YearBuilt       *int     `json:"yearBuilt,omitempty"`
	PurchasePrice   float64  `json:"purchasePrice" validate:"required,gt=0"`
	PurchaseDate    *string  `json:"purchaseDate,omitempty"`
	CurrentValue    *float64 `json:"currentValue,omitempty"`
	MonthlyRent     *float64 `json:"monthlyRent,omitempty"`
	VacancyRate     *float64 `json:"vacancyRate,omitempty"`     // Vacancy rate percentage
	Expenses        *PropertyExpenses `json:"expenses,omitempty"` // Expense breakdown
	MortgageBalance *float64 `json:"mortgageBalance,omitempty"`
	MortgageRate    *float64 `json:"mortgageRate,omitempty"`
	MortgagePayment *float64 `json:"mortgagePayment,omitempty"`
	MonthlyExpenses *float64 `json:"monthlyExpenses,omitempty"` // Deprecated: use Expenses instead
	Notes           *string  `json:"notes,omitempty"`
	ImageURL        *string  `json:"imageUrl,omitempty"`
}

// UpdatePropertyRequest represents a request to update a property
type UpdatePropertyRequest struct {
	Address         *string  `json:"address,omitempty"`
	City            *string  `json:"city,omitempty"`
	State           *string  `json:"state,omitempty" validate:"omitempty,len=2"`
	ZipCode         *string  `json:"zipCode,omitempty"`
	PropertyType    *string  `json:"propertyType,omitempty"`
	PropertyStatus  *string  `json:"propertyStatus,omitempty" validate:"omitempty,oneof=owner_occupied rented rental_vacant"`
	Beds            *int     `json:"beds,omitempty"`
	Baths           *float64 `json:"baths,omitempty"`
	Sqft            *int     `json:"sqft,omitempty"`
	YearBuilt       *int     `json:"yearBuilt,omitempty"`
	PurchasePrice   *float64 `json:"purchasePrice,omitempty" validate:"omitempty,gt=0"`
	PurchaseDate    *string  `json:"purchaseDate,omitempty"`
	CurrentValue    *float64 `json:"currentValue,omitempty"`
	MonthlyRent     *float64 `json:"monthlyRent,omitempty"`
	VacancyRate     *float64 `json:"vacancyRate,omitempty"`     // Vacancy rate percentage
	Expenses        *PropertyExpenses `json:"expenses,omitempty"` // Expense breakdown
	MortgageBalance *float64 `json:"mortgageBalance,omitempty"`
	MortgageRate    *float64 `json:"mortgageRate,omitempty"`
	MortgagePayment *float64 `json:"mortgagePayment,omitempty"`
	MonthlyExpenses *float64 `json:"monthlyExpenses,omitempty"` // Deprecated: use Expenses instead
	Status          *string  `json:"status,omitempty" validate:"omitempty,oneof=active sold inactive"`
	Notes           *string  `json:"notes,omitempty"`
	ImageURL        *string  `json:"imageUrl,omitempty"`
}

// PortfolioSummary represents summary metrics for the portfolio
type PortfolioSummary struct {
	TotalProperties int     `json:"totalProperties"`
	TotalValue      float64 `json:"totalValue"`
	TotalEquity     float64 `json:"totalEquity"`
	TotalDebt       float64 `json:"totalDebt"`
	MonthlyIncome   float64 `json:"monthlyIncome"`
	MonthlyExpenses float64 `json:"monthlyExpenses"`
	MonthlyCashFlow float64 `json:"monthlyCashFlow"`
	AverageCapRate  float64 `json:"averageCapRate"`
}

// Helper functions for safely dereferencing pointer types
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

func derefFloat64(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// Helper functions for converting pgtype to pointer types
func pgtextToStringPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func pgint4ToIntPtr(i pgtype.Int4) *int {
	if !i.Valid {
		return nil
	}
	val := int(i.Int32)
	return &val
}

func pgfloat8ToFloat64Ptr(f pgtype.Float8) *float64 {
	if !f.Valid {
		return nil
	}
	return &f.Float64
}

func pgtimestampToTimePtr(ts pgtype.Timestamp) *time.Time {
	if !ts.Valid {
		return nil
	}
	return &ts.Time
}

// AllocationItem represents allocation breakdown by category
type AllocationItem struct {
	Name    string  `json:"name"`
	Value   float64 `json:"value"`
	Percent float64 `json:"percent"`
	Count   int     `json:"count"`
}

// PropertyScoreFactors represents the factors that contribute to a property score
type PropertyScoreFactors struct {
	CapRate      int `json:"capRate"`
	CashFlow     int `json:"cashFlow"`
	Equity       int `json:"equity"`
	Appreciation int `json:"appreciation"`
}

// PropertyScore represents a property's performance score
type PropertyScore struct {
	PropertyID string               `json:"propertyId"`
	Address    string               `json:"address"`
	Score      int                  `json:"score"`
	Grade      string               `json:"grade"`
	Factors    PropertyScoreFactors `json:"factors"`
}

// PortfolioCumulativeMetrics represents lifetime/cumulative metrics
type PortfolioCumulativeMetrics struct {
	OldestPurchaseDate           string  `json:"oldestPurchaseDate"`
	AvgYearsOwned                float64 `json:"avgYearsOwned"`
	TotalRentCollected           float64 `json:"totalRentCollected"`
	TotalExpensesPaid            float64 `json:"totalExpensesPaid"`
	TotalMortgagePaid            float64 `json:"totalMortgagePaid"`
	TotalCashFlow                float64 `json:"totalCashFlow"`
	TotalPrincipalPaidDown       float64 `json:"totalPrincipalPaidDown"`
	TotalAppreciationGain        float64 `json:"totalAppreciationGain"`
	TotalEquityBuilt             float64 `json:"totalEquityBuilt"`
	TotalReturn                  float64 `json:"totalReturn"`
	AnnualizedReturn             float64 `json:"annualizedReturn"`
	PortfolioCashOnCashLifetime  float64 `json:"portfolioCashOnCashLifetime"`
}

// MetricsResponse represents the comprehensive portfolio metrics response
type MetricsResponse struct {
	Success     bool             `json:"success"`
	Metrics     PortfolioMetrics `json:"metrics"`
	GeneratedAt string           `json:"generatedAt"`
}

// PortfolioMetrics represents comprehensive portfolio analytics
type PortfolioMetrics struct {
	// Basic metrics
	TotalValue         float64 `json:"totalValue"`
	TotalEquity        float64 `json:"totalEquity"`
	TotalDebt          float64 `json:"totalDebt"`
	MonthlyGrossIncome float64 `json:"monthlyGrossIncome"`
	MonthlyNetIncome   float64 `json:"monthlyNetIncome"`
	MonthlyCashFlow    float64 `json:"monthlyCashFlow"`
	PropertyCount      int     `json:"propertyCount"`

	// Performance metrics
	PortfolioCapRate    float64 `json:"portfolioCapRate"`
	CashOnCashReturn    float64 `json:"cashOnCashReturn"`
	LTV                 float64 `json:"ltv"`
	DSCR                float64 `json:"dscr"`
	GrossYield          float64 `json:"grossYield"`
	AverageVacancyRate  float64 `json:"averageVacancyRate"`

	// Allocation breakdowns
	AllocationByMarket []AllocationItem `json:"allocationByMarket"`
	AllocationByType   []AllocationItem `json:"allocationByType"`
	AllocationByState  []AllocationItem `json:"allocationByState"`

	// Property scores
	PropertyScores []PropertyScore `json:"propertyScores"`
	BestPerformer  *PropertyScore  `json:"bestPerformer"`
	WorstPerformer *PropertyScore  `json:"worstPerformer"`
	AverageScore   int             `json:"averageScore"`

	// Lifetime metrics
	CumulativeMetrics PortfolioCumulativeMetrics `json:"cumulativeMetrics"`
}

// ListResponse wraps the list response
type ListResponse struct {
	Success    bool                `json:"success"`
	Properties []PortfolioProperty `json:"properties"`
	Summary    PortfolioSummary    `json:"summary"`
}

// List returns all properties in the user's portfolio
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	h.logger.Info("listing portfolio properties", "userId", user.UserID)

	// Query properties
	properties, err := h.getPropertiesByUserID(ctx, user.UserID)
	if err != nil {
		h.logger.Error("failed to get properties", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to retrieve portfolio")
		return
	}

	// Calculate summary
	summary := h.calculateSummary(properties)

	response := ListResponse{
		Success:    true,
		Properties: properties,
		Summary:    summary,
	}

	httputil.JSON(w, http.StatusOK, response)
}

// Create adds a new property to the portfolio
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	// Parse request
	var req CreatePropertyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate request
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	h.logger.Info("creating portfolio property",
		"userId", user.UserID,
		"address", req.Address,
	)

	// Create property
	property, err := h.createProperty(ctx, user.UserID, &req)
	if err != nil {
		h.logger.Error("failed to create property", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to create property")
		return
	}

	// Log feature usage for customer support and chargeback defense
	_ = util.LogFeatureUsage(ctx, h.store, r, user.UserID, "FEATURE_PORTFOLIO_ADD",
		fmt.Sprintf("Property added: %s, %s %s", req.Address, req.City, req.State),
		map[string]any{
			"propertyId":    property.ID,
			"address":       req.Address,
			"city":          req.City,
			"state":         req.State,
			"zipCode":       req.ZipCode,
			"purchasePrice": req.PurchasePrice,
		},
	)

	httputil.JSON(w, http.StatusCreated, map[string]interface{}{
		"success":  true,
		"property": property,
	})
}

// Get returns a specific property from the portfolio
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	propertyID := chi.URLParam(r, "id")
	if propertyID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	h.logger.Info("getting portfolio property",
		"userId", user.UserID,
		"propertyId", propertyID,
	)

	// Get property
	property, err := h.getPropertyByID(ctx, propertyID, user.UserID)
	if err != nil {
		if err.Error() == "property not found" {
			httputil.NotFound(w, "property not found")
			return
		}
		h.logger.Error("failed to get property", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to retrieve property")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"property": property,
	})
}

// Update updates a property in the portfolio
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	propertyID := chi.URLParam(r, "id")
	if propertyID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	// Parse request
	var req UpdatePropertyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate request
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	h.logger.Info("updating portfolio property",
		"userId", user.UserID,
		"propertyId", propertyID,
		"hasPurchaseDate", req.PurchaseDate != nil,
		"purchaseDate", req.PurchaseDate,
	)

	// Update property
	property, err := h.updateProperty(ctx, propertyID, user.UserID, &req)
	if err != nil {
		if err.Error() == "property not found" {
			httputil.NotFound(w, "property not found")
			return
		}
		h.logger.Error("failed to update property", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to update property")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"property": property,
	})
}

// Delete removes a property from the portfolio
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	propertyID := chi.URLParam(r, "id")
	if propertyID == "" {
		httputil.BadRequest(w, "id is required")
		return
	}

	h.logger.Info("deleting portfolio property",
		"userId", user.UserID,
		"propertyId", propertyID,
	)

	// Delete property
	err := h.deleteProperty(ctx, propertyID, user.UserID)
	if err != nil {
		if err.Error() == "property not found" {
			httputil.NotFound(w, "property not found")
			return
		}
		h.logger.Error("failed to delete property", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to delete property")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "property deleted",
	})
}

// Database operations

// mapDBPropertyToPortfolioProperty converts a sqlc-generated V2PortfolioProperty to the handler's PortfolioProperty type.
func mapDBPropertyToPortfolioProperty(dp queries.V2PortfolioProperty) PortfolioProperty {
	p := PortfolioProperty{
		ID:             dp.ID,
		UserID:         dp.UserID,
		Address:        dp.Address,
		City:           dp.City,
		State:          dp.State,
		PurchasePrice:  dp.PurchasePrice,
		PropertyStatus: dp.PropertyStatus,
		Status:         dp.Status,
	}
	if dp.ZipCode != "" {
		z := dp.ZipCode
		p.ZipCode = &z
	}
	if dp.PropertyType.Valid {
		p.PropertyType = &dp.PropertyType.String
	}
	if dp.Bedrooms.Valid {
		b := int(dp.Bedrooms.Int32)
		p.Beds = &b
	}
	if dp.Bathrooms.Valid {
		p.Baths = &dp.Bathrooms.Float64
	}
	if dp.Sqft.Valid {
		s := int(dp.Sqft.Int32)
		p.Sqft = &s
	}
	if dp.YearBuilt.Valid {
		y := int(dp.YearBuilt.Int32)
		p.YearBuilt = &y
	}
	if dp.PurchaseDate.Valid {
		p.PurchaseDate = &dp.PurchaseDate.Time
	}
	if dp.CurrentValue.Valid {
		p.CurrentValue = dp.CurrentValue.Float64
	}
	if dp.MonthlyRent.Valid {
		p.MonthlyRent = dp.MonthlyRent.Float64
	}
	if dp.VacancyRate.Valid {
		p.VacancyRate = &dp.VacancyRate.Float64
	}
	if dp.MortgageBalance.Valid {
		p.MortgageBalance = dp.MortgageBalance.Float64
	}
	if dp.MortgageRate.Valid {
		p.MortgageRate = dp.MortgageRate.Float64
	}
	if dp.MortgagePayment.Valid {
		p.MortgagePayment = dp.MortgagePayment.Float64
	}
	if dp.Notes.Valid {
		p.Notes = &dp.Notes.String
	}
	if dp.CreatedAt.Valid {
		p.CreatedAt = dp.CreatedAt.Time
	}
	if dp.UpdatedAt.Valid {
		p.UpdatedAt = dp.UpdatedAt.Time
	}
	return p
}

func (h *Handler) getPropertiesByUserID(ctx context.Context, userID string) ([]PortfolioProperty, error) {
	dbProps, err := h.store.Q().ListPortfolioProperties(ctx, userID)
	if err != nil {
		return nil, err
	}

	properties := make([]PortfolioProperty, 0, len(dbProps))
	for _, dp := range dbProps {
		p := mapDBPropertyToPortfolioProperty(dp)

		// Parse expenses
		if len(dp.Expenses) > 0 {
			var exp PropertyExpenses
			if err := json.Unmarshal(dp.Expenses, &exp); err == nil {
				p.Expenses = &exp
				p.MonthlyExpenses = exp.Maintenance + exp.Tax + exp.Insurance + exp.HOA + exp.Other
			}
		}

		// Calculate derived fields
		h.calculatePropertyMetrics(&p)
		properties = append(properties, p)
	}

	return properties, nil
}

func (h *Handler) getPropertyByID(ctx context.Context, propertyID, userID string) (*PortfolioProperty, error) {
	dp, err := h.store.Q().GetPortfolioProperty(ctx, queries.GetPortfolioPropertyParams{
		ID:     propertyID,
		UserID: userID,
	})
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, fmt.Errorf("property not found")
		}
		return nil, err
	}

	// Note: GetPortfolioProperty doesn't filter by status != 'deleted',
	// so check it here for parity with the old query behavior.
	if dp.Status == "deleted" {
		return nil, fmt.Errorf("property not found")
	}

	p := mapDBPropertyToPortfolioProperty(dp)

	// Parse expenses
	if len(dp.Expenses) > 0 {
		var exp PropertyExpenses
		if err := json.Unmarshal(dp.Expenses, &exp); err == nil {
			p.Expenses = &exp
			p.MonthlyExpenses = exp.Maintenance + exp.Tax + exp.Insurance + exp.HOA + exp.Other
		}
	}

	// Calculate derived fields
	h.calculatePropertyMetrics(&p)
	return &p, nil
}

func (h *Handler) createProperty(ctx context.Context, userID string, req *CreatePropertyRequest) (*PortfolioProperty, error) {
	id := uuid.New().String()
	now := time.Now()

	// Set defaults
	currentValue := req.PurchasePrice
	if req.CurrentValue != nil {
		currentValue = *req.CurrentValue
	}

	var monthlyRent, mortgageBalance, mortgageRate, mortgagePayment float64
	if req.MonthlyRent != nil {
		monthlyRent = *req.MonthlyRent
	}
	if req.MortgageBalance != nil {
		mortgageBalance = *req.MortgageBalance
	}
	if req.MortgageRate != nil {
		mortgageRate = *req.MortgageRate
	}
	if req.MortgagePayment != nil {
		mortgagePayment = *req.MortgagePayment
	}

	var purchaseDate *time.Time
	if req.PurchaseDate != nil {
		if parsed, err := time.Parse("2006-01-02", *req.PurchaseDate); err == nil {
			purchaseDate = &parsed
		}
	}

	// Default property status to "rented" if not provided
	propertyStatus := "rented"
	if req.PropertyStatus != nil {
		propertyStatus = *req.PropertyStatus
	}

	// Calculate expenses using the expense calculator if not provided
	var vacancyRate *float64
	var expensesJSON []byte
	var propertyExpenses *PropertyExpenses

	if req.VacancyRate != nil {
		vacancyRate = req.VacancyRate
	}

	if req.Expenses != nil {
		// Use provided expenses
		propertyExpenses = req.Expenses
	} else if h.expenseCalc != nil {
		// Calculate expenses using the calculator
		yearBuilt := 0
		if req.YearBuilt != nil {
			yearBuilt = *req.YearBuilt
		}

		rentEstimate := int(monthlyRent)
		if rentEstimate == 0 {
			rentEstimate = 1500 // Default estimate for calculation
		}

		input := expenses.PropertyInput{
			Price:         int(req.PurchasePrice),
			State:         req.State,
			City:          req.City,
			YearBuilt:     yearBuilt,
			EstimatedRent: rentEstimate,
		}

		if vacancyRate != nil {
			input.VacancyRateOverride = vacancyRate
		}

		if calculated, err := h.expenseCalc.Calculate(input); err == nil {
			// Convert annual amounts to monthly
			propertyExpenses = &PropertyExpenses{
				Maintenance: calculated.Maintenance / 12,
				Tax:         calculated.PropertyTax / 12,
				Insurance:   calculated.Insurance / 12,
				HOA:         0, // HOA is usually 0 unless specified
				Other:       0,
			}

			// Set vacancy rate from calculation if not provided
			if vacancyRate == nil {
				vr := calculated.VacancyRate
				vacancyRate = &vr
			}

			h.logger.Info("calculated expenses for new property",
				"address", req.Address,
				"state", req.State,
				"insurance", propertyExpenses.Insurance,
				"tax", propertyExpenses.Tax,
				"maintenance", propertyExpenses.Maintenance,
				"vacancyRate", calculated.VacancyRate,
			)
		}
	}

	// Serialize expenses to JSON
	if propertyExpenses != nil {
		var err error
		expensesJSON, err = json.Marshal(propertyExpenses)
		if err != nil {
			h.logger.Warn("failed to serialize expenses", "error", err)
		}
	}

	// Use sqlc-generated CreatePortfolioProperty query
	dbProperty, err := h.store.Q().CreatePortfolioProperty(ctx, queries.CreatePortfolioPropertyParams{
		ID:               id,
		UserID:           userID,
		Address:          req.Address,
		City:             req.City,
		State:            req.State,
		ZipCode:          req.ZipCode,
		PropertyType:     pgtype.Text{String: derefString(req.PropertyType), Valid: req.PropertyType != nil && *req.PropertyType != ""},
		Bedrooms:         pgtype.Int4{Int32: int32(derefInt(req.Beds)), Valid: req.Beds != nil && *req.Beds != 0},
		Bathrooms:        pgtype.Float8{Float64: derefFloat64(req.Baths), Valid: req.Baths != nil && *req.Baths != 0},
		Sqft:             pgtype.Int4{Int32: int32(derefInt(req.Sqft)), Valid: req.Sqft != nil && *req.Sqft != 0},
		YearBuilt:        pgtype.Int4{Int32: int32(derefInt(req.YearBuilt)), Valid: req.YearBuilt != nil && *req.YearBuilt != 0},
		PurchasePrice:    req.PurchasePrice,
		PurchaseDate:     pgtype.Timestamp{Time: derefTime(purchaseDate), Valid: purchaseDate != nil},
		CurrentValue:     pgtype.Float8{Float64: currentValue, Valid: true},
		LastValuedAt:     pgtype.Timestamp{Time: now, Valid: true},
		MonthlyRent:      pgtype.Float8{Float64: monthlyRent, Valid: monthlyRent != 0},
		VacancyRate:      pgtype.Float8{Float64: derefFloat64(vacancyRate), Valid: vacancyRate != nil},
		Expenses:         expensesJSON,
		MortgageBalance:  pgtype.Float8{Float64: mortgageBalance, Valid: mortgageBalance != 0},
		MortgageRate:     pgtype.Float8{Float64: mortgageRate, Valid: mortgageRate != 0},
		MortgagePayment:  pgtype.Float8{Float64: mortgagePayment, Valid: mortgagePayment != 0},
		LoanTermYears:    pgtype.Int4{Valid: false}, // Not provided in request
		Lat:              pgtype.Float8{Valid: false},
		Lng:              pgtype.Float8{Valid: false},
		AcquisitionType:  "purchase",
		ExpenseFrequency: "monthly",
		RevenueFrequency: "monthly",
		Status:           "active",
		PropertyStatus:   propertyStatus,
		Notes:            pgtype.Text{String: derefString(req.Notes), Valid: req.Notes != nil && *req.Notes != ""},
	})
	if err != nil {
		return nil, err
	}

	// Convert DB property to handler property type
	zipCode := dbProperty.ZipCode
	p := PortfolioProperty{
		ID:              dbProperty.ID,
		UserID:          dbProperty.UserID,
		Address:         dbProperty.Address,
		City:            dbProperty.City,
		State:           dbProperty.State,
		ZipCode:         &zipCode,
		PropertyType:    pgtextToStringPtr(dbProperty.PropertyType),
		PropertyStatus:  dbProperty.PropertyStatus,
		Beds:            pgint4ToIntPtr(dbProperty.Bedrooms),
		Baths:           pgfloat8ToFloat64Ptr(dbProperty.Bathrooms),
		Sqft:            pgint4ToIntPtr(dbProperty.Sqft),
		YearBuilt:       pgint4ToIntPtr(dbProperty.YearBuilt),
		PurchasePrice:   dbProperty.PurchasePrice,
		PurchaseDate:    pgtimestampToTimePtr(dbProperty.PurchaseDate),
		CurrentValue:    dbProperty.CurrentValue.Float64,
		MonthlyRent:     dbProperty.MonthlyRent.Float64,
		MortgageBalance: dbProperty.MortgageBalance.Float64,
		MortgageRate:    dbProperty.MortgageRate.Float64,
		MortgagePayment: dbProperty.MortgagePayment.Float64,
		Status:          dbProperty.Status,
		Notes:           pgtextToStringPtr(dbProperty.Notes),
		CreatedAt:       dbProperty.CreatedAt.Time,
		UpdatedAt:       dbProperty.UpdatedAt.Time,
	}

	// Handle nullable fields
	if dbProperty.VacancyRate.Valid {
		p.VacancyRate = &dbProperty.VacancyRate.Float64
	}
	if len(dbProperty.Expenses) > 0 {
		var exp PropertyExpenses
		if err := json.Unmarshal(dbProperty.Expenses, &exp); err == nil {
			p.Expenses = &exp
			// Calculate total monthly expenses
			p.MonthlyExpenses = exp.Maintenance + exp.Tax + exp.Insurance + exp.HOA + exp.Other
		}
	}

	// Calculate derived fields
	h.calculatePropertyMetrics(&p)
	return &p, nil
}

func (h *Handler) updateProperty(ctx context.Context, propertyID, userID string, req *UpdatePropertyRequest) (*PortfolioProperty, error) {
	// Build update params using sqlc query
	// Parse PurchaseDate if provided
	var purchaseDate *time.Time

	if req.PurchaseDate != nil {
		h.logger.Info("updating purchase_date", "value", *req.PurchaseDate)
		if parsed, err := time.Parse("2006-01-02", *req.PurchaseDate); err == nil {
			purchaseDate = &parsed
		} else {
			h.logger.Error("failed to parse purchase_date", "value", *req.PurchaseDate, "error", err)
		}
	}

	// Serialize expenses if provided
	var expensesJSON []byte
	if req.Expenses != nil {
		var err error
		expensesJSON, err = json.Marshal(req.Expenses)
		if err != nil {
			h.logger.Warn("failed to serialize expenses", "error", err)
		}
	}

	// Use sqlc-generated UpdatePortfolioProperty query
	dbProperty, err := h.store.Q().UpdatePortfolioProperty(ctx, queries.UpdatePortfolioPropertyParams{
		ID:               propertyID,
		UserID:           userID,
		Address:          pgtype.Text{String: derefString(req.Address), Valid: req.Address != nil},
		City:             pgtype.Text{String: derefString(req.City), Valid: req.City != nil},
		State:            pgtype.Text{String: derefString(req.State), Valid: req.State != nil},
		ZipCode:          pgtype.Text{String: derefString(req.ZipCode), Valid: req.ZipCode != nil},
		PropertyType:     pgtype.Text{String: derefString(req.PropertyType), Valid: req.PropertyType != nil},
		Bedrooms:         pgtype.Int4{Int32: int32(derefInt(req.Beds)), Valid: req.Beds != nil},
		Bathrooms:        pgtype.Float8{Float64: derefFloat64(req.Baths), Valid: req.Baths != nil},
		Sqft:             pgtype.Int4{Int32: int32(derefInt(req.Sqft)), Valid: req.Sqft != nil},
		YearBuilt:        pgtype.Int4{Int32: int32(derefInt(req.YearBuilt)), Valid: req.YearBuilt != nil},
		PurchasePrice:    pgtype.Float8{Float64: derefFloat64(req.PurchasePrice), Valid: req.PurchasePrice != nil},
		PurchaseDate:     pgtype.Timestamp{Time: derefTime(purchaseDate), Valid: purchaseDate != nil},
		CurrentValue:     pgtype.Float8{Float64: derefFloat64(req.CurrentValue), Valid: req.CurrentValue != nil},
		LastValuedAt:     pgtype.Timestamp{Valid: false}, // Not updated via this endpoint
		MonthlyRent:      pgtype.Float8{Float64: derefFloat64(req.MonthlyRent), Valid: req.MonthlyRent != nil},
		VacancyRate:      pgtype.Float8{Float64: derefFloat64(req.VacancyRate), Valid: req.VacancyRate != nil},
		Expenses:         expensesJSON,
		MortgageBalance:  pgtype.Float8{Float64: derefFloat64(req.MortgageBalance), Valid: req.MortgageBalance != nil},
		MortgageRate:     pgtype.Float8{Float64: derefFloat64(req.MortgageRate), Valid: req.MortgageRate != nil},
		MortgagePayment:  pgtype.Float8{Float64: derefFloat64(req.MortgagePayment), Valid: req.MortgagePayment != nil},
		LoanTermYears:    pgtype.Int4{Valid: false},        // Not provided in request
		Lat:              pgtype.Float8{Valid: false},      // Not updated via this endpoint
		Lng:              pgtype.Float8{Valid: false},      // Not updated via this endpoint
		AcquisitionType:  pgtype.Text{Valid: false},        // Not updated via this endpoint
		ExpenseFrequency: pgtype.Text{Valid: false},        // Not updated via this endpoint
		RevenueFrequency: pgtype.Text{Valid: false},        // Not updated via this endpoint
		SaleDate:         pgtype.Timestamp{Valid: false},   // Not updated via this endpoint
		SalePrice:        pgtype.Float8{Valid: false},      // Not updated via this endpoint
		PropertyStatus:   pgtype.Text{String: derefString(req.PropertyStatus), Valid: req.PropertyStatus != nil},
		Notes:            pgtype.Text{String: derefString(req.Notes), Valid: req.Notes != nil},
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("property not found")
		}
		return nil, err
	}

	// Convert DB property to handler property type
	zipCode := dbProperty.ZipCode
	p := PortfolioProperty{
		ID:              dbProperty.ID,
		UserID:          dbProperty.UserID,
		Address:         dbProperty.Address,
		City:            dbProperty.City,
		State:           dbProperty.State,
		ZipCode:         &zipCode,
		PropertyType:    pgtextToStringPtr(dbProperty.PropertyType),
		PropertyStatus:  dbProperty.PropertyStatus,
		Beds:            pgint4ToIntPtr(dbProperty.Bedrooms),
		Baths:           pgfloat8ToFloat64Ptr(dbProperty.Bathrooms),
		Sqft:            pgint4ToIntPtr(dbProperty.Sqft),
		YearBuilt:       pgint4ToIntPtr(dbProperty.YearBuilt),
		PurchasePrice:   dbProperty.PurchasePrice,
		PurchaseDate:    pgtimestampToTimePtr(dbProperty.PurchaseDate),
		CurrentValue:    dbProperty.CurrentValue.Float64,
		MonthlyRent:     dbProperty.MonthlyRent.Float64,
		MortgageBalance: dbProperty.MortgageBalance.Float64,
		MortgageRate:    dbProperty.MortgageRate.Float64,
		MortgagePayment: dbProperty.MortgagePayment.Float64,
		Status:          dbProperty.Status,
		Notes:           pgtextToStringPtr(dbProperty.Notes),
		CreatedAt:       dbProperty.CreatedAt.Time,
		UpdatedAt:       dbProperty.UpdatedAt.Time,
	}

	// Handle nullable fields
	if dbProperty.VacancyRate.Valid {
		p.VacancyRate = &dbProperty.VacancyRate.Float64
	}
	if len(dbProperty.Expenses) > 0 {
		var exp PropertyExpenses
		if err := json.Unmarshal(dbProperty.Expenses, &exp); err == nil {
			p.Expenses = &exp
			// Calculate total monthly expenses
			p.MonthlyExpenses = exp.Maintenance + exp.Tax + exp.Insurance + exp.HOA + exp.Other
		}
	}

	// Calculate derived fields
	h.calculatePropertyMetrics(&p)
	return &p, nil
}

func (h *Handler) deleteProperty(ctx context.Context, propertyID, userID string) error {
	// First verify the property exists and belongs to this user
	_, err := h.store.Q().GetPortfolioProperty(ctx, queries.GetPortfolioPropertyParams{
		ID:     propertyID,
		UserID: userID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("property not found")
		}
		return err
	}

	// SoftDeletePortfolioProperty sets status = 'deleted' and updated_at = NOW()
	return h.store.Q().SoftDeletePortfolioProperty(ctx, queries.SoftDeletePortfolioPropertyParams{
		ID:     propertyID,
		UserID: userID,
	})
}

// Helper functions

func (h *Handler) calculatePropertyMetrics(p *PortfolioProperty) {
	// Equity = Current Value - Mortgage Balance
	p.Equity = p.CurrentValue - p.MortgageBalance

	// Monthly Cash Flow = Rent - Mortgage - Expenses
	p.MonthlyCashFlow = p.MonthlyRent - p.MortgagePayment - p.MonthlyExpenses

	// Cap Rate = (Annual NOI / Current Value) * 100
	if p.CurrentValue > 0 {
		annualNOI := (p.MonthlyRent - p.MonthlyExpenses) * 12
		p.CapRate = (annualNOI / p.CurrentValue) * 100
	}

	// Cash on Cash = (Annual Cash Flow / Cash Invested) * 100
	cashInvested := p.PurchasePrice - p.MortgageBalance
	if cashInvested > 0 {
		annualCashFlow := p.MonthlyCashFlow * 12
		p.CashOnCash = (annualCashFlow / cashInvested) * 100
	}
}

func (h *Handler) calculateSummary(properties []PortfolioProperty) PortfolioSummary {
	summary := PortfolioSummary{}

	if len(properties) == 0 {
		return summary
	}

	summary.TotalProperties = len(properties)
	var totalCapRate float64

	for _, p := range properties {
		summary.TotalValue += p.CurrentValue
		summary.TotalEquity += p.Equity
		summary.TotalDebt += p.MortgageBalance
		summary.MonthlyIncome += p.MonthlyRent
		summary.MonthlyExpenses += p.MonthlyExpenses + p.MortgagePayment
		summary.MonthlyCashFlow += p.MonthlyCashFlow
		totalCapRate += p.CapRate
	}

	if summary.TotalProperties > 0 {
		summary.AverageCapRate = totalCapRate / float64(summary.TotalProperties)
	}

	return summary
}

// join is a simple helper to join strings

// ProjectionsResponse wraps the projections response
type ProjectionsResponse struct {
	Success          bool                         `json:"success"`
	Metrics          *investment.PortfolioMetrics `json:"metrics"`
	GrowthProjection *investment.GrowthProjection `json:"growthProjection"`
	Summary          PortfolioSummary             `json:"summary"`
}

// GetProjections returns portfolio growth projections
// GET /api/v2/portfolio/projections?years=10&appreciationRate=0.03&rentGrowthRate=0.02
func (h *Handler) GetProjections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	// Parse query parameters
	years := 10 // default
	if yearsParam := r.URL.Query().Get("years"); yearsParam != "" {
		if y, err := strconv.Atoi(yearsParam); err == nil && y > 0 && y <= 30 {
			years = y
		}
	}

	// Optional: custom projection config from query params
	config := projection.DefaultProjectionConfig()
	if rate := r.URL.Query().Get("appreciationRate"); rate != "" {
		if r, err := strconv.ParseFloat(rate, 64); err == nil && r >= 0 && r <= 0.2 {
			config.AppreciationRate = r
		}
	}
	if rate := r.URL.Query().Get("rentGrowthRate"); rate != "" {
		if r, err := strconv.ParseFloat(rate, 64); err == nil && r >= 0 && r <= 0.1 {
			config.RentGrowthRate = r
		}
	}
	if ratio := r.URL.Query().Get("expenseRatio"); ratio != "" {
		if r, err := strconv.ParseFloat(ratio, 64); err == nil && r >= 0 && r <= 0.6 {
			config.ExpenseRatio = r
		}
	}

	h.logger.Info("calculating portfolio projections",
		"userId", user.UserID,
		"years", years,
	)

	// Get portfolio properties
	properties, err := h.getPropertiesByUserID(ctx, user.UserID)
	if err != nil {
		h.logger.Error("failed to get properties", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to retrieve portfolio")
		return
	}

	if len(properties) == 0 {
		// Return empty projections for empty portfolio
		httputil.JSON(w, http.StatusOK, ProjectionsResponse{
			Success:          true,
			Metrics:          &investment.PortfolioMetrics{},
			GrowthProjection: &investment.GrowthProjection{Years: years, YearlyData: []investment.YearlyProjection{}},
			Summary:          PortfolioSummary{},
		})
		return
	}

	// Convert portfolio properties to investment properties
	investmentProperties := h.convertToInvestmentProperties(properties, config)

	// Calculate projections
	calculator := projection.NewCalculator(&config)
	metrics := calculator.CalculateMetrics(investmentProperties)
	growthProjection := calculator.CalculateGrowth(investmentProperties, years)

	// Also include summary
	summary := h.calculateSummary(properties)

	response := ProjectionsResponse{
		Success:          true,
		Metrics:          metrics,
		GrowthProjection: growthProjection,
		Summary:          summary,
	}

	httputil.JSON(w, http.StatusOK, response)
}

// convertToInvestmentProperties converts PortfolioProperty slice to investment.PropertyInPortfolio slice
func (h *Handler) convertToInvestmentProperties(properties []PortfolioProperty, config projection.DefaultConfig) []investment.PropertyInPortfolio {
	result := make([]investment.PropertyInPortfolio, len(properties))

	for i, p := range properties {
		// Convert to int values for the investment package
		price := int(p.CurrentValue) // Use current value as price for projections
		monthlyRent := int(p.MonthlyRent)
		mortgageBalance := int(p.MortgageBalance)
		monthlyPayment := int(p.MortgagePayment)
		downPayment := int(p.Equity) // Equity represents the down payment equivalent

		beds := 0
		if p.Beds != nil {
			beds = *p.Beds
		}
		baths := 0.0
		if p.Baths != nil {
			baths = *p.Baths
		}
		sqft := 0
		if p.Sqft != nil {
			sqft = *p.Sqft
		}
		yearBuilt := 0
		if p.YearBuilt != nil {
			yearBuilt = *p.YearBuilt
		}
		zipCode := ""
		if p.ZipCode != nil {
			zipCode = *p.ZipCode
		}
		propertyType := ""
		if p.PropertyType != nil {
			propertyType = *p.PropertyType
		}

		// Calculate NOI for DSCR
		annualRent := float64(monthlyRent * 12)
		effectiveRent := annualRent * (1 - config.VacancyRate)
		noi := effectiveRent * (1 - config.ExpenseRatio)

		// Calculate DSCR
		dscr := 0.0
		annualDebtService := float64(monthlyPayment * 12)
		if annualDebtService > 0 {
			dscr = noi / annualDebtService
		}

		result[i] = investment.PropertyInPortfolio{
			Property: investment.Property{
				ID:            p.ID,
				Address:       p.Address,
				City:          p.City,
				State:         p.State,
				ZipCode:       zipCode,
				Price:         price,
				Beds:          beds,
				Baths:         baths,
				Sqft:          sqft,
				YearBuilt:     yearBuilt,
				PropertyType:  propertyType,
				EstimatedRent: monthlyRent,
			},
			DownPayment:     downPayment,
			LoanAmount:      mortgageBalance,
			MonthlyPayment:  monthlyPayment,
			MonthlyCashFlow: int(p.MonthlyCashFlow),
			CapRate:         p.CapRate,
			CashOnCash:      p.CashOnCash,
			DSCR:            dscr,
		}
	}

	return result
}

// =============================================================================
// Address Lookup Endpoint
// =============================================================================

// LookupRequest represents a request to lookup property by address
type LookupRequest struct {
	Address string `json:"address" validate:"required,min=5"`
}

// LookupResponse represents the response from address lookup
type LookupResponse struct {
	Success            bool                    `json:"success"`
	Property           *LookupPropertyResult   `json:"property,omitempty"`
	Estimates          *LookupEstimates        `json:"estimates,omitempty"`
	LastSale           *LookupLastSale         `json:"lastSale,omitempty"`
	Listing            *LookupListing          `json:"listing,omitempty"`
	Meta               *LookupMeta             `json:"meta,omitempty"`
	Error              string                  `json:"error,omitempty"`
	Suggestions        []string                `json:"suggestions,omitempty"`
	AttemptedProviders []string                `json:"attemptedProviders,omitempty"`
}

// LookupPropertyResult contains property details from lookup
type LookupPropertyResult struct {
	Address           string   `json:"address"`
	City              string   `json:"city"`
	State             string   `json:"state"`
	ZipCode           string   `json:"zipCode"`
	Lat               *float64 `json:"lat,omitempty"`
	Lng               *float64 `json:"lng,omitempty"`
	NormalizedAddress string   `json:"normalizedAddress"`
	Bedrooms          *int     `json:"bedrooms,omitempty"`
	Bathrooms         *float64 `json:"bathrooms,omitempty"`
	SquareFeet        *int     `json:"squareFeet,omitempty"`
	YearBuilt         *int     `json:"yearBuilt,omitempty"`
	PropertyType      *string  `json:"propertyType,omitempty"`
	LotSize           *int     `json:"lotSize,omitempty"`
}

// LookupEstimates contains value/rent estimates
type LookupEstimates struct {
	EstimatedValue   *int    `json:"estimatedValue,omitempty"`
	ValueSource      *string `json:"valueSource,omitempty"`
	EstimatedRent    *int    `json:"estimatedRent,omitempty"`
	RentSource       *string `json:"rentSource,omitempty"`
	EstimatedTax     *int    `json:"estimatedTax,omitempty"`
	TaxAssessedValue *int    `json:"taxAssessedValue,omitempty"`
	HoaFee           *int    `json:"hoaFee,omitempty"`
	DataSource       string  `json:"dataSource"`
}

// LookupLastSale contains last sale information
type LookupLastSale struct {
	Date  string `json:"date"`
	Price *int   `json:"price,omitempty"`
}

// LookupListing contains current listing information
type LookupListing struct {
	IsListed     bool    `json:"isListed"`
	ListingPrice *int    `json:"listingPrice,omitempty"`
	DaysOnMarket *int    `json:"daysOnMarket,omitempty"`
	ListingURL   *string `json:"listingUrl,omitempty"`
}

// LookupMeta contains metadata about the lookup
type LookupMeta struct {
	Provider      string  `json:"provider"`
	DataFreshness *string `json:"dataFreshness,omitempty"`
}

// Lookup handles POST /api/v2/portfolio/lookup
// Looks up property details by address for portfolio entry
func (h *Handler) Lookup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	// Check if property finder is available
	if h.propertyFinder == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "Property lookup service not available")
		return
	}

	// Parse request
	var req LookupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate request
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	h.logger.Info("looking up address",
		"userId", user.UserID,
		"address", req.Address,
	)

	// Parse address components
	streetAddress, city, state, _ := parseAddressComponents(req.Address)

	if city == "" || state == "" {
		httputil.JSON(w, http.StatusOK, LookupResponse{
			Success:     false,
			Error:       "Please include city and state in the address (e.g., \"123 Main St, Austin, TX\")",
			Suggestions: []string{},
		})
		return
	}

	// Use PropertyFinder to get property data
	result, err := h.propertyFinder.GetPropertyByAddress(ctx, streetAddress, city, state)
	if err != nil {
		h.logger.Error("failed to lookup property", "error", err)
		httputil.JSON(w, http.StatusOK, LookupResponse{
			Success:            false,
			Error:              "Failed to lookup property",
			AttemptedProviders: []string{"hasdata"},
		})
		return
	}

	if !result.Success || result.Property == nil {
		httputil.JSON(w, http.StatusOK, LookupResponse{
			Success:            false,
			Error:              result.Error,
			AttemptedProviders: result.AttemptedProviders,
		})
		return
	}

	prop := result.Property

	// Build response
	response := LookupResponse{
		Success: true,
		Property: &LookupPropertyResult{
			Address:           prop.Address,
			City:              prop.City,
			State:             prop.State,
			ZipCode:           prop.ZipCode,
			NormalizedAddress: fmt.Sprintf("%s, %s, %s %s", prop.Address, prop.City, prop.State, prop.ZipCode),
		},
		Estimates: &LookupEstimates{
			DataSource: "zillow",
		},
		Meta: &LookupMeta{
			Provider: result.ProviderName,
		},
		AttemptedProviders: result.AttemptedProviders,
	}

	// Add coordinates
	if prop.Latitude != 0 {
		response.Property.Lat = &prop.Latitude
	}
	if prop.Longitude != 0 {
		response.Property.Lng = &prop.Longitude
	}

	// Add property details
	if prop.Beds > 0 {
		response.Property.Bedrooms = &prop.Beds
	}
	if prop.Baths > 0 {
		response.Property.Bathrooms = &prop.Baths
	}
	if prop.Sqft > 0 {
		response.Property.SquareFeet = &prop.Sqft
	}
	if prop.YearBuilt > 0 {
		response.Property.YearBuilt = &prop.YearBuilt
	}
	if prop.PropertyType != "" {
		pt := string(prop.PropertyType)
		response.Property.PropertyType = &pt
	}
	if prop.LotSize > 0 {
		response.Property.LotSize = &prop.LotSize
	}

	// Add estimates
	if prop.Price > 0 {
		response.Estimates.EstimatedValue = &prop.Price
		source := "zestimate"
		response.Estimates.ValueSource = &source
	}
	if prop.EstimatedRent > 0 {
		response.Estimates.EstimatedRent = &prop.EstimatedRent
		source := "rentZestimate"
		response.Estimates.RentSource = &source
	}

	// Add listing info if available
	if prop.ListingURL != "" {
		response.Listing = &LookupListing{
			IsListed:   prop.Status == "active",
			ListingURL: &prop.ListingURL,
		}
		if prop.Price > 0 && prop.Status == "active" {
			response.Listing.ListingPrice = &prop.Price
		}
		if prop.DaysOnMarket > 0 {
			response.Listing.DaysOnMarket = &prop.DaysOnMarket
		}
	}

	httputil.JSON(w, http.StatusOK, response)
}

// parseAddressComponents parses address string into components
func parseAddressComponents(address string) (streetAddress, city, state, zipCode string) {
	parts := strings.Split(address, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	if len(parts) >= 3 {
		streetAddress = parts[0]
		city = parts[1]

		// Parse state and zip from last part
		lastPart := parts[len(parts)-1]
		stateZipParts := strings.Fields(lastPart)
		if len(stateZipParts) >= 1 {
			state = strings.ToUpper(stateZipParts[0])
		}
		if len(stateZipParts) >= 2 {
			zipCode = stateZipParts[1]
		}
	} else if len(parts) == 2 {
		streetAddress = parts[0]
		// Try to parse "City State ZIP" format
		cityStateZip := parts[1]
		fields := strings.Fields(cityStateZip)
		if len(fields) >= 2 {
			// Last field might be state or zip
			lastField := fields[len(fields)-1]
			if len(lastField) == 2 && strings.ToUpper(lastField) == lastField {
				// It's a state
				state = lastField
				city = strings.Join(fields[:len(fields)-1], " ")
			} else if len(lastField) == 5 || len(lastField) == 10 {
				// It's a zip
				zipCode = lastField
				if len(fields) >= 3 {
					state = strings.ToUpper(fields[len(fields)-2])
					city = strings.Join(fields[:len(fields)-2], " ")
				}
			}
		}
	}

	return
}

// =============================================================================
// Snapshots Endpoint
// =============================================================================

// PortfolioSnapshot represents a historical portfolio snapshot
type PortfolioSnapshot struct {
	ID               string                 `json:"id"`
	SnapshotDate     time.Time              `json:"snapshotDate"`
	TotalValue       float64                `json:"totalValue"`
	TotalEquity      float64                `json:"totalEquity"`
	TotalDebt        float64                `json:"totalDebt"`
	MonthlyCashFlow  float64                `json:"monthlyCashFlow"`
	PortfolioCapRate float64                `json:"portfolioCapRate"`
	PropertyCount    int                    `json:"propertyCount"`
	MetricsJSON      map[string]interface{} `json:"metricsJson,omitempty"`
}

// SnapshotTrends represents calculated trends from snapshots
type SnapshotTrends struct {
	ValueChange        float64 `json:"valueChange"`
	ValueChangePercent float64 `json:"valueChangePercent"`
	EquityChange       float64 `json:"equityChange"`
	EquityChangePercent float64 `json:"equityChangePercent"`
	CashFlowChange     float64 `json:"cashFlowChange"`
	CashFlowChangePercent float64 `json:"cashFlowChangePercent"`
	Period             string  `json:"period"`
}

// SnapshotsResponse represents the snapshots API response
type SnapshotsResponse struct {
	Success        bool                `json:"success"`
	Snapshots      []PortfolioSnapshot `json:"snapshots"`
	LatestSnapshot *PortfolioSnapshot  `json:"latestSnapshot"`
	Trends         *SnapshotTrends     `json:"trends"`
	Count          int                 `json:"count"`
}

// GetSnapshots handles GET /api/v2/portfolio/snapshots
// Returns historical portfolio snapshots with trend calculations
func (h *Handler) GetSnapshots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	// Check if queries are available
	if h.store == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "Snapshot service not available")
		return
	}

	// Parse query parameters
	startDateStr := r.URL.Query().Get("startDate")
	endDateStr := r.URL.Query().Get("endDate")
	limitStr := r.URL.Query().Get("limit")
	includeTrends := r.URL.Query().Get("includeTrends") != "false"
	regenerate := r.URL.Query().Get("regenerate") == "true"

	var startDate, endDate *time.Time
	if startDateStr != "" {
		if t, err := time.Parse(time.RFC3339, startDateStr); err == nil {
			startDate = &t
		}
	}
	if endDateStr != "" {
		if t, err := time.Parse(time.RFC3339, endDateStr); err == nil {
			endDate = &t
		}
	}

	limit := int32(365)
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = int32(l)
		}
	}

	h.logger.Info("getting portfolio snapshots",
		"userId", user.UserID,
		"startDate", startDate,
		"endDate", endDate,
		"limit", limit,
		"regenerate", regenerate,
	)

	// If regenerate requested, backfill snapshots first
	if regenerate {
		if err := h.backfillSnapshots(ctx, user.UserID); err != nil {
			h.logger.Error("failed to backfill snapshots", "error", err)
			// Continue even if backfill fails
		}
	}

	// Get snapshots from database
	dbSnapshots, err := h.store.Q().GetPortfolioSnapshots(ctx, queries.GetPortfolioSnapshotsParams{
		UserID:  user.UserID,
		Column2: timeToPgTimestamp(startDate),
		Column3: timeToPgTimestamp(endDate),
		Limit:   limit,
	})
	if err != nil {
		h.logger.Error("failed to get snapshots", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to retrieve snapshots")
		return
	}

	// Convert to response format
	snapshots := make([]PortfolioSnapshot, len(dbSnapshots))
	for i, s := range dbSnapshots {
		snapshots[i] = PortfolioSnapshot{
			ID:               s.ID,
			SnapshotDate:     s.SnapshotDate.Time,
			TotalValue:       s.TotalValue,
			TotalEquity:      s.TotalEquity,
			TotalDebt:        s.TotalDebt,
			MonthlyCashFlow:  s.MonthlyCashFlow,
			PortfolioCapRate: s.PortfolioCapRate,
			PropertyCount:    int(s.PropertyCount),
		}
		if s.MetricsJson != nil {
			var metrics map[string]interface{}
			if err := json.Unmarshal(s.MetricsJson, &metrics); err == nil {
				snapshots[i].MetricsJSON = metrics
			}
		}
	}

	// Get latest snapshot
	var latestSnapshot *PortfolioSnapshot
	latestDB, err := h.store.Q().GetLatestPortfolioSnapshot(ctx, user.UserID)
	if err == nil {
		latestSnapshot = &PortfolioSnapshot{
			ID:               latestDB.ID,
			SnapshotDate:     latestDB.SnapshotDate.Time,
			TotalValue:       latestDB.TotalValue,
			TotalEquity:      latestDB.TotalEquity,
			TotalDebt:        latestDB.TotalDebt,
			MonthlyCashFlow:  latestDB.MonthlyCashFlow,
			PortfolioCapRate: latestDB.PortfolioCapRate,
			PropertyCount:    int(latestDB.PropertyCount),
		}
		if latestDB.MetricsJson != nil {
			var metrics map[string]interface{}
			if err := json.Unmarshal(latestDB.MetricsJson, &metrics); err == nil {
				latestSnapshot.MetricsJSON = metrics
			}
		}
	}

	// Calculate trends if requested and we have enough data
	var trends *SnapshotTrends
	if includeTrends && len(snapshots) >= 2 {
		trends = h.calculateTrends(snapshots)
	}

	response := SnapshotsResponse{
		Success:        true,
		Snapshots:      snapshots,
		LatestSnapshot: latestSnapshot,
		Trends:         trends,
		Count:          len(snapshots),
	}

	httputil.JSON(w, http.StatusOK, response)
}

// backfillSnapshots generates historical snapshots from portfolio data
// It creates monthly snapshots showing portfolio evolution over time by:
// 1. Filtering properties owned as of each snapshot date
// 2. Interpolating property values from purchase price to current value
// 3. Interpolating debt from original loan (80% LTV) to current balance
func (h *Handler) backfillSnapshots(ctx context.Context, userID string) error {
	// Delete existing snapshots if regenerating
	if err := h.store.Q().DeletePortfolioSnapshots(ctx, userID); err != nil {
		return fmt.Errorf("failed to delete existing snapshots: %w", err)
	}

	// Get current portfolio properties
	properties, err := h.getPropertiesByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get properties: %w", err)
	}

	if len(properties) == 0 {
		return nil // No properties to create snapshots for
	}

	// Find earliest purchase date
	var earliestDate time.Time
	for _, p := range properties {
		if p.PurchaseDate != nil && (earliestDate.IsZero() || p.PurchaseDate.Before(earliestDate)) {
			earliestDate = *p.PurchaseDate
		}
	}

	if earliestDate.IsZero() {
		earliestDate = time.Now().AddDate(-1, 0, 0) // Default to 1 year ago
	}

	// Generate monthly snapshots from earliest date to now
	currentDate := time.Date(earliestDate.Year(), earliestDate.Month(), 1, 0, 0, 0, 0, time.UTC)
	now := time.Now()

	h.logger.Info("backfilling portfolio snapshots",
		"userId", userID,
		"propertyCount", len(properties),
		"earliestDate", earliestDate,
	)

	for currentDate.Before(now) {
		// Calculate portfolio metrics as of this historical date
		metrics := h.calculateHistoricalMetrics(properties, currentDate, now)

		if metrics.propertyCount > 0 {
			// Create snapshot
			metricsJSON, _ := json.Marshal(map[string]interface{}{
				"properties":      metrics.propertyCount,
				"cashOnCashReturn": metrics.cashOnCashReturn,
				"ltv":             metrics.ltv,
				"backfilled":      true,
			})

			_, err := h.store.Q().CreatePortfolioSnapshot(ctx, queries.CreatePortfolioSnapshotParams{
				ID:               uuid.New().String(),
				UserID:           userID,
				SnapshotDate:     pgTimestamp(currentDate),
				TotalValue:       metrics.totalValue,
				TotalEquity:      metrics.totalEquity,
				TotalDebt:        metrics.totalDebt,
				MonthlyCashFlow:  metrics.monthlyCashFlow,
				PortfolioCapRate: metrics.portfolioCapRate,
				PropertyCount:    int32(metrics.propertyCount),
				MetricsJson:      metricsJSON,
			})
			if err != nil {
				h.logger.Warn("failed to create snapshot", "date", currentDate, "error", err)
			}
		}

		// Move to next month
		currentDate = currentDate.AddDate(0, 1, 0)
	}

	return nil
}

// historicalMetrics holds calculated metrics for a historical date
type historicalMetrics struct {
	totalValue       float64
	totalEquity      float64
	totalDebt        float64
	monthlyCashFlow  float64
	portfolioCapRate float64
	propertyCount    int
	cashOnCashReturn float64
	ltv              float64
}

// calculateHistoricalMetrics calculates what the portfolio looked like at a specific historical date
// Key assumptions:
// - Property value appreciates linearly from purchase price to current value
// - Original loan = purchasePrice * 0.8 (standard 20% down payment)
// - Debt decreases linearly from original loan to current balance
// - Rent and expenses use current values (simplified - no baseline change tracking)
func (h *Handler) calculateHistoricalMetrics(properties []PortfolioProperty, asOfDate, now time.Time) historicalMetrics {
	var result historicalMetrics

	// Convert to month numbers for comparison
	asOfMonth := asOfDate.Year()*12 + int(asOfDate.Month())

	var totalValue, totalDebt, monthlyRent, monthlyExpenses, monthlyMortgage, totalDownPayment float64

	for _, p := range properties {
		// Skip properties not owned yet as of this date
		if p.PurchaseDate == nil {
			continue
		}
		purchaseMonth := p.PurchaseDate.Year()*12 + int(p.PurchaseDate.Month())
		if purchaseMonth > asOfMonth {
			continue // Property not yet purchased
		}

		result.propertyCount++

		// Calculate progress ratio: 0 at purchase, 1 at now
		totalMonths := float64(monthsBetweenDates(*p.PurchaseDate, now))
		if totalMonths < 1 {
			totalMonths = 1
		}
		elapsedMonths := float64(monthsBetweenDates(*p.PurchaseDate, asOfDate))
		if elapsedMonths < 0 {
			elapsedMonths = 0
		}
		progressRatio := elapsedMonths / totalMonths
		if progressRatio > 1 {
			progressRatio = 1
		}

		// VALUE: Interpolate from purchase price to current value
		currentValue := p.CurrentValue
		if currentValue == 0 {
			currentValue = p.PurchasePrice
		}
		valueAtDate := p.PurchasePrice + (currentValue-p.PurchasePrice)*progressRatio

		// DEBT: Interpolate from original loan (80% of purchase) to current balance
		originalLoan := p.PurchasePrice * 0.8
		currentBalance := p.MortgageBalance
		if currentBalance == 0 {
			currentBalance = originalLoan
		}
		debtAtDate := originalLoan - (originalLoan-currentBalance)*progressRatio
		if debtAtDate < 0 {
			debtAtDate = 0
		}

		totalValue += valueAtDate
		totalDebt += debtAtDate

		// RENT: Use current rent (simplified)
		vacancyRate := 5.0 // Default 5%
		effectiveRent := p.MonthlyRent * (1 - vacancyRate/100)
		monthlyRent += effectiveRent

		// EXPENSES: Use current expenses
		monthlyExpenses += p.MonthlyExpenses

		// MORTGAGE: Use current payment
		monthlyMortgage += p.MortgagePayment

		// Down payment = 20% of purchase price
		totalDownPayment += p.PurchasePrice * 0.2
	}

	if result.propertyCount == 0 {
		return result
	}

	result.totalValue = totalValue
	result.totalDebt = totalDebt
	result.totalEquity = totalValue - totalDebt
	result.monthlyCashFlow = monthlyRent - monthlyExpenses - monthlyMortgage

	// NOI = (monthlyRent - monthlyExpenses) * 12
	noi := (monthlyRent - monthlyExpenses) * 12
	if totalValue > 0 {
		result.portfolioCapRate = (noi / totalValue) * 100
		result.ltv = (totalDebt / totalValue) * 100
	}

	annualCashFlow := result.monthlyCashFlow * 12
	if totalDownPayment > 0 {
		result.cashOnCashReturn = (annualCashFlow / totalDownPayment) * 100
	}

	return result
}

// monthsBetweenDates calculates months between two dates
func monthsBetweenDates(start, end time.Time) int {
	return (end.Year()-start.Year())*12 + int(end.Month()) - int(start.Month())
}

// calculateTrends calculates trends from snapshots
func (h *Handler) calculateTrends(snapshots []PortfolioSnapshot) *SnapshotTrends {
	if len(snapshots) < 2 {
		return nil
	}

	// Snapshots are ordered DESC, so first is latest, last is oldest
	latest := snapshots[0]
	oldest := snapshots[len(snapshots)-1]

	trends := &SnapshotTrends{
		ValueChange:  latest.TotalValue - oldest.TotalValue,
		EquityChange: latest.TotalEquity - oldest.TotalEquity,
		CashFlowChange: latest.MonthlyCashFlow - oldest.MonthlyCashFlow,
	}

	if oldest.TotalValue > 0 {
		trends.ValueChangePercent = (trends.ValueChange / oldest.TotalValue) * 100
	}
	if oldest.TotalEquity > 0 {
		trends.EquityChangePercent = (trends.EquityChange / oldest.TotalEquity) * 100
	}
	if oldest.MonthlyCashFlow > 0 {
		trends.CashFlowChangePercent = (trends.CashFlowChange / oldest.MonthlyCashFlow) * 100
	}

	// Calculate period
	days := int(latest.SnapshotDate.Sub(oldest.SnapshotDate).Hours() / 24)
	if days < 30 {
		trends.Period = fmt.Sprintf("%d days", days)
	} else if days < 365 {
		trends.Period = fmt.Sprintf("%d months", days/30)
	} else {
		trends.Period = fmt.Sprintf("%.1f years", float64(days)/365)
	}

	return trends
}

// timeToPgTimestamp converts *time.Time to pgtype.Timestamp
func timeToPgTimestamp(t *time.Time) pgtype.Timestamp {
	if t == nil {
		return pgtype.Timestamp{Valid: false}
	}
	return pgtype.Timestamp{Time: *t, Valid: true}
}

// pgTimestamp converts time.Time to pgtype.Timestamp for CreatePortfolioSnapshot
func pgTimestamp(t time.Time) pgtype.Timestamp {
	return pgtype.Timestamp{Time: t, Valid: true}
}

// GetMetrics returns comprehensive portfolio analytics
// GET /api/v2/portfolio/metrics
func (h *Handler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	h.logger.Info("calculating portfolio metrics", "userId", user.UserID)

	// Get all properties for the user
	properties, err := h.getPropertiesByUserID(ctx, user.UserID)
	if err != nil {
		h.logger.Error("failed to get properties", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to retrieve portfolio")
		return
	}

	// Calculate comprehensive metrics
	metrics := h.calculatePortfolioMetrics(properties)

	response := MetricsResponse{
		Success:     true,
		Metrics:     metrics,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	httputil.JSON(w, http.StatusOK, response)
}

// calculatePortfolioMetrics computes comprehensive portfolio analytics
func (h *Handler) calculatePortfolioMetrics(properties []PortfolioProperty) PortfolioMetrics {
	if len(properties) == 0 {
		return PortfolioMetrics{
			AllocationByMarket: []AllocationItem{},
			AllocationByType:   []AllocationItem{},
			AllocationByState:  []AllocationItem{},
			PropertyScores:     []PropertyScore{},
			CumulativeMetrics:  PortfolioCumulativeMetrics{OldestPurchaseDate: time.Now().UTC().Format(time.RFC3339)},
		}
	}

	var totalValue, totalDebt, monthlyGrossIncome, monthlyNetIncome, monthlyCashFlow float64
	var totalNOI, totalDebtService, totalVacancyRate float64
	var propertiesWithVacancy int

	// Calculate basic metrics
	for _, p := range properties {
		value := p.CurrentValue
		if value == 0 {
			value = p.PurchasePrice
		}
		totalValue += value
		totalDebt += p.MortgageBalance

		rent := p.MonthlyRent
		vacancyRate := 5.0 // Default 5% vacancy
		effectiveRent := rent * (1 - vacancyRate/100)
		expenses := p.MonthlyExpenses
		mortgagePayment := p.MortgagePayment

		monthlyGrossIncome += rent
		monthlyNetIncome += effectiveRent - expenses
		monthlyCashFlow += effectiveRent - expenses - mortgagePayment

		// NOI = (effective rent - expenses) * 12
		noi := (effectiveRent - expenses) * 12
		totalNOI += noi
		totalDebtService += mortgagePayment * 12

		totalVacancyRate += vacancyRate
		propertiesWithVacancy++
	}

	totalEquity := totalValue - totalDebt

	// Performance metrics
	var portfolioCapRate, cashOnCashReturn, ltv, dscr, grossYield, avgVacancyRate float64

	if totalValue > 0 {
		portfolioCapRate = (totalNOI / totalValue) * 100
		grossYield = ((monthlyGrossIncome * 12) / totalValue) * 100
		ltv = (totalDebt / totalValue) * 100
	}
	if totalEquity > 0 {
		cashOnCashReturn = ((monthlyCashFlow * 12) / totalEquity) * 100
	}
	if totalDebtService > 0 {
		dscr = totalNOI / totalDebtService
	}
	if propertiesWithVacancy > 0 {
		avgVacancyRate = totalVacancyRate / float64(propertiesWithVacancy)
	}

	// Calculate allocations
	allocationByMarket := h.calculateAllocation(properties, func(p PortfolioProperty) string {
		return fmt.Sprintf("%s, %s", p.City, p.State)
	})
	allocationByType := h.calculateAllocation(properties, func(p PortfolioProperty) string {
		if p.PropertyType != nil {
			return *p.PropertyType
		}
		return "Single Family"
	})
	allocationByState := h.calculateAllocation(properties, func(p PortfolioProperty) string {
		return p.State
	})

	// Calculate property scores
	propertyScores := make([]PropertyScore, len(properties))
	var totalScore int
	for i, p := range properties {
		propertyScores[i] = h.calculatePropertyScore(p)
		totalScore += propertyScores[i].Score
	}

	// Find best/worst performers
	var bestPerformer, worstPerformer *PropertyScore
	if len(propertyScores) > 0 {
		best := propertyScores[0]
		worst := propertyScores[0]
		for i := 1; i < len(propertyScores); i++ {
			if propertyScores[i].Score > best.Score {
				best = propertyScores[i]
			}
			if propertyScores[i].Score < worst.Score {
				worst = propertyScores[i]
			}
		}
		bestPerformer = &best
		worstPerformer = &worst
	}

	avgScore := 0
	if len(propertyScores) > 0 {
		avgScore = totalScore / len(propertyScores)
	}

	// Calculate cumulative metrics
	cumulativeMetrics := h.calculateCumulativeMetrics(properties)

	return PortfolioMetrics{
		TotalValue:          totalValue,
		TotalEquity:         totalEquity,
		TotalDebt:           totalDebt,
		MonthlyGrossIncome:  monthlyGrossIncome,
		MonthlyNetIncome:    monthlyNetIncome,
		MonthlyCashFlow:     monthlyCashFlow,
		PropertyCount:       len(properties),
		PortfolioCapRate:    round2(portfolioCapRate),
		CashOnCashReturn:    round2(cashOnCashReturn),
		LTV:                 round2(ltv),
		DSCR:                round2(dscr),
		GrossYield:          round2(grossYield),
		AverageVacancyRate:  round2(avgVacancyRate),
		AllocationByMarket:  allocationByMarket,
		AllocationByType:    allocationByType,
		AllocationByState:   allocationByState,
		PropertyScores:      propertyScores,
		BestPerformer:       bestPerformer,
		WorstPerformer:      worstPerformer,
		AverageScore:        avgScore,
		CumulativeMetrics:   cumulativeMetrics,
	}
}

// calculateAllocation groups properties by a key and calculates allocation
func (h *Handler) calculateAllocation(properties []PortfolioProperty, getKey func(PortfolioProperty) string) []AllocationItem {
	groups := make(map[string]struct{ value float64; count int })
	var totalValue float64

	for _, p := range properties {
		key := getKey(p)
		if key == "" {
			key = "Unknown"
		}
		value := p.CurrentValue
		if value == 0 {
			value = p.PurchasePrice
		}
		totalValue += value

		existing := groups[key]
		groups[key] = struct{ value float64; count int }{
			value: existing.value + value,
			count: existing.count + 1,
		}
	}

	items := make([]AllocationItem, 0, len(groups))
	for name, data := range groups {
		percent := 0.0
		if totalValue > 0 {
			percent = (data.value / totalValue) * 100
		}
		items = append(items, AllocationItem{
			Name:    name,
			Value:   data.value,
			Percent: round2(percent),
			Count:   data.count,
		})
	}

	// Sort by value descending
	for i := 0; i < len(items)-1; i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Value > items[i].Value {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	return items
}

// calculatePropertyScore computes a score (0-100) and grade for a property
func (h *Handler) calculatePropertyScore(p PortfolioProperty) PropertyScore {
	value := p.CurrentValue
	if value == 0 {
		value = p.PurchasePrice
	}

	vacancyRate := 5.0 // Default 5%
	effectiveRent := p.MonthlyRent * (1 - vacancyRate/100)
	noi := (effectiveRent - p.MonthlyExpenses) * 12
	equity := value - p.MortgageBalance
	cashFlow := effectiveRent - p.MonthlyExpenses - p.MortgagePayment

	// Cap rate factor (0-25 points) - Higher is better, max at 10%
	capRate := 0.0
	if value > 0 {
		capRate = (noi / value) * 100
	}
	capRateScore := min(capRate*2.5, 25)

	// Cash-on-Cash Return factor (0-25 points) - max at 12%
	annualCashFlow := cashFlow * 12
	cashInvested := p.PurchasePrice - p.MortgageBalance
	cocReturn := 0.0
	if cashInvested > 0 {
		cocReturn = (annualCashFlow / cashInvested) * 100
	}
	cocScore := min(max((cocReturn/12)*25, 0), 25)

	// Equity factor (0-25 points)
	equityPercent := 0.0
	if value > 0 {
		equityPercent = (equity / value) * 100
	}
	equityScore := min((equityPercent/100)*25, 25)

	// Appreciation factor (0-25 points)
	appreciation := 0.0
	if value > 0 && p.PurchasePrice > 0 {
		appreciation = ((value - p.PurchasePrice) / p.PurchasePrice) * 100
	}
	appreciationScore := min(max(appreciation*1.25, 0), 25)

	totalScore := int(capRateScore + cocScore + equityScore + appreciationScore)

	// Convert to grade
	var grade string
	switch {
	case totalScore >= 90:
		grade = "A+"
	case totalScore >= 80:
		grade = "A"
	case totalScore >= 70:
		grade = "B+"
	case totalScore >= 60:
		grade = "B"
	case totalScore >= 50:
		grade = "C+"
	case totalScore >= 40:
		grade = "C"
	case totalScore >= 30:
		grade = "D"
	default:
		grade = "F"
	}

	return PropertyScore{
		PropertyID: p.ID,
		Address:    p.Address,
		Score:      totalScore,
		Grade:      grade,
		Factors: PropertyScoreFactors{
			CapRate:      int(capRateScore),
			CashFlow:     int(cocScore),
			Equity:       int(equityScore),
			Appreciation: int(appreciationScore),
		},
	}
}

// calculateCumulativeMetrics computes lifetime metrics for the portfolio
func (h *Handler) calculateCumulativeMetrics(properties []PortfolioProperty) PortfolioCumulativeMetrics {
	if len(properties) == 0 {
		return PortfolioCumulativeMetrics{OldestPurchaseDate: time.Now().UTC().Format(time.RFC3339)}
	}

	now := time.Now()
	var oldestDate time.Time
	var totalRentCollected, totalExpensesPaid, totalMortgagePaid, totalCashFlow float64
	var totalPrincipalPaidDown, totalAppreciationGain, totalEquityBuilt float64
	var totalYearsOwned, totalCashInvested float64

	for _, p := range properties {
		purchaseDate := now
		if p.PurchaseDate != nil {
			purchaseDate = *p.PurchaseDate
		}

		if oldestDate.IsZero() || purchaseDate.Before(oldestDate) {
			oldestDate = purchaseDate
		}

		// Months owned
		monthsOwned := int(now.Sub(purchaseDate).Hours() / (24 * 30.44))
		if monthsOwned < 1 {
			monthsOwned = 1
		}
		yearsOwned := float64(monthsOwned) / 12.0
		totalYearsOwned += yearsOwned

		// Cumulative income (simplified - uses current baseline × months)
		vacancyRate := 5.0 // Default
		effectiveRent := p.MonthlyRent * (1 - vacancyRate/100)
		rentCollected := effectiveRent * float64(monthsOwned)
		expensesPaid := p.MonthlyExpenses * float64(monthsOwned)
		mortgagePaid := p.MortgagePayment * float64(monthsOwned)
		cashFlow := rentCollected - expensesPaid - mortgagePaid

		totalRentCollected += rentCollected
		totalExpensesPaid += expensesPaid
		totalMortgagePaid += mortgagePaid
		totalCashFlow += cashFlow

		// Principal paid down (estimate)
		principalPaid := 0.0
		if p.MortgagePayment > 0 && p.MortgageRate > 0 {
			// Rough estimate: about 30% of payments go to principal on average
			principalPaid = mortgagePaid * 0.3
		}
		totalPrincipalPaidDown += principalPaid

		// Appreciation
		currentValue := p.CurrentValue
		if currentValue == 0 {
			currentValue = p.PurchasePrice
		}
		appreciation := currentValue - p.PurchasePrice
		totalAppreciationGain += appreciation
		totalEquityBuilt += appreciation + principalPaid

		// Cash invested
		cashInvested := p.PurchasePrice - p.MortgageBalance
		if cashInvested < 0 {
			cashInvested = p.PurchasePrice * 0.2 // Default 20%
		}
		totalCashInvested += cashInvested
	}

	avgYearsOwned := 0.0
	if len(properties) > 0 {
		avgYearsOwned = totalYearsOwned / float64(len(properties))
	}

	totalReturn := totalCashFlow + totalEquityBuilt
	annualizedReturn := 0.0
	if avgYearsOwned > 0 {
		annualizedReturn = totalReturn / avgYearsOwned
	}

	portfolioCoCLifetime := 0.0
	if totalCashInvested > 0 {
		portfolioCoCLifetime = (totalCashFlow / totalCashInvested) * 100
	}

	return PortfolioCumulativeMetrics{
		OldestPurchaseDate:          oldestDate.UTC().Format(time.RFC3339),
		AvgYearsOwned:               round1(avgYearsOwned),
		TotalRentCollected:          round0(totalRentCollected),
		TotalExpensesPaid:           round0(totalExpensesPaid),
		TotalMortgagePaid:           round0(totalMortgagePaid),
		TotalCashFlow:               round0(totalCashFlow),
		TotalPrincipalPaidDown:      round0(totalPrincipalPaidDown),
		TotalAppreciationGain:       round0(totalAppreciationGain),
		TotalEquityBuilt:            round0(totalEquityBuilt),
		TotalReturn:                 round0(totalReturn),
		AnnualizedReturn:            round0(annualizedReturn),
		PortfolioCashOnCashLifetime: round1(portfolioCoCLifetime),
	}
}

// Helper functions for rounding
func round0(v float64) float64 {
	return float64(int(v + 0.5))
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// ============================================================================
// Recommendations Endpoint
// ============================================================================

// RecommendationType represents the type of recommendation
type RecommendationType string

const (
	RecommendationTypeRefinance       RecommendationType = "refinance_opportunity"
	RecommendationTypeDiversification RecommendationType = "diversification_alert"
	RecommendationTypeUnderperformer  RecommendationType = "underperformer"
	RecommendationTypeOverperformer   RecommendationType = "overperformer"
	RecommendationTypeConcentration   RecommendationType = "concentration_risk"
	RecommendationTypeCashFlow        RecommendationType = "cash_flow_improvement"
)

// RecommendationPriority represents the priority level
type RecommendationPriority string

const (
	PriorityHigh   RecommendationPriority = "high"
	PriorityMedium RecommendationPriority = "medium"
	PriorityLow    RecommendationPriority = "low"
)

// Recommendation represents a portfolio recommendation
type Recommendation struct {
	ID              string                 `json:"id"`
	Type            RecommendationType     `json:"type"`
	Priority        RecommendationPriority `json:"priority"`
	Title           string                 `json:"title"`
	Description     string                 `json:"description"`
	Impact          string                 `json:"impact"`
	PropertyID      *string                `json:"propertyId,omitempty"`
	PropertyAddress *string                `json:"propertyAddress,omitempty"`
	Details         map[string]interface{} `json:"details"`
}

// RecommendationsSummary summarizes recommendations by priority
type RecommendationsSummary struct {
	Total          int `json:"total"`
	HighPriority   int `json:"highPriority"`
	MediumPriority int `json:"mediumPriority"`
	LowPriority    int `json:"lowPriority"`
}

// RecommendationsResponse is the API response for recommendations
type RecommendationsResponse struct {
	Success         bool                   `json:"success"`
	Recommendations []Recommendation       `json:"recommendations"`
	Summary         RecommendationsSummary `json:"summary"`
	AnalyzedAt      string                 `json:"analyzedAt"`
	Message         string                 `json:"message,omitempty"`
}

// Current market rate (in production, this would come from market data service)
const currentMarketRate = 6.5

// GetRecommendations returns AI-powered portfolio recommendations
// GET /api/v2/portfolio/recommendations
func (h *Handler) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	h.logger.Info("generating portfolio recommendations", "userId", user.UserID)

	// Get all properties for the user
	properties, err := h.getPropertiesByUserID(ctx, user.UserID)
	if err != nil {
		h.logger.Error("failed to get properties", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to retrieve portfolio")
		return
	}

	if len(properties) == 0 {
		httputil.JSON(w, http.StatusOK, RecommendationsResponse{
			Success:         true,
			Recommendations: []Recommendation{},
			Summary:         RecommendationsSummary{},
			AnalyzedAt:      time.Now().UTC().Format(time.RFC3339),
			Message:         "Add properties to your portfolio to receive recommendations",
		})
		return
	}

	// Calculate metrics first (needed for some recommendations)
	metrics := h.calculatePortfolioMetrics(properties)

	// Generate recommendations
	recommendations := h.generateRecommendations(properties, metrics)

	// Calculate summary
	summary := RecommendationsSummary{Total: len(recommendations)}
	for _, rec := range recommendations {
		switch rec.Priority {
		case PriorityHigh:
			summary.HighPriority++
		case PriorityMedium:
			summary.MediumPriority++
		case PriorityLow:
			summary.LowPriority++
		}
	}

	httputil.JSON(w, http.StatusOK, RecommendationsResponse{
		Success:         true,
		Recommendations: recommendations,
		Summary:         summary,
		AnalyzedAt:      time.Now().UTC().Format(time.RFC3339),
	})
}

// generateRecommendations creates portfolio recommendations
func (h *Handler) generateRecommendations(properties []PortfolioProperty, metrics PortfolioMetrics) []Recommendation {
	var recommendations []Recommendation

	// Run all analysis modules
	recommendations = append(recommendations, h.analyzeRefinanceOpportunities(properties)...)
	recommendations = append(recommendations, h.analyzeDiversification(properties, metrics)...)
	recommendations = append(recommendations, h.analyzePerformance(properties, metrics)...)
	recommendations = append(recommendations, h.analyzeConcentrationRisk(properties, metrics)...)
	recommendations = append(recommendations, h.analyzeCashFlowImprovement(properties)...)

	// Sort by priority (high first)
	priorityOrder := map[RecommendationPriority]int{
		PriorityHigh:   0,
		PriorityMedium: 1,
		PriorityLow:    2,
	}
	for i := 0; i < len(recommendations)-1; i++ {
		for j := i + 1; j < len(recommendations); j++ {
			if priorityOrder[recommendations[j].Priority] < priorityOrder[recommendations[i].Priority] {
				recommendations[i], recommendations[j] = recommendations[j], recommendations[i]
			}
		}
	}

	return recommendations
}

// analyzeRefinanceOpportunities checks for refinance opportunities
func (h *Handler) analyzeRefinanceOpportunities(properties []PortfolioProperty) []Recommendation {
	var recommendations []Recommendation

	for _, p := range properties {
		if p.MortgageRate == 0 || p.MortgageBalance == 0 {
			continue
		}

		rateDiff := p.MortgageRate - currentMarketRate
		if rateDiff >= 1.0 {
			// Calculate potential savings
			currentPayment := calculateMonthlyMortgagePayment(p.MortgageBalance, p.MortgageRate, 30)
			newPayment := calculateMonthlyMortgagePayment(p.MortgageBalance, currentMarketRate, 30)
			monthlySavings := currentPayment - newPayment

			priority := PriorityLow
			if rateDiff >= 2.0 {
				priority = PriorityHigh
			} else if rateDiff >= 1.5 {
				priority = PriorityMedium
			}

			recommendations = append(recommendations, Recommendation{
				ID:              fmt.Sprintf("refinance-%s", p.ID),
				Type:            RecommendationTypeRefinance,
				Priority:        priority,
				Title:           "Refinance Opportunity",
				Description:     fmt.Sprintf("%s has a %.1f%% rate, which is %.1f%% above current market rate of %.1f%%", p.Address, p.MortgageRate, rateDiff, currentMarketRate),
				Impact:          fmt.Sprintf("Potential savings: $%s/month", formatNumber(monthlySavings)),
				PropertyID:      &p.ID,
				PropertyAddress: &p.Address,
				Details: map[string]interface{}{
					"currentRate":            p.MortgageRate,
					"marketRate":             currentMarketRate,
					"rateDifference":         rateDiff,
					"mortgageBalance":        p.MortgageBalance,
					"estimatedMonthlySavings": monthlySavings,
					"estimatedAnnualSavings":  monthlySavings * 12,
				},
			})
		}
	}

	return recommendations
}

// analyzeDiversification checks for concentration issues
func (h *Handler) analyzeDiversification(properties []PortfolioProperty, metrics PortfolioMetrics) []Recommendation {
	var recommendations []Recommendation

	// Check market concentration
	if len(metrics.AllocationByMarket) > 0 {
		topMarket := metrics.AllocationByMarket[0]
		if topMarket.Percent > 70 {
			priority := PriorityMedium
			if topMarket.Percent > 85 {
				priority = PriorityHigh
			}

			recommendations = append(recommendations, Recommendation{
				ID:          "diversification-market",
				Type:        RecommendationTypeDiversification,
				Priority:    priority,
				Title:       "Geographic Concentration Risk",
				Description: fmt.Sprintf("%.0f%% of your portfolio is concentrated in %s", topMarket.Percent, topMarket.Name),
				Impact:      "Consider diversifying to reduce geographic risk exposure",
				Details: map[string]interface{}{
					"concentratedMarket":    topMarket.Name,
					"concentrationPercent":  topMarket.Percent,
					"marketCount":           len(metrics.AllocationByMarket),
				},
			})
		}
	}

	// Check property type concentration
	if len(metrics.AllocationByType) > 0 && len(properties) >= 3 {
		topType := metrics.AllocationByType[0]
		if topType.Percent > 80 {
			recommendations = append(recommendations, Recommendation{
				ID:          "diversification-type",
				Type:        RecommendationTypeDiversification,
				Priority:    PriorityLow,
				Title:       "Property Type Concentration",
				Description: fmt.Sprintf("%.0f%% of your portfolio is %s properties", topType.Percent, topType.Name),
				Impact:      "Consider diversifying property types for balanced risk",
				Details: map[string]interface{}{
					"concentratedType":     topType.Name,
					"concentrationPercent": topType.Percent,
					"typeCount":            len(metrics.AllocationByType),
				},
			})
		}
	}

	return recommendations
}

// analyzePerformance checks for under/over performers
func (h *Handler) analyzePerformance(properties []PortfolioProperty, metrics PortfolioMetrics) []Recommendation {
	var recommendations []Recommendation

	if len(properties) < 2 {
		return recommendations
	}

	avgScore := metrics.AverageScore

	for _, score := range metrics.PropertyScores {
		// Underperformer
		if score.Score < avgScore-20 && score.Score < 50 {
			priority := PriorityMedium
			if score.Score < 30 {
				priority = PriorityHigh
			}

			addr := score.Address
			recommendations = append(recommendations, Recommendation{
				ID:              fmt.Sprintf("underperformer-%s", score.PropertyID),
				Type:            RecommendationTypeUnderperformer,
				Priority:        priority,
				Title:           "Underperforming Property",
				Description:     fmt.Sprintf("%s scores %s (%d), which is %d points below your portfolio average", score.Address, score.Grade, score.Score, avgScore-score.Score),
				Impact:          "Consider evaluating whether to hold, improve, or sell this property",
				PropertyID:      &score.PropertyID,
				PropertyAddress: &addr,
				Details: map[string]interface{}{
					"propertyScore":    score.Score,
					"propertyGrade":    score.Grade,
					"portfolioAverage": avgScore,
					"scoreDifference":  avgScore - score.Score,
					"factors":          score.Factors,
				},
			})
		}

		// Overperformer
		if score.Score > avgScore+20 && score.Score >= 80 {
			addr := score.Address
			recommendations = append(recommendations, Recommendation{
				ID:              fmt.Sprintf("overperformer-%s", score.PropertyID),
				Type:            RecommendationTypeOverperformer,
				Priority:        PriorityLow,
				Title:           "Top Performing Property",
				Description:     fmt.Sprintf("%s scores %s (%d), which is %d points above your portfolio average", score.Address, score.Grade, score.Score, score.Score-avgScore),
				Impact:          "This property is a strong performer - consider similar investments",
				PropertyID:      &score.PropertyID,
				PropertyAddress: &addr,
				Details: map[string]interface{}{
					"propertyScore":    score.Score,
					"propertyGrade":    score.Grade,
					"portfolioAverage": avgScore,
					"scoreDifference":  score.Score - avgScore,
					"factors":          score.Factors,
				},
			})
		}
	}

	return recommendations
}

// analyzeConcentrationRisk checks for single property dominance
func (h *Handler) analyzeConcentrationRisk(properties []PortfolioProperty, metrics PortfolioMetrics) []Recommendation {
	var recommendations []Recommendation

	if len(properties) < 3 || metrics.TotalValue == 0 {
		return recommendations
	}

	for _, p := range properties {
		propValue := p.CurrentValue
		if propValue == 0 {
			propValue = p.PurchasePrice
		}
		valuePercent := (propValue / metrics.TotalValue) * 100

		if valuePercent > 50 {
			priority := PriorityMedium
			if valuePercent > 70 {
				priority = PriorityHigh
			}

			recommendations = append(recommendations, Recommendation{
				ID:              fmt.Sprintf("concentration-%s", p.ID),
				Type:            RecommendationTypeConcentration,
				Priority:        priority,
				Title:           "Single Property Concentration",
				Description:     fmt.Sprintf("%s represents %.0f%% of your total portfolio value", p.Address, valuePercent),
				Impact:          "High exposure to single property risk",
				PropertyID:      &p.ID,
				PropertyAddress: &p.Address,
				Details: map[string]interface{}{
					"propertyValue":        propValue,
					"portfolioTotal":       metrics.TotalValue,
					"concentrationPercent": valuePercent,
				},
			})
		}
	}

	return recommendations
}

// analyzeCashFlowImprovement checks for cash flow issues
func (h *Handler) analyzeCashFlowImprovement(properties []PortfolioProperty) []Recommendation {
	var recommendations []Recommendation

	for _, p := range properties {
		// Check for negative cash flow
		if p.MonthlyCashFlow < -200 {
			priority := PriorityMedium
			if p.MonthlyCashFlow < -500 {
				priority = PriorityHigh
			}

			recommendations = append(recommendations, Recommendation{
				ID:              fmt.Sprintf("negative-cf-%s", p.ID),
				Type:            RecommendationTypeCashFlow,
				Priority:        priority,
				Title:           "Negative Cash Flow Property",
				Description:     fmt.Sprintf("%s has negative cash flow of $%s/month", p.Address, formatNumber(-p.MonthlyCashFlow)),
				Impact:          "Consider rent increase, expense reduction, or refinancing",
				PropertyID:      &p.ID,
				PropertyAddress: &p.Address,
				Details: map[string]interface{}{
					"monthlyRent":     p.MonthlyRent,
					"totalExpenses":   p.MonthlyExpenses + p.MortgagePayment,
					"cashFlow":        p.MonthlyCashFlow,
				},
			})
		}
	}

	return recommendations
}

// calculateMonthlyMortgagePayment calculates monthly payment using amortization formula
func calculateMonthlyMortgagePayment(principal, annualRate float64, termYears int) float64 {
	monthlyRate := annualRate / 100 / 12
	numPayments := float64(termYears * 12)

	if monthlyRate == 0 {
		return principal / numPayments
	}

	return (principal * (monthlyRate * pow(1+monthlyRate, numPayments))) /
		(pow(1+monthlyRate, numPayments) - 1)
}

// pow calculates power (simple implementation)
func pow(base, exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}

// formatNumber formats a number with commas
func formatNumber(n float64) string {
	return fmt.Sprintf("%.0f", n)
}

// =============================================================================
// Adjustments Endpoints
// =============================================================================

// AdjustmentResponse represents an adjustment in API responses
type AdjustmentResponse struct {
	ID        string  `json:"id"`
	Month     string  `json:"month"` // YYYY-MM format
	Type      string  `json:"type"`
	Amount    float64 `json:"amount"`
	Note      *string `json:"note,omitempty"`
	CreatedAt string  `json:"createdAt"`
}

// AdjustmentsListResponse represents the adjustments list API response
type AdjustmentsListResponse struct {
	Success     bool                 `json:"success"`
	Adjustments []AdjustmentResponse `json:"adjustments"`
}

// CreateAdjustmentRequest represents a request to create/update an adjustment
type CreateAdjustmentRequest struct {
	Month  string  `json:"month" validate:"required"` // YYYY-MM format
	Type   string  `json:"type" validate:"required,oneof=rent expense mortgage"`
	Amount float64 `json:"amount" validate:"required"`
	Note   *string `json:"note,omitempty"`
}

// GetAdjustments handles GET /api/v2/portfolio/{id}/adjustments
// Lists all adjustments for a property
func (h *Handler) GetAdjustments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	propertyID := chi.URLParam(r, "id")
	if propertyID == "" {
		httputil.BadRequest(w, "property id is required")
		return
	}

	// Verify property exists and user owns it
	property, err := h.getPropertyByID(ctx, propertyID, user.UserID)
	if err != nil {
		if err.Error() == "property not found" {
			httputil.NotFound(w, "property not found")
			return
		}
		h.logger.Error("failed to get property", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to verify property")
		return
	}

	if property.UserID != user.UserID {
		httputil.Error(w, http.StatusForbidden, "not authorized to access this property")
		return
	}

	// Check if queries are available
	if h.store == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "Database service not available")
		return
	}

	// Get adjustments
	adjustments, err := h.store.Q().GetPropertyAdjustments(ctx, propertyID)
	if err != nil {
		h.logger.Error("failed to get adjustments", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get adjustments")
		return
	}

	// Convert to response format
	response := AdjustmentsListResponse{
		Success:     true,
		Adjustments: make([]AdjustmentResponse, 0, len(adjustments)),
	}

	for _, adj := range adjustments {
		var note *string
		if adj.Note.Valid {
			note = &adj.Note.String
		}

		response.Adjustments = append(response.Adjustments, AdjustmentResponse{
			ID:        adj.ID,
			Month:     adj.Month.Time.Format("2006-01"),
			Type:      adj.Type,
			Amount:    adj.Amount,
			Note:      note,
			CreatedAt: adj.CreatedAt.Time.Format(time.RFC3339),
		})
	}

	httputil.JSON(w, http.StatusOK, response)
}

// CreateAdjustment handles POST /api/v2/portfolio/{id}/adjustments
// Creates or updates an adjustment for a property
func (h *Handler) CreateAdjustment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	propertyID := chi.URLParam(r, "id")
	if propertyID == "" {
		httputil.BadRequest(w, "property id is required")
		return
	}

	// Parse request
	var req CreateAdjustmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate request
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Validate month format (YYYY-MM)
	if len(req.Month) != 7 || req.Month[4] != '-' {
		httputil.BadRequest(w, "invalid month format. Use YYYY-MM")
		return
	}

	// Verify property exists and user owns it
	property, err := h.getPropertyByID(ctx, propertyID, user.UserID)
	if err != nil {
		if err.Error() == "property not found" {
			httputil.NotFound(w, "property not found")
			return
		}
		h.logger.Error("failed to get property", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to verify property")
		return
	}

	if property.UserID != user.UserID {
		httputil.Error(w, http.StatusForbidden, "not authorized to modify this property")
		return
	}

	// Check if queries are available
	if h.store == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "Database service not available")
		return
	}

	// Parse month to timestamp (first day of month)
	monthDate, err := time.Parse("2006-01", req.Month)
	if err != nil {
		httputil.BadRequest(w, "invalid month format")
		return
	}

	// Validate month is within ownership period
	if property.PurchaseDate != nil {
		purchaseMonth := time.Date(property.PurchaseDate.Year(), property.PurchaseDate.Month(), 1, 0, 0, 0, 0, time.UTC)
		if monthDate.Before(purchaseMonth) {
			httputil.BadRequest(w, "adjustment month cannot be before property purchase date")
			return
		}
	}

	// Validate month is not in the future
	now := time.Now()
	currentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if monthDate.After(currentMonth) {
		httputil.BadRequest(w, "adjustment month cannot be in the future")
		return
	}

	// Create or update adjustment
	id := uuid.New().String()
	var noteText pgtype.Text
	if req.Note != nil {
		noteText = pgtype.Text{String: *req.Note, Valid: true}
	}

	adjustment, err := h.store.Q().CreateOrUpdateAdjustment(ctx, queries.CreateOrUpdateAdjustmentParams{
		ID:         id,
		PropertyID: propertyID,
		Month:      pgtype.Timestamp{Time: monthDate, Valid: true},
		Type:       req.Type,
		Amount:     req.Amount,
		Note:       noteText,
	})
	if err != nil {
		h.logger.Error("failed to create adjustment", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to create adjustment")
		return
	}

	var note *string
	if adjustment.Note.Valid {
		note = &adjustment.Note.String
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"adjustment": AdjustmentResponse{
			ID:        adjustment.ID,
			Month:     adjustment.Month.Time.Format("2006-01"),
			Type:      adjustment.Type,
			Amount:    adjustment.Amount,
			Note:      note,
			CreatedAt: adjustment.CreatedAt.Time.Format(time.RFC3339),
		},
	})
}

// =============================================================================
// Baseline Changes Endpoints
// =============================================================================

// Valid fields that can have baseline changes
var validBaselineFields = []string{
	"monthlyRent",
	"vacancyRate",
	"expenses.maintenance",
	"expenses.tax",
	"expenses.insurance",
	"expenses.hoa",
	"expenses.other",
	"mortgagePayment",
	"mortgageRate",
	"mortgageBalance",
}

// BaselineChangeResponse represents a baseline change in API responses
type BaselineChangeResponse struct {
	ID            string   `json:"id"`
	Field         string   `json:"field"`
	EffectiveDate string   `json:"effectiveDate"` // YYYY-MM format
	NewValue      float64  `json:"newValue"`
	PreviousValue *float64 `json:"previousValue,omitempty"`
	Note          *string  `json:"note,omitempty"`
	CreatedAt     string   `json:"createdAt"`
}

// BaselineChangesListResponse represents the baseline changes list API response
type BaselineChangesListResponse struct {
	Success         bool                     `json:"success"`
	BaselineChanges []BaselineChangeResponse `json:"baselineChanges"`
}

// CreateBaselineChangeRequest represents a request to create/update a baseline change
type CreateBaselineChangeRequest struct {
	Field         string  `json:"field" validate:"required"`
	EffectiveDate string  `json:"effectiveDate" validate:"required"` // YYYY-MM format
	NewValue      float64 `json:"newValue" validate:"required"`
	Note          *string `json:"note,omitempty"`
}

// GetBaselineChanges handles GET /api/v2/portfolio/{id}/baseline-changes
// Lists all baseline changes for a property
func (h *Handler) GetBaselineChanges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	propertyID := chi.URLParam(r, "id")
	if propertyID == "" {
		httputil.BadRequest(w, "property id is required")
		return
	}

	// Verify property exists and user owns it
	property, err := h.getPropertyByID(ctx, propertyID, user.UserID)
	if err != nil {
		if err.Error() == "property not found" {
			httputil.NotFound(w, "property not found")
			return
		}
		h.logger.Error("failed to get property", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to verify property")
		return
	}

	if property.UserID != user.UserID {
		httputil.Error(w, http.StatusForbidden, "not authorized to access this property")
		return
	}

	// Check if queries are available
	if h.store == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "Database service not available")
		return
	}

	// Get baseline changes
	changes, err := h.store.Q().GetPropertyBaselineChanges(ctx, propertyID)
	if err != nil {
		h.logger.Error("failed to get baseline changes", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get baseline changes")
		return
	}

	// Convert to response format
	response := BaselineChangesListResponse{
		Success:         true,
		BaselineChanges: make([]BaselineChangeResponse, 0, len(changes)),
	}

	for _, bc := range changes {
		var previousValue *float64
		if bc.PreviousValue.Valid {
			previousValue = &bc.PreviousValue.Float64
		}

		var note *string
		if bc.Note.Valid {
			note = &bc.Note.String
		}

		response.BaselineChanges = append(response.BaselineChanges, BaselineChangeResponse{
			ID:            bc.ID,
			Field:         bc.Field,
			EffectiveDate: bc.EffectiveDate.Time.Format("2006-01"),
			NewValue:      bc.NewValue,
			PreviousValue: previousValue,
			Note:          note,
			CreatedAt:     bc.CreatedAt.Time.Format(time.RFC3339),
		})
	}

	httputil.JSON(w, http.StatusOK, response)
}

// CreateBaselineChange handles POST /api/v2/portfolio/{id}/baseline-changes
// Creates or updates a baseline change for a property
func (h *Handler) CreateBaselineChange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	propertyID := chi.URLParam(r, "id")
	if propertyID == "" {
		httputil.BadRequest(w, "property id is required")
		return
	}

	// Parse request
	var req CreateBaselineChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate request
	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Validate field name
	validField := false
	for _, f := range validBaselineFields {
		if f == req.Field {
			validField = true
			break
		}
	}
	if !validField {
		httputil.BadRequest(w, fmt.Sprintf("invalid field. Must be one of: %s", strings.Join(validBaselineFields, ", ")))
		return
	}

	// Validate effective date format (YYYY-MM)
	if len(req.EffectiveDate) != 7 || req.EffectiveDate[4] != '-' {
		httputil.BadRequest(w, "invalid effectiveDate format. Use YYYY-MM")
		return
	}

	// Verify property exists and user owns it
	property, err := h.getPropertyByID(ctx, propertyID, user.UserID)
	if err != nil {
		if err.Error() == "property not found" {
			httputil.NotFound(w, "property not found")
			return
		}
		h.logger.Error("failed to get property", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to verify property")
		return
	}

	if property.UserID != user.UserID {
		httputil.Error(w, http.StatusForbidden, "not authorized to modify this property")
		return
	}

	// Check if queries are available
	if h.store == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "Database service not available")
		return
	}

	// Parse effective date to timestamp (first day of month)
	effectiveDate, err := time.Parse("2006-01", req.EffectiveDate)
	if err != nil {
		httputil.BadRequest(w, "invalid effectiveDate format")
		return
	}

	// Validate effective date is after purchase date
	if property.PurchaseDate != nil && effectiveDate.Before(*property.PurchaseDate) {
		httputil.BadRequest(w, "effective date cannot be before purchase date")
		return
	}

	// Validate effective date is not in the future
	now := time.Now()
	currentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if effectiveDate.After(currentMonth) {
		httputil.BadRequest(w, "effective date cannot be in the future")
		return
	}

	// Get previous value for this field
	var previousValue pgtype.Float8

	// Try to get the most recent baseline change for this field before the effective date
	latestChange, err := h.store.Q().GetLatestBaselineChange(ctx, queries.GetLatestBaselineChangeParams{
		PropertyID:    propertyID,
		Field:         req.Field,
		EffectiveDate: pgtype.Timestamp{Time: effectiveDate, Valid: true},
	})
	if err == nil {
		previousValue = pgtype.Float8{Float64: latestChange.NewValue, Valid: true}
	} else {
		// If no previous change, get from property's current value
		switch req.Field {
		case "monthlyRent":
			previousValue = pgtype.Float8{Float64: property.MonthlyRent, Valid: true}
		case "mortgagePayment":
			previousValue = pgtype.Float8{Float64: property.MortgagePayment, Valid: true}
		case "mortgageRate":
			previousValue = pgtype.Float8{Float64: property.MortgageRate, Valid: true}
		case "mortgageBalance":
			previousValue = pgtype.Float8{Float64: property.MortgageBalance, Valid: true}
		}
	}

	// Create or update baseline change
	id := uuid.New().String()
	var noteText pgtype.Text
	if req.Note != nil {
		noteText = pgtype.Text{String: *req.Note, Valid: true}
	}

	change, err := h.store.Q().CreateOrUpdateBaselineChange(ctx, queries.CreateOrUpdateBaselineChangeParams{
		ID:            id,
		PropertyID:    propertyID,
		Field:         req.Field,
		EffectiveDate: pgtype.Timestamp{Time: effectiveDate, Valid: true},
		NewValue:      req.NewValue,
		PreviousValue: previousValue,
		Note:          noteText,
	})
	if err != nil {
		h.logger.Error("failed to create baseline change", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to create baseline change")
		return
	}

	// For mortgageBalance changes, update the property's current balance
	if req.Field == "mortgageBalance" {
		// Get the most recent mortgageBalance baseline change
		latestBalance, err := h.store.Q().GetLatestBaselineChange(ctx, queries.GetLatestBaselineChangeParams{
			PropertyID:    propertyID,
			Field:         "mortgageBalance",
			EffectiveDate: pgtype.Timestamp{Time: time.Now().AddDate(100, 0, 0), Valid: true}, // Far future to get latest
		})
		if err == nil {
			// Update property's mortgage balance
			_, updateErr := h.updateProperty(ctx, propertyID, user.UserID, &UpdatePropertyRequest{
				MortgageBalance: &latestBalance.NewValue,
			})
			if updateErr != nil {
				h.logger.Warn("failed to update property mortgage balance", "error", updateErr)
			}
		}
	}

	var prevVal *float64
	if change.PreviousValue.Valid {
		prevVal = &change.PreviousValue.Float64
	}

	var note *string
	if change.Note.Valid {
		note = &change.Note.String
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"baselineChange": BaselineChangeResponse{
			ID:            change.ID,
			Field:         change.Field,
			EffectiveDate: change.EffectiveDate.Time.Format("2006-01"),
			NewValue:      change.NewValue,
			PreviousValue: prevVal,
			Note:          note,
			CreatedAt:     change.CreatedAt.Time.Format(time.RFC3339),
		},
	})
}
