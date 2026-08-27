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

-- name: DeleteAdminLoginAttemptsBefore :exec
DELETE FROM admin_login_attempts
WHERE occurred_at < ?;

-- name: PruneAdminLoginAttempts :exec
DELETE FROM admin_login_attempts
WHERE id NOT IN (
  SELECT id
  FROM admin_login_attempts
  ORDER BY occurred_at DESC, id DESC
  LIMIT ?
);

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

-- name: DeleteExpiredRotateLocks :exec
DELETE FROM rotate_locks
WHERE until_at <= ?;

-- name: InsertAuditEvent :exec
INSERT INTO audit_events (
  occurred_at, actor, source_ip, event_type, target, detail_json, result, error_code
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAuditEventsBefore :exec
DELETE FROM audit_events
WHERE occurred_at < ?;

-- name: PruneAuditEvents :exec
DELETE FROM audit_events
WHERE id NOT IN (
  SELECT id
  FROM audit_events
  ORDER BY occurred_at DESC, id DESC
  LIMIT ?
);

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

-- name: UpsertClientMigrationObservation :exec
INSERT INTO client_migration_observations (
  token_id, first_seen_unix_ns, last_seen_unix_ns, last_seen_at,
  client_product, client_version, protocol_version, ingress, key_mode,
  private_key_returned, migration_class,
  last_secure_bootstrap_unix_ns, last_legacy_unix_ns, last_unknown_unix_ns,
  secure_bootstrap_count, legacy_count, unknown_count, compat_ingress_count
)
VALUES (
  sqlc.arg(token_id), sqlc.arg(occurred_at_unix_ns), sqlc.arg(occurred_at_unix_ns), sqlc.arg(occurred_at),
  sqlc.arg(client_product), sqlc.arg(client_version), sqlc.arg(protocol_version), sqlc.arg(ingress), sqlc.arg(key_mode),
  sqlc.arg(private_key_returned), sqlc.arg(migration_class),
  CASE WHEN sqlc.arg(migration_class) = 'secure_bootstrap' THEN sqlc.arg(occurred_at_unix_ns) ELSE 0 END,
  CASE WHEN sqlc.arg(migration_class) = 'legacy' THEN sqlc.arg(occurred_at_unix_ns) ELSE 0 END,
  CASE WHEN sqlc.arg(migration_class) = 'unknown' THEN sqlc.arg(occurred_at_unix_ns) ELSE 0 END,
  CASE WHEN sqlc.arg(migration_class) = 'secure_bootstrap' THEN 1 ELSE 0 END,
  CASE WHEN sqlc.arg(migration_class) = 'legacy' THEN 1 ELSE 0 END,
  CASE WHEN sqlc.arg(migration_class) = 'unknown' THEN 1 ELSE 0 END,
  CASE WHEN sqlc.arg(ingress) = 'compat' THEN 1 ELSE 0 END
)
ON CONFLICT(token_id) DO UPDATE SET
  first_seen_unix_ns = MIN(client_migration_observations.first_seen_unix_ns, excluded.first_seen_unix_ns),
  last_seen_unix_ns = MAX(client_migration_observations.last_seen_unix_ns, excluded.last_seen_unix_ns),
  last_seen_at = CASE WHEN excluded.last_seen_unix_ns >= client_migration_observations.last_seen_unix_ns THEN excluded.last_seen_at ELSE client_migration_observations.last_seen_at END,
  client_product = CASE WHEN excluded.last_seen_unix_ns >= client_migration_observations.last_seen_unix_ns THEN excluded.client_product ELSE client_migration_observations.client_product END,
  client_version = CASE WHEN excluded.last_seen_unix_ns >= client_migration_observations.last_seen_unix_ns THEN excluded.client_version ELSE client_migration_observations.client_version END,
  protocol_version = CASE WHEN excluded.last_seen_unix_ns >= client_migration_observations.last_seen_unix_ns THEN excluded.protocol_version ELSE client_migration_observations.protocol_version END,
  ingress = CASE WHEN excluded.last_seen_unix_ns >= client_migration_observations.last_seen_unix_ns THEN excluded.ingress ELSE client_migration_observations.ingress END,
  key_mode = CASE WHEN excluded.last_seen_unix_ns >= client_migration_observations.last_seen_unix_ns THEN excluded.key_mode ELSE client_migration_observations.key_mode END,
  private_key_returned = CASE WHEN excluded.last_seen_unix_ns >= client_migration_observations.last_seen_unix_ns THEN excluded.private_key_returned ELSE client_migration_observations.private_key_returned END,
  migration_class = CASE WHEN excluded.last_seen_unix_ns >= client_migration_observations.last_seen_unix_ns THEN excluded.migration_class ELSE client_migration_observations.migration_class END,
  last_secure_bootstrap_unix_ns = MAX(client_migration_observations.last_secure_bootstrap_unix_ns, excluded.last_secure_bootstrap_unix_ns),
  last_legacy_unix_ns = MAX(client_migration_observations.last_legacy_unix_ns, excluded.last_legacy_unix_ns),
  last_unknown_unix_ns = MAX(client_migration_observations.last_unknown_unix_ns, excluded.last_unknown_unix_ns),
  secure_bootstrap_count = client_migration_observations.secure_bootstrap_count + excluded.secure_bootstrap_count,
  legacy_count = client_migration_observations.legacy_count + excluded.legacy_count,
  unknown_count = client_migration_observations.unknown_count + excluded.unknown_count,
  compat_ingress_count = client_migration_observations.compat_ingress_count + excluded.compat_ingress_count;

-- name: ListClientMigrationObservations :many
SELECT token_id, first_seen_unix_ns, last_seen_unix_ns, last_seen_at,
       client_product, client_version, protocol_version, ingress, key_mode,
       private_key_returned, migration_class,
       last_secure_bootstrap_unix_ns, last_legacy_unix_ns, last_unknown_unix_ns,
       secure_bootstrap_count, legacy_count, unknown_count, compat_ingress_count
FROM client_migration_observations
ORDER BY token_id;
