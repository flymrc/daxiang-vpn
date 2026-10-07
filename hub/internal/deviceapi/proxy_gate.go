package deviceapi

import (
	"context"
	"net/netip"
	"sort"
	"time"
	"zongheng-vpn/hub/internal/deviceauth"
	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/proxygate"
)

type proxyController interface {
	Policy() proxygate.Policy
	GrantUntil(context.Context, time.Time) error
	Closed(context.Context) error
	Close() error
}

// ValidateProxyGatePolicy refuses arbitrary nongated listeners and ambiguous
// source ownership. The receiver additionally attests its actual bound listener
// and the exact immutable policy in the closed-ACK handshake.
func ValidateProxyGatePolicy(policy deviceauth.Policy, profile dc.ProxyRouteProfile, iface string, gate proxygate.Policy) error {
	if !profileMatchesHosting(profile, policy, iface) || gate.Validate() != nil || gate.Epoch != policy.Epoch || gate.ManagedBy != policy.ManagedBy || gate.Interface != iface || gate.Listener != profile.ProxyAddress {
		return deviceauth.ErrPolicy
	}
	policyDigest, err := deviceauth.CanonicalPolicyDigest(policy)
	if err != nil || gate.PolicySHA256 != policyDigest {
		return deviceauth.ErrPolicy
	}
	profileDigest, err := dc.ProxyRouteProfileDigest(profile)
	if err != nil || gate.ProfileSHA256 != profileDigest {
		return deviceauth.ErrPolicy
	}
	pools := append([]string(nil), policy.AddressPools...)
	sources := append([]string(nil), gate.ManagedSources...)
	sort.Strings(pools)
	sort.Strings(sources)
	if len(pools) != len(sources) {
		return deviceauth.ErrPolicy
	}
	for i, pool := range pools {
		if pool != sources[i] {
			return deviceauth.ErrPolicy
		}
		p, err := netip.ParsePrefix(pool)
		if err != nil || p != p.Masked() {
			return deviceauth.ErrPolicy
		}
		for _, protection := range policy.Protected {
			if protection.Prefix == "" {
				continue
			}
			protected, err := netip.ParsePrefix(protection.Prefix)
			if err != nil || p.Overlaps(protected) {
				return deviceauth.ErrProtected
			}
		}
	}
	return nil
}

func (s *Service) grantProxy(ctx context.Context, control proxyController) error {
	return s.Scheduler.WithProxyConvergence(ctx, s.gatePolicy.ManagedSources, func(ctx context.Context, p deviceauth.ProxyConvergenceProof) error {
		if p.Epoch != s.gatePolicy.Epoch || p.ManagedBy != s.gatePolicy.ManagedBy || p.PolicySHA256 != s.gatePolicy.PolicySHA256 {
			return deviceauth.ErrPolicy
		}
		until := time.Now().UTC().Add(2 * time.Second)
		if !p.EarliestValidUntil.IsZero() {
			if p.EarliestValidUntil.Before(until) {
				until = p.EarliestValidUntil
			}
		}
		if until.Sub(time.Now().UTC()) < proxygate.MinLease {
			return deviceauth.ErrExpired
		}
		// Exact absolute expiry survives sender scheduling delays; neither the
		// receiver nor the ACK may extend a device/current-credential lifetime.
		return control.GrantUntil(ctx, until)
	})
}
