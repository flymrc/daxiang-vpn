//go:build integration && with_gvisor

package deviceapi

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zongheng-vpn/hub/internal/deviceauth"
	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/proxygate"
)

// This controller proves Service ordering only. Product reverse admission and
// UDS loss/lease expiry require the separate actual Linux process fixture.
type startupUnitController struct {
	policy   proxygate.Policy
	closed   atomic.Bool
	grants   atomic.Int64
	grantErr error
}

func (c *startupUnitController) Policy() proxygate.Policy { return c.policy.Clone() }
func (c *startupUnitController) GrantUntil(ctx context.Context, _ time.Time) error {
	if ctx.Err() != nil || c.closed.Load() {
		return proxygate.ErrClosed
	}
	c.grants.Add(1)
	return c.grantErr
}

func TestProfileServiceRefusesMissingGateBeforeReconciling(t *testing.T) {
	f := newRealWGFixture(t)
	e := &startupBarrierExecutor{Executor: f.executor, entered: make(chan struct{}), release: make(chan struct{})}
	s, address, _ := startupServiceFixture(t, f, e, true, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("refused profile served handler") }))
	s.gatePolicy = nil
	err := s.Run(context.Background())
	if err == nil || err.Error() != "device authority proxy gate configuration required" {
		t.Fatal("profile without gate accepted")
	}
	requireNoStartupListener(t, address)
	select {
	case <-e.entered:
		t.Fatal("unconfigured gate reached runtime reconciliation")
	default:
	}
}

func TestProfileServiceClosedACKFailureDoesNotReconcileOrListen(t *testing.T) {
	f := newRealWGFixture(t)
	e := &startupBarrierExecutor{Executor: f.executor, entered: make(chan struct{}), release: make(chan struct{})}
	s, address, _ := startupServiceFixture(t, f, e, true, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unacknowledged gate served handler") }))
	s.gateDial = func(context.Context, string, proxygate.Policy) (proxyController, error) {
		return nil, errors.New("SYNTHETIC_INTERNAL_SECRET")
	}
	err := s.Run(context.Background())
	if err == nil || err.Error() != "device authority proxy gate closed acknowledgement failed" {
		t.Fatal("closed ACK failure leaked diagnostic or succeeded")
	}
	requireNoStartupListener(t, address)
	select {
	case <-e.entered:
		t.Fatal("unacknowledged gate reached runtime reconciliation")
	default:
	}
}

func TestProfileServiceGrantACKFailureClosesBeforeTLS(t *testing.T) {
	f := newRealWGFixture(t)
	customer := newRealWGNode(t, newRealWGKey(t), "10.250.0.2/32", nil)
	defer customer.close()
	f.enrollAndApply(t, customer)
	f.tick()
	e := &startupBarrierExecutor{Executor: f.executor, entered: make(chan struct{}), release: make(chan struct{})}
	close(e.release)
	s, address, _ := startupServiceFixture(t, f, e, true, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unacknowledged grant served handler") }))
	control := &startupUnitController{policy: s.gatePolicy.Clone(), grantErr: errors.New("SYNTHETIC_ACK_SECRET")}
	s.gateDial = func(context.Context, string, proxygate.Policy) (proxyController, error) { return control, nil }
	err := s.Run(context.Background())
	if err == nil || err.Error() != "device authority initial proxy convergence failed" {
		t.Fatal("grant ACK failure leaked diagnostic or succeeded")
	}
	if control.grants.Load() != 1 || !control.closed.Load() {
		t.Fatal("unknown grant did not close control before return")
	}
	requireNoStartupListener(t, address)
}

func TestProfileServiceCancellationClosesBeforeBlockedSnapshotReturns(t *testing.T) {
	f := newRealWGFixture(t)
	customer := newRealWGNode(t, newRealWGKey(t), "10.250.0.2/32", nil)
	defer customer.close()
	f.enrollAndApply(t, customer)
	e := &startupBarrierExecutor{Executor: f.executor, entered: make(chan struct{}), release: make(chan struct{})}
	s, address, _ := startupServiceFixture(t, f, e, true, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("cancelled startup served handler") }))
	control := &startupUnitController{policy: s.gatePolicy.Clone()}
	s.gateDial = func(context.Context, string, proxygate.Policy) (proxyController, error) { return control, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.Run(ctx) }()
	select {
	case <-e.entered:
	case <-ctx.Done():
		t.Fatal("held real snapshot not reached")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled startup succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled startup did not reap")
	}
	if !control.closed.Load() || control.grants.Load() != 0 {
		t.Fatal("cancelled startup left control or granted")
	}
	requireNoStartupListener(t, address)
}
func (c *startupUnitController) Closed(context.Context) error { return nil }
func (c *startupUnitController) Close() error                 { c.closed.Store(true); return nil }

