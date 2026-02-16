-- V2 Decision Records and Evaluations Queries

-- name: ListDecisionRecordsWithEvaluation :many
SELECT
    d.id, d.evaluation_id, d.user_id, d.memo_content, d.pdf_url,
    d.exported_at, d.created_at,
    e.property_address, e.property_city, e.property_state,
    e.purchase_price, e.status::text AS evaluation_status
FROM v2_decision_records d
JOIN v2_evaluations e ON d.evaluation_id = e.id
WHERE d.user_id = $1
ORDER BY d.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountDecisionRecordsByUser :one
SELECT COUNT(*) FROM v2_decision_records
WHERE user_id = $1;

-- name: GetDecisionRecordWithEvaluation :one
SELECT
    d.id, d.evaluation_id, d.user_id, d.memo_content, d.pdf_url,
    d.exported_at, d.created_at,
    e.property_address, e.property_city, e.property_state, e.property_zip,
    e.property_details, e.purchase_price, e.down_payment_pct,
    e.interest_rate, e.loan_term_years, e.monthly_rent,
    e.vacancy_rate_pct, e.maintenance_cost, e.property_tax,
    e.insurance, e.hoa_fees, e.appreciation_rate,
    e.scenarios, e.sensitivity_data, e.status::text AS evaluation_status,
    e.created_at AS evaluation_created_at
FROM v2_decision_records d
JOIN v2_evaluations e ON d.evaluation_id = e.id
WHERE d.id = $1 AND d.user_id = $2;

-- name: GetEvaluationsWithDecisionRecords :many
SELECT
    e.id, e.user_id, e.property_id, e.property_address, e.property_city,
    e.property_state, e.property_zip, e.property_details,
    e.purchase_price, e.down_payment_pct, e.interest_rate,
    e.loan_term_years, e.monthly_rent, e.vacancy_rate_pct,
    e.maintenance_cost, e.property_tax, e.insurance, e.hoa_fees,
    e.appreciation_rate, e.scenarios, e.sensitivity_data,
    e.status::text, e.created_at, e.updated_at,
    d.id AS decision_record_id, d.memo_content, d.pdf_url, d.exported_at
FROM v2_evaluations e
LEFT JOIN v2_decision_records d ON d.evaluation_id = e.id
WHERE e.user_id = $1
ORDER BY e.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: FetchEvaluationsByIDs :many
SELECT
    e.id,
    e.property_address, e.property_city, e.property_state, e.property_zip,
    e.property_details,
    e.purchase_price, e.down_payment_pct, e.interest_rate,
    e.loan_term_years, e.monthly_rent, e.vacancy_rate_pct,
    e.maintenance_cost, e.property_tax, e.insurance, e.hoa_fees,
    e.appreciation_rate, e.scenarios, e.status::text,
    r.id AS decision_record_id
FROM v2_evaluations e
LEFT JOIN v2_decision_records r ON r.evaluation_id = e.id
WHERE e.user_id = $1 AND e.id = ANY($2::text[]);

-- name: CreateDecisionRecord :one
INSERT INTO v2_decision_records (
    id, evaluation_id, user_id, memo_content, pdf_url, exported_at, created_at
) VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
RETURNING *;

-- name: CreateV2Evaluation :one
INSERT INTO v2_evaluations (
    id, user_id, property_id, property_address, property_city, property_state,
    property_zip, property_details, purchase_price, down_payment_pct,
    interest_rate, loan_term_years, monthly_rent, vacancy_rate_pct,
    maintenance_cost, property_tax, insurance, hoa_fees, appreciation_rate,
    scenarios, sensitivity_data, status, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19,
    $20, $21, 'DRAFT'::"V2EvaluationStatus", NOW(), NOW()
)
RETURNING id, user_id, property_id, property_address, property_city, property_state,
          property_zip, property_details, purchase_price, down_payment_pct,
          interest_rate, loan_term_years, monthly_rent, vacancy_rate_pct,
          maintenance_cost, property_tax, insurance, hoa_fees, appreciation_rate,
          scenarios, sensitivity_data, status::text, created_at, updated_at;

-- name: UpdateV2EvaluationStatus :exec
UPDATE v2_evaluations
SET status = $2::text::"V2EvaluationStatus", updated_at = NOW()
WHERE id = $1;

-- name: GetV2EvaluationByID :one
SELECT id, user_id, property_id, property_address, property_city, property_state,
       property_zip, property_details, purchase_price, down_payment_pct,
       interest_rate, loan_term_years, monthly_rent, vacancy_rate_pct,
       maintenance_cost, property_tax, insurance, hoa_fees, appreciation_rate,
       scenarios, sensitivity_data, status::text, created_at, updated_at
FROM v2_evaluations
WHERE id = $1 AND user_id = $2;
