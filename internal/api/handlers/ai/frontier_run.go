package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/estara-ai/www/internal/api/middleware"
	queries "github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/investment/optimization"
	"github.com/estara-ai/www/internal/services/property/providers"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/estara-ai/www/pkg/sse"
)

// ============================================================
// ADR-088 Phase 12: Full Pipeline Endpoint — /api/ai/frontier/run
// ============================================================
// RunFrontierPipeline accepts investment criteria and orchestrates the full
// discover → score → frontier pipeline, streaming SSE progress events.

// FrontierRunRequest is the request body for POST /api/ai/frontier/run.
// It replaces the hardcoded demo data with real user-supplied criteria.
type FrontierRunRequest struct {
	// Locations to search (e.g. ["Austin, TX", "Denver, CO"])
	Locations []string `json:"locations"`
	// Budget: total capital available for down payments
	Budget int `json:"budget" validate:"required,min=10000"`
	// MortgageRate: annual rate as a decimal (e.g. 0.075 for 7.5%)
	MortgageRate float64 `json:"mortgageRate" validate:"required,min=0.01,max=0.3"`
	// DownPaymentPct: fraction of purchase price (e.g. 0.20 for 20%)
	DownPaymentPct float64 `json:"downPaymentPct" validate:"required,min=0.05,max=0.5"`
	// Strategy: "cash-flow", "appreciation", "balanced", "risk-adjusted"
	Strategy string `json:"strategy" validate:"required"`
	// RiskTolerance: "conservative", "moderate", "aggressive"
	RiskTolerance string `json:"riskTolerance" validate:"required"`
	// ProjectionYears: number of years for Monte Carlo / wealth projection
	ProjectionYears int `json:"projectionYears" validate:"min=1,max=30"`
	// ForceRescore: when true, bypasses the AI scoring cache and re-scores properties fresh.
	// Used by "Re-analyze" to get updated scores without triggering a new property search.
	ForceRescore bool `json:"forceRescore,omitempty"`
	// DiscoverySessionID: when set, use properties from this Discovery session instead of auto-discover.
	DiscoverySessionID string `json:"discoverySessionId,omitempty"`
	// IncludePortfolio: when true, factor the user's existing portfolio into analysis.
	IncludePortfolio bool `json:"includePortfolio,omitempty"`
	// ExistingPortfolio: the user's current portfolio data (populated by frontend).
	ExistingPortfolio *investment.ExistingPortfolio `json:"existingPortfolio,omitempty"`
}

