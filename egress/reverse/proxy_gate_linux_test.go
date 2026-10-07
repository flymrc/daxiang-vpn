//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zongheng-vpn/shared/proxygate"
)

func startAdmissionControl(t *testing.T, gate *proxygate.Gate, policy proxygate.Policy) (*proxygate.Controller, string) {
	t.Helper()
	dir := admissionControlDir(t)
	path := filepath.Join(dir, "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	server, err := proxygate.ListenControl(ctx, path, gate)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = server.Close() })
	controller, err := proxygate.DialControl(context.Background(), path, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	if err := controller.Grant(context.Background(), proxygate.MaxLease); err != nil {
		t.Fatal(err)
	}
	return controller, path
}

func admissionControlDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "zhpg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestProxyGateLinuxClosesEstablishedOrdinaryAndStripedOwnedFlows(t *testing.T) {
	for _, striped := range []bool{false, true} {
		for _, closure := range []string{"closed", "eof", "expiry"} {
			t.Run(fmt.Sprintf("striped_%v_%s", striped, closure), func(t *testing.T) {
				manager, gate, policy, proxy := newAdmissionProxy(t)
				target := newAdmissionEchoTarget(t)
				protected := admissionConnect(t, proxy, "127.0.0.3", target.listener.Addr().String(), false)
				defer protected.Close()
				unknown := admissionConnect(t, proxy, "127.0.0.7", target.listener.Addr().String(), false)
				defer unknown.Close()
				controller, _ := startAdmissionControl(t, gate, policy)
				managed := admissionConnect(t, proxy, "127.0.0.2", target.listener.Addr().String(), striped)
				defer managed.Close()
				admissionEcho(t, managed, "managed-owned-marker")
				if target.active.Load() != 3 {
					t.Fatalf("active target flows=%d", target.active.Load())
				}
				switch closure {
				case "closed":
					if err := controller.Closed(context.Background()); err != nil {
						t.Fatal(err)
					}
				case "eof":
					_ = controller.Close()
				case "expiry":
					if err := controller.Grant(context.Background(), 200*time.Millisecond); err != nil {
						t.Fatal(err)
					}
				}
				assertAdmissionClosed(t, managed)
				waitAdmission(t, "managed upstream target was not closed", func() bool { return target.active.Load() == 2 })
				admissionEcho(t, protected, "protected-still-owned")
				admissionEcho(t, unknown, "unknown-still-owned")
				if _, err := gate.AcquireSource("127.0.0.2:1"); !errors.Is(err, proxygate.ErrClosed) {
					t.Fatalf("new managed admission after closure err=%v", err)
				}
				if manager.sessionCount() != 1 {
					t.Fatal("closing managed traffic removed the unrelated egress session")
				}
			})
		}
	}
}

func TestProxyGateLinuxClosesFetchTargetOnControllerEOF(t *testing.T) {
	_, gate, policy, proxy := newAdmissionProxy(t)
	var active atomic.Int64
	payload := strings.Repeat("owned-fetch-marker", 256)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		_, _ = io.WriteString(w, payload)
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	}))
	defer target.Close()
	controller, _ := startAdmissionControl(t, gate, policy)
	transport := &http.Transport{DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}, Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	response, err := client.Get("http://" + proxy + "/fetch?url=" + url.QueryEscape(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fetch status=%d", response.StatusCode)
	}
	marker := make([]byte, len("owned-fetch-marker"))
	if _, err := io.ReadFull(response.Body, marker); err != nil || string(marker) != "owned-fetch-marker" {
		t.Fatalf("fetch marker=%q err=%v", marker, err)
	}
	if active.Load() != 1 {
		t.Fatal("owned fetch target was not active")
	}
	_ = controller.Close()
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, int64(len(payload)+1)))
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("managed fetch client reached its own timeout instead of closing")
	}
	waitAdmission(t, "fetch upstream HTTP target survived stream EOF", func() bool { return active.Load() == 0 })
}

func TestProxyGateLinuxCancelsPendingOpenWithoutDroppingEgress(t *testing.T) {
	manager, gate, policy, proxy := newAdmissionProxy(t)
	controller, _ := startAdmissionControl(t, gate, policy)
	entered := make(chan struct{})
	manager.mu.Lock()
	manager.sessions = nil
	manager.sessionStats = nil
	manager.mu.Unlock()
	session := &admissionTestSession{open: func(ctx context.Context) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	manager.set(session)
	done := make(chan struct{})
	go func() {
		defer close(done)
		dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}, Timeout: time.Second}
		conn, err := dialer.Dial("tcp", proxy)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.WriteString(conn, "CONNECT 127.0.0.1:9 HTTP/1.1\r\nHost: 127.0.0.1:9\r\n\r\n")
		_, _ = io.Copy(io.Discard, conn)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("OpenStream was not entered")
	}
	if err := controller.Closed(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pending managed request did not end")
	}
	waitAdmission(t, "pending slot did not release", func() bool {
		manager.activeProxyMu.Lock()
		defer manager.activeProxyMu.Unlock()
		return manager.activeProxyConns == 0
	})
	if manager.sessionCount() != 1 || session.opens.Load() != 1 {
		t.Fatal("pending managed cancellation changed the underlying egress session")
	}
}

