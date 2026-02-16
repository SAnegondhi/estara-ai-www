-- Analysis Jobs Queries

-- name: ListAnalysisJobsByUser :many
SELECT id, "userId", "jobType"::text, status::text, progress, location,
       criteria, "metricsData", "metricsError", "narrativeData", "narrativeError",
       "reportId", "queuedAt", "startedAt", "completedAt", "dismissedAt",
       error, "retryCount"
FROM analysis_jobs
WHERE "userId" = $1 AND "dismissedAt" IS NULL
ORDER BY "queuedAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: SearchAnalysisJobsByUser :many
SELECT id, "userId", "jobType"::text, status::text, progress, location,
       criteria, "metricsData", "metricsError", "narrativeData", "narrativeError",
       "reportId", "queuedAt", "startedAt", "completedAt", "dismissedAt",
       error, "retryCount"
FROM analysis_jobs
WHERE "userId" = $1 AND "dismissedAt" IS NULL
  AND LOWER(location) LIKE '%' || LOWER(sqlc.arg('search')::text) || '%'
ORDER BY "queuedAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetAnalysisJobByID :one
SELECT id, "userId", "jobType"::text, status::text, progress, location,
       criteria, "metricsData", "metricsError", "narrativeData", "narrativeError",
       "reportId", "queuedAt", "startedAt", "completedAt", "dismissedAt",
       error, "retryCount"
FROM analysis_jobs
WHERE id = $1;

-- name: GetAnalysisJobByIDAndUser :one
SELECT id, "userId", "jobType"::text, status::text, progress, location,
       criteria, "metricsData", "metricsError", "narrativeData", "narrativeError",
       "reportId", "queuedAt", "startedAt", "completedAt", "dismissedAt",
       error, "retryCount"
FROM analysis_jobs
WHERE id = $1 AND "userId" = $2;

-- name: CreateAnalysisJob :one
INSERT INTO analysis_jobs (
    id, "userId", "jobType", status, progress, location, criteria, "queuedAt"
) VALUES ($1, $2, $3::text::"AnalysisJobType", $4::text::"AnalysisStatus", $5, $6, $7, NOW())
RETURNING id, "userId", "jobType"::text, status::text, progress, location,
          criteria, "metricsData", "metricsError", "narrativeData", "narrativeError",
          "reportId", "queuedAt", "startedAt", "completedAt", "dismissedAt",
          error, "retryCount";

-- name: UpdateAnalysisJobStatus :exec
UPDATE analysis_jobs
SET status = $2::text::"AnalysisStatus",
    progress = $3,
    "startedAt" = COALESCE(sqlc.narg('started_at')::timestamp, "startedAt"),
    "completedAt" = COALESCE(sqlc.narg('completed_at')::timestamp, "completedAt"),
    error = COALESCE(sqlc.narg('error')::text, error)
WHERE id = $1;

-- name: UpdateAnalysisJobResults :exec
UPDATE analysis_jobs
SET "metricsData" = COALESCE(sqlc.narg('metrics_data')::jsonb, "metricsData"),
    "metricsError" = COALESCE(sqlc.narg('metrics_error')::text, "metricsError"),
    "narrativeData" = COALESCE(sqlc.narg('narrative_data')::jsonb, "narrativeData"),
    "narrativeError" = COALESCE(sqlc.narg('narrative_error')::text, "narrativeError"),
    "reportId" = COALESCE(sqlc.narg('report_id')::text, "reportId")
WHERE id = $1;

-- name: DismissAnalysisJob :exec
UPDATE analysis_jobs
SET "dismissedAt" = NOW()
WHERE id = $1 AND "userId" = $2;

-- name: CountAnalysisJobsByUser :one
SELECT COUNT(*) FROM analysis_jobs
WHERE "userId" = $1 AND "dismissedAt" IS NULL;

-- name: DeleteOldAnalysisJobs :exec
DELETE FROM analysis_jobs
WHERE "completedAt" < NOW() - INTERVAL '90 days'
  AND "dismissedAt" IS NOT NULL;
