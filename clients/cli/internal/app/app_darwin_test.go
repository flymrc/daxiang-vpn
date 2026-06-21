//go:build darwin

package app

import (
	"strings"
	"testing"

	"zongheng-vpn/shared/paths"
)

func TestMissingConfigMessageUsesMacOSCommandName(t *testing.T) {
	_, err := loadLocalInstalledConfig(paths.FromRoot(t.TempDir()))
	if err == nil {
		t.Fatal("expected missing config error")
	}
	text := err.Error()
	if strings.Contains(text, "zhvpn.exe") {
		t.Fatalf("macOS error should not mention .exe: %q", text)
	}
	if !strings.Contains(text, "zhvpn login <授权码>") {
		t.Fatalf("missing macOS login hint: %q", text)
	}
}
