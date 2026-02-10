// Package importer provides market data download, parsing, and import functionality.
// It handles CSV/TSV data from Zillow and Redfin, converting them to JSONB format
// for storage in the market database. Used by both cron endpoints and CLI tool.
package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/db/marketqueries"
	"github.com/estara-ai/www/internal/db/postgres"
)

// Service orchestrates market data import operations.
type Service struct {
	marketDB *postgres.Pool
	queries  *marketqueries.Queries
	logger   *slog.Logger
}

// NewService creates a new importer service.
func NewService(marketDB *postgres.Pool) *Service {
	return &Service{
		marketDB: marketDB,
		queries:  marketqueries.New(marketDB),
		logger:   slog.Default().With("component", "market_importer"),
	}
}

// ImportResult tracks the outcome of an import operation.
type ImportResult struct {
	Source           string        `json:"source"`
	Level            string        `json:"level"`
	RecordsProcessed int64         `json:"recordsProcessed"`
	RecordsUpserted  int64         `json:"recordsUpserted"`
	RecordsSkipped   int64         `json:"recordsSkipped"`
	Errors           []string      `json:"errors,omitempty"`
	Duration         time.Duration `json:"duration"`
}

// StatusResult provides freshness information about market data.
type StatusResult struct {
	Metro  TableStatus `json:"metro"`
	City   TableStatus `json:"city"`
	State  TableStatus `json:"state"`
	Zip    TableStatus `json:"zip"`
	County TableStatus `json:"county"`
}

// TableStatus tracks row count and freshness for a table.
type TableStatus struct {
	RowCount     int64      `json:"rowCount"`
	LatestUpdate *time.Time `json:"latestUpdate,omitempty"`
}

// ImportZillowZHVI downloads and imports Zillow ZHVI data for the given level.
func (s *Service) ImportZillowZHVI(ctx context.Context, level string) (*ImportResult, error) {
	start := time.Now()
	result := &ImportResult{Source: "zillow_zhvi", Level: level}

	url, err := zillowZHVIURL(level)
	if err != nil {
		return nil, err
	}

	s.logger.Info("importing Zillow ZHVI", "level", level, "url", url)

	data, err := downloadCSV(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("download ZHVI %s: %w", level, err)
	}

	s.logger.Info("ZHVI downloaded", "level", level, "bytes", len(data))

	rows, err := parseZillowCSV(data)
	if err != nil {
		return nil, fmt.Errorf("parse ZHVI %s: %w", level, err)
	}

	result.RecordsProcessed = int64(len(rows))
	s.logger.Info("ZHVI parsed", "level", level, "rows", len(rows))

	// Build metro lookup for city-level linking
	var metroLookup map[string]int32
	if level == "city" || level == "zip" {
		metroLookup, err = s.buildMetroLookup(ctx)
		if err != nil {
			s.logger.Warn("failed to build metro lookup", "error", err)
		}
	}

	// Build batch statements
	var stmts []batchStmt
	for _, row := range rows {
		ts := extractTimeSeries(row)
		tsJSON, err := json.Marshal(ts)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("marshal %s: %v", row.RegionName, err))
			continue
		}

		switch level {
		case "metro":
			regionID := safeAtoi(row.RegionID)
			if regionID == 0 {
				result.RecordsSkipped++
				continue
			}
			stmts = append(stmts, batchStmt{
				sql:   upsertMetroZHVISQL,
				args:  []any{int32(regionID), row.RegionName, textOrNull(row.StateName), tsJSON},
				label: row.RegionName,
			})
		case "city":
			regionID := safeAtoi(row.RegionID)
			if regionID == 0 {
				result.RecordsSkipped++
				continue
			}
			metroID := lookupMetro(row.Metro, metroLookup)
			stmts = append(stmts, batchStmt{
				sql:   upsertCityZHVISQL,
				args:  []any{int32(regionID), row.RegionName, textOrNull(row.StateName), int4OrNull(metroID), tsJSON},
				label: row.RegionName,
			})
		case "zip":
			if row.RegionName == "" {
				result.RecordsSkipped++
				continue
			}
			metroID := lookupMetro(row.Metro, metroLookup)
			stmts = append(stmts, batchStmt{
				sql:   upsertZipZHVISQL,
				args:  []any{row.RegionName, textOrNull(row.City), textOrNull(row.State), textOrNull(row.CountyName), int4OrNull(metroID), tsJSON},
				label: row.RegionName,
			})
		case "state":
			regionID := safeAtoi(row.RegionID)
			if regionID == 0 {
				result.RecordsSkipped++
				continue
			}
			stateCode := row.State
			if stateCode == "" {
				stateCode = stateNameToCode(row.RegionName)
			}
			if stateCode == "" {
				result.RecordsSkipped++
				continue
			}
			stmts = append(stmts, batchStmt{
				sql:   upsertStateZHVISQL,
				args:  []any{int32(regionID), stateCode, row.RegionName, tsJSON},
				label: row.RegionName,
			})
		}
	}

	// Execute batch
	upserted, errs := batchExec(ctx, s.marketDB, stmts, s.logger)
	result.RecordsUpserted = upserted
	result.Errors = append(result.Errors, errs...)

	result.Duration = time.Since(start)
	s.logger.Info("ZHVI import complete",
		"level", level,
		"processed", result.RecordsProcessed,
		"upserted", result.RecordsUpserted,
		"skipped", result.RecordsSkipped,
		"errors", len(result.Errors),
		"duration", result.Duration,
	)
	return result, nil
}

