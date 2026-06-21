//go:build darwin

package proxy

import (
	"encoding/json"
	"os"
	"testing"

	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
)

func TestWriteSingBoxConfigOnDarwinUsesHubProxyOverWireGuard(t *testing.T) {
	ctx := paths.FromRoot(t.TempDir())
	if err := ctx.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Client: config.ClientConfig{Name: "mac-test"},
		Hub: config.HubConfig{
			Endpoint:  "36.50.84.68:51820",
			PublicKey: "hub-public-key",
		},
		Egress: config.EgressConfig{
			Name:           "jp-android-01",
			DisplayName:    "Rakuten",
			ManagementAddr: "10.66.0.101:2022",
			ProxyAddr:      "10.66.0.1:18081",
		},
		WireGuard: config.WireGuardConfig{
			Address:    "10.66.0.30/32",
			PrivateKey: "client-private-key",
		},
	}
	cfg.ApplyDefaults()

	if err := WriteSingBoxConfig(ctx, cfg, false); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(ctx.SingBoxConfig)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Endpoints []struct {
			Type       string   `json:"type"`
			Tag        string   `json:"tag"`
			System     bool     `json:"system"`
			Address    []string `json:"address"`
			PrivateKey string   `json:"private_key"`
			Peers      []struct {
				Address    string   `json:"address"`
				Port       int      `json:"port"`
				PublicKey  string   `json:"public_key"`
				AllowedIPs []string `json:"allowed_ips"`
			} `json:"peers"`
		} `json:"endpoints"`
		Inbounds []struct {
			Type       string `json:"type"`
			Listen     string `json:"listen"`
			ListenPort int    `json:"listen_port"`
		} `json:"inbounds"`
		Outbounds []struct {
			Type       string `json:"type"`
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
			Detour     string `json:"detour"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if len(got.Endpoints) != 1 || got.Endpoints[0].Type != "wireguard" || got.Endpoints[0].System {
		t.Fatalf("endpoint = %+v", got.Endpoints)
	}
	if got.Endpoints[0].Address[0] != "10.66.0.30/32" || got.Endpoints[0].PrivateKey != "client-private-key" {
		t.Fatalf("endpoint address/private key = %+v", got.Endpoints[0])
	}
	peer := got.Endpoints[0].Peers[0]
	if peer.Address != "36.50.84.68" || peer.Port != 51820 || peer.PublicKey != "hub-public-key" {
		t.Fatalf("peer = %+v", peer)
	}
	if len(peer.AllowedIPs) != 1 || peer.AllowedIPs[0] != "10.66.0.0/24" {
		t.Fatalf("allowed ips = %+v", peer.AllowedIPs)
	}
	if len(got.Inbounds) != 1 || got.Inbounds[0].Type != "mixed" || got.Inbounds[0].Listen != "127.0.0.1" || got.Inbounds[0].ListenPort != 7890 {
		t.Fatalf("inbounds = %+v", got.Inbounds)
	}
	if len(got.Outbounds) != 1 || got.Outbounds[0].Type != "http" || got.Outbounds[0].Server != "10.66.0.1" || got.Outbounds[0].ServerPort != 18081 || got.Outbounds[0].Detour != "zhvpn-wg" {
		t.Fatalf("outbounds = %+v", got.Outbounds)
	}
}
