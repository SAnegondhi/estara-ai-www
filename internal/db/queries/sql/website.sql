-- Website and Public API Queries

-- Contact Submissions

-- name: GetContactSubmissionByID :one
SELECT * FROM contact_submissions WHERE id = $1;

-- name: CreateContactSubmission :one
INSERT INTO contact_submissions (
    id, name, email, phone, subject, message, source,
    "ipAddress", "userAgent", status, "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW()
) RETURNING *;

-- name: UpdateContactSubmissionStatus :one
UPDATE contact_submissions SET
    status = $2,
    notes = COALESCE($3, notes),
    "respondedAt" = CASE WHEN $2 = 'RESPONDED' THEN NOW() ELSE "respondedAt" END,
    "respondedBy" = COALESCE($4, "respondedBy"),
    "updatedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: ListContactSubmissions :many
SELECT * FROM contact_submissions
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: ListContactSubmissionsByStatus :many
SELECT * FROM contact_submissions
WHERE status = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: CountContactSubmissions :one
SELECT COUNT(*) FROM contact_submissions;

-- name: CountContactSubmissionsByStatus :one
SELECT COUNT(*) FROM contact_submissions WHERE status = $1;

-- Early Access (Waitlist)

-- name: GetEarlyAccessByID :one
SELECT * FROM early_access WHERE id = $1;

-- name: GetEarlyAccessByEmail :one
SELECT * FROM early_access WHERE email = $1;

-- name: CreateEarlyAccess :one
INSERT INTO early_access (
    id, email, name, source, "ipAddress", "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, NOW()
) RETURNING *;

-- name: UpdateEarlyAccessInvited :one
UPDATE early_access SET
    "invitedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateEarlyAccessAccepted :one
UPDATE early_access SET
    "acceptedAt" = NOW()
WHERE id = $1
RETURNING *;

-- name: ListEarlyAccess :many
SELECT * FROM early_access
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: ListPendingEarlyAccess :many
SELECT * FROM early_access
WHERE "invitedAt" IS NULL
ORDER BY "createdAt" ASC
LIMIT $1;

-- name: ListInvitedEarlyAccess :many
SELECT * FROM early_access
WHERE "invitedAt" IS NOT NULL AND "acceptedAt" IS NULL
ORDER BY "invitedAt" ASC
LIMIT $1;

-- name: CountEarlyAccess :one
SELECT COUNT(*) FROM early_access;

-- name: CountPendingEarlyAccess :one
SELECT COUNT(*) FROM early_access WHERE "invitedAt" IS NULL;

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