// ImportZillowZORI downloads and imports Zillow ZORI data for the given level.
func (s *Service) ImportZillowZORI(ctx context.Context, level string) (*ImportResult, error) {
	start := time.Now()
	result := &ImportResult{Source: "zillow_zori", Level: level}

	url, err := zillowZORIURL(level)
	if err != nil {
		return nil, err
	}

	s.logger.Info("importing Zillow ZORI", "level", level, "url", url)

	data, err := downloadCSV(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("download ZORI %s: %w", level, err)
	}

	s.logger.Info("ZORI downloaded", "level", level, "bytes", len(data))

	rows, err := parseZillowCSV(data)
	if err != nil {
		return nil, fmt.Errorf("parse ZORI %s: %w", level, err)
	}

	result.RecordsProcessed = int64(len(rows))

	var metroLookup map[string]int32
	if level == "city" || level == "zip" {
		metroLookup, _ = s.buildMetroLookup(ctx)
	}

	var stmts []batchStmt
	for _, row := range rows {
		ts := extractTimeSeries(row)
		tsJSON, err := json.Marshal(ts)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("marshal %s: %v", row.RegionName, err))
			continue
		}

		switch level {
		case "metro":
			regionID := safeAtoi(row.RegionID)
			if regionID == 0 {
				result.RecordsSkipped++
				continue
			}
			stmts = append(stmts, batchStmt{
				sql:   upsertMetroZORISQL,
				args:  []any{int32(regionID), row.RegionName, textOrNull(row.StateName), tsJSON},
				label: row.RegionName,
			})
		case "city":
			regionID := safeAtoi(row.RegionID)
			if regionID == 0 {
				result.RecordsSkipped++
				continue
			}
			metroID := lookupMetro(row.Metro, metroLookup)
			stmts = append(stmts, batchStmt{
				sql:   upsertCityZORISQL,
				args:  []any{int32(regionID), row.RegionName, textOrNull(row.StateName), int4OrNull(metroID), tsJSON},
				label: row.RegionName,
			})
		case "zip":
			if row.RegionName == "" {
				result.RecordsSkipped++
				continue
			}
			stmts = append(stmts, batchStmt{
				sql:   upsertZipZORISQL,
				args:  []any{row.RegionName, textOrNull(row.City), textOrNull(row.State), nil, tsJSON},
				label: row.RegionName,
			})
		}
	}

	upserted, errs := batchExec(ctx, s.marketDB, stmts, s.logger)
	result.RecordsUpserted = upserted
	result.Errors = append(result.Errors, errs...)

	result.Duration = time.Since(start)
	s.logger.Info("ZORI import complete",
		"level", level,
		"processed", result.RecordsProcessed,
		"upserted", result.RecordsUpserted,
		"duration", result.Duration,
	)
	return result, nil
}

