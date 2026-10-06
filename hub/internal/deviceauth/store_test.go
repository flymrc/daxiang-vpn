package deviceauth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func testKey(n int) string {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint64(b, uint64(n)+1)
	return base64.StdEncoding.EncodeToString(b)
}
func testPolicy() Policy {
	return Policy{Epoch: "offline-epoch-1", ManagedBy: "offline-customer-executor", AddressPools: []string{"10.66.0.0/24"}, Protected: []Protection{{PublicKey: testKey(90000)}, {Prefix: "10.66.0.1/32"}, {Prefix: "10.66.0.11/32"}, {Prefix: "10.66.0.100/32"}}}
}
func testOptions() Options {
	return Options{Policy: testPolicy(), Now: func() time.Time { return testNow }}
}
func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func newTestStore(t *testing.T) (*Store, *sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "authority.sqlite")
	db := openTestDB(t, path)
	s, err := New(context.Background(), db, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	return s, db, path
}
func reopen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := New(context.Background(), openTestDB(t, path), testOptions())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func enroll(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.Enroll(context.Background(), Enrollment{DeviceID: id, OwnerID: "owner", Role: "customer", ValidUntil: testNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}
func command(id, action string, generation int64, key int, address, idempotency string) Command {
	c := Command{Actor: Actor{ID: "owner-admin", OwnerID: "owner"}, DeviceID: id, Action: action, IdempotencyKey: idempotency, ExpectedGeneration: generation, Reason: "synthetic-test"}
	if action == "apply" {
		c.PublicKey = testKey(key)
		c.Address = address
	}
	return c
}
func submit(t *testing.T, s *Store, c Command) Operation {
	t.Helper()
	op, err := s.Submit(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func process(t *testing.T, s *Store, op Operation, e Executor) {
	t.Helper()
	if err := s.Process(context.Background(), op.ID, e); err != nil {
		t.Fatal(err)
	}
}

type fakeWG struct {
	mu                    sync.Mutex
	peers                 map[string][]string
	actions               []string
	failApply, failRemove bool
	failVerify            bool
	acted                 bool
	delay                 time.Duration
	entered               chan struct{}
	release               chan struct{}
}

func newFake() *fakeWG { return &fakeWG{peers: make(map[string][]string)} }
func (f *fakeWG) Snapshot(ctx context.Context, _ Fence) ([]Peer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failVerify && f.acted {
		return nil, errors.New("synthetic lost response with secret-stderr-not-persisted")
	}
	var result []Peer
	for k, v := range f.peers {
		result = append(result, Peer{PublicKey: k, AllowedIPs: append([]string(nil), v...)})
	}
	return result, nil
}
func (f *fakeWG) Apply(ctx context.Context, _ Fence, p Peer) error {
	f.mu.Lock()
	f.peers[p.PublicKey] = append([]string(nil), p.AllowedIPs...)
	f.actions = append(f.actions, "apply:"+p.PublicKey)
	f.acted = true
	fail, delay, entered, release := f.failApply, f.delay, f.entered, f.release
	f.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	if fail {
		return errors.New("synthetic response lost after apply")
	}
	return nil
}
func (f *fakeWG) Remove(ctx context.Context, _ Fence, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.peers, key)
	f.actions = append(f.actions, "remove:"+key)
	f.acted = true
	if f.failRemove {
		return errors.New("synthetic response lost after remove")
	}
	return nil
}
func (f *fakeWG) state() map[string][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := map[string][]string{}
	for k, v := range f.peers {
		r[k] = append([]string(nil), v...)
	}
	return r
}

func TestAcceptedIsNotAppliedAndIdempotentRetry(t *testing.T) {
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	c := command("d1", "apply", 0, 1, "10.66.0.30/32", "first")
	op := submit(t, s, c)
	d, err := s.Device(context.Background(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	if op.State != "pending" || d.Generation != 1 || d.AppliedGeneration != 0 {
		t.Fatalf("premature success: %+v %+v", op, d)
	}
	retried := submit(t, s, c)
	if retried.ID != op.ID || retried.Generation != 1 {
		t.Fatal("idempotency created another operation")
	}
	wg := newFake()
	process(t, s, op, wg)
	process(t, s, op, wg)
	d, _ = s.Device(context.Background(), "d1")
	op, _ = s.Operation(context.Background(), op.ID)
	if op.State != "done" || op.Attempts != 2 || d.AppliedGeneration != 1 || len(wg.actions) != 1 {
		t.Fatalf("not exact verified retry: %+v %+v %v", op, d, wg.actions)
	}
}

func TestIdempotencyConflictOwnerAndCASRejectWithoutMutation(t *testing.T) {
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	c := command("d1", "apply", 0, 1, "10.66.0.30/32", "same")
	op := submit(t, s, c)
	changed := c
	changed.Reason = "different"
	if _, err := s.Submit(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("digest conflict: %v", err)
	}
	foreign := c
	foreign.Actor.OwnerID = "foreign"
	if _, err := s.Submit(context.Background(), foreign); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("foreign owner: %v", err)
	}
	stale := command("d1", "apply", 0, 2, "10.66.0.30/32", "stale")
	if _, err := s.Submit(context.Background(), stale); !errors.Is(err, ErrGeneration) {
		t.Fatalf("stale generation: %v", err)
	}
	d, _ := s.Device(context.Background(), "d1")
	pending, _ := s.Pending(context.Background())
	if d.Generation != 1 || len(pending) != 1 || pending[0].ID != op.ID {
		t.Fatal("rejected request mutated state")
	}
}

func TestIndependentStoresConcurrentSameIdempotencyAndGeneration(t *testing.T) {
	s, _, path := newTestStore(t)
	s2 := reopen(t, path)
	enroll(t, s, "d1")
	cmd := command("d1", "apply", 0, 1, "10.66.0.30/32", "concurrent")
	results := make(chan Operation, 16)
	failures := make(chan error, 16)
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			store := s
			if i%2 != 0 {
				store = s2
			}
			op, err := store.Submit(context.Background(), cmd)
			if err != nil {
				failures <- err
			} else {
				results <- op
			}
		}(i)
	}
	workers.Wait()
	close(failures)
	close(results)
	for err := range failures {
		t.Fatal(err)
	}
	id := ""
	for op := range results {
		if id == "" {
			id = op.ID
		}
		if op.ID != id || op.Generation != 1 {
			t.Fatal("duplicate accepted operation")
		}
	}
	d, _ := s.Device(context.Background(), "d1")
	if d.Generation != 1 {
		t.Fatal("generation advanced twice")
	}
}

func TestConcurrentDifferentRequestsOnlyOneGenerationWins(t *testing.T) {
	s, _, path := newTestStore(t)
	s2 := reopen(t, path)
	enroll(t, s, "d1")
	errs := make(chan error, 12)
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			store := s
			if i%2 != 0 {
				store = s2
			}
			_, err := store.Submit(context.Background(), command("d1", "apply", 0, i+1, "10.66.0.30/32", fmt.Sprintf("request-%d", i)))
			errs <- err
		}(i)
	}
	workers.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrGeneration) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("winners: %d", success)
	}
}

