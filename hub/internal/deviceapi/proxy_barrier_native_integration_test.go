//go:build linux && integration && with_gvisor

package deviceapi

// This fixture owns a new user/network namespace, real kernel WireGuard, the
// product reverse server/client binaries and local marker/echo targets. It never
// changes the host namespace or reads production configuration.
import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"zongheng-vpn/hub/internal/deviceauth"
	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/proxygate"
)

func TestProxyBarrierNativeProductWireGuard(t *testing.T) {
	dir := t.TempDir()
	repo, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range []struct{ name, pkg string }{{"reverse", "./egress/reverse"}, {"supervisor", "./hub/cmd/zhhub-device-executor"}, {"peer-helper", "./hub/internal/deviceapi/testdata/proxybarrier"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		c := exec.CommandContext(ctx, "go", "build", "-tags", "with_gvisor", "-o", filepath.Join(dir, b.name), b.pkg)
		c.Dir = repo
		output, e := c.CombinedOutput()
		cancel()
		if e != nil {
			t.Fatalf("owned binary build %s: %.2000s", b.name, output)
		}
	}
	testExe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "unshare", "-Urn", "--", testExe, "-test.run=^TestProxyBarrierNamespaceChild$", "-test.v")
	c.Env = append(os.Environ(), "ZH_BARRIER_NAMESPACE=1", "ZH_BARRIER_BIN_DIR="+dir)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second
	if output, e := c.CombinedOutput(); e != nil {
		t.Fatalf("owned namespace failed: %.8000s", output)
	} else {
		t.Logf("actual namespace receipt: %.8000s", output)
	}
}

func nativeCommand(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	if c.Run() != nil {
		t.Fatalf("owned native command failed (%s), diagnostics suppressed", args[0])
	}
}
func nativeWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if os.WriteFile(path, b, 0600) != nil {
		t.Fatal("owned fixture file write")
	}
}
func nativeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	nativeWrite(t, path, b)
}
func nativePort(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func nativeStart(t *testing.T, path string, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := exec.CommandContext(ctx, path, args...)
	log, err := os.CreateTemp(os.Getenv("ZH_BARRIER_PROCESS_LOG_DIR"), "process-")
	if err != nil {
		cancel()
		t.Fatal("owned process log")
	}
	c.Stdout = log
	c.Stderr = log
	c.WaitDelay = time.Second
	if c.Start() != nil {
		cancel()
		t.Fatal("owned process start")
	}
	t.Cleanup(func() {
		cancel()
		_ = c.Wait()
		log.Close()
		if t.Failed() {
			b, _ := os.ReadFile(log.Name())
			text := strings.ReplaceAll(string(b), "owned-native-fixture-token", "[owned-token]")
			t.Logf("owned product diagnostics %.3500s", text)
		}
	})
	return c
}

type nativePeer struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	dec *json.Decoder
}
type nativePeerRequest struct {
	Action, KeyFile, Endpoint, Address, LinkAddress, Proxy, Target, Echo string
	UntilUnixNano                                                        int64
}
type nativePeerReceipt struct {
	PID            int
	OK             bool
	Status         int
	Marker, Closed bool
	TimedOut       bool `json:"timed_out"`
}

