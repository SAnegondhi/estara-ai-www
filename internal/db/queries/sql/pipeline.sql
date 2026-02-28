-- ADR-101: Investment Pipeline queries

-- ============================================================
-- PIPELINE DEALS
-- ============================================================

-- name: CreatePipelineDeal :one
INSERT INTO pipeline_deals (
    id, user_id, name, source, status, notes,
    property_count, memo_count, portfolio_excluded, last_activity_at,
    created_at, updated_at
) VALUES (
    gen_random_uuid(), $1, $2, $3, 'under_review', $4,
    0, 0, $5, NULL,
    NOW(), NOW()
) RETURNING *;

-- name: GetPipelineDeal :one
SELECT * FROM pipeline_deals
WHERE id = $1 AND user_id = $2;

-- name: ListPipelineDeals :many
SELECT * FROM pipeline_deals
WHERE user_id = $1
  AND (sqlc.arg('include_archived')::boolean = TRUE OR status NOT IN ('passed', 'closed'))
ORDER BY
    CASE WHEN status IN ('passed', 'closed') THEN 1 ELSE 0 END,
    COALESCE(last_activity_at, created_at) DESC;

-- name: UpdatePipelineDeal :one
UPDATE pipeline_deals SET
    name               = COALESCE(sqlc.narg('name')::text, name),
    source             = COALESCE(sqlc.narg('source')::text, source),
    status             = COALESCE(sqlc.narg('status')::text, status),
    notes              = COALESCE(sqlc.narg('notes')::text, notes),
    portfolio_excluded = COALESCE(sqlc.narg('portfolio_excluded')::boolean, portfolio_excluded),
    updated_at         = NOW()
WHERE id = $1 AND user_id = $2
RETURNING *;

-- name: UpdatePipelineDealStatus :one
UPDATE pipeline_deals SET
    status     = $3,
    updated_at = NOW()
WHERE id = $1 AND user_id = $2
RETURNING *;

-- name: BumpPipelineDealActivity :exec
UPDATE pipeline_deals SET
    property_count   = (SELECT COUNT(*) FROM pipeline_properties WHERE pipeline_deal_id = $1),
    last_activity_at = NOW(),
    updated_at       = NOW()
WHERE id = $1;

-- name: BumpPipelineDealMemoCount :exec
UPDATE pipeline_deals SET
    memo_count       = memo_count + 1,
    status           = CASE WHEN status = 'under_review' THEN 'memo_generated' ELSE status END,
    last_activity_at = NOW(),
    updated_at       = NOW()
WHERE id = $1;

-- name: DeletePipelineDeal :execrows
DELETE FROM pipeline_deals WHERE id = $1 AND user_id = $2;

-- name: GetPipelineStats :one
WITH prop_agg AS (
    SELECT
        COALESCE(SUM(COALESCE(pp.target_price, pp.asking_price)), 0)          AS total_pipeline_value,
        COUNT(*) FILTER (WHERE pp.system_rent IS NOT NULL)                     AS total_properties_with_rent_data,
        COUNT(*) FILTER (
            WHERE pp.system_rent IS NOT NULL
              AND COALESCE(pp.target_price, pp.asking_price) > 0
        )                                                                      AS weighted_avg_cap_rate_property_count,
        CASE
            WHEN SUM(COALESCE(pp.target_price, pp.asking_price)) FILTER (
                WHERE pp.system_rent IS NOT NULL
                  AND COALESCE(pp.target_price, pp.asking_price) > 0
            ) > 0
            THEN SUM(pp.system_rent * 12 * 0.58) FILTER (
                WHERE pp.system_rent IS NOT NULL
                  AND COALESCE(pp.target_price, pp.asking_price) > 0
            ) / SUM(COALESCE(pp.target_price, pp.asking_price)) FILTER (
                WHERE pp.system_rent IS NOT NULL
                  AND COALESCE(pp.target_price, pp.asking_price) > 0
            )
            ELSE NULL
        END                                                                    AS weighted_avg_cap_rate
    FROM pipeline_properties pp
    JOIN pipeline_deals pd ON pd.id = pp.pipeline_deal_id
    WHERE pd.user_id = $1
      AND pd.status NOT IN ('passed', 'closed')
)
SELECT
    COUNT(*) FILTER (WHERE status NOT IN ('passed', 'closed'))                                    AS active_deals,
    COALESCE(SUM(property_count) FILTER (WHERE status NOT IN ('passed', 'closed')), 0)::bigint    AS properties_in_pipeline,
    COALESCE(SUM(memo_count), 0)::bigint                                                          AS decision_memos_generated,
    COUNT(*) FILTER (WHERE status = 'under_review')                                               AS deals_pending_decision,
    COUNT(*) FILTER (WHERE status = 'proceeding')                                                 AS qualified_count,
    COUNT(*) FILTER (WHERE status IN ('under_review', 'memo_generated'))                          AS pending_count,
    COUNT(*) FILTER (WHERE status = 'passed')                                                     AS passed_count,
    COUNT(*) FILTER (WHERE source = 'broker')                                                     AS source_broker,
    COUNT(*) FILTER (WHERE source = 'off-market')                                                 AS source_off_market,
    COUNT(*) FILTER (WHERE source = 'syndication')                                                AS source_syndication,
    COUNT(*) FILTER (WHERE source = 'jv')                                                         AS source_jv,
    COUNT(*) FILTER (WHERE source = 'auction')                                                    AS source_auction,
    COUNT(*) FILTER (WHERE source = 'direct')                                                     AS source_direct,
    COUNT(*) FILTER (WHERE source = 'other')                                                      AS source_other,
    COALESCE((SELECT total_pipeline_value FROM prop_agg), 0)::double precision                    AS total_pipeline_value,
    (SELECT weighted_avg_cap_rate FROM prop_agg)::double precision                                AS weighted_avg_cap_rate,
    (SELECT weighted_avg_cap_rate_property_count FROM prop_agg)::bigint                           AS weighted_avg_cap_rate_property_count,
    (SELECT total_properties_with_rent_data FROM prop_agg)::bigint                                AS total_properties_with_rent_data
