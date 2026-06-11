package app

import (
	"testing"

	"zongheng-vpn/shared/config"
)

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
