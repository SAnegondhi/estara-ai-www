-- name: GetWebAuthnCredentialsByUserID :many
SELECT * FROM webauthn_credentials
WHERE "userId" = $1
ORDER BY "createdAt" DESC;

-- name: GetWebAuthnCredentialByCredentialID :one
SELECT * FROM webauthn_credentials
WHERE credential_id = $1;

-- name: CreateWebAuthnCredential :one
INSERT INTO webauthn_credentials (
    id, "userId", credential_id, public_key, attestation_type,
    transport, flags_value, sign_count, aaguid, friendly_name,
    "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    NOW(), NOW()
) RETURNING *;

-- name: UpdateWebAuthnCredentialSignCount :exec
UPDATE webauthn_credentials
SET sign_count = $2, "lastUsedAt" = NOW(), "updatedAt" = NOW()
WHERE id = $1;

-- name: UpdateWebAuthnCredentialName :exec
UPDATE webauthn_credentials
SET friendly_name = $2, "updatedAt" = NOW()
WHERE id = $1;

-- name: DeleteWebAuthnCredential :exec
DELETE FROM webauthn_credentials
WHERE id = $1 AND "userId" = $2;

-- name: CountWebAuthnCredentialsByUser :one
SELECT COUNT(*) FROM webauthn_credentials
WHERE "userId" = $1;
