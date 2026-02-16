-- Admin Waitlist (Early Access) Management Queries

-- name: DeleteEarlyAccess :exec
DELETE FROM early_access WHERE id = $1;

-- name: UpdateEarlyAccessStatus :one
UPDATE early_access SET status = $2, invited = $3, "updatedAt" = NOW()
WHERE id = $1
RETURNING id, email, metadata->>'name' as name, source, status, invited, contacted, notes, "requestedAt", "createdAt", "updatedAt";

-- name: CountWaitlistFiltered :one
SELECT COUNT(*) FROM early_access
WHERE (sqlc.narg('status_filter')::text IS NULL OR status = sqlc.narg('status_filter'))
  AND (sqlc.narg('search')::text IS NULL OR
       LOWER(email) LIKE '%' || LOWER(sqlc.narg('search')) || '%' OR
       LOWER(COALESCE(metadata->>'name', '')) LIKE '%' || LOWER(sqlc.narg('search')) || '%');

-- name: ListWaitlistFiltered :many
SELECT id, email, metadata->>'name' as name, source, status, invited, contacted, notes, "requestedAt", "createdAt", "updatedAt"
FROM early_access
WHERE (sqlc.narg('status_filter')::text IS NULL OR status = sqlc.narg('status_filter'))
  AND (sqlc.narg('search')::text IS NULL OR
       LOWER(email) LIKE '%' || LOWER(sqlc.narg('search')) || '%' OR
       LOWER(COALESCE(metadata->>'name', '')) LIKE '%' || LOWER(sqlc.narg('search')) || '%')
ORDER BY "createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetWaitlistByID :one
SELECT id, email, metadata->>'name' as name, source, status, invited, contacted, notes, "requestedAt", "createdAt", "updatedAt"
FROM early_access WHERE id = $1;

-- name: InviteWaitlistEntry :one
UPDATE early_access SET invited = true, status = 'INVITED', "updatedAt" = NOW()
WHERE id = $1
RETURNING id, email, metadata->>'name' as name, source, status, invited, contacted, notes, "requestedAt", "createdAt", "updatedAt";

-- name: GetWaitlistSummary :one
SELECT
    COUNT(*) as total,
    COUNT(*) FILTER (WHERE status = 'PENDING') as pending,
    COUNT(*) FILTER (WHERE status = 'INVITED') as invited,
    COUNT(*) FILTER (WHERE status = 'REVOKED') as revoked
FROM early_access;

-- name: RevokeWaitlistByEmail :exec
UPDATE early_access SET status = 'REVOKED', invited = false, "updatedAt" = NOW()
WHERE LOWER(email) = $1;
