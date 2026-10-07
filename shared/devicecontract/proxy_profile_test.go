package devicecontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func profileFixture() ProxyRouteProfile {
	return ProxyRouteProfile{Version: 1, AuthorityEpoch: "fixture-epoch", ManagedBy: "fixture-owner", WgInterface: "wg-customer", Revision: 7, WgEndpoint: "127.0.0.1:51820", WgPublicKey: "3p7bfXt9wbTTW2HC7OQ1Nz+DQ8hbeGdNrfx+FG+IK08=", ProxyAddress: "10.250.0.1:18081", EgressId: "fixture-egress", EgressName: "Fixture egress", AllowedIps: []string{"10.250.0.1/32"}}
}

func TestProxyProfileCanonicalDigestAndInputCopies(t *testing.T) {
	p := profileFixture()
	got, err := ProxyRouteProfileDigest(p)
	if err != nil {
		t.Fatal(err)
	}
	// An independently fixed wire vector establishes field order and domain.
	vector := `["zhvpn-device-route","v1","1","fixture-epoch","fixture-owner","wg-customer","7","127.0.0.1:51820","3p7bfXt9wbTTW2HC7OQ1Nz+DQ8hbeGdNrfx+FG+IK08=","10.250.0.1:18081","fixture-egress","Fixture egress","10.250.0.1/32"]`
	h := sha256.Sum256([]byte(vector))
	if got != hex.EncodeToString(h[:]) {
		t.Fatal("canonical vector drift")
	}
	p.Revision++
	changed, _ := ProxyRouteProfileDigest(p)
	if changed == got {
		t.Fatal("revision not bound")
	}
	data, _ := json.Marshal(profileFixture())
	decoded, err := DecodeProxyRouteProfile(data)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'x'
	if decoded.AuthorityEpoch != "fixture-epoch" {
		t.Fatal("input aliases output")
	}
}

func TestProxyProfileRejectsRouteAndKeyAmbiguities(t *testing.T) {
	cases := map[string]func(*ProxyRouteProfile){
		"version": func(p *ProxyRouteProfile) { p.Version = 2 }, "revision": func(p *ProxyRouteProfile) { p.Revision = ProxySafeInteger + 1 },
		"epoch": func(p *ProxyRouteProfile) { p.AuthorityEpoch = " epoch" }, "interface": func(p *ProxyRouteProfile) { p.WgInterface = "wg;exec" },
		"default": func(p *ProxyRouteProfile) { p.AllowedIps = []string{"0.0.0.0/0"} }, "subnet": func(p *ProxyRouteProfile) { p.AllowedIps = []string{"10.250.0.0/24"} },
		"management": func(p *ProxyRouteProfile) {
			p.ProxyAddress = "10.66.0.1:18081"
			p.AllowedIps = []string{"10.66.0.1/32"}
		},
		"multiple": func(p *ProxyRouteProfile) { p.AllowedIps = append(p.AllowedIps, "10.250.0.2/32") }, "differentproxy": func(p *ProxyRouteProfile) { p.AllowedIps = []string{"10.250.0.2/32"} },
		"publicproxy": func(p *ProxyRouteProfile) { p.ProxyAddress = "192.0.2.1:80"; p.AllowedIps = []string{"192.0.2.1/32"} },
		"dns":         func(p *ProxyRouteProfile) { p.WgEndpoint = "localhost:51820" }, "zeroendpoint": func(p *ProxyRouteProfile) { p.WgEndpoint = "0.0.0.0:51820" },
		"multicast": func(p *ProxyRouteProfile) { p.WgEndpoint = "224.0.0.1:51820" }, "portalias": func(p *ProxyRouteProfile) { p.WgEndpoint = "127.0.0.1:051820" },
		"zone": func(p *ProxyRouteProfile) { p.WgEndpoint = "[fe80::1%lo]:51820" }, "loopbackproxy": func(p *ProxyRouteProfile) {
			p.ProxyAddress = "127.0.0.1:18081"
			p.AllowedIps = []string{"127.0.0.1/32"}
		},
		"zero-key": func(p *ProxyRouteProfile) { p.WgPublicKey = base64.StdEncoding.EncodeToString(make([]byte, 32)) },
		"loworder-key": func(p *ProxyRouteProfile) {
			b := make([]byte, 32)
			b[0] = 1
			p.WgPublicKey = base64.StdEncoding.EncodeToString(b)
		},
		"noncanonical-key": func(p *ProxyRouteProfile) {
			b := bytes.Repeat([]byte{255}, 32)
			b[0] = 237
			b[31] = 127
			p.WgPublicKey = base64.StdEncoding.EncodeToString(b)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := profileFixture()
			change(&p)
			if ValidateProxyRouteProfile(p) == nil {
				t.Fatal("invalid route accepted")
			}
			if _, err := ProxyRouteProfileDigest(p); err == nil {
				t.Fatal("invalid route digested")
			}
		})
	}
	for _, bad := range []string{"10.66.0.30/32", "10.250.0.0/24", "192.0.2.30/32", "::ffff:10.250.0.30/128"} {
		if ValidProxyCustomerAddress(bad) {
			t.Fatal("invalid customer host")
		}
	}
	if !ValidProxyCustomerAddress("10.250.0.30/32") {
		t.Fatal("customer host rejected")
	}
}

func TestProxyProfileStrictBoundedJSON(t *testing.T) {
	data, _ := json.Marshal(profileFixture())
	bad := [][]byte{
		[]byte(strings.Replace(string(data), `"revision":7`, `"revision":7,"revision":7`, 1)),
		[]byte(strings.Replace(string(data), `"revision":7`, `"Revision":7`, 1)),
		[]byte(strings.Replace(string(data), `"revision":7`, `"revision":null`, 1)),
		[]byte(strings.Replace(string(data), `"revision":7`, `"revision":9223372036854775808`, 1)),
		[]byte(strings.Replace(string(data), `"revision":7`, `"revision":9007199254740992`, 1)),
		[]byte(strings.Replace(string(data), `"revision":7`, `"revision":7.0`, 1)),
		[]byte(strings.Replace(string(data), `"revision":7,`, "", 1)),
		[]byte(strings.Replace(string(data), `"allowed_ips":["10.250.0.1/32"]`, `"allowed_ips":[{"nested":null}]`, 1)),
		[]byte(strings.Replace(string(data), `"egress_name":"Fixture egress"`, `"egress_name":"\ud800"`, 1)),
		append(append([]byte{}, data...), []byte(` {}`)...),
		bytes.Repeat([]byte{' '}, 32769),
	}
	invalidUTF8 := append([]byte{}, data...)
	invalidUTF8[10] = 255
	bad = append(bad, invalidUTF8)
	for i, b := range bad {
		if _, err := DecodeProxyRouteProfile(b); err == nil {
			t.Fatalf("bad profile %d accepted", i)
		}
	}
	if _, err := DecodeProxyRouteProfile(data); err != nil {
		t.Fatal(err)
	}
}
