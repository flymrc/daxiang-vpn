package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type secureTestCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}
type secureTestLeaf struct {
	cert              *x509.Certificate
	certPath, keyPath string
	pair              tls.Certificate
}

func makeSecureTestCA(t *testing.T) secureTestCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test reverse CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return secureTestCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func makeSecureTestLeaf(t *testing.T, ca secureTestCA, dir, role, id string, expiry time.Time) secureTestLeaf {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := url.Parse("spiffe://zhvpn/" + role + "/" + id)
	usage := x509.ExtKeyUsageClientAuth
	if role == "hub" {
		usage = x509.ExtKeyUsageServerAuth
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "no-common-name-identity"}, NotBefore: time.Now().Add(-time.Hour).Truncate(time.Second), NotAfter: expiry.Truncate(time.Second), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, URIs: []*url.URL{uri}, DNSNames: []string{"hub.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	name := role + "-" + id + "-" + serial.String()
	leaf := secureTestLeaf{cert: cert, certPath: filepath.Join(dir, name+".crt"), keyPath: filepath.Join(dir, name+".key"), pair: pair}
	if err := os.WriteFile(leaf.certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf.keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	return leaf
}

func testSecureCredential(leaf secureTestLeaf, id string) secureCredential {
	sum := sha256.Sum256(leaf.cert.Raw)
	return secureCredential{CredentialID: id, LeafSHA256: hex.EncodeToString(sum[:]), State: "active", NotBefore: leaf.cert.NotBefore, ExpiresAt: leaf.cert.NotAfter}
}

func writeSecureTestPolicy(t *testing.T, path string, p secureRegistry) {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	writeSecureTestData(t, path, data)
}

func writeSecureTestData(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.CreateTemp(filepath.Dir(path), ".test-policy-*")
	if err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replaceSecureFile(name, path); err != nil {
		t.Fatal(err)
	}
}

type secureTestFixture struct {
	dir           string
	ca            secureTestCA
	hub, phone    secureTestLeaf
	serverOptions serverOptions
	clientOptions clientOptions
	policy        secureRegistry
}

func newSecureTestFixture(t *testing.T) *secureTestFixture {
	t.Helper()
	dir := t.TempDir()
	// testing.TempDir's numbered directory inherits the host umask. Provision
	// the private service directory explicitly, as required by secure transport.
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ca := makeSecureTestCA(t)
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, ca.pem, 0600); err != nil {
		t.Fatal(err)
	}
	hub := makeSecureTestLeaf(t, ca, dir, "hub", "test-hub", time.Now().Add(2*time.Hour))
	phone := makeSecureTestLeaf(t, ca, dir, "egress", "test-phone", time.Now().Add(time.Hour))
	registryPath := filepath.Join(dir, "registry.json")
	policy := secureRegistry{SchemaVersion: 1, Generation: 1, HubID: "test-hub", Egresses: []secureEgress{{EgressID: "test-phone", State: "active", Credentials: []secureCredential{testSecureCredential(phone, "phone-key-1")}}}}
	writeSecureTestPolicy(t, registryPath, policy)
	if err := initializeSecureRegistry(registryPath, "test-hub"); err != nil {
		t.Fatal(err)
	}
	return &secureTestFixture{dir: dir, ca: ca, hub: hub, phone: phone, policy: policy,
		serverOptions: serverOptions{Transport: secureTCPTransport, TLSCAFile: caPath, TLSCertFile: hub.certPath, TLSKeyFile: hub.keyPath, HubID: "test-hub", EgressRegistryFile: registryPath},
		clientOptions: clientOptions{Transport: secureTCPTransport, TLSCAFile: caPath, TLSCertFile: phone.certPath, TLSKeyFile: phone.keyPath, HubID: "test-hub", EgressID: "test-phone", TLSServerName: "hub.test", Connections: 2, AddressFamily: "auto"},
	}
}

func startSecureTestServer(t *testing.T, fixture *secureTestFixture) (*secureTCPServer, *sessionManager, string) {
	t.Helper()
	s, err := newSecureTCPServer(fixture.serverOptions)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.close()
		t.Fatal(err)
	}
	m := &sessionManager{resolve: "client", maxProxyConns: 96, maxProxyConnsPeer: 48, proxyIdleTimeout: 2 * time.Minute, proxyPreemptIdle: 10 * time.Second, secureTransport: s}
	done := make(chan struct{})
	go func() { defer close(done); _ = s.serve(ln, m) }()
	t.Cleanup(func() {
		_ = ln.Close()
		s.close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("TLS listener did not close")
		}
	})
	return s, m, ln.Addr().String()
}