// ImportZillowForecasts downloads and imports ZHVI forecast data (metro only).
func (s *Service) ImportZillowForecasts(ctx context.Context) (*ImportResult, error) {
	start := time.Now()
	result := &ImportResult{Source: "zillow_zhvf", Level: "metro"}

	url := zillowBaseURL + "zhvf_growth/Metro_zhvf_growth_uc_sfrcondo_tier_0.33_0.67_sm_sa_month.csv"
	s.logger.Info("importing Zillow forecasts", "url", url)

	data, err := downloadCSV(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("download ZHVF: %w", err)
	}

	rows, err := parseZillowCSV(data)
	if err != nil {
		return nil, fmt.Errorf("parse ZHVF: %w", err)
	}

	result.RecordsProcessed = int64(len(rows))

	var stmts []batchStmt
	for _, row := range rows {
		regionID := safeAtoi(row.RegionID)
		if regionID == 0 {
			result.RecordsSkipped++
			continue
		}

		ts := extractTimeSeries(row)
		tsJSON, err := json.Marshal(ts)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("marshal %s: %v", row.RegionName, err))
			continue
		}

		stmts = append(stmts, batchStmt{
			sql:   upsertMetroZHVFSQL,
			args:  []any{int32(regionID), row.RegionName, textOrNull(row.StateName), tsJSON},
			label: row.RegionName,
		})
	}

	upserted, errs := batchExec(ctx, s.marketDB, stmts, s.logger)
	result.RecordsUpserted = upserted
	result.Errors = append(result.Errors, errs...)

	result.Duration = time.Since(start)
	s.logger.Info("ZHVF import complete",
		"processed", result.RecordsProcessed,
		"upserted", result.RecordsUpserted,
		"duration", result.Duration,
	)
	return result, nil
}

// ImportZillowMetrics downloads and imports sales, DOM, heat, affordability metrics (metro only).
func (s *Service) ImportZillowMetrics(ctx context.Context) (*ImportResult, error) {
	start := time.Now()
	result := &ImportResult{Source: "zillow_metrics", Level: "metro"}

	metricsURLs := map[string]string{
		"sales":         zillowBaseURL + "sales_count_now/Metro_sales_count_now_uc_sfrcondo_month.csv",
		"dom":           zillowBaseURL + "mean_doz_pending/Metro_mean_doz_pending_uc_sfrcondo_sm_month.csv",
		"heat":          zillowBaseURL + "market_temp_index/Metro_market_temp_index_uc_sfrcondo_month.csv",
		"affordability": zillowBaseURL + "renter_affordability/Metro_renter_affordability_uc_sfrcondomfr_sm_month.csv",
	}

	// Download and parse all metrics CSVs
	type metricData struct {
		name string
		rows []ZillowRow
	}
	var metrics []metricData

	for name, url := range metricsURLs {
		data, err := downloadCSV(ctx, url)
		if err != nil {
			s.logger.Warn("failed to download metric", "metric", name, "error", err)
			result.Errors = append(result.Errors, fmt.Sprintf("download %s: %v", name, err))
			continue
		}
		rows, err := parseZillowCSV(data)
		if err != nil {
			s.logger.Warn("failed to parse metric", "metric", name, "error", err)
			result.Errors = append(result.Errors, fmt.Sprintf("parse %s: %v", name, err))
			continue
		}
		metrics = append(metrics, metricData{name: name, rows: rows})
	}

	// Build per-metro merged maps
	type metricsBundle struct {
		regionName string
		stateName  string
		sales      []byte
		dom        []byte
		heat       []byte
		afford     []byte
	}
	metroMetrics := make(map[int32]*metricsBundle)

	for _, m := range metrics {
		for _, row := range m.rows {
			regionID := int32(safeAtoi(row.RegionID))
			if regionID == 0 {
				continue
			}
			if _, ok := metroMetrics[regionID]; !ok {
				metroMetrics[regionID] = &metricsBundle{
					regionName: row.RegionName,
					stateName:  row.StateName,
				}
			}
			ts := extractTimeSeries(row)
			tsJSON, _ := json.Marshal(ts)
			switch m.name {
			case "sales":
				metroMetrics[regionID].sales = tsJSON
			case "dom":
				metroMetrics[regionID].dom = tsJSON
			case "heat":
				metroMetrics[regionID].heat = tsJSON
			case "affordability":
				metroMetrics[regionID].afford = tsJSON
			}
		}
	}

	result.RecordsProcessed = int64(len(metroMetrics))

	var stmts []batchStmt
	for regionID, bundle := range metroMetrics {
		stmts = append(stmts, batchStmt{
			sql:   upsertMetroMetricsSQL,
			args:  []any{regionID, bundle.regionName, textOrNull(bundle.stateName), bundle.sales, bundle.dom, bundle.heat, bundle.afford},
			label: fmt.Sprintf("metro-%d", regionID),
		})
	}

	upserted, errs := batchExec(ctx, s.marketDB, stmts, s.logger)
	result.RecordsUpserted = upserted
	result.Errors = append(result.Errors, errs...)

	result.Duration = time.Since(start)
	s.logger.Info("metrics import complete",
		"processed", result.RecordsProcessed,
		"upserted", result.RecordsUpserted,
		"duration", result.Duration,
	)
	return result, nil
}

