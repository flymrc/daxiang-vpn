-- Offline device authorization foundation. These names do not replace Admin or YAML authority.
CREATE TABLE IF NOT EXISTS deviceauth_meta (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    schema_version INTEGER NOT NULL CHECK (schema_version = 2),
    epoch TEXT NOT NULL,
    managed_by TEXT NOT NULL,
    policy_json TEXT NOT NULL,
    fence_path TEXT NOT NULL,
    executor_fence INTEGER NOT NULL DEFAULT 0 CHECK (executor_fence >= 0)
);
CREATE TABLE IF NOT EXISTS deviceauth_devices (
    device_id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role = 'customer'),
    state TEXT NOT NULL CHECK (state IN ('active','disabled','expired','revoked')),
    valid_until INTEGER NOT NULL,
    generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0),
    applied_generation INTEGER NOT NULL DEFAULT 0 CHECK (applied_generation >= 0 AND applied_generation <= generation),
    current_key TEXT,
    current_address TEXT,
    CHECK ((current_key IS NULL) = (current_address IS NULL)),
    CHECK (state = 'active' OR current_key IS NULL)
);
-- Reservations are not automatically recycled: a transfer requires a later audited contract.
CREATE TABLE IF NOT EXISTS deviceauth_addresses (
    address TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES deviceauth_devices(device_id)
);
CREATE TABLE IF NOT EXISTS deviceauth_bindings (
    public_key TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES deviceauth_devices(device_id),
    address TEXT NOT NULL,
    managed_by TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role = 'customer'),
    created_generation INTEGER NOT NULL CHECK (created_generation > 0),
    revoked_generation INTEGER NOT NULL DEFAULT 0 CHECK (revoked_generation >= 0),
    removed_generation INTEGER NOT NULL DEFAULT 0 CHECK (removed_generation >= 0),
    applied INTEGER NOT NULL DEFAULT 0 CHECK (applied IN (0,1)),
    CHECK (revoked_generation = 0 OR revoked_generation > created_generation)
);
CREATE UNIQUE INDEX IF NOT EXISTS deviceauth_current_binding ON deviceauth_bindings(device_id) WHERE revoked_generation = 0;
CREATE TABLE IF NOT EXISTS deviceauth_tombstones (
    public_key TEXT NOT NULL REFERENCES deviceauth_bindings(public_key),
    generation INTEGER NOT NULL,
    reason TEXT NOT NULL,
    committed_at INTEGER NOT NULL,
    verified_at INTEGER,
    PRIMARY KEY (public_key, generation)
);
CREATE TABLE IF NOT EXISTS deviceauth_operations (
    operation_id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('apply','disable','expire','revoke')),
    idempotency_key TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    device_id TEXT NOT NULL REFERENCES deviceauth_devices(device_id),
    epoch TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    committed_at INTEGER NOT NULL,
    deadline INTEGER NOT NULL,
    UNIQUE (actor_id, action, idempotency_key)
);
CREATE TABLE IF NOT EXISTS deviceauth_outbox (
    operation_id TEXT PRIMARY KEY REFERENCES deviceauth_operations(operation_id),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','degraded','done','superseded')),
    attempts INTEGER NOT NULL DEFAULT 0,
    fence INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    verified_at INTEGER
);
CREATE TABLE IF NOT EXISTS deviceauth_intents (
    operation_id TEXT NOT NULL REFERENCES deviceauth_operations(operation_id),
    public_key TEXT NOT NULL REFERENCES deviceauth_bindings(public_key),
    action TEXT NOT NULL CHECK (action IN ('apply','remove')),
    epoch TEXT NOT NULL,
    generation INTEGER NOT NULL,
    fence INTEGER NOT NULL,
    address TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('prepared','verified')),
    PRIMARY KEY (operation_id, public_key, action)
);
-- V2 additive credential schema. Never imports legacy YAML/admin identities.
CREATE TABLE IF NOT EXISTS deviceauth_activations (
    activation_id TEXT PRIMARY KEY,
    verifier TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    valid_until INTEGER NOT NULL,
    activation_until INTEGER NOT NULL,
    consumed_device TEXT REFERENCES deviceauth_devices(device_id),
    consumed_at INTEGER
);
CREATE TABLE IF NOT EXISTS deviceauth_credentials (
    credential_id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES deviceauth_devices(device_id),
    public_key TEXT NOT NULL UNIQUE,
    valid_until INTEGER NOT NULL,
    revoked_at INTEGER,
    replaced_by TEXT REFERENCES deviceauth_credentials(credential_id)
);
CREATE TABLE IF NOT EXISTS deviceauth_challenges (
    challenge_id TEXT PRIMARY KEY,
    nonce TEXT NOT NULL,
    purpose TEXT NOT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    body_digest TEXT NOT NULL,
    device_id TEXT NOT NULL,
    credential_id TEXT NOT NULL,
    activation_id TEXT NOT NULL,
    auth_public_key TEXT NOT NULL,
    expires INTEGER NOT NULL,
    client_nonce TEXT NOT NULL,
    proof_digest TEXT NOT NULL,
    receipt_request_id TEXT NOT NULL DEFAULT '',
    receipt_kind TEXT NOT NULL DEFAULT '',
    receipt_idempotency_key TEXT NOT NULL DEFAULT '',
    consumed_at INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS deviceauth_challenge_client_nonce ON deviceauth_challenges(auth_public_key,client_nonce);
CREATE TABLE IF NOT EXISTS deviceauth_requests (
    principal_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    operation_id TEXT REFERENCES deviceauth_operations(operation_id),
    PRIMARY KEY(principal_id,request_id)
);
CREATE TABLE IF NOT EXISTS deviceauth_credential_receipts (
    kind TEXT NOT NULL CHECK(kind IN ('activate','credential.rotate')),
    request_id TEXT NOT NULL,
    public_key TEXT NOT NULL,
    principal_id TEXT NOT NULL,
    credential_id TEXT NOT NULL REFERENCES deviceauth_credentials(credential_id),
    PRIMARY KEY(kind,request_id,public_key)
);
-- Immutable negative receipts reserve the original principal/request ID. The
-- new key/kind/idempotency are retained for exact repeated-resolution binding.
CREATE TABLE IF NOT EXISTS deviceauth_cancellations (
    principal_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('activate','credential.rotate','command.apply','command.disable','command.revoke')),
    public_key TEXT NOT NULL,
    device_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    committed_at INTEGER NOT NULL,
    PRIMARY KEY(principal_id,request_id)
);
CREATE TABLE IF NOT EXISTS deviceauth_audit (
    id INTEGER PRIMARY KEY,
    device_id TEXT NOT NULL,
    credential_id TEXT NOT NULL,
    purpose TEXT NOT NULL,
    request_id TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