func (p *nativePeer) call(t *testing.T, q nativePeerRequest) nativePeerReceipt {
	t.Helper()
	if json.NewEncoder(p.in).Encode(q) != nil {
		t.Fatal("peer request write")
	}
	return p.read(t)
}
func (p *nativePeer) read(t *testing.T) nativePeerReceipt {
	t.Helper()
	type result struct {
		r nativePeerReceipt
		e error
	}
	done := make(chan result, 1)
	go func() { var r nativePeerReceipt; e := p.dec.Decode(&r); done <- result{r, e} }()
	select {
	case v := <-done:
		if v.e != nil {
			t.Fatal("peer receipt unavailable")
		}
		return v.r
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		t.Fatal("peer receipt deadline")
	}
	return nativePeerReceipt{}
}
func nativeMakePeer(t *testing.T, dir, hubPublic string, hubPort, n int, key realWGKey) *nativePeer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := exec.CommandContext(ctx, "unshare", "-n", "--", filepath.Join(os.Getenv("ZH_BARRIER_BIN_DIR"), "peer-helper"))
	c.Env = append(os.Environ(), "ZH_BARRIER_HUB_PUBLIC="+hubPublic)
	c.Stderr = io.Discard
	c.WaitDelay = time.Second
	in, e := c.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	out, e := c.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if c.Start() != nil {
		cancel()
		t.Fatal("namespace peer start")
	}
	p := &nativePeer{c, in, json.NewDecoder(io.LimitReader(out, 1<<20))}
	t.Cleanup(func() { in.Close(); cancel(); _ = c.Wait() })
	r := p.read(t)
	if !r.OK || r.PID != c.Process.Pid {
		t.Fatal("peer namespace identity")
	}
	outer := fmt.Sprintf("vb%d", n)
	inner := fmt.Sprintf("vp%d", n)
	nativeCommand(t, "ip", "link", "add", outer, "type", "veth", "peer", "name", inner)
	nativeCommand(t, "ip", "link", "set", inner, "netns", strconv.Itoa(r.PID), "name", "vethpeer")
	nativeCommand(t, "ip", "address", "add", fmt.Sprintf("10.211.%d.1/30", n), "dev", outer)
	nativeCommand(t, "ip", "link", "set", outer, "up")
	path := filepath.Join(dir, fmt.Sprintf("peer%d.key", n))
	nativeWrite(t, path, []byte(base64.StdEncoding.EncodeToString(key.private)+"\n"))
	r = p.call(t, nativePeerRequest{Action: "setup", KeyFile: path, Endpoint: fmt.Sprintf("10.211.%d.1:%d", n, hubPort), Address: fmt.Sprintf("10.250.0.%d/32", n), LinkAddress: fmt.Sprintf("10.211.%d.2/30", n)})
	if !r.OK {
		t.Fatal("actual WireGuard peer setup")
	}
	return p
}

func nativeCertificates(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	x := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "owned"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	cert, e := x509.CreateCertificate(rand.Reader, x, x, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	c, k := filepath.Join(dir, "tls.cert"), filepath.Join(dir, "tls.key")
	nativeWrite(t, c, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}))
	nativeWrite(t, k, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}))
	return c, k
}