func TestUniqueOwnershipProtectionAndCanonicalInput(t *testing.T) {
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	enroll(t, s, "d2")
	submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	cases := []struct {
		name string
		cmd  Command
		want error
	}{
		{"other device key", command("d2", "apply", 0, 1, "10.66.0.31/32", "key"), ErrConflict},
		{"other device address", command("d2", "apply", 0, 2, "10.66.0.30/32", "address"), ErrConflict},
		{"protected key", command("d2", "apply", 0, 90000, "10.66.0.31/32", "protected-key"), ErrProtected},
		{"RDP address", command("d2", "apply", 0, 2, "10.66.0.11/32", "rdp"), ErrProtected},
		{"Mac address", command("d2", "apply", 0, 2, "10.66.0.100/32", "mac"), ErrProtected},
		{"outside pool", command("d2", "apply", 0, 2, "192.0.2.1/32", "outside"), ErrProtected},
		{"broad range", command("d2", "apply", 0, 2, "10.66.0.32/27", "broad"), ErrInvalid},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.Submit(context.Background(), tt.cmd); !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
	if err := s.Enroll(context.Background(), Enrollment{DeviceID: "rdp", OwnerID: "owner", Role: "admin", ValidUntil: testNow.Add(time.Hour)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("noncustomer enrolled: %v", err)
	}
	d, _ := s.Device(context.Background(), "d2")
	if d.Generation != 0 {
		t.Fatal("rejection mutated foreign device")
	}
}

func TestUnknownRuntimePeerNeverAdoptedOrRemoved(t *testing.T) {
	for _, variant := range []string{"same-key", "overlapping-unknown-prefix"} {
		t.Run(variant, func(t *testing.T) {
			s, _, _ := newTestStore(t)
			enroll(t, s, "d1")
			op := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
			wg := newFake()
			if variant == "same-key" {
				wg.peers[testKey(1)] = []string{"10.66.0.30/32"}
			} else {
				wg.peers[testKey(88)] = []string{"10.66.0.0/24"}
			}
			before := wg.state()
			if err := s.Process(context.Background(), op.ID, wg); !errors.Is(err, ErrProtected) {
				t.Fatalf("unknown runtime accepted: %v", err)
			}
			if !reflect.DeepEqual(before, wg.state()) || len(wg.actions) != 0 {
				t.Fatal("unknown peer mutated")
			}
			op, _ = s.Operation(context.Background(), op.ID)
			if op.State != "degraded" || op.LastError != "protected_peer" {
				t.Fatalf("unsafe failure state: %+v", op)
			}
		})
	}
}

func TestRevokeProtectsUnrelatedUnknownAndNoncustomerPeers(t *testing.T) {
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	wg := newFake()
	wg.peers[testKey(90000)] = []string{"10.66.0.11/32"}
	wg.peers[testKey(88)] = []string{"10.66.0.60/32"}
	protected := wg.state()
	apply := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	process(t, s, apply, wg)
	revoke := submit(t, s, command("d1", "revoke", 1, 0, "", "revoke"))
	process(t, s, revoke, wg)
	if !reflect.DeepEqual(wg.state(), protected) {
		t.Fatal("revoke disturbed protected or unknown peers")
	}
}

func TestRuntimeOwnershipDriftBlocksEvenRevoke(t *testing.T) {
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	wg := newFake()
	apply := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	process(t, s, apply, wg)
	wg.peers[testKey(1)] = []string{"10.66.0.11/32", "10.66.0.30/32"}
	before := wg.state()
	actions := len(wg.actions)
	revoke := submit(t, s, command("d1", "revoke", 1, 0, "", "revoke"))
	if err := s.Process(context.Background(), revoke.ID, wg); !errors.Is(err, ErrProtected) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, wg.state()) || len(wg.actions) != actions {
		t.Fatal("drifted key removed without verified ownership")
	}
}

func TestApplyRotatePendingThenRevokeRetainsAllOldKeyObligations(t *testing.T) {
	s, db, _ := newTestStore(t)
	enroll(t, s, "d1")
	wg := newFake()
	firstCmd := command("d1", "apply", 0, 1, "10.66.0.30/32", "first")
	a := submit(t, s, firstCmd)
	process(t, s, a, wg)
	b := submit(t, s, command("d1", "apply", 1, 2, "10.66.0.30/32", "rotate"))
	r := submit(t, s, command("d1", "revoke", 2, 0, "", "revoke"))
	if err := s.Process(context.Background(), b.ID, wg); !errors.Is(err, ErrSuperseded) {
		t.Fatal(err)
	}
	process(t, s, r, wg)
	if len(wg.state()) != 0 {
		t.Fatal("superseded rotation lost A removal")
	}
	if err := s.Process(context.Background(), a.ID, wg); !errors.Is(err, ErrSuperseded) {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), firstCmd); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("historic idempotency bypassed revoke: %v", err)
	}
	var count, verified int
	if err := db.QueryRow(`SELECT COUNT(*),COUNT(verified_at) FROM deviceauth_tombstones`).Scan(&count, &verified); err != nil {
		t.Fatal(err)
	}
	if count != 2 || verified != 2 {
		t.Fatalf("tombstone loss: %d/%d", verified, count)
	}
	d, _ := s.Device(context.Background(), "d1")
	if d.State != "revoked" || d.Generation != 3 || d.AppliedGeneration != 3 {
		t.Fatalf("bad revoke: %+v", d)
	}
}