func TestProxyGateLinuxLateOpenedStreamCannotDispatchAfterClosedACK(t *testing.T) {
	manager, gate, policy, proxy := newAdmissionProxy(t)
	controller, _ := startAdmissionControl(t, gate, policy)
	entered := make(chan struct{})
	returnStream := make(chan struct{})
	commandRead := make(chan error, 1)
	manager.mu.Lock()
	manager.sessions = nil
	manager.sessionStats = nil
	manager.mu.Unlock()
	session := &admissionTestSession{open: func(context.Context) (net.Conn, error) {
		client, peer := net.Pipe()
		close(entered)
		<-returnStream
		go func() {
			defer peer.Close()
			var one [1]byte
			_, err := peer.Read(one[:])
			commandRead <- err
		}()
		return client, nil
	}}
	manager.set(session)
	done := make(chan struct{})
	go func() {
		defer close(done)
		dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}, Timeout: time.Second}
		conn, err := dialer.Dial("tcp", proxy)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.WriteString(conn, "CONNECT 127.0.0.1:9 HTTP/1.1\r\nHost: 127.0.0.1:9\r\n\r\n")
		_, _ = io.Copy(io.Discard, conn)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("late OpenStream was not entered")
	}
	closed := make(chan error, 1)
	go func() { closed <- controller.Closed(context.Background()) }()
	waitAdmission(t, "scope did not close while OpenStream was pending", func() bool {
		probe, err := gate.AcquireSource("127.0.0.2:1")
		if probe != nil {
			probe.Release()
		}
		return errors.Is(err, proxygate.ErrClosed)
	})
	select {
	case err := <-closed:
		close(returnStream)
		t.Fatalf("closed proof preceded the reserved late stream: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	// Deliberately violate the tunnel's cancellation convention. The gate must
	// retain the pre-Open reservation and withhold ACK until this late stream
	// has attached and physically closed without sending a command.
	close(returnStream)
	select {
	case err := <-commandRead:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("a command reached the late stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("late stream was not immediately closed")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("closed proof did not follow the late stream's physical closure")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("late request did not finish")
	}
	if manager.sessionCount() != 1 {
		t.Fatal("late managed stream removal changed the unrelated egress session")
	}
}

func TestProxyGateLinuxRuntimeBindsProtectedPolicyToActualListener(t *testing.T) {
	dir := admissionControlDir(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	policy := proxyTestPolicy(listener.Addr().String())
	_ = listener.Close()
	raw, _ := json.Marshal(policy)
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	opts := serverOptions{Transport: "tcp", Proxy: policy.Listener, ProxyGatePolicyFile: path, ProxyGateControlSocket: filepath.Join(dir, "control.sock")}
	manager := &sessionManager{}
	runtime, err := prepareProxyGate(opts, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.listener.Addr().String() != policy.Listener || manager.proxyGate == nil {
		t.Fatal("dedicated product listener did not use the immutable policy endpoint")
	}
	if _, err := manager.proxyGate.AcquireSource("127.0.0.2:1"); !errors.Is(err, proxygate.ErrClosed) {
		t.Fatal("runtime admitted managed sources before the controller handshake")
	}
	opts.Proxy = "127.0.0.1:18082"
	if _, err := prepareProxyGate(opts, &sessionManager{}); !errors.Is(err, proxygate.ErrPolicy) {
		t.Fatalf("listener-policy mismatch err=%v", err)
	}
}

func TestProxyGateLinuxStartupRejectsQUICBeforeCreatingResources(t *testing.T) {
	dir := admissionControlDir(t)
	policyPath := filepath.Join(dir, "absent-policy.json")
	controlPath := filepath.Join(dir, "not-created.sock")
	certPath := filepath.Join(dir, "not-created-cert.pem")
	keyPath := filepath.Join(dir, "not-created-key.pem")
	for _, transportArgs := range [][]string{nil, {"--transport", "quic"}} {
		args := []string{
			"--token", "owned-startup-test", "--proxy", "invalid-listener-before-bind", "--listen", "invalid-tunnel-before-bind",
			"--proxy-gate-policy-file", policyPath, "--proxy-gate-control-socket", controlPath,
			"--tls-cert-file", certPath, "--tls-key-file", keyPath,
		}
		args = append(args, transportArgs...)
		if err := runServer(args); err == nil || err.Error() != "proxy_gate_unsupported_transport" {
			t.Fatalf("QUIC startup %v err=%v", transportArgs, err)
		}
		for _, path := range []string{policyPath, controlPath, certPath, keyPath} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected startup created an unexpected resource %s: %v", path, err)
			}
		}
	}
}

func TestProxyGateLinuxRealTunnelBenchRoutePreservesScopedDenialAfterGrant(t *testing.T) {
	manager, gate, policy, proxy := newAdmissionProxy(t)
	manager.mu.Lock()
	counter := &admissionCountingSession{tunnelSession: manager.sessions[0]}
	manager.sessions = nil
	manager.sessionStats = nil
	manager.mu.Unlock()
	manager.set(counter)
	request := func(source, path string) (int, string) {
		t.Helper()
		transport := &http.Transport{DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}, Timeout: time.Second}).DialContext}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
		response, err := client.Get("http://" + proxy + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 32768))
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(body)
	}
	bench := "/debug/tunnel-bench?bytes=16&streams=1"
	if status, body := request("127.0.0.2", bench); status != http.StatusForbidden || strings.TrimSpace(body) != "proxy_gate_managed_diagnostic_forbidden" {
		t.Fatalf("closed managed route status=%d body=%q", status, body)
	}
	startAdmissionControl(t, gate, policy)
	if status, body := request("127.0.0.2", bench); status != http.StatusForbidden || strings.TrimSpace(body) != "proxy_gate_managed_diagnostic_forbidden" {
		t.Fatalf("granted managed route status=%d body=%q", status, body)
	}
	if status, _ := request("127.0.0.2", "/debug/session-health"); status != http.StatusOK || counter.opens.Load() != 0 {
		t.Fatal("managed diagnostics dispatched a stream or changed read-only health behavior")
	}
	for _, source := range []string{"127.0.0.3", "127.0.0.7"} {
		status, body := request(source, bench)
		var report tunnelBenchReport
		if json.Unmarshal([]byte(body), &report) != nil || status != http.StatusOK || !report.OK || report.BytesRead != 16 {
			t.Fatalf("retained benchmark status=%d body=%q", status, body)
		}
	}
	if counter.opens.Load() != 2 {
		t.Fatalf("benchmark stream count=%d; want only two retained requests", counter.opens.Load())
	}
}

