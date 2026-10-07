package deviceapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"zongheng-vpn/hub/internal/deviceauth"
	dc "zongheng-vpn/shared/devicecontract"
)

const proxyBootstrapPath = "/api/v2/proxy/bootstrap"

func apiProxyProfile() dc.ProxyRouteProfile {
	return dc.ProxyRouteProfile{Version: 1, AuthorityEpoch: "test-api-epoch", ManagedBy: "test-api-executor", WgInterface: "wg-customer", Revision: 1, WgEndpoint: "127.0.0.1:51820", WgPublicKey: "3p7bfXt9wbTTW2HC7OQ1Nz+DQ8hbeGdNrfx+FG+IK08=", ProxyAddress: "10.250.0.1:18081", EgressId: "owned-fixture", EgressName: "Owned fixture", AllowedIps: []string{"10.250.0.1/32"}}
}

type proxyAPIFixture struct {
	store     *deviceauth.Store
	db        *sql.DB
	handler   *Server
	client    *wireClient
	key       string
	scheduler *deviceauth.Scheduler
}

func newProxyAPIFixture(t *testing.T) proxyAPIFixture {
	t.Helper()
	store, db := apiStore(t)
	handler, err := NewServerWithProfile(store, apiProxyProfile(), "wg-customer")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	t.Cleanup(server.Close)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &wireClient{t: t, url: server.URL, client: server.Client(), pub: base64.StdEncoding.EncodeToString(pub), priv: priv}
	activation, err := store.IssueActivation(context.Background(), "owned-customer", time.Now().Add(time.Hour), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	body := encoded(t, dc.ActivationRequest{ActivationCredential: activation.Credential, AuthPublicKey: c.pub})
	q := c.proof("activate", "POST", "/api/v2/activate", body, activation.Credential)
	code, data := c.call("POST", "/api/v2/activate", body, &q, nil)
	if code != 201 || json.Unmarshal(data, &c.credential) != nil {
		t.Fatal("activation failed")
	}
	private := make([]byte, 32)
	if _, err = rand.Read(private); err != nil {
		t.Fatal(err)
	}
	wgpub, err := curve25519.X25519(private, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(wgpub)
	body = encoded(t, dc.Command{Action: dc.Apply, IdempotencyKey: "owned-bind", ExpectedGeneration: 0, WgPublicKey: &key, Address: stringPtr("10.250.0.30/32")})
	q = c.proof("command.apply", "POST", "/api/v2/commands", body, "")
	code, _ = c.call("POST", "/api/v2/commands", body, &q, nil)
	if code != 202 {
		t.Fatal("apply failed")
	}
	scheduler, err := deviceauth.NewScheduler(store, &memoryWG{peers: map[string][]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	return proxyAPIFixture{store: store, db: db, handler: handler, client: c, key: key, scheduler: scheduler}
}
func stringPtr(value string) *string { return &value }
func (f proxyAPIFixture) bootstrapProof(t *testing.T) ([]byte, deviceauth.SignedRequest) {
	t.Helper()
	b := encoded(t, dc.ProxyBootstrapRequest{ExpectedGeneration: 1, WireguardPublicKey: f.key})
	return b, f.client.proof("proxy.bootstrap", "POST", proxyBootstrapPath, b, "")
}

func TestTLSProxyBootstrapDefaultOffAndCurrentProjection(t *testing.T) {
	f := newProxyAPIFixture(t)
	off, _ := NewServer(f.store)
	req := httptest.NewRequest("POST", proxyBootstrapPath, strings.NewReader(`{}`))
	req.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	off.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal("default registered bootstrap")
	}
	plain := httptest.NewRequest("POST", proxyBootstrapPath, strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, plain)
	if w.Code != 401 {
		t.Fatal("plaintext profile projected")
	}
	body, q := f.bootstrapProof(t)
	code, data := f.client.call("POST", proxyBootstrapPath, body, &q, nil)
	var result dc.ProxyBootstrap
	if code != 200 || json.Unmarshal(data, &result) != nil {
		t.Fatal("projection failed")
	}
	digest, _ := dc.ProxyRouteProfileDigest(apiProxyProfile())
	if result.Version != 1 || result.DeviceId != f.client.credential.DeviceId || result.CredentialId != f.client.credential.CredentialId || result.WireguardPublicKey != f.key || result.Address != "10.250.0.30/32" || result.DesiredGeneration != 1 || result.AppliedGeneration != 1 || result.ProfileSha256 != digest || result.ExpiresUnixSeconds <= result.IssuedUnixSeconds || result.ExpiresUnixSeconds > result.IssuedUnixSeconds+30 {
		t.Fatal("projection binding mismatch")
	}
	for _, secret := range []string{"private_key", "activation_credential", "management_addr", "token"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("projection leaked control material")
		}
	}
	code, _ = f.client.call("POST", proxyBootstrapPath, body, &q, nil)
	if code != 401 {
		t.Fatal("nonce replay authorized")
	}
	var ops int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM deviceauth_operations").Scan(&ops); err != nil || ops != 1 {
		t.Fatal("read changed desired operations")
	}
}

func TestTLSProxyBootstrapRechecksCurrentAuthorityFacts(t *testing.T) {
	cases := map[string]struct {
		sql  string
		args func(proxyAPIFixture) []any
	}{
		"credential-revoked": {"UPDATE deviceauth_credentials SET revoked_at=1 WHERE credential_id=?", func(f proxyAPIFixture) []any { return []any{f.client.credential.CredentialId} }},
		"credential-expired": {"UPDATE deviceauth_credentials SET valid_until=1 WHERE credential_id=?", func(f proxyAPIFixture) []any { return []any{f.client.credential.CredentialId} }},
		"device-expired":     {"UPDATE deviceauth_devices SET valid_until=1 WHERE device_id=?", func(f proxyAPIFixture) []any { return []any{f.client.credential.DeviceId} }},
		"desired-advanced":   {"UPDATE deviceauth_devices SET generation=2 WHERE device_id=?", func(f proxyAPIFixture) []any { return []any{f.client.credential.DeviceId} }},
		"not-applied":        {"UPDATE deviceauth_devices SET applied_generation=0 WHERE device_id=?", func(f proxyAPIFixture) []any { return []any{f.client.credential.DeviceId} }},
		"wrong-key": {"UPDATE deviceauth_devices SET current_key=? WHERE device_id=?", func(f proxyAPIFixture) []any {
			return []any{apiProxyProfile().WgPublicKey, f.client.credential.DeviceId}
		}},
		"wrong-binding-owner": {"UPDATE deviceauth_bindings SET managed_by='another-owner' WHERE public_key=?", func(f proxyAPIFixture) []any { return []any{f.key} }},
		"binding-unverified":  {"UPDATE deviceauth_bindings SET applied=0 WHERE public_key=?", func(f proxyAPIFixture) []any { return []any{f.key} }},
		"address-owner":       {"UPDATE deviceauth_addresses SET device_id='another-device' WHERE address='10.250.0.30/32'", func(f proxyAPIFixture) []any { return nil }},
		"pending":             {"UPDATE deviceauth_outbox SET state='pending'", func(f proxyAPIFixture) []any { return nil }},
		"degraded":            {"UPDATE deviceauth_outbox SET state='degraded'", func(f proxyAPIFixture) []any { return nil }},
		"no-verified-time":    {"UPDATE deviceauth_outbox SET verified_at=NULL", func(f proxyAPIFixture) []any { return nil }},
		"policy-changed":      {"UPDATE deviceauth_meta SET epoch='other-epoch'", func(f proxyAPIFixture) []any { return nil }},
		"tombstoned-key":      {"INSERT INTO deviceauth_tombstones(public_key,generation,reason,committed_at) VALUES(?,2,'owned-negative',1)", func(f proxyAPIFixture) []any { return []any{f.key} }},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newProxyAPIFixture(t)
			body, q := f.bootstrapProof(t)
			if name == "address-owner" {
				if err := f.store.Enroll(context.Background(), deviceauth.Enrollment{DeviceID: "another-device", OwnerID: "another-owner", Role: "customer", ValidUntil: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.db.Exec(tt.sql, tt.args(f)...); err != nil {
				t.Fatal(err)
			}
			code, data := f.client.call("POST", proxyBootstrapPath, body, &q, nil)
			if code == 200 || bytes.Contains(data, []byte("profile")) {
				t.Fatal("stale authority granted config")
			}
			var consumed sql.NullInt64
			if err := f.db.QueryRow("SELECT consumed_at FROM deviceauth_challenges WHERE challenge_id=?", q.ChallengeID).Scan(&consumed); err != nil || consumed.Valid {
				t.Fatal("failed read consumed nonce")
			}
		})
	}
}

func TestTLSProxyBootstrapRejectsInvalidBodyAndPurpose(t *testing.T) {
	f := newProxyAPIFixture(t)
	for _, body := range []string{`{"wireguard_public_key":"x","expected_generation":1,"expected_generation":1}`, `{"Wireguard_Public_Key":"x","expected_generation":1}`, `{"wireguard_public_key":null,"expected_generation":1}`, `{"wireguard_public_key":"x","expected_generation":9007199254740992}`, `{"wireguard_public_key":"x","expected_generation":1,"profile":{}}`} {
		code, _ := f.client.call("POST", proxyBootstrapPath, []byte(body), nil, nil)
		if code != 400 && code != 401 {
			t.Fatal("invalid body admitted")
		}
	}
	body, q := f.bootstrapProof(t)
	q.Purpose = "operation.status"
	q.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(f.client.priv, deviceauth.SigningBytes(q)))
	code, _ := f.client.call("POST", proxyBootstrapPath, body, &q, nil)
	if code != 401 {
		t.Fatal("cross-purpose proof accepted")
	}
}

func TestProxyBootstrapProfileMatchesAuthorityAndIsImmutable(t *testing.T) {
	store, _ := apiStore(t)
	for _, change := range []func(*dc.ProxyRouteProfile){func(p *dc.ProxyRouteProfile) { p.AuthorityEpoch = "another" }, func(p *dc.ProxyRouteProfile) { p.ManagedBy = "another" }, func(p *dc.ProxyRouteProfile) { p.WgInterface = "wg0" }, func(p *dc.ProxyRouteProfile) {
		p.ProxyAddress = "10.250.0.2:18081"
		p.AllowedIps = []string{"10.250.0.2/32"}
	}} {
		p := apiProxyProfile()
		change(&p)
		if _, err := NewServerWithProfile(store, p, "wg-customer"); err == nil {
			t.Fatal("mismatching trusted profile accepted")
		}
	}
	p := apiProxyProfile()
	h, err := NewServerWithProfile(store, p, "wg-customer")
	if err != nil {
		t.Fatal(err)
	}
	p.AllowedIps[0] = "0.0.0.0/0"
	p.Revision = 99
	if h.proxyProfile.AllowedIps[0] != "10.250.0.1/32" || h.proxyProfile.Revision != 1 {
		t.Fatal("caller mutated runtime profile")
	}
}

func TestTLSProxyBootstrapTTLNeverExtendsAuthorization(t *testing.T) {
	f := newProxyAPIFixture(t)
	body, q := f.bootstrapProof(t)
	deadline := time.Now().Add(4 * time.Second).UnixNano()
	if _, err := f.db.Exec("UPDATE deviceauth_credentials SET valid_until=? WHERE credential_id=?", deadline, f.client.credential.CredentialId); err != nil {
		t.Fatal(err)
	}
	code, data := f.client.call("POST", proxyBootstrapPath, body, &q, nil)
	var r dc.ProxyBootstrap
	if code != 200 || json.Unmarshal(data, &r) != nil || r.ExpiresUnixSeconds > time.Unix(0, deadline).Unix() {
		t.Fatal("credential deadline extended")
	}
	// Cache-Control is part of the live handler, independently of decoded DTO.
	unsigned := httptest.NewRequest("POST", proxyBootstrapPath, strings.NewReader(`{}`))
	unsigned.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, unsigned)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("config response cacheable")
	}
	_, _ = io.Copy(io.Discard, w.Result().Body)
}

