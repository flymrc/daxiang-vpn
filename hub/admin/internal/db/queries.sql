-- name: UpsertAdminUser :exec
INSERT INTO admin_users (username, password_hash, created_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(username) DO UPDATE SET
  password_hash = excluded.password_hash,
  updated_at = excluded.updated_at;

-- name: GetAdminUser :one
SELECT username, password_hash, created_at, updated_at
FROM admin_users
WHERE username = ?;

-- name: CreateAdminSession :exec
INSERT INTO admin_sessions (
  id, username, token_hash, csrf_token, source_ip, user_agent,
  created_at, last_seen_at, expires_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAdminSessionByHash :one
SELECT id, username, token_hash, csrf_token, source_ip, user_agent,
       created_at, last_seen_at, expires_at
FROM admin_sessions
WHERE token_hash = ?;

-- name: TouchAdminSession :exec
UPDATE admin_sessions
SET last_seen_at = ?
WHERE token_hash = ?;

-- name: DeleteAdminSessionByHash :exec
DELETE FROM admin_sessions
WHERE token_hash = ?;

-- name: DeleteExpiredAdminSessions :exec
DELETE FROM admin_sessions
WHERE expires_at <= ?;

-- name: InsertLoginAttempt :exec
INSERT INTO admin_login_attempts (occurred_at, username, source_ip, success, error_code)
VALUES (?, ?, ?, ?, ?);

-- name: CountRecentFailedLoginAttempts :one
SELECT count(*)
FROM admin_login_attempts
WHERE username = ?
  AND source_ip = ?
  AND success = 0
  AND occurred_at >= ?;

-- name: UpsertTokenCache :exec
INSERT INTO tokens_cache (
  token, masked_token, client_name, enabled, expires_at,
  egress_id, egress_name, wg_address, last_sync_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(token) DO UPDATE SET
  masked_token = excluded.masked_token,
  client_name = excluded.client_name,
  enabled = excluded.enabled,
  expires_at = excluded.expires_at,
  egress_id = excluded.egress_id,
  egress_name = excluded.egress_name,
  wg_address = excluded.wg_address,
  last_sync_at = excluded.last_sync_at;

-- name: UpsertTokenLease :exec
INSERT INTO token_leases (
  token, masked_token, client_name, source_ip, egress_id, seen_at, expires_at
)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(token) DO UPDATE SET
  masked_token = excluded.masked_token,
  client_name = excluded.client_name,
  source_ip = excluded.source_ip,
  egress_id = excluded.egress_id,
  seen_at = excluded.seen_at,
  expires_at = excluded.expires_at;

-- name: DeleteExpiredTokenLeases :exec
DELETE FROM token_leases
WHERE expires_at IS NOT NULL AND expires_at <= ?;

-- name: UpsertEgressNode :exec
INSERT INTO egress_nodes (
  egress_id, display_name, region, type, management_addr, proxy_addr, deprecated, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(egress_id) DO UPDATE SET
  display_name = excluded.display_name,
  region = excluded.region,
  type = excluded.type,
  management_addr = excluded.management_addr,
  proxy_addr = excluded.proxy_addr,
  deprecated = excluded.deprecated,
  updated_at = excluded.updated_at;

-- name: UpsertRotateLock :exec
INSERT INTO rotate_locks (egress_id, started_at, until_at)
VALUES (?, ?, ?)
ON CONFLICT(egress_id) DO UPDATE SET
  started_at = excluded.started_at,
  until_at = excluded.until_at;

-- name: DeleteRotateLock :exec
DELETE FROM rotate_locks
WHERE egress_id = ?;

-- name: InsertAuditEvent :exec
INSERT INTO audit_events (
  occurred_at, actor, source_ip, event_type, target, detail_json, result, error_code
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditEvents :many
SELECT id, occurred_at, actor, source_ip, event_type, target, detail_json, result, error_code
FROM audit_events
ORDER BY occurred_at DESC, id DESC
LIMIT ?;

-- name: CountAuditEventsSince :one
SELECT count(*)
FROM audit_events
WHERE event_type IN ('client.rotate_ip', 'admin.rotate_ip')
  AND result = 'ok'
  AND occurred_at >= ?;