type admissionHeldFINTransport struct {
	net.Conn
	enabled atomic.Bool
	entered chan struct{}
	finish  chan struct{}
	first   sync.Once
	drained sync.Once
}

func (c *admissionHeldFINTransport) Write(packet []byte) (int, error) {
	// This stalls a real yamux FIN header in its physical send loop after the
	// managed CONNECT has already relayed a target marker. It does not replace
	// Stream.Close with a fake blocking implementation.
	if c.enabled.Load() && len(packet) >= 12 && binary.BigEndian.Uint16(packet[2:4])&4 != 0 {
		c.first.Do(func() { close(c.entered) })
		<-c.finish
	}
	return c.Conn.Write(packet)
}
func (c *admissionHeldFINTransport) release() { c.drained.Do(func() { close(c.finish) }) }
func (c *admissionHeldFINTransport) Close() error {
	c.release()
	return c.Conn.Close()
}

func TestProxyGateLinuxActualYamuxStalledFINCannotProduceClosedACK(t *testing.T) {
	held := &admissionHeldFINTransport{entered: make(chan struct{}), finish: make(chan struct{})}
	manager, gate, policy, proxy := newAdmissionProxyTransport(t, func(conn net.Conn) net.Conn {
		held.Conn = conn
		return held
	})
	defer func() {
		held.release()
		if err := gate.AwaitClosed(context.Background()); err != nil {
			t.Errorf("owned FIN stall left unfinished cleanup: %v", err)
		}
	}()
	target := newAdmissionEchoTarget(t)
	protected := admissionConnect(t, proxy, "127.0.0.3", target.listener.Addr().String(), false)
	defer protected.Close()
	admissionEcho(t, protected, "protected-before-owned-stall")
	controller, controlPath := startAdmissionControl(t, gate, policy)
	managed := admissionConnect(t, proxy, "127.0.0.2", target.listener.Addr().String(), false)
	defer managed.Close()
	admissionEcho(t, managed, "managed-before-owned-stall")
	held.enabled.Store(true)
	closed := make(chan error, 1)
	started := time.Now()
	go func() { closed <- controller.Closed(context.Background()) }()
	select {
	case <-held.entered:
	case <-time.After(time.Second):
		t.Fatal("the actual yamux FIN did not enter the owned congestion hook")
	}
	if _, err := gate.AcquireSource("127.0.0.2:1"); !errors.Is(err, proxygate.ErrClosed) {
		t.Fatalf("pending cleanup accepted a managed source: %v", err)
	}
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("closed ACK preceded the congested physical FIN completion")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("controller close exceeded its finite command budget")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("controller close did not return within its bounded timeout")
	}
	assertAdmissionClosed(t, managed)
	if target.active.Load() != 2 {
		t.Fatalf("fixture did not retain the physical target until FIN delivery: active=%d", target.active.Load())
	}
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		next, err := proxygate.DialControl(ctx, controlPath, policy)
		cancel()
		if err == nil {
			_ = next.Close()
			t.Fatal("a new controller received a false closed proof during quarantine")
		}
	}
	retained, err := gate.AcquireSource("127.0.0.3:1")
	if err != nil {
		t.Fatal("pending cleanup closed the retained source scope")
	}
	retained.Release()
	if manager.sessionCount() != 1 {
		t.Fatal("managed FIN congestion actively closed the shared egress session")
	}
	held.release()
	if err := gate.AwaitClosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitAdmission(t, "physical managed target survived released FIN", func() bool { return target.active.Load() == 1 })
	admissionEcho(t, protected, "protected-after-owned-stall")
	if _, err := gate.AcquireSource("127.0.0.2:1"); !errors.Is(err, proxygate.ErrClosed) {
		t.Fatal("draining the transport implicitly reopened the managed scope")
	}
}

