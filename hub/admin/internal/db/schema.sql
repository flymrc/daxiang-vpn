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
