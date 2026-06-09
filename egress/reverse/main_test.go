package main

import (
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
	if server.Transport != "quic" {
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
	if client.Connections != 4 {
		t.Fatalf("client connections = %d", client.Connections)
	}
	if got := normalizeFingerprint(client.ServerCertSHA256); len(got) != 64 {
		t.Fatalf("client server cert pin length = %d", len(got))
	}
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
