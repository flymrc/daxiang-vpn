package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"zongheng-vpn/shared/proxygate"
)

func proxyTestPolicy(listener string) proxygate.Policy {
	var uid uint32
	if os.Geteuid() >= 0 {
		uid = uint32(os.Geteuid())
	}
	return proxygate.Policy{
		Version: proxygate.Version, Epoch: "owned-epoch", Interface: "wg-v2", ManagedBy: "deviceapi",
		PolicySHA256: strings.Repeat("1", 64), ProfileSHA256: strings.Repeat("2", 64),
		Listener: listener, ControllerUID: uid, ManagedSources: []string{"127.0.0.2/32", "127.0.0.4/32"},
		RetainedSources: []string{"127.0.0.3/32"},
	}
}

func TestProxyGateDefaultOffAndPartialConfigurationRejected(t *testing.T) {
	manager := &sessionManager{}
	runtime, err := prepareProxyGate(defaultServerOptions(), manager)
	if err != nil || runtime != nil || manager.proxyGate != nil {
		t.Fatalf("default gate runtime=%v err=%v", runtime, err)
	}
	for _, opts := range []serverOptions{
		{ProxyGatePolicyFile: "not-read"},
		{ProxyGateControlSocket: "not-opened"},
	} {
		if _, err := prepareProxyGate(opts, manager); err == nil {
			t.Fatal("partial gate configuration accepted")
		}
	}
}

func TestProxyGateRejectsUnsupportedTransportBeforePolicyOrListener(t *testing.T) {
	for _, entry := range []struct{ name, transport string }{
		{"default_quic", defaultServerOptions().Transport}, {"explicit_quic", "quic"}, {"missing_transport", ""}, {"unknown_transport", "udp"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			manager := &sessionManager{}
			opts := serverOptions{
				Transport: entry.transport, Proxy: "invalid-listener-before-bind",
				ProxyGatePolicyFile: "not-read", ProxyGateControlSocket: "not-created",
			}
			runtime, err := prepareProxyGate(opts, manager)
			if err == nil || err.Error() != "proxy_gate_unsupported_transport" || runtime != nil || manager.proxyGate != nil {
				t.Fatalf("runtime=%v gate=%v err=%v", runtime, manager.proxyGate, err)
			}
		})
	}
	for _, transport := range []string{"tcp", secureTCPTransport} {
		opts := serverOptions{Transport: transport, ProxyGatePolicyFile: "not-read", ProxyGateControlSocket: "not-created"}
		if _, err := prepareProxyGate(opts, &sessionManager{}); err == nil || err.Error() == "proxy_gate_unsupported_transport" {
			t.Fatalf("supported transport %q did not reach protected policy loading: %v", transport, err)
		}
	}
}

