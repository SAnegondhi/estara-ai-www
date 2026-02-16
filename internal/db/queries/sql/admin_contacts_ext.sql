-- Admin Contact Submission Management Queries

-- name: DeleteContactSubmission :exec
DELETE FROM contact_submissions WHERE id = $1;

-- name: CountContactsFiltered :one
SELECT COUNT(*) FROM contact_submissions
WHERE (sqlc.narg('status_filter')::text IS NULL OR status = sqlc.narg('status_filter'))
  AND (sqlc.narg('search')::text IS NULL OR
       LOWER(name) LIKE '%' || LOWER(sqlc.narg('search')) || '%' OR
       LOWER(email) LIKE '%' || LOWER(sqlc.narg('search')) || '%');

-- name: ListContactsFiltered :many
SELECT id, name, email, company, phone, subject, message, category,
       source, "ipAddress", "userAgent", status, "assignedTo", notes,
       "firstResponseAt", "resolvedAt", "responseCount", "createdAt", "updatedAt"
FROM contact_submissions
WHERE (sqlc.narg('status_filter')::text IS NULL OR status = sqlc.narg('status_filter'))
  AND (sqlc.narg('search')::text IS NULL OR
       LOWER(name) LIKE '%' || LOWER(sqlc.narg('search')) || '%' OR
       LOWER(email) LIKE '%' || LOWER(sqlc.narg('search')) || '%')
ORDER BY "createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetContactByID :one
SELECT id, name, email, company, phone, subject, message, category,
       source, "ipAddress", "userAgent", status, "assignedTo", notes,
       "firstResponseAt", "resolvedAt", "responseCount", "createdAt", "updatedAt"
FROM contact_submissions WHERE id = $1;

-- name: UpdateContactAdmin :one
UPDATE contact_submissions SET
    status = COALESCE(sqlc.narg('status')::text, status),
    notes = COALESCE(sqlc.narg('notes')::text, notes),
    "assignedTo" = COALESCE(sqlc.narg('assigned_to')::text, "assignedTo"),
    "firstResponseAt" = CASE WHEN sqlc.narg('status')::text IN ('ASSIGNED', 'IN_PROGRESS') AND "firstResponseAt" IS NULL THEN NOW() ELSE "firstResponseAt" END,
    "resolvedAt" = CASE WHEN sqlc.narg('status')::text = 'RESOLVED' THEN NOW() ELSE "resolvedAt" END,
    "updatedAt" = NOW()
WHERE id = $1
RETURNING id, name, email, company, phone, subject, message, category,
          source, "ipAddress", "userAgent", status, "assignedTo", notes,
          "firstResponseAt", "resolvedAt", "responseCount", "createdAt", "updatedAt";

-- name: GetContactSummary :one
SELECT
    COUNT(*) as total,
    COUNT(*) FILTER (WHERE status = 'NEW') as new_count,
    COUNT(*) FILTER (WHERE status = 'ASSIGNED') as assigned_count,
    COUNT(*) FILTER (WHERE status = 'IN_PROGRESS') as in_progress_count,
    COUNT(*) FILTER (WHERE status = 'AWAITING_RESPONSE') as awaiting_response_count,
    COUNT(*) FILTER (WHERE status = 'RESOLVED') as resolved_count,
    COUNT(*) FILTER (WHERE status = 'CLOSED') as closed_count
FROM contact_submissions;
