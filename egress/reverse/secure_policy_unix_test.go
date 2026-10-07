//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecureTCPRejectsReadablePrivateKeyOnUnix(t *testing.T) {
	f := newSecureTestFixture(t)
	if err := os.Chmod(f.clientOptions.TLSKeyFile, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := secureClientTLSConfig(f.clientOptions); err == nil {
		t.Fatal("group/other readable private key accepted")
	}
}

func TestSecureAuthorityRejectsReplaceableDirectoryOnUnix(t *testing.T) {
	for _, mode := range []os.FileMode{0755, 0777} {
		f := newSecureTestFixture(t)
		if err := os.Chmod(f.dir, mode); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadSecureRegistry(f.serverOptions.EgressRegistryFile, f.serverOptions.HubID); err == nil {
			t.Fatal("non-private registry namespace accepted")
		}
		if lock, err := lockSecureRegistry(f.serverOptions.EgressRegistryFile); err == nil {
			lock.Close()
			t.Fatal("replaceable lock namespace accepted")
		}
		if _, _, err := secureClientTLSConfig(f.clientOptions); err == nil {
			t.Fatal("non-private key/trust namespace accepted")
		}
	}
}

func TestSecureAuthorityChecksAncestorNamespaceOnUnix(t *testing.T) {
	ancestor := t.TempDir()
	private := filepath.Join(ancestor, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(private, "authority.json")
	if err := validateSecureDirectoryPath(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ancestor, 0777); err != nil {
		t.Fatal(err)
	}
	if err := validateSecureDirectoryPath(path); err == nil {
		t.Fatal("private leaf directory accepted below replaceable ancestor")
	}
	if os.Geteuid() == 0 {
		if err := os.Chmod(ancestor, 0777|os.ModeSticky); err != nil {
			t.Fatal(err)
		}
		if err := validateSecureDirectoryPath(path); err != nil {
			t.Fatalf("root-owned sticky ancestor rejected: %v", err)
		}
		if err := os.Chmod(ancestor, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(ancestor, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		if err := validateSecureDirectoryPath(path); err == nil {
			t.Fatal("foreign-owned non-writable ancestor accepted")
		}
	}
}

func TestSecureTCPRejectsWritableAuthorityOnUnix(t *testing.T) {
	f := newSecureTestFixture(t)
	if err := os.Chmod(f.serverOptions.EgressRegistryFile, 0666); err != nil {
		t.Fatal(err)
	}
	if s, err := newSecureTCPServer(f.serverOptions); err == nil {
		s.close()
		t.Fatal("group/other writable authorization accepted")
	}
}
