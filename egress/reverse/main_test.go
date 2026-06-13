package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if server.MaxProxyConns != 96 {
		t.Fatalf("server max proxy connections = %d", server.MaxProxyConns)
	}
	if server.MaxProxyConnsPeer != 48 {
		t.Fatalf("server max proxy per-client connections = %d", server.MaxProxyConnsPeer)
	}
	if server.ProxyIdleTimeout != 2*time.Minute {
		t.Fatalf("server proxy idle timeout = %s", server.ProxyIdleTimeout)
	}
	if server.V4OnlyDirect {
		t.Fatal("server v4_only_direct should be disabled in the example")
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
	if client.AddressFamily != "ipv6" {
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

func TestPipeBothReapsIdleConnections(t *testing.T) {
	clientConn, proxyClient := net.Pipe()
	reverseClient, reverseServer := net.Pipe()
	defer clientConn.Close()
	defer reverseServer.Close()

	done := make(chan struct{})
	go func() {
		pipeBoth(proxyClient, reverseClient, 20*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idle proxy connection was not reaped")
	}

	if _, err := clientConn.Write([]byte("x")); err == nil {
		t.Fatal("client side remained writable after idle reap")
	}
	if _, err := reverseServer.Write([]byte("x")); err == nil {
		t.Fatal("reverse side remained writable after idle reap")
	}
}

func resetDNSCache() {
	dnsCacheMu.Lock()
	defer dnsCacheMu.Unlock()
	dnsCache = map[string]dnsCacheEntry{}
}

func TestDNSCacheHitAndExpiry(t *testing.T) {
	resetDNSCache()
	now := time.Now()
	ips := []net.IPAddr{{IP: net.ParseIP("192.0.2.10")}, {IP: net.ParseIP("2001:db8::1")}}

	if _, ok := dnsCacheGet("example.com", now); ok {
		t.Fatal("unexpected hit on empty cache")
	}
	dnsCachePut("example.com", ips, now)
	got, ok := dnsCacheGet("example.com", now.Add(dnsCacheTTL-time.Second))
	if !ok || len(got) != 2 || got[0].IP.String() != "192.0.2.10" {
		t.Fatalf("cache hit = %v ok=%v", got, ok)
	}
	if _, ok := dnsCacheGet("example.com", now.Add(dnsCacheTTL+time.Second)); ok {
		t.Fatal("expired entry still served")
	}
	if _, ok := dnsCacheGet("other.example.com", now); ok {
		t.Fatal("hit for host never stored")
	}
}

func TestDNSCacheCapacityReset(t *testing.T) {
	resetDNSCache()
	now := time.Now()
	ips := []net.IPAddr{{IP: net.ParseIP("192.0.2.10")}}
	for i := 0; i < dnsCacheMaxEntries; i++ {
		dnsCachePut(fmt.Sprintf("host%d.example.com", i), ips, now)
	}
	dnsCachePut("overflow.example.com", ips, now)
	dnsCacheMu.Lock()
	size := len(dnsCache)
	dnsCacheMu.Unlock()
	if size != 1 {
		t.Fatalf("cache size after overflow = %d, want 1", size)
	}
	if _, ok := dnsCacheGet("overflow.example.com", now); !ok {
		t.Fatal("entry stored after reset not served")
	}
	resetDNSCache()
}

func TestLooksLikeTLSClientHello(t *testing.T) {
	hello := []byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x00, 0x00, 0x01, 0x00}
	if !looksLikeTLSClientHello(hello) {
		t.Fatal("client hello not recognized")
	}
	for name, payload := range map[string][]byte{
		"plain http":      []byte("GET / HTTP/1.1\r\n"),
		"too short":       {0x16, 0x03, 0x01},
		"not handshake":   {0x17, 0x03, 0x03, 0x00, 0x05, 0x01},
		"server hello":    {0x16, 0x03, 0x03, 0x00, 0x05, 0x02},
		"bad tls version": {0x16, 0x02, 0x01, 0x00, 0x05, 0x01},
	} {
		if looksLikeTLSClientHello(payload) {
			t.Fatalf("%s misdetected as client hello", name)
		}
	}
}

func relayTestHello() []byte {
	return append([]byte{0x16, 0x03, 0x01, 0x00, 0x06, 0x01}, []byte("hello")...)
}

func TestRelayHandshakeRetryReplaysClientHello(t *testing.T) {
	oldTimeout := handshakeFirstByteTimeout
	handshakeFirstByteTimeout = 40 * time.Millisecond
	defer func() { handshakeFirstByteTimeout = oldTimeout }()

	hello := relayTestHello()
	hubSide, clientSide := net.Pipe()
	defer hubSide.Close()

	// 第一条目标连接:吞掉 ClientHello 后永远不响应(模拟 F5 v4 黑洞)。
	deadNear, deadFar := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, deadFar) }()

	redials := 0
	redial := func() (net.Conn, error) {
		redials++
		near, far := net.Pipe()
		go func() {
			defer far.Close()
			got := make([]byte, len(hello))
			if _, err := io.ReadFull(far, got); err != nil {
				return
			}
			if !bytes.Equal(got, hello) {
				return // 重放内容不对就不回包,让测试在读响应处失败
			}
			_, _ = far.Write([]byte("SH"))
		}()
		return near, nil
	}

	done := make(chan struct{})
	go func() {
		relayWithHandshakeRetry(clientSide, deadNear, "example.com:443", redial)
		close(done)
	}()

	if _, err := hubSide.Write(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	_ = hubSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp := make([]byte, 2)
	if _, err := io.ReadFull(hubSide, resp); err != nil {
		t.Fatalf("read response after replay: %v", err)
	}
	if string(resp) != "SH" {
		t.Fatalf("response = %q", resp)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not finish")
	}
	if redials != 1 {
		t.Fatalf("redials = %d", redials)
	}
}

func TestRelayHandshakeNoRetryForNonTLS(t *testing.T) {
	oldTimeout := handshakeFirstByteTimeout
	handshakeFirstByteTimeout = 40 * time.Millisecond
	defer func() { handshakeFirstByteTimeout = oldTimeout }()

	hubSide, clientSide := net.Pipe()
	defer hubSide.Close()

	targetNear, targetFar := net.Pipe()
	go func() {
		buf := make([]byte, 1024)
		_, _ = targetFar.Read(buf)
		_ = targetFar.Close() // 收到明文请求后直接断开:不应触发重拨
	}()

	redials := 0
	redial := func() (net.Conn, error) {
		redials++
		return nil, errors.New("must not redial")
	}

	go func() { _, _ = hubSide.Write([]byte("GET / HTTP/1.1\r\n\r\n")) }()
	relayWithHandshakeRetry(clientSide, targetNear, "example.com:80", redial)
	if redials != 0 {
		t.Fatalf("redials = %d", redials)
	}
}

func TestRelayHandshakeNoRetryAfterServerBytes(t *testing.T) {
	oldTimeout := handshakeFirstByteTimeout
	handshakeFirstByteTimeout = 40 * time.Millisecond
	defer func() { handshakeFirstByteTimeout = oldTimeout }()

	hello := relayTestHello()
	hubSide, clientSide := net.Pipe()
	defer hubSide.Close()

	targetNear, targetFar := net.Pipe()
	go func() {
		got := make([]byte, len(hello))
		_, _ = io.ReadFull(targetFar, got)
		_, _ = targetFar.Write([]byte{0x16}) // 回一个字节后断开
		_ = targetFar.Close()
	}()

	redials := 0
	redial := func() (net.Conn, error) {
		redials++
		return nil, errors.New("must not redial")
	}

	done := make(chan struct{})
	go func() {
		relayWithHandshakeRetry(clientSide, targetNear, "example.com:443", redial)
		close(done)
	}()

	if _, err := hubSide.Write(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	_ = hubSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp := make([]byte, 1)
	if _, err := io.ReadFull(hubSide, resp); err != nil {
		t.Fatalf("read server byte: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not finish")
	}
	if redials != 0 {
		t.Fatalf("redials = %d", redials)
	}
}

func TestRelayHandshakeFallsBackAfterMaxDials(t *testing.T) {
	oldTimeout := handshakeFirstByteTimeout
	handshakeFirstByteTimeout = 40 * time.Millisecond
	defer func() { handshakeFirstByteTimeout = oldTimeout }()

	hello := relayTestHello()
	hubSide, clientSide := net.Pipe()
	defer hubSide.Close()

	deadNear, deadFar := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, deadFar) }()

	redials := 0
	redial := func() (net.Conn, error) {
		redials++
		near, far := net.Pipe()
		if redials == handshakeMaxDials-1 {
			// 最后一次拨号:超过看门狗死线后才响应,验证额度用完会退回
			// 阻塞中继而不是掐掉连接。
			go func() {
				defer far.Close()
				got := make([]byte, len(hello))
				if _, err := io.ReadFull(far, got); err != nil {
					return
				}
				time.Sleep(6 * handshakeFirstByteTimeout)
				_, _ = far.Write([]byte("LATE"))
			}()
		} else {
			go func() { _, _ = io.Copy(io.Discard, far) }()
		}
		return near, nil
	}

	done := make(chan struct{})
	go func() {
		relayWithHandshakeRetry(clientSide, deadNear, "example.com:443", redial)
		close(done)
	}()

	if _, err := hubSide.Write(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	_ = hubSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp := make([]byte, 4)
	if _, err := io.ReadFull(hubSide, resp); err != nil {
		t.Fatalf("read late response: %v", err)
	}
	if string(resp) != "LATE" {
		t.Fatalf("response = %q", resp)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not finish")
	}
	if redials != handshakeMaxDials-1 {
		t.Fatalf("redials = %d, want %d", redials, handshakeMaxDials-1)
	}
}

func TestRelayHandshakeReplayLimitDisablesRetry(t *testing.T) {
	oldTimeout := handshakeFirstByteTimeout
	handshakeFirstByteTimeout = 40 * time.Millisecond
	defer func() { handshakeFirstByteTimeout = oldTimeout }()

	hubSide, clientSide := net.Pipe()

	deadNear, deadFar := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, deadFar) }()

	redials := 0
	redial := func() (net.Conn, error) {
		redials++
		return nil, errors.New("must not redial")
	}

	done := make(chan struct{})
	go func() {
		relayWithHandshakeRetry(clientSide, deadNear, "example.com:443", redial)
		close(done)
	}()

	if _, err := hubSide.Write(relayTestHello()); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	// 超过重放缓冲上限后,看门狗必须放弃重试退回阻塞中继。
	if _, err := hubSide.Write(make([]byte, handshakeReplayLimit)); err != nil {
		t.Fatalf("write oversized flight: %v", err)
	}
	time.Sleep(3 * handshakeFirstByteTimeout)
	_ = hubSide.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not finish")
	}
	if redials != 0 {
		t.Fatalf("redials = %d", redials)
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

func TestOpenCommandPrefersLowerRTTSession(t *testing.T) {
	slow := okTunnelSession("slow")
	fast := okTunnelSession("fast")
	manager := &sessionManager{
		sessions: []tunnelSession{slow, fast},
		sessionStats: map[tunnelSession]*sessionHealth{
			slow: {ewmaCommandRTT: 500 * time.Millisecond},
			fast: {ewmaCommandRTT: 20 * time.Millisecond},
		},
	}

	stream, _, status, err := manager.openCommand("CONNECT example.com:443")
	if err != nil {
		t.Fatalf("open command: %v", err)
	}
	defer stream.Close()
	if status != "OK" {
		t.Fatalf("status = %q", status)
	}
	if slow.opens != 0 || fast.opens != 1 {
		t.Fatalf("opens slow=%d fast=%d, want slow=0 fast=1", slow.opens, fast.opens)
	}
}

func TestOpenCommandPrefersIdleSessionOverLowerRTTBusySession(t *testing.T) {
	idle := okTunnelSession("idle")
	busy := okTunnelSession("busy")
	manager := &sessionManager{
		sessions: []tunnelSession{idle, busy},
		sessionStats: map[tunnelSession]*sessionHealth{
			idle: {ewmaCommandRTT: 500 * time.Millisecond},
			busy: {activeStreams: 1, ewmaCommandRTT: 20 * time.Millisecond},
		},
	}

	stream, _, status, err := manager.openCommand("CONNECT example.com:443")
	if err != nil {
		t.Fatalf("open command: %v", err)
	}
	defer stream.Close()
	if status != "OK" {
		t.Fatalf("status = %q", status)
	}
	if idle.opens != 1 || busy.opens != 0 {
		t.Fatalf("opens idle=%d busy=%d, want idle=1 busy=0", idle.opens, busy.opens)
	}
}

func TestSessionHealthEndpointReportsSchedulerState(t *testing.T) {
	left := okTunnelSession("left")
	right := okTunnelSession("right")
	manager := &sessionManager{
		sessions: []tunnelSession{left, right},
		sessionStats: map[tunnelSession]*sessionHealth{
			left: {
				activeStreams:       2,
				consecutiveFailures: 1,
				ewmaCommandRTT:      125 * time.Millisecond,
				lastFailure:         time.Now().Add(-5 * time.Second),
			},
			right: {
				ewmaCommandRTT: 25 * time.Millisecond,
			},
		},
		activeProxyConns: 3,
		activeProxyByPeer: map[string]int{
			"10.66.0.20": 3,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://10.66.0.1:18081/debug/session-health", nil)
	recorder := httptest.NewRecorder()
	manager.handleProxy(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	var report sessionHealthReport
	if err := json.NewDecoder(recorder.Body).Decode(&report); err != nil {
		t.Fatalf("decode session health: %v", err)
	}
	if report.GeneratedAt.IsZero() {
		t.Fatal("missing generated_at")
	}
	if report.SessionCount != 2 || len(report.Sessions) != 2 {
		t.Fatalf("sessions count=%d len=%d", report.SessionCount, len(report.Sessions))
	}
	if report.ActiveProxyConnections != 3 || report.ActiveProxyConnectionsByPeer["10.66.0.20"] != 3 {
		t.Fatalf("active proxy snapshot = %#v", report)
	}
	if report.Sessions[0].RemoteAddr != "left" || report.Sessions[0].ActiveStreams != 2 || report.Sessions[0].ConsecutiveFailures != 1 {
		t.Fatalf("left session snapshot = %#v", report.Sessions[0])
	}
	if report.Sessions[0].EWMACommandRTTMillis != 125 {
		t.Fatalf("left ewma rtt = %d", report.Sessions[0].EWMACommandRTTMillis)
	}
	if report.Sessions[0].LastFailureAgoMillis <= 0 {
		t.Fatalf("left last failure age = %d", report.Sessions[0].LastFailureAgoMillis)
	}
	if report.Sessions[1].RemoteAddr != "right" || report.Sessions[1].SchedulerScoreMillis >= report.Sessions[0].SchedulerScoreMillis {
		t.Fatalf("scheduler scores not ordered as expected: %#v", report.Sessions)
	}
}

func TestSplitTunnelBenchBytes(t *testing.T) {
	got := splitTunnelBenchBytes(10, 3)
	want := []int64{4, 3, 3}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("split[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestTunnelBenchEndpointAggregatesStreams(t *testing.T) {
	left := benchTunnelSession("left")
	right := benchTunnelSession("right")
	manager := &sessionManager{
		sessions: []tunnelSession{left, right},
	}

	req := httptest.NewRequest(http.MethodGet, "http://10.66.0.1:18081/debug/tunnel-bench?bytes=10000&streams=2", nil)
	recorder := httptest.NewRecorder()
	manager.handleProxy(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	var report tunnelBenchReport
	if err := json.NewDecoder(recorder.Body).Decode(&report); err != nil {
		t.Fatalf("decode tunnel bench: %v", err)
	}
	if !report.OK {
		t.Fatalf("bench failed: %#v", report)
	}
	if report.RequestedBytes != 10000 || report.BytesRead != 10000 || report.Streams != 2 {
		t.Fatalf("report totals = %#v", report)
	}
	if len(report.PerStream) != 2 {
		t.Fatalf("per_stream len = %d", len(report.PerStream))
	}
	for _, stream := range report.PerStream {
		if stream.RequestedBytes != 5000 || stream.BytesRead != 5000 || stream.Error != "" {
			t.Fatalf("stream result = %#v", stream)
		}
	}
	if left.opens+right.opens != 2 {
		t.Fatalf("opens left=%d right=%d, want total=2", left.opens, right.opens)
	}
}

func TestTunnelBenchEndpointRejectsBadParams(t *testing.T) {
	manager := &sessionManager{}
	tests := map[string]string{
		"bad bytes":  "http://10.66.0.1:18081/debug/tunnel-bench?bytes=abc",
		"too many":   fmt.Sprintf("http://10.66.0.1:18081/debug/tunnel-bench?bytes=%d", maxTunnelBenchBytes+1),
		"bad stream": "http://10.66.0.1:18081/debug/tunnel-bench?bytes=10&streams=0",
	}
	for name, target := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			recorder := httptest.NewRecorder()
			manager.handleProxy(recorder, req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func okTunnelSession(name string) *fakeTunnelSession {
	return &fakeTunnelSession{
		remote: dummyAddr(name),
		handler: func(conn net.Conn) {
			defer conn.Close()
			_, _ = bufio.NewReader(conn).ReadString('\n')
			_, _ = io.WriteString(conn, "OK\n")
		},
	}
}

func benchTunnelSession(name string) *fakeTunnelSession {
	return &fakeTunnelSession{
		remote: dummyAddr(name),
		handler: func(conn net.Conn) {
			defer conn.Close()
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil {
				return
			}
			rawBytes, ok := strings.CutPrefix(strings.TrimSpace(line), "BENCH ")
			if !ok {
				_, _ = io.WriteString(conn, "ERR invalid command\n")
				return
			}
			handleBenchStream(conn, rawBytes)
		},
	}
}

type fakeTunnelSession struct {
	remote  net.Addr
	handler func(net.Conn)
	opens   int
}

func (s *fakeTunnelSession) OpenStream(context.Context) (net.Conn, error) {
	s.opens++
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
