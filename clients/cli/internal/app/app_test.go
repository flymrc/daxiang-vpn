package app

import (
	"testing"

	"daxiang-vpn/shared/config"
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
	if opts.downSeconds != 8 || opts.waitSeconds != 30 {
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
}

func TestParseRotateIPOptionsRejectsBadPort(t *testing.T) {
	cfg := config.Config{}
	cfg.ApplyDefaults()

	_, err := parseRotateIPOptions([]string{"--port", "70000"}, cfg)
	if err == nil {
		t.Fatal("expected error")
	}
}
