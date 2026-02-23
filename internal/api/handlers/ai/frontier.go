package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/optimization"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/estara-ai/www/pkg/sse"
)

// ============================================================
// ADR-088 Phase 9: Interactive Workspace Backend Endpoints
// ============================================================

// FrontierAnalyzeRequest is the request body for POST /api/ai/frontier/analyze.
// It accepts pre-scored properties and runs the full frontier generation pipeline
// with real-time SSE progress updates.
type FrontierAnalyzeRequest struct {
	ScoredProperties []investment.ScoredProperty        `json:"scoredProperties" validate:"required,min=5"`
	Profile          investment.InvestorProfile          `json:"profile"          validate:"required"`
	Params           investment.InvestmentPlanningParams `json:"params"           validate:"required"`
}

// FrontierRecalculateRequest is the request body for POST /api/ai/frontier/recalculate.
// It accepts existing FrontierPoint configurations and re-runs phases 5-8 (Reinvestment,
// Monte Carlo, Scenarios, Verdict) with updated assumption overrides. This is the fast path
// for the interactive workspace sliders (debounced 500ms client-side).
type FrontierRecalculateRequest struct {
	FrontierPoints []investment.FrontierPoint         `json:"frontierPoints" validate:"required,min=1"`
	Profile        investment.InvestorProfile          `json:"profile"        validate:"required"`
	Params         investment.InvestmentPlanningParams `json:"params"         validate:"required"`
	Assumptions    investment.AssumptionOverrides      `json:"assumptions"`
}

// FrontierAnalyzeResponse is the SSE "complete" event payload.
type FrontierAnalyzeResponse struct {
	FrontierPoints []investment.FrontierPoint `json:"frontierPoints"`
	GeneratedAt    string                     `json:"generatedAt"`
	DurationMs     int64                      `json:"durationMs"`
}

// FrontierRecalculateResponse is the JSON response for /recalculate.
type FrontierRecalculateResponse struct {
	FrontierPoints []investment.FrontierPoint `json:"frontierPoints"`
	GeneratedAt    string                     `json:"generatedAt"`
	DurationMs     int64                      `json:"durationMs"`
	Assumptions    investment.AssumptionOverrides `json:"assumptions"`
}

// RunFrontierAnalysis streams frontier generation progress via SSE and sends the
// complete frontier points in the final "complete" event.
// POST /api/ai/frontier/analyze
func (h *Handler) RunFrontierAnalysis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if h.frontierOptimizer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "frontier optimizer not configured")
		return
	}

	var req FrontierAnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Set up SSE writer
	sseWriter, err := sse.NewWriter(w)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Start heartbeat to keep connection alive during long computation
	heartbeatDone := sseWriter.StartHeartbeat(sse.HeartbeatInterval)
	defer close(heartbeatDone)

	// Progress callback emits SSE progress events
	startTime := time.Now()
	progress := func(phase int, totalPhases int, message string) {
		pct := float64(phase) / float64(totalPhases) * 100
		_ = sseWriter.WriteEventJSON("progress", sse.ProgressEvent{
			Stage:    fmt.Sprintf("phase_%d", phase),
			Progress: pct,
			Message:  message,
		})
	}

	// Emit initial status
	_ = sseWriter.WriteEventJSON("status", map[string]interface{}{
		"status":  "running",
		"message": "Starting frontier analysis",
	})

	// Build per-config property cohorts from the scored properties
	budget := req.Profile.AvailableCapital
	dpPct := req.Params.DownPaymentPct
	if dpPct == 0 {
		dpPct = 0.20
	}
	mortgageRate := req.Params.MortgageRate
	if mortgageRate == 0 {
		mortgageRate = 0.075
	}
	cohorts := investment.BuildCohorts(req.ScoredProperties, req.Profile.Strategy, req.Profile.RiskTolerance, budget, mortgageRate, dpPct)
	if len(cohorts) == 0 {
		_ = sseWriter.WriteError("no affordable properties found for the given budget")
		return
	}

	// Run frontier generation
	frontierPoints, err := h.frontierOptimizer.GenerateFrontier(
		ctx,
		cohorts,
		req.Profile,
		req.Params,
		optimization.ProgressFunc(progress),
	)
	if err != nil {
		h.logger.Error("frontier analysis failed",
			"user_id", user.ID,
			"error", err,
		)
		_ = sseWriter.WriteError("frontier analysis failed: " + err.Error())
		return
	}

	// Send complete event with all frontier points
	duration := time.Since(startTime)
	_ = sseWriter.WriteComplete(FrontierAnalyzeResponse{
		FrontierPoints: frontierPoints,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		DurationMs:     duration.Milliseconds(),
	})

	h.logger.Info("frontier analysis complete",
		"user_id", user.ID,
		"configs", len(frontierPoints),
		"duration_ms", duration.Milliseconds(),
	)
}

// RecalculateFrontier re-runs financial projections (phases 5-8) for existing frontier
// configurations with updated assumption overrides. Returns JSON synchronously.
// POST /api/ai/frontier/recalculate
func (h *Handler) RecalculateFrontier(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if h.frontierOptimizer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "frontier optimizer not configured")
		return
	}

	var req FrontierRecalculateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	startTime := time.Now()

	updatedPoints, err := h.frontierOptimizer.Recalculate(
		ctx,
		req.FrontierPoints,
		req.Profile,
		req.Params,
		req.Assumptions,
	)
	if err != nil {
		h.logger.Error("frontier recalculation failed",
			"user_id", user.ID,
			"error", err,
		)
		httputil.Error(w, http.StatusInternalServerError, "recalculation failed: "+err.Error())
		return
	}

	duration := time.Since(startTime)
	h.logger.Info("frontier recalculation complete",
		"user_id", user.ID,
		"configs", len(updatedPoints),
		"duration_ms", duration.Milliseconds(),
		"mortgage_override", req.Assumptions.MortgageRate,
		"appreciation_override", req.Assumptions.AppreciationRate,
		"rent_growth_override", req.Assumptions.RentGrowthRate,
	)

	httputil.JSON(w, http.StatusOK, FrontierRecalculateResponse{
		FrontierPoints: updatedPoints,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		DurationMs:     duration.Milliseconds(),
		Assumptions:    req.Assumptions,
	})
}
