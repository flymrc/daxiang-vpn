//go:build darwin

package paths

import (
	"path/filepath"
	"testing"
)

func TestNewContextUsesMacOSApplicationSupportByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZHVPN_HOME", "")
	t.Setenv("LOCALAPPDATA", "")

	ctx, err := NewContext()
	if err != nil {
		t.Fatal(err)
	}

	wantRoot := filepath.Join(home, "Library", "Application Support", "ZonghengVPN")
	if ctx.Root != wantRoot {
		t.Fatalf("root = %q, want %q", ctx.Root, wantRoot)
	}
	if ctx.ConfigPath != filepath.Join(wantRoot, "config.yaml") {
		t.Fatalf("config path = %q", ctx.ConfigPath)
	}
	if ctx.SingBoxPath != filepath.Join(wantRoot, "bin", "zhvpn") {
		t.Fatalf("binary path = %q", ctx.SingBoxPath)
	}
}
