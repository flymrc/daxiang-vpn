package deviceauth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrRestorePlan = errors.New("deviceauth: offline restore plan input refused")

// RestoreCheckpoint is an explicit caller assertion, not proof that no later
// revocation exists. A newer incident fact requires a newly verified checkpoint.
type RestoreCheckpoint struct {
	SchemaVersion                  int       `json:"schema_version"`
	AuthoritySchemaVersion         int       `json:"authority_schema_version"`
	AuthoritySHA256                string    `json:"authority_sha256"`
	Epoch                          string    `json:"epoch"`
	ManagedBy                      string    `json:"managed_by"`
	PolicySHA256                   string    `json:"policy_sha256"`
	VerifiedAt                     time.Time `json:"verified_at"`
	LatestRevocationFactsConfirmed bool      `json:"latest_revocation_facts_confirmed"`
}

type RestorePlanRequest struct {
	BackupPath, LatestAuthorityPath, LatestCheckpointPath string
	AsOf                                                  time.Time
	MaxCheckpointAge                                      time.Duration
	ObservedPeers                                         []Peer
}

type RestoreSource struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type RestoreTableFacts struct {
	Table            string `json:"table"`
	Rows             int    `json:"rows"`
	AddedSinceBackup int    `json:"added_since_backup"`
	RowDigest        string `json:"row_digest"`
}
type RestoreDeviceDecision struct {
	DeviceID          string `json:"device_id"`
	State             string `json:"state"`
	Generation        int64  `json:"generation"`
	AppliedGeneration int64  `json:"applied_generation"`
	DenyGrant         bool   `json:"deny_grant"`
	Reason            string `json:"reason"`
}
type RestorePeerDecision struct {
	PeerDigest string `json:"peer_digest"`
	Decision   string `json:"decision"`
}
type RestorePlan struct {
	SchemaVersion           int                     `json:"schema_version"`
	Kind                    string                  `json:"kind"`
	PlanID                  string                  `json:"plan_id"`
	ReadyToRestore          bool                    `json:"ready_to_restore"`
	AsOf                    time.Time               `json:"as_of"`
	MaxCheckpointAgeSeconds int64                   `json:"max_checkpoint_age_seconds"`
	CheckpointVerifiedAt    time.Time               `json:"checkpoint_verified_at"`
	CheckpointTrust         string                  `json:"checkpoint_trust"`
	Epoch                   string                  `json:"epoch"`
	ManagedBy               string                  `json:"managed_by"`
	Backup                  RestoreSource           `json:"backup"`
	LatestAuthority         RestoreSource           `json:"latest_authority"`
	Checkpoint              RestoreSource           `json:"checkpoint"`
	Facts                   []RestoreTableFacts     `json:"retained_latest_facts"`
	Devices                 []RestoreDeviceDecision `json:"devices"`
	Peers                   []RestorePeerDecision   `json:"observed_peer_decisions"`
	RequiredFollowup        []string                `json:"required_followup"`
}

type restoreTable struct{ name, columns, key, mutable string }

var restoreTables = []restoreTable{
	{"deviceauth_meta", "singleton schema_version epoch managed_by policy_json fence_path executor_fence", "singleton", "executor_fence"},
	{"deviceauth_devices", "device_id owner_id role state valid_until generation applied_generation current_key current_address", "device_id", "state generation applied_generation current_key current_address"},
	{"deviceauth_addresses", "address device_id", "address", ""},
	{"deviceauth_bindings", "public_key device_id address managed_by role created_generation revoked_generation removed_generation applied", "public_key", "revoked_generation removed_generation applied"},
	{"deviceauth_tombstones", "public_key generation reason committed_at verified_at", "public_key generation", "verified_at"},
	{"deviceauth_operations", "operation_id actor_id owner_id action idempotency_key request_digest device_id epoch generation committed_at deadline", "operation_id", ""},
	{"deviceauth_outbox", "operation_id state attempts fence last_error verified_at", "operation_id", "state attempts fence last_error verified_at"},
	{"deviceauth_intents", "operation_id public_key action epoch generation fence address state", "operation_id public_key action", "fence state"},
	{"deviceauth_activations", "activation_id verifier owner_id valid_until activation_until consumed_device consumed_at", "activation_id", "consumed_device consumed_at"},
	{"deviceauth_credentials", "credential_id device_id public_key valid_until revoked_at replaced_by", "credential_id", "revoked_at replaced_by"},
	{"deviceauth_challenges", "challenge_id nonce purpose method path body_digest device_id credential_id activation_id auth_public_key expires client_nonce proof_digest receipt_request_id receipt_kind receipt_idempotency_key consumed_at", "challenge_id", ""},
	{"deviceauth_requests", "principal_id request_id created_at operation_id", "principal_id request_id", ""},
	{"deviceauth_credential_receipts", "kind request_id public_key principal_id credential_id", "kind request_id public_key", ""},
	{"deviceauth_cancellations", "principal_id request_id kind public_key device_id idempotency_key committed_at", "principal_id request_id", ""},
	{"deviceauth_audit", "id device_id credential_id purpose request_id created_at", "id", ""},
}

