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
    closed_outcome     = COALESCE(sqlc.narg('closed_outcome')::text, closed_outcome),
    input_complete     = COALESCE(sqlc.narg('input_complete')::boolean, input_complete),
    updated_at         = NOW()
WHERE id = $1 AND user_id = $2
RETURNING *;

-- name: MarkDealInputComplete :one
-- ADR-107: mark a deal as input-complete (ready for analysis).
UPDATE pipeline_deals SET
    input_complete = true,
    updated_at     = NOW()
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

-- name: SavePipelineDealMemoText :exec
-- ADR-109: persist generated decision memo text (overwrites previous).
UPDATE pipeline_deals SET
    memo_text  = $2,
    updated_at = NOW()
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
    COALESCE((SELECT weighted_avg_cap_rate FROM prop_agg), 0)::double precision                   AS weighted_avg_cap_rate,
    COALESCE((SELECT weighted_avg_cap_rate_property_count FROM prop_agg), 0)::bigint              AS weighted_avg_cap_rate_property_count,
    COALESCE((SELECT total_properties_with_rent_data FROM prop_agg), 0)::bigint                   AS total_properties_with_rent_data
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
    expense_overrides, unit_mix, lot_sqft, building_count,
    notes, source_type,
    lease_type, tenant_count, anchor_tenant, weighted_avg_lease_yrs, commercial_sqft, commercial_mix,
    year_renovated, stories, zoning, construction, parking_spaces,
    broker_cap_rate,
    description, parking, broker_noi, broker_noi_stabilized,
    gross_potential_rent, effective_gross_income, vacancy_pct, vacancy_label,
    expense_items, om_date, broker_contact, latitude, longitude,
    broker_5yr_irr, broker_yr1_coc, loan_term_years,
    broker_grm, broker_dscr, assumable_debt_balance, assumable_debt_term, cap_rate_pro_forma,
    building_class, hoa_monthly,
    clear_height_ft, loading_docks, office_pct, sprinklered,
    total_storage_units, climate_controlled_pct,
    assumable_debt_rate, investment_highlights,
    other_income_items, renovation_cost, claimed_renovation_noi_uplift,
    value_add_data, building_amenities, market_overview_text,
    created_at, updated_at
) VALUES (
    gen_random_uuid(), $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19,
    $20, $21, $22, $23,
    $24, $25,
    $26, $27, $28, $29, $30, $31,
    $32, $33, $34, $35, $36,
    $37,
    $38, $39, $40, $41,
    $42, $43, $44, $45,
    $46, $47, $48, $49, $50,
    $51, $52, $53,
    $54, $55, $56, $57, $58,
    $59, $60,
    $61, $62, $63, $64,
    $65, $66,
    $67, $68,
    $69, $70, $71,
    $72, $73, $74,
    NOW(), NOW()
) RETURNING *;

-- name: UpdatePipelinePropertyOM :one
-- ADR-105: store parsed OM data + file reference for a property.
-- Uses COALESCE so callers can update only the fields they have.
UPDATE pipeline_properties SET
    om_data         = COALESCE(sqlc.narg('om_data')::jsonb,   om_data),
    broker_cap_rate = COALESCE(sqlc.narg('broker_cap_rate')::numeric, broker_cap_rate),
    om_file_path    = COALESCE(sqlc.narg('om_file_path')::text, om_file_path),
    om_file_data    = COALESCE(sqlc.narg('om_file_data')::bytea, om_file_data),
    om_file_name    = COALESCE(sqlc.narg('om_file_name')::text, om_file_name),
    om_file_type    = COALESCE(sqlc.narg('om_file_type')::text, om_file_type),
    updated_at      = NOW()
WHERE id = $1
RETURNING *;

