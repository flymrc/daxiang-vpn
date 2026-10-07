CREATE TABLE IF NOT EXISTS admin_users (
  username TEXT PRIMARY KEY,
  password_hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_sessions (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL REFERENCES admin_users(username) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  csrf_token TEXT NOT NULL,
  source_ip TEXT NOT NULL,
  user_agent TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_admin_sessions_token_hash ON admin_sessions(token_hash);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_expires_at ON admin_sessions(expires_at);

CREATE TABLE IF NOT EXISTS admin_login_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  occurred_at TEXT NOT NULL,
  username TEXT NOT NULL,
  source_ip TEXT NOT NULL,
  success INTEGER NOT NULL,
  error_code TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_admin_login_attempts_lookup
  ON admin_login_attempts(username, source_ip, occurred_at);

CREATE TABLE IF NOT EXISTS tokens_cache (
  token TEXT PRIMARY KEY,
  masked_token TEXT NOT NULL,
  client_name TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  expires_at TEXT,
  egress_id TEXT NOT NULL,
  egress_name TEXT NOT NULL,
  wg_address TEXT NOT NULL,
  last_sync_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS token_leases (
  token TEXT PRIMARY KEY,
  masked_token TEXT NOT NULL,
  client_name TEXT NOT NULL,
  source_ip TEXT NOT NULL,
  egress_id TEXT NOT NULL,
  seen_at TEXT NOT NULL,
  expires_at TEXT
);

CREATE TABLE IF NOT EXISTS egress_nodes (
  egress_id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  region TEXT NOT NULL,
  type TEXT NOT NULL,
  management_addr TEXT NOT NULL,
  proxy_addr TEXT NOT NULL,
  deprecated INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS rotate_locks (
  egress_id TEXT PRIMARY KEY,
  started_at TEXT NOT NULL,
  until_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  occurred_at TEXT NOT NULL,
  actor TEXT NOT NULL,
  source_ip TEXT NOT NULL,
  event_type TEXT NOT NULL,
  target TEXT NOT NULL,
  detail_json TEXT NOT NULL,
  result TEXT NOT NULL,
  error_code TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_events_occurred_at ON audit_events(occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_type_time ON audit_events(event_type, occurred_at DESC);

CREATE TABLE IF NOT EXISTS client_migration_observations (
  token_id TEXT PRIMARY KEY,
  first_seen_unix_ns INTEGER NOT NULL,
  last_seen_unix_ns INTEGER NOT NULL,
  last_seen_at TEXT NOT NULL,
  client_product TEXT NOT NULL,
  client_version TEXT NOT NULL,
  protocol_version INTEGER NOT NULL,
  ingress TEXT NOT NULL,
  key_mode TEXT NOT NULL,
  private_key_returned INTEGER NOT NULL,
  migration_class TEXT NOT NULL,
  last_secure_bootstrap_unix_ns INTEGER NOT NULL DEFAULT 0,
  last_legacy_unix_ns INTEGER NOT NULL DEFAULT 0,
  last_unknown_unix_ns INTEGER NOT NULL DEFAULT 0,
  secure_bootstrap_count INTEGER NOT NULL DEFAULT 0,
  legacy_count INTEGER NOT NULL DEFAULT 0,
  unknown_count INTEGER NOT NULL DEFAULT 0,
  compat_ingress_count INTEGER NOT NULL DEFAULT 0
);

-- Provisional inventory is an approval record, never campaign activation.
-- No T0/compliant column or reset mutation exists. Old negative facts survive
-- pruning of the general-purpose audit table.
CREATE TABLE IF NOT EXISTS migration_inventory (
  singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
  registry_id TEXT NOT NULL UNIQUE CHECK(length(registry_id) = 32),
  approved_sha256 TEXT NOT NULL CHECK(length(approved_sha256) = 64),
  baseline_count INTEGER NOT NULL CHECK(baseline_count BETWEEN 1 AND 4096),
  registered_at TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS migration_inventory_no_update BEFORE UPDATE ON migration_inventory BEGIN SELECT RAISE(ABORT, 'immutable_inventory'); END;
CREATE TRIGGER IF NOT EXISTS migration_inventory_no_delete BEFORE DELETE ON migration_inventory BEGIN SELECT RAISE(ABORT, 'immutable_inventory'); END;
CREATE TABLE IF NOT EXISTS migration_members (
  token_id TEXT PRIMARY KEY CHECK(length(token_id) = 12),
  membership TEXT NOT NULL CHECK(membership IN ('baseline', 'extra')),
  owner_ref TEXT NOT NULL,
  shared INTEGER NOT NULL CHECK(shared IN (0,1)),
  installation_refs_json TEXT NOT NULL,
  registered_at TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS migration_members_no_update BEFORE UPDATE ON migration_members BEGIN SELECT RAISE(ABORT, 'immutable_member'); END;
CREATE TRIGGER IF NOT EXISTS migration_members_no_delete BEFORE DELETE ON migration_members BEGIN SELECT RAISE(ABORT, 'immutable_member'); END;
CREATE TRIGGER IF NOT EXISTS migration_members_capacity BEFORE INSERT ON migration_members WHEN (SELECT COUNT(*) FROM migration_members) >= 4096 BEGIN SELECT RAISE(ABORT, 'member_capacity'); END;
CREATE TABLE IF NOT EXISTS migration_observation_facts (
  token_id TEXT PRIMARY KEY CHECK(length(token_id) = 12),
  first_seen_unix_ns INTEGER NOT NULL CHECK(first_seen_unix_ns > 0),
  last_seen_unix_ns INTEGER NOT NULL CHECK(last_seen_unix_ns >= first_seen_unix_ns),
  secure_bootstrap_count INTEGER NOT NULL CHECK(secure_bootstrap_count BETWEEN 0 AND 9007199254740991),
  legacy_count INTEGER NOT NULL CHECK(legacy_count BETWEEN 0 AND 9007199254740991),
  unknown_count INTEGER NOT NULL CHECK(unknown_count BETWEEN 0 AND 9007199254740991),
  compat_ingress_count INTEGER NOT NULL CHECK(compat_ingress_count BETWEEN 0 AND 9007199254740991),
  denied_count INTEGER NOT NULL CHECK(denied_count BETWEEN 0 AND 9007199254740991),
  error_count INTEGER NOT NULL CHECK(error_count BETWEEN 0 AND 9007199254740991)
);
CREATE TRIGGER IF NOT EXISTS migration_facts_no_delete BEFORE DELETE ON migration_observation_facts BEGIN SELECT RAISE(ABORT, 'immutable_history'); END;
CREATE TRIGGER IF NOT EXISTS migration_facts_monotonic BEFORE UPDATE ON migration_observation_facts
WHEN NEW.token_id != OLD.token_id OR NEW.first_seen_unix_ns > OLD.first_seen_unix_ns OR NEW.last_seen_unix_ns < OLD.last_seen_unix_ns
 OR NEW.secure_bootstrap_count < OLD.secure_bootstrap_count OR NEW.legacy_count < OLD.legacy_count OR NEW.unknown_count < OLD.unknown_count
 OR NEW.compat_ingress_count < OLD.compat_ingress_count OR NEW.denied_count < OLD.denied_count OR NEW.error_count < OLD.error_count
BEGIN SELECT RAISE(ABORT, 'nonmonotonic_history'); END;
CREATE TRIGGER IF NOT EXISTS migration_facts_capacity BEFORE INSERT ON migration_observation_facts WHEN NOT EXISTS(SELECT 1 FROM migration_observation_facts WHERE token_id=NEW.token_id) AND (SELECT COUNT(*) FROM migration_observation_facts) >= 4096 BEGIN SELECT RAISE(ABORT, 'history_capacity'); END;
CREATE TABLE IF NOT EXISTS migration_observer_runs (
  run_id TEXT PRIMARY KEY CHECK(length(run_id) = 32),
  started_at TEXT NOT NULL,
  ended_at TEXT,
  state TEXT NOT NULL CHECK(state IN ('open', 'closed', 'failed')),
  reason TEXT NOT NULL CHECK(reason IN ('none', 'write_failed', 'unclean_shutdown', 'sink_replaced', 'observer_closed')),
  last_success_at TEXT
);
CREATE TRIGGER IF NOT EXISTS migration_runs_no_delete BEFORE DELETE ON migration_observer_runs BEGIN SELECT RAISE(ABORT, 'immutable_run'); END;
CREATE TRIGGER IF NOT EXISTS migration_runs_terminal BEFORE UPDATE ON migration_observer_runs
WHEN OLD.run_id != NEW.run_id OR OLD.started_at != NEW.started_at OR OLD.state = 'failed' OR (OLD.state = 'closed' AND NEW.state != 'failed')
BEGIN SELECT RAISE(ABORT, 'terminal_run'); END;
CREATE TRIGGER IF NOT EXISTS migration_runs_capacity BEFORE INSERT ON migration_observer_runs WHEN (SELECT COUNT(*) FROM migration_observer_runs) >= 4096 BEGIN SELECT RAISE(ABORT, 'run_capacity'); END;

-- A v1 latest "secure" label with invalid metadata creates one durable unknown
-- fact. Later valid metadata cannot erase it and reopening cannot count it twice.
CREATE TABLE IF NOT EXISTS migration_import_flags (
  token_id TEXT PRIMARY KEY CHECK(length(token_id)=12),
  reason TEXT NOT NULL CHECK(reason='invalid_metadata')
);
CREATE TRIGGER IF NOT EXISTS migration_import_flags_no_update BEFORE UPDATE ON migration_import_flags BEGIN SELECT RAISE(ABORT, 'immutable_import_flag'); END;
CREATE TRIGGER IF NOT EXISTS migration_import_flags_no_delete BEFORE DELETE ON migration_import_flags BEGIN SELECT RAISE(ABORT, 'immutable_import_flag'); END;
CREATE TRIGGER IF NOT EXISTS migration_import_flags_capacity BEFORE INSERT ON migration_import_flags WHEN NOT EXISTS(SELECT 1 FROM migration_import_flags WHERE token_id=NEW.token_id) AND (SELECT COUNT(*) FROM migration_import_flags)>=4096 BEGIN SELECT RAISE(ABORT,'import_capacity'); END;
