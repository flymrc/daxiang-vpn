package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClaimTokenLease(t *testing.T) {
	server := &Server{
		tokenLeases:   map[string]tokenLease{},
		tokenLeaseTTL: 30 * time.Second,
	}
	now := time.Unix(1000, 0)

	if !server.claimToken("token-a", "198.51.100.10", now) {
		t.Fatal("initial claim rejected")
	}
	if !server.claimToken("token-a", "198.51.100.10", now.Add(5*time.Second)) {
		t.Fatal("same source refresh rejected")
	}
	if server.claimToken("token-a", "203.0.113.20", now.Add(10*time.Second)) {
		t.Fatal("different source within lease accepted")
	}
	if !server.claimToken("token-a", "203.0.113.20", now.Add(36*time.Second)) {
		t.Fatal("different source after lease rejected")
	}
}

func TestClaimTokenLeaseDisabled(t *testing.T) {
	server := &Server{
		tokenLeases:   map[string]tokenLease{},
		tokenLeaseTTL: 0,
	}

	if !server.claimToken("token-a", "198.51.100.10", time.Unix(1000, 0)) {
		t.Fatal("initial claim rejected")
	}
	if !server.claimToken("token-a", "203.0.113.20", time.Unix(1001, 0)) {
		t.Fatal("different source rejected with disabled lease")
	}
}

func TestClientIPIgnoresSpoofedForwardedForFromPublicClient(t *testing.T) {
	req := &http.Request{
		RemoteAddr: "198.51.100.10:54321",
		Header:     http.Header{"X-Forwarded-For": []string{"203.0.113.20"}},
	}

	if got := clientIP(req); got != "198.51.100.10" {
		t.Fatalf("clientIP() = %q, want tcp source", got)
	}
}

func TestClientIPTrustsForwardedForFromLocalProxy(t *testing.T) {
	req := &http.Request{
		RemoteAddr: "127.0.0.1:54321",
		Header:     http.Header{"X-Forwarded-For": []string{"203.0.113.20, 10.0.0.1"}},
	}

	if got := clientIP(req); got != "203.0.113.20" {
		t.Fatalf("clientIP() = %q, want forwarded source", got)
	}
}