type startupBarrierExecutor struct {
	deviceauth.Executor
	once    sync.Once
	entered chan struct{}
	release chan struct{}
	fail    bool
}

func (e *startupBarrierExecutor) Snapshot(ctx context.Context, f deviceauth.Fence) ([]deviceauth.Peer, error) {
	wait := false
	e.once.Do(func() { wait = true; close(e.entered) })
	if wait {
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if e.fail {
			return nil, errors.New("SYNTHETIC_INTERNAL_SECRET_AND_PATH")
		}
	}
	return e.Executor.Snapshot(ctx, f)
}

func startupServiceFixture(t *testing.T, f *realWGFixture, e deviceauth.Executor, profileOn bool, h http.Handler) (*Service, string, *http.Client) {
	t.Helper()
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := seed.Client()
	certificate := seed.TLS.Certificates[0]
	seed.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	ln.Close()
	scheduler, err := deviceauth.NewScheduler(f.store, e)
	if err != nil {
		t.Fatal(err)
	}
	c := Config{}
	if profileOn {
		c.ProxyProfilePath = "owned-synthetic-profile"
	}
	s := &Service{Store: f.store, DB: f.db, Scheduler: scheduler, HTTP: &http.Server{Addr: address, Handler: h, ReadHeaderTimeout: time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}}}, config: c}
	if profileOn {
		policy := realWGFixturePolicy(f)
		policyDigest, err := deviceauth.CanonicalPolicyDigest(policy)
		if err != nil {
			t.Fatal(err)
		}
		profileDigest, err := dc.ProxyRouteProfileDigest(realWGProfile(f))
		if err != nil {
			t.Fatal(err)
		}
		gate := proxygate.Policy{Version: 1, Epoch: realWGFixtureEpoch, Interface: "ownedfixture0", ManagedBy: realWGFixtureManager, PolicySHA256: policyDigest, ProfileSHA256: profileDigest, Listener: realWGFixtureProxy, ManagedSources: policy.AddressPools, RetainedSources: []string{"10.250.0.3/32", "10.250.0.7/32"}}
		if err := ValidateProxyGatePolicy(policy, realWGProfile(f), "ownedfixture0", gate); err != nil {
			t.Fatal(err)
		}
		s.gatePolicy = &gate
		controller := &startupUnitController{policy: gate}
		s.gateDial = func(context.Context, string, proxygate.Policy) (proxyController, error) { return controller, nil }
	}
	return s, address, client
}
func requireNoStartupListener(t *testing.T, address string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 150*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("TLS service listened before initial reconciliation")
	}
}
func awaitStartupTLS(t *testing.T, client *http.Client, address string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		r, _ := http.NewRequestWithContext(ctx, "GET", "https://"+address+"/owned-ready-check", nil)
		resp, err := client.Do(r)
		cancel()
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("owned TLS listener did not start")
}

func TestProfileServiceInitialFailureDoesNotListenOrExposeRawError(t *testing.T) {
	f := newRealWGFixture(t)
	customer := newRealWGNode(t, newRealWGKey(t), "10.250.0.2/32", nil)
	defer customer.close()
	customer.routeTo(t, f.executor.hub)
	f.enrollAndApply(t, customer)
	e := &startupBarrierExecutor{Executor: f.executor, entered: make(chan struct{}), release: make(chan struct{}), fail: true}
	var handlers atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handlers.Add(1); w.WriteHeader(204) })
	s, address, _ := startupServiceFixture(t, f, e, true, h)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.Run(ctx) }()
	select {
	case <-e.entered:
	case <-ctx.Done():
		t.Fatal("initial real reconciliation not invoked")
	}
	requireNoStartupListener(t, address)
	close(e.release)
	select {
	case err := <-result:
		if err == nil || err.Error() != "device authority initial reconciliation failed" || strings.Contains(err.Error(), "SYNTHETIC") {
			t.Fatal("startup failure diagnostic leaked or returned success")
		}
	case <-ctx.Done():
		t.Fatal("failed initial reconciliation did not return")
	}
	requireNoStartupListener(t, address)
	if handlers.Load() != 0 {
		t.Fatal("failed initial gate served an HTTP handler")
	}
}