// RunFrontierPipeline is the Phase 12 full-pipeline endpoint.
// It streams SSE progress events across three stages:
//
//	Phase 1 (0-30%):  Property discovery (parallel per location)
//	Phase 2 (30-60%): AI scoring
//	Phase 3 (60-100%): Frontier generation
//
// POST /api/ai/frontier/run
func (h *Handler) RunFrontierPipeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if h.investmentOptimizer == nil || h.propertyFinder == nil || h.frontierOptimizer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "frontier pipeline not configured")
		return
	}

	var req FrontierRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Validate property source: either locations or a discovery session ID must be provided
	if req.DiscoverySessionID == "" && len(req.Locations) == 0 {
		httputil.BadRequest(w, "either locations or discoverySessionId is required")
		return
	}

	// Apply defaults
	if req.ProjectionYears == 0 {
		req.ProjectionYears = 10
	}

	// Set up SSE
	sseWriter, err := sse.NewWriter(w)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	heartbeatDone := sseWriter.StartHeartbeat(sse.HeartbeatInterval)
	defer close(heartbeatDone)

	startTime := time.Now()

	emitProgress := func(pct float64, message string) {
		_ = sseWriter.WriteEventJSON("progress", sse.ProgressEvent{
			Progress: pct,
			Message:  message,
		})
	}
	emitStatus := func(status, message string) {
		_ = sseWriter.WriteEventJSON("status", map[string]string{
			"status":  status,
			"message": message,
		})
	}

	emitStatus("running", "Starting frontier pipeline")

	// ----------------------------------------------------------------
	// Phase 1: Property Discovery (0–30%)
	// ----------------------------------------------------------------

	var allProperties []investment.Property
	var discoveryLocations []string // locations derived from property source

	if req.DiscoverySessionID != "" {
		// ADR-089: Use properties from a Discovery session — skip auto-discover
		emitProgress(5, "Loading properties from Discovery session")

		session, sessionErr := h.store.Q().GetDiscoverySessionByUser(ctx, queries.GetDiscoverySessionByUserParams{
			ID:     req.DiscoverySessionID,
			UserId: user.UserID,
		})
		if sessionErr != nil {
			_ = sseWriter.WriteEventJSON("error", map[string]string{
				"error":   "session_not_found",
				"message": "Discovery session not found or access denied",
			})
			return
		}

		sessionProps, propsErr := h.store.Q().ListSessionProperties(ctx, session.ID)
		if propsErr != nil {
			_ = sseWriter.WriteEventJSON("error", map[string]string{
				"error":   "session_load_failed",
				"message": "Failed to load session properties: " + propsErr.Error(),
			})
			return
		}

		allProperties = make([]investment.Property, 0, len(sessionProps))
		locationSet := make(map[string]struct{})
		for _, sp := range sessionProps {
			loc := sp.City + ", " + sp.State
			locationSet[loc] = struct{}{}

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

			allProperties = append(allProperties, investment.Property{
				ID:            sp.ListingId,
				Address:       sp.Address,
				City:          sp.City,
				State:         sp.State,
				ZipCode:       sp.ZipCode.String,
				Price:         int(sp.Price),
				Beds:          int(sp.Beds),
				EstimatedRent: estimatedRent,
				YearBuilt:     int(sp.YearBuilt.Int32),
				PropertyType:  sp.PropertyType.String,
				ListingURL:    sp.ListingSearchUrl.String,
				ImageURL:      sp.ImageUrl.String,
				DaysOnMarket:  int(sp.DaysOnMarket.Int32),
				Latitude:      lat,
				Longitude:     lng,
			})
		}

		for loc := range locationSet {
			discoveryLocations = append(discoveryLocations, loc)
		}

		emitProgress(30, fmt.Sprintf("Loaded %d properties from Discovery session", len(allProperties)))
	} else {
		// Auto-discover: parallel search across all requested locations
		emitProgress(0, fmt.Sprintf("Searching %d location(s)", len(req.Locations)))
		discoveryLocations = req.Locations

		maxPrice := int(float64(req.Budget) / req.DownPaymentPct)

		type searchResult struct {
			properties []investment.Property
			err        error
		}

		resultChan := make(chan searchResult, len(req.Locations))
		sem := make(chan struct{}, 5)
		var wg sync.WaitGroup

		for i, loc := range req.Locations {
			wg.Add(1)
			go func(idx int, location string) {
				defer wg.Done()

				// ADR-088 Phase 13: Progressive disclosure — emit "searching" before acquiring sem
				_ = sseWriter.WriteEventJSON("location_status", map[string]interface{}{
					"location": location,
					"status":   "searching",
					"count":    0,
				})

				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					resultChan <- searchResult{err: ctx.Err()}
					return
				}

				pct := float64(idx)/float64(len(req.Locations)) * 30
				emitProgress(pct, fmt.Sprintf("Searching %s", location))

				parts := strings.SplitN(location, ",", 2)
				city := strings.TrimSpace(parts[0])
				state := ""
				if len(parts) > 1 {
					state = strings.TrimSpace(parts[1])
				}

				resp, searchErr := h.propertyFinder.Search(ctx, providers.SearchParams{
					City:     city,
					State:    state,
					MaxPrice: maxPrice,
					Limit:    50,
				})
				if searchErr != nil {
					h.logger.Warn("search failed for location",
						"location", location,
						"error", searchErr,
					)
					_ = sseWriter.WriteEventJSON("location_status", map[string]interface{}{
						"location": location,
						"status":   "found",
						"count":    0,
					})
					resultChan <- searchResult{err: searchErr}
					return
				}

				props := make([]investment.Property, 0, len(resp.Properties))
				for _, r := range resp.Properties {
					imageURL := ""
					if len(r.Images) > 0 {
						imageURL = r.Images[0]
					}
					props = append(props, investment.Property{
						ID:            r.ID,
						Address:       r.Address,
						City:          r.City,
						State:         r.State,
						ZipCode:       r.ZipCode,
						Price:         r.Price,
						Beds:          r.Beds,
						Baths:         r.Baths,
						Sqft:          r.Sqft,
						EstimatedRent: r.EstimatedRent,
						YearBuilt:     r.YearBuilt,
						PropertyType:  string(r.PropertyType),
						ListingURL:    r.ListingURL,
						ImageURL:      imageURL,
						DaysOnMarket:  r.DaysOnMarket,
						Provider:      r.ProviderName,
						Latitude:      r.Latitude,
						Longitude:     r.Longitude,
					})
				}

				emitProgress(float64(idx+1)/float64(len(req.Locations))*30,
					fmt.Sprintf("Found %d properties in %s", len(props), location))

				// ADR-088 Phase 13: Progressive disclosure — emit "found" after search completes
				_ = sseWriter.WriteEventJSON("location_status", map[string]interface{}{
					"location": location,
					"status":   "found",
					"count":    len(props),
				})

				resultChan <- searchResult{properties: props}
			}(i, loc)
		}

		go func() {
			wg.Wait()
			close(resultChan)
		}()

		allProperties = make([]investment.Property, 0)
		for res := range resultChan {
			if res.err == nil {
				allProperties = append(allProperties, res.properties...)
			}
		}

		emitProgress(30, fmt.Sprintf("Found %d properties across all locations", len(allProperties)))
	}

	if len(allProperties) == 0 {
		_ = sseWriter.WriteEventJSON("error", map[string]string{
			"error":   "no_properties_found",
			"message": "No properties found for the given criteria. Try different locations or a higher budget.",
		})
		return
	}

	// ----------------------------------------------------------------
	// Phase 2: AI Scoring (30–60%) — with granular progress ticker
	// ----------------------------------------------------------------
	emitProgress(30, "Scoring properties with AI")

	profile := investment.InvestorProfile{
		RiskTolerance:    investment.RiskTolerance(req.RiskTolerance),
		Strategy:         investment.InvestmentStrategy(req.Strategy),
		AvailableCapital: req.Budget,
	}

	// Ticker emits incremental progress while ScoreProperties blocks (no callback available).
	scoringDone := make(chan struct{})
	var scoringOnce sync.Once
	stopScoring := func() { scoringOnce.Do(func() { close(scoringDone) }) }
	defer stopScoring()
	go func() {
		scoringMsgs := []string{
			"Scoring properties with AI",
			"Analyzing cash flow potential",
			"Computing risk-adjusted metrics",
			"Building investment scores",
		}
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		pct := 33.0
		msgIdx := 0
		for {
			select {
			case <-scoringDone:
				return
			case <-ticker.C:
				if pct < 58 {
					pct += 4
				}
				emitProgress(pct, scoringMsgs[msgIdx%len(scoringMsgs)])
				msgIdx++
			}
		}
	}()

	scoredProperties, err := h.investmentOptimizer.ScoreProperties(
		ctx,
		allProperties,
		profile,
		req.ExistingPortfolio, // ADR-089: pass portfolio context if provided
		req.ForceRescore,
	)
	stopScoring() // stop ticker before next emitProgress

	if err != nil {
		h.logger.Error("property scoring failed", "error", err, "userId", user.ID)
		_ = sseWriter.WriteEventJSON("error", map[string]string{
			"error":   "scoring_failed",
			"message": "Property scoring failed: " + err.Error(),
		})
		return
	}

	emitProgress(60, fmt.Sprintf("Scored %d properties", len(scoredProperties)))

	// ----------------------------------------------------------------
	// Phase 3: Frontier Generation (60–100%) — with granular progress ticker
	// ----------------------------------------------------------------
	emitProgress(60, "Running frontier analysis")

	// Ticker covers the gap when the optimizer doesn't call ProgressFunc frequently.
	frontierDone := make(chan struct{})
	var frontierOnce sync.Once
	stopFrontier := func() { frontierOnce.Do(func() { close(frontierDone) }) }
	defer stopFrontier()
	go func() {
		frontierMsgs := []string{
			"Running frontier analysis",
			"Computing efficient portfolios",
			"Optimizing risk-return tradeoffs",
			"Evaluating portfolio configurations",
		}
		ticker := time.NewTicker(2000 * time.Millisecond)
		defer ticker.Stop()
		pct := 63.0
		msgIdx := 0
		for {
			select {
			case <-frontierDone:
				return
			case <-ticker.C:
				if pct < 96 {
					pct += 5
				}
				emitProgress(pct, frontierMsgs[msgIdx%len(frontierMsgs)])
				msgIdx++
			}
		}
	}()

	params := investment.InvestmentPlanningParams{
		Locations:         discoveryLocations, // ADR-089: use locations from property source
		Budget:            req.Budget,
		DownPaymentPct:    req.DownPaymentPct,
		Strategy:          investment.InvestmentStrategy(req.Strategy),
		RiskTolerance:     investment.RiskTolerance(req.RiskTolerance),
		MaxProperties:     10,
		ExistingPortfolio: req.ExistingPortfolio, // ADR-089: portfolio context
	}

	// Wrap frontier progress (60–100%)
	frontierProgress := func(phase int, totalPhases int, message string) {
		base := 60.0
		pct := base + (float64(phase)/float64(totalPhases))*40
		_ = sseWriter.WriteEventJSON("progress", sse.ProgressEvent{
			Stage:    fmt.Sprintf("frontier_phase_%d", phase),
			Progress: pct,
			Message:  message,
		})
	}

	frontierPoints, err := h.frontierOptimizer.GenerateFrontier(
		ctx,
		scoredProperties,
		profile,
		params,
		optimization.ProgressFunc(frontierProgress),
	)
	stopFrontier() // stop ticker before final event

	if err != nil {
		h.logger.Error("frontier generation failed", "error", err, "userId", user.ID)
		_ = sseWriter.WriteEventJSON("error", map[string]string{
			"error":   "frontier_failed",
			"message": "Frontier generation failed: " + err.Error(),
		})
		return
	}

	// ----------------------------------------------------------------
	// Complete
	// ----------------------------------------------------------------
	_ = sseWriter.WriteEventJSON("complete", FrontierAnalyzeResponse{
		FrontierPoints: frontierPoints,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		DurationMs:     time.Since(startTime).Milliseconds(),
	})
}
