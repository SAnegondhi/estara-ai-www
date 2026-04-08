package ai

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/jackc/pgx/v5/pgtype"
)

// PreloadRequest defines the request body for intelligent preloading
type PreloadRequest struct {
	Strategy string `json:"strategy"` // "geo", "history", "recency"

	// Geo strategy parameters
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
	Radius    *int     `json:"radius,omitempty"` // km
	City      string   `json:"city,omitempty"`
	State     string   `json:"state,omitempty"`

	// History strategy parameters
	Locations []string `json:"locations,omitempty"`

	// Tier limits
	Tier1Limit int `json:"tier1Limit,omitempty"` // default: 10
	Tier2Limit int `json:"tier2Limit,omitempty"` // default: 20
	Tier3Limit int `json:"tier3Limit,omitempty"` // default: 20
}

// AnalysisItem represents a lightweight analysis report item for preloading
type AnalysisItem struct {
	ID                 string  `json:"id"`
	Location           string  `json:"location"`
	LocationNormalized string  `json:"locationNormalized"`
	CacheKey           string  `json:"cacheKey"`
	Status             string  `json:"status"`
	GeneratedAt        *string `json:"generatedAt"`
	CreatedAt          string  `json:"createdAt"`
	AccessedCount      int32   `json:"accessedCount"`
	DistanceKm         *float64 `json:"distanceKm,omitempty"` // Only for geo strategy
	Preview            string   `json:"preview"`              // Executive summary preview
	HasReport          bool     `json:"hasReport"`            // Whether full report is available
}

// PreloadResponse defines the response for intelligent preloading
type PreloadResponse struct {
	Success       bool           `json:"success"`
	AnalysisItems []AnalysisItem `json:"analysisItems"`
	Strategy      string         `json:"strategy"`
	TotalLoaded   int            `json:"totalLoaded"`
	Tiers         map[string]int `json:"tiers"` // "tier1": 10, "tier2": 20, etc.
}