// redfinEntity holds aggregated per-month data for a single geographic entity.
type redfinEntity struct {
	regionID  int32
	name      string
	stateCode string
	fips      string // county only
	metroID   int32  // county only
	months    map[string]json.RawMessage
}

// ImportRedfinData downloads and imports Redfin data for the given level.
// Redfin TSVs have one row per (entity, month, property_type). We aggregate
// all months for each entity into a single JSONB document before upserting.
func (s *Service) ImportRedfinData(ctx context.Context, level string) (*ImportResult, error) {
	start := time.Now()
	result := &ImportResult{Source: "redfin", Level: level}

	url, err := redfinURL(level)
	if err != nil {
		return nil, err
	}

	s.logger.Info("importing Redfin data", "level", level, "url", url)

	// Build metro lookup for matching
	metroLookup, _ := s.buildMetroLookup(ctx)

	// Accumulate per-entity data in memory
	entities := make(map[string]*redfinEntity) // key = unique entity ID

	err = streamRedfinTSV(ctx, url, func(record RedfinRecord) error {
		result.RecordsProcessed++

		if len(record.PeriodBegin) < 7 {
			result.RecordsSkipped++
			return nil
		}

		// Only process "All Residential" property type to avoid duplicates
		if record.PropertyType != "" && record.PropertyType != "All Residential" {
			result.RecordsSkipped++
			return nil
		}

		monthKey := record.PeriodBegin[:7]
		parsed := parseRedfinMetrics(record)
		metricsJSON, err := json.Marshal(parsed)
		if err != nil {
			return nil
		}

		var entityKey string
		var ent *redfinEntity

		switch level {
		case "national":
			entityKey = "us"
			if _, ok := entities[entityKey]; !ok {
				entities[entityKey] = &redfinEntity{
					regionID: 0,
					name:     "United States",
					months:   make(map[string]json.RawMessage),
				}
			}
			ent = entities[entityKey]
		case "state":
			if record.StateCode == "" {
				result.RecordsSkipped++
				return nil
			}
			entityKey = "state:" + record.StateCode
			if _, ok := entities[entityKey]; !ok {
				entities[entityKey] = &redfinEntity{
					regionID:  stateRegionHash(record.StateCode),
					name:      record.Region,
					stateCode: record.StateCode,
					months:    make(map[string]json.RawMessage),
				}
			}
			ent = entities[entityKey]
		case "metro":
			// Redfin uses "City, ST metro area" — strip suffix for Zillow lookup
			metroName := strings.TrimSuffix(record.Region, " metro area")
			metroID := lookupMetro(metroName, metroLookup)
			if metroID == 0 {
				result.RecordsSkipped++
				return nil
			}
			entityKey = fmt.Sprintf("metro:%d", metroID)
			if _, ok := entities[entityKey]; !ok {
				entities[entityKey] = &redfinEntity{
					regionID:  metroID,
					name:      record.Region,
					stateCode: record.StateCode,
					months:    make(map[string]json.RawMessage),
				}
			}
			ent = entities[entityKey]
		case "county":
			if record.Region == "" || record.TableID == "" {
				result.RecordsSkipped++
				return nil
			}
			regionID := countyRegionHash(record.Region, record.StateCode)
			entityKey = fmt.Sprintf("county:%s", record.TableID) // use Redfin table_id as canonical key
			if _, ok := entities[entityKey]; !ok {
				metroID := lookupMetro(record.Region, metroLookup)
				entities[entityKey] = &redfinEntity{
					regionID:  regionID,
					name:      record.Region,
					stateCode: record.StateCode,
					fips:      record.TableID, // Redfin table_id as county identifier
					metroID:   metroID,
					months:    make(map[string]json.RawMessage),
				}
			}
			ent = entities[entityKey]
		case "city":
			regionID := cityRegionHash(record.Region, record.StateCode)
			entityKey = fmt.Sprintf("city:%d", regionID)
			if _, ok := entities[entityKey]; !ok {
				entities[entityKey] = &redfinEntity{
					regionID:  regionID,
					name:      record.Region,
					stateCode: record.StateCode,
					months:    make(map[string]json.RawMessage),
				}
			}
			ent = entities[entityKey]
		case "zip":
			if record.Region == "" {
				result.RecordsSkipped++
				return nil
			}
			entityKey = "zip:" + record.Region
			if _, ok := entities[entityKey]; !ok {
				entities[entityKey] = &redfinEntity{
					name:      record.Region,
					stateCode: record.StateCode,
					months:    make(map[string]json.RawMessage),
				}
			}
			ent = entities[entityKey]
		}

		if ent != nil {
			ent.months[monthKey] = metricsJSON
		}

		// Log progress every 100,000 records
		if result.RecordsProcessed%100000 == 0 {
			s.logger.Info("Redfin streaming progress",
				"level", level,
				"processed", result.RecordsProcessed,
				"entities", len(entities),
			)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("stream redfin %s: %w", level, err)
	}

	s.logger.Info("Redfin stream complete, building upserts",
		"level", level,
		"records", result.RecordsProcessed,
		"entities", len(entities),
	)

	// Build batch statements from aggregated entities
	var stmts []batchStmt
	for _, ent := range entities {
		monthsJSON, err := json.Marshal(ent.months)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("marshal %s: %v", ent.name, err))
			continue
		}

		switch level {
		case "national":
			stmts = append(stmts, batchStmt{
				sql:   upsertMetroRedfinSQL,
				args:  []any{int32(0), "United States", textOrNull(""), monthsJSON},
				label: "United States",
			})
		case "state":
			stmts = append(stmts, batchStmt{
				sql:   upsertStateRedfinSQL,
				args:  []any{ent.regionID, ent.stateCode, ent.name, monthsJSON},
				label: ent.name,
			})
		case "metro":
			stmts = append(stmts, batchStmt{
				sql:   upsertMetroRedfinSQL,
				args:  []any{ent.regionID, ent.name, textOrNull(ent.stateCode), monthsJSON},
				label: ent.name,
			})
		case "county":
			stmts = append(stmts, batchStmt{
				sql:   upsertCountyRedfinSQL,
				args:  []any{ent.regionID, textOrNull(ent.fips), ent.name, ent.stateCode, int4OrNull(ent.metroID), monthsJSON},
				label: ent.name,
			})
		case "city":
			stmts = append(stmts, batchStmt{
				sql:   upsertCityRedfinSQL,
				args:  []any{ent.regionID, ent.name, textOrNull(ent.stateCode), monthsJSON},
				label: ent.name,
			})
		case "zip":
			stmts = append(stmts, batchStmt{
				sql:   upsertZipRedfinSQL,
				args:  []any{ent.name, textOrNull(ent.stateCode), monthsJSON},
				label: ent.name,
			})
		}
	}

	// Batch execute
	upserted, errs := batchExec(ctx, s.marketDB, stmts, s.logger)
	result.RecordsUpserted = upserted
	result.Errors = append(result.Errors, errs...)

	result.Duration = time.Since(start)
	s.logger.Info("Redfin import complete",
		"level", level,
		"records", result.RecordsProcessed,
		"entities", len(entities),
		"upserted", result.RecordsUpserted,
		"duration", result.Duration,
	)
	return result, nil
}