func TestProxyGateRejectsBeforeSlotsOrUpstreamForAllTargetPaths(t *testing.T) {
	gate, err := proxygate.New(proxyTestPolicy("127.0.0.1:18081"))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	session := &admissionTestSession{handler: func(conn net.Conn) { _ = conn.Close() }}
	manager := &sessionManager{proxyGate: gate, fetch: true, resolve: "client", maxProxyConns: 1}
	manager.set(session)
	for _, entry := range []struct{ name, method, target, striped string }{
		{"ordinary", http.MethodConnect, "127.0.0.1:9", ""},
		{"striped", http.MethodConnect, "127.0.0.1:9", "2"},
		{"fetch", http.MethodGet, "http://proxy/fetch?url=http%3A%2F%2F127.0.0.1%3A9", ""},
	} {
		t.Run(entry.name, func(t *testing.T) {
			req := httptest.NewRequest(entry.method, entry.target, nil)
			req.RemoteAddr = "127.0.0.2:41111"
			req.Header.Set(stripedConnectHeader, entry.striped)
			response := httptest.NewRecorder()
			manager.handleProxy(response, req)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if session.opens.Load() != 0 || manager.activeProxyConns != 0 || manager.activeProxyPeak != 0 {
				t.Fatalf("closed gate consumed resources: opens=%d active=%d peak=%d", session.opens.Load(), manager.activeProxyConns, manager.activeProxyPeak)
			}
		})
	}
	manager.allowedProxyNets, err = parseCIDRs("allowed", []string{"127.0.0.3/32"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodConnect, "127.0.0.1:9", nil)
	req.RemoteAddr = "127.0.0.2:41111"
	response := httptest.NewRecorder()
	manager.handleProxy(response, req)
	if response.Code != http.StatusForbidden || session.opens.Load() != 0 {
		t.Fatal("the original CIDR restriction was bypassed")
	}
}

func TestProxyGateManagedTunnelBenchDeniedWithoutUpstreamResources(t *testing.T) {
	gate, err := proxygate.New(proxyTestPolicy("127.0.0.1:18081"))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	session := &admissionTestSession{handler: func(conn net.Conn) { _ = conn.Close() }}
	manager := &sessionManager{proxyGate: gate}
	manager.set(session)
	request := httptest.NewRequest(http.MethodGet, "/debug/tunnel-bench?bytes=16&streams=8", nil)
	request.RemoteAddr = "127.0.0.2:41111"
	response := httptest.NewRecorder()
	manager.handleProxy(response, request)
	if response.Code != http.StatusForbidden || strings.TrimSpace(response.Body.String()) != "proxy_gate_managed_diagnostic_forbidden" {
		t.Fatalf("managed benchmark status=%d body=%q", response.Code, response.Body.String())
	}
	if session.opens.Load() != 0 || manager.activeProxyConns != 0 || manager.activeProxyPeak != 0 {
		t.Fatal("the managed benchmark dispatched an upstream or consumed a proxy slot")
	}
}

type admissionTestSession struct {
	opens   atomic.Int64
	handler func(net.Conn)
	open    func(context.Context) (net.Conn, error)
}

type admissionCountingSession struct {
	tunnelSession
	opens atomic.Int64
}

func (s *admissionCountingSession) OpenStream(ctx context.Context) (net.Conn, error) {
	s.opens.Add(1)
	return s.tunnelSession.OpenStream(ctx)
}

func (s *admissionTestSession) OpenStream(ctx context.Context) (net.Conn, error) {
	s.opens.Add(1)
	if s.open != nil {
		return s.open(ctx)
	}
	client, server := net.Pipe()
	go s.handler(server)
	return client, nil
}
func (s *admissionTestSession) Close() error         { return nil }
func (s *admissionTestSession) IsClosed() bool       { return false }
func (s *admissionTestSession) RemoteAddr() net.Addr { return dummyAddr("owned-admission-egress") }

type admissionEchoTarget struct {
	listener net.Listener
	conns    sync.Map
	accepted atomic.Int64
	active   atomic.Int64
}

func newAdmissionEchoTarget(t *testing.T) *admissionEchoTarget {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := &admissionEchoTarget{listener: listener}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			target.accepted.Add(1)
			target.active.Add(1)
			target.conns.Store(conn, struct{}{})
			go func() {
				defer target.active.Add(-1)
				defer target.conns.Delete(conn)
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		target.conns.Range(func(conn, _ any) bool { _ = conn.(net.Conn).Close(); return true })
	})
	return target
}

func newAdmissionProxy(t *testing.T) (*sessionManager, *proxygate.Gate, proxygate.Policy, string) {
	return newAdmissionProxyTransport(t, nil)
}

