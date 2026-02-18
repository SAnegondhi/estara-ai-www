-- Early Access Request Queries (ADR-085)

-- name: CreateEarlyAccessRequest :one
INSERT INTO early_access_requests (
    id, email, first_name, last_name, company, use_case,
    portfolio_size, linkedin_url, status, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, 'pending', NOW()
) RETURNING *;

-- name: GetEarlyAccessRequestByID :one
SELECT * FROM early_access_requests WHERE id = $1;

-- name: GetEarlyAccessRequestByEmail :one
SELECT * FROM early_access_requests WHERE email = $1;

-- name: ListEarlyAccessRequests :many
SELECT * FROM early_access_requests
WHERE ($1::text = '' OR status = $1)
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountEarlyAccessRequests :one
SELECT COUNT(*) FROM early_access_requests
WHERE ($1::text = '' OR status = $1);

-- name: ApproveEarlyAccessRequest :exec
UPDATE early_access_requests SET
    status = 'approved',
    user_id = $2,
    admin_notes = $3,
    reviewed_at = NOW(),
    reviewed_by = $4
WHERE id = $1;

-- name: RejectEarlyAccessRequest :exec
UPDATE early_access_requests SET
    status = 'rejected',
    admin_notes = $2,
    reviewed_at = NOW(),
    reviewed_by = $3
WHERE id = $1;
