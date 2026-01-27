package portfolio

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/projection"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles portfolio-related HTTP requests
type Handler struct {
	db       *postgres.DB
	cfg      *config.Config
	validate *validator.Validate
	logger   *slog.Logger
}

// NewHandler creates a new portfolio handler
func NewHandler(db *postgres.DB, cfg *config.Config) *Handler {
	return &Handler{
		db:       db,
		cfg:      cfg,
		validate: validator.New(),
		logger:   slog.Default().With("component", "portfolio_handler"),
	}
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
	Beds            *int       `json:"beds,omitempty"`
	Baths           *float64   `json:"baths,omitempty"`
	Sqft            *int       `json:"sqft,omitempty"`
	YearBuilt       *int       `json:"yearBuilt,omitempty"`
	PurchasePrice   float64    `json:"purchasePrice"`
	PurchaseDate    *time.Time `json:"purchaseDate,omitempty"`
	CurrentValue    float64    `json:"currentValue"`
	MonthlyRent     float64    `json:"monthlyRent"`
	MortgageBalance float64    `json:"mortgageBalance"`
	MortgageRate    float64    `json:"mortgageRate"`
	MortgagePayment float64    `json:"mortgagePayment"`
	MonthlyExpenses float64    `json:"monthlyExpenses"`
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
	Beds            *int     `json:"beds,omitempty"`
	Baths           *float64 `json:"baths,omitempty"`
	Sqft            *int     `json:"sqft,omitempty"`
	YearBuilt       *int     `json:"yearBuilt,omitempty"`
	PurchasePrice   float64  `json:"purchasePrice" validate:"required,gt=0"`
	PurchaseDate    *string  `json:"purchaseDate,omitempty"`
	CurrentValue    *float64 `json:"currentValue,omitempty"`
	MonthlyRent     *float64 `json:"monthlyRent,omitempty"`
	MortgageBalance *float64 `json:"mortgageBalance,omitempty"`
	MortgageRate    *float64 `json:"mortgageRate,omitempty"`
	MortgagePayment *float64 `json:"mortgagePayment,omitempty"`
	MonthlyExpenses *float64 `json:"monthlyExpenses,omitempty"`
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
	Beds            *int     `json:"beds,omitempty"`
	Baths           *float64 `json:"baths,omitempty"`
	Sqft            *int     `json:"sqft,omitempty"`
	YearBuilt       *int     `json:"yearBuilt,omitempty"`
	PurchasePrice   *float64 `json:"purchasePrice,omitempty" validate:"omitempty,gt=0"`
	CurrentValue    *float64 `json:"currentValue,omitempty"`
	MonthlyRent     *float64 `json:"monthlyRent,omitempty"`
	MortgageBalance *float64 `json:"mortgageBalance,omitempty"`
	MortgageRate    *float64 `json:"mortgageRate,omitempty"`
	MortgagePayment *float64 `json:"mortgagePayment,omitempty"`
	MonthlyExpenses *float64 `json:"monthlyExpenses,omitempty"`
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

func (h *Handler) getPropertiesByUserID(ctx context.Context, userID string) ([]PortfolioProperty, error) {
	// Column names match Prisma @map directives in V2PortfolioProperty model
	query := `
		SELECT id, user_id, address, city, state, zip_code, property_type,
		       bedrooms, bathrooms, sqft, year_built, purchase_price, purchase_date,
		       current_value, monthly_rent, mortgage_balance, mortgage_rate,
		       mortgage_payment, status, notes,
		       created_at, updated_at
		FROM v2_portfolio_properties
		WHERE user_id = $1 AND status != 'deleted'
		ORDER BY created_at DESC
	`

	rows, err := h.db.Main.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	properties := make([]PortfolioProperty, 0)
	for rows.Next() {
		var p PortfolioProperty
		err := rows.Scan(
			&p.ID, &p.UserID, &p.Address, &p.City, &p.State, &p.ZipCode,
			&p.PropertyType, &p.Beds, &p.Baths, &p.Sqft, &p.YearBuilt,
			&p.PurchasePrice, &p.PurchaseDate, &p.CurrentValue, &p.MonthlyRent,
			&p.MortgageBalance, &p.MortgageRate, &p.MortgagePayment,
			&p.Status, &p.Notes,
			&p.CreatedAt, &p.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		// Calculate derived fields
		h.calculatePropertyMetrics(&p)
		properties = append(properties, p)
	}

	return properties, rows.Err()
}

func (h *Handler) getPropertyByID(ctx context.Context, propertyID, userID string) (*PortfolioProperty, error) {
	// Column names match Prisma @map directives in V2PortfolioProperty model
	query := `
		SELECT id, user_id, address, city, state, zip_code, property_type,
		       bedrooms, bathrooms, sqft, year_built, purchase_price, purchase_date,
		       current_value, monthly_rent, mortgage_balance, mortgage_rate,
		       mortgage_payment, status, notes,
		       created_at, updated_at
		FROM v2_portfolio_properties
		WHERE id = $1 AND user_id = $2 AND status != 'deleted'
	`

	var p PortfolioProperty
	err := h.db.Main.QueryRow(ctx, query, propertyID, userID).Scan(
		&p.ID, &p.UserID, &p.Address, &p.City, &p.State, &p.ZipCode,
		&p.PropertyType, &p.Beds, &p.Baths, &p.Sqft, &p.YearBuilt,
		&p.PurchasePrice, &p.PurchaseDate, &p.CurrentValue, &p.MonthlyRent,
		&p.MortgageBalance, &p.MortgageRate, &p.MortgagePayment,
		&p.Status, &p.Notes,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, fmt.Errorf("property not found")
		}
		return nil, err
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

	// Column names match Prisma @map directives in V2PortfolioProperty model
	query := `
		INSERT INTO v2_portfolio_properties (
			id, user_id, address, city, state, zip_code, property_type,
			bedrooms, bathrooms, sqft, year_built, purchase_price, purchase_date,
			current_value, monthly_rent, mortgage_balance, mortgage_rate,
			mortgage_payment, status, notes,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			$16, $17, $18, $19, $20, $21, $22
		)
		RETURNING id, user_id, address, city, state, zip_code, property_type,
		          bedrooms, bathrooms, sqft, year_built, purchase_price, purchase_date,
		          current_value, monthly_rent, mortgage_balance, mortgage_rate,
		          mortgage_payment, status, notes,
		          created_at, updated_at
	`

	var p PortfolioProperty
	err := h.db.Main.QueryRow(ctx, query,
		id, userID, req.Address, req.City, req.State, req.ZipCode,
		req.PropertyType, req.Beds, req.Baths, req.Sqft, req.YearBuilt,
		req.PurchasePrice, purchaseDate, currentValue, monthlyRent,
		mortgageBalance, mortgageRate, mortgagePayment,
		"active", req.Notes, now, now,
	).Scan(
		&p.ID, &p.UserID, &p.Address, &p.City, &p.State, &p.ZipCode,
		&p.PropertyType, &p.Beds, &p.Baths, &p.Sqft, &p.YearBuilt,
		&p.PurchasePrice, &p.PurchaseDate, &p.CurrentValue, &p.MonthlyRent,
		&p.MortgageBalance, &p.MortgageRate, &p.MortgagePayment,
		&p.Status, &p.Notes,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Calculate derived fields
	h.calculatePropertyMetrics(&p)
	return &p, nil
}

func (h *Handler) updateProperty(ctx context.Context, propertyID, userID string, req *UpdatePropertyRequest) (*PortfolioProperty, error) {
	// First verify property exists and belongs to user
	existing, err := h.getPropertyByID(ctx, propertyID, userID)
	if err != nil {
		return nil, err
	}

	// Build dynamic update query - column names match Prisma @map directives
	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argIndex := 1

	addUpdate := func(field string, value interface{}) {
		updates = append(updates, fmt.Sprintf(`%s = $%d`, field, argIndex))
		args = append(args, value)
		argIndex++
	}

	if req.Address != nil {
		addUpdate("address", *req.Address)
	}
	if req.City != nil {
		addUpdate("city", *req.City)
	}
	if req.State != nil {
		addUpdate("state", *req.State)
	}
	if req.ZipCode != nil {
		addUpdate("zip_code", *req.ZipCode)
	}
	if req.PropertyType != nil {
		addUpdate("property_type", *req.PropertyType)
	}
	if req.Beds != nil {
		addUpdate("bedrooms", *req.Beds)
	}
	if req.Baths != nil {
		addUpdate("bathrooms", *req.Baths)
	}
	if req.Sqft != nil {
		addUpdate("sqft", *req.Sqft)
	}
	if req.YearBuilt != nil {
		addUpdate("year_built", *req.YearBuilt)
	}
	if req.PurchasePrice != nil {
		addUpdate("purchase_price", *req.PurchasePrice)
	}
	if req.CurrentValue != nil {
		addUpdate("current_value", *req.CurrentValue)
	}
	if req.MonthlyRent != nil {
		addUpdate("monthly_rent", *req.MonthlyRent)
	}
	if req.MortgageBalance != nil {
		addUpdate("mortgage_balance", *req.MortgageBalance)
	}
	if req.MortgageRate != nil {
		addUpdate("mortgage_rate", *req.MortgageRate)
	}
	if req.MortgagePayment != nil {
		addUpdate("mortgage_payment", *req.MortgagePayment)
	}
	if req.Status != nil {
		addUpdate("status", *req.Status)
	}
	if req.Notes != nil {
		addUpdate("notes", *req.Notes)
	}

	// Always update updated_at
	addUpdate("updated_at", time.Now())

	if len(updates) == 1 {
		// Only updated_at, nothing else to update
		return existing, nil
	}

	// Build and execute query
	query := fmt.Sprintf(`
		UPDATE v2_portfolio_properties
		SET %s
		WHERE id = $%d AND user_id = $%d
		RETURNING id, user_id, address, city, state, zip_code, property_type,
		          bedrooms, bathrooms, sqft, year_built, purchase_price, purchase_date,
		          current_value, monthly_rent, mortgage_balance, mortgage_rate,
		          mortgage_payment, status, notes,
		          created_at, updated_at
	`, join(updates, ", "), argIndex, argIndex+1)

	args = append(args, propertyID, userID)

	var p PortfolioProperty
	err = h.db.Main.QueryRow(ctx, query, args...).Scan(
		&p.ID, &p.UserID, &p.Address, &p.City, &p.State, &p.ZipCode,
		&p.PropertyType, &p.Beds, &p.Baths, &p.Sqft, &p.YearBuilt,
		&p.PurchasePrice, &p.PurchaseDate, &p.CurrentValue, &p.MonthlyRent,
		&p.MortgageBalance, &p.MortgageRate, &p.MortgagePayment,
		&p.Status, &p.Notes,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Calculate derived fields
	h.calculatePropertyMetrics(&p)
	return &p, nil
}

func (h *Handler) deleteProperty(ctx context.Context, propertyID, userID string) error {
	// Soft delete by setting status to 'deleted'
	// Column names match Prisma @map directives
	query := `
		UPDATE v2_portfolio_properties
		SET status = 'deleted', updated_at = $1
		WHERE id = $2 AND user_id = $3 AND status != 'deleted'
	`

	result, err := h.db.Main.Exec(ctx, query, time.Now(), propertyID, userID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("property not found")
	}

	return nil
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
func join(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for _, s := range strs[1:] {
		result += sep + s
	}
	return result
}

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
