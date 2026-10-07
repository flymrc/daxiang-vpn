//go:build integration && with_gvisor

package deviceapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zongheng-vpn/hub/internal/deviceauth"
	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

func TestRealWireGuardSchedulerRevocationPreservesProtectedPeer(t *testing.T) {
	f := newRealWGFixture(t)
	customer := newRealWGNode(t, newRealWGKey(t), "10.250.0.2/32", nil)
	t.Cleanup(customer.close)
	customer.routeTo(t, f.executor.hub)
	apply := f.enrollAndApply(t, customer)
	f.requireBlocked(customer, "not-effective-before-scheduler")
	f.requireMarker(f.protected, "protected-before-apply")
	f.tick()
	d, err := f.store.Device(context.Background(), "owned-customer")
	if err != nil || d.Generation != 1 || d.AppliedGeneration != 1 {
		t.Fatal("actual apply generation not effective")
	}
	f.requireMarker(customer, "customer-applied")
	f.requirePeerTraffic(customer.key.publicBase64(), "10.250.0.2/32")
	f.requirePeerTraffic(f.protected.key.publicBase64(), "10.250.0.3/32")
	revoke, err := f.store.Submit(context.Background(), deviceauth.Command{Actor: deviceauth.Actor{ID: "owned-actor", OwnerID: "owned-owner"}, DeviceID: "owned-customer", Action: "revoke", IdempotencyKey: "owned-revoke", ExpectedGeneration: 1, Reason: "owned fixture revocation"})
	if err != nil {
		t.Fatal(err)
	}
	if revoke.State != "pending" {
		t.Fatal("revoke effective before runtime reconciliation")
	}
	f.tick()
	f.requireAbsent(customer.key.publicBase64())
	f.requireBlocked(customer, "after-revoke")
	f.requireMarker(f.protected, "protected-after-revoke")
	if err := f.store.Process(context.Background(), apply.ID, f.executor); err != nil && !errors.Is(err, deviceauth.ErrSuperseded) {
		t.Fatalf("old apply reconciliation: %v", err)
	}
	f.requireAbsent(customer.key.publicBase64())
	f.requireBlocked(customer, "after-late-apply")
	_, err = f.store.Submit(context.Background(), deviceauth.Command{Actor: deviceauth.Actor{ID: "owned-actor", OwnerID: "owned-owner"}, DeviceID: "owned-customer", Action: "apply", IdempotencyKey: "owned-stale-apply", ExpectedGeneration: 0, PublicKey: customer.key.publicBase64(), Address: "10.250.0.2/32"})
	if err == nil {
		t.Fatal("stale generation accepted after revocation")
	}
	// A real user-space WireGuard restart is loaded with old configuration. The
	// startup reconciliation runs before service readiness or customer traffic.
	f.reopenAuthority()
	f.restoreOldRuntime(t, customer)
	f.tick()
	f.requireAbsent(customer.key.publicBase64())
	f.requireBlocked(customer, "after-stale-runtime-restart")
	f.requireMarker(f.protected, "protected-after-restart")
	f.requirePeerTraffic(f.protected.key.publicBase64(), "10.250.0.3/32")
	d, err = f.store.Device(context.Background(), "owned-customer")
	if err != nil || d.State != "revoked" || d.Generation != 2 || d.AppliedGeneration != 2 {
		t.Fatal("persisted revoke tombstone lost after actual runtime restart")
	}
}

type realCLIDataReceipt struct {
	Version          int    `json:"contract_version"`
	Command          string `json:"command"`
	OK               bool   `json:"ok"`
	Outcome, Code    string
	Pending          bool           `json:"pending"`
	Credential       *dc.Credential `json:"credential"`
	Operation        *dc.Operation  `json:"operation"`
	EngineState      string         `json:"engine_state"`
	InstanceID       string         `json:"instance_id"`
	ConfigGeneration string         `json:"config_generation"`
	Proxy            string         `json:"proxy"`
	Generation       int64          `json:"generation"`
}

func realCLIEnv(home string, cgo bool) []string {
	var env []string
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.EqualFold(name, "ZHVPN_HOME") || (cgo && strings.EqualFold(name, "CGO_ENABLED")) {
			continue
		}
		env = append(env, value)
	}
	if home != "" {
		env = append(env, "ZHVPN_HOME="+home)
	}
	if cgo {
		env = append(env, "CGO_ENABLED=0")
	}
	return env
}

