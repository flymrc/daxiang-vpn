package deviceapi

import (
	"net/netip"
	"zongheng-vpn/hub/internal/deviceauth"
	dc "zongheng-vpn/shared/devicecontract"
)

// ReadRouteProfile never repairs source permissions or uses the network. Linux
// hosting loads the protected file; Windows authority fixtures supply a DTO.
func ReadRouteProfile(path string) (dc.ProxyRouteProfile, error) {
	b, err := readProtectedProfile(path)
	if err != nil {
		return dc.ProxyRouteProfile{}, deviceauth.ErrInvalid
	}
	p, err := dc.DecodeProxyRouteProfile(b)
	if err != nil {
		return dc.ProxyRouteProfile{}, deviceauth.ErrInvalid
	}
	return p, nil
}

func profileMatchesHosting(p dc.ProxyRouteProfile, policy deviceauth.Policy, wgInterface string) bool {
	if dc.ValidateProxyRouteProfile(p) != nil || p.AuthorityEpoch != policy.Epoch || p.ManagedBy != policy.ManagedBy || p.WgInterface != wgInterface {
		return false
	}
	proxy := netip.MustParsePrefix(p.AllowedIps[0])
	for _, protection := range policy.Protected {
		prefix, err := netip.ParsePrefix(protection.Prefix)
		if err == nil && prefix == prefix.Masked() && prefix.Contains(proxy.Addr()) {
			return true
		}
	}
	return false
}
