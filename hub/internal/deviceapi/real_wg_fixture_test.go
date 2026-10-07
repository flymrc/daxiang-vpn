//go:build integration && with_gvisor

package deviceapi

// These fixtures use real encrypted WireGuard UDP and gVisor TCP. They neither
// open a system TUN nor execute wg, change OS routes, or reach a remote target.
// IpcGet contains private material: parse it in memory and never print it.
import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sbwg "github.com/sagernet/sing-box/transport/wireguard"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/wireguard-go/conn"
	wg "github.com/sagernet/wireguard-go/device"

	"zongheng-vpn/hub/internal/deviceauth"
)

const realWGFixtureEpoch = "owned-real-wg-fixture-epoch"
const realWGFixtureManager = "owned-real-wg-fixture-executor"
const realWGFixtureProxy = "10.250.0.1:18080"

type realWGKey struct{ private, public []byte }

func newRealWGKey(t *testing.T) realWGKey {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("generate fixture WireGuard key")
	}
	return realWGKey{private: k.Bytes(), public: k.PublicKey().Bytes()}
}
func (k realWGKey) publicBase64() string { return base64.StdEncoding.EncodeToString(k.public) }

type realWGNode struct {
	stack    sbwg.Device
	device   *wg.Device
	key      realWGKey
	endpoint string
}

// wireguard-go's production default bind listens on all host interfaces.
// This fixture owns only an IPv4 loopback UDP socket and refuses any other
// endpoint. Encryption, handshake, replay protection and routing remain the
// actual WireGuard implementation; this adapter replaces only socket binding.
type realWGLoopbackBind struct {
	mu     sync.Mutex
	socket *net.UDPConn
}
type realWGLoopbackEndpoint struct{ address netip.AddrPort }

func (e *realWGLoopbackEndpoint) ClearSrc()           {}
func (e *realWGLoopbackEndpoint) SrcToString() string { return "127.0.0.1" }
func (e *realWGLoopbackEndpoint) DstToString() string { return e.address.String() }
func (e *realWGLoopbackEndpoint) DstToBytes() []byte  { b, _ := e.address.MarshalBinary(); return b }
func (e *realWGLoopbackEndpoint) DstIP() netip.Addr   { return e.address.Addr() }
func (e *realWGLoopbackEndpoint) SrcIP() netip.Addr   { return netip.MustParseAddr("127.0.0.1") }
func (b *realWGLoopbackBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.socket != nil {
		return nil, 0, conn.ErrBindAlreadyOpen
	}
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)})
	if err != nil {
		return nil, 0, err
	}
	b.socket = socket
	read := func(packets [][]byte, sizes []int, endpoints []conn.Endpoint) (int, error) {
		n, source, err := socket.ReadFromUDPAddrPort(packets[0])
		if err != nil {
			return 0, err
		}
		if source.Addr().Unmap() != netip.MustParseAddr("127.0.0.1") {
			return 0, errors.New("fixture non-loopback UDP refused")
		}
		sizes[0], endpoints[0] = n, &realWGLoopbackEndpoint{address: source}
		return 1, nil
	}
	return []conn.ReceiveFunc{read}, uint16(socket.LocalAddr().(*net.UDPAddr).Port), nil
}
func (b *realWGLoopbackBind) Close() error {
	b.mu.Lock()
	socket := b.socket
	b.socket = nil
	b.mu.Unlock()
	if socket != nil {
		return socket.Close()
	}
	return nil
}
func (b *realWGLoopbackBind) SetMark(mark uint32) error {
	if mark != 0 {
		return errors.New("fixture socket mark refused")
	}
	return nil
}
func (b *realWGLoopbackBind) BatchSize() int                                 { return 1 }
func (b *realWGLoopbackBind) SetReservedForEndpoint(netip.AddrPort, [3]byte) {}
func (b *realWGLoopbackBind) ParseEndpoint(value string) (conn.Endpoint, error) {
	address, err := netip.ParseAddrPort(value)
	if err != nil || address.Addr() != netip.MustParseAddr("127.0.0.1") || address.Port() == 0 {
		return nil, errors.New("fixture endpoint outside owned loopback")
	}
	return &realWGLoopbackEndpoint{address: address}, nil
}
func (b *realWGLoopbackBind) Send(packets [][]byte, endpoint conn.Endpoint, offset int) error {
	remote, ok := endpoint.(*realWGLoopbackEndpoint)
	if !ok || remote.address.Addr() != netip.MustParseAddr("127.0.0.1") {
		return conn.ErrWrongEndpointType
	}
	b.mu.Lock()
	socket := b.socket
	b.mu.Unlock()
	if socket == nil {
		return net.ErrClosed
	}
	for _, packet := range packets {
		if _, err := socket.WriteToUDPAddrPort(packet[offset:], remote.address); err != nil {
			return err
		}
	}
	return nil
}

