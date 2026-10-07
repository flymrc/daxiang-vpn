package updateclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
	"zongheng-vpn/shared/updateverify"
)

type fixture struct {
	scope                  Scope
	doc                    PolicyDocument
	key                    ed25519.PrivateKey
	now                    time.Time
	root, policy, artifact string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	scope := Scope{"cli", runtime.GOOS, runtime.GOARCH, "stable"}
	f := fixture{scope: scope, key: key, now: now, root: t.TempDir()}
	f.doc = PolicyDocument{SchemaVersion: Version, Revision: 1, Scope: scope, MinVersion: "1.0.0", MinProtocol: 1, MaxProtocol: 2, MinSequence: 1, SecurityFloor: 1, MaxArtifactSize: 1 << 20, MaxValiditySeconds: 3600, Keys: []PolicyKey{{ID: "fixture-one", PublicKey: base64.StdEncoding.EncodeToString(pub), ValidFrom: now.Unix() - 60, ValidUntil: now.Unix() + 86400, MinSequence: 1, MaxSequence: updateverify.MaxSafeInteger}}}
	f.policy = filepath.Join(f.root, "approved-policy.json")
	f.artifact = filepath.Join(f.root, "candidate.bin")
	f.writePolicy(t)
	if e = os.WriteFile(f.artifact, []byte("synthetic-approved-artifact-never-executed"), 0600); e != nil {
		t.Fatal(e)
	}
	return f
}

