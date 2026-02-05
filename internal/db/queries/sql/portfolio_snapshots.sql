-- Portfolio Snapshots SQLC Queries
-- name: GetPortfolioSnapshots :many
-- Get portfolio snapshots for a user within a date range
SELECT id, user_id, snapshot_date, total_value, total_equity, total_debt,
       monthly_cash_flow, portfolio_cap_rate, property_count, metrics_json, created_at
FROM v2_portfolio_snapshots
WHERE user_id = $1
  AND ($2::timestamp IS NULL OR snapshot_date >= $2)
  AND ($3::timestamp IS NULL OR snapshot_date <= $3)
ORDER BY snapshot_date DESC
LIMIT $4;

-- name: GetLatestPortfolioSnapshot :one
-- Get the most recent snapshot for a user
SELECT id, user_id, snapshot_date, total_value, total_equity, total_debt,
       monthly_cash_flow, portfolio_cap_rate, property_count, metrics_json, created_at
FROM v2_portfolio_snapshots
WHERE user_id = $1
ORDER BY snapshot_date DESC
LIMIT 1;

-- name: CreatePortfolioSnapshot :one
-- Create a new portfolio snapshot
INSERT INTO v2_portfolio_snapshots (
    id, user_id, snapshot_date, total_value, total_equity, total_debt,
    monthly_cash_flow, portfolio_cap_rate, property_count, metrics_json, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW()
)
ON CONFLICT (user_id, snapshot_date) DO UPDATE SET
    total_value = EXCLUDED.total_value,
    total_equity = EXCLUDED.total_equity,
    total_debt = EXCLUDED.total_debt,
    monthly_cash_flow = EXCLUDED.monthly_cash_flow,
    portfolio_cap_rate = EXCLUDED.portfolio_cap_rate,
    property_count = EXCLUDED.property_count,
    metrics_json = EXCLUDED.metrics_json
RETURNING id, user_id, snapshot_date, total_value, total_equity, total_debt,
          monthly_cash_flow, portfolio_cap_rate, property_count, metrics_json, created_at;

-- name: DeletePortfolioSnapshots :exec
-- Delete all snapshots for a user (for regeneration)
DELETE FROM v2_portfolio_snapshots
WHERE user_id = $1;

-- name: CountPortfolioSnapshots :one
-- Count snapshots for a user
SELECT COUNT(*) FROM v2_portfolio_snapshots WHERE user_id = $1;

-- name: GetEarliestPropertyDate :one
-- Get the earliest purchase date from portfolio properties for backfill
SELECT MIN(purchase_date) as earliest_date
FROM v2_portfolio_properties
WHERE user_id = $1 AND status != 'deleted' AND purchase_date IS NOT NULL;

-- ============================================
-- Portfolio Adjustments Queries
-- ============================================

-- name: GetPropertyAdjustments :many
-- Get all adjustments for a property ordered by month descending
SELECT id, property_id, month, type, amount, note, created_at, updated_at
FROM v2_portfolio_adjustments
WHERE property_id = $1
ORDER BY month DESC;

-- name: CreateOrUpdateAdjustment :one
-- Create or update an adjustment for a property/month/type combination
INSERT INTO v2_portfolio_adjustments (
    id, property_id, month, type, amount, note, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, NOW(), NOW()
)
ON CONFLICT (property_id, month, type) DO UPDATE SET
    amount = EXCLUDED.amount,
    note = EXCLUDED.note,
    updated_at = NOW()
RETURNING id, property_id, month, type, amount, note, created_at, updated_at;

-- name: DeleteAdjustment :exec
-- Delete an adjustment by ID
DELETE FROM v2_portfolio_adjustments WHERE id = $1;

-- ============================================
-- Baseline Changes Queries
-- ============================================

-- name: GetPropertyBaselineChanges :many
-- Get all baseline changes for a property ordered by effective date descending
SELECT id, property_id, field, effective_date, new_value, previous_value, note, created_at, updated_at
FROM v2_baseline_changes
WHERE property_id = $1
ORDER BY effective_date DESC, field ASC;

-- name: GetLatestBaselineChange :one
-- Get the most recent baseline change for a property/field before a given date
SELECT id, property_id, field, effective_date, new_value, previous_value, note, created_at, updated_at
FROM v2_baseline_changes
WHERE property_id = $1 AND field = $2 AND effective_date < $3
ORDER BY effective_date DESC
LIMIT 1;

-- name: CreateOrUpdateBaselineChange :one
-- Create or update a baseline change for a property/field/effective_date combination
INSERT INTO v2_baseline_changes (
    id, property_id, field, effective_date, new_value, previous_value, note, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, NOW(), NOW()
)
ON CONFLICT (property_id, field, effective_date) DO UPDATE SET
    new_value = EXCLUDED.new_value,
    previous_value = EXCLUDED.previous_value,
    note = EXCLUDED.note,
    updated_at = NOW()
RETURNING id, property_id, field, effective_date, new_value, previous_value, note, created_at, updated_at;

-- name: DeleteBaselineChange :exec
-- Delete a baseline change by ID
DELETE FROM v2_baseline_changes WHERE id = $1;
