-- Property Finder Metrics Queries

-- name: CreatePropertyFinderMetric :exec
INSERT INTO property_finder_metrics (
    id, user_id, session_id, provider_used, providers_attempted,
    cache_hit, result_count, search_time_ms, total_time_ms,
    location, search_type
) VALUES (
    @id, @user_id, @session_id, @provider_used, @providers_attempted,
    @cache_hit, @result_count, @search_time_ms, @total_time_ms,
    @location, @search_type
);

-- name: GetPropertyFinderStats :one
SELECT
    COUNT(*)                                            AS total_searches,
    COUNT(*) FILTER (WHERE cache_hit = true)           AS cache_hits,
    COUNT(DISTINCT user_id) FILTER (WHERE user_id IS NOT NULL) AS unique_users,
    COALESCE(AVG(total_time_ms), 0)::bigint            AS avg_total_time_ms,
    COALESCE(AVG(result_count), 0)::bigint             AS avg_result_count
FROM property_finder_metrics
WHERE created_at >= NOW() - INTERVAL '30 days';

-- name: GetPropertyFinderStatsByProvider :many
SELECT
    provider_used,
    COUNT(*)                                   AS total_searches,
    COUNT(*) FILTER (WHERE cache_hit = true)  AS cache_hits,
    COALESCE(AVG(result_count), 0)::bigint    AS avg_results,
    COALESCE(AVG(total_time_ms), 0)::bigint   AS avg_time_ms
FROM property_finder_metrics
WHERE created_at >= NOW() - INTERVAL '30 days'
  AND provider_used IS NOT NULL
GROUP BY provider_used
ORDER BY total_searches DESC;
