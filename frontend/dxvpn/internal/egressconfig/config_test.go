package egressconfig

import "testing"

func TestWireGuardMTUDefault(t *testing.T) {
	var cfg WireGuardConfig
	if got := cfg.MTUOrDefault(); got != 1280 {
		t.Fatalf("MTUOrDefault() = %d, want 1280", got)
	}

	cfg.MTU = 1200
	if got := cfg.MTUOrDefault(); got != 1200 {
		t.Fatalf("MTUOrDefault() = %d, want 1200", got)
	}
}

func TestConfigValidateWireGuardTuning(t *testing.T) {
	cfg := validConfig()
	cfg.WireGuard.MTU = 575
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted too-small wireguard.mtu")
	}

	cfg = validConfig()
	cfg.WireGuard.Workers = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted negative wireguard.workers")
	}
}

func validConfig() Config {
	return Config{
		Node: NodeConfig{Name: "test"},
		Hub: HubConfig{
			Endpoint:  "36.50.84.68:51820",
			PublicKey: "public-key",
		},
		WireGuard: WireGuardConfig{
			Address:    "10.66.0.101/24",
			PrivateKey: "private-key",
		},
		Proxy: ProxyConfig{ListenPort: 1080},
	}
}