func (f fixture) writePolicy(t *testing.T) {
	t.Helper()
	raw, e := json.MarshalIndent(f.doc, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(f.policy, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func scopeArgs(s Scope) []string {
	return []string{"--product", s.Product, "--platform", s.Platform, "--architecture", s.Architecture, "--channel", s.Channel}
}
func (f fixture) approvalArgs(t *testing.T, command string) []string {
	t.Helper()
	raw, e := os.ReadFile(f.policy)
	if e != nil {
		t.Fatal(e)
	}
	args := append([]string{command}, scopeArgs(f.scope)...)
	return append(args, "--policy-file", f.policy, "--approve-policy-sha256", hash(raw), "--policy-revision", fmt.Sprint(f.doc.Revision))
}
func (f fixture) metadata(t *testing.T, sequence uint64, change func(*updateverify.Metadata)) string {
	t.Helper()
	artifact, e := os.ReadFile(f.artifact)
	if e != nil {
		t.Fatal(e)
	}
	m := updateverify.Metadata{Product: f.scope.Product, Platform: f.scope.Platform, Architecture: f.scope.Architecture, Channel: f.scope.Channel, Version: "2.0.0", Protocol: 1, SourceCommit: strings.Repeat("a", 40), ArtifactName: "approved-fixture.bin", ArtifactSize: int64(len(artifact)), ArtifactSHA256: hash(artifact), IssuedAt: f.now.Unix() - 1, ExpiresAt: f.now.Unix() + 1800, ReleaseSequence: sequence, SecurityVersion: 2, SecurityFloor: 1}
	if change != nil {
		change(&m)
	}
	raw, e := updateverify.CanonicalMetadata(m)
	if e != nil {
		t.Fatal(e)
	}
	message, e := updateverify.SigningMessage(f.doc.Keys[0].ID, raw)
	if e != nil {
		t.Fatal(e)
	}
	envelope := struct {
		SchemaVersion int             `json:"schema_version"`
		KeyID         string          `json:"key_id"`
		Metadata      json.RawMessage `json:"metadata"`
		Signature     string          `json:"signature"`
	}{1, f.doc.Keys[0].ID, raw, hex.EncodeToString(ed25519.Sign(f.key, message))}
	encoded, e := json.Marshal(envelope)
	if e != nil {
		t.Fatal(e)
	}
	file, e := os.CreateTemp(f.root, "candidate-envelope-*.json")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = file.Write(encoded); e != nil {
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	return file.Name()
}
func (f fixture) verifyArgs(metadata string) []string {
	args := append([]string{"verify"}, scopeArgs(f.scope)...)
	return append(args, "--metadata-file", metadata, "--artifact-file", f.artifact)
}

func run(t *testing.T, home paths.Context, args []string) Receipt {
	t.Helper()
	var out, stderr bytes.Buffer
	e := Run(context.Background(), home, args, &out, &stderr)
	r, de := DecodeReceipt(out.Bytes())
	if de != nil || stderr.Len() != 0 || (e == nil) != r.OK || (!r.OK && !errors.Is(e, ErrReported)) {
		t.Fatalf("receipt/exit contract failed: error=%v decode=%v", e, de)
	}
	return r
}
func expectCode(t *testing.T, r Receipt, code string) {
	t.Helper()
	if r.OK || r.Code != code || r.Staging != nil || r.WatermarkCommitted {
		t.Fatalf("got outcome=%s code=%s OK=%v, want %s", r.Outcome, r.Code, r.OK, code)
	}
}

func TestRunPersistsWatermarkAndRechecksIdempotentArtifact(t *testing.T) {
	f := newFixture(t)
	home := privateHome(t)
	expectCode(t, run(t, home, append([]string{"inspect"}, scopeArgs(f.scope)...)), "registration_required")
	if r := run(t, home, f.approvalArgs(t, "enroll")); !r.OK || r.Outcome != "enrolled" {
		t.Fatal("enroll failed")
	}
	metadata := f.metadata(t, 10, nil)
	r := run(t, home, f.verifyArgs(metadata))
	if !r.OK || !r.WatermarkCommitted || r.Staging.Idempotent || r.Floors.MinSequence != 10 {
		t.Fatal("first staging watermark failed")
	}
	r = run(t, home, f.verifyArgs(metadata))
	if !r.OK || !r.Staging.Idempotent {
		t.Fatal("identical current package was not idempotent")
	}
	expectCode(t, run(t, home, f.verifyArgs(f.metadata(t, 9, nil))), "rollback_refused")
	expectCode(t, run(t, home, f.verifyArgs(f.metadata(t, 10, func(m *updateverify.Metadata) { m.SourceCommit = strings.Repeat("b", 40) }))), "replay_refused")
	if e := os.WriteFile(f.artifact, []byte("same invocation must rehash wrong artifact"), 0600); e != nil {
		t.Fatal(e)
	}
	expectCode(t, run(t, home, f.verifyArgs(metadata)), "artifact_refused")
	r = run(t, home, append([]string{"inspect"}, scopeArgs(f.scope)...))
	if !r.OK || r.Floors.MinSequence != 10 || r.Staging != nil || r.WatermarkCommitted {
		t.Fatal("inspect pretended a new verification or lost watermark")
	}
}

func TestEnrollmentRequiresExactApprovedBytesAndMissingStateNeverResets(t *testing.T) {
	f := newFixture(t)
	t.Run("approval", func(t *testing.T) {
		home := privateHome(t)
		args := f.approvalArgs(t, "enroll")
		for i := range args {
			if args[i] == "--approve-policy-sha256" {
				args[i+1] = strings.Repeat("0", 64)
			}
		}
		expectCode(t, run(t, home, args), "policy_approval_mismatch")
		for _, name := range []string{RegistrationName, StateName} {
			if _, e := os.Stat(filepath.Join(home.Root, name)); !os.IsNotExist(e) {
				t.Fatal("wrong approval created registration")
			}
		}
	})
	for _, missing := range []string{RegistrationName, StateName, "both"} {
		t.Run(missing, func(t *testing.T) {
			home := privateHome(t)
			if !run(t, home, f.approvalArgs(t, "enroll")).OK {
				t.Fatal("enroll")
			}
			if missing == "both" {
				for _, name := range []string{RegistrationName, StateName} {
					if e := os.Remove(filepath.Join(home.Root, name)); e != nil {
						t.Fatal(e)
					}
				}
			} else if e := os.Remove(filepath.Join(home.Root, missing)); e != nil {
				t.Fatal(e)
			}
			want := "registration_incomplete"
			if missing == "both" {
				want = "registration_required"
			}
			expectCode(t, run(t, home, f.verifyArgs(f.metadata(t, 10, nil))), want)
			if missing != "both" {
				expectCode(t, run(t, home, f.approvalArgs(t, "enroll")), want)
			}
		})
	}
	home := privateHome(t)
	if !run(t, home, f.approvalArgs(t, "enroll")).OK {
		t.Fatal("enroll")
	}
	expectCode(t, run(t, home, f.approvalArgs(t, "enroll")), "already_registered")
}

func TestPolicyApprovalIsMonotonicAtomicAndRevokesKeysWithoutDiscardingPrevious(t *testing.T) {
	f := newFixture(t)
	home := privateHome(t)
	if !run(t, home, f.approvalArgs(t, "enroll")).OK {
		t.Fatal("enroll")
	}
	oldMetadata := f.metadata(t, 10, nil)
	if !run(t, home, f.verifyArgs(oldMetadata)).OK {
		t.Fatal("verify")
	}
	expectCode(t, run(t, home, f.approvalArgs(t, "policy-approve")), "policy_revision_refused")
	f.doc.Revision = 2
	f.writePolicy(t)
	expectCode(t, run(t, home, f.approvalArgs(t, "policy-approve")), "policy_revision_refused")
	f.doc.MinVersion = "2.0.0"
	f.doc.MinSequence = 10
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	f.key = key
	f.doc.Keys[0].PublicKey = base64.StdEncoding.EncodeToString(pub)
	f.doc.Keys[0].ID = "fixture-two"
	f.writePolicy(t)
	if r := run(t, home, f.approvalArgs(t, "policy-approve")); !r.OK || r.PolicyRevision != 2 || r.Floors.MinSequence != 10 || r.Staging != nil {
		t.Fatal("rotation approval lost watermark")
	}
	expectCode(t, run(t, home, f.verifyArgs(oldMetadata)), "signature_refused")
	expectCode(t, run(t, home, f.verifyArgs(f.metadata(t, 10, nil))), "replay_refused")
	if !run(t, home, f.verifyArgs(f.metadata(t, 11, nil))).OK {
		t.Fatal("new sequence/new approved key failed")
	}
	f.doc.Revision = 3
	f.doc.MinSequence = 11
	f.doc.SecurityFloor = 5
	f.doc.MinVersion = "3.0.0"
	f.writePolicy(t)
	if !run(t, home, f.approvalArgs(t, "policy-approve")).OK {
		t.Fatal("raising approval floors failed")
	}
	expectCode(t, run(t, home, f.verifyArgs(f.metadata(t, 12, nil))), "rollback_refused")
	r := run(t, home, append([]string{"inspect"}, scopeArgs(f.scope)...))
	if !r.OK || r.Floors.SecurityFloor != 5 || r.Floors.MinVersion != "3.0.0" || r.Floors.MinSequence != 11 {
		t.Fatal("bad candidate erased approved floors")
	}
}

type memoryStore struct {
	values               map[string][]byte
	failName, failAction string
	persistOnFail        bool
	readAfterFail        bool
	committed            bool
	onCommit             func()
}

func memory() *memoryStore { return &memoryStore{values: map[string][]byte{}} }
func (s *memoryStore) read(name string) ([]byte, error) {
	if s.readAfterFail && s.committed && name == s.failName {
		return nil, errors.New("synthetic-private-path-secret")
	}
	raw, ok := s.values[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), raw...), nil
}
func (s *memoryStore) create(name string, raw []byte) error {
	if _, ok := s.values[name]; ok {
		return os.ErrExist
	}
	return s.put(name, raw, "create")
}
func (s *memoryStore) write(name string, raw []byte) error { return s.put(name, raw, "write") }
func (s *memoryStore) put(name string, raw []byte, action string) error {
	bad := name == s.failName && action == s.failAction
	if !bad || s.persistOnFail {
		s.values[name] = append([]byte(nil), raw...)
		s.committed = true
		if s.onCommit != nil {
			s.onCommit()
		}
	}
	if bad {
		return errors.New("synthetic-private-path-secret")
	}
	return nil
}
func executeArgs(t *testing.T, s storage, args []string, clock func() time.Time) Receipt {
	t.Helper()
	o, e := parse(args)
	if e != nil {
		t.Fatal(e)
	}
	r := execute(context.Background(), s, o, clock)
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = DecodeReceipt(raw); e != nil {
		t.Fatal("private receipt violated public contract")
	}
	if bytes.Contains(raw, []byte("synthetic-private-path-secret")) {
		t.Fatal("raw storage error leaked")
	}
	return r
}

func TestCommitFailuresNeverReturnStagingPermitOrRepairPrevious(t *testing.T) {
	f := newFixture(t)
	clock := func() time.Time { return f.now }
	for _, phase := range []string{"anchor-before", "state-create-before", "state-write-before", "state-write-after", "readback"} {
		t.Run(phase, func(t *testing.T) {
			s := memory()
			if phase == "anchor-before" || phase == "state-create-before" {
				s.failAction = "create"
				s.failName = RegistrationName
				if phase == "state-create-before" {
					s.failName = StateName
				}
				r := executeArgs(t, s, f.approvalArgs(t, "enroll"), clock)
				expectCode(t, r, "commit_unknown")
				if !r.CommitUnknown {
					t.Fatal("commit failed without uncertainty")
				}
				if phase == "state-create-before" {
					s.failAction = ""
					expectCode(t, executeArgs(t, s, f.approvalArgs(t, "enroll"), clock), "registration_incomplete")
				}
				return
			}
			if !executeArgs(t, s, f.approvalArgs(t, "enroll"), clock).OK {
				t.Fatal("enroll")
			}
			if !executeArgs(t, s, f.verifyArgs(f.metadata(t, 10, nil)), clock).OK {
				t.Fatal("verify")
			}
			old := append([]byte(nil), s.values[StateName]...)
			s.committed = false
			s.failName = StateName
			s.failAction = "write"
			s.persistOnFail = phase == "state-write-after"
			if phase == "readback" {
				s.failAction = ""
				s.readAfterFail = true
			}
			r := executeArgs(t, s, f.verifyArgs(f.metadata(t, 11, nil)), clock)
			expectCode(t, r, "commit_unknown")
			if !r.CommitUnknown {
				t.Fatal("uncertain commit was not explicit")
			}
			if phase == "state-write-before" && !bytes.Equal(old, s.values[StateName]) {
				t.Fatal("prewrite failure changed previous")
			}
			if phase != "state-write-before" && bytes.Equal(old, s.values[StateName]) {
				t.Fatal("successful write was rolled back after lost acknowledgement")
			}
			s.failAction = ""
			s.readAfterFail = false
			r = executeArgs(t, s, append([]string{"inspect"}, scopeArgs(f.scope)...), clock)
			want := uint64(11)
			if phase == "state-write-before" {
				want = 10
			}
			if !r.OK || r.Floors.MinSequence != want {
				t.Fatal("reopen could not reveal actual committed watermark")
			}
		})
	}
}

func TestProtectedStateStrictnessAndNegativeFacts(t *testing.T) {
	f := newFixture(t)
	s := memory()
	clock := func() time.Time { return f.now }
	if !executeArgs(t, s, f.approvalArgs(t, "enroll"), clock).OK {
		t.Fatal("enroll")
	}
	if !executeArgs(t, s, f.verifyArgs(f.metadata(t, 10, nil)), clock).OK {
		t.Fatal("verify")
	}
	saved := append([]byte(nil), s.values[StateName]...)
	cases := map[string]func(*state){"id": func(st *state) { st.ID = strings.Repeat("0", 32) }, "digest": func(st *state) { st.PolicySHA256 = strings.Repeat("0", 64) }, "revision": func(st *state) { st.PolicyRevision = 0 }, "floor": func(st *state) { st.Floors.MinSequence = 1 }, "missing-history": func(st *state) { st.Previous = nil }, "flag": func(st *state) { st.HasPrevious = false }, "receipt": func(st *state) { st.Previous.Metadata.SourceCommit = strings.Repeat("b", 40) }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var st state
			if e := json.Unmarshal(saved, &st); e != nil {
				t.Fatal(e)
			}
			change(&st)
			raw, e := json.Marshal(st)
			if e != nil {
				t.Fatal(e)
			}
			s.values[StateName] = raw
			expectCode(t, executeArgs(t, s, append([]string{"inspect"}, scopeArgs(f.scope)...), clock), "corrupt_state")
		})
	}
	for name, raw := range map[string][]byte{"case": bytes.Replace(saved, []byte("schema_version"), []byte("Schema_Version"), 1), "duplicate": append([]byte(`{"schema_version":1,`), saved[1:]...), "null": bytes.Replace(saved, []byte(`"has_previous":true`), []byte(`"has_previous":null`), 1), "utf8": append(append([]byte(nil), saved[:len(saved)-1]...), 0xff), "oversize": bytes.Repeat([]byte(" "), MaxStateBytes+1)} {
		t.Run(name, func(t *testing.T) {
			s.values[StateName] = raw
			expectCode(t, executeArgs(t, s, append([]string{"inspect"}, scopeArgs(f.scope)...), clock), "corrupt_state")
		})
	}
	s.values[StateName] = saved
	wrong := f.scope
	wrong.Channel = "beta"
	expectCode(t, executeArgs(t, s, append([]string{"inspect"}, scopeArgs(wrong)...), clock), "scope_mismatch")
}

func TestPolicyJSONResourcesNumbersAndArgumentsAreStrict(t *testing.T) {
	f := newFixture(t)
	raw, e := os.ReadFile(f.policy)
	if e != nil {
		t.Fatal(e)
	}
	for name, bad := range map[string][]byte{"case": bytes.Replace(raw, []byte("schema_version"), []byte("SCHEMA_VERSION"), 1), "duplicate": append([]byte(`{"schema_vers\u0069on":1,`), raw[1:]...), "null": bytes.Replace(raw, []byte(`"keys": [`), []byte(`"keys": null,"other": [`), 1), "unknown": append([]byte(`{"unknown":1,`), raw[1:]...), "seconds-wrap": bytes.Replace(raw, []byte(`"max_validity_seconds": 3600`), []byte(`"max_validity_seconds": 36028797018967568`), 1), "revision-over": bytes.Replace(raw, []byte(`"policy_revision": 1`), []byte(`"policy_revision": 9007199254740992`), 1), "float": bytes.Replace(raw, []byte(`"policy_revision": 1`), []byte(`"policy_revision": 1.0`), 1), "exponent": bytes.Replace(raw, []byte(`"min_sequence": 1`), []byte(`"min_sequence": 1e0`), 1), "utf8": append(raw[:len(raw)-1], 0xff), "size": bytes.Repeat([]byte(" "), MaxPolicyBytes+1)} {
		t.Run(name, func(t *testing.T) {
			if _, _, e := decodePolicy(bad); e == nil {
				t.Fatal("malformed policy accepted")
			}
		})
	}
	args := f.approvalArgs(t, "enroll")
	for _, bad := range [][]string{append(append([]string{}, args...), "--policy-revision", "2"), append(append([]string{}, args...), "--now", "1"), append(append([]string{}, args...), "--json=false"), append(append([]string{}, args...), "--timeout", "31s"), append(append([]string{}, f.verifyArgs("x")...), "--policy-file", f.policy), {"inspect"}} {
		if _, e := parse(bad); e == nil {
			t.Fatal("irrelevant/duplicate option accepted")
		}
	}
}

func TestExpiryOrKeyWindowDuringArtifactReadRefusesCommit(t *testing.T) {
	for _, kind := range []string{"metadata", "key"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			if kind == "key" {
				f.doc.Keys[0].ValidUntil = f.now.Unix() + 5
				f.writePolicy(t)
			}
			s := memory()
			clock := func() time.Time { return f.now }
			if !executeArgs(t, s, f.approvalArgs(t, "enroll"), clock).OK {
				t.Fatal("enroll")
			}
			old := append([]byte(nil), s.values[StateName]...)
			calls := 0
			late := f.now.Add(time.Hour)
			if kind == "key" {
				late = f.now.Add(6 * time.Second)
			}
			clock = func() time.Time {
				calls++
				if calls >= 3 {
					return late
				}
				return f.now
			}
			code := "metadata_refused"
			if kind == "key" {
				code = "signature_refused"
			}
			expectCode(t, executeArgs(t, s, f.verifyArgs(f.metadata(t, 10, nil)), clock), code)
			if !bytes.Equal(old, s.values[StateName]) {
				t.Fatal("expired verification committed watermark")
			}
		})
	}
}

