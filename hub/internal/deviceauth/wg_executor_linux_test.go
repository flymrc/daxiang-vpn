//go:build linux

package deviceauth

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func trustedExecutableFixture(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// Never executed: these permission fixtures must be refused before launch.
	if err := os.WriteFile(path, []byte("permission-fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLinuxWGAndSupervisorRejectUntrustedExecutablePaths(t *testing.T) {
	for _, target := range []string{"wg", "helper"} {
		for _, fault := range []string{"writable-file", "non-executable", "writable-ancestor", "symlink-file", "symlink-ancestor", "untrusted-owner", "untrusted-ancestor-owner"} {
			t.Run(target+"/"+fault, func(t *testing.T) {
				if (fault == "untrusted-owner" || fault == "untrusted-ancestor-owner") && os.Geteuid() != 0 {
					t.Skip("actual ownership fixture requires root; separately executed in private WSL root test")
				}
				dir := t.TempDir()
				ancestor := filepath.Join(dir, "ancestor")
				if err := os.Mkdir(ancestor, 0700); err != nil {
					t.Fatal(err)
				}
				leaf := filepath.Join(ancestor, "private")
				if err := os.Mkdir(leaf, 0700); err != nil {
					t.Fatal(err)
				}
				binary := trustedExecutableFixture(t, leaf, "wg")
				helper := trustedExecutableFixture(t, dir, "helper")
				bad := &binary
				if target == "helper" {
					helper = trustedExecutableFixture(t, leaf, "helper")
					binary = trustedExecutableFixture(t, dir, "wg")
					bad = &helper
				}
				switch fault {
				case "writable-file":
					if err := os.Chmod(*bad, 0777); err != nil {
						t.Fatal(err)
					}
				case "non-executable":
					if err := os.Chmod(*bad, 0600); err != nil {
						t.Fatal(err)
					}
				case "writable-ancestor":
					if err := os.Chmod(ancestor, 0777); err != nil {
						t.Fatal(err)
					}
				case "symlink-file":
					link := filepath.Join(dir, "executable-alias")
					if err := os.Symlink(*bad, link); err != nil {
						t.Fatal(err)
					}
					*bad = link
				case "symlink-ancestor":
					link := filepath.Join(dir, "parent-alias")
					if err := os.Symlink(ancestor, link); err != nil {
						t.Fatal(err)
					}
					*bad = filepath.Join(link, "private", filepath.Base(*bad))
				case "untrusted-owner":
					if err := os.Chown(*bad, 65534, -1); err != nil {
						t.Fatal(err)
					}
				case "untrusted-ancestor-owner":
					if err := os.Chown(ancestor, 65534, -1); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := NewSupervisedWGExecutor(binary, helper, "test-wg", testPolicy(), time.Second); err == nil {
					t.Fatal("untrusted executable accepted before privileged launch")
				}
				if target == "wg" && supervisorArguments([]string{"zhvpn-supervisor-v1", binary, "show", "test-wg", "allowed-ips"}) {
					t.Fatal("standalone helper bypassed wg installation trust")
				}
			})
		}
	}
}

func TestLinuxExecutableTrustRecheckedBeforeEveryLaunch(t *testing.T) {
	for _, target := range []string{"wg", "helper"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			binary := trustedExecutableFixture(t, dir, "wg")
			helper := trustedExecutableFixture(t, dir, "helper")
			e, err := NewSupervisedWGExecutor(binary, helper, "test-wg", testPolicy(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			bad := binary
			if target == "helper" {
				bad = helper
			}
			if err := os.Chmod(bad, 0777); err != nil {
				t.Fatal(err)
			}
			s, _, _ := newTestStore(t)
			unlock, fence, err := s.acquirePinned(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			ctx := context.WithValue(context.Background(), executionFenceKey{}, fence)
			if _, err := e.Snapshot(ctx, Fence{Epoch: testPolicy().Epoch, DeviceID: "d1", Generation: 1, Sequence: 1}); !errors.Is(err, ErrSupervision) {
				t.Fatalf("changed executable was launched: %v", err)
			}
		})
	}
}

func TestLinuxUnsupervisedWGExecutorCannotStart(t *testing.T) {
	// Own test executable is a real, regular trusted executable, but that alone
	// does not make it safe to execute after an authority-parent crash.
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewWGExecutor(binary, "test-wg", testPolicy(), time.Second); !errors.Is(err, ErrSupervision) {
		t.Fatalf("Linux constructor accepted an unsupervised external executor: %v", err)
	}
	cmd := exec.Command(binary, "-test.run=^TestDeviceAuthProcessHelper$")
	cleanup, err := startBounded(cmd)
	if !errors.Is(err, ErrSupervision) || cleanup != nil || cmd.Process != nil {
		t.Fatal("Linux low-level path started a subprocess without a supervisor")
	}
	// Also protect an internal/later constructor that might forget the gate.
	a := &WGExecutor{binary: binary, iface: "test-wg", policy: testPolicy(), timeout: time.Second}
	_, err = a.Snapshot(context.Background(), Fence{Epoch: testPolicy().Epoch, DeviceID: "d1", Generation: 1, Sequence: 1})
	if !errors.Is(err, ErrExecutionUnknown) {
		t.Fatalf("Linux execution bypass reported success: %v", err)
	}
}
