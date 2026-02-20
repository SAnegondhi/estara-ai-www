-- Market Analysis Reports Queries (ADR-087)

-- name: CreateMarketAnalysisReport :one
INSERT INTO market_analysis_reports (
    id, location, location_normalized, cache_key, status,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, NOW(), NOW()
)
RETURNING *;

-- name: GetReportByCacheKey :one
SELECT * FROM market_analysis_reports
WHERE cache_key = $1;

-- name: GetReportByID :one
SELECT * FROM market_analysis_reports
WHERE id = $1;

-- name: ListRecentReports :many
SELECT * FROM market_analysis_reports
WHERE status = 'completed'
ORDER BY generated_at DESC
LIMIT $1 OFFSET $2;

-- name: ListAllReports :many
SELECT * FROM market_analysis_reports
ORDER BY generated_at DESC NULLS LAST
LIMIT $1 OFFSET $2;

-- name: ListReportsWithSearch :many
SELECT * FROM market_analysis_reports
WHERE location ILIKE '%' || sqlc.narg('search') || '%'
ORDER BY generated_at DESC NULLS LAST
LIMIT $1 OFFSET $2;

-- name: CountReports :one
SELECT COUNT(*) FROM market_analysis_reports;

-- name: CountCompletedReports :one
SELECT COUNT(*) FROM market_analysis_reports
WHERE status = 'completed';

-- name: CountReportsWithSearch :one
SELECT COUNT(*) FROM market_analysis_reports
WHERE location ILIKE '%' || sqlc.narg('search') || '%';

-- name: UpdateReportStatus :exec
UPDATE market_analysis_reports
SET status = $2,
    error_message = $3,
    generated_at = CASE WHEN $2 = 'completed' THEN NOW() ELSE generated_at END,
    updated_at = NOW()
WHERE cache_key = $1;

-- name: UpdateReportStatusWithMetadata :exec
UPDATE market_analysis_reports
SET status = $2,
    error_message = $3,
    generated_at = CASE WHEN $2 = 'completed' THEN NOW() ELSE generated_at END,
    data_freshness_date = $4,
    report_size_bytes = $5,
    updated_at = NOW()
WHERE cache_key = $1;

-- name: UpdateReportStatusWithVersions :exec
-- Updates report status and captures data source versions (ADR-087 Phase 7)
UPDATE market_analysis_reports
SET status = $2,
    error_message = sqlc.narg('error_message'),
    generated_at = CASE WHEN $2 = 'completed' THEN NOW() ELSE generated_at END,
    data_freshness_date = sqlc.narg('data_freshness_date'),
    report_size_bytes = sqlc.narg('report_size_bytes'),
    zhvi_version = sqlc.narg('zhvi_version'),
    zori_version = sqlc.narg('zori_version'),
    redfin_version = sqlc.narg('redfin_version'),
    fred_version = sqlc.narg('fred_version'),
    census_version = sqlc.narg('census_version'),
    ai_context_version = sqlc.narg('ai_context_version'),
    updated_at = NOW()
WHERE cache_key = $1;

-- name: IncrementReportAccessCount :exec
UPDATE market_analysis_reports
SET accessed_count = accessed_count + 1,
    last_accessed_at = NOW(),
    updated_at = NOW()
WHERE cache_key = $1;

-- name: DeleteReport :exec
DELETE FROM market_analysis_reports
WHERE id = $1;

-- name: DeleteReportByCacheKey :exec
DELETE FROM market_analysis_reports
WHERE cache_key = $1;

-- User Access Tracking

-- name: TrackUserAccess :exec
INSERT INTO user_market_analysis_access (
    id, user_id, report_id, accessed_at
) VALUES (
    $1, $2, $3, NOW()
)
ON CONFLICT (user_id, report_id) DO UPDATE
SET accessed_at = NOW();

-- name: GetUserRecentAccess :many
SELECT
    mar.*
FROM user_market_analysis_access umaa
JOIN market_analysis_reports mar ON umaa.report_id = mar.id
WHERE umaa.user_id = $1
ORDER BY umaa.accessed_at DESC
LIMIT $2 OFFSET $3;

-- name: GetUserRecentAccessWithSearch :many
SELECT
    mar.*
FROM user_market_analysis_access umaa
JOIN market_analysis_reports mar ON umaa.report_id = mar.id
WHERE umaa.user_id = $1
  AND (sqlc.narg('search')::text IS NULL OR mar.location ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY umaa.accessed_at DESC
LIMIT $2 OFFSET $3;

-- name: CountUserAccess :one
SELECT COUNT(*) FROM user_market_analysis_access
WHERE user_id = $1;

-- name: CountUserAccessWithSearch :one
SELECT COUNT(*)
FROM user_market_analysis_access umaa
JOIN market_analysis_reports mar ON umaa.report_id = mar.id
WHERE umaa.user_id = $1
  AND (sqlc.narg('search')::text IS NULL OR mar.location ILIKE '%' || sqlc.narg('search') || '%');

-- name: DeleteUserAccess :exec
DELETE FROM user_market_analysis_access
WHERE user_id = $1 AND report_id = $2;

-- name: GetReportStats :one
SELECT
    COUNT(*) as total_reports,
    COUNT(*) FILTER (WHERE status = 'completed') as completed_reports,
    COUNT(*) FILTER (WHERE status = 'pending') as pending_reports,
    COUNT(*) FILTER (WHERE status = 'failed') as failed_reports,
    COUNT(DISTINCT location_normalized) as unique_locations,
    SUM(accessed_count) as total_accesses
FROM market_analysis_reports;
