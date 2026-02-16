-- Discovery Session Queries

-- name: CreateDiscoverySession :one
INSERT INTO discovery_sessions (
    id, "userId", "searchCriteria", location, "propertyCount",
    "cachedPropertyIds", status, "createdAt", "updatedAt", "lastAccessedAt", "expiresAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, 'ACTIVE', NOW(), NOW(), NOW(), NOW() + INTERVAL '180 days'
) RETURNING *;

-- name: GetDiscoverySession :one
SELECT * FROM discovery_sessions WHERE id = $1;

-- name: GetDiscoverySessionByUser :one
SELECT * FROM discovery_sessions WHERE id = $1 AND "userId" = $2;

-- name: ListUserDiscoverySessions :many
SELECT * FROM discovery_sessions
WHERE "userId" = $1 AND status = $2
ORDER BY "lastAccessedAt" DESC
LIMIT $3 OFFSET $4;

-- name: CountUserDiscoverySessions :one
SELECT COUNT(*) FROM discovery_sessions
WHERE "userId" = $1 AND status = $2;

-- name: UpdateDiscoverySessionAccess :one
UPDATE discovery_sessions SET
    "lastAccessedAt" = NOW(),
    "updatedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateDiscoverySession :one
UPDATE discovery_sessions SET
    name = COALESCE($2, name),
    notes = COALESCE($3, notes),
    "updatedAt" = NOW()
WHERE id = $1 AND "userId" = $2
RETURNING *;

-- name: ArchiveDiscoverySession :one
UPDATE discovery_sessions SET
    status = 'ARCHIVED',
    "archivedAt" = NOW(),
    "updatedAt" = NOW()
WHERE id = $1 AND "userId" = $2
RETURNING *;

-- name: RestoreDiscoverySession :one
UPDATE discovery_sessions SET
    status = 'ACTIVE',
    "archivedAt" = NULL,
    "updatedAt" = NOW()
WHERE id = $1 AND "userId" = $2
RETURNING *;

-- name: AutoArchiveOldSessions :exec
UPDATE discovery_sessions SET
    status = 'ARCHIVED',
    "archivedAt" = NOW(),
    "updatedAt" = NOW()
WHERE status = 'ACTIVE'
  AND "createdAt" < NOW() - INTERVAL '30 days';

-- name: AutoArchiveOldSessionsRows :execrows
UPDATE discovery_sessions SET
    status = 'ARCHIVED',
    "archivedAt" = NOW(),
    "updatedAt" = NOW()
WHERE status = 'ACTIVE'
  AND "createdAt" < NOW() - INTERVAL '30 days';

-- name: DeleteExpiredSessions :exec
DELETE FROM discovery_sessions
WHERE "expiresAt" < NOW();

-- name: DeleteExpiredSessionsRows :execrows
DELETE FROM discovery_sessions
WHERE "expiresAt" < NOW();

-- name: IncrementChatSessionCount :exec
UPDATE discovery_sessions SET
    "chatSessionCount" = "chatSessionCount" + 1,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: IncrementEvaluationCount :exec
UPDATE discovery_sessions SET
    "evaluationCount" = "evaluationCount" + 1,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: UpdateSessionPriceStats :exec
UPDATE discovery_sessions SET
    median_price = stats.med,
    mode_price = stats.mod
FROM (
    SELECT
        percentile_cont(0.5) WITHIN GROUP (ORDER BY price)::int AS med,
        mode() WITHIN GROUP (ORDER BY price) AS mod
    FROM discovery_session_properties
    WHERE "discoverySessionId" = $1
) stats
WHERE discovery_sessions.id = $1;

-- name: UpdateSessionEvaluationCount :exec
UPDATE discovery_sessions SET
    "evaluationCount" = (SELECT COUNT(*) FROM discovery_session_evaluations WHERE "discoverySessionId" = $1),
    "updatedAt" = NOW()
WHERE id = $1;

-- name: SessionExistsByUser :one
SELECT EXISTS(SELECT 1 FROM discovery_sessions WHERE id = $1 AND "userId" = $2);

-- Activity Linking Queries

-- name: CreateActivityLink :one
INSERT INTO discovery_session_activities (
    id, "discoverySessionId", "activityType", "activityId", "createdAt"
) VALUES (
    $1, $2, $3, $4, NOW()
)
ON CONFLICT ("discoverySessionId", "activityType", "activityId") DO NOTHING
RETURNING *;

-- name: ListSessionActivities :many
SELECT * FROM discovery_session_activities
WHERE "discoverySessionId" = $1
ORDER BY "createdAt" DESC;

-- name: GetActivityLink :one
SELECT * FROM discovery_session_activities
WHERE "discoverySessionId" = $1 AND "activityType" = $2 AND "activityId" = $3;

-- name: DeleteActivityLink :exec
DELETE FROM discovery_session_activities
WHERE "discoverySessionId" = $1 AND "activityType" = $2 AND "activityId" = $3;

-- name: CountSessionActivitiesByType :one
SELECT COUNT(*) FROM discovery_session_activities
WHERE "discoverySessionId" = $1 AND "activityType" = $2;

-- Discovery Session Properties Queries

-- name: CreateDiscoverySessionProperty :one
INSERT INTO discovery_session_properties (
    id, "discoverySessionId", "listingId", address, city, state, "zipCode",
    price, "estimatedRent", "capRateMin", "capRateMax", beds, baths, sqft,
    "yearBuilt", "propertyType", "listingDate", "daysOnMarket", "imageUrl",
    "listingSearchUrl", "googleSearchUrl", latitude, longitude, "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, NOW()
)
ON CONFLICT ("discoverySessionId", "listingId") DO NOTHING
RETURNING *;

-- name: ListSessionProperties :many
SELECT * FROM discovery_session_properties
WHERE "discoverySessionId" = $1
ORDER BY price ASC;

-- name: GetSessionProperty :one
SELECT * FROM discovery_session_properties
WHERE "discoverySessionId" = $1 AND "listingId" = $2;

-- name: DeleteSessionProperties :exec
DELETE FROM discovery_session_properties
WHERE "discoverySessionId" = $1;

-- name: CountSessionProperties :one
SELECT COUNT(*) FROM discovery_session_properties
WHERE "discoverySessionId" = $1;

-- Discovery Session Evaluations Queries

-- name: CreateDiscoverySessionEvaluation :one
INSERT INTO discovery_session_evaluations (
    id, "discoverySessionId", "propertyId", address, price, "estimatedRent",
    scenarios, recommendation, "riskLevel", score, status, "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW()
)
ON CONFLICT ("discoverySessionId", "propertyId") DO UPDATE SET
    scenarios = EXCLUDED.scenarios,
    recommendation = EXCLUDED.recommendation,
    "riskLevel" = EXCLUDED."riskLevel",
    score = EXCLUDED.score,
    status = EXCLUDED.status
RETURNING *;

-- name: ListSessionEvaluations :many
SELECT * FROM discovery_session_evaluations
WHERE "discoverySessionId" = $1
ORDER BY "createdAt" DESC;

-- name: GetSessionEvaluation :one
SELECT * FROM discovery_session_evaluations
WHERE "discoverySessionId" = $1 AND "propertyId" = $2;

-- name: DeleteSessionEvaluations :exec
DELETE FROM discovery_session_evaluations
WHERE "discoverySessionId" = $1;

-- name: CountSessionEvaluations :one
SELECT COUNT(*) FROM discovery_session_evaluations
WHERE "discoverySessionId" = $1;
