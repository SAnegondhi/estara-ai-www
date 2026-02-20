-- ADR-087 Phase 7: Data Source Version Tracking Queries
-- Event-driven refresh based on data source version changes

-- name: UpdateDataSourceVersion :exec
-- Updates or inserts a data source version after import completes
INSERT INTO data_source_versions (source, version, last_updated, import_job_id, metadata)
VALUES ($1, $2, NOW(), sqlc.narg('import_job_id'), COALESCE(sqlc.narg('metadata'), '{}'::JSONB))
ON CONFLICT (source) DO UPDATE
SET version = EXCLUDED.version,
    last_updated = EXCLUDED.last_updated,
    import_job_id = EXCLUDED.import_job_id,
    metadata = EXCLUDED.metadata;

-- name: GetCurrentVersion :one
-- Gets the current version for a specific data source
SELECT version FROM data_source_versions WHERE source = $1;

-- name: GetAllVersions :many
-- Gets all data source versions (for staleness detection)
SELECT source, version, last_updated, import_job_id, metadata
FROM data_source_versions
ORDER BY last_updated DESC;

-- name: GetVersionsMap :many
-- Returns all versions as key-value pairs for easy lookup
SELECT source, version FROM data_source_versions;

-- name: ListStaleReportsByZHVI :many
-- Finds reports with outdated ZHVI version, ordered by access count (for proactive refresh)
SELECT id, location, cache_key, zhvi_version, accessed_count, last_accessed_at
FROM market_analysis_reports
WHERE status = 'completed'
  AND zhvi_version IS NOT NULL
  AND zhvi_version != $1
ORDER BY accessed_count DESC
LIMIT $2;

-- name: ListStaleReportsByZORI :many
-- Finds reports with outdated ZORI version, ordered by access count (for proactive refresh)
SELECT id, location, cache_key, zori_version, accessed_count, last_accessed_at
FROM market_analysis_reports
WHERE status = 'completed'
  AND zori_version IS NOT NULL
  AND zori_version != $1
ORDER BY accessed_count DESC
LIMIT $2;

-- name: ListStaleReportsByRedfin :many
-- Finds reports with outdated Redfin version, ordered by access count (for proactive refresh)
SELECT id, location, cache_key, redfin_version, accessed_count, last_accessed_at
FROM market_analysis_reports
WHERE status = 'completed'
  AND redfin_version IS NOT NULL
  AND redfin_version != $1
ORDER BY accessed_count DESC
LIMIT $2;

-- name: ListStaleReportsByFRED :many
-- Finds reports with outdated FRED version, ordered by access count (for proactive refresh)
SELECT id, location, cache_key, fred_version, accessed_count, last_accessed_at
FROM market_analysis_reports
WHERE status = 'completed'
  AND fred_version IS NOT NULL
  AND fred_version != $1
ORDER BY accessed_count DESC
LIMIT $2;

-- name: ListStaleReportsByCensus :many
-- Finds reports with outdated Census version, ordered by access count (for proactive refresh)
SELECT id, location, cache_key, census_version, accessed_count, last_accessed_at
FROM market_analysis_reports
WHERE status = 'completed'
  AND census_version IS NOT NULL
  AND census_version != $1
ORDER BY accessed_count DESC
LIMIT $2;

-- name: CountStaleReportsBySource :one
-- Counts how many reports are stale for a given data source
SELECT COUNT(*) FROM market_analysis_reports
WHERE status = 'completed'
  AND CASE
    WHEN sqlc.arg('source') = 'zhvi' THEN zhvi_version != sqlc.arg('current_version')
    WHEN sqlc.arg('source') = 'zori' THEN zori_version != sqlc.arg('current_version')
    WHEN sqlc.arg('source') = 'redfin' THEN redfin_version != sqlc.arg('current_version')
    WHEN sqlc.arg('source') = 'fred' THEN fred_version != sqlc.arg('current_version')
    WHEN sqlc.arg('source') = 'census' THEN census_version != sqlc.arg('current_version')
    ELSE FALSE
  END;

-- name: GetReportVersions :one
-- Gets all data source versions for a specific report (staleness check)
SELECT zhvi_version, zori_version, redfin_version, fred_version, census_version, ai_context_version
FROM market_analysis_reports
WHERE id = $1;

-- name: UpdateReportVersions :exec
-- Updates data source versions after selective refresh
UPDATE market_analysis_reports
SET zhvi_version = COALESCE(sqlc.narg('zhvi_version'), zhvi_version),
    zori_version = COALESCE(sqlc.narg('zori_version'), zori_version),
    redfin_version = COALESCE(sqlc.narg('redfin_version'), redfin_version),
    fred_version = COALESCE(sqlc.narg('fred_version'), fred_version),
    census_version = COALESCE(sqlc.narg('census_version'), census_version),
    ai_context_version = COALESCE(sqlc.narg('ai_context_version'), ai_context_version),
    updated_at = NOW()
WHERE id = $1;
