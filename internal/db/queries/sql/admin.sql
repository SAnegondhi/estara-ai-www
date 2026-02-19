-- Admin and Audit Queries

-- Audit Log Queries

-- name: CreateAuditLog :exec
INSERT INTO audit_logs (
    id, "createdAt", "userId", event, description, "ipAddress", "userAgent",
    metadata, success, error, action, "complianceFlags", endpoint,
    "performanceMetrics", "requestId", resource, "securityContext", "sessionId", severity
) VALUES (
    $1, NOW(), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
);

-- name: GetAuditLogByID :one
SELECT id, "createdAt", "userId", event::text, description, "ipAddress", "userAgent",
    metadata, success, error, action, "complianceFlags", endpoint,
    "performanceMetrics", "requestId", resource, "securityContext", "sessionId", severity
FROM audit_logs WHERE id = $1;

-- name: ListAuditLogs :many
SELECT id, "createdAt", "userId", event::text, description, "ipAddress", "userAgent",
    metadata, success, error, action, "complianceFlags", endpoint,
    "performanceMetrics", "requestId", resource, "securityContext", "sessionId", severity
FROM audit_logs
ORDER BY "createdAt" DESC
LIMIT $1 OFFSET $2;

-- name: ListAuditLogsByUser :many
SELECT id, "createdAt", "userId", event::text, description, "ipAddress", "userAgent",
    metadata, success, error, action, "complianceFlags", endpoint,
    "performanceMetrics", "requestId", resource, "securityContext", "sessionId", severity
FROM audit_logs
WHERE "userId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditLogsByEventType :many
SELECT id, "createdAt", "userId", event::text, description, "ipAddress", "userAgent",
    metadata, success, error, action, "complianceFlags", endpoint,
    "performanceMetrics", "requestId", resource, "securityContext", "sessionId", severity
FROM audit_logs
WHERE event::text = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditLogsBySeverity :many
SELECT id, "createdAt", "userId", event::text, description, "ipAddress", "userAgent",
    metadata, success, error, action, "complianceFlags", endpoint,
    "performanceMetrics", "requestId", resource, "securityContext", "sessionId", severity
FROM audit_logs
WHERE severity = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditLogsByDateRange :many
SELECT id, "createdAt", "userId", event::text, description, "ipAddress", "userAgent",
    metadata, success, error, action, "complianceFlags", endpoint,
    "performanceMetrics", "requestId", resource, "securityContext", "sessionId", severity
FROM audit_logs
WHERE "createdAt" >= $1 AND "createdAt" <= $2
ORDER BY "createdAt" DESC
LIMIT $3 OFFSET $4;

-- name: CountAuditLogsByEventType :many
SELECT event::text, COUNT(*) as count
FROM audit_logs
WHERE "createdAt" >= $1 AND "createdAt" <= $2
GROUP BY event::text
ORDER BY count DESC;

-- name: DeleteAuditLogsOlderThan :exec
DELETE FROM audit_logs WHERE "createdAt" < $1;

-- Admin Audit Log Queries

-- name: CreateAdminAuditLog :exec
INSERT INTO admin_audit_log (
    id, "adminId", "adminEmail", "actorType", action, resource, "resourceId",
    details, "ipAddress", "userAgent", "createdAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW()
);

-- name: GetAdminAuditLogByID :one
SELECT id, "adminId", "adminEmail", "actorType"::text, action::text, resource, "resourceId",
    details, "ipAddress", "userAgent", "createdAt"
FROM admin_audit_log WHERE id = $1;

-- name: ListAdminAuditLogs :many
SELECT id, "adminId", "adminEmail", "actorType"::text, action::text, resource, "resourceId",
    details, "ipAddress", "userAgent", "createdAt"
FROM admin_audit_log
WHERE (sqlc.narg('actor_type')::text IS NULL OR "actorType"::text = sqlc.narg('actor_type'))
  AND (sqlc.narg('action_filter')::text IS NULL OR action::text = sqlc.narg('action_filter'))
  AND (sqlc.narg('resource_filter')::text IS NULL OR resource = sqlc.narg('resource_filter'))
  AND (sqlc.narg('admin_id')::text IS NULL OR "adminId" = sqlc.narg('admin_id'))
