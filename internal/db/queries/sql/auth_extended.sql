-- Password Reset Token Queries

-- name: GetPasswordResetTokenByToken :one
SELECT * FROM password_reset_tokens
WHERE token = $1 AND used = false AND "expiresAt" > NOW();

-- name: GetPasswordResetTokenByID :one
SELECT * FROM password_reset_tokens WHERE id = $1;

-- name: CreatePasswordResetToken :one
INSERT INTO password_reset_tokens (
    id, token, "userId", email, "expiresAt", used, "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, false, NOW(), NOW()
) RETURNING *;

-- name: MarkPasswordResetTokenUsed :exec
UPDATE password_reset_tokens
SET used = true, "updatedAt" = NOW()
WHERE id = $1;

-- name: DeleteExpiredPasswordResetTokens :exec
DELETE FROM password_reset_tokens WHERE "expiresAt" < NOW();

-- name: GetPasswordResetTokensByUser :many
SELECT * FROM password_reset_tokens
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2;

-- name: InvalidateUserPasswordResetTokens :exec
UPDATE password_reset_tokens
SET used = true, "updatedAt" = NOW()
WHERE "userId" = $1 AND used = false;

-- Silent Login Session Queries

-- name: GetSilentLoginSessionByID :one
SELECT * FROM silent_login_sessions
WHERE id = $1 AND active = true AND "expiresAt" > NOW();

-- name: CreateSilentLoginSession :one
INSERT INTO silent_login_sessions (
    id, "userId", "jwtToken", "clientUrl", "createdAt", "expiresAt", "lastAccessedAt", active
) VALUES (
    $1, $2, $3, $4, NOW(), $5, NOW(), true
) RETURNING *;

-- name: UpdateSilentLoginSessionAccess :exec
UPDATE silent_login_sessions
SET "lastAccessedAt" = NOW()
WHERE id = $1;

-- name: DeactivateSilentLoginSession :exec
UPDATE silent_login_sessions
SET active = false
WHERE id = $1;

-- name: DeactivateUserSilentLoginSessions :exec
UPDATE silent_login_sessions
SET active = false
WHERE "userId" = $1;

-- name: GetActiveSilentLoginSessionsByUser :many
SELECT * FROM silent_login_sessions
WHERE "userId" = $1 AND active = true AND "expiresAt" > NOW()
ORDER BY "createdAt" DESC;

-- name: DeleteExpiredSilentLoginSessions :exec
DELETE FROM silent_login_sessions WHERE "expiresAt" < NOW();

-- Email Verification Code Queries

-- name: GetEmailVerificationCode :one
SELECT * FROM email_verification_codes
WHERE email = $1 AND code = $2 AND verified = false AND "expiresAt" > NOW();

-- name: GetEmailVerificationCodeByID :one
SELECT * FROM email_verification_codes WHERE id = $1;

-- name: CreateEmailVerificationCode :one
INSERT INTO email_verification_codes (
    id, email, code, "expiresAt", verified, attempts, "createdAt"
) VALUES (
    $1, $2, $3, $4, false, 0, NOW()
) RETURNING *;

-- name: MarkEmailVerificationCodeVerified :exec
UPDATE email_verification_codes
SET verified = true
WHERE id = $1;

-- name: IncrementEmailVerificationAttempts :exec
UPDATE email_verification_codes
SET attempts = attempts + 1
WHERE id = $1;

-- name: GetLatestEmailVerificationCode :one
SELECT * FROM email_verification_codes
WHERE email = $1 AND verified = false AND "expiresAt" > NOW()
ORDER BY "createdAt" DESC
LIMIT 1;

-- name: DeleteExpiredEmailVerificationCodes :exec
DELETE FROM email_verification_codes WHERE "expiresAt" < NOW();

-- name: DeleteEmailVerificationCodesByEmail :exec
DELETE FROM email_verification_codes WHERE email = $1;

-- name: DeleteEmailVerificationCodeByID :exec
DELETE FROM email_verification_codes WHERE id = $1;

-- name: CountRecentEmailVerificationCodes :one
SELECT COUNT(*) FROM email_verification_codes WHERE email = $1 AND "createdAt" >= $2;

-- name: DeletePasswordResetTokenByToken :exec
DELETE FROM password_reset_tokens WHERE token = $1;

-- name: DeleteUnverifiedEmailCodesByEmail :exec
DELETE FROM email_verification_codes WHERE email = $1 AND verified = false;

-- name: GetPasswordResetTokenRaw :one
-- Gets token without checking expiry/used (handler validates manually)
SELECT "userId", email, used, "expiresAt" FROM password_reset_tokens
WHERE token = $1;

-- name: MarkPasswordResetTokenUsedByToken :exec
UPDATE password_reset_tokens SET used = true, "updatedAt" = NOW()
WHERE token = $1;
