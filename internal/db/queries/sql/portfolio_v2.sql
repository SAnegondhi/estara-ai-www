-- V2 Portfolio Properties Queries

-- name: ListPortfolioProperties :many
SELECT id, user_id, address, city, state, zip_code,
       property_type, bedrooms, bathrooms, sqft, year_built,
       purchase_price, purchase_date, current_value, last_valued_at,
       monthly_rent, vacancy_rate, expenses,
       mortgage_balance, mortgage_rate, mortgage_payment, loan_term_years,
       lat, lng, acquisition_type, expense_frequency, revenue_frequency,
       sale_date, sale_price, status, property_status, last_confirmed_at,
       notes, created_at, updated_at
FROM v2_portfolio_properties
WHERE user_id = $1 AND status != 'deleted'
ORDER BY created_at DESC;

-- name: GetPortfolioProperty :one
SELECT id, user_id, address, city, state, zip_code,
       property_type, bedrooms, bathrooms, sqft, year_built,
       purchase_price, purchase_date, current_value, last_valued_at,
       monthly_rent, vacancy_rate, expenses,
       mortgage_balance, mortgage_rate, mortgage_payment, loan_term_years,
       lat, lng, acquisition_type, expense_frequency, revenue_frequency,
       sale_date, sale_price, status, property_status, last_confirmed_at,
       notes, created_at, updated_at
FROM v2_portfolio_properties
WHERE id = $1 AND user_id = $2;

-- name: GetPortfolioPropertiesByIDs :many
SELECT id, user_id, address, city, state, zip_code,
       property_type, bedrooms, bathrooms, sqft, year_built,
       purchase_price, purchase_date, current_value, last_valued_at,
       monthly_rent, vacancy_rate, expenses,
       mortgage_balance, mortgage_rate, mortgage_payment, loan_term_years,
       lat, lng, acquisition_type, expense_frequency, revenue_frequency,
       sale_date, sale_price, status, property_status, last_confirmed_at,
       notes, created_at, updated_at
FROM v2_portfolio_properties
WHERE id = ANY(sqlc.arg('ids')::text[]) AND user_id = $1;

-- name: CreatePortfolioProperty :one
INSERT INTO v2_portfolio_properties (
    id, user_id, address, city, state, zip_code,
    property_type, bedrooms, bathrooms, sqft, year_built,
    purchase_price, purchase_date, current_value, last_valued_at,
    monthly_rent, vacancy_rate, expenses,
    mortgage_balance, mortgage_rate, mortgage_payment, loan_term_years,
    lat, lng, acquisition_type, expense_frequency, revenue_frequency,
    status, property_status, notes, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13, $14, $15,
    $16, $17, $18,
    $19, $20, $21, $22,
    $23, $24, $25, $26, $27,
    $28, $29, $30, NOW(), NOW()
)
RETURNING *;

-- name: UpdatePortfolioProperty :one
UPDATE v2_portfolio_properties SET
    address = COALESCE(sqlc.narg('address')::text, address),
    city = COALESCE(sqlc.narg('city')::text, city),
    state = COALESCE(sqlc.narg('state')::text, state),
    zip_code = COALESCE(sqlc.narg('zip_code')::text, zip_code),
    property_type = COALESCE(sqlc.narg('property_type')::text, property_type),
    bedrooms = COALESCE(sqlc.narg('bedrooms')::integer, bedrooms),
    bathrooms = COALESCE(sqlc.narg('bathrooms')::double precision, bathrooms),
    sqft = COALESCE(sqlc.narg('sqft')::integer, sqft),
    year_built = COALESCE(sqlc.narg('year_built')::integer, year_built),
    purchase_price = COALESCE(sqlc.narg('purchase_price')::double precision, purchase_price),
    purchase_date = COALESCE(sqlc.narg('purchase_date')::timestamp, purchase_date),
    current_value = COALESCE(sqlc.narg('current_value')::double precision, current_value),
    last_valued_at = COALESCE(sqlc.narg('last_valued_at')::timestamp, last_valued_at),
    monthly_rent = COALESCE(sqlc.narg('monthly_rent')::double precision, monthly_rent),
    vacancy_rate = COALESCE(sqlc.narg('vacancy_rate')::double precision, vacancy_rate),
    expenses = COALESCE(sqlc.narg('expenses')::jsonb, expenses),
    mortgage_balance = COALESCE(sqlc.narg('mortgage_balance')::double precision, mortgage_balance),
    mortgage_rate = COALESCE(sqlc.narg('mortgage_rate')::double precision, mortgage_rate),
    mortgage_payment = COALESCE(sqlc.narg('mortgage_payment')::double precision, mortgage_payment),
    loan_term_years = COALESCE(sqlc.narg('loan_term_years')::integer, loan_term_years),
    lat = COALESCE(sqlc.narg('lat')::double precision, lat),
    lng = COALESCE(sqlc.narg('lng')::double precision, lng),
    acquisition_type = COALESCE(sqlc.narg('acquisition_type')::text, acquisition_type),
    expense_frequency = COALESCE(sqlc.narg('expense_frequency')::text, expense_frequency),
    revenue_frequency = COALESCE(sqlc.narg('revenue_frequency')::text, revenue_frequency),
    sale_date = sqlc.narg('sale_date')::timestamp,
    sale_price = sqlc.narg('sale_price')::double precision,
    property_status = COALESCE(sqlc.narg('property_status')::varchar, property_status),
    notes = sqlc.narg('notes')::text,
    updated_at = NOW()
WHERE id = $1 AND user_id = $2
RETURNING *;

-- name: SoftDeletePortfolioProperty :exec
UPDATE v2_portfolio_properties
SET status = 'deleted', updated_at = NOW()
WHERE id = $1 AND user_id = $2;

-- name: CountPortfolioProperties :one
SELECT COUNT(*) FROM v2_portfolio_properties
WHERE user_id = $1 AND status != 'deleted';