// PreloadReports handles intelligent report preloading
// POST /api/ai/analysis/preload
func (h *Handler) PreloadReports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req PreloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "Invalid request body")
		return
	}

	// Set defaults
	if req.Tier1Limit == 0 {
		req.Tier1Limit = 10
	}
	if req.Tier2Limit == 0 {
		req.Tier2Limit = 20
	}
	if req.Tier3Limit == 0 {
		req.Tier3Limit = 20
	}

	var analysisItems []AnalysisItem
	tiers := make(map[string]int)

	h.logger.Info("preloading reports", "strategy", req.Strategy)

	switch req.Strategy {
	case "geo":
		// Tier 1: User's city (exact location match)
		if req.City != "" && req.State != "" {
			location := req.City + ", " + req.State
			tier1Reports, err := h.store.Q().ListReportsByLocation(ctx, queries.ListReportsByLocationParams{
				Location: location,
				Limit:    int32(req.Tier1Limit),
			})
			if err == nil {
				for _, report := range tier1Reports {
					analysisItems = append(analysisItems, convertToAnalysisItemBasic(report))
				}
				tiers["tier1"] = len(tier1Reports)
				h.logger.Debug("tier1 loaded", "count", len(tier1Reports), "location", req.City+", "+req.State)
			}
		}

		// Tier 2: Nearby cities (200km radius)
		if req.Latitude != nil && req.Longitude != nil && req.Radius != nil {
			radius := *req.Radius
			if radius == 0 {
				radius = 200 // default 200km
			}

			tier2Reports, err := h.store.Q().ListReportsByProximity(ctx, queries.ListReportsByProximityParams{
				Latitude:  *req.Latitude,
				Longitude: *req.Longitude,
				RadiusKm:  pgtype.Float8{Float64: float64(radius), Valid: true},
				Limit:     int32(req.Tier2Limit),
				Offset:    0,
			})
			if err == nil {
				for _, report := range tier2Reports {
					// Skip if already in tier1
					if !containsReport(analysisItems, report.CacheKey) {
						item := convertToAnalysisItemWithDistanceBasic(report)
						analysisItems = append(analysisItems, item)
					}
				}
				tier2Count := len(tier2Reports)
				tiers["tier2"] = tier2Count
				h.logger.Debug("tier2 loaded", "count", tier2Count, "radius", radius)
			}
		}

		// Tier 3: Popular nationwide (by access count)
		tier3Reports, err := h.store.Q().ListPopularReports(ctx, queries.ListPopularReportsParams{
			Limit:  int32(req.Tier3Limit),
			Offset: 0,
		})
		if err == nil {
			for _, report := range tier3Reports {
				// Skip if already in tier1 or tier2
				if !containsReport(analysisItems, report.CacheKey) {
					analysisItems = append(analysisItems, convertToAnalysisItemBasic(report))
				}
			}
			tier3Count := len(tier3Reports)
			tiers["tier3"] = tier3Count
			h.logger.Debug("tier3 loaded", "count", tier3Count)
		}

	case "history":
		// Tier 1: Top viewed locations (user's history)
		tier1Count := 0
		for _, location := range req.Locations {
			if tier1Count >= req.Tier1Limit {
				break
			}
			reports, err := h.store.Q().ListReportsByLocation(ctx, queries.ListReportsByLocationParams{
				Location: location,
				Limit:    3, // 3 per location
			})
			if err == nil {
				for _, report := range reports {
					if tier1Count >= req.Tier1Limit {
						break
					}
					analysisItems = append(analysisItems, convertToAnalysisItemBasic(report))
					tier1Count++
				}
			}
		}
		tiers["tier1"] = tier1Count

		// Tier 2: Popular nationwide
		tier2Reports, err := h.store.Q().ListPopularReports(ctx, queries.ListPopularReportsParams{
			Limit:  int32(req.Tier2Limit),
			Offset: 0,
		})
		if err == nil {
			for _, report := range tier2Reports {
				if !containsReport(analysisItems, report.CacheKey) {
					analysisItems = append(analysisItems, convertToAnalysisItemBasic(report))
				}
			}
			tiers["tier2"] = len(tier2Reports)
		}

	case "recency":
		// All chronological (most recent)
		recentReports, err := h.store.Q().ListRecentReports(ctx, queries.ListRecentReportsParams{
			Limit:  int32(req.Tier1Limit + req.Tier2Limit + req.Tier3Limit),
			Offset: 0,
		})
		if err == nil {
			for _, report := range recentReports {
				analysisItems = append(analysisItems, h.convertToAnalysisItem(ctx, report))
			}
			tiers["all"] = len(recentReports)
		}

	default:
		httputil.BadRequest(w, "Invalid strategy. Must be 'geo', 'history', or 'recency'")
		return
	}

	// Batch fetch cache data for all items to populate preview/hasReport.
	// analysis_cache uses a different key format ("analysis_{loc}") than market_analysis_reports
	// ("market_analysis:{loc}"). Derive the correct key from location.
	if len(analysisItems) > 0 {
		cacheKeys := make([]string, len(analysisItems))
		for i, item := range analysisItems {
			cacheKeys[i] = locationToAnalysisCacheKey(item.Location)
		}

		cachedItems, err := h.store.Q().BatchGetAnalysisCacheByKeys(ctx, cacheKeys)
		if err == nil {
			cacheMap := make(map[string]queries.BatchGetAnalysisCacheByKeysRow)
			for _, cached := range cachedItems {
				cacheMap[cached.Key] = cached
			}

			// Populate preview and hasReport for each item
			for i := range analysisItems {
				if cachedData, found := cacheMap[locationToAnalysisCacheKey(analysisItems[i].Location)]; found {
					content := string(cachedData.Content)
					fullReport := ""
					if cachedData.FullReport.Valid {
						fullReport = cachedData.FullReport.String
					}

					reportContent := fullReport
					if reportContent == "" {
						reportContent = content
					}
					analysisItems[i].HasReport = reportContent != ""
					analysisItems[i].Preview = extractExecutiveSummary(reportContent)
				}
			}
		}
	}

	h.logger.Info("preload complete",
		"strategy", req.Strategy,
		"totalLoaded", len(analysisItems),
		"tiers", tiers,
	)

	httputil.JSON(w, http.StatusOK, PreloadResponse{
		Success:       true,
		AnalysisItems: analysisItems,
		Strategy:      req.Strategy,
		TotalLoaded:   len(analysisItems),
		Tiers:         tiers,
	})
}

