package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIBaseDefaultsToHTTPSPublicHost(t *testing.T) {
	t.Setenv("ZHVPN_API_BASE", "")

	if got := apiBase(); got != "https://jp-proxy.ruichao.dev" {
		t.Fatalf("apiBase() = %q", got)
	}
}

func TestAPIBaseEnvOverrideTrimsTrailingSlash(t *testing.T) {
	t.Setenv("ZHVPN_API_BASE", "http://127.0.0.1:18080/")

	if got := apiBase(); got != "http://127.0.0.1:18080" {
		t.Fatalf("apiBase() = %q", got)
	}
}

func TestFetchSendsWireGuardPublicKey(t *testing.T) {
	const publicKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/bootstrap" {
			http.NotFound(w, r)
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Token != "ZH-OK" {
			t.Fatalf("token = %q", req.Token)
		}
		if req.WireGuardPublicKey != publicKey {
			t.Fatalf("wireguard_public_key = %q", req.WireGuardPublicKey)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"client":{"name":"test"},
			"hub":{"endpoint":"36.50.84.68:51820","public_key":"hub-public"},
			"egress":{"name":"jp-android-01","display_name":"Rakuten","region":"Japan","type":"phone","management_addr":"10.66.0.101:2022","proxy_addr":"10.66.0.1:18081"},
			"local_proxy":{"listen_addr":"127.0.0.1","listen_port":7890},
			"wireguard":{"address":"10.66.0.30/32"}
		}`))
	}))
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	cfg, err := Fetch("ZH-OK", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WireGuard.PrivateKey != "" {
		t.Fatalf("private key = %q", cfg.WireGuard.PrivateKey)
	}
}

func TestRotateIPBusyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/rotate-ip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(RotateIPResult{
			Status:            "busy",
			Egress:            "jp-android-01",
			DownSeconds:       8,
			Message:           "换 IP 正在进行中，请稍后再试",
			RetryAfterSeconds: 42,
		})
	}))
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	res, err := RotateIP("ZH-OK", 8)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "busy" {
		t.Fatalf("status = %q", res.Status)
	}
	if res.RetryAfterSeconds != 42 {
		t.Fatalf("retry_after_seconds = %d", res.RetryAfterSeconds)
	}
}

func TestRotateIPTriggeredResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/rotate-ip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RotateIPResult{
			Status:      "triggered",
			Egress:      "jp-android-01",
			DownSeconds: 8,
		})
	}))
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	res, err := RotateIP("ZH-OK", 8)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "triggered" {
		t.Fatalf("status = %q", res.Status)
	}
}

func TestRotateIPInvalidDownSecondsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/rotate-ip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: "invalid_down_seconds"})
	}))
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	_, err := RotateIP("ZH-OK", 61)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "断网秒数必须在 1 到 60 秒之间" {
		t.Fatalf("error = %q", err.Error())
	}
}
