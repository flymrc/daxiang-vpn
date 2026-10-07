package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"zongheng-vpn/shared/config"
	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/paths"
)

func ownedDeviceStamp(t *testing.T) config.AuthorizationConfig {
	t.Helper()
	private := [32]byte{9}
	pub, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	p := dc.ProxyRouteProfile{Version: 1, AuthorityEpoch: "owned-epoch", ManagedBy: "owned-customer", WgInterface: "wg-customer", Revision: 1, WgEndpoint: "127.0.0.1:51820", WgPublicKey: base64.StdEncoding.EncodeToString(pub), ProxyAddress: "10.250.0.1:18081", EgressId: "owned-egress", EgressName: "owned egress", AllowedIps: []string{"10.250.0.1/32"}}
	digest, err := dc.ProxyRouteProfileDigest(p)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	return config.AuthorizationConfig{Source: config.DeviceV2Source, DeviceID: strings.Repeat("a", 32), CredentialID: strings.Repeat("b", 32), DesiredGeneration: 1, AppliedGeneration: 1, Profile: p, ProfileSHA256: digest, IssuedUnixSeconds: now, ExpiresUnixSeconds: now + 30}
}

func TestEngineGenerationBindsV2AuthorityAndPreservesLegacy(t *testing.T) {
	content := []byte(`{"inbounds":[]}`)
	legacy := sha256.Sum256(content)
	if configGeneration(content, nil) != hex.EncodeToString(legacy[:]) {
		t.Fatal("legacy generation changed")
	}
	a := ownedDeviceStamp(t)
	one := configGeneration(content, &a)
	a.DesiredGeneration++
	a.AppliedGeneration++
	if configGeneration(content, &a) == one {
		t.Fatal("identical routes hid a new authority generation")
	}
	a = ownedDeviceStamp(t)
	a.Profile.AuthorityEpoch = "other-epoch"
	a.ProfileSHA256, _ = dc.ProxyRouteProfileDigest(a.Profile)
	if configGeneration(content, &a) == one {
		t.Fatal("epoch not bound to engine identity")
	}
}

func TestExpiredV2ProjectionCannotPublishOrActivate(t *testing.T) {
	home, cfg, content := syntheticRuntime(t)
	record, err := prepareLaunch(home, cfg)
	if err != nil {
		t.Fatal(err)
	}
	a := ownedDeviceStamp(t)
	a.IssuedUnixSeconds = time.Now().Unix() - 60
	a.ExpiresUnixSeconds = time.Now().Unix() - 1
	record.Authorization = &a
	record.Identity.Generation = configGeneration(content, &a)
	data, _ := json.Marshal(record)
	if err = writePrivateFile(home, launchPath(home), data); err != nil {
		t.Fatal(err)
	}
	if control, err := beginEngineControl(home, content); err == nil {
		control.close()
		t.Fatal("expired projection published control identity")
	}
	if _, err := os.Stat(statePath(home)); !os.IsNotExist(err) {
		t.Fatal("refused launch published state")
	}

	a = ownedDeviceStamp(t)
	a.ExpiresUnixSeconds = time.Now().Unix() + 1
	record.Authorization = &a
	record.Identity.Generation = configGeneration(content, &a)
	data, _ = json.Marshal(record)
	if err = writePrivateFile(home, launchPath(home), data); err != nil {
		t.Fatal(err)
	}
	control, err := beginEngineControl(home, content)
	if err != nil {
		t.Fatal(err)
	}
	defer control.close()
	control.ready()
	time.Sleep(time.Until(time.Unix(a.ExpiresUnixSeconds, 0)) + 20*time.Millisecond)
	status, err := requestControl(control.record, "activate")
	if err != nil || status.State != "stopping" {
		t.Fatalf("expired startup was acknowledged ready: %s %v", status.State, err)
	}
	select {
	case <-control.done:
	default:
		t.Fatal("expired startup did not withdraw owned child")
	}
}

func TestPrecancelledStartCreatesNoLaunch(t *testing.T) {
	home := paths.FromRoot(t.TempDir() + "/never-created")
	operation, cancel := context.WithCancel(context.Background())
	cancel()
	if StartContext(operation, home, config.Config{}, false) == nil {
		t.Fatal("precancelled start succeeded")
	}
	if _, err := os.Stat(home.Root); !os.IsNotExist(err) {
		t.Fatal("precancelled start wrote state")
	}
}