func TestProfileServiceReconcilesRealStaleSourceBeforeTLSReady(t *testing.T) {
	f := newRealWGFixture(t)
	customer := newRealWGNode(t, newRealWGKey(t), "10.250.0.2/32", nil)
	defer customer.close()
	customer.routeTo(t, f.executor.hub)
	f.enrollAndApply(t, customer)
	f.tick()
	f.requireMarker(customer, "startup-before-revoke")
	_, err := f.store.Submit(context.Background(), deviceauth.Command{Actor: deviceauth.Actor{ID: "owned-actor", OwnerID: "owned-owner"}, DeviceID: "owned-customer", Action: "revoke", IdempotencyKey: "owned-service-revoke", ExpectedGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	f.tick()
	f.requireAbsent(customer.key.publicBase64())
	f.reopenAuthority()
	f.restoreOldRuntime(t, customer)
	peers, err := readRealWGPeers(f.executor.hub.device)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := peers[customer.key.publicBase64()]; !present {
		t.Fatal("stale source fixture did not revive actual peer")
	}
	handler, err := NewServerWithProfile(f.store, realWGProfile(f), "ownedfixture0")
	if err != nil {
		t.Fatal(err)
	}
	var seen atomic.Int64
	var staleAtReady atomic.Bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		f.executor.mu.Lock()
		peers, err := readRealWGPeers(f.executor.hub.device)
		f.executor.mu.Unlock()
		if _, present := peers[customer.key.publicBase64()]; err != nil || present {
			staleAtReady.Store(true)
		}
		handler.ServeHTTP(w, r)
	})
	e := &startupBarrierExecutor{Executor: f.executor, entered: make(chan struct{}), release: make(chan struct{})}
	s, address, client := startupServiceFixture(t, f, e, true, h)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	result := make(chan error, 1)
	go func() { result <- s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-result:
		case <-time.After(7 * time.Second):
			t.Error("owned service failed shutdown")
		}
	}()
	select {
	case <-e.entered:
	case <-ctx.Done():
		t.Fatal("initial actual WG snapshot missing")
	}
	requireNoStartupListener(t, address)
	if seen.Load() != 0 {
		t.Fatal("initial held gate served HTTP")
	}
	close(e.release)
	awaitStartupTLS(t, client, address)
	if seen.Load() == 0 || staleAtReady.Load() {
		t.Fatal("TLS ready before stale customer peer removed")
	}
	f.requireAbsent(customer.key.publicBase64())
	f.requireBlocked(customer, "startup-after-reconcile")
	f.requireMarker(f.protected, "startup-protected-still-authorized")
}

func TestProfileOffServicePreservesExistingListenerOrdering(t *testing.T) {
	f := newRealWGFixture(t)
	customer := newRealWGNode(t, newRealWGKey(t), "10.250.0.2/32", nil)
	defer customer.close()
	f.enrollAndApply(t, customer)
	e := &startupBarrierExecutor{Executor: f.executor, entered: make(chan struct{}), release: make(chan struct{}), fail: true}
	s, address, client := startupServiceFixture(t, f, e, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	result := make(chan error, 1)
	go func() { result <- s.Run(ctx) }()
	defer func() {
		cancel()
		close(e.release)
		select {
		case <-result:
		case <-time.After(7 * time.Second):
			t.Error("owned service failed shutdown")
		}
	}()
	select {
	case <-e.entered:
	case <-ctx.Done():
		t.Fatal("background reconcile not started")
	}
	awaitStartupTLS(t, client, address)
}
