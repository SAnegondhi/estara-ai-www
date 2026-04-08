package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/api/middleware"
	queries "github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/optimization"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/estara-ai/www/pkg/sse"
	"github.com/google/uuid"
)

// ============================================================
// ADR-088 Phase 9: Interactive Workspace Backend Endpoints
// ============================================================

// FrontierAnalyzeRequest is the request body for POST /api/ai/frontier/analyze.
// It accepts pre-scored properties and runs the full frontier generation pipeline
// with real-time SSE progress updates.
type FrontierAnalyzeRequest struct {
	ScoredProperties []investment.ScoredProperty        `json:"scoredProperties"`
	Profile          investment.InvestorProfile          `json:"profile"          validate:"required"`
	Params           investment.InvestmentPlanningParams `json:"params"           validate:"required"`
	// ADR-091: Pinned cohort semantics for user-selected property sources.
	// PinnedPropertyIDs holds the IDs of evaluated / memo properties that must appear verbatim
	// as Config 0 ("Your Selection"), bypassing FilterAffordable and ranking algorithms.
	PinnedPropertyIDs []string `json:"pinnedPropertyIds,omitempty"`
	// DiscoverySeedSessionID: when set, the backend fetches this session's property pool and
	// appends it (de-duplicated) to ScoredProperties so Configs 1/2 can draw from a wider pool
	// than just the user's selected evaluations.
	DiscoverySeedSessionID string `json:"discoverySeedSessionId,omitempty"`
	// Filters: optional quality gates applied to pool-only (non-pinned) properties before
	// BuildCohorts. Used by the Discovery Session source when the user enables filter controls.
	// ADR-091 Phase 5.
	Filters *investment.PropertyFilters `json:"filters,omitempty"`
	// ADR-103: PipelineDealID — when set, the backend fetches this deal's pipeline properties
	// and uses them as the scored property pool (all treated as pinned, bypassing quality filters).
	PipelineDealID string `json:"pipelineDealId,omitempty"`
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
	// ADR-103: if a pipeline deal ID is specified, fetch its properties and use them as the
	// scored property pool (all treated as pinned — bypasses quality filters, same semantics
	// as pinned evaluations).
	if req.PipelineDealID != "" && h.store != nil {
		dealID, err := uuid.Parse(req.PipelineDealID)
		if err != nil {
			httputil.BadRequest(w, "invalid pipelineDealId")
			return
		}
		mapped, skipped := h.mapPipelinePropertiesToScored(ctx, dealID, user.UserID)
		if skipped > 0 {
			h.logger.Warn("pipeline properties skipped (missing city/state or price)",
				"pipeline_deal_id", req.PipelineDealID,
				"skipped", skipped,
				"included", len(mapped),
			)
		}
		if len(mapped) == 0 {
			_ = sseWriter.WriteError("no valid pipeline properties found (city, state, and price are required)")
			return
		}

		// Discover comparable peers (non-pinned) so Frontier can build Configs 1 & 2.
		// Peers are comparable in type + price (±40%). Commercial types search LoopNet/Crexi
		// via Claude; residential types use the normal HasData/BrightData priority chain.
		peers := h.discoverPipelinePeers(ctx, mapped)
		if len(peers) > 0 {
			h.logger.Info("pipeline peer discovery",
				"pipeline_deal_id", req.PipelineDealID,
				"peers_found", len(peers),
			)
		}

		// Peers first (non-pinned pool for Configs 1/2), then deal properties (pinned below).
		allScored := append(peers, mapped...)
		req.ScoredProperties = allScored

		// All pipeline deal properties are pinned — they form Config 0 verbatim.
		for _, sp := range mapped {
			req.PinnedPropertyIDs = append(req.PinnedPropertyIDs, sp.Property.ID)
		}
	}

	// ADR-091: build pinnedIDs set from request (evaluations/memos path).
	pinnedIDs := make(map[string]bool, len(req.PinnedPropertyIDs))
	for _, id := range req.PinnedPropertyIDs {
		pinnedIDs[id] = true
	}

	// Require at least one property (pipeline or pre-scored).
	if len(req.ScoredProperties) == 0 {
		_ = sseWriter.WriteError("scoredProperties is required (provide at least one property or a pipelineDealId)")
		return
	}

	// ADR-091: if a discovery seed session is specified, fetch its full property pool and
	// merge (de-duplicated) into the scored properties so Configs 1/2 can draw from a wider
	// pool than just the user's pinned evaluations.
	allProps := req.ScoredProperties
	if req.DiscoverySeedSessionID != "" && h.store != nil {
		allProps = h.mergeDiscoverySeedPool(ctx, req.DiscoverySeedSessionID, user.UserID, req.ScoredProperties)
	}

	// ADR-091 Phase 5: apply optional quality filters to pool-only (non-pinned) properties
	// when the caller supplies filters (Discovery Session source with filter controls set).
	// Pinned properties (user's explicit evaluation selection) are never filtered.
	if req.Filters != nil {
		filtered := make([]investment.ScoredProperty, 0, len(allProps))
		poolOnly := make([]investment.ScoredProperty, 0, len(allProps))
		for _, p := range allProps {
			if pinnedIDs[p.Property.ID] {
				filtered = append(filtered, p)
			} else {
				poolOnly = append(poolOnly, p)
			}
		}
		props := make([]investment.Property, len(poolOnly))
		for i, sp := range poolOnly {
			props[i] = sp.Property
		}
		filteredProps, _ := investment.ApplyPropertyFilters(props, *req.Filters)
		filteredSet := make(map[string]bool, len(filteredProps))
		for _, p := range filteredProps {
			filteredSet[p.ID] = true
		}
		for _, sp := range poolOnly {
			if filteredSet[sp.Property.ID] {
				filtered = append(filtered, sp)
			}
		}
		allProps = filtered
	}

	cohorts := investment.BuildCohorts(allProps, pinnedIDs, req.Profile.Strategy, req.Profile.RiskTolerance, budget, mortgageRate, dpPct)
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

// mergeDiscoverySeedPool fetches the full property list from a discovery session and merges
// it (de-duplicated by property ID) into existing to produce a wider candidate pool.
// ADR-091: used by RunFrontierAnalysis when DiscoverySeedSessionID is set, so that Configs 1/2
// can draw from properties beyond the user's pinned evaluations.
// On any error the original existing slice is returned unchanged (graceful degradation).
func (h *Handler) mergeDiscoverySeedPool(ctx context.Context, sessionID, userID string, existing []investment.ScoredProperty) []investment.ScoredProperty {
	session, err := h.store.Q().GetDiscoverySessionByUser(ctx, queries.GetDiscoverySessionByUserParams{
		ID:     sessionID,
		UserId: userID,
	})
	if err != nil {
		h.logger.Warn("seed session not found or access denied",
			"sessionId", sessionID,
			"userId", userID,
			"error", err,
		)
		return existing
	}

	sessionProps, err := h.store.Q().ListSessionProperties(ctx, session.ID)
	if err != nil || len(sessionProps) == 0 {
		return existing
	}

	// Build a set of property IDs already in existing to avoid duplicates.
	seen := make(map[string]bool, len(existing))
	for _, sp := range existing {
		seen[sp.Property.ID] = true
	}

	merged := make([]investment.ScoredProperty, len(existing), len(existing)+len(sessionProps))
	copy(merged, existing)

	for _, sp := range sessionProps {
		if seen[sp.ListingId] {
			continue
		}
		seen[sp.ListingId] = true

		var lat, lng float64
		if sp.Latitude.Valid {
			_ = sp.Latitude.Scan(&lat)
		}
		if sp.Longitude.Valid {
			_ = sp.Longitude.Scan(&lng)
		}

		var estimatedRent int
		if sp.EstimatedRent.Valid {
			estimatedRent = int(sp.EstimatedRent.Int32)
		}

		merged = append(merged, investment.ScoredProperty{
			Property: investment.Property{
				ID:           sp.ListingId,
				Address:      sp.Address,
				City:         sp.City,
				State:        sp.State,
				ZipCode:      sp.ZipCode.String,
				Price:        int(sp.Price),
				Beds:         int(sp.Beds),
				EstimatedRent: estimatedRent,
				YearBuilt:    int(sp.YearBuilt.Int32),
				PropertyType: sp.PropertyType.String,
				ImageURL:     sp.ImageUrl.String,
				Latitude:     lat,
				Longitude:    lng,
			},
			OverallScore: 50, // pool-only; will be re-scored by ScoreProperties in the caller
		})
	}

	h.logger.Info("merged discovery seed pool",
		"sessionId", sessionID,
		"existingCount", len(existing),
		"poolCount", len(sessionProps),
		"mergedCount", len(merged),
	)
	return merged
}