func TestRunCancellationLockAndLostResponseKeepAuthority(t *testing.T) {
	f := newFixture(t)
	home := privateHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if e := Run(ctx, home, f.approvalArgs(t, "enroll"), &out, io.Discard); !errors.Is(e, ErrReported) {
		t.Fatal("precancel exit")
	}
	r, e := DecodeReceipt(out.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	expectCode(t, r, "command_cancelled")
	if _, e = os.Stat(filepath.Join(home.Root, RegistrationName)); !os.IsNotExist(e) {
		t.Fatal("precancel registered")
	}
	if !run(t, home, f.approvalArgs(t, "enroll")).OK {
		t.Fatal("enroll")
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- proxy.WithOperationLock(home, func() error { close(locked); <-release; return nil })
	}()
	<-locked
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	out.Reset()
	start := time.Now()
	e = Run(ctx, home, append(append([]string{"inspect"}, scopeArgs(f.scope)...), "--timeout", "100ms"), &out, io.Discard)
	close(release)
	if lockErr := <-finished; lockErr != nil {
		t.Fatal(lockErr)
	}
	if !errors.Is(e, ErrReported) || time.Since(start) > time.Second {
		t.Fatal("lock cancellation was not bounded")
	}
	r, e = DecodeReceipt(out.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	expectCode(t, r, "command_cancelled")
	metadata := f.metadata(t, 10, nil)
	if e = Run(context.Background(), home, f.verifyArgs(metadata), failingWriter{}, io.Discard); !errors.Is(e, ErrReported) {
		t.Fatal("lost output reported success")
	}
	r = run(t, home, append([]string{"inspect"}, scopeArgs(f.scope)...))
	if !r.OK || r.Floors.MinSequence != 10 {
		t.Fatal("response loss erased committed watermark")
	}
	r = run(t, home, f.verifyArgs(metadata))
	if !r.OK || !r.Staging.Idempotent {
		t.Fatal("response loss did not permit exact current revalidation")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic-output-secret") }

func TestConcurrentCommandsShareOneLatestWatermark(t *testing.T) {
	f := newFixture(t)
	home := privateHome(t)
	if !run(t, home, f.approvalArgs(t, "enroll")).OK {
		t.Fatal("enroll")
	}
	low, high := f.metadata(t, 50, nil), f.metadata(t, 51, nil)
	gate := make(chan struct{})
	results := make(chan Receipt, 2)
	var wg sync.WaitGroup
	for _, metadata := range []string{low, high} {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			<-gate
			var out bytes.Buffer
			_ = Run(context.Background(), home, f.verifyArgs(path), &out, io.Discard)
			r, e := DecodeReceipt(out.Bytes())
			if e != nil {
				results <- Receipt{}
				return
			}
			results <- r
		}(metadata)
	}
	close(gate)
	wg.Wait()
	close(results)
	for r := range results {
		if !r.OK && r.Code != "replay_refused" && r.Code != "rollback_refused" {
			t.Fatalf("unexpected concurrent result %s", r.Code)
		}
	}
	r := run(t, home, append([]string{"inspect"}, scopeArgs(f.scope)...))
	if !r.OK || r.Floors.MinSequence != 51 {
		t.Fatal("out-of-order race lowered latest watermark")
	}
}

func TestPublicReceiptAndSchemasRejectFabricatedSuccess(t *testing.T) {
	f := newFixture(t)
	home := privateHome(t)
	r := run(t, home, f.approvalArgs(t, "enroll"))
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	for name, bad := range map[string][]byte{"duplicate": append([]byte(`{"ok":true,`), raw[1:]...), "case": bytes.Replace(raw, []byte(`"ok"`), []byte(`"OK"`), 1), "null": bytes.Replace(raw, []byte(`"ok":true`), []byte(`"ok":null`), 1), "pretend": bytes.Replace(raw, []byte(`"watermark_committed":false`), []byte(`"watermark_committed":true`), 1), "outcome": bytes.Replace(raw, []byte(`"enrolled"`), []byte(`"watermark_committed"`), 1)} {
		t.Run(name, func(t *testing.T) {
			if _, e := DecodeReceipt(bad); e == nil {
				t.Fatal("fabricated receipt accepted")
			}
		})
	}
	for name, generate := range map[string]func() ([]byte, error){"receipt-v1.schema.json": ReceiptSchema, "policy-v1.schema.json": PolicySchema} {
		fresh, e := generate()
		if e != nil {
			t.Fatal(e)
		}
		saved, e := os.ReadFile(name)
		if e != nil || !bytes.Equal(fresh, saved) {
			t.Fatal("generated schema drift")
		}
	}
}

func TestOwnerControlledOldSnapshotIsOutsideLocalAntiRollbackGuarantee(t *testing.T) {
	f := newFixture(t)
	s := memory()
	clock := func() time.Time { return f.now }
	if !executeArgs(t, s, f.approvalArgs(t, "enroll"), clock).OK {
		t.Fatal("enroll")
	}
	oldAnchor := append([]byte(nil), s.values[RegistrationName]...)
	oldState := append([]byte(nil), s.values[StateName]...)
	if !executeArgs(t, s, f.verifyArgs(f.metadata(t, 50, nil)), clock).OK {
		t.Fatal("verify")
	}
	// An administrator with storage authority can restore a complete old snapshot.
	// Neither ACLs nor a local hash can detect this without an external trust anchor.
	s.values[RegistrationName] = oldAnchor
	s.values[StateName] = oldState
	if !executeArgs(t, s, f.verifyArgs(f.metadata(t, 49, nil)), clock).OK {
		t.Fatal("test unexpectedly claimed external rollback protection")
	}
}