-- name: GetPropertyOMFile :one
-- ADR-105: fetch only the file columns (avoid pulling BYTEA in normal property fetches).
SELECT om_file_path, om_file_data, om_file_name, om_file_type
FROM pipeline_properties pp
JOIN pipeline_deals pd ON pd.id = pp.pipeline_deal_id
WHERE pp.id = $1 AND pd.user_id = $2;

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
    address                = COALESCE(sqlc.narg('address')::text, address),
    city                   = COALESCE(sqlc.narg('city')::text, city),
    state                  = COALESCE(sqlc.narg('state')::text, state),
    zip                    = COALESCE(sqlc.narg('zip')::text, zip),
    property_type          = COALESCE(sqlc.narg('property_type')::text, property_type),
    beds                   = COALESCE(sqlc.narg('beds')::numeric, beds),
    baths                  = COALESCE(sqlc.narg('baths')::numeric, baths),
    sqft                   = COALESCE(sqlc.narg('sqft')::integer, sqft),
    year_built             = COALESCE(sqlc.narg('year_built')::integer, year_built),
    units                  = COALESCE(sqlc.narg('units')::integer, units),
    asking_price           = COALESCE(sqlc.narg('asking_price')::numeric, asking_price),
    target_price           = COALESCE(sqlc.narg('target_price')::numeric, target_price),
    down_payment_pct       = COALESCE(sqlc.narg('down_payment_pct')::numeric, down_payment_pct),
    financing_type         = COALESCE(sqlc.narg('financing_type')::text, financing_type),
    interest_rate          = COALESCE(sqlc.narg('interest_rate')::numeric, interest_rate),
    broker_rent            = COALESCE(sqlc.narg('broker_rent')::numeric, broker_rent),
    system_rent            = COALESCE(sqlc.narg('system_rent')::numeric, system_rent),
    current_occupancy      = COALESCE(sqlc.narg('current_occupancy')::numeric, current_occupancy),
    expense_overrides      = COALESCE(sqlc.narg('expense_overrides')::jsonb, expense_overrides),
    unit_mix               = COALESCE(sqlc.narg('unit_mix')::jsonb, unit_mix),
    lot_sqft               = COALESCE(sqlc.narg('lot_sqft')::integer, lot_sqft),
    building_count         = COALESCE(sqlc.narg('building_count')::integer, building_count),
    notes                  = COALESCE(sqlc.narg('notes')::text, notes),
    lease_type             = COALESCE(sqlc.narg('lease_type')::text, lease_type),
    tenant_count           = COALESCE(sqlc.narg('tenant_count')::integer, tenant_count),
    anchor_tenant          = COALESCE(sqlc.narg('anchor_tenant')::text, anchor_tenant),
    weighted_avg_lease_yrs = COALESCE(sqlc.narg('weighted_avg_lease_yrs')::numeric, weighted_avg_lease_yrs),
    commercial_sqft        = COALESCE(sqlc.narg('commercial_sqft')::integer, commercial_sqft),
    commercial_mix         = COALESCE(sqlc.narg('commercial_mix')::jsonb, commercial_mix),
    year_renovated         = COALESCE(sqlc.narg('year_renovated')::integer, year_renovated),
    stories                = COALESCE(sqlc.narg('stories')::integer, stories),
    zoning                 = COALESCE(sqlc.narg('zoning')::text, zoning),
    construction           = COALESCE(sqlc.narg('construction')::text, construction),
    parking_spaces         = COALESCE(sqlc.narg('parking_spaces')::integer, parking_spaces),
    description            = COALESCE(sqlc.narg('description')::text, description),
    parking                = COALESCE(sqlc.narg('parking')::text, parking),
    broker_noi             = COALESCE(sqlc.narg('broker_noi')::numeric, broker_noi),
    broker_noi_stabilized  = COALESCE(sqlc.narg('broker_noi_stabilized')::numeric, broker_noi_stabilized),
    gross_potential_rent   = COALESCE(sqlc.narg('gross_potential_rent')::numeric, gross_potential_rent),
    effective_gross_income = COALESCE(sqlc.narg('effective_gross_income')::numeric, effective_gross_income),
    vacancy_pct            = COALESCE(sqlc.narg('vacancy_pct')::numeric, vacancy_pct),
    vacancy_label          = COALESCE(sqlc.narg('vacancy_label')::text, vacancy_label),
    expense_items          = COALESCE(sqlc.narg('expense_items')::jsonb, expense_items),
    om_date                = COALESCE(sqlc.narg('om_date')::text, om_date),
    broker_contact         = COALESCE(sqlc.narg('broker_contact')::jsonb, broker_contact),
    latitude               = COALESCE(sqlc.narg('latitude')::numeric, latitude),
    longitude              = COALESCE(sqlc.narg('longitude')::numeric, longitude),
    broker_5yr_irr         = COALESCE(sqlc.narg('broker_5yr_irr')::numeric, broker_5yr_irr),
    broker_yr1_coc         = COALESCE(sqlc.narg('broker_yr1_coc')::numeric, broker_yr1_coc),
    loan_term_years        = COALESCE(sqlc.narg('loan_term_years')::integer, loan_term_years),
    broker_cap_rate        = COALESCE(sqlc.narg('broker_cap_rate')::numeric, broker_cap_rate),
    broker_grm             = COALESCE(sqlc.narg('broker_grm')::numeric, broker_grm),
    broker_dscr            = COALESCE(sqlc.narg('broker_dscr')::numeric, broker_dscr),
    assumable_debt_balance = COALESCE(sqlc.narg('assumable_debt_balance')::numeric, assumable_debt_balance),
    assumable_debt_term    = COALESCE(sqlc.narg('assumable_debt_term')::numeric, assumable_debt_term),
    cap_rate_pro_forma     = COALESCE(sqlc.narg('cap_rate_pro_forma')::numeric, cap_rate_pro_forma),
    building_class         = COALESCE(sqlc.narg('building_class')::text, building_class),
    hoa_monthly            = COALESCE(sqlc.narg('hoa_monthly')::numeric, hoa_monthly),
    clear_height_ft        = COALESCE(sqlc.narg('clear_height_ft')::numeric, clear_height_ft),
    loading_docks          = COALESCE(sqlc.narg('loading_docks')::integer, loading_docks),
    office_pct             = COALESCE(sqlc.narg('office_pct')::numeric, office_pct),
    sprinklered            = COALESCE(sqlc.narg('sprinklered')::text, sprinklered),
    total_storage_units    = COALESCE(sqlc.narg('total_storage_units')::integer, total_storage_units),
    climate_controlled_pct = COALESCE(sqlc.narg('climate_controlled_pct')::numeric, climate_controlled_pct),
    assumable_debt_rate    = COALESCE(sqlc.narg('assumable_debt_rate')::numeric, assumable_debt_rate),
    investment_highlights  = COALESCE(sqlc.narg('investment_highlights')::jsonb, investment_highlights),
    other_income_items     = COALESCE(sqlc.narg('other_income_items')::jsonb, other_income_items),
    renovation_cost        = COALESCE(sqlc.narg('renovation_cost')::numeric, renovation_cost),
    claimed_renovation_noi_uplift = COALESCE(sqlc.narg('claimed_renovation_noi_uplift')::numeric, claimed_renovation_noi_uplift),
    value_add_data         = COALESCE(sqlc.narg('value_add_data')::jsonb, value_add_data),
    building_amenities     = COALESCE(sqlc.narg('building_amenities')::text[], building_amenities),
    market_overview_text   = COALESCE(sqlc.narg('market_overview_text')::text, market_overview_text),
    updated_at             = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOMValidatedData :one
