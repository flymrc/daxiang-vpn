package deviceauth

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func restoreTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	restoreFixturePrivate(t, dir)
	return dir
}
func restoreTestSnapshot(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	path := filepath.Join(restoreTestDir(t), name+".sqlite")
	if _, err := db.Exec("VACUUM INTO '" + strings.ReplaceAll(path, "'", "''") + "'"); err != nil {
		t.Fatal(err)
	}
	restoreFixturePrivate(t, path)
	return path
}
func restoreTestRequest(t *testing.T, backup, latest string, at time.Time) RestorePlanRequest {
	t.Helper()
	db, err := sql.Open("sqlite", latest)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var epoch, managed, policy string
	if err = db.QueryRow("SELECT epoch,managed_by,policy_json FROM deviceauth_meta WHERE singleton=1").Scan(&epoch, &managed, &policy); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	} // no writable SQLite handle may survive snapshot authentication.
	_, source, err := restoreFile(latest, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := RestoreCheckpoint{SchemaVersion: 1, AuthoritySchemaVersion: 2, AuthoritySHA256: source.SHA256, Epoch: epoch, ManagedBy: managed, PolicySHA256: restoreDigest([]byte(policy)), VerifiedAt: at, LatestRevocationFactsConfirmed: true}
	path := filepath.Join(restoreTestDir(t), "checkpoint.json")
	if err = os.WriteFile(path, restoreJSON(checkpoint), 0600); err != nil {
		t.Fatal(err)
	}
	restoreFixturePrivate(t, path)
	return RestorePlanRequest{BackupPath: backup, LatestAuthorityPath: latest, LatestCheckpointPath: path, AsOf: at, MaxCheckpointAge: 5 * time.Minute}
}
func restoreTestMutate(t *testing.T, path, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("PRAGMA foreign_keys=OFF; PRAGMA ignore_check_constraints=ON; " + statement); err != nil {
		t.Fatal(err)
	}
}