func waitSecureCondition(t *testing.T, budget time.Duration, description string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %s", budget, description)
}

func startSecureTestClient(t *testing.T, addr string, opts clientOptions) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- clientOnce(secureTCPTransport, addr, "", opts, newTunnelBindController(opts)) }()
	return done
}

func requireSecureClientRejected(t *testing.T, addr string, opts clientOptions) {
	t.Helper()
	done := startSecureTestClient(t, addr, opts)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unauthorized client succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unauthorized client remained connected")
	}
}

func openSecureProxyEcho(t *testing.T, m *sessionManager) (net.Conn, *bufio.Reader, <-chan struct{}) {
	t.Helper()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	var mu sync.Mutex
	var target net.Conn
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			close(closed)
			return
		}
		mu.Lock()
		target = conn
		mu.Unlock()
		_, _ = io.Copy(conn, conn)
		_ = conn.Close()
		close(closed)
	}()
	proxy := httptest.NewServer(http.HandlerFunc(m.handleProxy))
	t.Cleanup(func() {
		proxy.Close()
		echo.Close()
		mu.Lock()
		if target != nil {
			target.Close()
		}
		mu.Unlock()
	})
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echo.Addr(), echo.Addr()); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("CONNECT status=%q err=%v", status, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	payload := []byte("phone-side-echo")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo = %q", got)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, reader, closed
}

func TestSecureTCPRealYamuxConnectAndCredentialRevoke(t *testing.T) {
	f := newSecureTestFixture(t)
	s, m, addr := startSecureTestServer(t, f)
	clientDone := startSecureTestClient(t, addr, f.clientOptions)
	waitSecureCondition(t, 3*time.Second, "authenticated session", func() bool { return m.sessionCount() == 1 })
	conn, reader, targetClosed := openSecureProxyEcho(t, m)
	health := m.sessionHealthSnapshot()
	if health.SessionCount != 1 || health.Sessions[0].Security == nil || health.Sessions[0].Security.EgressID != "test-phone" || health.ReverseSecurity.Degraded || health.MaxProxyConnections != 96 || health.MaxProxyConnectionsPerClient != 48 || health.ProxyPreemptIdleMillis != 10000 {
		t.Fatalf("health lost auth/scheduler invariants: %+v", health)
	}
	f.policy.Generation++
	f.policy.Egresses[0].Credentials[0].State = "revoked"
	started := time.Now()
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	_ = conn.SetReadDeadline(started.Add(5 * time.Second))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("active proxy stream survived revoke")
	}
	select {
	case <-targetClosed:
	case <-time.After(time.Second):
		t.Fatal("phone target connection survived revoke")
	}
	select {
	case <-clientDone:
	case <-time.After(time.Second):
		t.Fatal("phone yamux session survived revoke")
	}
	waitSecureCondition(t, time.Second, "revoked session removed", func() bool { return m.sessionCount() == 0 })
	if time.Since(started) > 5*time.Second || s.health().ClosedUnauthorizedSessions != 1 {
		t.Fatalf("closure outside budget or missing metric: %+v", s.health())
	}
	requireSecureClientRejected(t, addr, f.clientOptions)
}