func TestSameAddressRotationRemovesOldBeforeApplyingNew(t *testing.T) {
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	wg := newFake()
	a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	process(t, s, a, wg)
	b := submit(t, s, command("d1", "apply", 1, 2, "10.66.0.30/32", "rotate"))
	process(t, s, b, wg)
	if !reflect.DeepEqual(wg.actions, []string{"apply:" + testKey(1), "remove:" + testKey(1), "apply:" + testKey(2)}) {
		t.Fatalf("unsafe route order: %v", wg.actions)
	}
	if _, err := s.Submit(context.Background(), command("d1", "apply", 2, 1, "10.66.0.30/32", "resurrect")); !errors.Is(err, ErrConflict) {
		t.Fatalf("old key resurrected: %v", err)
	}
}

func TestPartialRotationAndLostResultRecoverFromDurableIntent(t *testing.T) {
	for _, variant := range []string{"apply-response-lost", "verification-response-lost", "remove-response-lost"} {
		t.Run(variant, func(t *testing.T) {
			s, _, path := newTestStore(t)
			enroll(t, s, "d1")
			wg := newFake()
			a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
			process(t, s, a, wg)
			b := submit(t, s, command("d1", "apply", 1, 2, "10.66.0.30/32", "rotate"))
			switch variant {
			case "apply-response-lost":
				wg.failApply = true
			case "verification-response-lost":
				wg.acted = false
				wg.failVerify = true
			case "remove-response-lost":
				wg.failRemove = true
			}
			if err := s.Process(context.Background(), b.ID, wg); !errors.Is(err, ErrExecutionUnknown) {
				t.Fatalf("lost response certified: %v", err)
			}
			op, _ := s.Operation(context.Background(), b.ID)
			d, _ := s.Device(context.Background(), "d1")
			if op.State != "degraded" || op.LastError != "execution_unknown" || d.AppliedGeneration != 1 {
				t.Fatalf("premature ack: %+v %+v", op, d)
			}
			wg.failApply = false
			wg.failVerify = false
			wg.failRemove = false
			restarted := reopen(t, path)
			process(t, restarted, b, wg)
			d, _ = restarted.Device(context.Background(), "d1")
			if d.AppliedGeneration != 2 || !reflect.DeepEqual(wg.state(), map[string][]string{testKey(2): {"10.66.0.30/32"}}) {
				t.Fatal("restart did not converge rotation")
			}
		})
	}
}

