package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/curve25519"
	dc "zongheng-vpn/shared/devicecontract"
)

const DeviceV2Source = "device-v2"

var errDeviceProjection = errors.New("device v2 startup projection invalid")

func publicID(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == s
}

func (a AuthorizationConfig) empty() bool {
	return a.Source == "" && a.DeviceID == "" && a.CredentialID == "" && a.DesiredGeneration == 0 && a.AppliedGeneration == 0 && a.ProfileSHA256 == "" && a.IssuedUnixSeconds == 0 && a.ExpiresUnixSeconds == 0 && reflect.DeepEqual(a.Profile, dc.ProxyRouteProfile{})
}

// Validate checks the persisted projection's shape, without treating it as a
// current authority check. Status may read an expired cache; start must obtain
// a fresh response and call ValidateFresh.
func (a AuthorizationConfig) Validate() error {
	if a.Source != DeviceV2Source || !publicID(a.DeviceID) || !publicID(a.CredentialID) || a.DesiredGeneration < 1 || a.DesiredGeneration > dc.ProxySafeInteger || a.AppliedGeneration != a.DesiredGeneration {
		return errDeviceProjection
	}
	digest, err := dc.ProxyRouteProfileDigest(a.Profile)
	if err != nil || digest != a.ProfileSHA256 {
		return errDeviceProjection
	}
	// A bounded startup projection cannot overflow a Duration or masquerade as
	// a multi-year lease. Expiry is for launching, not ongoing session renewal.
	if a.IssuedUnixSeconds < 1 || a.IssuedUnixSeconds >= 7289654400 || a.ExpiresUnixSeconds <= a.IssuedUnixSeconds || a.ExpiresUnixSeconds-a.IssuedUnixSeconds > 30 {
		return errDeviceProjection
	}
	return nil
}

func (a AuthorizationConfig) ValidateFresh(now time.Time) error {
	if a.Validate() != nil || a.IssuedUnixSeconds > now.Unix() || a.ExpiresUnixSeconds <= now.Unix() {
		return errDeviceProjection
	}
	return nil
}

func plainLabel(s string) bool {
	return s != "" && len(s) <= 128 && strings.TrimSpace(s) == s && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func wgKey(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 || base64.StdEncoding.EncodeToString(b) != s {
		return nil, errDeviceProjection
	}
	return b, nil
}

func (c Config) validateDeviceProjection() error {
	a := c.Authorization
	if a.Validate() != nil || c.License.Token != "" || c.Egress.ManagementAddr != "" || !plainLabel(c.Client.Name) || c.Egress.Name != a.Profile.EgressId || c.Egress.DisplayName != a.Profile.EgressName || c.Egress.ProxyAddr != a.Profile.ProxyAddress || c.Hub.Endpoint != a.Profile.WgEndpoint || c.Hub.PublicKey != a.Profile.WgPublicKey {
		return errDeviceProjection
	}
	if c.LocalProxy.ListenAddr != "127.0.0.1" || c.LocalProxy.ListenPort < 1 || c.LocalProxy.ListenPort > 65535 || !reflect.DeepEqual(c.WireGuard.AllowedIPs, a.Profile.AllowedIps) || !dc.ValidWireGuardPublicKey(c.WireGuard.PublicKey) {
		return errDeviceProjection
	}
	p, err := netip.ParsePrefix(c.WireGuard.Address)
	legacy := netip.MustParsePrefix("10.66.0.0/24")
	if err != nil || !p.Addr().Is4() || p.Bits() != 32 || p.String() != c.WireGuard.Address || !p.Addr().IsPrivate() || legacy.Contains(p.Addr()) || c.WireGuard.Address == a.Profile.AllowedIps[0] {
		return errDeviceProjection
	}
	return nil
}

// ValidateForProxyStart adds private-key possession and startup freshness to
// structural validation. Legacy config semantics remain unchanged here.
func (c Config) ValidateForProxyStart(now time.Time) error {
	if c.Authorization.Source == "" {
		if !c.Authorization.empty() || len(c.WireGuard.AllowedIPs) != 0 {
			return errDeviceProjection
		}
		return nil
	}
	if c.Validate() != nil || c.Authorization.ValidateFresh(now) != nil {
		return errDeviceProjection
	}
	private, err := wgKey(c.WireGuard.PrivateKey)
	if err != nil {
		return errDeviceProjection
	}
	pub, err := curve25519.X25519(private, curve25519.Basepoint)
	if err != nil || base64.StdEncoding.EncodeToString(pub) != c.WireGuard.PublicKey {
		return errDeviceProjection
	}
	return nil
}