type restoreRow map[string]any
type restoreSnapshot struct {
	source RestoreSource
	tables map[string]map[string]restoreRow
	input  *restoreInput
}

func restoreFailure(code string) error { return fmt.Errorf("%w: %s", ErrRestorePlan, code) }
func restoreDigest(b []byte) string    { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func restoreSHA(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func restoreJSON(v any) []byte                    { b, _ := json.Marshal(v); return b }
func restoreText(r restoreRow, key string) string { s, _ := r[key].(string); return s }
func restoreInt(r restoreRow, key string) int64   { i, _ := r[key].(int64); return i }
func restoreRowKey(r restoreRow, cols string) string {
	v := []any{}
	for _, c := range strings.Fields(cols) {
		v = append(v, r[c])
	}
	return string(restoreJSON(v))
}
func restoreContains(fields, key string) bool {
	for _, c := range strings.Fields(fields) {
		if c == key {
			return true
		}
	}
	return false
}

var restoreMaxTime = time.Date(2200, 12, 31, 23, 59, 59, 999999999, time.UTC)

func restoreCalendar(t time.Time) bool {
	return !t.IsZero() && t.UTC().Year() >= 1970 && !t.After(restoreMaxTime) && t.UnixNano() > 0
}
func restoreTextCap(column string) int {
	if column == "policy_json" {
		return 256 << 10
	}
	if restoreContains("nonce challenge_id client_nonce credential_id activation_id operation_id public_key auth_public_key verifier proof_digest body_digest request_digest receipt_kind kind action role state", column) {
		return 256
	}
	return 4096
}
func restoreIntegerColumn(column string) bool {
	return restoreContains("singleton schema_version executor_fence valid_until generation applied_generation created_generation revoked_generation removed_generation applied committed_at verified_at deadline attempts fence activation_until consumed_at revoked_at expires created_at id", column)
}

func restoreFile(path string, max int64) ([]byte, RestoreSource, error) {
	r, b, err := restoreOpenInput(path, max)
	if err != nil {
		return nil, RestoreSource{}, err
	}
	defer r.Close()
	return b, r.source, nil
}

// RestoreStrictJSON rejects duplicate, unknown and trailing JSON members. Errors
// never include input bytes, credentials, verifier hashes or raw request bodies.
func RestoreStrictJSON(data []byte, dst any) error {
	if dst == nil || reflect.TypeOf(dst).Kind() != reflect.Pointer || reflect.ValueOf(dst).IsNil() {
		return restoreFailure("invalid_json_destination")
	}
	if !utf8.Valid(data) {
		return restoreFailure("invalid_json")
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	members := 0
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 8 {
			return ErrInvalid
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					members++
					if members > 40000 {
						return ErrInvalid
					}
					k, e := d.Token()
					if e != nil {
						return e
					}
					key, ok := k.(string)
					if !ok || seen[key] {
						return ErrInvalid
					}
					seen[key] = true
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
				_, err = d.Token()
				return err
			case '[':
				for d.More() {
					members++
					if members > 40000 {
						return ErrInvalid
					}
					if e := walk(depth + 1); e != nil {
						return e
					}
				}
				_, err = d.Token()
				return err
			default:
				return ErrInvalid
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return restoreFailure("invalid_json")
	}
	if _, err := d.Token(); err != io.EOF {
		return restoreFailure("trailing_json")
	}
	var exactFields func(json.RawMessage, reflect.Type) error
	exactFields = func(raw json.RawMessage, t reflect.Type) error {
		if t.Kind() == reflect.Pointer {
			return exactFields(raw, t.Elem())
		}
		if t == reflect.TypeOf(time.Time{}) {
			return nil
		}
		if t.Kind() == reflect.Struct {
			var obj map[string]json.RawMessage
			if json.Unmarshal(raw, &obj) != nil || obj == nil {
				return ErrInvalid
			}
			fields := map[string]reflect.Type{}
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name == "" {
					name = field.Name
				}
				if name != "-" {
					fields[name] = field.Type
				}
			}
			for key, value := range obj {
				ft, ok := fields[key]
				if !ok {
					return ErrInvalid
				}
				if err := exactFields(value, ft); err != nil {
					return err
				}
			}
		}
		if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			var values []json.RawMessage
			if json.Unmarshal(raw, &values) != nil {
				return ErrInvalid
			}
			for _, value := range values {
				if err := exactFields(value, t.Elem()); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := exactFields(data, reflect.TypeOf(dst)); err != nil {
		return restoreFailure("noncanonical_json_fields")
	}
	d = json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return restoreFailure("invalid_json_contract")
	}
	return nil
}

func restoreReadSnapshot(ctx context.Context, path string) (result *restoreSnapshot, returned error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, restoreFailure("invalid_database_path")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, e := os.Lstat(abs + suffix); !os.IsNotExist(e) {
			return nil, restoreFailure("non_standalone_database")
		}
	}
	input, b, err := restoreOpenInput(abs, 256<<20)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returned != nil {
			input.Close()
		}
	}()
	source := input.source
	if len(b) < 100 || string(b[:16]) != "SQLite format 3\x00" {
		return nil, restoreFailure("invalid_sqlite_database")
	}
	copyPath, cleanup, err := restoreOwnedCopy(filepath.Dir(abs), b)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	copyInput, _, err := restoreOpenInput(copyPath, 256<<20)
	if err != nil {
		return nil, err
	}
	defer copyInput.Close()
	if copyInput.source != source {
		return nil, restoreFailure("private_copy_digest")
	}
	uriPath := filepath.ToSlash(copyPath)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("immutable", "1")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, restoreFailure("readonly_database_open")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	c, err := db.Conn(ctx)
	if err != nil {
		return nil, restoreFailure("readonly_database_connection")
	}
	defer c.Close()
	if _, err = c.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return nil, restoreFailure("readonly_database_mode")
	}
	if _, err = c.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, restoreFailure("readonly_database_transaction")
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	var integrity string
	if err = c.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nil, restoreFailure("database_integrity")
	}
	fk, err := c.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, restoreFailure("database_foreign_keys")
	}
	broken := fk.Next()
	e := fk.Err()
	fk.Close()
	if broken || e != nil {
		return nil, restoreFailure("database_foreign_keys")
	}
	s := &restoreSnapshot{source: source, tables: map[string]map[string]restoreRow{}, input: input}
	known := map[string]bool{}
	for _, table := range restoreTables {
		known[table.name] = true
	}
	objects, err := c.QueryContext(ctx, "SELECT name,type FROM sqlite_master WHERE (name LIKE 'deviceauth_%' OR tbl_name LIKE 'deviceauth_%') AND type IN ('table','view','trigger')")
	if err != nil {
		return nil, restoreFailure("authority_schema_inventory")
	}
	for objects.Next() {
		var name, kind string
		if objects.Scan(&name, &kind) != nil || kind != "table" || !known[name] {
			objects.Close()
			return nil, restoreFailure("unsupported_authority_object")
		}
	}
	e = objects.Err()
	objects.Close()
	if e != nil {
		return nil, restoreFailure("authority_schema_inventory")
	}
	total := 0
	for _, table := range restoreTables {
		var kind string
		if err = c.QueryRowContext(ctx, "SELECT type FROM sqlite_master WHERE name=?", table.name).Scan(&kind); err != nil || kind != "table" {
			return nil, restoreFailure("missing_authority_schema")
		}
		shape, err := c.QueryContext(ctx, "PRAGMA table_info("+table.name+")")
		if err != nil {
			return nil, restoreFailure("authority_schema_shape")
		}
		columns := strings.Fields(table.columns)
		position := 0
		for shape.Next() {
			var cid, notnull, pk int
			var name, typ string
			var def any
			if err = shape.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil || position >= len(columns) || columns[position] != name {
				shape.Close()
				return nil, restoreFailure("authority_schema_shape")
			}
			want := "TEXT"
			if restoreContains("singleton schema_version executor_fence valid_until generation applied_generation created_generation revoked_generation removed_generation applied committed_at verified_at deadline attempts fence activation_until consumed_at revoked_at expires created_at id", name) {
				want = "INTEGER"
			}
			wantPK := 0
			for i, k := range strings.Fields(table.key) {
				if k == name {
					wantPK = i + 1
				}
			}
			if typ != want || pk != wantPK {
				shape.Close()
				return nil, restoreFailure("authority_schema_shape")
			}
			position++
		}
		e = shape.Err()
		shape.Close()
		if e != nil || position != len(columns) {
			return nil, restoreFailure("authority_schema_shape")
		}
		conditions := []string{}
		for _, column := range columns {
			if !restoreIntegerColumn(column) {
				conditions = append(conditions, fmt.Sprintf("length(CAST(%s AS BLOB))>%d", column, restoreTextCap(column)))
			}
		}
		if len(conditions) > 0 {
			var oversized int
			if err = c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table.name+" WHERE "+strings.Join(conditions, " OR ")+")").Scan(&oversized); err != nil || oversized != 0 {
				return nil, restoreFailure("authority_text_resource_limit")
			}
		}
		rows, err := c.QueryContext(ctx, "SELECT "+strings.Join(strings.Fields(table.columns), ",")+" FROM "+table.name)
		if err != nil {
			return nil, restoreFailure("unsupported_authority_columns")
		}
		cols := strings.Fields(table.columns)
		records := map[string]restoreRow{}
		for rows.Next() {
			if total++; total > 1000000 {
				rows.Close()
				return nil, restoreFailure("authority_row_limit")
			}
			values := make([]any, len(cols))
			pointers := make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, restoreFailure("authority_row_decode")
			}
			r := restoreRow{}
			for i, k := range cols {
				_, integer := values[i].(int64)
				_, text := values[i].(string)
				wantInteger := restoreContains("singleton schema_version executor_fence valid_until generation applied_generation created_generation revoked_generation removed_generation applied committed_at verified_at deadline attempts fence activation_until consumed_at revoked_at expires created_at id", k)
				nullable := restoreContains("current_key current_address verified_at consumed_device consumed_at revoked_at replaced_by", k) || (table.name == "deviceauth_requests" && k == "operation_id")
				if values[i] == nil && !nullable || (values[i] != nil && ((wantInteger && !integer) || (!wantInteger && !text))) {
					rows.Close()
					return nil, restoreFailure("authority_row_type")
				}
				if value, ok := values[i].(string); ok && (!utf8.ValidString(value) || len(value) > restoreTextCap(k)) {
					rows.Close()
					return nil, restoreFailure("authority_text_encoding_or_size")
				}
				if value, ok := values[i].(int64); ok {
					if restoreContains("valid_until committed_at verified_at deadline activation_until consumed_at revoked_at created_at", k) && (value <= 0 || value > restoreMaxTime.UnixNano()) {
						rows.Close()
						return nil, restoreFailure("authority_nanosecond_calendar")
					}
					if k == "expires" && (value <= 0 || value > restoreMaxTime.Unix()) {
						rows.Close()
						return nil, restoreFailure("authority_second_calendar")
					}
				}
				r[k] = values[i]
			}
			key := restoreRowKey(r, table.key)
			if _, exists := records[key]; exists {
				rows.Close()
				return nil, restoreFailure("duplicate_authority_identity")
			}
			records[key] = r
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, restoreFailure("authority_rows")
		}
		s.tables[table.name] = records
	}
	if err = input.unchanged(); err != nil {
		return nil, restoreFailure("changed_database_input")
	}
	if err = copyInput.unchanged(); err != nil {
		return nil, restoreFailure("changed_private_copy")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, e := os.Lstat(abs + suffix); !os.IsNotExist(e) {
			return nil, restoreFailure("changed_database_sidecar")
		}
	}
	if err = restoreValidate(s); err != nil {
		return nil, err
	}
	return s, nil
}

