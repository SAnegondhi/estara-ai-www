// Package report provides handlers for investor report endpoints
package report

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/services/market/economics"
	"github.com/estara-ai/www/internal/services/pdf"
	"github.com/estara-ai/www/internal/services/reports"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles investor report HTTP requests
type Handler struct {
	db      *postgres.DB
	cfg     *config.Config
	service *reports.InvestorReportService
	logger  *slog.Logger
}

// NewHandler creates a new report handler
func NewHandler(db *postgres.DB, cfg *config.Config) *Handler {
	return &Handler{
		db:      db,
		cfg:     cfg,
		service: reports.NewInvestorReportService(db, cfg),
		logger:  slog.Default().With("component", "report_handler"),
	}
}

// NewHandlerWithEconomics creates a report handler with economic data integration (ADR-069)
func NewHandlerWithEconomics(db *postgres.DB, cfg *config.Config, econ economics.Provider) *Handler {
	return &Handler{
		db:      db,
		cfg:     cfg,
		service: reports.NewInvestorReportServiceWithEconomics(db, cfg, econ),
		logger:  slog.Default().With("component", "report_handler"),
	}
}

// ListResponse represents the response for listing reports
type ListResponse struct {
	Success bool                     `json:"success"`
	Reports []reports.InvestorReport `json:"reports"`
	Total   int64                    `json:"total"`
	Limit   int                      `json:"limit"`
	Offset  int                      `json:"offset"`
}

// List returns all investor reports for the authenticated user
// GET /api/investor-report/list
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	// Parse pagination params
	limit := 20
	offset := 0
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	reportList, total, err := h.service.List(ctx, claims.UserID, int32(limit), int32(offset))
	if err != nil {
		h.logger.Error("failed to list reports", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to retrieve reports")
		return
	}

	httputil.JSON(w, http.StatusOK, ListResponse{
		Success: true,
		Reports: reportList,
		Total:   total,
		Limit:   limit,
		Offset:  offset,
	})
}

// GenerateRequest represents the request to generate a new report
type GenerateRequest struct {
	PropertyID         string                 `json:"propertyId"`
	PropertyAddress    string                 `json:"propertyAddress"`
	Type               string                 `json:"type"`
	InvestmentCriteria map[string]interface{} `json:"investmentCriteria"`
	CacheKey           *string                `json:"cacheKey,omitempty"`
}

// Generate creates a new investor report
// POST /api/investor-report/generate
func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var req GenerateRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "Invalid request body")
		return
	}

	// Validate required fields
	if req.PropertyID == "" || req.PropertyAddress == "" {
		httputil.BadRequest(w, "propertyId and propertyAddress are required")
		return
	}

	// Map report type
	reportType := reports.ReportTypeComprehensive
	switch req.Type {
	case "SNAPSHOT":
		reportType = reports.ReportTypeSnapshot
	case "MIR":
		reportType = reports.ReportTypeMIR
	case "CIP":
		reportType = reports.ReportTypeCIP
	case "COMPREHENSIVE", "":
		reportType = reports.ReportTypeComprehensive
	default:
		httputil.BadRequest(w, "Invalid report type")
		return
	}

	input := reports.GenerateReportInput{
		PropertyID:         req.PropertyID,
		PropertyAddress:    req.PropertyAddress,
		Type:               reportType,
		InvestmentCriteria: req.InvestmentCriteria,
		CacheKey:           req.CacheKey,
	}

	report, err := h.service.Generate(ctx, claims.UserID, claims.Email, input)
	if err != nil {
		if errors.Is(err, reports.ErrInsufficientQuota) {
			httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
				"success": false,
				"error":   "Insufficient report quota",
				"message": "You have no remaining reports. Please purchase a report pack or upgrade your subscription.",
			})
			return
		}
		h.logger.Error("failed to generate report", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to generate report")
		return
	}

	httputil.JSON(w, http.StatusAccepted, map[string]interface{}{
		"success": true,
		"report":  report,
		"message": "Report generation queued",
	})
}

// GetStatus returns the status of a specific report
// GET /api/investor-report/status/{id}
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	reportID := chi.URLParam(r, "id")
	if reportID == "" {
		httputil.BadRequest(w, "Report ID is required")
		return
	}

	report, err := h.service.GetStatus(ctx, claims.UserID, reportID)
	if err != nil {
		if errors.Is(err, reports.ErrReportNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "Not found",
				"message": "Report not found",
			})
			return
		}
		h.logger.Error("failed to get report status", "error", err, "report_id", reportID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to get report status")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"id":      report.ID,
		"status":  report.Status,
	})
}

// Get returns a specific investor report
// GET /api/investor-report/{id}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	reportID := chi.URLParam(r, "id")
	if reportID == "" {
		httputil.BadRequest(w, "Report ID is required")
		return
	}

	report, err := h.service.Get(ctx, claims.UserID, reportID)
	if err != nil {
		if errors.Is(err, reports.ErrReportNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "Not found",
				"message": "Report not found",
			})
			return
		}
		h.logger.Error("failed to get report", "error", err, "report_id", reportID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to get report")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"report":  report,
	})
}

// Download returns a generated PDF for the report
// GET /api/investor-report/download/{id}
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	reportID := chi.URLParam(r, "id")
	if reportID == "" {
		httputil.BadRequest(w, "Report ID is required")
		return
	}

	report, err := h.service.Get(ctx, claims.UserID, reportID)
	if err != nil {
		if errors.Is(err, reports.ErrReportNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "Not found",
				"message": "Report not found",
			})
			return
		}
		h.logger.Error("failed to get report", "error", err, "report_id", reportID)
		httputil.Error(w, http.StatusInternalServerError, "failed to get report")
		return
	}

	if report.Status != reports.ReportStatusComplete {
		httputil.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Report not ready for download",
			"status":  report.Status,
		})
		return
	}

	metricsBytes, _ := json.Marshal(report.MetricsData)
	narrativeBytes, _ := json.Marshal(report.NarrativeData)

	email := claims.Email
	if report.Email != nil && *report.Email != "" {
		email = *report.Email
	}
	address := ""
	if report.PropertyAddress != nil {
		address = *report.PropertyAddress
	}

	pdfBytes, err := pdf.BuildInvestorReportPDF(pdf.InvestorReportPDFInput{
		ReportType:      string(report.Type),
		PropertyAddress: address,
		Email:           email,
		MetricsData:     metricsBytes,
		NarrativeData:   narrativeBytes,
		FullReport:      report.FullReport,
	})
	if err != nil {
		h.logger.Error("failed to generate report pdf", "error", err, "report_id", reportID)
		httputil.Error(w, http.StatusInternalServerError, "failed to generate report pdf")
		return
	}

	filename := "estara-ai-investor-report.pdf"
	if address != "" {
		filename = "estara-ai-investor-report-" + sanitizeFilename(address) + ".pdf"
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

func sanitizeFilename(value string) string {
	safe := strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(
		" ", "-",
		"/", "-",
		"\\", "-",
		":", "-",
		".", "-",
		",", "-",
	)
	safe = replacer.Replace(safe)
	safe = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, safe)
	return strings.Trim(safe, "-")
}