func TestTLSProxyBootstrapOneNonceOneConcurrentProjection(t *testing.T) {
	f := newProxyAPIFixture(t)
	body, q := f.bootstrapProof(t)
	// Use one exact proof twice against the real TLS handler/SQLite fence.
	status := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			r, _ := http.NewRequest("POST", f.client.url+proxyBootstrapPath, bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-ZH-Challenge", q.ChallengeID)
			r.Header.Set("X-ZH-Nonce", q.Nonce)
			r.Header.Set("X-ZH-Expires", strconv.FormatInt(q.Expires, 10))
			r.Header.Set("X-ZH-Request-ID", q.RequestID)
			r.Header.Set("X-ZH-Signature", q.Signature)
			r.Header.Set("X-ZH-Device", q.DeviceID)
			r.Header.Set("X-ZH-Credential", q.CredentialID)
			resp, err := f.client.client.Do(r)
			if err != nil {
				status <- 0
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			status <- resp.StatusCode
		}()
	}
	a, b := <-status, <-status
	if !((a == 200 && b == 401) || (a == 401 && b == 200)) {
		t.Fatal("concurrent proof did not consume exactly once")
	}
}

func TestTLSProxyBootstrapRealDisableAfterChallengeRefusesProjection(t *testing.T) {
	f := newProxyAPIFixture(t)
	body, q := f.bootstrapProof(t)
	disable := encoded(t, dc.Command{Action: dc.Disable, IdempotencyKey: "owned-disable", ExpectedGeneration: 1})
	proof := f.client.proof("command.disable", "POST", "/api/v2/commands", disable, "")
	code, _ := f.client.call("POST", "/api/v2/commands", disable, &proof, nil)
	if code != 202 {
		t.Fatal("disable was not accepted")
	}
	code, data := f.client.call("POST", proxyBootstrapPath, body, &q, nil)
	if code != 401 || bytes.Contains(data, []byte("profile")) {
		t.Fatal("accepted disable did not revoke projection")
	}
	d, err := f.store.Device(context.Background(), f.client.credential.DeviceId)
	if err != nil || d.Generation != 2 || d.AppliedGeneration != 1 || d.PublicKey != "" {
		t.Fatal("read altered pending revoke facts")
	}
}
