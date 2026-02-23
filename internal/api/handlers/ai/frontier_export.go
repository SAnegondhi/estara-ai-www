package ai

// ADR-088 Phase 10: Frontier PDF Export Handler
//
// POST /api/ai/frontier/export-pdf
// Accepts the current frontier points, selected config index, and planning params.
// Renders a 10-section "Efficient Frontier Decision Memo" PDF and streams it
// as application/pdf with a timestamped filename.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/pdf"
	"github.com/estara-ai/www/pkg/httputil"
)

// FrontierExportPDFRequest is the request body for POST /api/ai/frontier/export-pdf.
type FrontierExportPDFRequest struct {
	FrontierPoints      []investment.FrontierPoint         `json:"frontierPoints"      validate:"required,min=1"`
	SelectedConfigIndex int                                `json:"selectedConfigIndex"`
	Profile             investment.InvestorProfile          `json:"profile"             validate:"required"`
	Params              investment.InvestmentPlanningParams `json:"params"              validate:"required"`
}

// ExportFrontierPDF renders and streams the decision memo PDF.
// POST /api/ai/frontier/export-pdf
func (h *Handler) ExportFrontierPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req FrontierExportPDFRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Build PDF
	builder := pdf.NewFrontierPDFBuilder(h.logger)
	pdfBytes, err := builder.Build(ctx, pdf.FrontierPDFRequest{
		FrontierPoints:      req.FrontierPoints,
		SelectedConfigIndex: req.SelectedConfigIndex,
		Profile:             req.Profile,
		Params:              req.Params,
	})
	if err != nil {
		h.logger.Error("frontier PDF generation failed",
			"user_id", user.UserID,
			"error", err,
		)
		httputil.Error(w, http.StatusInternalServerError, "PDF generation failed: "+err.Error())
		return
	}

	// Timestamped filename
	ts := time.Now().Format("2006-01-02")
	filename := fmt.Sprintf("frontier-decision-memo-%s-config%d.pdf",
		ts, req.SelectedConfigIndex+1)

	h.logger.Info("frontier PDF exported",
		"user_id", user.UserID,
		"configs", len(req.FrontierPoints),
		"selected", req.SelectedConfigIndex,
		"bytes", len(pdfBytes),
	)

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfBytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}
