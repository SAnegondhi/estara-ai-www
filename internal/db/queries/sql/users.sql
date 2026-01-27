-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByStripeCustomerID :one
SELECT * FROM users WHERE "stripeCustomerId" = $1;

-- name: CreateUser :one
INSERT INTO users (
    id, email, "firstName", "lastName", role,
    "subscriptionTier", "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, NOW(), NOW()
) RETURNING *;

-- name: UpdateUserUpdatedAt :exec
UPDATE users SET "updatedAt" = NOW() WHERE id = $1;

-- name: UpdateUserSubscription :exec
UPDATE users SET
    "subscriptionTier" = $2,
    "stripeCustomerId" = $3,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: UpdateUserStripeCustomer :exec
UPDATE users SET
    "stripeCustomerId" = $2,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: ListUsersByRole :many
SELECT * FROM users
WHERE role = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: SearchUsersByEmail :many
SELECT * FROM users
WHERE email ILIKE '%' || $1 || '%'
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: UpdateUserRole :exec
UPDATE users SET
    role = $2,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: UpdateUserProfile :exec
UPDATE users SET
    "firstName" = COALESCE($2, "firstName"),
    "lastName" = COALESCE($3, "lastName"),
    email = COALESCE($4, email),
    "updatedAt" = NOW()
WHERE id = $1;

-- name: GetUserStats :one
SELECT
    COUNT(*) FILTER (WHERE role = 'ADMIN') as admin_count,
    COUNT(*) FILTER (WHERE role = 'USER') as user_count,
    COUNT(*) FILTER (WHERE "subscriptionTier" = 'free') as free_count,
    COUNT(*) FILTER (WHERE "subscriptionTier" = 'pro') as pro_count
FROM users;

-- name: SuspendUser :exec
UPDATE users SET
    "suspendedAt" = NOW(),
    "suspendedBy" = $2,
    "suspendReason" = $3,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: UnsuspendUser :exec
UPDATE users SET
    "suspendedAt" = NULL,
    "suspendedBy" = NULL,
    "suspendReason" = NULL,
    "updatedAt" = NOW()
WHERE id = $1;