func TestProxyGateLinuxLateActualYamuxAttachQuarantinesFINWithoutBlockingHandler(t *testing.T) {
	held := &admissionHeldFINTransport{entered: make(chan struct{}), finish: make(chan struct{})}
	manager, gate, policy, proxy := newAdmissionProxyTransport(t, func(conn net.Conn) net.Conn {
		held.Conn = conn
		return held
	})
	target := newAdmissionEchoTarget(t)
	protected := admissionConnect(t, proxy, "127.0.0.3", target.listener.Addr().String(), false)
	defer protected.Close()
	admissionEcho(t, protected, "protected-before-late-open")
	controller, path := startAdmissionControl(t, gate, policy)
	manager.mu.Lock()
	original := manager.sessions[0]
	manager.sessions = nil
	manager.sessionStats = nil
	manager.mu.Unlock()
	entered := make(chan struct{})
	returnStream := make(chan struct{})
	var returnOnce sync.Once
	releaseOpen := func() { returnOnce.Do(func() { close(returnStream) }) }
	defer func() {
		releaseOpen()
		held.release()
		_ = gate.Close()
		if err := gate.AwaitClosed(context.Background()); err != nil {
			t.Errorf("late yamux creation left owned cleanup unfinished: %v", err)
		}
	}()
	manager.set(&admissionTestSession{open: func(context.Context) (net.Conn, error) {
		// The real upstream stream is created before cancellation, but this
		// deliberately hostile adapter returns it after the scope closes.
		stream, err := original.OpenStream(context.Background())
		close(entered)
		<-returnStream
		return stream, err
	}})
	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}, Timeout: time.Second}
	client, err := dialer.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, _ = fmt.Fprintf(client, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target.listener.Addr(), target.listener.Addr())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("real yamux OpenStream was not held before late return")
	}
	closed := make(chan error, 1)
	go func() { closed <- controller.Closed(context.Background()) }()
	waitAdmission(t, "scope did not close with a reserved creation pending", func() bool {
		probe, err := gate.AcquireSource("127.0.0.2:1")
		if probe != nil {
			probe.Release()
		}
		return errors.Is(err, proxygate.ErrClosed)
	})
	held.enabled.Store(true)
	releaseOpen()
	select {
	case <-held.entered:
	case <-time.After(time.Second):
		t.Fatal("late actual yamux stream was not closed by the quarantine worker")
	}
	waitAdmission(t, "late Attach blocked the request handler on physical FIN", func() bool {
		manager.activeProxyMu.Lock()
		defer manager.activeProxyMu.Unlock()
		return manager.activeProxyConns == 1 // only the pre-existing protected CONNECT
	})
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("reserved late FIN produced a false closed ACK")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late FIN exceeded the control command budget")
	}
	if target.accepted.Load() != 1 || target.active.Load() != 1 {
		t.Fatal("the late stream dispatched a CONNECT command or closed the protected target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	next, err := proxygate.DialControl(ctx, path, policy)
	cancel()
	if err == nil {
		_ = next.Close()
		t.Fatal("a new controller bypassed the pending late FIN quarantine")
	}
	if original.IsClosed() {
		t.Fatal("late managed cleanup closed the shared physical egress session")
	}
	held.release()
	if err := gate.AwaitClosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	admissionEcho(t, protected, "protected-after-late-open")
	if target.accepted.Load() != 1 {
		t.Fatal("a command reached the late upstream after cleanup")
	}
}