func TestDisableExpireAndPermanentRevoke(t *testing.T) {
	for _, action := range []string{"disable", "expire", "revoke"} {
		t.Run(action, func(t *testing.T) {
			s, _, _ := newTestStore(t)
			enroll(t, s, "d1")
			wg := newFake()
			a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
			process(t, s, a, wg)
			if action == "expire" {
				if _, err := s.Submit(context.Background(), command("d1", action, 1, 0, "", "tooearly")); !errors.Is(err, ErrInvalid) {
					t.Fatal("expired early")
				}
				s.opts.Now = func() time.Time { return testNow.Add(time.Hour) }
			}
			r := submit(t, s, command("d1", action, 1, 0, "", "stop"))
			if r.Deadline.Sub(s.opts.Now()) != 30*time.Second {
				t.Fatal("budget not durable")
			}
			process(t, s, r, wg)
			if len(wg.state()) != 0 {
				t.Fatal("terminal state retained peer")
			}
			if _, err := s.Submit(context.Background(), command("d1", "apply", 2, 2, "10.66.0.30/32", "newgrant")); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("terminal grant: %v", err)
			}
			if action == "revoke" {
				r2 := submit(t, s, command("d1", "disable", 2, 0, "", "weaken"))
				process(t, s, r2, wg)
				d, _ := s.Device(context.Background(), "d1")
				if d.State != "revoked" {
					t.Fatal("weakened permanent revoke")
				}
			}
		})
	}
}