func TestRestorePlanPreservesPostBackupNegativeFactsWithoutSecrets(t *testing.T) {
	s, db, _ := newTestStore(t)
	cr, priv := activated(t, s)
	initial := command(cr.DeviceID, "apply", 0, 101, "10.66.0.2/32", "initial")
	initial.Reason = "hidden-reason-body"
	initialOperation := submit(t, s, initial)
	process(t, s, initialOperation, newFake())
	for i, id := range []string{"disable-device", "expire-device", "clock-expired-device"} {
		enroll(t, s, id)
		submit(t, s, command(id, "apply", 0, 102+i, []string{"10.66.0.3/32", "10.66.0.4/32", "10.66.0.5/32"}[i], "initial-"+id))
	}
	a, err := s.IssueActivation(context.Background(), "owner", testNow.Add(3*time.Hour), testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	backup := restoreTestSnapshot(t, db, "backup")
	newKey, newPriv := authPair(t)
	rawBody := []byte("hidden-exact-request-body")
	r := request(t, s, cr, priv, "credential.rotate", "POST", "/api/v2/credentials/rotate", rawBody, "post-backup-rotation")
	newCR, err := s.RotateCredential(context.Background(), r, newKey, base64.StdEncoding.EncodeToString(ed25519.Sign(newPriv, SigningBytes(r))))
	if err != nil {
		t.Fatal(err)
	}
	revoke := request(t, s, newCR, newPriv, "command.revoke", "POST", "/api/v2/commands", rawBody, "post-backup-self-revoke")
	if _, err = s.SubmitSigned(context.Background(), revoke, Command{DeviceID: cr.DeviceID, Action: "revoke", ExpectedGeneration: 1, IdempotencyKey: "self-revoke", Reason: "hidden-reason-body"}); err != nil {
		t.Fatal(err)
	}
	submit(t, s, command("disable-device", "disable", 1, 0, "", "post-backup-disable"))
	cancelKey, cancelPriv := authPair(t)
	got := resolve(t, s, Credential{}, ResolveRequest{Kind: "activate", RequestID: "unknown-activation-request", PublicKey: cancelKey, ActivationCredential: a.Credential}, cancelPriv, nil)
	if got.State != "cancelled" {
		t.Fatal("fixture cancellation failed")
	}
	s.opts.Now = func() time.Time { return testNow.Add(2 * time.Hour) }
	submit(t, s, command("expire-device", "expire", 1, 0, "", "post-backup-expire"))
	latest := restoreTestSnapshot(t, db, "latest")
	q := restoreTestRequest(t, backup, latest, s.opts.Now())
	unknownKey := testKey(80000)
	q.ObservedPeers = []Peer{{PublicKey: unknownKey, AllowedIPs: []string{"10.77.0.10/32"}}, {PublicKey: testKey(90000), AllowedIPs: []string{"10.66.0.100/32"}}}
	before := map[string]string{}
	for _, path := range []string{backup, latest, q.LatestCheckpointPath} {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		before[path] = restoreDigest(b)
	}
	plan, err := PlanOfflineRestore(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReadyToRestore || plan.Kind != "offline_authority_comparison" || !restoreSHA(plan.PlanID) || plan.CheckpointTrust != "caller_asserted_not_self_proving" {
		t.Fatal("planner falsely claimed restore readiness")
	}
	facts := map[string]RestoreTableFacts{}
	for _, f := range plan.Facts {
		facts[f.Table] = f
		if !restoreSHA(f.RowDigest) {
			t.Fatal("retained facts lack digest")
		}
	}
	if facts["deviceauth_credentials"].Rows != 2 || facts["deviceauth_credentials"].AddedSinceBackup != 1 || facts["deviceauth_tombstones"].Rows != 3 || facts["deviceauth_cancellations"].Rows != 1 || facts["deviceauth_credential_receipts"].Rows != 2 || facts["deviceauth_requests"].AddedSinceBackup != 2 {
		t.Fatalf("post-backup negative/history facts missing: %+v", facts)
	}
	decisions := map[string]RestoreDeviceDecision{}
	for _, d := range plan.Devices {
		decisions[d.DeviceID] = d
		if !d.DenyGrant {
			t.Fatal("revoked/disabled/expired snapshot granted a device")
		}
	}
	if decisions[cr.DeviceID].State != "revoked" || decisions["disable-device"].State != "disabled" || decisions["expire-device"].State != "expired" || decisions["clock-expired-device"].Reason != "expired_at_planning_time" {
		t.Fatal("latest device state was not selected")
	}
	for _, peer := range plan.Peers {
		if peer.Decision != "preserve_unknown_peer" || !restoreSHA(peer.PeerDigest) {
			t.Fatal("unknown/protected runtime peer adopted")
		}
	}
	encoded := string(restoreJSON(plan))
	for _, secret := range []string{cr.PublicKey, newKey, cancelKey, a.Credential, string(rawBody), "hidden-reason-body", unknownKey, testKey(90000), backup, latest} {
		if strings.Contains(encoded, secret) {
			t.Fatal("plan exposed confidential input")
		}
	}
	second, err := PlanOfflineRestore(context.Background(), q)
	if err != nil || !reflect.DeepEqual(plan, second) {
		t.Fatal("repeated plan is not deterministic")
	}
	for path, digest := range before {
		b, e := os.ReadFile(path)
		if e != nil || restoreDigest(b) != digest {
			t.Fatal("planner changed an immutable input")
		}
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if _, e = os.Stat(path + suffix); !os.IsNotExist(e) {
				t.Fatal("planner created a database sidecar")
			}
		}
	}
	// Caller-selected older authority cannot erase the newer snapshot's facts.
	reverse := restoreTestRequest(t, latest, backup, s.opts.Now())
	if _, err = PlanOfflineRestore(context.Background(), reverse); !errors.Is(err, ErrRestorePlan) {
		t.Fatal("backup newer than latest authority was accepted")
	}
}

func TestRestorePlanRejectsMissingOrStaleCheckpoint(t *testing.T) {
	s, db, _ := newTestStore(t)
	enroll(t, s, "device")
	snapshot := restoreTestSnapshot(t, db, "authority")
	base := restoreTestRequest(t, snapshot, snapshot, testNow)
	cases := []struct {
		name   string
		mutate func(*RestoreCheckpoint, *RestorePlanRequest)
	}{
		{"unconfirmed", func(c *RestoreCheckpoint, q *RestorePlanRequest) { c.LatestRevocationFactsConfirmed = false }},
		{"wrong-digest", func(c *RestoreCheckpoint, q *RestorePlanRequest) { c.AuthoritySHA256 = strings.Repeat("0", 64) }},
		{"wrong-epoch", func(c *RestoreCheckpoint, q *RestorePlanRequest) { c.Epoch = "foreign-epoch" }},
		{"wrong-policy", func(c *RestoreCheckpoint, q *RestorePlanRequest) { c.PolicySHA256 = strings.Repeat("0", 64) }},
		{"future-checkpoint", func(c *RestoreCheckpoint, q *RestorePlanRequest) { c.VerifiedAt = q.AsOf.Add(time.Second) }},
		{"stale", func(c *RestoreCheckpoint, q *RestorePlanRequest) {
			q.AsOf = q.AsOf.Add(5*time.Minute + time.Nanosecond)
		}},
		{"missing-budget", func(c *RestoreCheckpoint, q *RestorePlanRequest) { q.MaxCheckpointAge = 0 }},
		{"excess-budget", func(c *RestoreCheckpoint, q *RestorePlanRequest) { q.MaxCheckpointAge = 25 * time.Hour }},
		{"fractional-budget", func(c *RestoreCheckpoint, q *RestorePlanRequest) { q.MaxCheckpointAge = 1500 * time.Millisecond }},
		{"missing-asof", func(c *RestoreCheckpoint, q *RestorePlanRequest) { q.AsOf = time.Time{} }},
		{"overflow-asof", func(c *RestoreCheckpoint, q *RestorePlanRequest) {
			q.AsOf = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		}},
		{"overflow-checkpoint", func(c *RestoreCheckpoint, q *RestorePlanRequest) {
			c.VerifiedAt = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		}},
		{"pre-epoch-asof", func(c *RestoreCheckpoint, q *RestorePlanRequest) {
			q.AsOf = time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC)
		}},
		{"post-supported-calendar", func(c *RestoreCheckpoint, q *RestorePlanRequest) { q.AsOf = restoreMaxTime.Add(time.Nanosecond) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			b, err := os.ReadFile(base.LatestCheckpointPath)
			if err != nil {
				t.Fatal(err)
			}
			var c RestoreCheckpoint
			if err = json.Unmarshal(b, &c); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&c, &q)
			q.LatestCheckpointPath = filepath.Join(restoreTestDir(t), "checkpoint.json")
			if err = os.WriteFile(q.LatestCheckpointPath, restoreJSON(c), 0600); err != nil {
				t.Fatal(err)
			}
			restoreFixturePrivate(t, q.LatestCheckpointPath)
			if plan, err := PlanOfflineRestore(context.Background(), q); !errors.Is(err, ErrRestorePlan) || plan.ReadyToRestore || plan.PlanID != "" {
				t.Fatal("invalid checkpoint produced a plan")
			}
		})
	}
	for _, data := range []string{`{"schema_version":1,"schema_version":1}`, `{"Schema_Version":1}`, `{"schema_version":1} {}`, `{"schema_version":true}`, `null`} {
		t.Run("strict-json", func(t *testing.T) {
			q := base
			q.LatestCheckpointPath = filepath.Join(restoreTestDir(t), "checkpoint.json")
			os.WriteFile(q.LatestCheckpointPath, []byte(data), 0600)
			restoreFixturePrivate(t, q.LatestCheckpointPath)
			if _, err := PlanOfflineRestore(context.Background(), q); !errors.Is(err, ErrRestorePlan) {
				t.Fatal("noncanonical checkpoint accepted")
			}
		})
	}
	base.LatestCheckpointPath = filepath.Join(restoreTestDir(t), "missing.json")
	if _, err := PlanOfflineRestore(context.Background(), base); !errors.Is(err, ErrRestorePlan) {
		t.Fatal("missing checkpoint accepted")
	}
}