func TestSecureTCPRejectsWrongHubAndUnregisteredIdentities(t *testing.T) {
	for _, scenario := range []string{"wrong-hub-uri", "wrong-hub-hostname", "wrong-ca", "unregistered-egress", "unregistered-leaf", "wrong-role", "expired", "revoked-egress"} {
		t.Run(scenario, func(t *testing.T) {
			f := newSecureTestFixture(t)
			opts := f.clientOptions
			switch scenario {
			case "wrong-hub-uri":
				opts.HubID = "another-hub"
			case "wrong-hub-hostname":
				opts.TLSServerName = "wrong.test"
			case "wrong-ca":
				ca := makeSecureTestCA(t)
				opts.TLSCAFile = filepath.Join(f.dir, "wrong-ca.pem")
				if err := os.WriteFile(opts.TLSCAFile, ca.pem, 0600); err != nil {
					t.Fatal(err)
				}
			case "unregistered-egress":
				leaf := makeSecureTestLeaf(t, f.ca, f.dir, "egress", "another-phone", time.Now().Add(time.Hour))
				opts.EgressID = "another-phone"
				opts.TLSCertFile, opts.TLSKeyFile = leaf.certPath, leaf.keyPath
			case "unregistered-leaf":
				leaf := makeSecureTestLeaf(t, f.ca, f.dir, "egress", "test-phone", time.Now().Add(time.Hour))
				opts.TLSCertFile, opts.TLSKeyFile = leaf.certPath, leaf.keyPath
			case "wrong-role":
				opts.TLSCertFile, opts.TLSKeyFile = f.hub.certPath, f.hub.keyPath
			case "expired":
				leaf := makeSecureTestLeaf(t, f.ca, f.dir, "egress", "test-phone", time.Now().Add(-time.Minute))
				opts.TLSCertFile, opts.TLSKeyFile = leaf.certPath, leaf.keyPath
			case "revoked-egress":
				f.policy.Generation++
				f.policy.Egresses[0].State = "revoked"
				writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
			}
			_, m, addr := startSecureTestServer(t, f)
			requireSecureClientRejected(t, addr, opts)
			if m.sessionCount() != 0 {
				t.Fatal("unauthorized identity entered scheduler")
			}
		})
	}
}