func TestBootstrapWithClientPublicKeyAppliesPeerAndOmitsPrivateKey(t *testing.T) {
	server := testBootstrapServer()
	publicKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	var appliedKey, appliedAddress string
	server.applyClientPeer = func(publicKey string, address string) error {
		appliedKey = publicKey
		appliedAddress = address
		return nil
	}

	body, err := json.Marshal(bootstrapRequest{
		Token:              "ZH-OK",
		WireGuardPublicKey: publicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/client/bootstrap", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.Bootstrap(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if appliedKey != publicKey || appliedAddress != "10.66.0.30/24" {
		t.Fatalf("applied key/address = %q %q", appliedKey, appliedAddress)
	}
	var res bootstrapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.WireGuard.PrivateKey != "" {
		t.Fatalf("new bootstrap leaked private key: %q", res.WireGuard.PrivateKey)
	}
	if res.WireGuard.PublicKey != publicKey {
		t.Fatalf("public key = %q", res.WireGuard.PublicKey)
	}
}

func TestBootstrapLegacyClientStillReceivesConfiguredPrivateKey(t *testing.T) {
	server := testBootstrapServer()
	body, err := json.Marshal(bootstrapRequest{Token: "ZH-OK"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/client/bootstrap", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.Bootstrap(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var res bootstrapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.WireGuard.PrivateKey != "CLIENT_PRIVATE_KEY" {
		t.Fatalf("legacy private key = %q", res.WireGuard.PrivateKey)
	}
}

func TestBootstrapRejectsInvalidClientPublicKey(t *testing.T) {
	server := testBootstrapServer()
	body, err := json.Marshal(bootstrapRequest{
		Token:              "ZH-OK",
		WireGuardPublicKey: "not-a-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/client/bootstrap", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.Bootstrap(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPeerAllowedIPNarrowsClientAddress(t *testing.T) {
	got, err := peerAllowedIP("10.66.0.30/24")
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.66.0.30/32" {
		t.Fatalf("allowed IP = %q", got)
	}

	got, err = peerAllowedIP("fd00::10/64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fd00::10/128" {
		t.Fatalf("allowed IP = %q", got)
	}
}

func TestEgressWithCachedCarrierFallsBackToConfiguredDisplayName(t *testing.T) {
	server := &Server{}
	egress := server.egressWithCachedCarrierName(Egress{DisplayName: "静态出口", ProxyAddr: "127.0.0.1:1"})

	if egress.DisplayName != "静态出口" {
		t.Fatalf("display name = %q", egress.DisplayName)
	}
}

func TestCachedAndroidCarrierSuppressesRepeatedProbe(t *testing.T) {
	calls := 0
	server := &Server{
		carrierCache:    map[string]carrierCacheEntry{},
		carrierCacheTTL: time.Minute,
		carrierProbe: func(addr string) string {
			calls++
			if addr != "10.66.0.101:2022" {
				t.Fatalf("addr = %q", addr)
			}
			return "Rakuten"
		},
	}
	now := time.Unix(1000, 0)

	first := server.cachedAndroidCarrier("10.66.0.101:2022", now)
	second := server.cachedAndroidCarrier("10.66.0.101:2022", now.Add(10*time.Second))
	third := server.cachedAndroidCarrier("10.66.0.101:2022", now.Add(time.Minute+time.Second))

	if first != "Rakuten" || second != "Rakuten" || third != "Rakuten" {
		t.Fatalf("carrier values = %q %q %q", first, second, third)
	}
	if calls != 2 {
		t.Fatalf("probe calls = %d, want 2", calls)
	}
}

func TestCachedAndroidCarrierCachesEmptyResult(t *testing.T) {
	calls := 0
	server := &Server{
		carrierCache:    map[string]carrierCacheEntry{},
		carrierCacheTTL: time.Minute,
		carrierProbe: func(string) string {
			calls++
			return ""
		},
	}
	now := time.Unix(1000, 0)

	if got := server.cachedAndroidCarrier("10.66.0.101:2022", now); got != "" {
		t.Fatalf("carrier = %q", got)
	}
	if got := server.cachedAndroidCarrier("10.66.0.101:2022", now.Add(10*time.Second)); got != "" {
		t.Fatalf("carrier = %q", got)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1", calls)
	}
}

func TestSplitHostPortDefault(t *testing.T) {
	host, port := splitHostPortDefault("10.66.0.101", "2022")
	if host != "10.66.0.101" || port != "2022" {
		t.Fatalf("host=%q port=%q", host, port)
	}
	host, port = splitHostPortDefault("10.66.0.101:2222", "2022")
	if host != "10.66.0.101" || port != "2222" {
		t.Fatalf("host=%q port=%q", host, port)
	}
}

func TestFirstCSVValue(t *testing.T) {
	if got := firstCSVValue("Rakuten,au"); got != "Rakuten" {
		t.Fatalf("firstCSVValue = %q", got)
	}
	if got := firstCSVValue(", au "); got != "au" {
		t.Fatalf("firstCSVValue = %q", got)
	}
}

func TestRotateIPRejectsConcurrentRequest(t *testing.T) {
	server := testRotateServer()
	calls := 0
	server.triggerRotateIP = func(_ string, _ int) error {
		calls++
		return nil
	}

	first := postRotateIP(t, server, "ZH-OK", 8)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	second := postRotateIP(t, server, "ZH-OK", 8)
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}
	if calls != 1 {
		t.Fatalf("trigger calls = %d, want 1", calls)
	}
	var res rotateIPResponse
	if err := json.Unmarshal(second.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "busy" || res.RetryAfterSeconds <= 0 {
		t.Fatalf("busy response = %+v", res)
	}
}

func TestRotateIPReleasesLockAfterTriggerFailure(t *testing.T) {
	server := testRotateServer()
	calls := 0
	server.triggerRotateIP = func(_ string, _ int) error {
		calls++
		if calls == 1 {
			return errors.New("boom")
		}
		return nil
	}

	first := postRotateIP(t, server, "ZH-OK", 8)
	if first.Code != http.StatusBadGateway {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	second := postRotateIP(t, server, "ZH-OK", 8)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}
	if calls != 2 {
		t.Fatalf("trigger calls = %d, want 2", calls)
	}
}

func testRotateServer() *Server {
	return &Server{
		store: &TokenStore{Tokens: map[string]TokenRecord{
			"ZH-OK": {
				Enabled:    true,
				ClientName: "test-client",
				Egress: Egress{
					Name:           "jp-android-01",
					ManagementAddr: "10.66.0.101:2022",
				},
			},
		}},
		tokenLeases:     map[string]tokenLease{},
		tokenLeaseTTL:   30 * time.Second,
		rotateLocks:     map[string]rotateLock{},
		rotateLockExtra: time.Minute,
	}
}

func testBootstrapServer() *Server {
	return &Server{
		store: &TokenStore{Tokens: map[string]TokenRecord{
			"ZH-OK": {
				Enabled:    true,
				ClientName: "test-client",
				Hub: Hub{
					Endpoint:  "36.50.84.68:51820",
					PublicKey: "HUB_PUBLIC_KEY",
				},
				Egress: Egress{
					Name:           "jp-android-01",
					DisplayName:    "Rakuten",
					ManagementAddr: "10.66.0.101:2022",
					ProxyAddr:      "10.66.0.1:18081",
				},
				LocalProxy: LocalProxy{
					ListenAddr: "127.0.0.1",
					ListenPort: 7890,
				},
				WireGuard: WireGuard{
					Address:    "10.66.0.30/24",
					PrivateKey: "CLIENT_PRIVATE_KEY",
				},
			},
		}},
		tokenLeases:     map[string]tokenLease{},
		tokenLeaseTTL:   30 * time.Second,
		rotateLocks:     map[string]rotateLock{},
		rotateLockExtra: time.Minute,
	}
}

func postRotateIP(t *testing.T, server *Server, token string, downSeconds int) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(rotateIPRequest{Token: token, DownSeconds: downSeconds})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/client/rotate-ip", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.RotateIP(rec, req)
	return rec
}