-- ADR-108: merge user-edited fields into om_validated_data JSONB.
-- Sets om_validation_status when confirm=true.
UPDATE pipeline_properties SET
    om_validated_data      = $2,
    om_validation_status   = CASE WHEN $3::boolean THEN 'validated' ELSE om_validation_status END,
    updated_at             = NOW()
WHERE id = $1
RETURNING *;

-- name: DeletePipelineProperty :execrows
DELETE FROM pipeline_properties pp
USING pipeline_deals pd
WHERE pp.id = $1
  AND pp.pipeline_deal_id = pd.id
  AND pd.user_id = $2;

-- name: UpdatePipelinePropertyOMValidation :one
-- ADR-106: write om validation status, questions, and merged validated data.
UPDATE pipeline_properties SET
    om_validation_status = $2,
    om_questions         = COALESCE($3::jsonb, om_questions),
    om_validated_data    = COALESCE($4::jsonb, om_validated_data),
    updated_at           = NOW()
WHERE id = $1
RETURNING *;

-- name: GetPipelinePropertyOMValidation :one
-- ADR-106: read validation columns only (avoids pulling full om_data/file in list views).
SELECT pp.id, pp.om_validation_status, pp.om_questions, pp.om_validated_data, pp.om_data
FROM pipeline_properties pp
JOIN pipeline_deals pd ON pd.id = pp.pipeline_deal_id
WHERE pp.id = $1 AND pd.user_id = $2;

-- name: UpdatePropertyCompleteness :exec
-- ADR-107: update computed completeness status after create/update.
UPDATE pipeline_properties SET
    property_completeness = $2,
    updated_at            = NOW()
WHERE id = $1;

-- name: UpdatePropertyExtractionIssues :exec
-- Pass 3: store validation issues after async OM re-read.
UPDATE pipeline_properties SET
    extraction_issues = $2,
    updated_at        = NOW()
WHERE id = $1;

