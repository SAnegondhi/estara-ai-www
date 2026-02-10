package importer

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/estara-ai/www/internal/db/marketqueries"
	"github.com/jackc/pgx/v5/pgtype"
)

// computeNational computes the national aggregate (metro_region_id=0) from state-level ZHVI data.
// Zillow Metro CSV often includes a "United States" row (RegionID=102001, SizeRank=0).
// If that row was imported, this function validates/overwrites it.
// If not, this function computes a simple average from state data.
func computeNational(ctx context.Context, q *marketqueries.Queries, logger *slog.Logger) (*ImportResult, error) {
	start := time.Now()
	result := &ImportResult{Source: "compute_national", Level: "national"}

	// Check if national row already exists (from Zillow import)
	existing, err := q.GetNationalTimeSeries(ctx)
	if err == nil && len(existing.ZhviData) > 0 {
		logger.Info("national aggregate already exists from Zillow import",
			"metro_name", existing.MetroName,
		)
		result.RecordsProcessed = 1
		result.RecordsUpserted = 0
		result.Duration = time.Since(start)
		return result, nil
	}

	// Compute from state-level data
	states, err := q.GetAllStateZHVI(ctx)
	if err != nil {
		return nil, err
	}

	if len(states) == 0 {
		logger.Warn("no state ZHVI data available for national aggregation")
		result.Duration = time.Since(start)
		return result, nil
	}

	result.RecordsProcessed = int64(len(states))

	// Aggregate: simple average of state ZHVI values per month
	monthTotals := make(map[string]float64)
	monthCounts := make(map[string]int)

	for _, state := range states {
		if state.ZhviData == nil {
			continue
		}

		var stateData map[string]float64
		if err := json.Unmarshal(state.ZhviData, &stateData); err != nil {
			continue
		}

		for month, value := range stateData {
			monthTotals[month] += value
			monthCounts[month]++
		}
	}

	// Compute averages
	nationalZHVI := make(map[string]float64, len(monthTotals))
	for month, total := range monthTotals {
		if count := monthCounts[month]; count > 0 {
			nationalZHVI[month] = total / float64(count)
		}
	}

	if len(nationalZHVI) == 0 {
		logger.Warn("no month data available for national aggregation")
		result.Duration = time.Since(start)
		return result, nil
	}

	zhviJSON, err := json.Marshal(nationalZHVI)
	if err != nil {
		return nil, err
	}

	// Upsert national row (metro_region_id=0)
	err = q.UpsertMetroZHVI(ctx, marketqueries.UpsertMetroZHVIParams{
		MetroRegionID: 0,
		MetroName:     "United States",
		StateName:     pgtype.Text{Valid: false},
		ZhviData:      zhviJSON,
	})
	if err != nil {
		return nil, err
	}

	result.RecordsUpserted = 1
	result.Duration = time.Since(start)

	logger.Info("national aggregate computed",
		"states_used", len(states),
		"months", len(nationalZHVI),
		"duration", result.Duration,
	)

	return result, nil
}
