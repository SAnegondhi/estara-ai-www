-- User Activity Tracking Queries (ADR-087 Phase 5)

-- name: TrackUserLocationActivity :exec
INSERT INTO user_location_activity (id, user_id, location, activity_type, created_at)
VALUES (sqlc.arg('id'), sqlc.arg('user_id'), sqlc.arg('location'), sqlc.arg('activity_type'), NOW());

-- name: UpsertUserLocationPreference :exec
INSERT INTO user_location_preferences (id, user_id, location, view_count, last_viewed_at, created_at)
VALUES (sqlc.arg('id'), sqlc.arg('user_id'), sqlc.arg('location'), 1, NOW(), NOW())
ON CONFLICT (user_id, location)
DO UPDATE SET
    view_count = user_location_preferences.view_count + 1,
    last_viewed_at = NOW();

-- name: GetUserTopLocations :many
SELECT location, view_count, last_viewed_at
FROM user_location_preferences
WHERE user_id = sqlc.arg('user_id')
ORDER BY view_count DESC, last_viewed_at DESC
LIMIT sqlc.arg('limit');

-- name: GetUserRecentActivity :many
SELECT location, activity_type, created_at
FROM user_location_activity
WHERE user_id = sqlc.arg('user_id')
ORDER BY created_at DESC
LIMIT sqlc.arg('limit');

-- name: CountUserActivityByLocation :one
SELECT COUNT(*) FROM user_location_activity
WHERE user_id = sqlc.arg('user_id')
  AND location = sqlc.arg('location');