func TestProxyBarrierNamespaceChild(t *testing.T) {
	if os.Getenv("ZH_BARRIER_NAMESPACE") != "1" {
		t.Skip("helper only in owned namespace")
	}
	dir, err := os.MkdirTemp("", "zpg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) }) // exact directory created by this fixture
	t.Setenv("ZH_BARRIER_PROCESS_LOG_DIR", dir)
	nativeCommand(t, "ip", "link", "set", "lo", "up")
	hub := newRealWGKey(t)
	keys := map[int]realWGKey{}
	for _, n := range []int{2, 3, 4, 7} {
		keys[n] = newRealWGKey(t)
	}
	hubKey := filepath.Join(dir, "hub.key")
	nativeWrite(t, hubKey, []byte(base64.StdEncoding.EncodeToString(hub.private)+"\n"))
	hubPort := nativePort(t)
	nativeCommand(t, "ip", "link", "add", "wgbarrier0", "type", "wireguard")
	nativeCommand(t, "wg", "set", "wgbarrier0", "private-key", hubKey, "listen-port", strconv.Itoa(hubPort))
	nativeCommand(t, "ip", "address", "add", "10.250.0.1/32", "dev", "wgbarrier0")
	nativeCommand(t, "ip", "link", "set", "wgbarrier0", "up")
	for _, n := range []int{2, 3, 4, 7} {
		nativeCommand(t, "ip", "route", "add", fmt.Sprintf("10.250.0.%d/32", n), "dev", "wgbarrier0")
		nativeCommand(t, "wg", "set", "wgbarrier0", "peer", keys[n].publicBase64(), "allowed-ips", fmt.Sprintf("10.250.0.%d/32", n))
	}
	peers := map[int]*nativePeer{}
	for _, n := range []int{2, 3, 4, 7} {
		peers[n] = nativeMakePeer(t, dir, hub.publicBase64(), hubPort, n, keys[n])
	}
	var markerHits atomic.Int64
	marker, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { markerHits.Add(1); fmt.Fprint(w, "owned-barrier-marker") }), ReadHeaderTimeout: time.Second}
	go server.Serve(marker)
	t.Cleanup(func() { server.Close() })
	echo, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { echo.Close() })
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				fmt.Fprint(c, "owned-ready\n")
				s := bufio.NewScanner(c)
				for s.Scan() {
					fmt.Fprintln(c, s.Text())
				}
			}()
		}
	}()
	proxyAddress := fmt.Sprintf("10.250.0.1:%d", nativePort(t))
	tunnelAddress := fmt.Sprintf("127.0.0.1:%d", nativePort(t))
	policy := deviceauth.Policy{Epoch: "native-barrier-epoch", ManagedBy: "native-barrier-authority", AddressPools: []string{"10.250.0.2/32", "10.250.0.4/32"}, Protected: []deviceauth.Protection{{Prefix: "10.250.0.1/32"}, {Prefix: "10.250.0.3/32", PublicKey: keys[3].publicBase64()}}}
	profile := dc.ProxyRouteProfile{Version: 1, Revision: 1, AuthorityEpoch: policy.Epoch, ManagedBy: policy.ManagedBy, WgInterface: "wgbarrier0", WgPublicKey: hub.publicBase64(), WgEndpoint: fmt.Sprintf("127.0.0.1:%d", hubPort), ProxyAddress: proxyAddress, EgressId: "owned-egress", EgressName: "owned-native", AllowedIps: []string{"10.250.0.1/32"}}
	policySHA, e := deviceauth.CanonicalPolicyDigest(policy)
	if e != nil {
		t.Fatal(e)
	}
	profileSHA, e := dc.ProxyRouteProfileDigest(profile)
	if e != nil {
		t.Fatal(e)
	}
	gate := proxygate.Policy{Version: 1, Epoch: policy.Epoch, ManagedBy: policy.ManagedBy, Interface: "wgbarrier0", PolicySHA256: policySHA, ProfileSHA256: profileSHA, Listener: proxyAddress, ControllerUID: uint32(os.Geteuid()), ManagedSources: policy.AddressPools, RetainedSources: []string{"10.250.0.1/32", "10.250.0.3/32", "10.250.0.7/32"}}
	policyPath, profilePath, gatePath := filepath.Join(dir, "authority.json"), filepath.Join(dir, "profile.json"), filepath.Join(dir, "gate.json")
	nativeJSON(t, policyPath, policy)
	nativeJSON(t, profilePath, profile)
	nativeJSON(t, gatePath, gate)
	controlDir := filepath.Join(dir, "control")
	if os.Mkdir(controlDir, 0700) != nil {
		t.Fatal("control directory")
	}
	socket := filepath.Join(controlDir, "gate.sock")
	token := filepath.Join(dir, "tunnel.token")
	nativeWrite(t, token, []byte("owned-native-fixture-token\n"))
	bin := filepath.Join(os.Getenv("ZH_BARRIER_BIN_DIR"), "reverse")
	reverseConfig := filepath.Join(dir, "reverse.yaml")
	nativeWrite(t, reverseConfig, []byte("server:\n  allowed_proxy_cidrs: [10.250.0.0/24]\n  debug_allowed_cidrs: [10.250.0.0/24]\n"))
	reverseArgs := []string{"server", "--config", reverseConfig, "--transport", "tcp", "--resolve", "client", "--listen", tunnelAddress, "--proxy", proxyAddress, "--token-file", token, "--enable-fetch", "--proxy-gate-policy-file", gatePath, "--proxy-gate-control-socket", socket}
	reverseProcess := nativeStart(t, bin, reverseArgs...)
	deadline := time.Now().Add(4 * time.Second)
	for {
		c, e := net.DialTimeout("tcp", tunnelAddress, 100*time.Millisecond)
		if e == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("product reverse server not listening")
		}
		time.Sleep(20 * time.Millisecond)
	}
	nativeStart(t, bin, "client", "--transport", "tcp", "--server", tunnelAddress, "--token-file", token)
	base := nativePeerRequest{Proxy: proxyAddress, Target: marker.Addr().String(), Echo: echo.Addr().String()}
	positive := func(n int) {
		t.Helper()
		q := base
		q.Action = "connect"
		until := time.Now().Add(6 * time.Second)
		for {
			r := peers[n].call(t, q)
			if r.OK && r.Status == 200 && r.Marker {
				return
			}
			if time.Now().After(until) {
				t.Fatalf("protected/unknown actual proxy failed source .%d status%d", n, r.Status)
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	negative := func(n int) {
		t.Helper()
		before := markerHits.Load()
		for _, action := range []string{"connect", "striped", "fetch"} {
			q := base
			q.Action = action
			if action == "fetch" {
				q.Target = url.QueryEscape("http://" + marker.Addr().String() + "/marker")
			}
			var r nativePeerReceipt
			until := time.Now().Add(6 * time.Second)
			for { // Require a refusal through a live WG handshake, not a dial failure.
				r = peers[n].call(t, q)
				if r.OK || time.Now().After(until) {
					break
				}
				time.Sleep(40 * time.Millisecond)
			}
			if !r.OK || r.Status != http.StatusServiceUnavailable || r.Marker {
				t.Fatalf("managed %s not denied source .%d status%d", action, n, r.Status)
			}
		}
		if markerHits.Load() != before {
			t.Fatal("closed product gate reached upstream marker")
		}
	}
	positive(3)
	positive(7)
	negative(2)
	negative(4)
	cert, key := nativeCertificates(t, dir)
	wgOwned := filepath.Join(dir, "wg-owned")
	wgBytes, e := os.ReadFile("/usr/bin/wg")
	if e != nil {
		t.Fatal("actual wg binary")
	}
	if os.WriteFile(wgOwned, wgBytes, 0700) != nil {
		t.Fatal("owned actual wg binary")
	}
	config := func(dbname, wgPath string) Config {
		return Config{ListenAddr: fmt.Sprintf("127.0.0.1:%d", nativePort(t)), DBPath: filepath.Join(dir, dbname), PolicyPath: policyPath, ProxyProfilePath: profilePath, ProxyGatePolicyPath: gatePath, ProxyGateSocketPath: socket, WGExecutable: wgPath, SupervisorExecutable: filepath.Join(os.Getenv("ZH_BARRIER_BIN_DIR"), "supervisor"), WGInterface: "wgbarrier0", TLSCert: cert, TLSKey: key}
	}
	preparation, e := deviceauth.NewSupervisedWGExecutor(wgOwned, filepath.Join(os.Getenv("ZH_BARRIER_BIN_DIR"), "supervisor"), "wgbarrier0", policy, 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	setupAuthority := func(s *Service, revoke bool) {
		t.Helper()
		for _, n := range []int{2, 4} {
			// A new authority cannot adopt an unregistered preexisting key.
			// Prepare its own synthetic peer through the actual owned executor.
			nativeCommand(t, "wg", "set", "wgbarrier0", "peer", keys[n].publicBase64(), "remove")
			id := fmt.Sprintf("native-device-%d", n)
			if e := s.Store.Enroll(context.Background(), deviceauth.Enrollment{DeviceID: id, OwnerID: "native-owner", Role: "customer", ValidUntil: time.Now().Add(time.Hour)}); e != nil {
				t.Fatal(e)
			}
			op, e := s.Store.Submit(context.Background(), deviceauth.Command{Actor: deviceauth.Actor{ID: "native-actor", OwnerID: "native-owner"}, DeviceID: id, Action: "apply", IdempotencyKey: fmt.Sprintf("native-apply-%d", n), PublicKey: keys[n].publicBase64(), Address: fmt.Sprintf("10.250.0.%d/32", n)})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Store.Process(context.Background(), op.ID, preparation); e != nil {
				t.Fatal(e)
			}
			if r := peers[n].call(t, nativePeerRequest{Action: "refresh"}); !r.OK {
				t.Fatal("owned handshake refresh")
			}
			if revoke || n == 4 {
				_, e = s.Store.Submit(context.Background(), deviceauth.Command{Actor: deviceauth.Actor{ID: "native-actor", OwnerID: "native-owner"}, DeviceID: id, Action: "revoke", ExpectedGeneration: 1, IdempotencyKey: fmt.Sprintf("native-revoke-%d", n)})
				if e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	// Fault is in the actual supervised wg command, after the first real peer
	// removal. The untouched second peer remains able to handshake, but no proxy.
	countPath := filepath.Join(dir, "snapshot-count")
	wrapper := filepath.Join(dir, "wg-fail-second")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = show ]; then n=0; [ ! -f '%s' ] || n=$(cat '%s'); n=$((n+1)); printf '%%s' \"$n\" > '%s'; [ \"$n\" -lt 2 ] || exit 71; fi\nexec '%s' \"$@\"\n", countPath, countPath, countPath, wgOwned)
	if os.WriteFile(wrapper, []byte(script), 0700) != nil {
		t.Fatal("fault wrapper")
	}
	s, e := Open(context.Background(), config("failed.db", wrapper))
	if e != nil {
		t.Fatal(e)
	}
	setupAuthority(s, true)
	if e = s.Run(context.Background()); e == nil {
		t.Fatal("partial startup falsely ready")
	}
	if c, e := net.DialTimeout("tcp", s.config.ListenAddr, 100*time.Millisecond); e == nil {
		c.Close()
		t.Fatal("partial startup listened for TLS")
	}
	s.Close()
	show := exec.Command("wg", "show", "wgbarrier0", "peers")
	b, e := show.Output()
	if e != nil || strings.Contains(string(b), keys[2].publicBase64()) || !strings.Contains(string(b), keys[4].publicBase64()) {
		t.Fatal("original real partial-removal premise not reproduced")
	}
	negative(4)
	positive(3)
	positive(7)
	t.Log("default closed ordinary/striped/fetch + real partial-WG failure + protected/unknown positive controls PASS")
	good, e := Open(context.Background(), config("good.db", wgOwned))
	if e != nil {
		t.Fatal(e)
	}
	defer good.Close()
	setupAuthority(good, false)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- good.Run(runCtx) }()
	defer cancel()
	positive(2)
	positive(3)
	positive(7)
	q := base
	q.Action = "hold"
	if r := peers[2].call(t, q); !r.OK {
		t.Fatal("actual managed CONNECT not established")
	}
	if r := peers[3].call(t, q); !r.OK {
		t.Fatal("protected CONNECT not established")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("coordinator shutdown deadline")
	}
	q.Action = "held"
	if r := peers[2].call(t, q); !r.OK || !r.Closed {
		t.Fatal("managed stream survived owner shutdown")
	}
	if r := peers[3].call(t, q); !r.OK || r.Closed {
		t.Fatal("protected stream spilled during shutdown")
	}
	negative(2)
	positive(7)
	t.Log("fenced actual grant + owner EOF closes managed existing/new streams and preserves protected existing stream PASS")
	nativeExpiryAfterSnapshot(t, policy, gate, preparation, config("expiry.db", wgOwned), keys[2], peers[2], negative, positive)
	nativeAdditionalFaults(t, dir, wgOwned, config, setupAuthority, keys, peers, base, &markerHits, negative, positive)
	// Leave a real, currently converged .2 authorization for transport-owner
	// failure tests. The helper exercises UDS ownership, not a device grant API.
	controllerService, e := Open(context.Background(), config("controller-ready.db", wgOwned))
	if e != nil {
		t.Fatal(e)
	}
	setupAuthority(controllerService, false)
	controllerCtx, cancelController := context.WithCancel(context.Background())
	controllerDone := make(chan error, 1)
	go func() { controllerDone <- controllerService.Run(controllerCtx) }()
	positive(2)
	cancelController()
	select {
	case <-controllerDone:
	case <-time.After(8 * time.Second):
		t.Fatal("controller preparation shutdown")
	}
	controllerService.Close()
	nativeKilledControllerAndReverseRestart(t, gatePath, socket, bin, reverseArgs, reverseProcess, peers, base, keys, negative, positive)
}

type nativeExpirySnapshot struct {
	deviceauth.Executor
	once    sync.Once
	advance func()
}

type nativeCountingController struct {
	*proxygate.Controller
	grants *atomic.Int64
}

func (c *nativeCountingController) GrantUntil(ctx context.Context, until time.Time) error {
	c.grants.Add(1)
	return c.Controller.GrantUntil(ctx, until)
}

func nativeAdditionalFaults(t *testing.T, dir, wgOwned string, config func(string, string) Config, setup func(*Service, bool), keys map[int]realWGKey, peers map[int]*nativePeer, base nativePeerRequest, marker *atomic.Int64, negative, positive func(int)) {
	t.Helper()
	for _, tc := range []struct {
		name, script string
		alive        bool
		trigger      bool
	}{
		{"first-snapshot", `if [ "$1" = show ]; then exit 71; fi`, true, false},
		{"remove-failure", `if [ "$1" = set ] && [ "$5" = remove ]; then exit 71; fi`, true, false},
		{"result-abort", "", false, true},
	} {
		path := wgOwned
		if tc.script != "" {
			path = filepath.Join(dir, "wg-"+tc.name)
			script := fmt.Sprintf("#!/bin/sh\n%s\nexec '%s' \"$@\"\n", tc.script, wgOwned)
			if os.WriteFile(path, []byte(script), 0700) != nil {
				t.Fatal("native fault wrapper")
			}
		}
		s, err := Open(context.Background(), config(tc.name+".db", path))
		if err != nil {
			t.Fatal(err)
		}
		setup(s, true)
		if tc.trigger {
			if _, err := s.DB.Exec(`CREATE TRIGGER native_result_abort BEFORE UPDATE ON deviceauth_outbox WHEN NEW.state='done' BEGIN SELECT RAISE(ABORT,'owned_result_abort'); END;`); err != nil {
				t.Fatal("native result failure trigger")
			}
		}
		var grants atomic.Int64
		s.gateDial = func(ctx context.Context, path string, p proxygate.Policy) (proxyController, error) {
			c, err := proxygate.DialControl(ctx, path, p)
			if err != nil {
				return nil, err
			}
			return &nativeCountingController{c, &grants}, nil
		}
		before := marker.Load()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		err = s.Run(ctx)
		cancel()
		if err == nil || grants.Load() != 0 {
			t.Fatalf("native %s falsely reached grant/ready", tc.name)
		}
		if c, err := net.DialTimeout("tcp", s.config.ListenAddr, 100*time.Millisecond); err == nil {
			c.Close()
			t.Fatal("faulted native service listened TLS")
		}
		show := exec.Command("wg", "show", s.config.WGInterface, "peers")
		publicPeers, err := show.Output()
		if err != nil {
			t.Fatal("native fault public peer read")
		}
		for _, n := range []int{2, 4} {
			if strings.Contains(string(publicPeers), keys[n].publicBase64()) != tc.alive {
				t.Fatalf("native %s WG premise source .%d", tc.name, n)
			}
			if tc.alive {
				negative(n)
			} else {
				q := base
				q.Action = "connect"
				if r := peers[n].call(t, q); r.Marker {
					t.Fatal("removed native source reached target")
				}
			}
		}
		if marker.Load() != before {
			t.Fatal("native failed startup changed owned target")
		}
		for _, n := range []int{3, 7} {
			if !strings.Contains(string(publicPeers), keys[n].publicBase64()) {
				t.Fatal("native startup fault removed retained peer")
			}
			positive(n)
		}
		s.Close()
		t.Logf("actual native %s failure: no grant/TLS, managed WG present=%t, zero managed target markers, retained positive controls PASS", tc.name, tc.alive)
	}
}

func nativeMakeController(t *testing.T, policyPath, socket string) *nativePeer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := exec.CommandContext(ctx, filepath.Join(os.Getenv("ZH_BARRIER_BIN_DIR"), "peer-helper"), "--controller", policyPath, socket)
	c.Stderr = io.Discard
	c.WaitDelay = time.Second
	in, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if c.Start() != nil {
		cancel()
		t.Fatal("actual controller helper start")
	}
	p := &nativePeer{c, in, json.NewDecoder(io.LimitReader(out, 1<<20))}
	t.Cleanup(func() { in.Close(); cancel(); _ = c.Wait() })
	if r := p.read(t); !r.OK || r.PID != c.Process.Pid {
		t.Fatal("actual closed-ACK controller helper")
	}
	return p
}

func nativeKilledControllerAndReverseRestart(t *testing.T, policyPath, socket, reverseBinary string, reverseArgs []string, reverse *exec.Cmd, peers map[int]*nativePeer, base nativePeerRequest, keys map[int]realWGKey, negative, positive func(int)) {
	t.Helper()
	controller := nativeMakeController(t, policyPath, socket)
	if r := controller.call(t, nativePeerRequest{Action: "grant", UntilUnixNano: time.Now().UTC().Add(4 * time.Second).UnixNano()}); !r.OK {
		t.Fatal("actual child controller grant")
	}
	for _, n := range []int{2, 3} {
		q := base
		q.Action = "hold"
		if r := peers[n].call(t, q); !r.OK {
			t.Fatal("controller kill held CONNECT")
		}
	}
	if controller.cmd.Process.Kill() != nil {
		t.Fatal("actual controller SIGKILL")
	}
	_ = controller.cmd.Wait()
	q := base
	q.Action = "held"
	if r := peers[2].call(t, q); !r.OK || !r.Closed || r.TimedOut {
		t.Fatal("SIGKILL managed stream failed actual non-timeout close")
	}
	if r := peers[3].call(t, q); !r.OK || r.Closed || r.TimedOut {
		t.Fatal("SIGKILL spilled retained existing stream")
	}
	negative(2)
	positive(7)
	t.Log("actual controller SIGKILL: managed existing stream non-timeout closed/new paths denied, retained existing .3 and fresh .7 survived PASS")
	// SIGKILL of reverse intentionally stops every old reverse stream. Check
	// reconnection of retained sources after restart, not preservation of the
	// killed server's old sockets. The immutable WG peers must remain unchanged.
	show := exec.Command("wg", "show", "wgbarrier0", "peers")
	before, err := show.Output()
	if err != nil {
		t.Fatal("restart public peers")
	}
	oldSocket, err := os.Lstat(socket)
	if err != nil || oldSocket.Mode()&os.ModeSocket == 0 {
		t.Fatal("owned restart socket identity")
	}
	parent, err := os.Lstat(filepath.Dir(socket))
	if err != nil || !parent.IsDir() {
		t.Fatal("owned restart socket namespace")
	}
	if reverse.Process.Kill() != nil {
		t.Fatal("actual reverse SIGKILL")
	}
	_ = reverse.Wait()
	if now, err := os.Lstat(socket); err == nil {
		st, ok := now.Sys().(*syscall.Stat_t)
		currentParent, pe := os.Lstat(filepath.Dir(socket))
		if !ok || st.Uid != uint32(os.Geteuid()) || now.Mode()&os.ModeSocket == 0 || !os.SameFile(oldSocket, now) || pe != nil || !os.SameFile(parent, currentParent) {
			t.Fatal("refused unowned stale UDS removal")
		}
		if err := os.Remove(socket); err != nil {
			t.Fatal("owned stale UDS removal")
		}
	} else if !os.IsNotExist(err) {
		t.Fatal("owned stale UDS inspection")
	}
	nativeStart(t, reverseBinary, reverseArgs...)
	positive(3)
	positive(7)
	negative(2)
	show = exec.Command("wg", "show", "wgbarrier0", "peers")
	after, err := show.Output()
	if err != nil || string(before) != string(after) {
		t.Fatal("reverse restart changed kernel WG peers")
	}
	for _, n := range []int{2, 3, 7} {
		if !strings.Contains(string(after), keys[n].publicBase64()) {
			t.Fatal("restart lost required real WG peer")
		}
	}
	t.Log("actual reverse SIGKILL/restart with exact owned stale-UDS cleanup: no controller, default managed denial, retained reconnected, WG unchanged PASS")
}

func (e *nativeExpirySnapshot) Snapshot(ctx context.Context, f deviceauth.Fence) ([]deviceauth.Peer, error) {
	peers, err := e.Executor.Snapshot(ctx, f)
	if err == nil {
		e.once.Do(e.advance)
	}
	return peers, err
}

// Cross the authority's injected clock at the actual supervised kernel WG
// Snapshot boundary. No fake external executor supplies applied/runtime facts.
func nativeExpiryAfterSnapshot(t *testing.T, policy deviceauth.Policy, gate proxygate.Policy, executor deviceauth.Executor, config Config, key realWGKey, peer *nativePeer, negative, positive func(int)) {
	t.Helper()
	s, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	clock := time.Now().UTC()
	start := clock
	store, err := deviceauth.New(context.Background(), s.DB, deviceauth.Options{Policy: policy, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	// The preceding good authority has stopped. Remove only its synthetic .2
	// key before proving a fresh absence-checked apply under this new DB fence.
	nativeCommand(t, "wg", "set", config.WGInterface, "peer", key.publicBase64(), "remove")
	if err := store.Enroll(context.Background(), deviceauth.Enrollment{DeviceID: "native-expiry-device", OwnerID: "native-expiry-owner", Role: "customer", ValidUntil: start.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	op, err := store.Submit(context.Background(), deviceauth.Command{Actor: deviceauth.Actor{ID: "native-expiry-actor", OwnerID: "native-expiry-owner"}, DeviceID: "native-expiry-device", Action: "apply", IdempotencyKey: "native-expiry-apply", PublicKey: key.publicBase64(), Address: "10.250.0.2/32"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Process(context.Background(), op.ID, executor); err != nil {
		t.Fatal(err)
	}
	if r := peer.call(t, nativePeerRequest{Action: "refresh"}); !r.OK {
		t.Fatal("expiry peer handshake refresh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	control, err := proxygate.DialControl(ctx, config.ProxyGateSocketPath, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	scheduler, err := deviceauth.NewScheduler(store, &nativeExpirySnapshot{Executor: executor, advance: func() { clock = start.Add(2 * time.Second) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Tick(ctx); err != nil {
		t.Fatalf("expiry Tick must return nil while ignoring ErrExpired: %v", err)
	}
	d, err := store.Device(ctx, "native-expiry-device")
	if err != nil || d.State != "expired" || d.Generation != 2 || d.AppliedGeneration != 1 {
		t.Fatal("real Snapshot expiry did not retain a pending removal generation")
	}
	show := exec.Command("wg", "show", config.WGInterface, "peers")
	publicPeers, err := show.Output()
	if err != nil || !strings.Contains(string(publicPeers), key.publicBase64()) {
		t.Fatal("expired kernel peer premise not reproduced")
	}
	grants := 0
	grant := func(ctx context.Context, p deviceauth.ProxyConvergenceProof) error {
		grants++
		if p.ActiveDevices != 0 {
			t.Fatal("expired device remained active in convergence proof")
		}
		return control.GrantUntil(ctx, time.Now().UTC().Add(2*time.Second))
	}
	err = store.WithProxyConvergence(ctx, executor, gate.ManagedSources, grant)
	if !errors.Is(err, deviceauth.ErrVerification) || grants != 0 {
		t.Fatal("Tick nil falsely reached a real grant for an expired pending generation")
	}
	negative(2)
	positive(3)
	positive(7)
	if err := scheduler.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	d, err = store.Device(ctx, "native-expiry-device")
	if err != nil || d.Generation != 2 || d.AppliedGeneration != 2 {
		t.Fatal("next Tick did not converge the new expiry removal")
	}
	show = exec.Command("wg", "show", config.WGInterface, "peers")
	publicPeers, err = show.Output()
	if err != nil || strings.Contains(string(publicPeers), key.publicBase64()) {
		t.Fatal("next Tick left expired kernel peer installed")
	}
	if err := store.WithProxyConvergence(ctx, executor, gate.ManagedSources, grant); err != nil || grants != 1 {
		t.Fatal("drained expiry generation did not produce current real grant proof")
	}
	if err := control.Closed(ctx); err != nil {
		t.Fatal("expiry stage final closed acknowledgement")
	}
	positive(3)
	positive(7)
	t.Log("real supervised kernel Snapshot crosses expiry: Tick nil/pending removal/peer still present; fenced grant callback zero; next Tick removes peer and converges PASS")
}