// ComputeNationalAggregates computes national-level data from state/metro data.
func (s *Service) ComputeNationalAggregates(ctx context.Context) (*ImportResult, error) {
	return computeNational(ctx, s.marketDB, s.queries, s.logger)
}

// FullRefresh runs all import jobs in sequence.
func (s *Service) FullRefresh(ctx context.Context) (*ImportResult, error) {
	start := time.Now()
	result := &ImportResult{Source: "full_refresh", Level: "all"}

	jobs := []struct {
		name string
		fn   func() (*ImportResult, error)
	}{
		{"zhvi_metro", func() (*ImportResult, error) { return s.ImportZillowZHVI(ctx, "metro") }},
		{"zhvi_city", func() (*ImportResult, error) { return s.ImportZillowZHVI(ctx, "city") }},
		{"zhvi_zip", func() (*ImportResult, error) { return s.ImportZillowZHVI(ctx, "zip") }},
		{"zhvi_state", func() (*ImportResult, error) { return s.ImportZillowZHVI(ctx, "state") }},
		{"zori_metro", func() (*ImportResult, error) { return s.ImportZillowZORI(ctx, "metro") }},
		{"zori_city", func() (*ImportResult, error) { return s.ImportZillowZORI(ctx, "city") }},
		{"zori_zip", func() (*ImportResult, error) { return s.ImportZillowZORI(ctx, "zip") }},
		{"zhvf", func() (*ImportResult, error) { return s.ImportZillowForecasts(ctx) }},
		{"metrics", func() (*ImportResult, error) { return s.ImportZillowMetrics(ctx) }},
		{"redfin_national", func() (*ImportResult, error) { return s.ImportRedfinData(ctx, "national") }},
		{"redfin_state", func() (*ImportResult, error) { return s.ImportRedfinData(ctx, "state") }},
		{"redfin_metro", func() (*ImportResult, error) { return s.ImportRedfinData(ctx, "metro") }},
		{"redfin_county", func() (*ImportResult, error) { return s.ImportRedfinData(ctx, "county") }},
		{"compute_national", func() (*ImportResult, error) { return s.ComputeNationalAggregates(ctx) }},
	}

	for _, job := range jobs {
		s.logger.Info("full refresh: starting job", "job", job.name)
		r, err := job.fn()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", job.name, err))
			s.logger.Error("full refresh: job failed", "job", job.name, "error", err)
			continue
		}
		result.RecordsProcessed += r.RecordsProcessed
		result.RecordsUpserted += r.RecordsUpserted
		result.RecordsSkipped += r.RecordsSkipped
	}

	result.Duration = time.Since(start)
	s.logger.Info("full refresh complete",
		"processed", result.RecordsProcessed,
		"upserted", result.RecordsUpserted,
		"errors", len(result.Errors),
		"duration", result.Duration,
	)
	return result, nil
}