func restoreMeta(s *restoreSnapshot) restoreRow {
	for _, r := range s.tables["deviceauth_meta"] {
		return r
	}
	return nil
}
func restoreFind(s *restoreSnapshot, table, keycols string, values ...any) restoreRow {
	return s.tables[table][string(restoreJSON(values))]
}

func restoreValidate(s *restoreSnapshot) error {
	m := restoreMeta(s)
	if len(s.tables["deviceauth_meta"]) != 1 || restoreInt(m, "singleton") != 1 || restoreInt(m, "schema_version") != 2 || !identifier(restoreText(m, "epoch")) || !identifier(restoreText(m, "managed_by")) || restoreInt(m, "executor_fence") < 0 {
		return restoreFailure("authority_meta")
	}
	var p Policy
	if err := RestoreStrictJSON([]byte(restoreText(m, "policy_json")), &p); err != nil {
		return restoreFailure("authority_policy")
	}
	if err := normalizePolicy(&p); err != nil || p.Epoch != restoreText(m, "epoch") || p.ManagedBy != restoreText(m, "managed_by") {
		return restoreFailure("authority_policy")
	}
	for _, r := range s.tables["deviceauth_devices"] {
		if !identifier(restoreText(r, "device_id")) || !identifier(restoreText(r, "owner_id")) || restoreText(r, "role") != "customer" || restoreInt(r, "valid_until") <= 0 || restoreInt(r, "generation") < 0 || restoreInt(r, "applied_generation") < 0 || restoreInt(r, "applied_generation") > restoreInt(r, "generation") {
			return restoreFailure("device_row")
		}
		state := restoreText(r, "state")
		if state != "active" && state != "disabled" && state != "expired" && state != "revoked" {
			return restoreFailure("device_state")
		}
		key, addr := restoreText(r, "current_key"), restoreText(r, "current_address")
		if (key == "") != (addr == "") || (state != "active" && key != "") {
			return restoreFailure("device_current_binding")
		}
		if key != "" {
			b := restoreFind(s, "deviceauth_bindings", "public_key", key)
			if b == nil || restoreText(b, "device_id") != restoreText(r, "device_id") || restoreText(b, "address") != addr || restoreInt(b, "revoked_generation") != 0 {
				return restoreFailure("device_current_binding")
			}
		}
	}
	for _, r := range s.tables["deviceauth_addresses"] {
		if restoreFind(s, "deviceauth_devices", "device_id", r["device_id"]) == nil {
			return restoreFailure("reservation_owner")
		}
	}
	current := map[string]bool{}
	for _, r := range s.tables["deviceauth_bindings"] {
		d := restoreFind(s, "deviceauth_devices", "device_id", r["device_id"])
		created, revoked, removed := restoreInt(r, "created_generation"), restoreInt(r, "revoked_generation"), restoreInt(r, "removed_generation")
		if d == nil || !validKey(restoreText(r, "public_key")) || restoreText(r, "role") != "customer" || restoreText(r, "managed_by") != p.ManagedBy || created <= 0 || created > restoreInt(d, "generation") || revoked < 0 || (revoked > 0 && (revoked <= created || revoked > restoreInt(d, "generation"))) || removed < 0 || removed > restoreInt(d, "generation") || (removed > 0 && revoked == 0) || (restoreInt(r, "applied") != 0 && restoreInt(r, "applied") != 1) {
			return restoreFailure("binding_ownership_generation")
		}
		if revoked == 0 {
			device := restoreText(r, "device_id")
			if current[device] || restoreText(d, "current_key") != restoreText(r, "public_key") {
				return restoreFailure("binding_current_identity")
			}
			current[device] = true
		}
		if a := restoreFind(s, "deviceauth_addresses", "address", r["address"]); a == nil || a["device_id"] != r["device_id"] {
			return restoreFailure("binding_address_ownership")
		}
		synthetic := &Store{opts: Options{Policy: p}}
		if _, err := synthetic.address(restoreText(r, "public_key"), restoreText(r, "address")); err != nil {
			return restoreFailure("binding_protected_address")
		}
		if revoked > 0 && restoreFind(s, "deviceauth_tombstones", "public_key generation", r["public_key"], revoked) == nil {
			return restoreFailure("missing_binding_tombstone")
		}
	}
	for _, r := range s.tables["deviceauth_tombstones"] {
		b := restoreFind(s, "deviceauth_bindings", "public_key", r["public_key"])
		if b == nil || restoreInt(r, "generation") != restoreInt(b, "revoked_generation") || restoreInt(r, "generation") <= restoreInt(b, "created_generation") || restoreInt(r, "committed_at") <= 0 {
			return restoreFailure("tombstone_row")
		}
	}
	for _, r := range s.tables["deviceauth_operations"] {
		d := restoreFind(s, "deviceauth_devices", "device_id", r["device_id"])
		action := restoreText(r, "action")
		if d == nil || !identifier(restoreText(r, "actor_id")) || !identifier(restoreText(r, "idempotency_key")) || !restoreSHA(restoreText(r, "request_digest")) || (action != "apply" && action != "disable" && action != "expire" && action != "revoke") || restoreInt(r, "committed_at") <= 0 || restoreInt(r, "deadline") < restoreInt(r, "committed_at") || r["owner_id"] != d["owner_id"] || r["epoch"] != m["epoch"] || restoreInt(r, "generation") <= 0 || restoreInt(r, "generation") > restoreInt(d, "generation") || restoreFind(s, "deviceauth_outbox", "operation_id", r["operation_id"]) == nil {
			return restoreFailure("operation_owner_epoch_generation")
		}
	}
	operationGenerations := map[string]map[int64]restoreRow{}
	for _, r := range s.tables["deviceauth_operations"] {
		id := restoreText(r, "device_id")
		if operationGenerations[id] == nil {
			operationGenerations[id] = map[int64]restoreRow{}
		}
		generation := restoreInt(r, "generation")
		if operationGenerations[id][generation] != nil {
			return restoreFailure("duplicate_operation_generation")
		}
		operationGenerations[id][generation] = r
	}
	for _, d := range s.tables["deviceauth_devices"] {
		ops := operationGenerations[restoreText(d, "device_id")]
		generation := restoreInt(d, "generation")
		if int64(len(ops)) != generation {
			return restoreFailure("missing_generation_history")
		}
		if generation > 0 {
			last := ops[generation]
			state := map[string]string{"apply": "active", "disable": "disabled", "expire": "expired", "revoke": "revoked"}[restoreText(last, "action")]
			if restoreText(d, "state") != state && restoreText(d, "state") != "revoked" {
				return restoreFailure("latest_operation_device_state")
			}
		}
		applied := restoreInt(d, "applied_generation")
		if applied > 0 {
			op := ops[applied]
			box := restoreFind(s, "deviceauth_outbox", "operation_id", op["operation_id"])
			if restoreText(box, "state") != "done" {
				return restoreFailure("unverified_applied_generation")
			}
		}
	}
	for _, r := range s.tables["deviceauth_outbox"] {
		state := restoreText(r, "state")
		if (state != "pending" && state != "degraded" && state != "done" && state != "superseded") || (state == "done" && r["verified_at"] == nil) || restoreFind(s, "deviceauth_operations", "operation_id", r["operation_id"]) == nil || restoreInt(r, "attempts") < 0 || restoreInt(r, "fence") < 0 || restoreInt(r, "fence") > restoreInt(m, "executor_fence") {
			return restoreFailure("outbox_row")
		}
	}
	for _, r := range s.tables["deviceauth_intents"] {
		op := restoreFind(s, "deviceauth_operations", "operation_id", r["operation_id"])
		b := restoreFind(s, "deviceauth_bindings", "public_key", r["public_key"])
		action, state := restoreText(r, "action"), restoreText(r, "state")
		if op == nil || b == nil || op["device_id"] != b["device_id"] || r["address"] != b["address"] || r["epoch"] != m["epoch"] || r["generation"] != op["generation"] || restoreInt(r, "fence") <= 0 || restoreInt(r, "fence") > restoreInt(m, "executor_fence") || (action != "apply" && action != "remove") || (state != "prepared" && state != "verified") {
			return restoreFailure("intent_owner_generation")
		}
	}
	credentialKeys := map[string]bool{}
	liveCredentials := map[string]bool{}
	for _, r := range s.tables["deviceauth_credentials"] {
		d := restoreFind(s, "deviceauth_devices", "device_id", r["device_id"])
		key, id := restoreText(r, "public_key"), restoreText(r, "device_id")
		_, keyErr := authKey(key)
		if d == nil || !identifier(restoreText(r, "credential_id")) || keyErr != nil || credentialKeys[key] || r["valid_until"] != d["valid_until"] || (r["revoked_at"] != nil && restoreInt(r, "revoked_at") <= 0) {
			return restoreFailure("credential_owner")
		}
		credentialKeys[key] = true
		if r["revoked_at"] == nil {
			if liveCredentials[id] {
				return restoreFailure("multiple_live_credentials")
			}
			liveCredentials[id] = true
		}
		if r["replaced_by"] != nil {
			next := restoreFind(s, "deviceauth_credentials", "credential_id", r["replaced_by"])
			if next == nil || next["device_id"] != r["device_id"] || r["revoked_at"] == nil || next["credential_id"] == r["credential_id"] {
				return restoreFailure("credential_rotation")
			}
		}
	}
	// Iterative coloring keeps corrupted replacement cycles bounded without a
	// recursive walk or repeatedly traversing every historical credential chain.
	colors := map[string]int{}
	for _, start := range s.tables["deviceauth_credentials"] {
		path := []string{}
		row := start
		for row != nil {
			id := restoreText(row, "credential_id")
			if colors[id] == 1 {
				return restoreFailure("credential_rotation_cycle")
			}
			if colors[id] == 2 {
				break
			}
			colors[id] = 1
			path = append(path, id)
			if row["replaced_by"] == nil {
				break
			}
			row = restoreFind(s, "deviceauth_credentials", "credential_id", row["replaced_by"])
		}
		for _, id := range path {
			colors[id] = 2
		}
	}
	for _, r := range s.tables["deviceauth_activations"] {
		if !identifier(restoreText(r, "activation_id")) || !identifier(restoreText(r, "owner_id")) || !restoreSHA(restoreText(r, "verifier")) || restoreInt(r, "activation_until") <= 0 || restoreInt(r, "activation_until") > restoreInt(r, "valid_until") || (r["consumed_device"] == nil) != (r["consumed_at"] == nil) {
			return restoreFailure("activation_consumption")
		}
		if r["consumed_device"] != nil {
			d := restoreFind(s, "deviceauth_devices", "device_id", r["consumed_device"])
			if d == nil || d["owner_id"] != r["owner_id"] || d["valid_until"] != r["valid_until"] {
				return restoreFailure("activation_owner")
			}
		}
	}
	for _, r := range s.tables["deviceauth_cancellations"] {
		kind := restoreText(r, "kind")
		principal := restoreText(r, "principal_id")
		device := restoreText(r, "device_id")
		credential := restoreFind(s, "deviceauth_credentials", "credential_id", principal)
		_, keyErr := authKey(restoreText(r, "public_key"))
		if !(kind == "activate" || kind == "credential.rotate" || kind == "command.apply" || kind == "command.disable" || kind == "command.revoke") || keyErr != nil || !identifier(restoreText(r, "request_id")) || restoreInt(r, "committed_at") <= 0 || (kind == "activate" && (!strings.HasPrefix(principal, "activation:") || restoreFind(s, "deviceauth_activations", "activation_id", strings.TrimPrefix(principal, "activation:")) == nil || device != "")) || (kind != "activate" && (credential == nil || restoreText(credential, "device_id") != device)) || restoreFind(s, "deviceauth_requests", "principal_id request_id", r["principal_id"], r["request_id"]) != nil {
			return restoreFailure("cancel_committed_request_conflict")
		}
	}
	for _, r := range s.tables["deviceauth_requests"] {
		principal := restoreText(r, "principal_id")
		if strings.HasPrefix(principal, "activation:") {
			if restoreFind(s, "deviceauth_activations", "activation_id", strings.TrimPrefix(principal, "activation:")) == nil {
				return restoreFailure("request_principal")
			}
		} else if restoreFind(s, "deviceauth_credentials", "credential_id", principal) == nil {
			return restoreFailure("request_principal")
		}
		if r["operation_id"] != nil {
			op := restoreFind(s, "deviceauth_operations", "operation_id", r["operation_id"])
			cr := restoreFind(s, "deviceauth_credentials", "credential_id", principal)
			if op == nil || cr == nil || op["actor_id"] != cr["device_id"] || op["device_id"] != cr["device_id"] {
				return restoreFailure("request_operation_actor")
			}
		}
	}
	credentialReceipts := map[string]bool{}
	for _, r := range s.tables["deviceauth_credential_receipts"] {
		cr := restoreFind(s, "deviceauth_credentials", "credential_id", r["credential_id"])
		req := restoreFind(s, "deviceauth_requests", "principal_id request_id", r["principal_id"], r["request_id"])
		if cr == nil || req == nil || cr["public_key"] != r["public_key"] {
			return restoreFailure("credential_receipt_identity")
		}
		id := restoreText(cr, "credential_id")
		if credentialReceipts[id] {
			return restoreFailure("duplicate_credential_creation_receipt")
		}
		credentialReceipts[id] = true
		switch restoreText(r, "kind") {
		case "activate":
			a := restoreFind(s, "deviceauth_activations", "activation_id", strings.TrimPrefix(restoreText(r, "principal_id"), "activation:"))
			if !strings.HasPrefix(restoreText(r, "principal_id"), "activation:") || a == nil || a["consumed_device"] != cr["device_id"] {
				return restoreFailure("activation_receipt_identity")
			}
		case "credential.rotate":
			old := restoreFind(s, "deviceauth_credentials", "credential_id", r["principal_id"])
			if old == nil || old["replaced_by"] != cr["credential_id"] || old["device_id"] != cr["device_id"] {
				return restoreFailure("rotation_receipt_identity")
			}
		default:
			return restoreFailure("credential_receipt_kind")
		}
	}
	if len(credentialReceipts) != len(s.tables["deviceauth_credentials"]) {
		return restoreFailure("missing_credential_creation_receipt")
	}
	return nil
}

