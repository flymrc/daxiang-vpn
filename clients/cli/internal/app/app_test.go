package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
)

func testClientConfig(token string) config.Config {
	cfg := config.Config{
		License: config.LicenseConfig{Token: token},
		Client:  config.ClientConfig{Name: "cn-test"},
		Hub: config.HubConfig{
			Endpoint:  "36.50.84.68:51820",
			PublicKey: "hub-public-key",
		},
		Egress: config.EgressConfig{
			Name:           "jp-android-01",
			DisplayName:    "Rakuten",
			Region:         "Japan",
			Type:           "phone",
			ManagementAddr: "10.66.0.101:2022",
			ProxyAddr:      "10.66.0.1:18081",
		},
		WireGuard: config.WireGuardConfig{
			Address:    "10.66.0.30/32",
			PrivateKey: "test-private-key",
		},
	}
	cfg.ApplyDefaults()
	return cfg
}

func bootstrapTestServer(t *testing.T, cfg config.Config, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/bootstrap" {
			http.NotFound(w, r)
			return
		}
		*calls += 1
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(cfg); err != nil {
			t.Errorf("encode bootstrap response: %v", err)
		}
	}))
}

func TestDoLoginPersistsStatusCacheWithoutPrivateKey(t *testing.T) {
	ctx := paths.FromRoot(t.TempDir())
	calls := 0
	srv := bootstrapTestServer(t, testClientConfig("ZH-TEST"), &calls)
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	cfg, err := doLogin(ctx, "ZH-TEST")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WireGuard.PrivateKey == "" {
		t.Fatal("login result lost in-memory private key")
	}
	if calls != 1 {
		t.Fatalf("bootstrap calls = %d, want 1", calls)
	}
	saved, err := config.Load(ctx.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.WireGuard.PrivateKey != "" {
		t.Fatal("saved config should not persist wireguard private key")
	}
	if saved.Egress.ProxyAddr != "10.66.0.1:18081" {
		t.Fatalf("saved proxy = %q", saved.Egress.ProxyAddr)
	}
}

func TestLoadInstalledConfigUsesCachedStatusWithoutBootstrap(t *testing.T) {
	ctx := paths.FromRoot(t.TempDir())
	cfg := testClientConfig("ZH-TEST")
	cfg.WireGuard.PrivateKey = ""
	if err := config.Save(ctx.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := bootstrapTestServer(t, testClientConfig("ZH-TEST"), &calls)
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	loaded, err := loadInstalledConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("bootstrap calls = %d, want 0", calls)
	}
	if loaded.Egress.CustomerName() != "Rakuten" {
		t.Fatalf("egress = %q", loaded.Egress.CustomerName())
	}
}

func TestLoadInstalledConfigMigratesTokenOnlyConfig(t *testing.T) {
	ctx := paths.FromRoot(t.TempDir())
	if err := config.Save(ctx.ConfigPath, config.Config{
		License: config.LicenseConfig{Token: "ZH-TEST"},
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := bootstrapTestServer(t, testClientConfig("ZH-TEST"), &calls)
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	loaded, err := loadInstalledConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("bootstrap calls = %d, want 1", calls)
	}
	if loaded.Egress.ProxyAddr != "10.66.0.1:18081" {
		t.Fatalf("loaded proxy = %q", loaded.Egress.ProxyAddr)
	}
	saved, err := config.Load(ctx.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Egress.ProxyAddr != "10.66.0.1:18081" {
		t.Fatalf("saved proxy = %q", saved.Egress.ProxyAddr)
	}
	if saved.WireGuard.PrivateKey != "" {
		t.Fatal("migrated status cache should not persist wireguard private key")
	}
}

func TestRefreshInstalledConfigAlwaysBootstraps(t *testing.T) {
	ctx := paths.FromRoot(t.TempDir())
	cached := testClientConfig("ZH-TEST")
	cached.Egress.DisplayName = "Old"
	cached.WireGuard.PrivateKey = ""
	if err := config.Save(ctx.ConfigPath, cached); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := bootstrapTestServer(t, testClientConfig("ZH-TEST"), &calls)
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	fresh, err := refreshInstalledConfig(ctx, cached)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("bootstrap calls = %d, want 1", calls)
	}
	if fresh.WireGuard.PrivateKey == "" {
		t.Fatal("fresh config should keep private key in memory")
	}
	if fresh.Egress.CustomerName() != "Rakuten" {
		t.Fatalf("fresh egress = %q", fresh.Egress.CustomerName())
	}
	saved, err := config.Load(ctx.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.WireGuard.PrivateKey != "" {
		t.Fatal("refreshed status cache should not persist wireguard private key")
	}
}

func TestParseRotateIPOptionsUsesConfigDefaults(t *testing.T) {
	cfg := config.Config{}
	cfg.ApplyDefaults()
	cfg.Egress.ManagementAddr = "10.66.0.101:2022"

	opts, err := parseRotateIPOptions(nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.phone != "10.66.0.101" {
		t.Fatalf("phone = %q", opts.phone)
	}
	if opts.port != 2022 {
		t.Fatalf("port = %d", opts.port)
	}
	if opts.proxyAddr != "127.0.0.1:7890" {
		t.Fatalf("proxyAddr = %q", opts.proxyAddr)
	}
	if opts.downSeconds != 8 || opts.waitSeconds != 75 {
		t.Fatalf("seconds = down:%d wait:%d", opts.downSeconds, opts.waitSeconds)
	}
}

func TestParseRotateIPOptionsOverrides(t *testing.T) {
	cfg := config.Config{}
	cfg.ApplyDefaults()

	opts, err := parseRotateIPOptions([]string{
		"--phone", "10.66.0.102",
		"--port=2222",
		"--down-seconds", "12",
		"--wait-seconds=45",
		"--key", "~/custom_key",
		"--proxy", "http://127.0.0.1:18081",
		"--jump", "root@36.50.84.68",
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.phone != "10.66.0.102" {
		t.Fatalf("phone = %q", opts.phone)
	}
	if opts.port != 2222 {
		t.Fatalf("port = %d", opts.port)
	}
	if opts.downSeconds != 12 || opts.waitSeconds != 45 {
		t.Fatalf("seconds = down:%d wait:%d", opts.downSeconds, opts.waitSeconds)
	}
	if opts.proxyAddr != "127.0.0.1:18081" {
		t.Fatalf("proxyAddr = %q", opts.proxyAddr)
	}
	if opts.jumpHost != "root@36.50.84.68" {
		t.Fatalf("jumpHost = %q", opts.jumpHost)
	}
	if !opts.direct {
		t.Fatal("direct = false")
	}
}

func TestParseRotateIPOptionsRejectsBadPort(t *testing.T) {
	cfg := config.Config{}
	cfg.ApplyDefaults()

	_, err := parseRotateIPOptions([]string{"--port", "70000"}, cfg)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRotateIPOptionsRejectsExcessiveDownSeconds(t *testing.T) {
	cfg := config.Config{}
	cfg.ApplyDefaults()

	_, err := parseRotateIPOptions([]string{"--down-seconds", "61"}, cfg)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRotateIPOptionsAcceptsJSON(t *testing.T) {
	cfg := config.Config{}
	cfg.ApplyDefaults()

	opts, err := parseRotateIPOptions([]string{"--json"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// --json is a caller concern; it must not flip the rotate to direct mode.
	if opts.direct {
		t.Fatal("direct = true, --json should not change rotate behavior")
	}
}

func TestWantJSON(t *testing.T) {
	if ok, err := wantJSON(nil); err != nil || ok {
		t.Fatalf("empty: ok=%v err=%v", ok, err)
	}
	if ok, err := wantJSON([]string{"--json"}); err != nil || !ok {
		t.Fatalf("--json: ok=%v err=%v", ok, err)
	}
	if _, err := wantJSON([]string{"--nope"}); err == nil {
		t.Fatal("expected error for unknown arg")
	}
}

func TestParseStartOptions(t *testing.T) {
	opts, err := parseStartOptions([]string{"--fast", "--port", "7891", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.fast {
		t.Fatal("fast = false")
	}
	if opts.port != 7891 {
		t.Fatalf("port = %d", opts.port)
	}
	if !opts.jsonOut {
		t.Fatal("jsonOut = false")
	}
}

func TestVersionRejectsUnknownArgs(t *testing.T) {
	if err := version([]string{"--bad"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseStatusOptions(t *testing.T) {
	opts, err := parseStatusOptions([]string{"--json", "--no-ip-check"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.jsonOut {
		t.Fatal("jsonOut = false")
	}
	if opts.checkIP {
		t.Fatal("checkIP = true")
	}
	if _, err := parseStatusOptions([]string{"--bad"}); err == nil {
		t.Fatal("expected error for unknown status arg")
	}
}

func TestPublicIPMatchesEndpoint(t *testing.T) {
	if !publicIPMatchesEndpoint("36.50.84.68", "36.50.84.68:51820") {
		t.Fatal("expected public IP to match hub endpoint")
	}
	if publicIPMatchesEndpoint("133.106.34.62", "36.50.84.68:51820") {
		t.Fatal("expected phone IPv4 not to match hub endpoint")
	}
	if publicIPMatchesEndpoint("", "36.50.84.68:51820") {
		t.Fatal("empty ip should not match")
	}
}

func TestHasFlag(t *testing.T) {
	if !hasFlag([]string{"a", "--json", "b"}, "--json") {
		t.Fatal("expected --json found")
	}
	if hasFlag([]string{"a", "b"}, "--json") {
		t.Fatal("expected --json not found")
	}
}