func TestRestorePlanRejectsSchemaOwnershipAndHistoryDrift(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"missing-table", "DROP TABLE deviceauth_cancellations"},
		{"wrong-schema", "UPDATE deviceauth_meta SET schema_version=1"},
		{"wrong-epoch", "UPDATE deviceauth_meta SET epoch='foreign'"},
		{"wrong-owner", "UPDATE deviceauth_devices SET owner_id='different-owner'"},
		{"bad-generation", "UPDATE deviceauth_bindings SET created_generation=99"},
		{"foreign-key", "UPDATE deviceauth_bindings SET device_id='missing-owner'"},
		{"missing-operation", "DELETE FROM deviceauth_outbox; DELETE FROM deviceauth_operations"},
		{"row-type", "UPDATE deviceauth_devices SET generation='not-an-integer'"},
		{"unmodelled-trigger", "CREATE TRIGGER audit_shadow AFTER UPDATE ON deviceauth_devices BEGIN SELECT 1; END"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			enroll(t, s, "device")
			submit(t, s, command("device", "apply", 0, 321, "10.66.0.2/32", "apply"))
			backup := restoreTestSnapshot(t, db, "backup")
			latest := restoreTestSnapshot(t, db, "latest")
			restoreTestMutate(t, latest, tc.sql)
			q := restoreTestRequest(t, backup, latest, testNow)
			if _, err := PlanOfflineRestore(context.Background(), q); !errors.Is(err, ErrRestorePlan) {
				t.Fatal("inconsistent or missing authority fact accepted")
			}
		})
	}
}