func TestRealCLIProxyBootstrapToWireGuardOwnedTargetAndRevoke(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Linux product engine launch is unsupported; separate actual WG fixture tests still run")
	}
	f := newRealWGFixture(t)
	handler, err := NewServerWithProfile(f.store, realWGProfile(f), "ownedfixture0")
	if err != nil {
		t.Fatal(err)
	}
	var authorityRequests atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { authorityRequests.Add(1); handler.ServeHTTP(w, r) }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	dir := t.TempDir()
	cli := filepath.Join(dir, "zhvpn-owned-wg-fixture"+cliExecutableSuffix())
	repository, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-tags", "with_gvisor", "-buildvcs=false", "-o", cli, "./clients/cli")
	build.Dir, build.Env = repository, realCLIEnv("", true)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("normal CGO=0 fixture CLI build failed: %s", output)
	}
	home := filepath.Join(dir, "owned-data-client-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	prepareCLIPrivateHome(t, home)
	homeContext := paths.FromRoot(home)
	t.Cleanup(func() {
		if _, err := proxy.Stop(homeContext); err != nil {
			t.Errorf("authenticated owned engine cleanup failed: %v", err)
		}
	})
	ca := filepath.Join(dir, "owned-fixture-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	activation, err := f.store.IssueActivation(context.Background(), "owned-cli-owner", time.Now().Add(time.Hour), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var secrets []string
	secrets = append(secrets, activation.Credential)
	call := func(input string, success bool, args ...string) realCLIDataReceipt {
		t.Helper()
		commandCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		arguments := append([]string{"device"}, args...)
		arguments = append(arguments, "--ca-file", ca)
		command := exec.CommandContext(commandCtx, cli, arguments...)
		command.Dir, command.Env, command.Stdin = repository, realCLIEnv(home, false), strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		for _, secret := range secrets {
			if secret != "" && (strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret)) {
				t.Fatal("private fixture material in CLI diagnostics")
			}
		}
		if stderr.Len() != 0 || strings.Contains(stdout.String(), "private_key") || strings.Contains(stdout.String(), "control_secret") || strings.Contains(stdout.String(), home) {
			t.Fatal("unexpected raw CLI diagnostic")
		}
		var receipt realCLIDataReceipt
		if json.Unmarshal(stdout.Bytes(), &receipt) != nil || receipt.Version != 2 || receipt.Command != args[0] || receipt.OK != success || (err == nil) != success {
			t.Fatalf("real CLI %s exit/receipt disagreement; outcome=%s code=%s", args[0], receipt.Outcome, receipt.Code)
		}
		return receipt
	}
	activated := call(activation.Credential, true, "activate", "--server", server.URL, "--activation-stdin")
	if activated.Credential == nil {
		t.Fatal("real CLI activation receipt missing")
	}
	bound := call("", true, "bind", "--address", "10.250.0.2/32", "--expected-generation", "0", "--idempotency-key", "owned-real-cli-bind")
	if bound.Operation == nil || !bound.Operation.Accepted || bound.Operation.Effective || bound.Operation.Action != string(dc.Apply) || bound.Operation.Generation != 1 {
		t.Fatal("real bind was not accepted-only before actual reconciliation")
	}
	local, err := proxy.NewPrivateState(homeContext, "device-v2-state.json")
	if err != nil {
		t.Fatal(err)
	}
	stateBytes, err := local.Read()
	if err != nil {
		t.Fatal("read owned private key state")
	}
	var privateState map[string]any
	if json.Unmarshal(stateBytes, &privateState) != nil {
		t.Fatal("owned private state invalid")
	}
	localBinding, ok := privateState["wireguard"].(map[string]any)
	if !ok {
		t.Fatal("bind did not persist owned WireGuard key")
	}
	pub, pubOK := localBinding["public_key"].(string)
	private, privateOK := localBinding["private_key"].(string)
	if !pubOK || !privateOK || pub == "" || private == "" {
		t.Fatal("owned WireGuard key fields missing")
	}
	secrets = append(secrets, private)
	if auth, ok := privateState["private_key"].(string); ok {
		secrets = append(secrets, auth)
	}
	device, err := f.store.Device(context.Background(), activated.Credential.DeviceId)
	if err != nil || device.PublicKey != pub || device.Address != "10.250.0.2/32" || device.AppliedGeneration != 0 {
		t.Fatal("CLI bind key differs from durable authority desired binding")
	}
	// Both corruption cases use the real protected private file, real CLI parser
	// and actual TLS request counter. Neither may silently generate another key.
	for _, corrupt := range []func(map[string]any){
		func(st map[string]any) {
			st["wireguard"].(map[string]any)["public_key"] = newRealWGKey(t).publicBase64()
		},
		func(st map[string]any) { delete(st, "wireguard") },
	} {
		var changed map[string]any
		if json.Unmarshal(stateBytes, &changed) != nil {
			t.Fatal("decode private corruption fixture")
		}
		corrupt(changed)
		bad, _ := json.Marshal(changed)
		if local.Write(bad) != nil {
			t.Fatal("save protected corruption fixture")
		}
		before := authorityRequests.Load()
		call("", false, "start", "--expected-generation", "1", "--port", "17891")
		if authorityRequests.Load() != before {
			t.Fatal("corrupt local WG identity reached authority or attempted regeneration")
		}
		if local.Write(stateBytes) != nil {
			t.Fatal("restore owned private state")
		}
	}
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	portListener.Close()
	startArgs := []string{"start", "--expected-generation", "1", "--port", strconv.Itoa(port)}
	call("", false, startArgs...)
	if live, err := proxy.Inspect(homeContext); err != nil || live.State != "stopped" {
		t.Fatal("unapplied real bind created an engine")
	}
	f.tick()
	status := call("", true, "status", "--operation-id", bound.Operation.OperationId)
	if status.Operation == nil || !status.Operation.Effective || status.Operation.Generation != 1 {
		t.Fatal("real CLI did not observe actual runtime convergence")
	}
	// A foreign listener must survive an otherwise valid authorized start. The
	// real CLI must refuse before writing a cache or private launch config.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("open owned foreign listener")
	}
	occupiedPort := occupied.Addr().(*net.TCPAddr).Port
	busy := call("", false, "start", "--expected-generation", "1", "--port", strconv.Itoa(occupiedPort))
	if busy.Code != "local_port_occupied" || busy.Outcome != "rejected" || busy.InstanceID != "" || busy.ConfigGeneration != "" {
		occupied.Close()
		t.Fatal("occupied port was mistaken for an authorized engine")
	}
	for _, path := range []string{homeContext.ConfigPath, homeContext.SingBoxConfig} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			occupied.Close()
			t.Fatal("occupied port refusal wrote a cache or launch configuration")
		}
	}
	if live, err := proxy.Inspect(homeContext); err != nil || live.State != "stopped" {
		occupied.Close()
		t.Fatal("occupied port refusal launched or claimed an engine")
	}
	probe, err := net.DialTimeout("tcp", occupied.Addr().String(), time.Second)
	if err != nil {
		occupied.Close()
		t.Fatal("occupied port refusal stopped the foreign listener")
	}
	probe.Close()
	occupied.Close()
	started := call("", true, startArgs...)
	if started.Outcome != "engine_ready" || started.EngineState != "ready" || started.InstanceID == "" || started.ConfigGeneration == "" || started.Generation != 1 || started.Proxy != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) {
		t.Fatal("real v2 engine receipt lacks authenticated local readiness")
	}
	if live, err := proxy.Inspect(homeContext); err != nil || live.State != "ready" || live.Identity.InstanceID != started.InstanceID || live.Identity.Generation != started.ConfigGeneration {
		t.Fatal("receipt was not bound to actual engine instance/configuration")
	}
	localProxyURL, _ := url.Parse("http://" + started.Proxy)
	request := func(marker string, timeout time.Duration) (string, error) {
		transport := &http.Transport{Proxy: http.ProxyURL(localProxyURL), DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", f.target.URL+"/owned-marker/"+marker, nil)
		if err != nil {
			return "", err
		}
		response, err := (&http.Client{Transport: transport}).Do(req)
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
		if err != nil || response.StatusCode != 200 {
			return "", errors.New("owned actual CLI marker refused")
		}
		return string(body), nil
	}
	before := f.marker.Load()
	if body, err := request("real-cli-before-revoke", 5*time.Second); err != nil || body != "owned-wg-target:/owned-marker/real-cli-before-revoke" || f.marker.Load() != before+1 {
		t.Fatalf("real CLI/sing-box/WG/proxy/target path failed: %v", err)
	}
	f.requirePeerTraffic(pub, "10.250.0.2/32")
	f.requireMarker(f.protected, "real-cli-protected-before-revoke")
	if r := call("", false, startArgs...); r.Code != "engine_already_present" {
		t.Fatal("v2 start took over or relabeled an existing engine")
	}
	revoked := call("", true, "revoke", "--expected-generation", "1", "--idempotency-key", "owned-real-cli-revoke")
	if revoked.Operation == nil || revoked.Operation.Effective {
		t.Fatal("real revoke became effective before actual WG removal")
	}
	f.tick()
	f.requireAbsent(pub)
	before = f.marker.Load()
	if _, err := request("real-cli-after-revoke", 750*time.Millisecond); err == nil || f.marker.Load() != before {
		t.Fatal("revoked real CLI new request reached owned target")
	}
	f.requireMarker(f.protected, "real-cli-protected-after-revoke")
	f.requirePeerTraffic(f.protected.key.publicBase64(), "10.250.0.3/32")
	if err := f.store.Process(context.Background(), bound.Operation.OperationId, f.executor); err != nil && !errors.Is(err, deviceauth.ErrSuperseded) {
		t.Fatalf("delayed real CLI apply reconciliation: %v", err)
	}
	f.requireAbsent(pub)
	if _, err := proxy.Stop(homeContext); err != nil {
		t.Fatal("authenticated owned engine stop failed")
	}
	call("", false, startArgs...)
	if live, err := proxy.Inspect(homeContext); err != nil || live.State != "stopped" {
		t.Fatal("cached v2 state restarted an engine after durable revoke")
	}
	if _, err := os.Stat(homeContext.WireGuardKeyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("v2 path read or created legacy WireGuard key")
	}
	if _, err := os.Stat(homeContext.SingBoxConfig); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private launch configuration retained after engine start")
	}
}

