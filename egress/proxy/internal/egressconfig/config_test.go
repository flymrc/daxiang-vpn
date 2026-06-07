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

func TestConfigValidateWireGuardMode(t *testing.T) {
	cfg := validConfig()
	cfg.WireGuard.Mode = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted invalid wireguard.mode")
	}

	cfg = validConfig()
	cfg.WireGuard.Mode = "external"
	cfg.WireGuard.PrivateKey = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected external mode without private key: %v", err)
	}

	cfg = validConfig()
	cfg.WireGuard.PrivateKey = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted embedded mode without private key")
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
