-- Scenario Queries

-- name: GetScenarioByID :one
SELECT * FROM scenarios WHERE id = $1;

-- name: GetScenarioByIDAndUser :one
SELECT * FROM scenarios WHERE id = $1 AND "userId" = $2;

-- name: CreateScenario :one
INSERT INTO scenarios (
    id, "userId", name, description, parameters, results,
    tags, favorite, "hasAIParameters", "createdAt", "lastModified"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW()
) RETURNING *;

-- name: UpdateScenario :one
UPDATE scenarios SET
    name = COALESCE($2, name),
    description = COALESCE($3, description),
    parameters = COALESCE($4, parameters),
    results = COALESCE($5, results),
    tags = COALESCE($6, tags),
    favorite = COALESCE($7, favorite),
    "hasAIParameters" = COALESCE($8, "hasAIParameters"),
    "lastModified" = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteScenario :exec
DELETE FROM scenarios WHERE id = $1;

-- name: DeleteScenarioByIDAndUser :exec
DELETE FROM scenarios WHERE id = $1 AND "userId" = $2;

-- name: ListUserScenarios :many
SELECT * FROM scenarios
WHERE "userId" = $1
ORDER BY "lastModified" DESC
LIMIT $2 OFFSET $3;

-- name: ListUserFavoriteScenarios :many
SELECT * FROM scenarios
WHERE "userId" = $1 AND favorite = true
ORDER BY "lastModified" DESC
LIMIT $2 OFFSET $3;

-- name: CountUserScenarios :one
SELECT COUNT(*) FROM scenarios WHERE "userId" = $1;

-- name: SearchUserScenarios :many
SELECT * FROM scenarios
WHERE "userId" = $1 AND (name ILIKE '%' || $2 || '%' OR description ILIKE '%' || $2 || '%')
ORDER BY "lastModified" DESC
LIMIT $3 OFFSET $4;

-- name: ToggleScenarioFavorite :one
UPDATE scenarios SET
    favorite = NOT favorite,
    "lastModified" = NOW()
WHERE id = $1 AND "userId" = $2
RETURNING *;

-- name: UpdateScenarioResults :exec
UPDATE scenarios SET
    results = $2,
    "lastModified" = NOW()
WHERE id = $1;

-- User Analysis Preferences Queries

-- name: GetUserAnalysisPreference :one
SELECT * FROM user_analysis_preferences
WHERE "userId" = $1 AND "requestId" = $2;

-- name: UpsertUserAnalysisPreference :one
INSERT INTO user_analysis_preferences (
    id, "userId", "requestId", hidden, favorited, notes, "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, NOW(), NOW()
)
ON CONFLICT ("userId", "requestId")
DO UPDATE SET
    hidden = EXCLUDED.hidden,
    favorited = EXCLUDED.favorited,
    notes = EXCLUDED.notes,
    "updatedAt" = NOW()
RETURNING *;

-- name: ListUserFavoritedAnalyses :many
SELECT * FROM user_analysis_preferences
WHERE "userId" = $1 AND favorited = true
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListUserHiddenAnalyses :many
SELECT * FROM user_analysis_preferences
WHERE "userId" = $1 AND hidden = true
ORDER BY "createdAt" DESC;

-- name: DeleteUserAnalysisPreference :exec
DELETE FROM user_analysis_preferences
WHERE "userId" = $1 AND "requestId" = $2;
