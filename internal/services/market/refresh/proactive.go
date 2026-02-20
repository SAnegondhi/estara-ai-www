// Package refresh provides proactive batch refresh for popular locations
package refresh

import (
	"context"
	"log/slog"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/jackc/pgx/v5/pgtype"
)

// ProactiveRefresher handles batch refresh of popular locations after data updates
type ProactiveRefresher struct {
	queries  *queries.Queries
	detector *StaleDetector
	logger   *slog.Logger
}

// NewProactiveRefresher creates a new proactive refresher
func NewProactiveRefresher(q *queries.Queries, detector *StaleDetector) *ProactiveRefresher {
	return &ProactiveRefresher{
		queries:  q,
		detector: detector,
		logger:   slog.Default().With("component", "proactive_refresher"),
	}
}

// RefreshTopReports finds and queues top N reports for refresh after data source update
func (pr *ProactiveRefresher) RefreshTopReports(ctx context.Context, source, newVersion string, topN int32) ([]string, error) {
	pr.logger.Info("proactive refresh triggered",
		"source", source,
		"new_version", newVersion,
		"top_n", topN,
	)

	var reports interface{}
	var err error

	// Find top N stale reports by access count for this source
	switch source {
	case "zhvi":
		reports, err = pr.queries.ListStaleReportsByZHVI(ctx, queries.ListStaleReportsByZHVIParams{
			ZhviVersion: pgtype.Text{String: newVersion, Valid: true},
			Limit:       topN,
		})
	case "zori":
		reports, err = pr.queries.ListStaleReportsByZORI(ctx, queries.ListStaleReportsByZORIParams{
			ZoriVersion: pgtype.Text{String: newVersion, Valid: true},
			Limit:       topN,
		})
	case "redfin":
		reports, err = pr.queries.ListStaleReportsByRedfin(ctx, queries.ListStaleReportsByRedfinParams{
			RedfinVersion: pgtype.Text{String: newVersion, Valid: true},
			Limit:         topN,
		})
	case "fred":
		reports, err = pr.queries.ListStaleReportsByFRED(ctx, queries.ListStaleReportsByFREDParams{
			FredVersion: pgtype.Text{String: newVersion, Valid: true},
			Limit:       topN,
		})
	case "census":
		reports, err = pr.queries.ListStaleReportsByCensus(ctx, queries.ListStaleReportsByCensusParams{
			CensusVersion: pgtype.Text{String: newVersion, Valid: true},
			Limit:         topN,
		})
	default:
		pr.logger.Warn("unknown data source for proactive refresh", "source", source)
		return []string{}, nil
	}

	if err != nil {
		pr.logger.Error("failed to list stale reports", "error", err, "source", source)
		return nil, err
	}

	// Extract report IDs for job queue
	reportIDs := []string{}

	switch r := reports.(type) {
	case []queries.ListStaleReportsByZHVIRow:
		for _, rep := range r {
			reportIDs = append(reportIDs, rep.ID)
		}
	case []queries.ListStaleReportsByZORIRow:
		for _, rep := range r {
			reportIDs = append(reportIDs, rep.ID)
		}
	case []queries.ListStaleReportsByRedfinRow:
		for _, rep := range r {
			reportIDs = append(reportIDs, rep.ID)
		}
	case []queries.ListStaleReportsByFREDRow:
		for _, rep := range r {
			reportIDs = append(reportIDs, rep.ID)
		}
	case []queries.ListStaleReportsByCensusRow:
		for _, rep := range r {
			reportIDs = append(reportIDs, rep.ID)
		}
	}

	pr.logger.Info("proactive refresh queued",
		"source", source,
		"report_count", len(reportIDs),
	)

	return reportIDs, nil
}

// EstimateRefreshLoad estimates how many reports would be affected by a data update
func (pr *ProactiveRefresher) EstimateRefreshLoad(ctx context.Context, source, newVersion string) (int64, error) {
	count, err := pr.detector.CountStaleReports(ctx, source, newVersion)
	if err != nil {
		return 0, err
	}

	pr.logger.Info("refresh load estimate",
		"source", source,
		"stale_reports", count,
	)

	return count, nil
}