FROM pipeline_deals
WHERE pipeline_deals.user_id = $1;

-- ============================================================
-- PIPELINE PROPERTIES
-- ============================================================

-- name: CreatePipelineProperty :one
INSERT INTO pipeline_properties (
    id, pipeline_deal_id, address, city, state, zip,
    property_type, beds, baths, sqft, year_built, units,
    asking_price, target_price, down_payment_pct, financing_type, interest_rate,
    broker_rent, system_rent, current_occupancy,
    expense_overrides, source_type,
    created_at, updated_at
) VALUES (
    gen_random_uuid(), $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19,
    $20, $21,
    NOW(), NOW()
) RETURNING *;

-- name: GetPipelineProperty :one
SELECT pp.* FROM pipeline_properties pp
JOIN pipeline_deals pd ON pd.id = pp.pipeline_deal_id
WHERE pp.id = $1 AND pd.user_id = $2;

-- name: ListPipelineProperties :many
SELECT pp.* FROM pipeline_properties pp
JOIN pipeline_deals pd ON pd.id = pp.pipeline_deal_id
WHERE pp.pipeline_deal_id = $1 AND pd.user_id = $2
ORDER BY pp.created_at ASC;

-- name: UpdatePipelineProperty :one
UPDATE pipeline_properties SET
    address           = COALESCE(sqlc.narg('address')::text, address),
    city              = COALESCE(sqlc.narg('city')::text, city),
    state             = COALESCE(sqlc.narg('state')::text, state),
    zip               = COALESCE(sqlc.narg('zip')::text, zip),
    property_type     = COALESCE(sqlc.narg('property_type')::text, property_type),
    beds              = COALESCE(sqlc.narg('beds')::numeric, beds),
    baths             = COALESCE(sqlc.narg('baths')::numeric, baths),
    sqft              = COALESCE(sqlc.narg('sqft')::integer, sqft),
    year_built        = COALESCE(sqlc.narg('year_built')::integer, year_built),
    units             = COALESCE(sqlc.narg('units')::integer, units),
    asking_price      = COALESCE(sqlc.narg('asking_price')::numeric, asking_price),
    target_price      = COALESCE(sqlc.narg('target_price')::numeric, target_price),
    down_payment_pct  = COALESCE(sqlc.narg('down_payment_pct')::numeric, down_payment_pct),
    financing_type    = COALESCE(sqlc.narg('financing_type')::text, financing_type),
    interest_rate     = COALESCE(sqlc.narg('interest_rate')::numeric, interest_rate),
    broker_rent       = COALESCE(sqlc.narg('broker_rent')::numeric, broker_rent),
    system_rent       = COALESCE(sqlc.narg('system_rent')::numeric, system_rent),
    current_occupancy = COALESCE(sqlc.narg('current_occupancy')::numeric, current_occupancy),
    expense_overrides = COALESCE(sqlc.narg('expense_overrides')::jsonb, expense_overrides),
    updated_at        = NOW()
WHERE id = $1
RETURNING *;

-- name: DeletePipelineProperty :execrows
DELETE FROM pipeline_properties pp
USING pipeline_deals pd
WHERE pp.id = $1
  AND pp.pipeline_deal_id = pd.id
  AND pd.user_id = $2;
