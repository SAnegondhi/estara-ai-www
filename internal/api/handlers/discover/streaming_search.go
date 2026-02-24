package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/estara-ai/www/internal/api/handlers/util"
	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/internal/services/property/providers"
	"github.com/estara-ai/www/pkg/httputil"
)

const (
	// Streaming search settings
	streamingSearchTimeout = 3 * time.Minute
	// sseHeartbeatInterval: how often to send ": heartbeat" SSE comments to keep
	// proxies and load balancers from closing idle connections. Must use a
	// time.Ticker (fixed interval), NOT time.After (which resets on every select
	// iteration, meaning the keepalive never fires while results are streaming).
	sseHeartbeatInterval = 15 * time.Second
)

// StreamingSearchResponse represents the final SSE complete event
type StreamingSearchResponse struct {
	Total              int    `json:"total"`
	Enriched           int    `json:"enriched"`
	Failed             int    `json:"failed"`
	NoData             int    `json:"noData"`
	DiscoverySessionId string `json:"discoverySessionId,omitempty"`
}

// StreamingPropertyResult represents a single property in the stream
type StreamingPropertyResult struct {
	Index    int              `json:"index"`
	Property V2PropertyResult `json:"property"`
}

// StreamingProgress represents progress update
type StreamingProgress struct {
	Processed int `json:"processed"`
	Total     int `json:"total"`
}

