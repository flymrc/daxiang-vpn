//go:build linux

package deviceapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"zongheng-vpn/hub/internal/deviceauth"
)

func privateProfileFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "profile.json")
	b, _ := json.Marshal(apiProxyProfile())
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestLinuxProxyProfileProtectedReadAndRefusedAliases(t *testing.T) {
	dir, path := privateProfileFixture(t)
	if _, err := ReadRouteProfile(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRouteProfile(path); err == nil {
		t.Fatal("public source accepted")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0644 {
		t.Fatal("reader repaired source permissions")
	}
	os.Chmod(path, 0600)
	alias := filepath.Join(dir, "alias.json")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRouteProfile(alias); err == nil {
		t.Fatal("symlink accepted")
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRouteProfile(path); err == nil {
		t.Fatal("hardlinked source accepted")
	}
	os.Remove(link)
	fifo := filepath.Join(dir, "fifo.json")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := ReadRouteProfile(fifo); err == nil {
		t.Fatal("FIFO accepted")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("FIFO blocked protected read")
	}
	if _, err := ReadRouteProfile(dir); err == nil {
		t.Fatal("directory accepted")
	}
	large := filepath.Join(dir, "large.json")
	if err := os.WriteFile(large, make([]byte, 32769), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRouteProfile(large); err == nil {
		t.Fatal("large profile accepted")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRouteProfile(path); err == nil {
		t.Fatal("public namespace accepted")
	}
	os.Chmod(dir, 0700)
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("profile reader changed content")
	}
}

func TestLinuxProxyProfileRejectsAncestorAlias(t *testing.T) {
	dir, path := privateProfileFixture(t)
	outer := t.TempDir()
	os.Chmod(outer, 0700)
	alias := filepath.Join(outer, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRouteProfile(filepath.Join(alias, filepath.Base(path))); err == nil {
		t.Fatal("ancestor symlink accepted")
	}
	if _, err := ReadRouteProfile(filepath.Base(path)); err == nil {
		t.Fatal("relative hosting path accepted")
	}
}

func TestLinuxBadProxyProfileHostingDoesNotCreateAuthority(t *testing.T) {
	dir, path := privateProfileFixture(t)
	policyPath := filepath.Join(dir, "policy.json")
	policy := deviceauth.Policy{Epoch: "another-epoch", ManagedBy: "test-api-executor", AddressPools: []string{"10.250.0.0/24"}, Protected: []deviceauth.Protection{{Prefix: "10.250.0.1/32"}}}
	b, _ := json.Marshal(policy)
	if err := os.WriteFile(policyPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "authority.sqlite")
	_, err := Open(context.Background(), Config{ListenAddr: "127.0.0.1:18443", DBPath: dbPath, PolicyPath: policyPath, ProxyProfilePath: path, WGInterface: "wg-customer", TLSCert: "synthetic", TLSKey: "synthetic"})
	if err == nil {
		t.Fatal("mismatch hosting accepted")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("bad profile created authority")
	}
}