func newRealWGNode(t *testing.T, key realWGKey, address string, handler tun.Handler) *realWGNode {
	t.Helper()
	stack, err := sbwg.NewDevice(sbwg.DeviceOptions{Context: context.Background(), Logger: logger.NOP(), System: false, MTU: 1408, Address: []netip.Prefix{netip.MustParsePrefix(address)}, Handler: handler, UDPTimeout: time.Second})
	if err != nil {
		t.Fatalf("create owned user-space WireGuard stack: %v", err)
	}
	if err = stack.Start(); err != nil {
		stack.Close()
		t.Fatalf("start owned stack: %v", err)
	}
	device := wg.NewDevice(context.Background(), stack, &realWGLoopbackBind{}, wg.NewLogger(wg.LogLevelSilent, ""), 2)
	stack.SetDevice(device)
	if err = device.IpcSet("private_key=" + hex.EncodeToString(key.private) + "\nlisten_port=0\n"); err != nil {
		device.Close()
		t.Fatal("configure owned WireGuard identity")
	}
	if err = device.Up(); err != nil {
		device.Close()
		t.Fatalf("open owned WireGuard UDP: %v", err)
	}
	raw, err := device.IpcGet()
	if err != nil {
		device.Close()
		t.Fatal("read owned WireGuard endpoint")
	}
	var port uint64
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "listen_port=") {
			port, err = strconv.ParseUint(strings.TrimPrefix(line, "listen_port="), 10, 16)
		}
	}
	if err != nil || port == 0 {
		device.Close()
		t.Fatal("owned UDP listener has no port")
	}
	return &realWGNode{stack: stack, device: device, key: key, endpoint: net.JoinHostPort("127.0.0.1", strconv.FormatUint(port, 10))}
}
func (n *realWGNode) close() { n.device.Close() }
func (n *realWGNode) routeTo(t *testing.T, hub *realWGNode) {
	t.Helper()
	if err := n.device.IpcSet("public_key=" + hex.EncodeToString(hub.key.public) + "\nendpoint=" + hub.endpoint + "\nreplace_allowed_ips=true\nallowed_ip=10.250.0.1/32\n"); err != nil {
		t.Fatal("configure owned tunnel route")
	}
}

type realWGPeerEvidence struct {
	deviceauth.Peer
	handshake, received, sent uint64
}

func readRealWGPeers(device *wg.Device) (map[string]realWGPeerEvidence, error) {
	raw, err := device.IpcGet()
	if err != nil {
		return nil, errors.New("fixture runtime snapshot unavailable")
	}
	peers := map[string]realWGPeerEvidence{}
	var current string
	for _, line := range strings.Split(raw, "\n") {
		field, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if field == "public_key" {
			key, err := hex.DecodeString(value)
			if err != nil || len(key) != 32 {
				return nil, errors.New("fixture runtime peer identity invalid")
			}
			current = base64.StdEncoding.EncodeToString(key)
			peers[current] = realWGPeerEvidence{Peer: deviceauth.Peer{PublicKey: current}}
			continue
		}
		if current == "" {
			continue
		}
		peer := peers[current]
		switch field {
		case "allowed_ip":
			peer.AllowedIPs = append(peer.AllowedIPs, value)
		case "last_handshake_time_sec":
			peer.handshake, err = strconv.ParseUint(value, 10, 64)
		case "rx_bytes":
			peer.received, err = strconv.ParseUint(value, 10, 64)
		case "tx_bytes":
			peer.sent, err = strconv.ParseUint(value, 10, 64)
		}
		if err != nil {
			return nil, errors.New("fixture runtime counters invalid")
		}
		peers[current] = peer
	}
	return peers, nil
}

// This executor reads and changes the real WireGuard device, never a peer map.
// The Store supplies its persisted generation/fence and serializes actions.
type realWGExecutor struct {
	mu  sync.Mutex
	hub *realWGNode
}