// Status returns row counts and freshness for all market tables.
func (s *Service) Status(ctx context.Context) (*StatusResult, error) {
	status := &StatusResult{}

	// Metro
	metroCount, err := s.queries.GetMetroCount(ctx)
	if err == nil {
		status.Metro.RowCount = metroCount
	}
	metroFresh, err := s.queries.GetMetroFreshness(ctx)
	if err == nil {
		status.Metro.LatestUpdate = toTimePtr(metroFresh)
	}

	// City time-series
	cityCount, err := s.queries.GetCityTimeSeriesCount(ctx)
	if err == nil {
		status.City.RowCount = cityCount
	}
	cityFresh, err := s.queries.GetCityTimeSeriesFreshness(ctx)
	if err == nil {
		status.City.LatestUpdate = toTimePtr(cityFresh)
	}

	// State
	stateCount, err := s.queries.GetStateTimeSeriesCount(ctx)
	if err == nil {
		status.State.RowCount = stateCount
	}
	stateFresh, err := s.queries.GetStateFreshness(ctx)
	if err == nil {
		status.State.LatestUpdate = toTimePtr(stateFresh)
	}

	// Zip
	zipCount, err := s.queries.GetZipTimeSeriesCount(ctx)
	if err == nil {
		status.Zip.RowCount = zipCount
	}
	zipFresh, err := s.queries.GetZipFreshness(ctx)
	if err == nil {
		status.Zip.LatestUpdate = toTimePtr(zipFresh)
	}

	// County
	countyCount, err := s.queries.GetCountyTimeSeriesCount(ctx)
	if err == nil {
		status.County.RowCount = countyCount
	}
	countyFresh, err := s.queries.GetCountyFreshness(ctx)
	if err == nil {
		status.County.LatestUpdate = toTimePtr(countyFresh)
	}

	return status, nil
}

// toTimePtr converts an interface{} (from sqlc MAX() aggregate) to *time.Time.
func toTimePtr(v any) *time.Time {
	if v == nil {
		return nil
	}
	if t, ok := v.(time.Time); ok {
		return &t
	}
	return nil
}

// buildMetroLookup returns a map of lowercase metro name → metro_region_id.
func (s *Service) buildMetroLookup(ctx context.Context) (map[string]int32, error) {
	metros, err := s.queries.GetAllMetroLookup(ctx)
	if err != nil {
		return nil, err
	}
	lookup := make(map[string]int32, len(metros))
	for _, m := range metros {
		key := normalizeMetroName(m.MetroName)
		lookup[key] = m.MetroRegionID
	}
	return lookup, nil
}