func restoreCompare(backup, latest *restoreSnapshot) error {
	for _, t := range restoreTables {
		if t.name == "deviceauth_challenges" {
			continue
		} // expired nonces are transient; never import them.
		for key, old := range backup.tables[t.name] {
			now := latest.tables[t.name][key]
			if now == nil {
				return restoreFailure("latest_missing_retained_history")
			}
			for _, col := range strings.Fields(t.columns) {
				if !restoreContains(t.mutable, col) && old[col] != now[col] {
					return restoreFailure("immutable_authority_fact_changed")
				}
			}
			switch t.name {
			case "deviceauth_meta":
				if restoreInt(now, "executor_fence") < restoreInt(old, "executor_fence") {
					return restoreFailure("executor_watermark_regressed")
				}
			case "deviceauth_devices":
				if restoreInt(now, "generation") < restoreInt(old, "generation") || restoreInt(now, "applied_generation") < restoreInt(old, "applied_generation") || (restoreText(old, "state") != "active" && restoreText(now, "state") == "active") || (restoreText(old, "state") == "revoked" && restoreText(now, "state") != "revoked") {
					return restoreFailure("device_authority_regressed")
				}
				if restoreInt(now, "generation") == restoreInt(old, "generation") && (now["state"] != old["state"] || now["current_key"] != old["current_key"] || now["current_address"] != old["current_address"]) {
					return restoreFailure("device_generation_conflict")
				}
			case "deviceauth_bindings":
				for _, col := range []string{"revoked_generation", "removed_generation", "applied"} {
					if restoreInt(now, col) < restoreInt(old, col) {
						return restoreFailure("binding_negative_fact_regressed")
					}
				}
				if restoreInt(old, "revoked_generation") > 0 && now["revoked_generation"] != old["revoked_generation"] {
					return restoreFailure("binding_revocation_changed")
				}
			case "deviceauth_credentials":
				for _, col := range []string{"revoked_at", "replaced_by"} {
					if old[col] != nil && now[col] != old[col] {
						return restoreFailure("credential_negative_fact_regressed")
					}
				}
			case "deviceauth_activations":
				for _, col := range []string{"consumed_device", "consumed_at"} {
					if old[col] != nil && now[col] != old[col] {
						return restoreFailure("activation_consumption_regressed")
					}
				}
			case "deviceauth_tombstones":
				if old["verified_at"] != nil && now["verified_at"] != old["verified_at"] {
					return restoreFailure("tombstone_verification_regressed")
				}
			case "deviceauth_outbox":
				if restoreInt(now, "attempts") < restoreInt(old, "attempts") || restoreInt(now, "fence") < restoreInt(old, "fence") || ((restoreText(old, "state") == "done" || restoreText(old, "state") == "superseded") && old["state"] != now["state"]) {
					return restoreFailure("outbox_history_regressed")
				}
			case "deviceauth_intents":
				if restoreInt(now, "fence") < restoreInt(old, "fence") {
					return restoreFailure("intent_fence_regressed")
				}
			}
		}
	}
	return nil
}