func realWGProfile(f *realWGFixture) dc.ProxyRouteProfile {
	return dc.ProxyRouteProfile{Version: 1, AuthorityEpoch: realWGFixtureEpoch, ManagedBy: realWGFixtureManager, WgInterface: "ownedfixture0", Revision: 1, WgEndpoint: f.executor.hub.endpoint, WgPublicKey: f.executor.hub.key.publicBase64(), ProxyAddress: realWGFixtureProxy, EgressId: "owned-fixture", EgressName: "Owned isolated target", AllowedIps: []string{"10.250.0.1/32"}}
}

func realWGActivate(t *testing.T, f *realWGFixture, server *httptest.Server) *wireClient {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("create owned authentication key")
	}
	c := &wireClient{t: t, url: server.URL, client: server.Client(), pub: base64.StdEncoding.EncodeToString(pub), priv: priv}
	activation, err := f.store.IssueActivation(context.Background(), "owned-tls-owner", time.Now().Add(time.Hour), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	body := encoded(t, dc.ActivationRequest{ActivationCredential: activation.Credential, AuthPublicKey: c.pub})
	q := c.proof("activate", "POST", "/api/v2/activate", body, activation.Credential)
	code, data := c.call("POST", "/api/v2/activate", body, &q, nil)
	if code != 201 || json.Unmarshal(data, &c.credential) != nil {
		t.Fatalf("owned TLS activation failed: status=%d", code)
	}
	return c
}

