package deviceapi

import (
	"encoding/json"
	"testing"
	"zongheng-vpn/hub/internal/deviceauth"
	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/proxygate"
)

func TestProxyGateAuthorityPolicyEncodingRejectsAliasesAndDuplicates(t *testing.T) {
	p := deviceauth.Policy{Epoch: "owned-epoch", ManagedBy: "owned-manager", AddressPools: []string{"10.250.0.2/32"}, Protected: []deviceauth.Protection{{Prefix: "10.250.0.1/32"}}}
	raw, _ := json.Marshal(p)
	if _, err := decodePolicy(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		append(append([]byte{}, raw...), []byte("\n")...),
		[]byte(`{"epoch":"different","epoch":"owned-epoch","managed_by":"owned-manager","address_pools":["10.250.0.2/32"],"protected":[{"prefix":"10.250.0.1/32"}]}`),
		[]byte(`{"Epoch":"owned-epoch","managed_by":"owned-manager","address_pools":["10.250.0.2/32"],"protected":[{"prefix":"10.250.0.1/32"}]}`),
	} {
		if _, err := decodePolicy(bad); err == nil {
			t.Fatal("ambiguous authority source accepted")
		}
	}
}

func TestProxyGateHostingBindsActualListenerAndEntireSourcePolicy(t *testing.T) {
	profile := apiProxyProfile()
	policy := deviceauth.Policy{Epoch: profile.AuthorityEpoch, ManagedBy: profile.ManagedBy, AddressPools: []string{"10.250.0.30/32", "10.250.0.31/32"}, Protected: []deviceauth.Protection{{Prefix: "10.250.0.1/32"}}}
	policyHash, err := deviceauth.CanonicalPolicyDigest(policy)
	if err != nil {
		t.Fatal(err)
	}
	profileHash, err := dc.ProxyRouteProfileDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	gate := proxygate.Policy{Version: 1, Epoch: policy.Epoch, ManagedBy: policy.ManagedBy, Interface: profile.WgInterface, PolicySHA256: policyHash, ProfileSHA256: profileHash, Listener: profile.ProxyAddress, ManagedSources: policy.AddressPools, RetainedSources: []string{}}
	if err := ValidateProxyGatePolicy(policy, profile, profile.WgInterface, gate); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*proxygate.Policy){
		"different epoch":           func(g *proxygate.Policy) { g.Epoch = "other-epoch" },
		"different manager":         func(g *proxygate.Policy) { g.ManagedBy = "other-owner" },
		"different interface":       func(g *proxygate.Policy) { g.Interface = "other0" },
		"nongated profile listener": func(g *proxygate.Policy) { g.Listener = "10.250.0.1:18082" },
		"authority digest":          func(g *proxygate.Policy) { g.PolicySHA256 = profileHash },
		"profile digest":            func(g *proxygate.Policy) { g.ProfileSHA256 = policyHash },
		"missing scope":             func(g *proxygate.Policy) { g.ManagedSources = g.ManagedSources[:1] },
		"extra scope":               func(g *proxygate.Policy) { g.ManagedSources = append(g.ManagedSources, "10.250.0.32/32") },
		"protected scope":           func(g *proxygate.Policy) { g.ManagedSources = []string{"10.250.0.0/24"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			copy := gate.Clone()
			change(&copy)
			if ValidateProxyGatePolicy(policy, profile, profile.WgInterface, copy) == nil {
				t.Fatal("mismatched hosting grant accepted")
			}
		})
	}
}