ORDER BY "createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAdminAuditLogs :one
SELECT COUNT(*) FROM admin_audit_log
WHERE (sqlc.narg('actor_type')::text IS NULL OR "actorType"::text = sqlc.narg('actor_type'))
  AND (sqlc.narg('action_filter')::text IS NULL OR action::text = sqlc.narg('action_filter'))
  AND (sqlc.narg('resource_filter')::text IS NULL OR resource = sqlc.narg('resource_filter'))
  AND (sqlc.narg('admin_id')::text IS NULL OR "adminId" = sqlc.narg('admin_id'));

-- name: ListAdminAuditLogsByAdmin :many
SELECT id, "adminId", "adminEmail", "actorType"::text, action::text, resource, "resourceId",
    details, "ipAddress", "userAgent", "createdAt"
FROM admin_audit_log
WHERE "adminId" = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListAdminAuditLogsByAction :many
SELECT id, "adminId", "adminEmail", "actorType"::text, action::text, resource, "resourceId",
    details, "ipAddress", "userAgent", "createdAt"
FROM admin_audit_log
WHERE action::text = $1
ORDER BY "createdAt" DESC
LIMIT $2 OFFSET $3;

-- name: ListAdminAuditLogsByResource :many
SELECT id, "adminId", "adminEmail", "actorType"::text, action::text, resource, "resourceId",
    details, "ipAddress", "userAgent", "createdAt"
FROM admin_audit_log
WHERE resource = $1 AND ("resourceId" = $2 OR $2 IS NULL)
ORDER BY "createdAt" DESC
LIMIT $3 OFFSET $4;

-- name: ListAdminAuditLogsByDateRange :many
SELECT id, "adminId", "adminEmail", "actorType"::text, action::text, resource, "resourceId",
    details, "ipAddress", "userAgent", "createdAt"
FROM admin_audit_log
WHERE "createdAt" >= $1 AND "createdAt" <= $2
ORDER BY "createdAt" DESC
LIMIT $3 OFFSET $4;

-- Admin Session Queries

-- name: CreateAdminSession :one
INSERT INTO admin_sessions (
    id, "userId", "tokenHash", "ipAddress", "userAgent",
    "deviceName", location, "createdAt", "lastActive", "expiresAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, NOW(), NOW(), $8
) RETURNING *;

-- name: GetAdminSessionByID :one
SELECT * FROM admin_sessions
WHERE id = $1 AND "revokedAt" IS NULL AND "expiresAt" > NOW();

-- name: GetAdminSessionByTokenHash :one
SELECT * FROM admin_sessions
WHERE "tokenHash" = $1 AND "revokedAt" IS NULL AND "expiresAt" > NOW();

-- name: ListAdminSessionsByUser :many
SELECT * FROM admin_sessions
WHERE "userId" = $1 AND "revokedAt" IS NULL
ORDER BY "createdAt" DESC;

-- name: ListActiveAdminSessions :many
SELECT * FROM admin_sessions
WHERE "revokedAt" IS NULL AND "expiresAt" > NOW()
ORDER BY "lastActive" DESC;

-- name: UpdateAdminSessionLastActive :exec
UPDATE admin_sessions SET "lastActive" = NOW() WHERE id = $1;

-- name: RevokeAdminSession :exec
UPDATE admin_sessions SET "revokedAt" = NOW() WHERE id = $1;

-- name: RevokeAllAdminSessionsByUser :exec
UPDATE admin_sessions SET "revokedAt" = NOW()
WHERE "userId" = $1 AND "revokedAt" IS NULL;

-- name: DeleteExpiredAdminSessions :exec
DELETE FROM admin_sessions WHERE "expiresAt" < NOW();

-- Admin Two-Factor Queries

-- name: GetAdminTwoFactorByUserID :one
SELECT * FROM admin_two_factor WHERE "userId" = $1;

-- name: CreateAdminTwoFactor :one
INSERT INTO admin_two_factor (
    id, "userId", secret, enabled, "backupCodes", "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, false, $4, NOW(), NOW()
) RETURNING *;

