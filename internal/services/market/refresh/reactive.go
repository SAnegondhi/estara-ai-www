// Package refresh provides reactive on-demand refresh for long-tail locations
package refresh

import (
	"context"
	"log/slog"
)

// ReactiveRefresher handles on-demand refresh when user accesses stale report
type ReactiveRefresher struct {
	detector *StaleDetector
	logger   *slog.Logger
}

// NewReactiveRefresher creates a new reactive refresher
func NewReactiveRefresher(detector *StaleDetector) *ReactiveRefresher {
	return &ReactiveRefresher{
		detector: detector,
		logger:   slog.Default().With("component", "reactive_refresher"),
	}
}

// CheckAndQueueRefresh checks if report is stale and returns stale sources
func (rr *ReactiveRefresher) CheckAndQueueRefresh(ctx context.Context, reportID string, isTrends bool) (bool, []StaleSource, error) {
	var isStale bool
	var staleSources []StaleSource
	var err error

	if isTrends {
		isStale, staleSources, err = rr.detector.IsReportStaleTrends(ctx, reportID)
	} else {
		isStale, staleSources, err = rr.detector.IsReportStale(ctx, reportID)
	}

	if err != nil {
		return false, nil, err
	}

	if isStale {
		rr.logger.Info("reactive refresh triggered",
			"report_id", reportID,
			"is_trends", isTrends,
			"stale_sources", len(staleSources),
		)
	}

	return isStale, staleSources, nil
}