// StreamingSearch handles GET /api/v2/discover/search/stream
// Returns fully enriched properties as they are processed via SSE
func (h *Handler) StreamingSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user ID from context for discovery session
	var userID string
	if user := middleware.GetUserFromContext(ctx); user != nil {
		userID = user.UserID
	}

	// Parse query parameters (same as SearchCriteria but from query string)
	location := r.URL.Query().Get("location")
	if location == "" {
		httputil.BadRequest(w, "location is required")
		return
	}

	// Parse location into city and state
	city, state := parseLocation(location)
	if city == "" || state == "" {
		httputil.BadRequest(w, "invalid location format, expected 'City, ST' or 'City, State'")
		return
	}

	// Check if orchestrator is configured
	if h.orchestrator == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "property search service not available")
		return
	}

	// Build search params from query string
	params := providers.SearchParams{
		City:  city,
		State: state,
	}

	if minPrice := r.URL.Query().Get("minPrice"); minPrice != "" {
		if val, err := strconv.Atoi(minPrice); err == nil {
			params.MinPrice = val
		}
	}
	if maxPrice := r.URL.Query().Get("maxPrice"); maxPrice != "" {
		if val, err := strconv.Atoi(maxPrice); err == nil {
			params.MaxPrice = val
		}
	}
	if minBeds := r.URL.Query().Get("minBeds"); minBeds != "" {
		if val, err := strconv.Atoi(minBeds); err == nil {
			params.MinBeds = val
		}
	}
	if propType := r.URL.Query().Get("propertyType"); propType != "" {
		params.PropertyType = providers.PropertyType(propType)
	}

	// Parse investment filters
	var minCapRate, maxCapRate, minGrossYield float64
	if mcr := r.URL.Query().Get("minCapRate"); mcr != "" {
		if val, err := strconv.ParseFloat(mcr, 64); err == nil {
			minCapRate = val
		}
	}
	if mcr := r.URL.Query().Get("maxCapRate"); mcr != "" {
		if val, err := strconv.ParseFloat(mcr, 64); err == nil {
			maxCapRate = val
		}
	}
	if mgy := r.URL.Query().Get("minGrossYield"); mgy != "" {
		if val, err := strconv.ParseFloat(mgy, 64); err == nil {
			minGrossYield = val
		}
	}

	// Parse suburb expansion radius
	var radiusMiles int
	if rm := r.URL.Query().Get("radiusMiles"); rm != "" {
		if val, err := strconv.Atoi(rm); err == nil && val > 0 {
			radiusMiles = val
		}
	}

	h.logger.Info("starting streaming search",
		"city", city,
		"state", state,
		"minPrice", params.MinPrice,
		"maxPrice", params.MaxPrice,
		"minBeds", params.MinBeds,
	)

	// Set up SSE headers BEFORE any response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		httputil.Error(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Disable the server's write deadline for this SSE connection. The default
	// WriteTimeout would close the connection mid-stream on long searches.
	// Heartbeats + context cancellation handle cleanup instead.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	// Start a fixed-interval heartbeat goroutine. A time.Ticker fires every N
	// seconds regardless of whether the main loop is busy processing results.
	// This is critical: time.After in a select resets on every loop iteration,
	// so it never fires while properties are actively streaming.
	heartbeatTicker := time.NewTicker(sseHeartbeatInterval)
	defer heartbeatTicker.Stop()

	// Create context with timeout for the streaming search
	streamCtx, cancel := context.WithTimeout(ctx, streamingSearchTimeout)
	defer cancel()

	// Create channel for streaming results (buffered to absorb bursts across parallel searches)
	results := make(chan finder.StreamingResult, 200)

	if radiusMiles > 0 {
		// Suburb expansion: fan out to origin + nearby cities, merge results as one logical search.
		// Deduplication by property ID prevents duplicates when suburbs overlap.
		cities := h.expandToNearby(ctx, city, state, radiusMiles)
		h.logger.Info("suburb expansion", "origin", city+","+state, "radius", radiusMiles, "cities", len(cities))
		go func() {
			defer close(results)
			var wg sync.WaitGroup
			var seenIDs sync.Map
			for _, loc := range cities {
				wg.Add(1)
				go func(c, s string) {
					defer wg.Done()
					cityParams := params
					cityParams.City = c
					cityParams.State = s
					cityResults := make(chan finder.StreamingResult, 50)
					go func() {
						defer close(cityResults)
						if err := h.orchestrator.SearchWithStreaming(streamCtx, cityParams, cityResults); err != nil {
							h.logger.Warn("suburb search failed", "city", c, "state", s, "error", err)
						}
					}()
					for result := range cityResults {
						if result.Type == "property" && result.Property != nil {
							// Deduplicate: only forward the first occurrence of each property ID
							if _, loaded := seenIDs.LoadOrStore(result.Property.ID, true); !loaded {
								results <- result
							}
						}
						// Progress/error/failed/nodata events from sub-searches are intentionally
						// suppressed to present a single unified result stream to the client.
					}
				}(loc.City, loc.State)
			}
			wg.Wait()
		}()
	} else {
		// Standard single-city search
		go func() {
			defer close(results)
			err := h.orchestrator.SearchWithStreaming(streamCtx, params, results)
			if err != nil {
				h.logger.Error("streaming search failed", "error", err)
				results <- finder.StreamingResult{
					Type:  "error",
					Error: err,
				}
			}
		}()
	}

	// Track properties for discovery session
	var properties []V2PropertyResult
	var totalCount, enrichedCount, failedCount, noDataCount int
	propertyIndex := 0

	logger := slog.Default().With("component", "streaming_search", "location", location)
	logger.Debug("SSE stream started")

	// Stream results to client
	for {
		select {
		case result, ok := <-results:
			if !ok {
				// Channel closed, search complete
				// Create discovery session with collected properties
				var discoverySessionId string
				if userID != "" && len(properties) > 0 {
					propertyIds := make([]string, len(properties))
					for i, p := range properties {
						propertyIds[i] = p.ID
					}

					// Serialize search criteria
					criteria := SearchCriteria{
						Location:      location,
						RadiusMiles:   radiusMiles,
						MinPrice:      params.MinPrice,
						MaxPrice:      params.MaxPrice,
						MinBeds:       params.MinBeds,
						MinCapRate:    minCapRate,
						MaxCapRate:    maxCapRate,
						MinGrossYield: minGrossYield,
					}
					if params.PropertyType != "" {
						criteria.PropertyTypes = []string{string(params.PropertyType)}
					}
					criteriaJSON, _ := json.Marshal(criteria)

					discoverySessionId = h.CreateDiscoverySessionForSearch(
						ctx,
						userID,
						criteriaJSON,
						location,
						len(properties),
						propertyIds,
						properties,
					)
				}

				// Log feature usage for customer support and chargeback defense
				if discoverySessionId != "" {
					_ = util.LogFeatureUsage(ctx, h.store, r, userID, "FEATURE_DISCOVER_SEARCH",
						fmt.Sprintf("Property search: %s", location),
						map[string]any{
							"location":           location,
							"discoverySessionId": discoverySessionId,
							"totalProperties":    totalCount,
							"enrichedProperties": enrichedCount,
							"failedProperties":   failedCount,
							"noDataProperties":   noDataCount,
							"filters": map[string]any{
								"minPrice":    params.MinPrice,
								"maxPrice":    params.MaxPrice,
								"minBeds":     params.MinBeds,
								"maxBeds":     params.MaxBeds,
								"minBaths":    params.MinBaths,
								"maxBaths":    params.MaxBaths,
								"minCapRate":  minCapRate,
								"propertyType": params.PropertyType,
							},
						},
					)
				}

				// Send complete event
				completeData := StreamingSearchResponse{
					Total:              totalCount,
					Enriched:           enrichedCount,
					Failed:             failedCount,
					NoData:             noDataCount,
					DiscoverySessionId: discoverySessionId,
				}
				h.sendStreamingSSEEvent(w, "complete", completeData)
				flusher.Flush()
				logger.Debug("SSE stream completed",
					"total", totalCount,
					"enriched", enrichedCount,
					"failed", failedCount,
					"noData", noDataCount,
				)
				return
			}

			// Handle different result types
			switch result.Type {
			case "property":
				if result.Property == nil {
					continue
				}

				p := result.Property

				// Skip properties with zero or negative price
				if p.Price <= 0 {
					continue
				}

				// Skip properties where enrichment failed (zero cap rate when price is valid)
				if p.EstimatedCapRate <= 0 && p.EstimatedRent <= 0 {
					continue
				}

				// Apply investment filters
				if minCapRate > 0 && p.EstimatedCapRate < minCapRate {
					continue
				}
				if maxCapRate > 0 && p.EstimatedCapRate > maxCapRate {
					continue
				}
				if minGrossYield > 0 && p.GrossYield < minGrossYield {
					continue
				}

				// Convert to V2PropertyResult
				prop := h.convertToV2PropertyResult(p)

				// Track for discovery session
				properties = append(properties, prop)
				enrichedCount++

				// Send property event
				propData := StreamingPropertyResult{
					Index:    propertyIndex,
					Property: prop,
				}
				h.sendStreamingSSEEvent(w, "property", propData)
				flusher.Flush()
				propertyIndex++

			case "progress":
				if result.Progress != nil {
					totalCount = result.Progress.Total
					progressData := StreamingProgress{
						Processed: result.Progress.Processed,
						Total:     result.Progress.Total,
					}
					h.sendStreamingSSEEvent(w, "progress", progressData)
					flusher.Flush()
				}

			case "failed":
				failedCount++

			case "nodata":
				noDataCount++

			case "error":
				if result.Error != nil {
					h.sendStreamingSSEEvent(w, "error", map[string]string{
						"message": result.Error.Error(),
					})
					flusher.Flush()
				}
			}

		case <-streamCtx.Done():
			logger.Warn("streaming search timed out")
			h.sendStreamingSSEEvent(w, "error", map[string]string{
				"message": "search timed out",
			})
			flusher.Flush()
			return

		case <-heartbeatTicker.C:
			// Fixed-interval heartbeat — keeps proxies from closing idle SSE connections
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// convertToV2PropertyResult converts a providers.Property to V2PropertyResult
func (h *Handler) convertToV2PropertyResult(p *providers.Property) V2PropertyResult {
	prop := V2PropertyResult{
		ID:               p.ID,
		Address:          p.Address,
		City:             p.City,
		State:            p.State,
		ZipCode:          p.ZipCode,
		Price:            p.Price,
		Beds:             p.Beds,
		Baths:            p.Baths,
		Sqft:             p.Sqft,
		PropertyType:     string(p.PropertyType),
		ListingSearchUrl: buildListingSearchUrl(p.Address, p.City, p.State, p.ZipCode),
		GoogleSearchUrl:  buildGoogleSearchUrl(p.Address, p.City, p.State, p.ZipCode),
		AgeCategory:      p.AgeCategory,
		MaintenanceRisk:  p.MaintenanceRisk,
		MaintenanceFactors: p.MaintenanceFactors,
	}

	// Enriched investment metrics
	if p.EstimatedRent > 0 {
		rent := p.EstimatedRent
		prop.EstimatedRent = &rent
	}
	if p.EstimatedCapRate > 0 {
		capRate := p.EstimatedCapRate
		prop.CapRate = &capRate
		capRange := &CapRateRange{
			Min: capRate - 0.5,
			Max: capRate + 0.5,
		}
		prop.CapRateRange = capRange
	}
	if p.GrossYield > 0 {
		grossYield := p.GrossYield
		prop.GrossYield = &grossYield
	}
	if p.CashOnCash != 0 {
		coc := p.CashOnCash
		prop.CashOnCash = &coc
	}
	if p.EstimatedCashFlow != 0 {
		cf := p.EstimatedCashFlow
		prop.MonthlyCashFlow = &cf
	}
	if p.NOI != 0 {
		noi := p.NOI
		prop.NOI = &noi
	}
	if p.PricePerSqft > 0 {
		pps := p.PricePerSqft
		prop.PricePerSqft = &pps
	}
	if p.InvestmentScore > 0 {
		score := p.InvestmentScore
		prop.InvestmentScore = &score
	}
	if p.PropertyAge > 0 {
		age := p.PropertyAge
		prop.PropertyAge = &age
	}

	// Other optional fields
	if p.YearBuilt > 0 {
		year := p.YearBuilt
		prop.YearBuilt = &year
	}
	if p.DaysOnMarket > 0 {
		dom := p.DaysOnMarket
		prop.DaysOnMarket = &dom
	}
	if len(p.Images) > 0 {
		img := p.Images[0]
		prop.ImageUrl = &img
	}
	if p.Latitude != 0 {
		lat := p.Latitude
		prop.Latitude = &lat
	}
	if p.Longitude != 0 {
		lng := p.Longitude
		prop.Longitude = &lng
	}

	return prop
}

// sendStreamingSSEEvent writes a server-sent event for streaming search
func (h *Handler) sendStreamingSSEEvent(w http.ResponseWriter, eventType string, data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(jsonData))
}
