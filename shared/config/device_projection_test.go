package config

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	dc "zongheng-vpn/shared/devicecontract"
)

func testDeviceConfig(t *testing.T) Config {
	t.Helper()
	key := [32]byte{9}
	pub, err := curve25519.X25519(key[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	pub64 := base64.StdEncoding.EncodeToString(pub)
	profile := dc.ProxyRouteProfile{Version: 1, AuthorityEpoch: "owned-epoch", ManagedBy: "owned-customer", WgInterface: "wg-customer", Revision: 1, WgEndpoint: "127.0.0.1:51820", WgPublicKey: pub64, ProxyAddress: "10.250.0.1:18081", EgressId: "owned-egress", EgressName: "受控出口", AllowedIps: []string{"10.250.0.1/32"}}
	digest, err := dc.ProxyRouteProfileDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	return Config{Authorization: AuthorizationConfig{Source: DeviceV2Source, DeviceID: strings.Repeat("a", 32), CredentialID: strings.Repeat("b", 32), DesiredGeneration: 1, AppliedGeneration: 1, Profile: profile, ProfileSHA256: digest, IssuedUnixSeconds: now, ExpiresUnixSeconds: now + 30}, Client: ClientConfig{Name: "owned-device"}, Hub: HubConfig{Endpoint: profile.WgEndpoint, PublicKey: pub64}, Egress: EgressConfig{Name: profile.EgressId, DisplayName: profile.EgressName, ProxyAddr: profile.ProxyAddress}, LocalProxy: LocalProxyConfig{ListenAddr: "127.0.0.1", ListenPort: 7890}, WireGuard: WireGuardConfig{Address: "10.250.0.2/32", PrivateKey: base64.StdEncoding.EncodeToString(key[:]), PublicKey: pub64, AllowedIPs: []string{"10.250.0.1/32"}}}
}

func TestDeviceProjectionRejectsAdjacentOverrides(t *testing.T) {
	cases := map[string]func(*Config){
		"legacy token":       func(c *Config) { c.License.Token = "owned-canary" },
		"management target":  func(c *Config) { c.Egress.ManagementAddr = "10.66.0.101:2022" },
		"proxy override":     func(c *Config) { c.Egress.ProxyAddr = "10.250.0.2:18081" },
		"endpoint override":  func(c *Config) { c.Hub.Endpoint = "127.0.0.2:51820" },
		"route widen":        func(c *Config) { c.WireGuard.AllowedIPs = []string{"0.0.0.0/0"} },
		"public address":     func(c *Config) { c.WireGuard.Address = "8.8.8.8/32" },
		"legacy address":     func(c *Config) { c.WireGuard.Address = "10.66.0.20/32" },
		"proxy self address": func(c *Config) { c.WireGuard.Address = "10.250.0.1/32" },
		"applied behind":     func(c *Config) { c.Authorization.AppliedGeneration = 0 },
		"profile digest":     func(c *Config) { c.Authorization.Profile.EgressName = "changed" },
		"broadcast listener": func(c *Config) { c.LocalProxy.ListenAddr = "0.0.0.0" },
		"unknown source":     func(c *Config) { c.Authorization.Source = "other" },
		"dropped source":     func(c *Config) { c.Authorization.Source = "" },
		"huge generation": func(c *Config) {
			c.Authorization.DesiredGeneration = dc.ProxySafeInteger + 1
			c.Authorization.AppliedGeneration = c.Authorization.DesiredGeneration
		},
		"extended startup lease": func(c *Config) { c.Authorization.ExpiresUnixSeconds = c.Authorization.IssuedUnixSeconds + 31 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := testDeviceConfig(t)
			change(&c)
			if c.Validate() == nil || c.ValidateForProxyStart(time.Now()) == nil {
				t.Fatal("untrusted override accepted")
			}
		})
	}
}

func TestDeviceProjectionFreshnessAndPrivateKeyPossession(t *testing.T) {
	c := testDeviceConfig(t)
	if c.ValidateForProxyStart(time.Now()) != nil {
		t.Fatal("fresh owned key rejected")
	}
	c.WireGuard.PrivateKey = ""
	if c.Validate() != nil || c.ValidateForProxyStart(time.Now()) == nil {
		t.Fatal("status cache became start authority")
	}
	c = testDeviceConfig(t)
	other := [32]byte{9, 1}
	c.WireGuard.PrivateKey = base64.StdEncoding.EncodeToString(other[:])
	if c.ValidateForProxyStart(time.Now()) == nil {
		t.Fatal("unrelated private key accepted")
	}
	c = testDeviceConfig(t)
	if c.ValidateForProxyStart(time.Unix(c.Authorization.ExpiresUnixSeconds, 0)) == nil || c.ValidateForProxyStart(time.Unix(c.Authorization.IssuedUnixSeconds-1, 0)) == nil {
		t.Fatal("startup freshness ignored")
	}
	if c.Validate() != nil {
		t.Fatal("status cache needs no new authority check")
	}
}

func TestDeviceConfigPrivateYAMLRoundTripAndLegacyProjection(t *testing.T) {
	c := testDeviceConfig(t)
	data, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(data)
	if err != nil || back.ValidateForProxyStart(time.Now()) != nil {
		t.Fatal("private cache lost explicit scope")
	}
	legacy := Config{License: LicenseConfig{Token: "placeholder"}}
	if legacy.Validate() != nil || legacy.ValidateForProxyStart(time.Now()) != nil {
		t.Fatal("legacy placeholder behavior changed")
	}
	b, _ := json.Marshal(legacy)
	if strings.Contains(string(b), "authorization") {
		t.Fatal("legacy projection gained v2 fields")
	}
}