func TestSecureTCPActiveCredentialExpiresAndPolicyReadFailureCloses(t *testing.T) {
	for _, scenario := range []string{"credential-expiry", "registry-missing", "registry-malformed", "trust-missing", "watermark-missing", "egress-revoke"} {
		t.Run(scenario, func(t *testing.T) {
			f := newSecureTestFixture(t)
			if scenario == "credential-expiry" {
				f.policy.Generation++
				f.policy.Egresses[0].Credentials[0].ExpiresAt = time.Now().Add(2 * time.Second)
				writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
			}
			s, m, addr := startSecureTestServer(t, f)
			clientDone := startSecureTestClient(t, addr, f.clientOptions)
			waitSecureCondition(t, 3*time.Second, "authenticated session", func() bool { return m.sessionCount() == 1 })
			conn, reader, targetClosed := openSecureProxyEcho(t, m)
			switch scenario {
			case "egress-revoke":
				f.policy.Generation++
				f.policy.Egresses[0].State = "revoked"
				writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
			case "watermark-missing":
				if err := os.Remove(f.serverOptions.EgressRegistryFile + ".accepted"); err != nil {
					t.Fatal(err)
				}
			case "registry-missing":
				if err := os.Remove(f.serverOptions.EgressRegistryFile); err != nil {
					t.Fatal(err)
				}
			case "registry-malformed":
				if err := os.WriteFile(f.serverOptions.EgressRegistryFile, []byte("{} broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "trust-missing":
				if err := os.Remove(f.serverOptions.TLSCAFile); err != nil {
					t.Fatal(err)
				}
			}
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := reader.ReadByte(); err == nil {
				t.Fatal("stream survived expired/unavailable authority")
			}
			select {
			case <-targetClosed:
			case <-time.After(time.Second):
				t.Fatal("phone target survived")
			}
			select {
			case <-clientDone:
			case <-time.After(time.Second):
				t.Fatal("phone session survived")
			}
			if scenario != "credential-expiry" && scenario != "egress-revoke" && (!s.health().Degraded || s.health().ErrorCode == "") {
				t.Fatal("reload failure not observable")
			}
		})
	}
}

func TestSecureTCPRotationHasBoundedOverlapAndDualSessions(t *testing.T) {
	f := newSecureTestFixture(t)
	f.policy.Generation++
	f.policy.Egresses[0].Credentials[0].ExpiresAt = time.Now().Add(10 * time.Minute)
	newLeaf := makeSecureTestLeaf(t, f.ca, f.dir, "egress", "test-phone", time.Now().Add(time.Hour))
	newCredential := testSecureCredential(newLeaf, "phone-key-2")
	newCredential.NotBefore = time.Now().Add(-time.Second)
	f.policy.Egresses[0].Credentials = append(f.policy.Egresses[0].Credentials, newCredential)
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	s, m, addr := startSecureTestServer(t, f)
	oldDone := startSecureTestClient(t, addr, f.clientOptions)
	newOpts := f.clientOptions
	newOpts.TLSCertFile, newOpts.TLSKeyFile = newLeaf.certPath, newLeaf.keyPath
	newDone := startSecureTestClient(t, addr, newOpts)
	waitSecureCondition(t, 3*time.Second, "dual old/new sessions", func() bool { return m.sessionCount() == 2 })
	requireSecureClientRejected(t, addr, f.clientOptions)
	f.policy.Generation++
	f.policy.Egresses[0].Credentials[0].State = "revoked"
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	select {
	case <-oldDone:
	case <-time.After(5 * time.Second):
		t.Fatal("old credential session survived rotation revoke")
	}
	waitSecureCondition(t, time.Second, "new credential session preserved", func() bool { return m.sessionCount() == 1 })
	select {
	case err := <-newDone:
		t.Fatalf("new credential unexpectedly closed: %v", err)
	default:
	}
	conn, _, _ := openSecureProxyEcho(t, m)
	conn.Close()
	if s.health().RegistryGeneration != 3 {
		t.Fatal("generation not observed")
	}
}

func TestSecureTCPRegistryRollbackIsRejectedAcrossRestart(t *testing.T) {
	f := newSecureTestFixture(t)
	s, err := newSecureTCPServer(f.serverOptions)
	if err != nil {
		t.Fatal(err)
	}
	f.policy.Generation = 2
	f.policy.Egresses[0].State = "revoked"
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	if err := s.refresh(); err != nil {
		t.Fatal(err)
	}
	s.close()
	f.policy.Generation = 1
	f.policy.Egresses[0].State = "active"
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	if next, err := newSecureTCPServer(f.serverOptions); err == nil {
		next.close()
		t.Fatal("restart accepted old grant")
	}
	f.policy.Generation = 2
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	if next, err := newSecureTCPServer(f.serverOptions); err == nil {
		next.close()
		t.Fatal("restart accepted modified same generation")
	}
}

func TestSecureTCPRegistryOverlapAndOwnerLock(t *testing.T) {
	f := newSecureTestFixture(t)
	s, err := newSecureTCPServer(f.serverOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if other, err := newSecureTCPServer(f.serverOptions); err == nil {
		other.close()
		t.Fatal("two listener owners accepted same registry")
	}
	leaf := makeSecureTestLeaf(t, f.ca, f.dir, "egress", "test-phone", time.Now().Add(time.Hour))
	f.policy.Generation++
	f.policy.Egresses[0].Credentials = append(f.policy.Egresses[0].Credentials, testSecureCredential(leaf, "phone-key-2"))
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	if err := s.refresh(); err == nil || !s.health().Degraded {
		t.Fatal("unbounded credential overlap accepted")
	}
}

func TestSecureTCPNeverFallsBackToPlaintextOrQUICPin(t *testing.T) {
	f := newSecureTestFixture(t)
	_, m, addr := startSecureTestServer(t, f)
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(raw, "ZHREV1 shared-token\n")
	buf := make([]byte, 64)
	_, _ = raw.Read(buf)
	raw.Close()
	if m.sessionCount() != 0 {
		t.Fatal("plaintext entered TLS scheduler")
	}
	plain, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	received := make(chan []byte, 1)
	go func() {
		conn, err := plain.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		data := make([]byte, 1024)
		n, _ := conn.Read(data)
		received <- data[:n]
	}()
	requireSecureClientRejected(t, plain.Addr().String(), f.clientOptions)
	select {
	case data := <-received:
		if len(data) == 0 || data[0] != 0x16 || bytes.Contains(data, []byte("ZHREV1")) || bytes.Contains(data, []byte("ZHREV2")) {
			t.Fatalf("unexpected plaintext/fallback data: %x", data)
		}
	case <-time.After(time.Second):
		t.Fatal("no TLS ClientHello")
	}
	for _, mutate := range []func(*clientOptions){func(o *clientOptions) { o.InsecureSkipVerify = true }, func(o *clientOptions) { o.ServerCertSHA256 = strings.Repeat("0", 64) }, func(o *clientOptions) { o.Token = "legacy" }, func(o *clientOptions) { o.TokenFile = "legacy" }} {
		opts := f.clientOptions
		mutate(&opts)
		if err := validateSecureClientOptions(opts); err == nil {
			t.Fatal("unsafe secure transport override accepted")
		}
	}
}

func TestSecureTCPServerRejectsInjectedWrongRoleAndExpiredCertificates(t *testing.T) {
	for _, role := range []string{"hub", "expired-egress", "unknown-egress"} {
		t.Run(role, func(t *testing.T) {
			f := newSecureTestFixture(t)
			s, m, addr := startSecureTestServer(t, f)
			config, _, err := secureClientTLSConfig(f.clientOptions)
			if err != nil {
				t.Fatal(err)
			}
			leaf := f.hub
			if role == "expired-egress" {
				leaf = makeSecureTestLeaf(t, f.ca, f.dir, "egress", "test-phone", time.Now().Add(-time.Minute))
			}
			if role == "unknown-egress" {
				leaf = makeSecureTestLeaf(t, f.ca, f.dir, "egress", "rogue-phone", time.Now().Add(time.Hour))
			}
			config.Certificates = []tls.Certificate{leaf.pair}
			dialer := &net.Dialer{Timeout: time.Second}
			conn, err := tls.DialWithDialer(dialer, "tcp", addr, config)
			if err == nil {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				_, _ = io.WriteString(conn, secureTCPHello)
				if ack, err := readLineBytewise(conn, 64); err == nil && ack == secureTCPAck {
					t.Fatal("Hub authorized injected certificate")
				}
			}
			waitSecureCondition(t, time.Second, "server rejection metric", func() bool { return s.health().RejectedHandshakes > 0 })
			if m.sessionCount() != 0 {
				t.Fatal("injected certificate entered scheduler")
			}
		})
	}
}

func TestSecureRegistryRejectsDuplicateKeysAndUnknownFields(t *testing.T) {
	for _, data := range []string{
		`{"schema_version":1,"schema_version":1,"generation":1,"hub_id":"test-hub","egresses":[]}`,
		`{"schema_version":1,"generation":1,"hub_id":"test-hub","egresses":[{"egress_id":"a","state":"active","state":"revoked","credentials":[]}]}`,
		`{"schema_version":1,"generation":1,"hub_id":"test-hub","egresses":[],"allow_everyone":true}`,
	} {
		var policy secureRegistry
		if err := decodeSecureJSON([]byte(data), &policy); err == nil {
			t.Fatal("ambiguous/unknown policy accepted")
		}
	}
}

func TestSecureTCPHubCertificateRotationAndTrustRetirement(t *testing.T) {
	f := newSecureTestFixture(t)
	// A single private PEM bundle provides an atomic certificate/key snapshot.
	bundle := filepath.Join(f.dir, "hub-bundle.pem")
	certData, _ := os.ReadFile(f.hub.certPath)
	keyData, _ := os.ReadFile(f.hub.keyPath)
	if err := os.WriteFile(bundle, append(certData, keyData...), 0600); err != nil {
		t.Fatal(err)
	}
	f.serverOptions.TLSCertFile, f.serverOptions.TLSKeyFile = bundle, bundle
	clientRoots := filepath.Join(f.dir, "phone-trust.pem")
	if err := os.WriteFile(clientRoots, f.ca.pem, 0600); err != nil {
		t.Fatal(err)
	}
	f.clientOptions.TLSCAFile = clientRoots
	s, m, addr := startSecureTestServer(t, f)
	oldDone := startSecureTestClient(t, addr, f.clientOptions)
	waitSecureCondition(t, 3*time.Second, "old Hub session", func() bool { return m.sessionCount() == 1 })
	newCA := makeSecureTestCA(t)
	newHub := makeSecureTestLeaf(t, newCA, f.dir, "hub", "test-hub", time.Now().Add(2*time.Hour))
	newPhone := makeSecureTestLeaf(t, newCA, f.dir, "egress", "test-phone", time.Now().Add(time.Hour))
	allRoots := append(append([]byte(nil), f.ca.pem...), newCA.pem...)
	writeSecureTestData(t, f.serverOptions.TLSCAFile, allRoots)
	writeSecureTestData(t, clientRoots, allRoots)
	f.policy.Generation++
	f.policy.Egresses[0].Credentials[0].ExpiresAt = time.Now().Add(10 * time.Minute)
	newCredential := testSecureCredential(newPhone, "phone-key-2")
	newCredential.NotBefore = time.Now().Add(-time.Second)
	f.policy.Egresses[0].Credentials = append(f.policy.Egresses[0].Credentials, newCredential)
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	newCertData, _ := os.ReadFile(newHub.certPath)
	newKeyData, _ := os.ReadFile(newHub.keyPath)
	stage := bundle + ".new"
	if err := os.WriteFile(stage, append(newCertData, newKeyData...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceSecureFile(stage, bundle); err != nil {
		t.Fatal(err)
	}
	if err := s.refresh(); err != nil {
		t.Fatal(err)
	}
	newOpts := f.clientOptions
	newOpts.TLSCertFile, newOpts.TLSKeyFile = newPhone.certPath, newPhone.keyPath
	newDone := startSecureTestClient(t, addr, newOpts)
	waitSecureCondition(t, 3*time.Second, "both CA generations", func() bool { return m.sessionCount() == 2 })
	select {
	case err := <-oldDone:
		t.Fatalf("bounded trust overlap closed old session: %v", err)
	default:
	}
	f.policy.Generation++
	f.policy.Egresses[0].Credentials[0].State = "revoked"
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	// Retire the old authority; old retained chains cannot survive a CA update.
	writeSecureTestData(t, f.serverOptions.TLSCAFile, newCA.pem)
	writeSecureTestData(t, clientRoots, newCA.pem)
	select {
	case <-oldDone:
	case <-time.After(5 * time.Second):
		t.Fatal("old trust session survived authority retirement")
	}
	waitSecureCondition(t, time.Second, "new CA session remains", func() bool { return m.sessionCount() == 1 })
	select {
	case err := <-newDone:
		t.Fatalf("new trust session closed: %v", err)
	default:
	}
	conn, _, _ := openSecureProxyEcho(t, m)
	conn.Close()
}

func TestSecureTCPLateAdmissionAfterRevoke(t *testing.T) {
	f := newSecureTestFixture(t)
	s, m, addr := startSecureTestServer(t, f)
	config, _, err := secureClientTLSConfig(f.clientOptions)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, config)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Finish mutual TLS while authorized, but withhold the application hello.
	f.policy.Generation++
	f.policy.Egresses[0].State = "revoked"
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	if err := s.refresh(); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(conn, secureTCPHello)
	if ack, err := readLineBytewise(conn, 64); err == nil && ack == secureTCPAck {
		t.Fatal("late hello admitted revoked identity")
	}
	if m.sessionCount() != 0 {
		t.Fatal("late session entered scheduler")
	}
}

func TestSecureTCPRetainedCertificateExpiryClosesStreams(t *testing.T) {
	for _, role := range []string{"hub", "egress"} {
		t.Run(role, func(t *testing.T) {
			f := newSecureTestFixture(t)
			id := "test-hub"
			if role == "egress" {
				id = "test-phone"
			}
			leaf := makeSecureTestLeaf(t, f.ca, f.dir, role, id, time.Now().Add(3*time.Second))
			if role == "hub" {
				f.serverOptions.TLSCertFile, f.serverOptions.TLSKeyFile = leaf.certPath, leaf.keyPath
			} else {
				f.policy.Generation++
				f.clientOptions.TLSCertFile, f.clientOptions.TLSKeyFile = leaf.certPath, leaf.keyPath
				f.policy.Egresses[0].Credentials[0] = testSecureCredential(leaf, "expiring")
				writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
			}
			_, m, addr := startSecureTestServer(t, f)
			done := startSecureTestClient(t, addr, f.clientOptions)
			waitSecureCondition(t, time.Second, "short-lived certificate session", func() bool { return m.sessionCount() == 1 })
			conn, reader, targetClosed := openSecureProxyEcho(t, m)
			_ = conn.SetReadDeadline(leaf.cert.NotAfter.Add(5 * time.Second))
			if _, err := reader.ReadByte(); err == nil {
				t.Fatal("retained expired certificate survived")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("expired TLS session remained")
			}
			select {
			case <-targetClosed:
			case <-time.After(time.Second):
				t.Fatal("expired TLS stream target remained")
			}
		})
	}
}

func TestSecureRegistryRejectsHardlinkAliases(t *testing.T) {
	f := newSecureTestFixture(t)
	alias := f.serverOptions.EgressRegistryFile + ".alias"
	if err := os.Link(f.serverOptions.EgressRegistryFile, alias); err != nil {
		t.Skipf("hardlink unavailable: %v", err)
	}
	if server, err := newSecureTCPServer(f.serverOptions); err == nil {
		server.close()
		t.Fatal("hardlinked authority accepted")
	}
	f.serverOptions.EgressRegistryFile = alias
	if server, err := newSecureTCPServer(f.serverOptions); err == nil {
		server.close()
		t.Fatal("hardlink alias authority accepted")
	}
}

func TestSecureRegistryRequiresExplicitOneTimeInitialization(t *testing.T) {
	f := newSecureTestFixture(t)
	if err := run([]string{"registry", "init", "--egress-registry-file", f.serverOptions.EgressRegistryFile, "--hub-id", "test-hub"}); err == nil {
		t.Fatal("reinitialization accepted existing state")
	}
	if err := os.Remove(f.serverOptions.EgressRegistryFile + ".accepted"); err != nil {
		t.Fatal(err)
	}
	if s, err := newSecureTCPServer(f.serverOptions); err == nil {
		s.close()
		t.Fatal("server silently initialized missing durable state")
	}
	f.policy.Generation = 2
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	if err := initializeSecureRegistry(f.serverOptions.EgressRegistryFile, "test-hub"); err == nil {
		t.Fatal("new registry accepted non-initial generation")
	}
	f.policy.Generation = 1
	writeSecureTestPolicy(t, f.serverOptions.EgressRegistryFile, f.policy)
	if err := run([]string{"registry", "init", "--egress-registry-file", f.serverOptions.EgressRegistryFile, "--hub-id", "test-hub"}); err != nil {
		t.Fatal(err)
	}
	s, err := newSecureTCPServer(f.serverOptions)
	if err != nil {
		t.Fatal(err)
	}
	s.close()
}
