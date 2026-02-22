-- ADR-088 Phase 13: Frontier Runs Queries (Saved Analyses)

-- name: InsertFrontierRun :one
INSERT INTO frontier_runs (
    id, user_id, name, locations, criteria, frontier_points, property_count, strategy, best_sharpe
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
) RETURNING id, name, created_at;

-- name: ListFrontierRuns :many
SELECT id, user_id, name, locations, property_count, strategy, best_sharpe, share_token, created_at, accessed_at
FROM frontier_runs
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetFrontierRun :one
SELECT * FROM frontier_runs
WHERE id = $1 AND user_id = $2;

-- name: GetFrontierRunByToken :one
SELECT * FROM frontier_runs
WHERE share_token = $1;

-- name: SetFrontierRunShareToken :exec
UPDATE frontier_runs
SET share_token = $3
WHERE id = $1 AND user_id = $2;

-- name: ClearFrontierRunShareToken :exec
UPDATE frontier_runs
SET share_token = NULL
WHERE id = $1 AND user_id = $2;

-- name: DeleteFrontierRun :exec
DELETE FROM frontier_runs
WHERE id = $1 AND user_id = $2;

-- name: TouchFrontierRun :exec
UPDATE frontier_runs
SET accessed_at = NOW()
WHERE id = $1;

-- name: CountFrontierRuns :one
SELECT COUNT(*) FROM frontier_runs
WHERE user_id = $1;
