package importer

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/estara-ai/www/internal/db/marketqueries"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const zillowNationalRegionID = 102001

// computeNational ensures metro_region_id=0 has complete national data by merging:
// 1. Zillow's "United States" row (RegionID=102001) — ZHVI, ZORI, ZHVF, metrics
// 2. Redfin national data already on row 0 (from ImportRedfinData national)
// If Zillow row 102001 doesn't exist, falls back to state-level ZHVI aggregation.
func computeNational(ctx context.Context, pool *postgres.Pool, q *marketqueries.Queries, logger *slog.Logger) (*ImportResult, error) {
	start := time.Now()
	result := &ImportResult{Source: "compute_national", Level: "national"}

	// Try to copy from Zillow's authoritative national row (102001)
	copied, err := copyZillowNationalRow(ctx, pool, logger)
	if err != nil {
		logger.Warn("failed to copy Zillow national row", "error", err)
	}
	if copied {
		result.RecordsProcessed = 1
		result.RecordsUpserted = 1
		result.Duration = time.Since(start)
		return result, nil
	}

	// Fallback: compute from state-level ZHVI data
	logger.Info("Zillow national row (102001) not found, computing from state data")
	return computeNationalFromStates(ctx, q, logger, start)
}

// copyZillowNationalRow copies all Zillow columns from row 102001 to row 0,
// preserving any existing redfin_data on row 0.
func copyZillowNationalRow(ctx context.Context, pool *postgres.Pool, logger *slog.Logger) (bool, error) {
	// Read source row (102001)
	var metroName string
	var stateName pgtype.Text
	var zhvi, zori, zhvf, sales, dom, heat, afford []byte

	err := pool.QueryRow(ctx, `
		SELECT metro_name, state_name, zhvi_data, zori_data, zhvf_data,
		       sales_count_data, days_on_market_data, market_heat_data, affordability_data
		FROM metro_time_series WHERE metro_region_id = $1`, zillowNationalRegionID,
	).Scan(&metroName, &stateName, &zhvi, &zori, &zhvf, &sales, &dom, &heat, &afford)

	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	// Upsert to row 0, preserving existing redfin_data
	_, err = pool.Exec(ctx, `
		INSERT INTO metro_time_series (metro_region_id, metro_name, state_name,
			zhvi_data, zori_data, zhvf_data, sales_count_data,
			days_on_market_data, market_heat_data, affordability_data, updated_at)
		VALUES (0, $1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (metro_region_id) DO UPDATE SET
			metro_name = EXCLUDED.metro_name,
			zhvi_data = EXCLUDED.zhvi_data,
			zori_data = EXCLUDED.zori_data,
			zhvf_data = EXCLUDED.zhvf_data,
			sales_count_data = EXCLUDED.sales_count_data,
			days_on_market_data = EXCLUDED.days_on_market_data,
			market_heat_data = EXCLUDED.market_heat_data,
			affordability_data = EXCLUDED.affordability_data,
			updated_at = NOW()`,
		metroName, stateName, zhvi, zori, zhvf, sales, dom, heat, afford)

	if err != nil {
		return false, err
	}

	logger.Info("national data merged from Zillow row 102001 to row 0",
		"metro_name", metroName,
		"zhvi", len(zhvi) > 0,
		"zori", len(zori) > 0,
		"zhvf", len(zhvf) > 0,
		"sales", len(sales) > 0,
		"dom", len(dom) > 0,
		"heat", len(heat) > 0,
	)

	return true, nil
}

// computeNationalFromStates computes national ZHVI by averaging state-level data.
func computeNationalFromStates(ctx context.Context, q *marketqueries.Queries, logger *slog.Logger, start time.Time) (*ImportResult, error) {
	result := &ImportResult{Source: "compute_national", Level: "national"}

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

	// Simple average of state ZHVI values per month
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

	logger.Info("national aggregate computed from states",
		"states_used", len(states),
		"months", len(nationalZHVI),
		"duration", result.Duration,
	)

	return result, nil
}