func TestRealWireGuardTLSBootstrapCurrentBindingAndFrozenProfile(t *testing.T) {
	f := newRealWGFixture(t)
	profile := realWGProfile(f)
	digest, err := dc.ProxyRouteProfileDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*dc.ProxyRouteProfile){
		func(p *dc.ProxyRouteProfile) { p.AuthorityEpoch = "other-epoch" },
		func(p *dc.ProxyRouteProfile) { p.ManagedBy = "other-executor" },
		func(p *dc.ProxyRouteProfile) { p.WgInterface = "another0" },
		func(p *dc.ProxyRouteProfile) { p.AllowedIps = []string{"0.0.0.0/0"} },
		func(p *dc.ProxyRouteProfile) {
			p.ProxyAddress = "127.0.0.1:18080"
			p.AllowedIps = []string{"127.0.0.1/32"}
		},
		func(p *dc.ProxyRouteProfile) {
			p.ProxyAddress = "10.66.0.1:18080"
			p.AllowedIps = []string{"10.66.0.1/32"}
		},
		func(p *dc.ProxyRouteProfile) {
			p.ProxyAddress = "10.250.0.4:18080"
			p.AllowedIps = []string{"10.250.0.4/32"}
		},
	} {
		candidate := profile
		candidate.AllowedIps = append([]string(nil), profile.AllowedIps...)
		change(&candidate)
		if _, err := NewServerWithProfile(f.store, candidate, "ownedfixture0"); err == nil {
			t.Fatal("invalid/unprotected hosting profile accepted")
		}
	}
	handler, err := NewServerWithProfile(f.store, profile, "ownedfixture0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	// The caller retains its slice. Mutating it after construction must not
	// replace a service-lifetime profile or broaden the future route projection.
	profile.AllowedIps[0] = "0.0.0.0/0"
	c := realWGActivate(t, f, server)
	customer := newRealWGNode(t, newRealWGKey(t), "10.250.0.2/32", nil)
	t.Cleanup(customer.close)
	customer.routeTo(t, f.executor.hub)
	public, address := customer.key.publicBase64(), "10.250.0.2/32"
	body := encoded(t, dc.Command{Action: dc.Apply, IdempotencyKey: "owned-tls-apply", ExpectedGeneration: 0, WgPublicKey: &public, Address: &address})
	q := c.proof("command.apply", "POST", "/api/v2/commands", body, "")
	code, data := c.call("POST", "/api/v2/commands", body, &q, nil)
	var operation dc.Operation
	if code != 202 || json.Unmarshal(data, &operation) != nil || !operation.Accepted || operation.Effective {
		t.Fatalf("owned TLS apply did not remain accepted-only: status=%d", code)
	}
	bootstrapBody := encoded(t, dc.ProxyBootstrapRequest{ExpectedGeneration: 1, WireguardPublicKey: public})
	q = c.proof("proxy.bootstrap", "POST", "/api/v2/proxy/bootstrap", bootstrapBody, "")
	if code, _ = c.call("POST", "/api/v2/proxy/bootstrap", bootstrapBody, &q, nil); code == 200 {
		t.Fatal("unapplied generation received a route projection")
	}
	f.tick()
	wrongBody := encoded(t, dc.ProxyBootstrapRequest{ExpectedGeneration: 1, WireguardPublicKey: newRealWGKey(t).publicBase64()})
	q = c.proof("proxy.bootstrap", "POST", "/api/v2/proxy/bootstrap", wrongBody, "")
	if code, _ = c.call("POST", "/api/v2/proxy/bootstrap", wrongBody, &q, nil); code == 200 {
		t.Fatal("unbound current public key received a route projection")
	}
	q = c.proof("proxy.bootstrap", "POST", "/api/v2/proxy/bootstrap", bootstrapBody, "")
	if code, _ = c.call("POST", "/api/v2/proxy/bootstrap", wrongBody, &q, nil); code == 200 {
		t.Fatal("signed raw bootstrap body binding lost")
	}
	q = c.proof("proxy.bootstrap", "POST", "/api/v2/proxy/bootstrap", bootstrapBody, "")
	code, data = c.call("POST", "/api/v2/proxy/bootstrap", bootstrapBody, &q, nil)
	var projection dc.ProxyBootstrap
	if code != 200 || json.Unmarshal(data, &projection) != nil || projection.ProfileSha256 != digest || projection.DeviceId != c.credential.DeviceId || projection.CredentialId != c.credential.CredentialId || projection.WireguardPublicKey != public || projection.Address != address || projection.DesiredGeneration != 1 || projection.AppliedGeneration != 1 || projection.ExpiresUnixSeconds <= projection.IssuedUnixSeconds || projection.ExpiresUnixSeconds > projection.IssuedUnixSeconds+30 || projection.ExpiresUnixSeconds <= time.Now().Unix() || len(projection.Profile.AllowedIps) != 1 || projection.Profile.AllowedIps[0] != "10.250.0.1/32" {
		t.Fatalf("current bound TLS route projection incorrect: status=%d", code)
	}
	if code, _ = c.call("POST", "/api/v2/proxy/bootstrap", bootstrapBody, &q, nil); code == 200 {
		t.Fatal("route proof replay returned a projection")
	}
	f.requireMarker(customer, "tls-authorized-customer")
	f.requirePeerTraffic(public, address)
	late := c.proof("proxy.bootstrap", "POST", "/api/v2/proxy/bootstrap", bootstrapBody, "")
	revokeBody := encoded(t, dc.Command{Action: dc.Revoke, IdempotencyKey: "owned-tls-revoke", ExpectedGeneration: 1})
	q = c.proof("command.revoke", "POST", "/api/v2/commands", revokeBody, "")
	if code, _ = c.call("POST", "/api/v2/commands", revokeBody, &q, nil); code != 202 {
		t.Fatalf("owned TLS revoke status=%d", code)
	}
	f.tick()
	if code, data = c.call("POST", "/api/v2/proxy/bootstrap", bootstrapBody, &late, nil); code == 200 {
		t.Fatal("pre-revoke proof returned a projection after durable revoke")
	}
	var rejected dc.ProxyBootstrap
	if json.Unmarshal(data, &rejected) == nil && rejected.ProfileSha256 != "" {
		t.Fatal("rejected bootstrap leaked usable profile")
	}
	f.requireAbsent(public)
	f.requireBlocked(customer, "tls-revoked-customer")
	f.requireMarker(f.protected, "tls-protected-after-revoke")
	f.requirePeerTraffic(f.protected.key.publicBase64(), "10.250.0.3/32")
}
