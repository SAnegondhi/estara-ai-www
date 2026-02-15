-- Website and Public API Queries

-- Contact Submissions

-- name: GetContactSubmissionByID :one
SELECT id, name, email, company, phone, subject, message,
       category::text as category, source, "inquiryType",
       status::text as status, "assignedTo", notes,
       "firstResponseAt", "resolvedAt", "responseCount",
       "ipAddress", "userAgent", "createdAt", "updatedAt"
FROM contact_submissions WHERE id = $1;

-- name: CreateContactSubmission :one
INSERT INTO contact_submissions (
    id, name, email, company, phone, subject, message,
    category, source, "ipAddress", "userAgent",
    status, "createdAt", "updatedAt"
) VALUES (
    sqlc.arg(id), sqlc.arg(name), sqlc.arg(email), sqlc.arg(company),
    sqlc.arg(phone), sqlc.arg(subject), sqlc.arg(message),
    sqlc.arg(category)::"ContactCategory", sqlc.arg(source),
    sqlc.arg(ip_address), sqlc.arg(user_agent),
    sqlc.arg(status)::"ContactStatus", NOW(), NOW()
) RETURNING id, name, email, company, phone, subject, message,
           category::text as category, source, "inquiryType",
           status::text as status, "assignedTo", notes,
           "firstResponseAt", "resolvedAt", "responseCount",
           "ipAddress", "userAgent", "createdAt", "updatedAt";

-- name: UpdateContactSubmissionStatus :one
UPDATE contact_submissions SET
    status = sqlc.arg(status)::"ContactStatus",
    notes = COALESCE(sqlc.arg(notes), notes),
    "assignedTo" = COALESCE(sqlc.arg(assigned_to), "assignedTo"),
    "firstResponseAt" = CASE WHEN sqlc.arg(status) = 'ASSIGNED' AND "firstResponseAt" IS NULL THEN NOW() ELSE "firstResponseAt" END,
    "updatedAt" = NOW()
WHERE id = sqlc.arg(id)
RETURNING id, name, email, company, phone, subject, message,
          category::text as category, source, "inquiryType",
          status::text as status, "assignedTo", notes,
          "firstResponseAt", "resolvedAt", "responseCount",
          "ipAddress", "userAgent", "createdAt", "updatedAt";

-- name: ListContactSubmissions :many
SELECT id, name, email, company, phone, subject, message,
       category::text as category, source, "inquiryType",
       status::text as status, "assignedTo", notes,
       "firstResponseAt", "resolvedAt", "responseCount",
       "ipAddress", "userAgent", "createdAt", "updatedAt"
FROM contact_submissions
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: ListContactSubmissionsByStatus :many
SELECT id, name, email, company, phone, subject, message,
       category::text as category, source, "inquiryType",
       status::text as status, "assignedTo", notes,
       "firstResponseAt", "resolvedAt", "responseCount",
       "ipAddress", "userAgent", "createdAt", "updatedAt"
FROM contact_submissions
WHERE status::text = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: CountContactSubmissions :one
SELECT COUNT(*) FROM contact_submissions;

-- name: CountContactSubmissionsByStatus :one
SELECT COUNT(*) FROM contact_submissions WHERE status::text = $1;

-- Early Access (Waitlist)

-- name: GetEarlyAccessByID :one
SELECT id, email, "requestedAt", source, metadata,
       contacted, invited, status, notes, "createdAt", "updatedAt"
FROM early_access WHERE id = $1;

-- name: GetEarlyAccessByEmail :one
SELECT id, email, "requestedAt", source, metadata,
       contacted, invited, status, notes, "createdAt", "updatedAt"
FROM early_access WHERE email = $1;

-- name: CreateEarlyAccess :one
INSERT INTO early_access (
    id, email, source, metadata, status, "requestedAt", "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, 'PENDING', NOW(), NOW(), NOW()
) RETURNING id, email, "requestedAt", source, metadata,
           contacted, invited, status, notes, "createdAt", "updatedAt";

-- name: UpdateEarlyAccessInvited :one
UPDATE early_access SET
    invited = true,
    status = 'INVITED',
    "updatedAt" = NOW()
WHERE id = $1
RETURNING id, email, "requestedAt", source, metadata,
          contacted, invited, status, notes, "createdAt", "updatedAt";

-- name: ListEarlyAccess :many
SELECT id, email, "requestedAt", source, metadata,
       contacted, invited, status, notes, "createdAt", "updatedAt"
FROM early_access
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: ListPendingEarlyAccess :many
SELECT id, email, "requestedAt", source, metadata,
       contacted, invited, status, notes, "createdAt", "updatedAt"
FROM early_access
WHERE status = 'PENDING'
ORDER BY "createdAt" ASC
LIMIT $1;

-- name: ListInvitedEarlyAccess :many
SELECT id, email, "requestedAt", source, metadata,
       contacted, invited, status, notes, "createdAt", "updatedAt"
FROM early_access
WHERE status = 'INVITED'
ORDER BY "updatedAt" ASC
LIMIT $1;

-- name: CountEarlyAccess :one
SELECT COUNT(*) FROM early_access;

-- name: CountPendingEarlyAccess :one
SELECT COUNT(*) FROM early_access WHERE status = 'PENDING';

-- Guest Sessions

-- name: GetGuestSessionByID :one
SELECT * FROM guest_sessions WHERE id = $1;

-- name: GetGuestSessionByToken :one
SELECT * FROM guest_sessions WHERE token = $1 AND "expiresAt" > NOW();

-- name: CreateGuestSession :one
INSERT INTO guest_sessions (
    id, token, "ipAddress", "userAgent", "deviceFingerprint",
    "expiresAt", "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, NOW()
) RETURNING *;

-- name: UpdateGuestSessionActivity :one
UPDATE guest_sessions SET
    "lastActivityAt" = NOW(),
    "snapshotsUsed" = COALESCE($2, "snapshotsUsed")
WHERE id = $1
RETURNING *;

-- name: IncrementGuestSessionSnapshots :one
UPDATE guest_sessions SET
    "snapshotsUsed" = "snapshotsUsed" + 1,
    "lastActivityAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteExpiredGuestSessions :exec
DELETE FROM guest_sessions
WHERE "expiresAt" < NOW();
