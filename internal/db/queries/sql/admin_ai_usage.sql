-- Admin AI Usage Analytics Queries

-- name: GetAIUsageStatsAllTime :one
SELECT
    COUNT(*) as total_requests,
    COALESCE(SUM("inputTokens"), 0)::bigint as total_input_tokens,
    COALESCE(SUM("outputTokens"), 0)::bigint as total_output_tokens,
    COALESCE(SUM("totalCost"), 0) as total_cost
FROM ai_usage;

-- name: GetAIUsageStatsToday :one
SELECT
    COUNT(*) as requests_today,
    COALESCE(SUM("totalCost"), 0) as cost_today
FROM ai_usage
WHERE "createdAt" >= CURRENT_DATE;

-- name: GetAIUsageStatsThisMonth :one
SELECT
    COUNT(*) as requests_this_month,
    COALESCE(SUM("totalCost"), 0) as cost_this_month
FROM ai_usage
WHERE "createdAt" >= DATE_TRUNC('month', CURRENT_DATE);

-- name: GetAIUsageCacheHitRate :one
SELECT
    COUNT(*) FILTER (WHERE "cacheHit" = true) as cached_count,
    COUNT(*) as total_count
FROM ai_usage;

-- name: CountAIUsageRecords :one
SELECT COUNT(*)
FROM ai_usage c
WHERE (sqlc.narg('user_id')::text IS NULL OR c."userId" = sqlc.narg('user_id'))
  AND (sqlc.narg('feature')::text IS NULL OR c.feature = sqlc.narg('feature'));

-- name: ListAIUsageRecords :many
SELECT c.id, c."userId", u.email, c.feature, c.model,
       c."inputTokens", c."outputTokens", c."totalCost", c."createdAt"
FROM ai_usage c
LEFT JOIN users u ON c."userId" = u.id
WHERE (sqlc.narg('user_id')::text IS NULL OR c."userId" = sqlc.narg('user_id'))
  AND (sqlc.narg('feature')::text IS NULL OR c.feature = sqlc.narg('feature'))
ORDER BY c."createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetAIUsageByFeature :many
SELECT
    feature,
    COUNT(*) as count,
    COALESCE(SUM("inputTokens"), 0)::bigint as input_tokens,
    COALESCE(SUM("outputTokens"), 0)::bigint as output_tokens,
    COALESCE(SUM("totalCost"), 0) as cost
FROM ai_usage
GROUP BY feature
ORDER BY count DESC;

-- name: GetAIUsageStatsByType :many
SELECT
    "requestType",
    COUNT(*) as count,
    COALESCE(SUM("totalCost"), 0) as cost
FROM ai_usage
GROUP BY "requestType"
ORDER BY count DESC;