// Helper function to convert MarketAnalysisReport to AnalysisItem (without cache lookup)
func convertToAnalysisItemBasic(report queries.MarketAnalysisReport) AnalysisItem {
	var generatedAt *string
	if report.GeneratedAt.Valid {
		ts := report.GeneratedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		generatedAt = &ts
	}

	return AnalysisItem{
		ID:                 report.ID,
		Location:           report.Location,
		LocationNormalized: report.LocationNormalized,
		CacheKey:           locationToAnalysisCacheKey(report.Location),
		Status:             report.Status,
		GeneratedAt:        generatedAt,
		CreatedAt:          report.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		AccessedCount:      report.AccessedCount,
		Preview:            "",
		HasReport:          false,
	}
}

// Helper function to convert MarketAnalysisReport to AnalysisItem
func (h *Handler) convertToAnalysisItem(ctx context.Context, report queries.MarketAnalysisReport) AnalysisItem {
	item := convertToAnalysisItemBasic(report)

	// Try to fetch from cache to get preview and hasReport.
	// analysis_cache key differs from market_analysis_reports.cache_key — derive from location.
	cachedData, err := h.store.Q().GetAnalysisCacheByKey(ctx, locationToAnalysisCacheKey(report.Location))
	if err == nil {
		content := string(cachedData.Content)
		fullReport := ""
		if cachedData.FullReport.Valid {
			fullReport = cachedData.FullReport.String
		}

		reportContent := fullReport
		if reportContent == "" {
			reportContent = content
		}
		item.HasReport = reportContent != ""
		item.Preview = extractExecutiveSummary(reportContent)
	}

	return item
}

// Helper function to convert ListReportsByProximityRow to AnalysisItem (without cache lookup)
func convertToAnalysisItemWithDistanceBasic(report queries.ListReportsByProximityRow) AnalysisItem {
	var generatedAt *string
	if report.GeneratedAt.Valid {
		ts := report.GeneratedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		generatedAt = &ts
	}

	distanceKm := &report.DistanceKm

	return AnalysisItem{
		ID:                 report.ID,
		Location:           report.Location,
		LocationNormalized: report.LocationNormalized,
		CacheKey:           locationToAnalysisCacheKey(report.Location),
		Status:             report.Status,
		GeneratedAt:        generatedAt,
		CreatedAt:          report.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		AccessedCount:      report.AccessedCount,
		DistanceKm:         distanceKm,
		Preview:            "",
		HasReport:          false,
	}
}

// Helper function to convert ListReportsByProximityRow to AnalysisItem
func (h *Handler) convertToAnalysisItemWithDistance(ctx context.Context, report queries.ListReportsByProximityRow) AnalysisItem {
	item := convertToAnalysisItemWithDistanceBasic(report)

	// Try to fetch from cache to get preview and hasReport.
	// analysis_cache key differs from market_analysis_reports.cache_key — derive from location.
	cachedData, err := h.store.Q().GetAnalysisCacheByKey(ctx, locationToAnalysisCacheKey(report.Location))
	if err == nil {
		content := string(cachedData.Content)
		fullReport := ""
		if cachedData.FullReport.Valid {
			fullReport = cachedData.FullReport.String
		}

		reportContent := fullReport
		if reportContent == "" {
			reportContent = content
		}
		item.HasReport = reportContent != ""
		item.Preview = extractExecutiveSummary(reportContent)
	}

	return item
}

// Helper function to check if report is already in the list
func containsReport(items []AnalysisItem, cacheKey string) bool {
	for _, item := range items {
		if item.CacheKey == cacheKey {
			return true
		}
	}
	return false
}