func TestRestorePlanRejectsMutableSidecarsAndCancellation(t *testing.T) {
	s, db, _ := newTestStore(t)
	enroll(t, s, "device")
	snapshot := restoreTestSnapshot(t, db, "authority")
	q := restoreTestRequest(t, snapshot, snapshot, testNow)
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			path := snapshot + suffix
			if err := os.WriteFile(path, []byte("uncheckpointed facts"), 0600); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(path)
			if _, err := PlanOfflineRestore(context.Background(), q); !errors.Is(err, ErrRestorePlan) {
				t.Fatal("mutable SQLite sidecar was ignored")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PlanOfflineRestore(ctx, q); !errors.Is(err, ErrRestorePlan) {
		t.Fatal("cancelled plan returned success")
	}
}

type restoreChangingContext struct {
	context.Context
	once   sync.Once
	change func()
}

func (c *restoreChangingContext) Done() <-chan struct{} { c.once.Do(c.change); return c.Context.Done() }

func TestRestorePlanDetectsInputChangeAfterCheckpointRead(t *testing.T) {
	s, db, _ := newTestStore(t)
	enroll(t, s, "device")
	snapshot := restoreTestSnapshot(t, db, "authority")
	q := restoreTestRequest(t, snapshot, snapshot, testNow)
	changed, blocked := false, false
	ctx := &restoreChangingContext{Context: context.Background(), change: func() {
		b, err := os.ReadFile(q.LatestCheckpointPath)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(q.LatestCheckpointPath, append(b, ' '), 0600); err != nil {
			blocked = true
			return
		}
		changed = true
	}}
	plan, err := PlanOfflineRestore(ctx, q)
	if runtime.GOOS == "windows" {
		if !blocked || changed || err != nil || plan.PlanID == "" {
			t.Fatal("Windows did not pin source against a concurrent writer")
		}
		return
	}
	if !changed || blocked || !errors.Is(err, ErrRestorePlan) || !strings.Contains(err.Error(), "changed_plan_input") || plan.PlanID != "" {
		t.Fatal("mid-plan checkpoint change was not detected")
	}
}

func TestRestorePlanRetainedReceiptAndNegativeHistoryCannotRegress(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"credential-unrevoke", "UPDATE deviceauth_credentials SET revoked_at=NULL,replaced_by=NULL WHERE replaced_by IS NOT NULL"},
		{"delete-request", "DELETE FROM deviceauth_requests"},
		{"delete-receipt", "DELETE FROM deviceauth_credential_receipts"},
		{"delete-audit", "DELETE FROM deviceauth_audit"},
		{"delete-cancel", "DELETE FROM deviceauth_cancellations"},
		{"ownership-transfer", "UPDATE deviceauth_addresses SET device_id='other-device'"},
		{"unknown-authority-schema", "CREATE TABLE deviceauth_future_negative_facts(id INTEGER PRIMARY KEY)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			cr, priv := activated(t, s)
			enroll(t, s, "other-device")
			submit(t, s, command(cr.DeviceID, "apply", 0, 456, "10.66.0.2/32", "apply"))
			newKey, newPriv := authPair(t)
			r := request(t, s, cr, priv, "credential.rotate", "POST", "/api/v2/credentials/rotate", []byte("secret-request-body"), "rotate")
			if _, err := s.RotateCredential(context.Background(), r, newKey, base64.StdEncoding.EncodeToString(ed25519.Sign(newPriv, SigningBytes(r)))); err != nil {
				t.Fatal(err)
			}
			a, err := s.IssueActivation(context.Background(), "owner", testNow.Add(time.Hour), testNow.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			cancelKey, cancelPriv := authPair(t)
			resolve(t, s, Credential{}, ResolveRequest{Kind: "activate", RequestID: "cancel-original", PublicKey: cancelKey, ActivationCredential: a.Credential}, cancelPriv, nil)
			backup := restoreTestSnapshot(t, db, "backup")
			latest := restoreTestSnapshot(t, db, "latest")
			restoreTestMutate(t, latest, tc.sql)
			q := restoreTestRequest(t, backup, latest, testNow)
			if plan, err := PlanOfflineRestore(context.Background(), q); !errors.Is(err, ErrRestorePlan) || plan.PlanID != "" {
				t.Fatal("retained receipt/negative history regressed")
			}
		})
	}
}