func (e *realWGExecutor) Snapshot(ctx context.Context, fence deviceauth.Fence) ([]deviceauth.Peer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fence.Epoch != realWGFixtureEpoch {
		return nil, deviceauth.ErrPolicy
	}
	peers, err := readRealWGPeers(e.hub.device)
	if err != nil {
		return nil, err
	}
	result := make([]deviceauth.Peer, 0, len(peers))
	for _, peer := range peers {
		result = append(result, peer.Peer)
	}
	return result, nil
}
func realWGPeerIPC(peer deviceauth.Peer) (string, error) {
	key, err := base64.StdEncoding.DecodeString(peer.PublicKey)
	if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != peer.PublicKey {
		return "", deviceauth.ErrInvalid
	}
	var command strings.Builder
	command.WriteString("public_key=" + hex.EncodeToString(key) + "\nreplace_allowed_ips=true\n")
	for _, address := range peer.AllowedIPs {
		prefix, err := netip.ParsePrefix(address)
		if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 || prefix.String() != address || !netip.MustParsePrefix("10.250.0.0/24").Contains(prefix.Addr()) {
			return "", deviceauth.ErrInvalid
		}
		command.WriteString("allowed_ip=" + address + "\n")
	}
	return command.String(), nil
}
func (e *realWGExecutor) Apply(ctx context.Context, fence deviceauth.Fence, peer deviceauth.Peer) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if fence.Epoch != realWGFixtureEpoch {
		return deviceauth.ErrPolicy
	}
	command, err := realWGPeerIPC(peer)
	if err != nil {
		return err
	}
	if e.hub.device.IpcSet(command) != nil {
		return errors.New("fixture real peer apply failed")
	}
	return nil
}
func (e *realWGExecutor) Remove(ctx context.Context, fence deviceauth.Fence, public string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if fence.Epoch != realWGFixtureEpoch {
		return deviceauth.ErrPolicy
	}
	key, err := base64.StdEncoding.DecodeString(public)
	if err != nil || len(key) != 32 {
		return deviceauth.ErrInvalid
	}
	if e.hub.device.IpcSet("public_key="+hex.EncodeToString(key)+"\nremove=true\n") != nil {
		return errors.New("fixture real peer remove failed")
	}
	return nil
}

type realWGProxy struct {
	allowedTarget string
	active        sync.Map
	accepted      atomic.Int64
	closed        atomic.Bool
}

func (p *realWGProxy) PrepareConnection(network string, source, destination M.Socksaddr, route tun.DirectRouteContext, timeout time.Duration) (tun.DirectRouteDestination, error) {
	if p.closed.Load() || network != N.NetworkTCP || destination.String() != realWGFixtureProxy {
		return nil, errors.New("owned fixture destination refused")
	}
	return nil, nil
}
func (p *realWGProxy) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	conn.Close()
	if onClose != nil {
		onClose(errors.New("fixture UDP application refused"))
	}
}
func (p *realWGProxy) NewConnectionEx(ctx context.Context, conn net.Conn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	if p.closed.Load() {
		conn.Close()
		return
	}
	p.active.Store(conn, struct{}{})
	defer p.active.Delete(conn)
	defer conn.Close()
	if onClose != nil {
		defer onClose(nil)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	request, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	defer request.Body.Close()
	if request.URL.Host != p.allowedTarget || (request.Method != http.MethodGet && request.Method != http.MethodConnect) {
		io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	p.accepted.Add(1)
	if request.Method == http.MethodConnect {
		upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", p.allowedTarget)
		if err != nil {
			return
		}
		defer upstream.Close()
		_ = upstream.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, conn); upstream.Close(); close(done) }()
		_, _ = io.Copy(conn, upstream)
		conn.Close()
		upstream.Close()
		<-done
		return
	}
	if request.URL.Scheme != "http" {
		return
	}
	request.RequestURI = ""
	request.Header.Del("Proxy-Connection")
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: time.Second}
	defer transport.CloseIdleConnections()
	response, err := transport.RoundTrip(request.WithContext(ctx))
	if err != nil {
		return
	}
	defer response.Body.Close()
	_ = response.Write(conn)
}
func (p *realWGProxy) close() {
	p.closed.Store(true)
	p.active.Range(func(k, _ any) bool { k.(net.Conn).Close(); return true })
}

