//go:build linux

package deviceapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"zongheng-vpn/hub/internal/deviceauth"
	dc "zongheng-vpn/shared/devicecontract"
)

func TestLinuxHostingWithoutSupervisorCreatesNoAuthorityDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(dir, "policy.json")
	policy := deviceauth.Policy{Epoch: "hosting-test", ManagedBy: "hosting-test", AddressPools: []string{"10.250.0.0/24"}}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(policyPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "authority.sqlite")
	_, err = Open(context.Background(), Config{ListenAddr: "127.0.0.1:18443", DBPath: dbPath, PolicyPath: policyPath, WGExecutable: binary, WGInterface: "test-wg", TLSCert: "synthetic-cert", TLSKey: "synthetic-key"})
	if !errors.Is(err, deviceauth.ErrInvalid) && !errors.Is(err, deviceauth.ErrSupervision) {
		t.Fatalf("unprotected hosting failure unexpected: %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("refused Linux hosting created an authority database")
	}
}

func TestLinuxProfileHostingWithoutGateCreatesNoAuthorityDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	profile := apiProxyProfile()
	policy := deviceauth.Policy{Epoch: profile.AuthorityEpoch, ManagedBy: profile.ManagedBy, AddressPools: []string{"10.250.0.30/32"}, Protected: []deviceauth.Protection{{Prefix: "10.250.0.1/32"}}}
	policyPath := filepath.Join(dir, "policy.json")
	raw, _ := json.Marshal(policy)
	if err := os.WriteFile(policyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(dir, "profile.json")
	raw, _ = json.Marshal(profile)
	if dc.ValidateProxyRouteProfile(profile) != nil {
		t.Fatal("invalid owned profile")
	}
	if err := os.WriteFile(profilePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "authority.sqlite")
	_, err := Open(context.Background(), Config{ListenAddr: "127.0.0.1:18443", DBPath: dbPath, PolicyPath: policyPath, ProxyProfilePath: profilePath, WGInterface: profile.WgInterface, TLSCert: "synthetic", TLSKey: "synthetic"})
	if !errors.Is(err, deviceauth.ErrPolicy) {
		t.Fatalf("profile without actual gated listener must be refused: %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("missing gate created initialized-looking authority DB")
	}
}
