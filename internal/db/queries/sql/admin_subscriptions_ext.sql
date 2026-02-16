-- Admin Subscription Management Queries

-- name: CountSubscriptionsFiltered :one
SELECT COUNT(*)
FROM subscriptions s
JOIN users u ON s."userId" = u.id
WHERE (sqlc.narg('tier_filter')::text IS NULL OR s.tier::text = sqlc.narg('tier_filter'))
  AND (sqlc.narg('status_filter')::text IS NULL OR s.status::text = sqlc.narg('status_filter'))
  AND (sqlc.narg('email_search')::text IS NULL OR LOWER(u.email) LIKE '%' || LOWER(sqlc.narg('email_search')) || '%');

-- name: ListSubscriptionsFiltered :many
SELECT s.id, s."userId", u.email, u."firstName", u."lastName",
    s.tier::text, s.status::text, s."stripeSubscriptionId", s."stripeCustomerId",
    s."currentPeriodStart", s."currentPeriodEnd", s."trialEnd",
    s."cancelAtPeriodEnd", s."createdAt", s."updatedAt"
FROM subscriptions s
JOIN users u ON s."userId" = u.id
WHERE (sqlc.narg('tier_filter')::text IS NULL OR s.tier::text = sqlc.narg('tier_filter'))
  AND (sqlc.narg('status_filter')::text IS NULL OR s.status::text = sqlc.narg('status_filter'))
  AND (sqlc.narg('email_search')::text IS NULL OR LOWER(u.email) LIKE '%' || LOWER(sqlc.narg('email_search')) || '%')
ORDER BY s."createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetSubscriptionStatusStats :one
SELECT
    COUNT(*) FILTER (WHERE status::text = 'ACTIVE') AS active_count,
    COUNT(*) FILTER (WHERE status::text = 'TRIALING') AS trialing_count,
    COUNT(*) FILTER (WHERE status::text = 'PAST_DUE') AS past_due_count,
    COUNT(*) FILTER (WHERE status::text = 'FREE') AS free_count
FROM subscriptions;

-- name: GetSubscriptionDetailAdmin :one
SELECT s.id, s."userId", u.email, u."firstName", u."lastName",
    s.tier::text, s.status::text, s."stripeSubscriptionId", s."stripeCustomerId",
    s."currentPeriodStart", s."currentPeriodEnd", s."trialEnd",
    s."cancelAtPeriodEnd", s."createdAt", s."updatedAt"
FROM subscriptions s
JOIN users u ON s."userId" = u.id
WHERE s.id = $1;

-- name: GetSubscriptionStripeID :one
SELECT "stripeSubscriptionId", tier::text FROM subscriptions WHERE id = $1;

-- name: GetSubscriptionUserID :one
SELECT "userId" FROM subscriptions WHERE id = $1;

-- name: GetSubscriptionStripeAndStatus :one
SELECT "stripeSubscriptionId", status::text FROM subscriptions WHERE id = $1;

-- name: GetUserSubscriptionInfo :one
SELECT tier::text, status::text,
    COALESCE(TO_CHAR("currentPeriodStart", 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '') as start_date,
    COALESCE(TO_CHAR("currentPeriodEnd", 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '') as end_date
FROM subscriptions
WHERE "userId" = $1
ORDER BY "createdAt" DESC LIMIT 1;
