package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/pdf"
	"github.com/estara-ai/www/pkg/httputil"
)

// GenerateMarketAnalysisPDF handles POST /api/report/generate
func (h *Handler) GenerateMarketAnalysisPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	var req struct {
		RequestID string                 `json:"requestId,omitempty"`
		Data      map[string]interface{} `json:"data,omitempty"`
		Format    string                 `json:"format,omitempty"`
	}
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	var pdfData pdf.MarketAnalysisPDFData
	if req.RequestID != "" {
		q := queries.New(h.db.Main)
		cache, err := q.GetCacheByUserAndKey(ctx, queries.GetCacheByUserAndKeyParams{
			UserId: claims.UserID,
			Key:    req.RequestID,
		})
		if err != nil {
			httputil.Error(w, http.StatusNotFound, "report not found")
			return
		}

		pdfData.Location = cache.Location
		if cache.MetricsData != nil {
			_ = json.Unmarshal(cache.MetricsData, &pdfData.Metrics)
		}
		if cache.NarrativeData != nil {
			_ = json.Unmarshal(cache.NarrativeData, &pdfData.Narrative)
		}
		if cache.FullReport.Valid {
			pdfData.FullReport = cache.FullReport.String
		} else if cache.Content != "" {
			pdfData.FullReport = cache.Content
		}
		if cache.Metadata != nil {
			_ = json.Unmarshal(cache.Metadata, &pdfData.DataPoints)
		}
	} else if req.Data != nil {
		pdfData.Location = toString(req.Data["location"])
		pdfData.Metrics = toMap(req.Data["metrics"])
		pdfData.Narrative = toMap(req.Data["narrative"])
		pdfData.DataPoints = toMap(req.Data["dataPoints"])
		if fullReport, ok := req.Data["fullReport"].(string); ok {
			pdfData.FullReport = fullReport
		}
	} else {
		httputil.BadRequest(w, "missing requestId or data")
		return
	}

	pdfBytes, err := pdf.BuildMarketAnalysisPDF(ctx, pdfData)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to generate pdf")
		return
	}

	filename := "estara_ai_market_analysis.pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// GenerateInvestmentPlanPDF handles POST /api/report/investment-plan
func (h *Handler) GenerateInvestmentPlanPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	var req pdf.InvestmentPlanPDFRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Portfolio.SelectedProperties == nil {
		httputil.BadRequest(w, "missing portfolio data")
		return
	}

	req.User = &pdf.PDFUser{
		Email:     claims.Email,
		FirstName: "",
		LastName:  "",
	}

	pdfBytes, err := pdf.BuildInvestmentPlanPDF(ctx, req)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to generate pdf")
		return
	}

	filename := "estara_ai_investment_plan.pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// GeneratePortfolioProjectionsPDF handles POST /api/report/portfolio-projections
func (h *Handler) GeneratePortfolioProjectionsPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	var req pdf.InvestmentPlanPDFRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Portfolio.SelectedProperties == nil {
		httputil.BadRequest(w, "missing portfolio data")
		return
	}

	req.User = &pdf.PDFUser{
		Email: claims.Email,
	}

	pdfBytes, err := pdf.BuildInvestmentPlanPDF(ctx, req)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to generate pdf")
		return
	}

	filename := "estara_ai_portfolio_projections.pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// GeneratePortfolioProjectionsCSV handles POST /api/report/portfolio-projections/csv
func (h *Handler) GeneratePortfolioProjectionsCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	var req struct {
		Projections map[string]interface{} `json:"projections"`
	}
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Projections == nil {
		httputil.BadRequest(w, "missing projections")
		return
	}

	csvData, err := buildProjectionsCSV(req.Projections)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to build csv")
		return
	}

	filename := "portfolio_projections.csv"
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(csvData))
}

func buildProjectionsCSV(projections map[string]interface{}) (string, error) {
	if projections == nil {
		return "", errors.New("no projections")
	}
	builder := &strings.Builder{}
	builder.WriteString("scenario,year,portfolioValue,equity,loanBalance,cashFlow,capRate,cashOnCash\n")

	for scenarioKey, raw := range projections {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		yearly, ok := obj["yearlyMetrics"].([]interface{})
		if !ok {
			continue
		}
		for _, entry := range yearly {
			row, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			builder.WriteString(strings.Join([]string{
				scenarioKey,
				fmt.Sprintf("%v", row["year"]),
				fmt.Sprintf("%v", row["totalPropertyValue"]),
				fmt.Sprintf("%v", row["totalEquity"]),
				fmt.Sprintf("%v", row["totalLoanBalance"]),
				fmt.Sprintf("%v", row["cashFlowAfterTax"]),
				fmt.Sprintf("%v", row["capRate"]),
				fmt.Sprintf("%v", row["cashOnCashReturn"]),
			}, ","))
			builder.WriteString("\n")
		}
	}
	return builder.String(), nil
}

func toMap(val interface{}) map[string]interface{} {
	if val == nil {
		return nil
	}
	if m, ok := val.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func toString(val interface{}) string {
	switch v := val.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", val)
	}
}
