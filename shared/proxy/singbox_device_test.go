//go:build windows || darwin

package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"zongheng-vpn/shared/config"
)

func TestV2GeneratedConfigRoutesOnlyExactProxyAndRefusesOverrides(t *testing.T) {
	home, _, _ := syntheticRuntime(t)
	a := ownedDeviceStamp(t)
	p := a.Profile
	private := [32]byte{9}
	c := config.Config{Authorization: a, Client: config.ClientConfig{Name: "owned-device"}, Hub: config.HubConfig{Endpoint: p.WgEndpoint, PublicKey: p.WgPublicKey}, Egress: config.EgressConfig{Name: p.EgressId, DisplayName: p.EgressName, ProxyAddr: p.ProxyAddress}, WireGuard: config.WireGuardConfig{Address: "10.250.0.2/32", PrivateKey: base64.StdEncoding.EncodeToString(private[:]), PublicKey: p.WgPublicKey, AllowedIPs: append([]string(nil), p.AllowedIps...)}, LocalProxy: config.LocalProxyConfig{ListenAddr: "127.0.0.1", ListenPort: 7890}}
	if err := WriteSingBoxConfig(home, c, false); err != nil {
		t.Fatal(err)
	}
	data, err := readPrivateFile(home, home.SingBoxConfig)
	if err != nil {
		t.Fatal(err)
	}
	var sb singBoxConfig
	if json.Unmarshal(data, &sb) != nil || !reflect.DeepEqual(sb.Endpoints[0].Peers[0].AllowedIPs, []string{"10.250.0.1/32"}) || sb.Endpoints[0].System {
		t.Fatal("v2 route broadened or elevated")
	}
	// The public start adapter must verify the existing bytes, rather than
	// granting the authority stamp to whatever a prior writer left on disk.
	if _, err := prepareLaunch(home, c); err != nil {
		t.Fatal(err)
	}
	launchBytes, err := readPrivateFile(home, launchPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var record controlRecord
	if json.Unmarshal(launchBytes, &record) != nil || record.Authorization == nil {
		t.Fatal("canonical launch lost authority stamp")
	}
	removeLaunch(home, record.Identity.InstanceID)
	wrong := []byte(`{"log":{"level":"error"},"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`)
	if err := writePrivateFile(home, home.SingBoxConfig, wrong); err != nil {
		t.Fatal(err)
	}
	var denied *RuntimeError
	if err := StartContext(context.Background(), home, c, false); !errors.As(err, &denied) || denied.Code != "engine_config_refused" {
		t.Fatal("v2 stamp accepted unrelated data-plane bytes")
	}
	if _, err := os.Stat(launchPath(home)); !os.IsNotExist(err) {
		t.Fatal("rejected bytes created launch identity")
	}
	if err := writePrivateFile(home, home.SingBoxConfig, data); err != nil {
		t.Fatal(err)
	}
	c.WireGuard.AllowedIPs = []string{"10.66.0.0/24"}
	if WriteSingBoxConfig(home, c, false) == nil {
		t.Fatal("legacy route accepted for v2")
	}
	after, _ := readPrivateFile(home, home.SingBoxConfig)
	if string(data) != string(after) {
		t.Fatal("refused override changed existing config")
	}
	c.WireGuard.AllowedIPs = p.AllowedIps
	if WriteSingBoxConfig(home, c, true) == nil {
		t.Fatal("v2 elevated path silently enabled")
	}
}