func TestRestorePlanCheckpointCannotPredateCommittedFacts(t *testing.T) {
	s, db, _ := newTestStore(t)
	enroll(t, s, "device")
	backup := restoreTestSnapshot(t, db, "backup")
	submit(t, s, command("device", "apply", 0, 999, "10.66.0.2/32", "apply"))
	latest := restoreTestSnapshot(t, db, "latest")
	q := restoreTestRequest(t, backup, latest, testNow.Add(-time.Nanosecond))
	q.AsOf = testNow
	if _, err := PlanOfflineRestore(context.Background(), q); !errors.Is(err, ErrRestorePlan) || !strings.Contains(err.Error(), "checkpoint_predates_authority_fact") {
		t.Fatal("checkpoint older than committed facts accepted")
	}
}

func TestRestorePlanRejectsLossyOrOversizedImmutableText(t *testing.T) {
	for _, tc := range []struct{ name, backupSQL, latestSQL, code string }{
		{"invalid-utf8-backup", "UPDATE deviceauth_audit SET purpose=CAST(X'80' AS TEXT)", "", "authority_text_encoding_or_size"},
		{"invalid-utf8-collision", "UPDATE deviceauth_audit SET purpose=CAST(X'80' AS TEXT)", "UPDATE deviceauth_audit SET purpose=CAST(X'81' AS TEXT)", "authority_text_encoding_or_size"},
		{"invalid-utf8-latest", "", "UPDATE deviceauth_audit SET purpose=CAST(X'81' AS TEXT)", "authority_text_encoding_or_size"},
		{"valid-utf8-change", "UPDATE deviceauth_audit SET purpose='验证甲'", "UPDATE deviceauth_audit SET purpose='验证乙'", "immutable_authority_fact_changed"},
		{"oversized-single-cell", "", "UPDATE deviceauth_audit SET purpose=CAST(zeroblob(2097152) AS TEXT)", "authority_text_resource_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			activated(t, s) // real SQLite audit row, not a fabricated in-memory map.
			backup := restoreTestSnapshot(t, db, "backup")
			latest := restoreTestSnapshot(t, db, "latest")
			if tc.backupSQL != "" {
				restoreTestMutate(t, backup, tc.backupSQL)
			}
			if tc.latestSQL != "" {
				restoreTestMutate(t, latest, tc.latestSQL)
			}
			q := restoreTestRequest(t, backup, latest, testNow)
			plan, err := PlanOfflineRestore(context.Background(), q)
			if !errors.Is(err, ErrRestorePlan) || !strings.Contains(err.Error(), tc.code) || plan.PlanID != "" {
				t.Fatalf("immutable text input was not refused with the fixed code: %v", err)
			}
			if strings.Contains(err.Error(), "验证") || strings.Contains(err.Error(), latest) || strings.Contains(err.Error(), backup) {
				t.Fatal("failure exposed input text or path")
			}
		})
	}
}

