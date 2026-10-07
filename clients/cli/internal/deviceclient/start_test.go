package deviceclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

func bindFixture(t *testing.T, a *testAuthority, home paths.Context) Receipt {
	t.Helper()
	activateFixture(t, a, home)
	r, e, _ := invoke(t, a, home, "", "bind", "--address", "10.77.0.10/32", "--expected-generation", "0", "--idempotency-key", "synthetic-bind")
	if e != nil || !r.OK || r.Command != "bind" || r.Operation == nil || r.Operation.Action != "apply" || r.Operation.Effective {
		t.Fatal("bind did not produce independent accepted receipt")
	}
	return r
}
func profileFixture(t *testing.T) dc.ProxyRouteProfile {
	t.Helper()
	b := bytes.Repeat([]byte{42}, 32)
	b[0] &= 248
	b[31] = (b[31] & 127) | 64
	_, pub, e := wireGuardKey(base64.StdEncoding.EncodeToString(b))
	if e != nil {
		t.Fatal(e)
	}
	p := dc.ProxyRouteProfile{Version: 1, AuthorityEpoch: "synthetic-epoch", ManagedBy: "owned-fixture", WgInterface: "wg-test", Revision: 2, WgEndpoint: "127.0.0.1:51820", WgPublicKey: pub, ProxyAddress: "10.77.0.1:18081", EgressId: "synthetic-egress", EgressName: "Fixture", AllowedIps: []string{"10.77.0.1/32"}}
	if dc.ValidateProxyRouteProfile(p) != nil {
		t.Fatal("invalid synthetic profile")
	}
	return p
}
func projectionFixture(t *testing.T, a *testAuthority, home paths.Context) dc.ProxyBootstrap {
	t.Helper()
	s, e := (privateStore{home}).load()
	if e != nil {
		t.Fatal(e)
	}
	p := profileFixture(t)
	digest, e := dc.ProxyRouteProfileDigest(p)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().Unix()
	return dc.ProxyBootstrap{Version: 1, DeviceId: s.Credential.DeviceId, CredentialId: s.Credential.CredentialId, WireguardPublicKey: s.WireGuard.PublicKey, Address: s.WireGuard.Address, DesiredGeneration: s.WireGuard.Generation, AppliedGeneration: s.WireGuard.Generation, IssuedUnixSeconds: now, ExpiresUnixSeconds: now + 30, Profile: p, ProfileSha256: digest}
}
func prepareFixture(t *testing.T, a *testAuthority, home paths.Context) (any, error) {
	t.Helper()
	var value any
	e := proxy.WithOperationLockContext(context.Background(), home, func() error {
		cfg, e := PrepareStartLocked(context.Background(), home, []string{"--ca-file", a.caFile, "--expected-generation", "1", "--port", "17890"})
		if e == nil {
			value = cfg
		}
		return e
	})
	return value, e
}

func TestBindPersistsDistinctLocalWGKeyBeforeApplyAndLostResponseRecoversSameKey(t *testing.T) {
	for _, resolve := range []string{"recover", "cancel-pending"} {
		t.Run(resolve, func(t *testing.T) {
			a := authority(t)
			home := privateHome(t)
			activateFixture(t, a, home)
			legacy, e := proxy.NewPrivateState(home, "wireguard/client.key")
			if e != nil {
				t.Fatal(e)
			}
			if e = legacy.Write([]byte("SYNTHETIC_LEGACY_PRIVATE_CANARY")); e != nil {
				t.Fatal(e)
			}
			a.applyCheck = func(cmd dc.Command) {
				st, e := (privateStore{home}).load()
				if e != nil || st.WireGuard == nil || st.Pending == nil || st.Pending.BindAddress != "10.77.0.10/32" || cmd.WgPublicKey == nil || *cmd.WgPublicKey != st.WireGuard.PublicKey || st.PrivateKey == st.WireGuard.PrivateKey {
					t.Error("apply preceded durable separate local WG key+intent")
				}
			}
			a.loseResponse("/api/v2/commands")
			r, e, out := invoke(t, a, home, "", "bind", "--address", "10.77.0.10/32", "--expected-generation", "0", "--idempotency-key", "stable-bind")
			if e == nil || r.Command != "bind" || !r.Pending || r.Outcome != "result_unknown" {
				t.Fatal("lost bind did not retain mutation intent")
			}
			st, e := (privateStore{home}).load()
			if e != nil {
				t.Fatal(e)
			}
			original := st.WireGuard.PrivateKey
			if strings.Contains(out, original) || strings.Contains(out, "SYNTHETIC_LEGACY") {
				t.Fatal("key appeared in public receipt")
			}
			r, e, _ = invoke(t, a, home, "", "bind", "--address", "10.77.0.11/32", "--expected-generation", "0", "--idempotency-key", "new-bind")
			if e == nil || r.Code != "pending_resolution_required" {
				t.Fatal("unknown bind allowed another bind")
			}
			before, _ := a.counts()
			r, e, _ = invoke(t, a, home, "", resolve)
			if e != nil || r.Outcome != "operation_recovered" {
				t.Fatal("bind receipt resolution failed")
			}
			st, e = (privateStore{home}).load()
			if e != nil || st.Pending != nil || st.WireGuard.PrivateKey != original || st.WireGuard.Generation != 1 || st.WireGuard.Address != "10.77.0.10/32" || st.WireGuard.OperationID != r.Operation.OperationId {
				t.Fatal("recovery did not preserve exact binding key/address/generation")
			}
			after, _ := a.counts()
			if before != after {
				t.Fatal("recovery repeated apply")
			}
			b, _ := legacy.Read()
			if string(b) != "SYNTHETIC_LEGACY_PRIVATE_CANARY" {
				t.Fatal("legacy key was changed")
			}
		})
	}
}

