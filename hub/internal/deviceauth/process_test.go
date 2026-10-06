package deviceauth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fileWG is entirely synthetic. Its durable map makes child-process crashes
// observable independently of SQLite. It never invokes wg, a service or network.
type fileWG struct {
	path, mode, marker, release string
	snapshots                   int
}

func (f *fileWG) read() (map[string][]string, error) {
	data, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var peers map[string][]string
	err = json.Unmarshal(data, &peers)
	return peers, err
}
func (f *fileWG) save(peers map[string][]string) error {
	data, err := json.Marshal(peers)
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, data, 0600)
}
func (f *fileWG) Snapshot(ctx context.Context, _ Fence) ([]Peer, error) {
	f.snapshots++
	if f.mode == "crash-before-action" && f.snapshots == 1 {
		os.Exit(23)
	}
	if f.mode == "crash-after-verify" && f.snapshots == 2 {
		os.Exit(23)
	}
	peers, err := f.read()
	if err != nil {
		return nil, err
	}
	var result []Peer
	for k, v := range peers {
		result = append(result, Peer{PublicKey: k, AllowedIPs: v})
	}
	return result, nil
}
func (f *fileWG) Apply(ctx context.Context, _ Fence, p Peer) error {
	peers, err := f.read()
	if err != nil {
		return err
	}
	peers[p.PublicKey] = p.AllowedIPs
	if err = f.save(peers); err != nil {
		return err
	}
	if f.mode == "crash-after-apply" {
		os.Exit(23)
	}
	if f.mode == "pause-after-apply" {
		if err := os.WriteFile(f.marker, []byte("synthetic action entered"), 0600); err != nil {
			return err
		}
		for {
			if _, err := os.Stat(f.release); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	return nil
}
func (f *fileWG) Remove(ctx context.Context, _ Fence, key string) error {
	peers, err := f.read()
	if err != nil {
		return err
	}
	delete(peers, key)
	if err = f.save(peers); err != nil {
		return err
	}
	if f.mode == "crash-after-remove" {
		os.Exit(23)
	}
	return nil
}

func helperCommand(t *testing.T, path, wirePath, opID, mode, marker, release, expected string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDeviceAuthProcessHelper$", "-test.timeout=9s")
	cmd.Env = append(os.Environ(), "ZHVPN_DEVICEAUTH_HELPER=1", "ZHVPN_DEVICEAUTH_DB="+path, "ZHVPN_DEVICEAUTH_WG="+wirePath, "ZHVPN_DEVICEAUTH_OP="+opID, "ZHVPN_DEVICEAUTH_MODE="+mode, "ZHVPN_DEVICEAUTH_MARKER="+marker, "ZHVPN_DEVICEAUTH_RELEASE="+release, "ZHVPN_DEVICEAUTH_EXPECT="+expected)
	return cmd
}

func TestDeviceAuthProcessHelper(t *testing.T) {
	if os.Getenv("ZHVPN_DEVICEAUTH_HELPER") != "1" {
		return
	}
	path := os.Getenv("ZHVPN_DEVICEAUTH_DB")
	s := reopen(t, path)
	mode := os.Getenv("ZHVPN_DEVICEAUTH_MODE")
	if mode == "crash-after-commit" {
		submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "child-commit"))
		os.Exit(23)
	}
	wg := &fileWG{path: os.Getenv("ZHVPN_DEVICEAUTH_WG"), mode: mode, marker: os.Getenv("ZHVPN_DEVICEAUTH_MARKER"), release: os.Getenv("ZHVPN_DEVICEAUTH_RELEASE")}
	err := s.Process(context.Background(), os.Getenv("ZHVPN_DEVICEAUTH_OP"), wg)
	if os.Getenv("ZHVPN_DEVICEAUTH_EXPECT") == "superseded" {
		if !errors.Is(err, ErrSuperseded) {
			t.Fatalf("old child did not supersede: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestRealProcessCrashCommitActionAndVerificationBoundaries(t *testing.T) {
	for _, phase := range []string{"crash-after-commit", "crash-before-action", "crash-after-apply", "crash-after-verify", "rotate-crash-after-remove", "revoke-crash-after-remove"} {
		t.Run(phase, func(t *testing.T) {
			s, _, path := newTestStore(t)
			enroll(t, s, "d1")
			wirePath := filepath.Join(filepath.Dir(path), "synthetic-wireguard.json")
			wg := &fileWG{path: wirePath}
			var op Operation
			expected := map[string][]string{testKey(1): {"10.66.0.30/32"}}
			switch phase {
			case "crash-after-commit":
			case "rotate-crash-after-remove", "revoke-crash-after-remove":
				a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
				process(t, s, a, wg)
				if strings.HasPrefix(phase, "rotate") {
					op = submit(t, s, command("d1", "apply", 1, 2, "10.66.0.30/32", "rotate"))
					expected = map[string][]string{testKey(2): {"10.66.0.30/32"}}
				} else {
					op = submit(t, s, command("d1", "revoke", 1, 0, "", "revoke"))
					expected = map[string][]string{}
				}
			default:
				op = submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
			}
			mode := phase
			if strings.Contains(phase, "crash-after-remove") {
				mode = "crash-after-remove"
			}
			output, err := helperCommand(t, path, wirePath, op.ID, mode, "", "", "").CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("did not crash at intended boundary: %v %s", err, output)
			}
			restarted := reopen(t, path)
			pending, err := restarted.Pending(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 {
				t.Fatalf("durable pending lost: %+v", pending)
			}
			d, _ := restarted.Device(context.Background(), "d1")
			if d.AppliedGeneration == d.Generation {
				t.Fatal("crash certified generation")
			}
			if err := restarted.Reconcile(context.Background(), "d1", wg); err != nil {
				t.Fatal(err)
			}
			actual, err := wg.read()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("restart incorrect map: %+v", actual)
			}
			final, _ := restarted.Operation(context.Background(), pending[0].ID)
			if final.State != "done" {
				t.Fatalf("restart never acked: %+v", final)
			}
		})
	}
}

func TestRealProcessesFenceDelayedApplyRevokeAndOldReplay(t *testing.T) {
	s, _, path := newTestStore(t)
	enroll(t, s, "d1")
	a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	wirePath := filepath.Join(filepath.Dir(path), "synthetic-wireguard.json")
	marker := filepath.Join(filepath.Dir(path), "entered")
	release := filepath.Join(filepath.Dir(path), "release")
	child := helperCommand(t, path, wirePath, a.ID, "pause-after-apply", marker, release, "")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.Process != nil {
			_ = child.Process.Kill()
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never reached external action")
		}
		time.Sleep(10 * time.Millisecond)
	}
	revoked := make(chan Operation, 1)
	failure := make(chan error, 1)
	go func() {
		op, err := s.Submit(context.Background(), command("d1", "revoke", 1, 0, "", "revoke"))
		if err != nil {
			failure <- err
		} else {
			revoked <- op
		}
	}()
	time.Sleep(80 * time.Millisecond)
	select {
	case <-revoked:
		t.Fatal("second process committed revoke before old child returned")
	case err := <-failure:
		t.Fatal(err)
	default:
	}
	if err := os.WriteFile(release, []byte("release synthetic action"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	var r Operation
	select {
	case r = <-revoked:
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("revoke did not acquire crash-safe fence")
	}
	wg := &fileWG{path: wirePath}
	process(t, s, r, wg)
	if output, err := helperCommand(t, path, wirePath, a.ID, "process", "", "", "superseded").CombinedOutput(); err != nil {
		t.Fatalf("old child replay: %v %s", err, output)
	}
	actual, err := wg.read()
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 0 {
		t.Fatal("old child revived revoked peer")
	}
}

func TestCompletedOperationsReconcileRuntimeRestartWithoutResurrection(t *testing.T) {
	s, _, path := newTestStore(t)
	enroll(t, s, "d1")
	wg := newFake()
	a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	process(t, s, a, wg)
	delete(wg.peers, testKey(1))
	restarted := reopen(t, path)
	if err := restarted.Reconcile(context.Background(), "d1", wg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wg.state(), map[string][]string{testKey(1): {"10.66.0.30/32"}}) {
		t.Fatal("done apply failed runtime restart")
	}
	r := submit(t, s, command("d1", "revoke", 1, 0, "", "revoke"))
	process(t, s, r, wg)
	// Synthetic old .conf recovery resurrects the key after a previously done revoke.
	wg.peers[testKey(1)] = []string{"10.66.0.30/32"}
	wg.peers[testKey(90000)] = []string{"10.66.0.11/32"}
	if err := restarted.Reconcile(context.Background(), "d1", wg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wg.state(), map[string][]string{testKey(90000): {"10.66.0.11/32"}}) {
		t.Fatal("restart resurrected auth or erased RDP")
	}
}

func TestOverdueGrantGateKeepsTombstoneBudgetAcrossSupersede(t *testing.T) {
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	wg := newFake()
	a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	process(t, s, a, wg)
	b := submit(t, s, command("d1", "apply", 1, 2, "10.66.0.30/32", "rotate"))
	s.opts.Now = func() time.Time { return testNow.Add(29 * time.Second) }
	c := submit(t, s, command("d1", "apply", 2, 3, "10.66.0.30/32", "supersede"))
	if err := s.Process(context.Background(), b.ID, wg); !errors.Is(err, ErrSuperseded) {
		t.Fatal(err)
	}
	s.opts.Now = func() time.Time { return testNow.Add(31 * time.Second) }
	if n, err := s.RefreshDeadlines(context.Background()); err != nil || n != 1 {
		t.Fatalf("superseding lost old tombstone overdue signal: %d %v", n, err)
	}
	if _, err := s.Submit(context.Background(), command("d1", "apply", 3, 4, "10.66.0.30/32", "budget-reset")); !errors.Is(err, ErrOverdue) {
		t.Fatalf("old tombstone deadline reset: %v", err)
	}
	op, _ := s.Operation(context.Background(), c.ID)
	if op.State != "degraded" || op.LastError != "deadline_exceeded" {
		t.Fatalf("overdue not persistent: %+v", op)
	}
	r := submit(t, s, command("d1", "revoke", 3, 0, "", "revoke-even-overdue"))
	process(t, s, r, wg)
	if len(wg.state()) != 0 {
		t.Fatal("overdue gate blocked revoke")
	}
}

func TestRefreshDeadlineSignalAndBlockedInitialGrant(t *testing.T) {
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	s.opts.Now = func() time.Time { return testNow.Add(30 * time.Second) }
	n, err := s.RefreshDeadlines(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("deadline signal: %d %v", n, err)
	}
	n, err = s.RefreshDeadlines(context.Background())
	if err != nil || n != 0 {
		t.Fatal("deadline signal not idempotent")
	}
	if _, err := s.Submit(context.Background(), command("d1", "apply", 1, 2, "10.66.0.30/32", "second")); !errors.Is(err, ErrOverdue) {
		t.Fatal(err)
	}
	wg := newFake()
	process(t, s, a, wg)
	submit(t, s, command("d1", "apply", 1, 2, "10.66.0.30/32", "second"))
}

func TestVerifiedLatestGenerationDoesNotRetainOldPendingGrantGate(t *testing.T) {
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	wg := newFake()
	a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first-not-run"))
	b := submit(t, s, command("d1", "apply", 1, 2, "10.66.0.30/32", "replace"))
	if err := s.Reconcile(context.Background(), "d1", wg); err != nil {
		t.Fatal(err)
	}
	old, _ := s.Operation(context.Background(), a.ID)
	if old.State != "superseded" {
		t.Fatalf("old job remains current: %+v", old)
	}
	s.opts.Now = func() time.Time { return testNow.Add(31 * time.Second) }
	if n, err := s.RefreshDeadlines(context.Background()); err != nil || n != 0 {
		t.Fatalf("verified history flagged overdue: %d %v", n, err)
	}
	c := submit(t, s, command("d1", "apply", 2, 3, "10.66.0.30/32", "next-legitimate-grant"))
	process(t, s, c, wg)
	if !reflect.DeepEqual(wg.state(), map[string][]string{testKey(3): {"10.66.0.30/32"}}) {
		t.Fatal("old pending gate blocked verified latest grant")
	}
	if err := s.Process(context.Background(), b.ID, wg); !errors.Is(err, ErrSuperseded) {
		t.Fatal(err)
	}
}

func TestHistoryQuotaCannotBlockRevocation(t *testing.T) {
	s, db, _ := newTestStore(t)
	enroll(t, s, "d1")
	wg := newFake()
	a := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "first"))
	process(t, s, a, wg)
	// Oversized imported/synthetic history: grants reject, revocation still reads
	// every retained removal obligation rather than truncating the old-key set.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4096; i++ {
		_, err = tx.Exec(`INSERT INTO deviceauth_bindings(public_key,device_id,address,managed_by,role,created_generation,revoked_generation) VALUES(?,'d1','10.66.0.30/32',?,'customer',1,2)`, testKey(i+100), s.opts.Policy.ManagedBy)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), command("d1", "apply", 1, 9000, "10.66.0.30/32", "over-quota")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("grant quota: %v", err)
	}
	r := submit(t, s, command("d1", "revoke", 1, 0, "", "revoke"))
	process(t, s, r, wg)
	if len(wg.state()) != 0 {
		t.Fatal("history quota blocked revoke")
	}
}