func TestRestorePlanRejectsHardlinkedSources(t *testing.T) {
	for _, which := range []string{"backup", "latest", "checkpoint"} {
		t.Run(which, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			enroll(t, s, "device")
			backup := restoreTestSnapshot(t, db, "backup")
			latest := restoreTestSnapshot(t, db, "latest")
			q := restoreTestRequest(t, backup, latest, testNow)
			path := map[string]string{"backup": backup, "latest": latest, "checkpoint": q.LatestCheckpointPath}[which]
			if err := os.Link(path, filepath.Join(restoreTestDir(t), "second-link")); err != nil {
				t.Fatal(err)
			}
			plan, err := PlanOfflineRestore(context.Background(), q)
			if !errors.Is(err, ErrRestorePlan) || plan.PlanID != "" {
				t.Fatal("hardlinked source produced a plan")
			}
		})
	}
}

func TestRestorePlanRejectsDatabaseCalendarOverflow(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"negative-validity", "UPDATE deviceauth_devices SET valid_until=-1"},
		{"outside-ns-range", "UPDATE deviceauth_devices SET valid_until=" + strconv.FormatInt(restoreMaxTime.UnixNano()+1, 10)},
		{"overflow-audit-created-at", "UPDATE deviceauth_audit SET created_at=9223372036854775807"},
		{"negative-activation-consumption", "UPDATE deviceauth_activations SET consumed_at=-1"},
		{"negative-credential-revocation", "UPDATE deviceauth_credentials SET revoked_at=-1"},
		{"overflow-challenge-seconds", "UPDATE deviceauth_challenges SET expires=9223372036854775807"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			activated(t, s)
			backup := restoreTestSnapshot(t, db, "backup")
			latest := restoreTestSnapshot(t, db, "latest")
			restoreTestMutate(t, latest, tc.sql)
			q := restoreTestRequest(t, backup, latest, testNow)
			plan, err := PlanOfflineRestore(context.Background(), q)
			if !errors.Is(err, ErrRestorePlan) || !strings.Contains(err.Error(), "_calendar") || plan.PlanID != "" {
				t.Fatalf("invalid SQL calendar produced a plan or escaped calendar preflight: %v", err)
			}
		})
	}
}

func TestRestorePlanRejectsHistoricalWeakCredentialKeys(t *testing.T) {
	s, db, _ := newTestStore(t)
	activated(t, s)
	backup := restoreTestSnapshot(t, db, "backup")
	latest := restoreTestSnapshot(t, db, "latest")
	identity := make([]byte, 32)
	identity[0] = 1
	weak := base64.StdEncoding.EncodeToString(identity)
	restoreTestMutate(t, latest, "UPDATE deviceauth_credentials SET public_key='"+weak+"'; UPDATE deviceauth_credential_receipts SET public_key='"+weak+"'")
	q := restoreTestRequest(t, backup, latest, testNow)
	plan, err := PlanOfflineRestore(context.Background(), q)
	if !errors.Is(err, ErrRestorePlan) || plan.PlanID != "" || strings.Contains(err.Error(), weak) {
		t.Fatal("legacy weak identity was accepted or exposed")
	}
}

func TestRestoreStrictJSONBoundsAndEncoding(t *testing.T) {
	var target []any
	for _, data := range [][]byte{
		[]byte(strings.Repeat("[", 10) + "0" + strings.Repeat("]", 10)),
		[]byte("[" + strings.Repeat("0,", 40000) + "0]"),
		{'[', '"', 0x80, '"', ']'},
	} {
		if err := RestoreStrictJSON(data, &target); !errors.Is(err, ErrRestorePlan) || err.Error() != restoreFailure("invalid_json").Error() {
			t.Fatal("unbounded or lossy JSON input was accepted")
		}
	}
	if err := RestoreStrictJSON([]byte(`[[[[[[[[0]]]]]]]]`), &target); err != nil {
		t.Fatal("bounded JSON refused", err)
	}
}
