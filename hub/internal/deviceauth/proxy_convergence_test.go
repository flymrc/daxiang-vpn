package deviceauth

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func proxyConvergenceStore(t *testing.T, now func() time.Time) *Store {
	t.Helper()
	p := Policy{Epoch: "proxy-epoch", ManagedBy: "proxy-owner", AddressPools: []string{"10.250.0.2/32", "10.250.0.4/32"}, Protected: []Protection{{PublicKey: testKey(90000)}, {Prefix: "10.250.0.1/32"}, {Prefix: "10.250.0.3/32"}}}
	s, err := New(context.Background(), openTestDB(t, filepath.Join(t.TempDir(), "authority.sqlite")), Options{Policy: p, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func proxyConvergenceEnroll(t *testing.T, s *Store, until time.Time) Operation {
	t.Helper()
	if err := s.Enroll(context.Background(), Enrollment{DeviceID: "proxy-customer", OwnerID: "owner", Role: "customer", ValidUntil: until}); err != nil {
		t.Fatal(err)
	}
	return submit(t, s, command("proxy-customer", "apply", 0, 7, "10.250.0.2/32", "first"))
}

type convergenceSnapshot struct {
	Executor
	after func()
}

func (e convergenceSnapshot) Snapshot(ctx context.Context, f Fence) ([]Peer, error) {
	peers, err := e.Executor.Snapshot(ctx, f)
	if e.after != nil {
		e.after()
	}
	return peers, err
}

func TestProxyConvergenceTickNilAfterSnapshotExpiryDoesNotGrant(t *testing.T) {
	clock := testNow
	s := proxyConvergenceStore(t, func() time.Time { return clock })
	op := proxyConvergenceEnroll(t, s, testNow.Add(time.Second))
	e := newFake()
	process(t, s, op, e)
	scheduler, _ := NewScheduler(s, convergenceSnapshot{Executor: e, after: func() { clock = testNow.Add(2 * time.Second) }})
	// SweepExpiry saw a valid device, but the external Snapshot crossed expiry.
	// Process creates a new removal generation and Tick deliberately ignores
	// ErrExpired. The final proof must observe the newer pending generation.
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, err := s.Device(context.Background(), "proxy-customer")
	if err != nil || d.Generation != 2 || d.AppliedGeneration != 1 || d.State != "expired" {
		t.Fatal("snapshot expiry did not create an unapplied removal")
	}
	if _, present := e.state()[testKey(7)]; !present {
		t.Fatal("counterexample requires the old runtime peer still present")
	}
	grants := 0
	err = s.WithProxyConvergence(context.Background(), e, s.opts.Policy.AddressPools, func(context.Context, ProxyConvergenceProof) error { grants++; return nil })
	if !errors.Is(err, ErrVerification) || grants != 0 {
		t.Fatal("Tick nil falsely granted an expired pending generation")
	}
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.WithProxyConvergence(context.Background(), e, s.opts.Policy.AddressPools, func(_ context.Context, p ProxyConvergenceProof) error {
		grants++
		if p.ActiveDevices != 0 {
			t.Fatal("revoked customer remains active")
		}
		return nil
	}); err != nil || grants != 1 {
		t.Fatal("drained latest removal failed convergence")
	}
}

func TestProxyConvergencePreservesOutsideUnknownAndRejectsInsideAlias(t *testing.T) {
	s := proxyConvergenceStore(t, func() time.Time { return testNow })
	op := proxyConvergenceEnroll(t, s, testNow.Add(time.Hour))
	e := newFake()
	process(t, s, op, e)
	e.peers[testKey(90000)] = []string{"10.250.0.3/32"}
	e.peers[testKey(99)] = []string{"10.250.0.7/32"}
	grants := 0
	grant := func(context.Context, ProxyConvergenceProof) error { grants++; return nil }
	if err := s.WithProxyConvergence(context.Background(), e, s.opts.Policy.AddressPools, grant); err != nil || grants != 1 {
		t.Fatal("outside protected/unknown peers incorrectly adopted")
	}
	e.peers[testKey(100)] = []string{"10.250.0.4/32"}
	if err := s.WithProxyConvergence(context.Background(), e, s.opts.Policy.AddressPools, grant); !errors.Is(err, ErrProtected) || grants != 1 {
		t.Fatal("inside unknown source was granted")
	}
	if len(e.state()) != 4 || len(e.actions) != 1 {
		t.Fatal("proof mutated unrelated runtime")
	}
}

func TestProxyConvergenceRejectsIncompleteAndProtectedScopes(t *testing.T) {
	s := proxyConvergenceStore(t, func() time.Time { return testNow })
	grants := 0
	for _, scope := range [][]string{{"10.250.0.2/32"}, {"10.250.0.0/24"}, {"10.250.0.2/32", "10.250.0.2/32"}, {"10.250.0.2/32", "10.250.0.3/32"}} {
		if err := s.WithProxyConvergence(context.Background(), newFake(), scope, func(context.Context, ProxyConvergenceProof) error { grants++; return nil }); err == nil {
			t.Fatal("mismatched immutable source scope accepted")
		}
	}
	if grants != 0 {
		t.Fatal("rejected source scopes reached grant")
	}
}

func TestProxyConvergenceHoldsWriterFenceThroughGrantAck(t *testing.T) {
	s := proxyConvergenceStore(t, func() time.Time { return testNow })
	op := proxyConvergenceEnroll(t, s, testNow.Add(time.Hour))
	e := newFake()
	process(t, s, op, e)
	entered := make(chan struct{})
	release := make(chan struct{})
	proofDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		proofDone <- s.WithProxyConvergence(ctx, e, s.opts.Policy.AddressPools, func(context.Context, ProxyConvergenceProof) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("proof did not enter fenced grant")
	}
	mutation := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, err := s.Submit(ctx, command("proxy-customer", "revoke", 1, 0, "", "concurrent-revoke"))
		mutation <- err
	}()
	<-started
	select {
	case <-mutation:
		t.Fatal("authority changed before grant ACK released its fence")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-proofDone; err != nil {
		t.Fatal(err)
	}
	if err := <-mutation; err != nil {
		t.Fatal(err)
	}
	grants := 0
	if err := s.WithProxyConvergence(ctx, e, s.opts.Policy.AddressPools, func(context.Context, ProxyConvergenceProof) error { grants++; return nil }); !errors.Is(err, ErrVerification) || grants != 0 {
		t.Fatal("renewal ignored intervening revoke")
	}
}

func TestProxyConvergenceExpiryDuringGrantCannotCertify(t *testing.T) {
	clock := testNow
	s := proxyConvergenceStore(t, func() time.Time { return clock })
	op := proxyConvergenceEnroll(t, s, testNow.Add(time.Second))
	e := newFake()
	process(t, s, op, e)
	err := s.WithProxyConvergence(context.Background(), e, s.opts.Policy.AddressPools, func(_ context.Context, p ProxyConvergenceProof) error {
		if !p.EarliestValidUntil.Equal(testNow.Add(time.Second)) {
			t.Fatal("grant lost earliest expiry")
		}
		clock = testNow.Add(2 * time.Second)
		return nil
	})
	if !errors.Is(err, ErrExpired) {
		t.Fatal("grant callback crossing expiry was certified")
	}
}

func TestProxyConvergenceCancelledSnapshotDoesNotGrant(t *testing.T) {
	s := proxyConvergenceStore(t, func() time.Time { return testNow })
	op := proxyConvergenceEnroll(t, s, testNow.Add(time.Hour))
	e := newFake()
	process(t, s, op, e)
	ctx, cancel := context.WithCancel(context.Background())
	var grants atomic.Int64
	err := s.WithProxyConvergence(ctx, convergenceSnapshot{Executor: e, after: cancel}, s.opts.Policy.AddressPools, func(context.Context, ProxyConvergenceProof) error { grants.Add(1); return nil })
	if err == nil || grants.Load() != 0 {
		t.Fatal("cancelled actual snapshot granted")
	}
}