-- name: EnableAdminTwoFactor :exec
UPDATE admin_two_factor SET
    enabled = true,
    "updatedAt" = NOW()
WHERE "userId" = $1;

-- name: DisableAdminTwoFactor :exec
UPDATE admin_two_factor SET
    enabled = false,
    "updatedAt" = NOW()
WHERE "userId" = $1;

-- name: UpdateAdminTwoFactorBackupCodes :exec
UPDATE admin_two_factor SET
    "backupCodes" = $2,
    "updatedAt" = NOW()
WHERE "userId" = $1;

-- name: DeleteAdminTwoFactor :exec
DELETE FROM admin_two_factor WHERE "userId" = $1;

-- System Alerts Queries

-- name: GetSystemAlertByID :one
SELECT * FROM system_alerts WHERE id = $1;

-- name: GetSystemAlertByKey :one
SELECT * FROM system_alerts WHERE "alertKey" = $1;

-- name: CreateSystemAlert :one
INSERT INTO system_alerts (
    id, type, severity, title, description, "alertKey", metadata,
    "firstSeen", "lastSeen", "occurrenceCount", dismissed, "actionRequired",
    "expiresAt", "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $8, 1, false, $9, $10, NOW(), NOW()
) RETURNING *;

-- name: UpsertSystemAlert :one
INSERT INTO system_alerts (
    id, type, severity, title, description, "alertKey", metadata,
    "firstSeen", "lastSeen", "occurrenceCount", dismissed, "actionRequired",
    "expiresAt", "createdAt", "updatedAt"
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, NOW(), NOW(), 1, false, $8, $9, NOW(), NOW()
)
ON CONFLICT ("alertKey")
DO UPDATE SET
    "lastSeen" = NOW(),
    "occurrenceCount" = system_alerts."occurrenceCount" + 1,
    severity = EXCLUDED.severity,
    title = EXCLUDED.title,
    description = EXCLUDED.description,
    metadata = EXCLUDED.metadata,
    "updatedAt" = NOW()
RETURNING *;

-- name: ListActiveSystemAlerts :many
SELECT * FROM system_alerts
WHERE dismissed = false AND ("expiresAt" IS NULL OR "expiresAt" > NOW())
ORDER BY
    CASE severity
        WHEN 'critical' THEN 1
        WHEN 'error' THEN 2
        WHEN 'warning' THEN 3
        ELSE 4
    END,
    "lastSeen" DESC
LIMIT $1 OFFSET $2;

-- name: ListSystemAlertsBySeverity :many
SELECT * FROM system_alerts
WHERE severity = $1 AND dismissed = false
ORDER BY "lastSeen" DESC
LIMIT $2 OFFSET $3;

-- name: ListSystemAlertsRequiringAction :many
SELECT * FROM system_alerts
WHERE "actionRequired" = true AND dismissed = false
ORDER BY "lastSeen" DESC;

-- name: DismissSystemAlert :exec
UPDATE system_alerts SET
    dismissed = true,
    "updatedAt" = NOW()
WHERE id = $1;

-- name: DismissSystemAlertByKey :exec
UPDATE system_alerts SET
    dismissed = true,
    "updatedAt" = NOW()
WHERE "alertKey" = $1;

-- name: CountActiveAlertsBySeverity :many
SELECT severity, COUNT(*) as count
FROM system_alerts
WHERE dismissed = false AND ("expiresAt" IS NULL OR "expiresAt" > NOW())
GROUP BY severity;

-- name: DeleteExpiredSystemAlerts :exec
DELETE FROM system_alerts WHERE "expiresAt" IS NOT NULL AND "expiresAt" < NOW();

-- ===============================
-- Admin Audit Log Cleanup Queries
-- ===============================

-- name: DeleteOldAdminAuditLogs :execrows
DELETE FROM admin_audit_log
WHERE "createdAt" < $1;

-- name: CountOldAdminAuditLogs :one
SELECT COUNT(*)
FROM admin_audit_log
WHERE "createdAt" < $1;
