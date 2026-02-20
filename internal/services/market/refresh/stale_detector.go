// Package refresh provides event-driven refresh for market analysis reports (ADR-087 Phase 8)
package refresh

import (
	"context"
	"log/slog"
	"time"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/jackc/pgx/v5/pgtype"
)

// StaleSource represents a data source that has been updated
type StaleSource struct {
	Source  string // 'zhvi', 'zori', 'redfin', 'fred', 'census', 'ai_context'
	Current string // Current version
	Old     string // Report's version (if different)
}

// StaleDetector detects which reports are stale based on data source versions
type StaleDetector struct {
	queries *queries.Queries
	logger  *slog.Logger
}

// NewStaleDetector creates a new stale detector
func NewStaleDetector(q *queries.Queries) *StaleDetector {
	return &StaleDetector{
		queries: q,
		logger:  slog.Default().With("component", "stale_detector"),
	}
}

// IsReportStale checks if a report is stale (any data source version mismatch)
func (sd *StaleDetector) IsReportStale(ctx context.Context, reportID string) (bool, []StaleSource, error) {
	// Get current versions
	currentVersions, err := sd.GetCurrentVersions(ctx)
	if err != nil {
		return false, nil, err
	}

	// Get report's versions
	reportVersions, err := sd.queries.GetReportVersions(ctx, reportID)
	if err != nil {
		return false, nil, err
	}

	staleSources := []StaleSource{}

	// Check each source
	if reportVersions.ZhviVersion.Valid && reportVersions.ZhviVersion.String != currentVersions["zhvi"] {
		staleSources = append(staleSources, StaleSource{
			Source:  "zhvi",
			Current: currentVersions["zhvi"],
			Old:     reportVersions.ZhviVersion.String,
		})
	}
	if reportVersions.ZoriVersion.Valid && reportVersions.ZoriVersion.String != currentVersions["zori"] {
		staleSources = append(staleSources, StaleSource{
			Source:  "zori",
			Current: currentVersions["zori"],
			Old:     reportVersions.ZoriVersion.String,
		})
	}
	if reportVersions.RedfinVersion.Valid && reportVersions.RedfinVersion.String != currentVersions["redfin"] {
		staleSources = append(staleSources, StaleSource{
			Source:  "redfin",
			Current: currentVersions["redfin"],
			Old:     reportVersions.RedfinVersion.String,
		})
	}
	if reportVersions.FredVersion.Valid && reportVersions.FredVersion.String != currentVersions["fred"] {
		staleSources = append(staleSources, StaleSource{
			Source:  "fred",
			Current: currentVersions["fred"],
			Old:     reportVersions.FredVersion.String,
		})
	}
	if reportVersions.CensusVersion.Valid && reportVersions.CensusVersion.String != currentVersions["census"] {
		staleSources = append(staleSources, StaleSource{
			Source:  "census",
			Current: currentVersions["census"],
			Old:     reportVersions.CensusVersion.String,
		})
	}

	// AI Context: Special case - check if generated more than 90 days ago
	// (AI Context version is date-based and always changes, use time-based check instead)
	if reportVersions.AiContextVersion.Valid {
		contextDate, err := time.Parse("2006-01-02", reportVersions.AiContextVersion.String)
		if err == nil && time.Since(contextDate) > 90*24*time.Hour {
			staleSources = append(staleSources, StaleSource{
				Source:  "ai_context",
				Current: time.Now().Format("2006-01-02"),
				Old:     reportVersions.AiContextVersion.String,
			})
		}
	}

	return len(staleSources) > 0, staleSources, nil
}

// IsReportStaleTrends checks if a Trends report is stale (subset of sources)
func (sd *StaleDetector) IsReportStaleTrends(ctx context.Context, reportID string) (bool, []StaleSource, error) {
	// Trends only uses ZHVI, ZORI, FRED (no Redfin, Census, AI Context)
	currentVersions, err := sd.GetCurrentVersions(ctx)
	if err != nil {
		return false, nil, err
	}

	reportVersions, err := sd.queries.GetReportVersions(ctx, reportID)
	if err != nil {
		return false, nil, err
	}

	staleSources := []StaleSource{}

	if reportVersions.ZhviVersion.Valid && reportVersions.ZhviVersion.String != currentVersions["zhvi"] {
		staleSources = append(staleSources, StaleSource{
			Source:  "zhvi",
			Current: currentVersions["zhvi"],
			Old:     reportVersions.ZhviVersion.String,
		})
	}
	if reportVersions.ZoriVersion.Valid && reportVersions.ZoriVersion.String != currentVersions["zori"] {
		staleSources = append(staleSources, StaleSource{
			Source:  "zori",
			Current: currentVersions["zori"],
			Old:     reportVersions.ZoriVersion.String,
		})
	}
	if reportVersions.FredVersion.Valid && reportVersions.FredVersion.String != currentVersions["fred"] {
		staleSources = append(staleSources, StaleSource{
			Source:  "fred",
			Current: currentVersions["fred"],
			Old:     reportVersions.FredVersion.String,
		})
	}

	return len(staleSources) > 0, staleSources, nil
}

// GetCurrentVersions returns a map of current data source versions
func (sd *StaleDetector) GetCurrentVersions(ctx context.Context) (map[string]string, error) {
	versions, err := sd.queries.GetVersionsMap(ctx)
	if err != nil {
		return nil, err
	}

	versionMap := make(map[string]string)
	for _, v := range versions {
		versionMap[v.Source] = v.Version
	}

	return versionMap, nil
}

// CountStaleReports counts how many reports are stale for a given source
func (sd *StaleDetector) CountStaleReports(ctx context.Context, source, currentVersion string) (int64, error) {
	count, err := sd.queries.CountStaleReportsBySource(ctx, queries.CountStaleReportsBySourceParams{
		Source:         source,
		CurrentVersion: pgtype.Text{String: currentVersion, Valid: true},
	})
	if err != nil {
		return 0, err
	}

	return count, nil
}
