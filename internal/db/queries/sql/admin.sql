-- Admin Audit Log Queries (Unified audit_logs table)

-- name: CreateAdminAuditLog :exec
INSERT INTO audit_logs (
    id, "adminId", "adminEmail", "actorType", event, action, resource, "resourceId",
    metadata, "ipAddress", "userAgent", "createdAt"
) VALUES (
    sqlc.arg('id'),
    sqlc.arg('admin_id'),
    sqlc.arg('admin_email'),
    sqlc.arg('actor_type')::"AuditActorType",
    'ADMIN_ACTION'::"AuditEventType",
    sqlc.arg('action'),
    sqlc.arg('resource'),
    sqlc.narg('resource_id'),
    sqlc.arg('details'),
    sqlc.arg('ip_address'),
    sqlc.arg('user_agent'),
    NOW()
);

-- name: ListAdminAuditLogs :many
SELECT id, COALESCE("adminId", "userId") as "adminId", COALESCE("adminEmail", '') as "adminEmail",
    "actorType"::text, COALESCE(action::text, event::text) as action, resource, "resourceId",
    metadata as details, "ipAddress", "userAgent", "createdAt"
FROM audit_logs
WHERE (sqlc.narg('actor_type')::text IS NULL OR "actorType"::text = sqlc.narg('actor_type'))
  AND (sqlc.narg('action_filter')::text IS NULL OR (action::text = sqlc.narg('action_filter') OR event::text = sqlc.narg('action_filter')))
  AND (sqlc.narg('resource_filter')::text IS NULL OR resource = sqlc.narg('resource_filter'))
  AND (sqlc.narg('admin_id')::text IS NULL OR ("adminId" = sqlc.narg('admin_id') OR "userId" = sqlc.narg('admin_id')))
ORDER BY "createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAdminAuditLogs :one
SELECT COUNT(*) FROM audit_logs
WHERE (sqlc.narg('actor_type')::text IS NULL OR "actorType"::text = sqlc.narg('actor_type'))
  AND (sqlc.narg('action_filter')::text IS NULL OR (action::text = sqlc.narg('action_filter') OR event::text = sqlc.narg('action_filter')))
  AND (sqlc.narg('resource_filter')::text IS NULL OR resource = sqlc.narg('resource_filter'))
  AND (sqlc.narg('admin_id')::text IS NULL OR ("adminId" = sqlc.narg('admin_id') OR "userId" = sqlc.narg('admin_id')));

-- name: SearchAdminAuditLogsByDetailsText :many
SELECT id, "adminId", "adminEmail", "actorType"::text, action::text, resource, "resourceId",
    metadata as details, "ipAddress", "userAgent", "createdAt"
FROM audit_logs
WHERE metadata::text ILIKE '%' || sqlc.arg('search_text')::text || '%'
  AND (sqlc.narg('actor_type')::text IS NULL OR "actorType"::text = sqlc.narg('actor_type'))
  AND (sqlc.narg('action')::text IS NULL OR action::text = sqlc.narg('action'))
  AND (sqlc.narg('resource')::text IS NULL OR resource = sqlc.narg('resource'))
ORDER BY "createdAt" DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAdminAuditLogsByDetailsText :one
SELECT COUNT(*) FROM audit_logs
WHERE metadata::text ILIKE '%' || sqlc.arg('search_text')::text || '%'
  AND (sqlc.narg('actor_type')::text IS NULL OR "actorType"::text = sqlc.narg('actor_type'))
  AND (sqlc.narg('action')::text IS NULL OR action::text = sqlc.narg('action'))
  AND (sqlc.narg('resource')::text IS NULL OR resource = sqlc.narg('resource'));

-- name: DeleteOldAdminAuditLogs :execrows
DELETE FROM audit_logs
WHERE "createdAt" < sqlc.arg('cutoff_timestamp') AND "actorType" IS NOT NULL;

-- name: CountOldAdminAuditLogs :one
SELECT COUNT(*) FROM audit_logs
WHERE "createdAt" < sqlc.arg('cutoff_timestamp') AND "actorType" IS NOT NULL;
