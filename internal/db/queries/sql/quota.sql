-- V2 Evaluation Quotas Queries

-- name: GetV2EvaluationQuota :one
SELECT id, user_id, tier::text, annual_limit, used_this_period,
       period_start_date, period_end_date, created_at, updated_at
FROM v2_evaluation_quotas
WHERE user_id = $1;

-- name: UpsertV2EvaluationQuota :one
INSERT INTO v2_evaluation_quotas (
    id, user_id, tier, annual_limit, used_this_period,
    period_start_date, period_end_date, created_at, updated_at
) VALUES ($1, $2, $3::text::"V2SubscriptionTier", $4, $5, $6, $7, NOW(), NOW())
ON CONFLICT (user_id) DO UPDATE SET
    tier = EXCLUDED.tier,
    annual_limit = EXCLUDED.annual_limit,
    period_start_date = EXCLUDED.period_start_date,
    period_end_date = EXCLUDED.period_end_date,
    updated_at = NOW()
RETURNING id, user_id, tier::text, annual_limit, used_this_period,
          period_start_date, period_end_date, created_at, updated_at;

-- name: IncrementV2EvaluationQuotaUsage :exec
UPDATE v2_evaluation_quotas
SET used_this_period = used_this_period + 1,
    updated_at = NOW()
WHERE user_id = $1;

-- name: ResetV2EvaluationQuotaUsage :exec
UPDATE v2_evaluation_quotas
SET used_this_period = 0,
    period_start_date = $2,
    period_end_date = $3,
    updated_at = NOW()
WHERE user_id = $1;

-- name: UpsertV2EvaluationQuotaWithPeriodReset :exec
INSERT INTO v2_evaluation_quotas (id, user_id, tier, annual_limit, used_this_period, period_start_date, period_end_date, created_at, updated_at)
VALUES ($1, $2, $3::text::"V2SubscriptionTier", $4, 0, $5, $6, NOW(), NOW())
ON CONFLICT (user_id) DO UPDATE SET
    tier = $3::text::"V2SubscriptionTier",
    annual_limit = $4,
    period_start_date = CASE WHEN v2_evaluation_quotas.period_end_date < NOW() THEN $5 ELSE v2_evaluation_quotas.period_start_date END,
    period_end_date = CASE WHEN v2_evaluation_quotas.period_end_date < NOW() THEN $6 ELSE v2_evaluation_quotas.period_end_date END,
    used_this_period = CASE WHEN v2_evaluation_quotas.period_end_date < NOW() THEN 0 ELSE v2_evaluation_quotas.used_this_period END,
    updated_at = NOW();

-- name: GetActiveSubscriptionForQuota :one
SELECT id, "userId", tier::text, status::text,
       "currentPeriodStart", "currentPeriodEnd"
FROM subscriptions
WHERE "userId" = $1 AND status::text IN ('ACTIVE', 'TRIALING')
ORDER BY "createdAt" DESC
LIMIT 1;