// PlanOfflineRestore only compares explicit standalone snapshots. It neither
// opens a Store nor acquires executor privileges, writes SQL, or calls WireGuard.
func PlanOfflineRestore(ctx context.Context, q RestorePlanRequest) (RestorePlan, error) {
	var zero RestorePlan
	if !restoreCalendar(q.AsOf) || q.MaxCheckpointAge < time.Second || q.MaxCheckpointAge > 24*time.Hour || q.MaxCheckpointAge%time.Second != 0 {
		return zero, restoreFailure("explicit_asof_and_checkpoint_age_required")
	}
	checkpointInput, b, err := restoreOpenInput(q.LatestCheckpointPath, 16384)
	if err != nil {
		return zero, err
	}
	defer checkpointInput.Close()
	checkpointSource := checkpointInput.source
	var checkpoint RestoreCheckpoint
	if err = RestoreStrictJSON(b, &checkpoint); err != nil {
		return zero, err
	}
	if checkpoint.SchemaVersion != 1 || checkpoint.AuthoritySchemaVersion != 2 || !restoreSHA(checkpoint.AuthoritySHA256) || !restoreSHA(checkpoint.PolicySHA256) || !checkpoint.LatestRevocationFactsConfirmed || !restoreCalendar(checkpoint.VerifiedAt) || checkpoint.VerifiedAt.After(q.AsOf) || q.AsOf.Sub(checkpoint.VerifiedAt) > q.MaxCheckpointAge {
		return zero, restoreFailure("latest_verified_checkpoint_required")
	}
	backup, err := restoreReadSnapshot(ctx, q.BackupPath)
	if err != nil {
		return zero, err
	}
	defer backup.input.Close()
	latest, err := restoreReadSnapshot(ctx, q.LatestAuthorityPath)
	if err != nil {
		return zero, err
	}
	defer latest.input.Close()
	m := restoreMeta(latest)
	if latest.source.SHA256 != checkpoint.AuthoritySHA256 || restoreText(m, "epoch") != checkpoint.Epoch || restoreText(m, "managed_by") != checkpoint.ManagedBy || restoreDigest([]byte(restoreText(m, "policy_json"))) != checkpoint.PolicySHA256 {
		return zero, restoreFailure("checkpoint_authority_mismatch")
	}
	for _, t := range restoreTables {
		for _, r := range latest.tables[t.name] {
			for _, field := range []string{"created_at", "committed_at", "consumed_at", "revoked_at", "verified_at"} {
				if value, ok := r[field].(int64); ok && value > checkpoint.VerifiedAt.UnixNano() {
					return zero, restoreFailure("checkpoint_predates_authority_fact")
				}
			}
		}
	}
	if err = restoreCompare(backup, latest); err != nil {
		return zero, err
	}
	plan := RestorePlan{SchemaVersion: 1, Kind: "offline_authority_comparison", ReadyToRestore: false, AsOf: q.AsOf.UTC(), MaxCheckpointAgeSeconds: int64(q.MaxCheckpointAge / time.Second), CheckpointVerifiedAt: checkpoint.VerifiedAt.UTC(), CheckpointTrust: "caller_asserted_not_self_proving", Epoch: checkpoint.Epoch, ManagedBy: checkpoint.ManagedBy, Backup: backup.source, LatestAuthority: latest.source, Checkpoint: checkpointSource, Facts: []RestoreTableFacts{}, Devices: []RestoreDeviceDecision{}, Peers: []RestorePeerDecision{}, RequiredFollowup: []string{"verify_latest_revocation_facts_after_incident", "preserve_all_latest_history_and_negative_receipts", "invalidate_all_snapshot_challenges", "reconcile_expired_and_pending_obligations_against_owned_runtime", "establish_recovery_epoch_and_exclusive_authority_before_any_restore", "obtain_separate_review_for_actual_restore"}}
	for _, t := range restoreTables {
		if t.name == "deviceauth_challenges" {
			continue
		}
		keys := []string{}
		for key := range latest.tables[t.name] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		rows := []restoreRow{}
		for _, key := range keys {
			rows = append(rows, latest.tables[t.name][key])
		}
		plan.Facts = append(plan.Facts, RestoreTableFacts{Table: t.name, Rows: len(keys), AddedSinceBackup: len(keys) - len(backup.tables[t.name]), RowDigest: restoreDigest(restoreJSON(rows))})
	}
	for _, d := range latest.tables["deviceauth_devices"] {
		state := restoreText(d, "state")
		reason := "latest_generation_only"
		deny := state != "active"
		if deny {
			reason = "latest_negative_device_state"
		} else if q.AsOf.UnixNano() >= restoreInt(d, "valid_until") {
			deny = true
			reason = "expired_at_planning_time"
		}
		plan.Devices = append(plan.Devices, RestoreDeviceDecision{DeviceID: restoreText(d, "device_id"), State: state, Generation: restoreInt(d, "generation"), AppliedGeneration: restoreInt(d, "applied_generation"), DenyGrant: deny, Reason: reason})
	}
	sort.Slice(plan.Devices, func(i, j int) bool { return plan.Devices[i].DeviceID < plan.Devices[j].DeviceID })
	peers, err := runtimeMap(q.ObservedPeers)
	if err != nil {
		return zero, restoreFailure("invalid_observed_peer_inventory")
	}
	for key, ips := range peers {
		decision := "preserve_unknown_peer"
		if binding := restoreFind(latest, "deviceauth_bindings", "public_key", key); binding != nil {
			decision = "manual_exact_ownership_reverification_required"
			if restoreInt(binding, "revoked_generation") > 0 {
				decision = "retained_removal_obligation_no_automatic_action"
			}
		}
		plan.Peers = append(plan.Peers, RestorePeerDecision{PeerDigest: restoreDigest(restoreJSON([]any{key, ips})), Decision: decision})
	}
	sort.Slice(plan.Peers, func(i, j int) bool { return plan.Peers[i].PeerDigest < plan.Peers[j].PeerDigest })
	for _, input := range []*restoreInput{backup.input, latest.input, checkpointInput} {
		if err = input.unchanged(); err != nil {
			return zero, restoreFailure("changed_plan_input")
		}
	}
	plan.PlanID = restoreDigest(restoreJSON(plan))
	return plan, nil
}
