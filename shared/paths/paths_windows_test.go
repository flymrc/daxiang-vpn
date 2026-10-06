//go:build windows

package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCanonicalRootResolvesWindowsJunction(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	// Junction creation needs no elevation or Developer Mode, unlike Windows
	// symlinks. Environment arguments keep temporary paths out of shell code.
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"New-Item -ItemType Junction -Path $env:ZHVPN_TEST_ALIAS -Target $env:ZHVPN_TEST_TARGET -ErrorAction Stop | Out-Null")
	cmd.Env = append(os.Environ(), "ZHVPN_TEST_ALIAS="+alias, "ZHVPN_TEST_TARGET="+real)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test junction: %v: %s", err, output)
	}
	want, err := CanonicalRoot(filepath.Join(real, "not-created"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalRoot(filepath.Join(alias, "not-created"))
	if err != nil || got != want {
		t.Fatalf("junction root=%q, err=%v, want=%q", got, err, want)
	}
}