-- name: FindOMDuplicates :many
-- ADR-107: find potential duplicate deals by broker email, OM date+company, or property description.
-- Pass empty string '' for any criterion to skip it.
-- Results are limited to active (non-archived) deals, 10 max, newest first.
SELECT
    pp.id                                                                AS prop_id,
    pp.pipeline_deal_id                                                  AS deal_id,
    pd.name                                                              AS deal_name,
    pp.address                                                           AS address,
    coalesce(pp.om_data ->>          'omDate',                   '')    AS om_date,
    coalesce(pp.om_data -> 'brokerContact' ->> 'name',           '')    AS broker_name,
    coalesce(pp.om_data -> 'brokerContact' ->> 'company',        '')    AS broker_company,
    coalesce(pp.om_data -> 'brokerContact' ->> 'email',          '')    AS broker_email,
    pd.created_at                                                        AS deal_created_at
FROM pipeline_properties pp
JOIN pipeline_deals pd ON pp.pipeline_deal_id = pd.id
WHERE pd.user_id   = sqlc.arg('user_id')::text
  AND pp.om_data   IS NOT NULL
  AND pd.status    NOT IN ('passed', 'closed')
  AND (
      -- Criterion 1: same broker email (strongest signal)
      (sqlc.arg('broker_email')::text  <> ''
       AND coalesce(pp.om_data -> 'brokerContact' ->> 'email', '') = sqlc.arg('broker_email')::text)
   OR -- Criterion 2: same OM date AND same broker company (medium signal)
      (sqlc.arg('om_date')::text       <> ''
       AND sqlc.arg('broker_company')::text <> ''
       AND coalesce(pp.om_data ->> 'omDate',                          '') = sqlc.arg('om_date')::text
       AND coalesce(pp.om_data -> 'brokerContact' ->> 'company',      '') = sqlc.arg('broker_company')::text)
   OR -- Criterion 3: same property description (medium signal)
      (sqlc.arg('property_desc')::text <> ''
       AND lower(coalesce(pp.om_data ->> 'propertyDescription', ''))  = lower(sqlc.arg('property_desc')::text))
  )
ORDER BY pd.created_at DESC
LIMIT 10;

-- ============================================================
-- ADR-104: Retrospective Analytics
-- ============================================================

-- name: GetPipelineRetrospective :one
-- $1 = user_id TEXT
-- $2 = days INT (0 = all time, otherwise last N days)
WITH period_evals AS (
    SELECT
        e.pipeline_deal_id,
        pd.source AS deal_source
    FROM v2_evaluations e
    LEFT JOIN pipeline_deals pd ON pd.id = e.pipeline_deal_id AND pd.user_id = $1
    WHERE e.user_id = $1
      AND ($2::int = 0 OR e.created_at >= NOW() - make_interval(days => $2::int))
),
pipeline_deal_stats AS (
    SELECT
        COUNT(*) FILTER (
            WHERE status = 'proceeding'
               OR (status = 'closed' AND closed_outcome = 'acquired')
        )::bigint                                                         AS proceeded_deals,
        COUNT(*)::bigint                                                  AS total_pipeline_deals,
        COUNT(*) FILTER (WHERE status NOT IN ('passed', 'closed'))::bigint AS active_pipeline_deals
    FROM pipeline_deals
    WHERE user_id = $1
)
SELECT
    COUNT(*)::bigint                                           AS total_evaluations,
    COUNT(*) FILTER (WHERE pipeline_deal_id IS NULL)::bigint  AS discovery_evaluations,
    COUNT(*) FILTER (WHERE pipeline_deal_id IS NOT NULL)::bigint AS pipeline_evaluations,
    COUNT(*) FILTER (WHERE deal_source = 'broker')::bigint    AS source_broker,
    COUNT(*) FILTER (WHERE deal_source = 'off-market')::bigint AS source_off_market,
    COUNT(*) FILTER (WHERE deal_source = 'syndication')::bigint AS source_syndication,
    COUNT(*) FILTER (WHERE deal_source = 'jv')::bigint        AS source_jv,
    COUNT(*) FILTER (WHERE deal_source = 'auction')::bigint   AS source_auction,
    COUNT(*) FILTER (WHERE deal_source = 'direct')::bigint    AS source_direct,
    COUNT(*) FILTER (WHERE deal_source = 'other')::bigint     AS source_other,
    (SELECT proceeded_deals     FROM pipeline_deal_stats)     AS proceeded_deals,
    (SELECT total_pipeline_deals FROM pipeline_deal_stats)    AS total_pipeline_deals,
    (SELECT active_pipeline_deals FROM pipeline_deal_stats)   AS active_pipeline_deals
FROM period_evals;