func newAdmissionProxyTransport(t *testing.T, wrap func(net.Conn) net.Conn) (*sessionManager, *proxygate.Gate, proxygate.Policy, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	policy := proxyTestPolicy(listener.Addr().String())
	gate, err := proxygate.New(policy)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	manager := &sessionManager{resolve: "client", proxyGate: gate, fetch: true}
	upstream, egress := net.Pipe()
	if wrap != nil {
		upstream = wrap(upstream)
	}
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = false
	cfg.ConnectionWriteTimeout = 30 * time.Second
	hubSession, err := yamux.Server(upstream, cfg)
	if err != nil {
		t.Fatal(err)
	}
	egressSession, err := yamux.Client(egress, cfg)
	if err != nil {
		t.Fatal(err)
	}
	manager.set(&yamuxSession{session: hubSession})
	go func() {
		for {
			stream, err := egressSession.AcceptStream()
			if err != nil {
				return
			}
			go handleClientStream(stream, clientOptions{AddressFamily: "auto"})
		}
	}()
	server := &http.Server{Handler: http.HandlerFunc(manager.handleProxy), ConnContext: proxyGateConnContext, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = gate.Close()
		_ = server.Close()
		_ = listener.Close()
		_ = hubSession.Close()
		_ = egressSession.Close()
	})
	return manager, gate, policy, listener.Addr().String()
}

func admissionConnect(t *testing.T, proxy, source, target string, striped bool) net.Conn {
	t.Helper()
	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}, Timeout: time.Second}
	conn, err := dialer.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	header := ""
	if striped {
		header = stripedConnectHeader + ": 2\r\n"
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", target, target, header); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		conn.Close()
		t.Fatalf("CONNECT response=%v err=%v", response, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return &bufferedConn{Conn: conn, reader: reader}
}

func admissionEcho(t *testing.T, conn net.Conn, marker string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(conn, marker); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(marker))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != marker {
		t.Fatalf("owned echo=%q err=%v", got, err)
	}
	_ = conn.SetDeadline(time.Time{})
}

func waitAdmission(t *testing.T, name string, test func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if test() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(name)
}

func TestProxyGatePreservesRetainedAndUnknownOwnedTargetConnections(t *testing.T) {
	_, _, _, proxy := newAdmissionProxy(t)
	target := newAdmissionEchoTarget(t)
	for _, source := range []string{"127.0.0.3", "127.0.0.7"} {
		conn := admissionConnect(t, proxy, source, target.listener.Addr().String(), false)
		admissionEcho(t, conn, "outside-gate-marker")
		_ = conn.Close()
	}
	if target.accepted.Load() != 2 {
		t.Fatalf("retained target accepts=%d", target.accepted.Load())
	}
}

type admissionHeldCloseConn struct {
	net.Conn
	entered chan struct{}
	finish  chan struct{}
	closes  atomic.Int64
}

func (c *admissionHeldCloseConn) Close() error {
	if c.closes.Add(1) == 1 {
		close(c.entered)
		<-c.finish
	}
	return nil
}

func TestProxyGateTrackedCloseWaitsForConcurrentTransportClosure(t *testing.T) {
	underlying := &admissionHeldCloseConn{entered: make(chan struct{}), finish: make(chan struct{})}
	var released atomic.Int64
	conn := &trackedConn{Conn: underlying, release: func() { released.Add(1) }}
	first := make(chan struct{})
	go func() { _ = conn.Close(); close(first) }()
	<-underlying.entered
	second := make(chan struct{})
	go func() { _ = conn.Close(); close(second) }()
	select {
	case <-second:
		t.Fatal("concurrent Close reported completion before the transport closed")
	case <-time.After(20 * time.Millisecond):
	}
	if released.Load() != 0 {
		t.Fatal("the connection released its slot before its close completed")
	}
	close(underlying.finish)
	for _, done := range []chan struct{}{first, second} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("transport completion did not release concurrent closers")
		}
	}
	if underlying.closes.Load() != 1 || released.Load() != 1 {
		t.Fatal("transport or slot closure was repeated")
	}
}

func assertAdmissionClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var one [1]byte
	_, err := conn.Read(one[:])
	if err == nil {
		t.Fatal("managed client remained usable")
	}
	var timed net.Error
	if errors.As(err, &timed) && timed.Timeout() {
		t.Fatal("managed client timed out instead of being closed")
	}
}
