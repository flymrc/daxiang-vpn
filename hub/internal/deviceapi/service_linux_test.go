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