func TestBindWriteFailuresPreventApplyOrRetainOriginalPendingProof(t *testing.T) {
	for _, failAt := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			a := authority(t)
			home := privateHome(t)
			activateFixture(t, a, home)
			st, e := (privateStore{home}).load()
			if e != nil {
				t.Fatal(e)
			}
			c, e := New(a.server.URL, a.server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			store := &failingStore{base: privateStore{home}, failAt: failAt}
			before, _ := a.counts()
			r := c.execute(context.Background(), store, st, options{command: "bind", address: "10.77.0.10/32", generation: 0, idempotency: "stable-bind"}, nil)
			after, _ := a.counts()
			if r.OK || r.Code != "local_storage_failure" {
				t.Fatal("bind falsely succeeded after persistence failure")
			}
			if failAt < 3 && after != before {
				t.Fatal("apply occurred before key+intent persisted")
			}
			if failAt == 3 {
				if after != before+1 || !r.Pending {
					t.Fatal("accepted bind lost recovery proof")
				}
				stored, e := store.load()
				if e != nil || stored.Pending == nil || stored.WireGuard == nil {
					t.Fatal("pending binding lost")
				}
				key := stored.WireGuard.PrivateKey
				r, e, _ = invoke(t, a, home, "", "recover")
				if e != nil || r.Outcome != "operation_recovered" {
					t.Fatal("accepted bind recovery failed")
				}
				stored, e = store.load()
				if e != nil || stored.WireGuard.PrivateKey != key || stored.WireGuard.Generation != 1 {
					t.Fatal("recovery changed binding key")
				}
			}
		})
	}
}

func TestCancelledUncommittedBindKeepsKeyWithoutGrantAndRequiresExplicitNewBind(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	activateFixture(t, a, home)
	a.failBeforeKind = "/api/v2/commands"
	r, e, _ := invoke(t, a, home, "", "bind", "--address", "10.77.0.10/32", "--expected-generation", "0", "--idempotency-key", "cancelled-bind")
	if e == nil || !r.Pending {
		t.Fatal("unconfirmed bind lost intent")
	}
	st, e := (privateStore{home}).load()
	if e != nil {
		t.Fatal(e)
	}
	key := st.WireGuard.PrivateKey
	r, e, _ = invoke(t, a, home, "", "cancel-pending")
	if e != nil || r.Outcome != "cancelled" {
		t.Fatal("negative tombstone did not cancel original request")
	}
	st, e = (privateStore{home}).load()
	if e != nil || st.Pending != nil || st.WireGuard.PrivateKey != key || st.WireGuard.Generation != 0 {
		t.Fatal("cancel discarded WG key or manufactured grant")
	}
	_, e = prepareFixture(t, a, home)
	if FailureCode(e) != "binding_required" {
		t.Fatal("unbound key was prepared")
	}
	r, e, _ = invoke(t, a, home, "", "bind", "--address", "10.77.0.10/32", "--expected-generation", "0", "--idempotency-key", "explicit-new-bind")
	if e != nil || !r.OK {
		t.Fatal("explicit new bind failed")
	}
	st, e = (privateStore{home}).load()
	if e != nil || st.WireGuard.PrivateKey != key {
		t.Fatal("explicit retry rotated old key")
	}
}

