// Package refresh provides event-driven refresh for market analysis reports (ADR-087 Phase 8)
package refresh

import (
	"context"
	"log/slog"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/economics"
	"github.com/estara-ai/www/internal/services/market/timeseries"
)

// Service orchestrates proactive and reactive refresh of market analysis reports
type Service struct {
	queries           *queries.Queries
	detector          *StaleDetector
	selective         *SelectiveRefresher
	proactive         *ProactiveRefresher
	reactive          *ReactiveRefresher
	logger            *slog.Logger
	proactiveTopN     int32  // How many top reports to refresh proactively
	rateLimit         int    // Max concurrent refreshes
	enableProactive   bool   // Enable proactive batch refresh
	enableReactive    bool   // Enable reactive on-demand refresh
}

// Config holds configuration for the refresh service
type Config struct {
	Queries         *queries.Queries
	Market          *aggregator.Aggregator
	Economics       *economics.Aggregator
	Metro           *timeseries.MetroReader
	ProactiveTopN   int32  // Default: 100 (top 100 locations)
	RateLimit       int    // Default: 10 (10 concurrent refreshes)
	EnableProactive bool   // Default: true
	EnableReactive  bool   // Default: true
}

// NewService creates a new refresh service
func NewService(cfg Config) *Service {
	// Set defaults
	if cfg.ProactiveTopN <= 0 {
		cfg.ProactiveTopN = 100
	}
	if cfg.RateLimit <= 0 {
		cfg.RateLimit = 10
	}

	detector := NewStaleDetector(cfg.Queries)
	selective := NewSelectiveRefresher(cfg.Market, cfg.Economics, cfg.Metro)
	proactive := NewProactiveRefresher(cfg.Queries, detector)
	reactive := NewReactiveRefresher(detector)

	return &Service{
		queries:           cfg.Queries,
		detector:          detector,
		selective:         selective,
		proactive:         proactive,
		reactive:          reactive,
		logger:            slog.Default().With("component", "refresh_service"),
		proactiveTopN:     cfg.ProactiveTopN,
		rateLimit:         cfg.RateLimit,
		enableProactive:   cfg.EnableProactive,
		enableReactive:    cfg.EnableReactive,
	}
}

// ProactiveRefresh triggers batch refresh of top N reports after data source update
// Called by ADR-075 importers after successful import
func (s *Service) ProactiveRefresh(ctx context.Context, source, newVersion string) ([]string, error) {
	if !s.enableProactive {
		s.logger.Debug("proactive refresh disabled", "source", source)
		return []string{}, nil
	}

	s.logger.Info("proactive refresh starting",
		"source", source,
		"new_version", newVersion,
		"top_n", s.proactiveTopN,
	)

	// Estimate total refresh load
	totalStale, err := s.proactive.EstimateRefreshLoad(ctx, source, newVersion)
	if err != nil {
		s.logger.Error("failed to estimate refresh load", "error", err)
		return nil, err
	}

	s.logger.Info("refresh load estimated",
		"source", source,
		"total_stale", totalStale,
		"will_refresh", min(int64(s.proactiveTopN), totalStale),
	)

	// Get top N reports to refresh
	reportIDs, err := s.proactive.RefreshTopReports(ctx, source, newVersion, s.proactiveTopN)
	if err != nil {
		return nil, err
	}

	// TODO: Queue refresh jobs for these reports (Phase 9 - job queue integration)
	// For now, just return the list of report IDs

	return reportIDs, nil
}

// ReactiveRefresh checks if a report is stale and returns stale sources
// Called when user accesses a report
func (s *Service) ReactiveRefresh(ctx context.Context, reportID string, isTrends bool) (bool, []StaleSource, error) {
	if !s.enableReactive {
		return false, nil, nil
	}

	isStale, staleSources, err := s.reactive.CheckAndQueueRefresh(ctx, reportID, isTrends)
	if err != nil {
		return false, nil, err
	}

	if isStale {
		s.logger.Info("reactive refresh needed",
			"report_id", reportID,
			"is_trends", isTrends,
			"stale_sources", len(staleSources),
		)
	}

	// TODO: Queue background refresh job (Phase 9 - job queue integration)
	// For now, just return stale status and sources

	return isStale, staleSources, nil
}

// GetCurrentVersions returns current data source versions
func (s *Service) GetCurrentVersions(ctx context.Context) (map[string]string, error) {
	return s.detector.GetCurrentVersions(ctx)
}

// IsReportStale checks if a specific report is stale
func (s *Service) IsReportStale(ctx context.Context, reportID string, isTrends bool) (bool, []StaleSource, error) {
	if isTrends {
		return s.detector.IsReportStaleTrends(ctx, reportID)
	}
	return s.detector.IsReportStale(ctx, reportID)
}

// min returns minimum of two int64 values
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