type realWGFixture struct {
	t            *testing.T
	target       *httptest.Server
	marker       atomic.Int64
	proxy        *realWGProxy
	executor     *realWGExecutor
	protected    *realWGNode
	store        *deviceauth.Store
	db           *sql.DB
	databasePath string
	scheduler    *deviceauth.Scheduler
}

func newRealWGFixture(t *testing.T) *realWGFixture {
	t.Helper()
	f := &realWGFixture{t: t}
	f.target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/owned-marker/") {
			http.Error(w, "owned fixture target refused", http.StatusForbidden)
			return
		}
		f.marker.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "owned-wg-target:"+r.URL.Path)
	}))
	t.Cleanup(f.target.Close)
	targetURL, _ := url.Parse(f.target.URL)
	f.proxy = &realWGProxy{allowedTarget: targetURL.Host}
	t.Cleanup(f.proxy.close)
	hubKey := newRealWGKey(t)
	f.executor = &realWGExecutor{hub: newRealWGNode(t, hubKey, "10.250.0.1/24", f.proxy)}
	t.Cleanup(func() { f.executor.mu.Lock(); defer f.executor.mu.Unlock(); f.executor.hub.close() })
	f.protected = newRealWGNode(t, newRealWGKey(t), "10.250.0.3/32", nil)
	t.Cleanup(f.protected.close)
	f.protected.routeTo(t, f.executor.hub)
	if err := f.executor.Apply(context.Background(), deviceauth.Fence{Epoch: realWGFixtureEpoch}, deviceauth.Peer{PublicKey: f.protected.key.publicBase64(), AllowedIPs: []string{"10.250.0.3/32"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "owned-authority.sqlite")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	f.db, f.databasePath = db, path
	t.Cleanup(func() { f.db.Close() })
	f.store, err = deviceauth.New(context.Background(), db, deviceauth.Options{Policy: deviceauth.Policy{Epoch: realWGFixtureEpoch, ManagedBy: realWGFixtureManager, AddressPools: []string{"10.250.0.0/24"}, Protected: []deviceauth.Protection{{Prefix: "10.250.0.1/32"}, {Prefix: "10.250.0.3/32", PublicKey: f.protected.key.publicBase64()}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.scheduler, err = deviceauth.NewScheduler(f.store, f.executor)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *realWGFixture) reopenAuthority() {
	f.t.Helper()
	if f.db.Close() != nil {
		f.t.Fatal("close owned authority before restart")
	}
	db, err := sql.Open("sqlite", f.databasePath)
	if err != nil {
		f.t.Fatal("reopen owned persistent authority")
	}
	f.db = db
	f.store, err = deviceauth.New(context.Background(), db, deviceauth.Options{Policy: deviceauth.Policy{Epoch: realWGFixtureEpoch, ManagedBy: realWGFixtureManager, AddressPools: []string{"10.250.0.0/24"}, Protected: []deviceauth.Protection{{Prefix: "10.250.0.1/32"}, {Prefix: "10.250.0.3/32", PublicKey: f.protected.key.publicBase64()}}}})
	if err != nil {
		f.t.Fatal("restore owned persisted authority after restart")
	}
	f.scheduler, err = deviceauth.NewScheduler(f.store, f.executor)
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *realWGFixture) tick() {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := f.scheduler.Tick(ctx); err != nil {
		f.t.Fatalf("owned actual WG reconcile: %v", err)
	}
}

func (f *realWGFixture) request(node *realWGNode, marker string, timeout time.Duration) (string, error) {
	proxyURL, _ := url.Parse("http://" + realWGFixtureProxy)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != realWGFixtureProxy {
			return nil, errors.New("fixture tunnel destination mismatch")
		}
		return node.stack.DialContext(ctx, network, M.ParseSocksaddr(address))
	}}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.target.URL+"/owned-marker/"+marker, nil)
	if err != nil {
		return "", err
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	if err != nil || response.StatusCode != http.StatusOK {
		return "", errors.New("owned marker response invalid")
	}
	return string(body), nil
}
func (f *realWGFixture) requireMarker(node *realWGNode, marker string) {
	f.t.Helper()
	// A preceding deliberate pre-apply rejection can leave WireGuard waiting
	// for its real five-second handshake retry. Keep that protocol timer intact.
	body, err := f.request(node, marker, 7*time.Second)
	if err != nil || body != "owned-wg-target:/owned-marker/"+marker {
		f.t.Fatalf("actual encrypted WG marker %s failed: %v", marker, err)
	}
}
func (f *realWGFixture) requireBlocked(node *realWGNode, marker string) {
	f.t.Helper()
	before := f.marker.Load()
	if _, err := f.request(node, marker, 750*time.Millisecond); err == nil {
		f.t.Fatalf("revoked actual peer reached target: %s", marker)
	}
	if f.marker.Load() != before {
		f.t.Fatal("revoked request changed owned target marker")
	}
}
func (f *realWGFixture) requirePeerTraffic(public, address string) {
	f.t.Helper()
	f.executor.mu.Lock()
	peers, err := readRealWGPeers(f.executor.hub.device)
	f.executor.mu.Unlock()
	peer, exists := peers[public]
	if err != nil || !exists || len(peer.AllowedIPs) != 1 || peer.AllowedIPs[0] != address || peer.handshake == 0 || peer.received == 0 || peer.sent == 0 {
		f.t.Fatal("actual peer handshake/transfer evidence missing or wrong assigned address")
	}
	f.t.Logf("actual WG evidence: address=%s handshake=%t rx=%d tx=%d", address, peer.handshake > 0, peer.received, peer.sent)
}
func (f *realWGFixture) requireAbsent(public string) {
	f.t.Helper()
	f.executor.mu.Lock()
	peers, err := readRealWGPeers(f.executor.hub.device)
	f.executor.mu.Unlock()
	if _, present := peers[public]; err != nil || present {
		f.t.Fatal("actual revoked peer remains in WireGuard runtime")
	}
}

func (f *realWGFixture) restoreOldRuntime(t *testing.T, customer *realWGNode) {
	t.Helper()
	f.executor.mu.Lock()
	key := f.executor.hub.key
	f.executor.hub.close()
	f.executor.hub = newRealWGNode(t, key, "10.250.0.1/24", f.proxy)
	for _, peer := range []deviceauth.Peer{{PublicKey: f.protected.key.publicBase64(), AllowedIPs: []string{"10.250.0.3/32"}}, {PublicKey: customer.key.publicBase64(), AllowedIPs: []string{"10.250.0.2/32"}}} {
		command, err := realWGPeerIPC(peer)
		if err != nil || f.executor.hub.device.IpcSet(command) != nil {
			f.executor.mu.Unlock()
			t.Fatal("owned stale runtime reconstruction failed")
		}
	}
	peers, err := readRealWGPeers(f.executor.hub.device)
	_, staleExists := peers[customer.key.publicBase64()]
	f.executor.mu.Unlock()
	if err != nil || !staleExists {
		t.Fatal("restart fixture did not load old customer peer")
	}
	// Restart both client stacks with their existing keys as well. A still-live
	// WireGuard client can otherwise retain the old transport session for two
	// minutes; this fixture exercises a genuine cold reconnect, not that timer.
	protectedKey, customerKey := f.protected.key, customer.key
	f.protected.close()
	customer.close()
	*f.protected = *newRealWGNode(t, protectedKey, "10.250.0.3/32", nil)
	*customer = *newRealWGNode(t, customerKey, "10.250.0.2/32", nil)
	f.protected.routeTo(t, f.executor.hub)
	customer.routeTo(t, f.executor.hub)
}

func (f *realWGFixture) enrollAndApply(t *testing.T, customer *realWGNode) deviceauth.Operation {
	t.Helper()
	if err := f.store.Enroll(context.Background(), deviceauth.Enrollment{DeviceID: "owned-customer", OwnerID: "owned-owner", Role: "customer", ValidUntil: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	operation, err := f.store.Submit(context.Background(), deviceauth.Command{Actor: deviceauth.Actor{ID: "owned-actor", OwnerID: "owned-owner"}, DeviceID: "owned-customer", Action: "apply", IdempotencyKey: "owned-first-apply", ExpectedGeneration: 0, PublicKey: customer.key.publicBase64(), Address: "10.250.0.2/32"})
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != "pending" {
		t.Fatalf("apply unexpectedly effective before actual WG: %s", operation.State)
	}
	return operation
}

var _ deviceauth.Executor = (*realWGExecutor)(nil)
var _ tun.Handler = (*realWGProxy)(nil)
var _ conn.Bind = (*realWGLoopbackBind)(nil)
var _ conn.Endpoint = (*realWGLoopbackEndpoint)(nil)
