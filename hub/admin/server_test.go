package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "zongheng-vpn/hub/admin/internal/spec/generated"
	"zongheng-vpn/hub/internal/auth"
)

func TestAdminAuthSessionAndCSRF(t *testing.T) {
	server := newTestServer(t)

	unauth := httptest.NewRecorder()
	server.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/admin/api/overview", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("overview without session status = %d", unauth.Code)
	}

	cookie, me := loginTestAdmin(t, server)

	meReq := httptest.NewRequest(http.MethodGet, "/admin/api/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	server.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d body=%s", meRec.Code, meRec.Body.String())
	}

	rotateReq := httptest.NewRequest(http.MethodPost, "/admin/api/egress/jp-android-01/rotate-ip", strings.NewReader(`{"down_seconds":8}`))
	rotateReq.AddCookie(cookie)
	rotateRec := httptest.NewRecorder()
	server.ServeHTTP(rotateRec, rotateReq)
	if rotateRec.Code != http.StatusForbidden {
		t.Fatalf("rotate without csrf status = %d body=%s", rotateRec.Code, rotateRec.Body.String())
	}

	rotateReq = httptest.NewRequest(http.MethodPost, "/admin/api/egress/jp-android-01/rotate-ip", strings.NewReader(`{"down_seconds":8}`))
	rotateReq.AddCookie(cookie)
	rotateReq.Header.Set("X-CSRF-Token", me.CsrfToken)
	rotateRec = httptest.NewRecorder()
	server.ServeHTTP(rotateRec, rotateReq)
	if rotateRec.Code != http.StatusOK {
		t.Fatalf("rotate with csrf status = %d body=%s", rotateRec.Code, rotateRec.Body.String())
	}
}

func TestAdminRotateBusy(t *testing.T) {
	server := newTestServer(t)
	cookie, me := loginTestAdmin(t, server)

	for i, want := range []int{http.StatusOK, http.StatusConflict} {
		req := httptest.NewRequest(http.MethodPost, "/admin/api/egress/jp-android-01/rotate-ip", strings.NewReader(`{"down_seconds":8}`))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", me.CsrfToken)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("rotate %d status = %d want %d body=%s", i+1, rec.Code, want, rec.Body.String())
		}
	}
}

func TestAdminTokensAreMasked(t *testing.T) {
	server := newTestServer(t)
	cookie, _ := loginTestAdmin(t, server)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/tokens", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tokens status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "ZH-JP-TEST-001") || strings.Contains(body, "CLIENT_PRIVATE_KEY") {
		t.Fatalf("tokens response leaked sensitive data: %s", body)
	}
	if !strings.Contains(body, "ZH-***01") {
		t.Fatalf("tokens response did not include masked token: %s", body)
	}
}

func TestAdminTokenReveal(t *testing.T) {
	server := newTestServer(t)
	cookie, _ := loginTestAdmin(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/admin/api/tokens", nil)
	listReq.AddCookie(cookie)
	listRec := httptest.NewRecorder()
	server.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("tokens status = %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listed generated.TokensResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Tokens) != 1 {
		t.Fatalf("tokens = %#v", listed.Tokens)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/tokens/"+listed.Tokens[0].Id+"/secret", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token secret status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got generated.TokenSecretResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Token != "ZH-JP-TEST-001" {
		t.Fatalf("token secret = %#v", got)
	}
}

func TestAdminEgressHealth(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"session_count":2,"active_proxy_connections":3}`))
	}))
	defer health.Close()

	server := newTestServer(t, func(cfg *Config) {
		cfg.ReverseHealthURL = health.URL
	})
	cookie, _ := loginTestAdmin(t, server)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/egress", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("egress status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got generated.EgressResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Egress) != 1 || got.Egress[0].Status != generated.Online {
		t.Fatalf("egress status = %#v", got.Egress)
	}
	if got.Egress[0].SessionCount == nil || *got.Egress[0].SessionCount != 2 {
		t.Fatalf("session_count = %#v", got.Egress[0].SessionCount)
	}
}

func TestAdminEgressExitIPRevealUsesProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("proxy method = %s", r.Method)
		}
		if got := r.URL.String(); got != "http://exit-ip.test/" {
			t.Fatalf("proxy request URL = %q", got)
		}
		w.Write([]byte("203.0.113.9"))
	}))
	defer proxy.Close()

	server := newTestServerWithTokenStore(t, testTokenStore(strings.TrimPrefix(proxy.URL, "http://")), func(cfg *Config) {
		cfg.ExitIPCheckURL = "http://exit-ip.test/"
	})
	cookie, _ := loginTestAdmin(t, server)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/egress/jp-android-01/exit-ip", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("exit ip status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got generated.EgressExitIPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ExitIp != "203.0.113.9" {
		t.Fatalf("exit ip = %#v", got)
	}
}

func TestAdminUIServesIndexWithoutRedirect(t *testing.T) {
	server := newTestServer(t)

	for _, path := range []string{"/admin/", "/admin/tokens"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d location=%q body=%s", path, rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
		if got := rec.Header().Get("Location"); got != "" {
			t.Fatalf("%s unexpected redirect location %q", path, got)
		}
		if !strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
			t.Fatalf("%s did not serve app index", path)
		}
	}
}

func newTestServer(t *testing.T, opts ...func(*Config)) *Server {
	return newTestServerWithTokenStore(t, testTokenStore("10.66.0.1:18081"), opts...)
}

func newTestServerWithTokenStore(t *testing.T, tokenStore *auth.TokenStore, opts ...func(*Config)) *Server {
	t.Helper()
	passwordHash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ListenAddr:       "127.0.0.1:0",
		DBPath:           filepath.Join(t.TempDir(), "admin.db"),
		PublicHost:       "panel.test",
		HubPublicIP:      "36.50.84.68",
		HubWGIP:          "10.66.0.1",
		Version:          "test",
		AdminUsername:    "admin",
		AdminPasswordPHC: passwordHash,
		ReverseHealthURL: "http://127.0.0.1:1/debug/session-health",
		SessionTTL:       time.Hour,
		CookieSecure:     false,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	authServer := auth.NewServer(tokenStore)
	authServer.SetRotateTrigger(func(string, int) error { return nil })
	server, err := NewServer(cfg, tokenStore, authServer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return server
}

func testTokenStore(proxyAddr string) *auth.TokenStore {
	return &auth.TokenStore{Tokens: map[string]auth.TokenRecord{
		"ZH-JP-TEST-001": {
			Enabled:    true,
			ClientName: "cn-client-01",
			ExpiresAt:  "2099-01-01",
			Egress: auth.Egress{
				Name:           "jp-android-01",
				DisplayName:    "日本手机出口",
				Region:         "日本",
				Type:           "手机 IP",
				ManagementAddr: "10.66.0.101:2022",
				ProxyAddr:      proxyAddr,
			},
			WireGuard: auth.WireGuard{
				Address:    "10.66.0.20/24",
				PrivateKey: "CLIENT_PRIVATE_KEY",
			},
		},
	}}
}

func loginTestAdmin(t *testing.T, server *Server) (*http.Cookie, generated.AuthMeResponse) {
	t.Helper()
	body := []byte(`{"username":"admin","password":"secret-password"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/auth/login", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", rec.Code, rec.Body.String())
	}
	var me generated.AuthMeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "zhhub_admin_session" {
			return cookie, me
		}
	}
	t.Fatal("session cookie missing")
	return nil, generated.AuthMeResponse{}
}