func TestPrepareStartConsumesSignedTLSProjectionAndStoresOnlyProfileFloor(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	r := bindFixture(t, a, home)
	status, e, _ := invoke(t, a, home, "", "status", "--operation-id", r.Operation.OperationId)
	if e != nil || !status.Operation.Effective {
		t.Fatal("effective fixture status failed")
	}
	p := projectionFixture(t, a, home)
	a.proxyReply = func(q dc.ProxyBootstrapRequest, cr dc.Credential) (any, int) {
		if q.WireguardPublicKey != p.WireguardPublicKey || q.ExpectedGeneration != 1 || cr.DeviceId != p.DeviceId {
			t.Error("bootstrap body identity mismatch")
		}
		return p, 200
	}
	v, e := prepareFixture(t, a, home)
	if e != nil || v == nil {
		t.Fatalf("TLS projection rejected: %s", FailureCode(e))
	}
	b, _ := json.Marshal(v)
	var cfg map[string]any
	_ = json.Unmarshal(b, &cfg)
	st, e := (privateStore{home}).load()
	if e != nil || st.ProxyProfile == nil || st.WireGuard.AppliedGeneration != 1 {
		t.Fatal("profile floor missing")
	}
	if cfg["authorization"].(map[string]any)["source"] != "device-v2" || cfg["license"].(map[string]any)["token"] != "" {
		t.Fatal("projection used legacy authorization")
	}
	if _, e = os.Stat(home.SingBoxConfig); !os.IsNotExist(e) {
		t.Fatal("prepare wrote runtime config")
	}
	if _, e = os.Stat(home.PIDPath); !os.IsNotExist(e) {
		t.Fatal("prepare started engine")
	}
}

func TestPrepareStartRejectsMalformedStaleOrExpandedProjection(t *testing.T) {
	cases := map[string]func(*dc.ProxyBootstrap){
		"version": func(p *dc.ProxyBootstrap) { p.Version = 2 }, "device": func(p *dc.ProxyBootstrap) { p.DeviceId = strings.Repeat("1", 32) }, "credential": func(p *dc.ProxyBootstrap) { p.CredentialId = strings.Repeat("2", 32) }, "public": func(p *dc.ProxyBootstrap) { p.WireguardPublicKey = wgFixture() }, "address": func(p *dc.ProxyBootstrap) { p.Address = "10.77.0.11/32" }, "desired": func(p *dc.ProxyBootstrap) { p.DesiredGeneration = 2 }, "unapplied": func(p *dc.ProxyBootstrap) { p.AppliedGeneration = 0 }, "old-issued": func(p *dc.ProxyBootstrap) { p.IssuedUnixSeconds -= 60 }, "future-issued": func(p *dc.ProxyBootstrap) { p.IssuedUnixSeconds += 60; p.ExpiresUnixSeconds += 60 }, "expired": func(p *dc.ProxyBootstrap) { p.IssuedUnixSeconds -= 31; p.ExpiresUnixSeconds = p.IssuedUnixSeconds + 30 }, "long-ttl": func(p *dc.ProxyBootstrap) { p.ExpiresUnixSeconds += 60 }, "after-credential": func(p *dc.ProxyBootstrap) { p.ExpiresUnixSeconds += 3600 }, "digest": func(p *dc.ProxyBootstrap) { p.ProfileSha256 = strings.Repeat("0", 64) }, "all-route": func(p *dc.ProxyBootstrap) { p.Profile.AllowedIps = []string{"0.0.0.0/0"} }, "legacy-subnet": func(p *dc.ProxyBootstrap) {
			p.Profile.ProxyAddress = "10.66.0.1:18081"
			p.Profile.AllowedIps = []string{"10.66.0.1/32"}
		}, "dns-endpoint": func(p *dc.ProxyBootstrap) { p.Profile.WgEndpoint = "synthetic.invalid:51820" }, "null-profile": func(p *dc.ProxyBootstrap) { p.Profile = dc.ProxyRouteProfile{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := authority(t)
			home := privateHome(t)
			bindFixture(t, a, home)
			p := projectionFixture(t, a, home)
			mutate(&p)
			a.proxyReply = func(dc.ProxyBootstrapRequest, dc.Credential) (any, int) { return p, 200 }
			before, e := (privateStore{home}).load()
			if e != nil {
				t.Fatal(e)
			}
			v, e := prepareFixture(t, a, home)
			if e == nil || v != nil {
				t.Fatal("invalid projection became startup config")
			}
			after, se := (privateStore{home}).load()
			if se != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected projection changed local authority floor")
			}
		})
	}
}