func TestLateApplyKeepsFenceAndCannotOvertakeRevoke(t *testing.T) {
	s, _, path := newTestStore(t)
	s2 := reopen(t, path)
	s.opts.ActionTimeout = 30 * time.Millisecond
	enroll(t, s, "d1")
	a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	wg := newFake()
	wg.entered = make(chan struct{}, 1)
	wg.release = make(chan struct{})
	applyResult := make(chan error, 1)
	go func() { applyResult <- s.Process(context.Background(), a.ID, wg) }()
	<-wg.entered
	revoked := make(chan Operation, 1)
	revokeError := make(chan error, 1)
	go func() {
		op, err := s2.Submit(context.Background(), command("d1", "revoke", 1, 0, "", "revoke"))
		if err != nil {
			revokeError <- err
		} else {
			revoked <- op
		}
	}()
	time.Sleep(60 * time.Millisecond)
	select {
	case <-revoked:
		t.Fatal("revoke committed while delayed external apply still running")
	case err := <-revokeError:
		t.Fatal(err)
	default:
	}
	close(wg.release)
	if err := <-applyResult; !errors.Is(err, ErrExecutionUnknown) {
		t.Fatalf("late nil certified: %v", err)
	}
	var r Operation
	select {
	case r = <-revoked:
	case err := <-revokeError:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("revoke never acquired released fence")
	}
	wg.release = nil
	wg.entered = nil
	process(t, s2, r, wg)
	if err := s.Process(context.Background(), a.ID, wg); !errors.Is(err, ErrSuperseded) {
		t.Fatal(err)
	}
	if len(wg.state()) != 0 {
		t.Fatal("late apply revived revoked key")
	}
}

func TestPolicyMismatchAndAlternateFenceReject(t *testing.T) {
	_, db, _ := newTestStore(t)
	opts := testOptions()
	opts.Policy.Epoch = "older-epoch"
	if _, err := New(context.Background(), db, opts); !errors.Is(err, ErrPolicy) {
		t.Fatalf("old authority accepted: %v", err)
	}
	opts = testOptions()
	opts.FencePath = filepath.Join(t.TempDir(), "second-lock")
	if _, err := New(context.Background(), db, opts); !errors.Is(err, ErrPolicy) {
		t.Fatalf("split lock accepted: %v", err)
	}
	inMemory, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer inMemory.Close()
	if _, err := New(context.Background(), inMemory, testOptions()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ephemeral DB accepted: %v", err)
	}
}

