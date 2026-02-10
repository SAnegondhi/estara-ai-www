package importer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/estara-ai/www/internal/db/postgres"
)

const batchSize = 100

// batchExec queues raw SQL statements with args into pgx batches and sends them.
// Returns (upserted count, errors).
func batchExec(ctx context.Context, pool *postgres.Pool, stmts []batchStmt, logger *slog.Logger) (int64, []string) {
	var upserted int64
	var errors []string

	for i := 0; i < len(stmts); i += batchSize {
		end := i + batchSize
		if end > len(stmts) {
			end = len(stmts)
		}
		chunk := stmts[i:end]

		batch := &pgx.Batch{}
		for _, s := range chunk {
			batch.Queue(s.sql, s.args...)
		}

		br := pool.SendBatch(ctx, batch)

		for j := 0; j < len(chunk); j++ {
			_, err := br.Exec()
			if err != nil {
				errors = append(errors, fmt.Sprintf("upsert %s: %v", chunk[j].label, err))
			} else {
				upserted++
			}
		}

		if err := br.Close(); err != nil {
			logger.Warn("batch close error", "error", err)
		}

		// Log progress for large imports
		if upserted > 0 && upserted%5000 == 0 {
			logger.Info("batch progress", "upserted", upserted, "total", len(stmts))
		}
	}

	return upserted, errors
}

type batchStmt struct {
	sql   string
	args  []any
	label string
}

// SQL constants for batch upserts (same as sqlc queries but used directly for batching)
const (
	upsertMetroZHVISQL = `INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, zhvi_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    metro_name = EXCLUDED.metro_name,
    state_name = EXCLUDED.state_name,
    zhvi_data = EXCLUDED.zhvi_data,
    updated_at = NOW()`

	upsertMetroZORISQL = `INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, zori_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    metro_name = EXCLUDED.metro_name,
    state_name = EXCLUDED.state_name,
    zori_data = EXCLUDED.zori_data,
    updated_at = NOW()`

	upsertMetroZHVFSQL = `INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, zhvf_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    metro_name = EXCLUDED.metro_name,
    state_name = EXCLUDED.state_name,
    zhvf_data = EXCLUDED.zhvf_data,
    updated_at = NOW()`

	upsertMetroMetricsSQL = `INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, sales_count_data, days_on_market_data, market_heat_data, affordability_data, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    sales_count_data = COALESCE(EXCLUDED.sales_count_data, metro_time_series.sales_count_data),
    days_on_market_data = COALESCE(EXCLUDED.days_on_market_data, metro_time_series.days_on_market_data),
    market_heat_data = COALESCE(EXCLUDED.market_heat_data, metro_time_series.market_heat_data),
    affordability_data = COALESCE(EXCLUDED.affordability_data, metro_time_series.affordability_data),
    updated_at = NOW()`

	upsertMetroRedfinSQL = `INSERT INTO metro_time_series (metro_region_id, metro_name, state_name, redfin_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (metro_region_id) DO UPDATE SET
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW()`

	upsertCityZHVISQL = `INSERT INTO city_time_series (city_region_id, city_name, state_name, metro_region_id, zhvi_data, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (city_region_id) DO UPDATE SET
    city_name = EXCLUDED.city_name,
    state_name = EXCLUDED.state_name,
    metro_region_id = EXCLUDED.metro_region_id,
    zhvi_data = EXCLUDED.zhvi_data,
    updated_at = NOW()`

	upsertCityZORISQL = `INSERT INTO city_time_series (city_region_id, city_name, state_name, metro_region_id, zori_data, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (city_region_id) DO UPDATE SET
    city_name = EXCLUDED.city_name,
    state_name = EXCLUDED.state_name,
    metro_region_id = EXCLUDED.metro_region_id,
    zori_data = EXCLUDED.zori_data,
    updated_at = NOW()`

	upsertCityRedfinSQL = `INSERT INTO city_time_series (city_region_id, city_name, state_name, redfin_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (city_region_id) DO UPDATE SET
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW()`

	upsertStateZHVISQL = `INSERT INTO state_time_series (state_region_id, state_code, state_name, zhvi_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (state_code) DO UPDATE SET
    state_name = EXCLUDED.state_name,
    zhvi_data = EXCLUDED.zhvi_data,
    updated_at = NOW()`

	upsertStateRedfinSQL = `INSERT INTO state_time_series (state_region_id, state_code, state_name, redfin_data, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (state_code) DO UPDATE SET
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW()`

	upsertZipZHVISQL = `INSERT INTO zip_time_series (zip_code, city, state, county, metro_region_id, zhvi_data, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (zip_code) DO UPDATE SET
    city = EXCLUDED.city,
    state = EXCLUDED.state,
    county = EXCLUDED.county,
    metro_region_id = EXCLUDED.metro_region_id,
    zhvi_data = EXCLUDED.zhvi_data,
    updated_at = NOW()`

	upsertZipZORISQL = `INSERT INTO zip_time_series (zip_code, city, state, zhvi_data, zori_data, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (zip_code) DO UPDATE SET
    zori_data = EXCLUDED.zori_data,
    updated_at = NOW()`

	upsertZipRedfinSQL = `INSERT INTO zip_time_series (zip_code, state, redfin_data, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (zip_code) DO UPDATE SET
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW()`

	upsertCountyRedfinSQL = `INSERT INTO county_time_series (county_region_id, county_fips, county_name, state_code, metro_region_id, redfin_data, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (county_fips) DO UPDATE SET
    county_region_id = EXCLUDED.county_region_id,
    county_name = EXCLUDED.county_name,
    state_code = EXCLUDED.state_code,
    metro_region_id = EXCLUDED.metro_region_id,
    redfin_data = EXCLUDED.redfin_data,
    updated_at = NOW()`
)