func TestPrepareStartProfileFloorAndMissingKeyRefuseBeforeNetwork(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	bindFixture(t, a, home)
	p := projectionFixture(t, a, home)
	a.proxyReply = func(dc.ProxyBootstrapRequest, dc.Credential) (any, int) { return p, 200 }
	if _, e := prepareFixture(t, a, home); e != nil {
		t.Fatal("initial profile rejected")
	}
	for _, change := range []string{"epoch", "revision", "same-revision"} {
		t.Run(change, func(t *testing.T) {
			copy := p
			copy.Profile = p.Profile
			copy.Profile.AllowedIps = append([]string(nil), p.Profile.AllowedIps...)
			switch change {
			case "epoch":
				copy.Profile.AuthorityEpoch = "another-epoch"
			case "revision":
				copy.Profile.Revision--
			case "same-revision":
				copy.Profile.EgressName = "changed"
			}
			copy.ProfileSha256, _ = dc.ProxyRouteProfileDigest(copy.Profile)
			a.proxyReply = func(dc.ProxyBootstrapRequest, dc.Credential) (any, int) { return copy, 200 }
			_, e := prepareFixture(t, a, home)
			if e == nil {
				t.Fatal("authority/profile rollback accepted")
			}
		})
	}
	st, e := (privateStore{home}).load()
	if e != nil {
		t.Fatal(e)
	}
	st.WireGuard = nil
	if e = (privateStore{home}).save(st); e != nil {
		t.Fatal(e)
	}
	_, before := a.counts()
	_, e = prepareFixture(t, a, home)
	_, after := a.counts()
	if e == nil || before != after {
		t.Fatal("missing initialized key reached network or was regenerated")
	}
	r, e, _ := invoke(t, a, home, "", "bind", "--address", "10.77.0.10/32", "--expected-generation", "1", "--idempotency-key", "replacement")
	if e == nil || r.OK {
		t.Fatal("lost key automatically regenerated")
	}
}

func TestPrepareStartStrictParsingCancellationAndCAFileLimits(t *testing.T) {
	for _, args := range [][]string{nil, {"--expected-generation", "0"}, {"--expected-generation", "1", "--port", "0"}, {"--expected-generation", "1", "--timeout", "31s"}, {"--expected-generation", "1", "--wg-public-key", "SYNTHETIC_SECRET"}, {"--expected-generation", "1", "--activation-stdin"}, {"--expected-generation", "1", "--json=false"}, {"--expected-generation", "1", "--expected-generation=1"}, {"--expected-generation", "1", "--server", "https://a", "-server=https://a"}} {
		if _, e := StartTimeout(args); e == nil {
			t.Fatal("invalid start arguments accepted")
		}
	}
	for _, args := range [][]string{{"bind", "--address", "10.77.0.10/32", "--expected-generation", "0", "--idempotency-key", "x", "--json=false"}, {"bind", "--address", "10.77.0.10/32", "--expected-generation", "0", "--idempotency-key", "x", "--address=10.77.0.10/32"}, {"bind", "--address", "10.66.0.10/32", "--expected-generation", "0", "--idempotency-key", "x"}, {"bind", "--address", "10.77.0.10/32", "--expected-generation", "0", "--idempotency-key", "x", "--wg-public-key", wgFixture()}, {"status", "--operation-id", strings.Repeat("1", 32), "--operation-id", strings.Repeat("1", 32)}} {
		if _, e := parse(args); e == nil {
			t.Fatal("duplicate/non-JSON/irrelevant bind flags accepted")
		}
	}
	a := authority(t)
	home := privateHome(t)
	bindFixture(t, a, home)
	_, before := a.counts()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := PrepareStartLocked(ctx, home, []string{"--expected-generation", "1"})
	_, after := a.counts()
	if FailureCode(e) != "command_timeout" || before != after {
		t.Fatal("pre-cancelled prepare accessed authority")
	}
	for _, data := range [][]byte{[]byte("SYNTHETIC_CA_SECRET"), bytes.Repeat([]byte{'x'}, (1<<20)+1)} {
		file := filepath.Join(t.TempDir(), "bad-ca.pem")
		if e = os.WriteFile(file, data, 0600); e != nil {
			t.Fatal(e)
		}
		_, e = loadRoots(context.Background(), file)
		if FailureCode(e) != "invalid_ca_file" || strings.Contains(e.Error(), "SYNTHETIC_CA") {
			t.Fatal("invalid CA leaked or accepted")
		}
	}
}