func TestDatabaseAndFenceHardlinksRejectedWithoutChangingTarget(t *testing.T) {
	s, db, path := newTestStore(t)
	fence := path + ".deviceauth.lock"
	for _, target := range []string{path, fence} {
		alias := target + ".hardlink"
		before, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Link(target, alias); err != nil {
			t.Skipf("hardlink unsupported: %v", err)
		}
		if _, err := New(context.Background(), db, testOptions()); !errors.Is(err, ErrPolicy) {
			t.Fatalf("hardlinked file accepted: %v", err)
		}
		if err := s.Enroll(context.Background(), Enrollment{DeviceID: "blocked", OwnerID: "owner", Role: "customer", ValidUntil: testNow.Add(time.Hour)}); !errors.Is(err, ErrPolicy) {
			t.Fatalf("existing Store accepted replaced link topology: %v", err)
		}
		after, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if before.Mode() != after.Mode() || before.Size() != after.Size() {
			t.Fatal("failed validation rewrote target")
		}
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectoryAliasSharesCanonicalFence(t *testing.T) {
	_, _, path := newTestStore(t)
	alias := filepath.Join(t.TempDir(), "directory-alias")
	if err := createDirectoryAlias(alias, filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	aliasDB := openTestDB(t, filepath.Join(alias, filepath.Base(path)))
	aliasPath, _, identityErr := databaseIdentity(context.Background(), aliasDB)
	var storedFence string
	if identityErr == nil {
		_ = aliasDB.QueryRow(`SELECT fence_path FROM deviceauth_meta WHERE singleton=1`).Scan(&storedFence)
	}
	s, err := New(context.Background(), aliasDB, testOptions())
	if err != nil {
		t.Fatalf("alias=%q canonical=%q storedFence=%q identity=%v: %v", alias, aliasPath, storedFence, identityErr, err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.fencePath != canonical+".deviceauth.lock" {
		t.Fatalf("split alias fence: %s", s.fencePath)
	}
}

type enteredFenceContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (c *enteredFenceContext) Err() error {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Err()
}

func TestDatabaseReplacedWhileWaitingFenceRejectsOldSnapshotApply(t *testing.T) {
	s, db, path := newTestStore(t)
	db.SetMaxIdleConns(0)
	enroll(t, s, "d1")
	wg := newFake()
	a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	process(t, s, a, wg)
	oldSnapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := submit(t, s, command("d1", "revoke", 1, 0, "", "revoke"))
	process(t, s, r, wg)
	// Only the fence handle is held by the fixture. A queued process has
	// finished its initial inode check, but has not acquired the fence yet.
	f, err := openRegular(s.fencePath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = lockFile(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unlockFile(f); _ = f.Close() })
	ctx := &enteredFenceContext{Context: context.Background(), entered: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- s.Process(ctx, a.ID, wg) }()
	select {
	case <-ctx.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("process never completed pre-wait database check")
	}
	archive := filepath.Join(filepath.Dir(path), "revoked-snapshot.sqlite")
	if err = os.Rename(path, archive); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, oldSnapshot, 0600); err != nil {
		t.Fatal(err)
	}
	if err = unlockFile(f); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("queued process did not resume")
	}
	if !errors.Is(err, ErrPolicy) {
		t.Fatalf("old snapshot was executed: %v", err)
	}
	if len(wg.state()) != 0 {
		t.Fatal("old apply revived revoked synthetic key")
	}
}

type clockAdvancingWG struct {
	*fakeWG
	firstSnapshot func()
	afterApply    func()
	snapshots     int
}

func (f *clockAdvancingWG) Snapshot(ctx context.Context, fence Fence) ([]Peer, error) {
	peers, err := f.fakeWG.Snapshot(ctx, fence)
	f.snapshots++
	if f.snapshots == 1 && f.firstSnapshot != nil {
		f.firstSnapshot()
	}
	return peers, err
}
func (f *clockAdvancingWG) Apply(ctx context.Context, fence Fence, peer Peer) error {
	err := f.fakeWG.Apply(ctx, fence, peer)
	if f.afterApply != nil {
		f.afterApply()
	}
	return err
}

func TestObservedExpiryBeforeOrDuringApplyPersistsRemovalGeneration(t *testing.T) {
	for _, phase := range []string{"before-intent-and-apply", "during-external-action"} {
		t.Run(phase, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			enroll(t, s, "d1")
			current := testNow
			s.opts.Now = func() time.Time { return current }
			a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
			wg := &clockAdvancingWG{fakeWG: newFake()}
			advance := func() { current = testNow.Add(time.Hour + time.Second) }
			if phase == "before-intent-and-apply" {
				wg.firstSnapshot = advance
			} else {
				wg.afterApply = advance
			}
			if err := s.Process(context.Background(), a.ID, wg); !errors.Is(err, ErrExpired) {
				t.Fatalf("expiry was certified: %v", err)
			}
			if phase == "before-intent-and-apply" && len(wg.actions) != 0 {
				t.Fatal("known expired authorization still applied")
			}
			d, err := s.Device(context.Background(), "d1")
			if err != nil {
				t.Fatal(err)
			}
			if d.State != "expired" || d.Generation != 2 || d.AppliedGeneration != 0 {
				t.Fatalf("expiry not a durable desired generation: %+v", d)
			}
			var tombstones int
			if err := db.QueryRow(`SELECT COUNT(*) FROM deviceauth_tombstones WHERE public_key=? AND generation=2`, testKey(1)).Scan(&tombstones); err != nil {
				t.Fatal(err)
			}
			if tombstones != 1 {
				t.Fatal("expiry lost removal obligation")
			}
			pending, err := s.Pending(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 || pending[0].Action != "expire" || pending[0].State != "degraded" {
				t.Fatalf("expiry outbox missing: %+v", pending)
			}
			if err := s.Reconcile(context.Background(), "d1", wg); err != nil {
				t.Fatal(err)
			}
			if len(wg.state()) != 0 {
				t.Fatal("cross-expiry external grant did not converge to revoke")
			}
		})
	}
}

func TestNewOwnsPolicyBeforeNormalizationAndCallerReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-policy.sqlite")
	db := openTestDB(t, path)
	opts := testOptions()
	opts.Policy.AddressPools = []string{"10.66.0.128/25", "10.66.0.0/25"}
	opts.Policy.Protected = []Protection{{Prefix: "10.66.0.30/32"}, {PublicKey: testKey(90000)}}
	beforePools := append([]string(nil), opts.Policy.AddressPools...)
	beforeProtected := append([]Protection(nil), opts.Policy.Protected...)
	s, err := New(context.Background(), db, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforePools, opts.Policy.AddressPools) || !reflect.DeepEqual(beforeProtected, opts.Policy.Protected) {
		t.Fatal("New normalized caller-owned policy")
	}
	enroll(t, s, "d1")
	blocked := command("d1", "apply", 0, 1, "10.66.0.30/32", "must-stay-protected")
	if _, err := s.Submit(context.Background(), blocked); !errors.Is(err, ErrProtected) {
		t.Fatal(err)
	}
	opts.Policy.Protected[0].Prefix = ""
	opts.Policy.Protected[1].PublicKey = ""
	if _, err := s.Submit(context.Background(), blocked); !errors.Is(err, ErrProtected) {
		t.Fatalf("caller reload weakened persisted protection: %v", err)
	}
	opts.Policy.AddressPools[0] = "192.0.2.0/24"
	opts.Policy.AddressPools[1] = "198.51.100.0/24"
	submit(t, s, command("d1", "apply", 0, 2, "10.66.0.31/32", "original-pool-stays"))
}

func TestConcurrentNewWithSharedImmutableOptions(t *testing.T) {
	opts := testOptions()
	opts.Policy.AddressPools = []string{"10.66.0.128/25", "10.66.0.0/25"}
	originalPools := append([]string(nil), opts.Policy.AddressPools...)
	originalProtected := append([]Protection(nil), opts.Policy.Protected...)
	dbs := make([]*sql.DB, 8)
	for i := range dbs {
		dbs[i] = openTestDB(t, filepath.Join(t.TempDir(), "concurrent-policy.sqlite"))
	}
	var workers sync.WaitGroup
	errs := make(chan error, len(dbs))
	for _, db := range dbs {
		workers.Add(1)
		go func(db *sql.DB) { defer workers.Done(); _, err := New(context.Background(), db, opts); errs <- err }(db)
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(opts.Policy.AddressPools, originalPools) || !reflect.DeepEqual(opts.Policy.Protected, originalProtected) {
		t.Fatal("concurrent New mutated shared Options")
	}
}
