package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestLoadReverseConfigExamples(t *testing.T) {
	serverCfg, err := loadReverseConfig("../../docs/20-operations/configs/egress/hub-reverse-server.yaml.example")
	if err != nil {
		t.Fatalf("load server example: %v", err)
	}
	server := serverCfg.Server.withDefaults(defaultServerOptions())
	if server.Listen != "0.0.0.0:39093" {
		t.Fatalf("server listen = %q", server.Listen)
	}
	if server.Transport != "tcp" {
		t.Fatalf("server transport = %q", server.Transport)
	}

	clientCfg, err := loadReverseConfig("../../docs/20-operations/configs/egress/android-reverse-client.yaml.example")
	if err != nil {
		t.Fatalf("load client example: %v", err)
	}
	client := clientCfg.Client.withDefaults(defaultClientOptions())
	if client.Server != "36.50.84.68:39093" {
		t.Fatalf("client server = %q", client.Server)
	}
	if client.Reconnect != 3*time.Second {
		t.Fatalf("client reconnect = %s", client.Reconnect)
	}
	if client.Transport != "tcp" {
		t.Fatalf("client transport = %q", client.Transport)
	}
	if client.Connections != 2 {
		t.Fatalf("client connections = %d", client.Connections)
	}
	if client.AddressFamily != "auto" {
		t.Fatalf("client address family = %q", client.AddressFamily)
	}
	if got := normalizeFingerprint(client.ServerCertSHA256); len(got) != 64 {
		t.Fatalf("client server cert pin length = %d", len(got))
	}
}

func TestAcquireProxySlotLimitsPerClient(t *testing.T) {
	manager := &sessionManager{
		maxProxyConns:     4,
		maxProxyConnsPeer: 2,
		activeProxyByPeer: map[string]int{},
	}

	release1, ok, reason := manager.acquireProxySlot("10.66.0.30:10001")
	if !ok {
		t.Fatalf("first acquire failed: %s", reason)
	}
	release2, ok, reason := manager.acquireProxySlot("10.66.0.30:10002")
	if !ok {
		t.Fatalf("second acquire failed: %s", reason)
	}
	if _, ok, reason := manager.acquireProxySlot("10.66.0.30:10003"); ok || reason != "proxy busy for client" {
		t.Fatalf("third per-client acquire ok=%v reason=%q", ok, reason)
	}

	release1()
	release3, ok, reason := manager.acquireProxySlot("10.66.0.30:10003")
	if !ok {
		t.Fatalf("acquire after release failed: %s", reason)
	}
	release2()
	release3()
}

func TestAcquireProxySlotLimitsGlobal(t *testing.T) {
	manager := &sessionManager{
		maxProxyConns:     2,
		activeProxyByPeer: map[string]int{},
	}

	release1, ok, reason := manager.acquireProxySlot("10.66.0.30:10001")
	if !ok {
		t.Fatalf("first acquire failed: %s", reason)
	}
	release2, ok, reason := manager.acquireProxySlot("10.66.0.31:10001")
	if !ok {
		t.Fatalf("second acquire failed: %s", reason)
	}
	if _, ok, reason := manager.acquireProxySlot("10.66.0.32:10001"); ok || reason != "proxy busy" {
		t.Fatalf("third global acquire ok=%v reason=%q", ok, reason)
	}

	release1()
	release2()
}

func TestQUICClientRequiresServerCertPin(t *testing.T) {
	err := run([]string{
		"client",
		"--server", "127.0.0.1:1",
		"--token", "test-token",
		"--transport", "quic",
		"--reconnect", "1ms",
	})
	if err == nil {
		t.Fatal("expected missing pin error")
	}
	if err.Error() != "--server-cert-sha256 is required for quic transport" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	got := normalizeFingerprint("SHA256:AA:bb cc")
	if got != "aabbcc" {
		t.Fatalf("normalized fingerprint = %q", got)
	}
}

func TestOrderIPsAddressFamily(t *testing.T) {
	ips := []net.IPAddr{
		{IP: net.ParseIP("2001:db8::1")},
		{IP: net.ParseIP("192.0.2.10")},
		{IP: net.ParseIP("2001:db8::2")},
	}
	if got := orderIPs(ips, "auto"); got[0].IP.String() != "192.0.2.10" {
		t.Fatalf("auto first IP = %s", got[0].IP)
	}
	if got := orderIPs(ips, "ipv4"); got[0].IP.String() != "192.0.2.10" {
		t.Fatalf("ipv4 first IP = %s", got[0].IP)
	}
	if got := orderIPs(ips, "ipv6"); got[0].IP.String() != "2001:db8::1" {
		t.Fatalf("ipv6 first IP = %s", got[0].IP)
	}
}

func TestProxyAllowedCIDRs(t *testing.T) {
	_, network, err := net.ParseCIDR("10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	manager := &sessionManager{allowedProxyNets: []*net.IPNet{network}}
	if !manager.proxyAllowed("10.66.0.30:51234") {
		t.Fatal("expected WireGuard client to be allowed")
	}
	if manager.proxyAllowed("192.0.2.10:51234") {
		t.Fatal("expected non-WireGuard client to be denied")
	}
}

func TestOpenCommandDropsStaleSession(t *testing.T) {
	oldTimeout := reverseCommandTimeout
	reverseCommandTimeout = 20 * time.Millisecond
	defer func() { reverseCommandTimeout = oldTimeout }()

	stale := &fakeTunnelSession{
		remote: dummyAddr("stale"),
		handler: func(conn net.Conn) {
			defer conn.Close()
			_, _ = bufio.NewReader(conn).ReadString('\n')
			time.Sleep(200 * time.Millisecond)
		},
	}
	healthy := &fakeTunnelSession{
		remote: dummyAddr("healthy"),
		handler: func(conn net.Conn) {
			defer conn.Close()
			_, _ = bufio.NewReader(conn).ReadString('\n')
			_, _ = io.WriteString(conn, "OK\n")
		},
	}
	manager := &sessionManager{sessions: []tunnelSession{stale, healthy}}

	stream, _, status, err := manager.openCommand("CONNECT example.com:443")
	if err != nil {
		t.Fatalf("open command: %v", err)
	}
	defer stream.Close()
	if status != "OK" {
		t.Fatalf("status = %q", status)
	}
	if len(manager.sessions) != 1 || manager.sessions[0] != healthy {
		t.Fatalf("stale session was not removed: %#v", manager.sessions)
	}
}

type fakeTunnelSession struct {
	remote  net.Addr
	handler func(net.Conn)
}

func (s *fakeTunnelSession) OpenStream(context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	go s.handler(server)
	return client, nil
}

func (s *fakeTunnelSession) Close() error { return nil }

func (s *fakeTunnelSession) IsClosed() bool { return false }

func (s *fakeTunnelSession) RemoteAddr() net.Addr { return s.remote }

type dummyAddr string

func (a dummyAddr) Network() string { return "test" }

func (a dummyAddr) String() string { return string(a) }
