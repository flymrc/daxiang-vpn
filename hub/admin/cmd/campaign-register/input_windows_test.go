//go:build windows

package main

import "testing"

func TestCampaignRegisterWindowsPublicPathSyntaxRejectsAliases(t *testing.T) {
	for _, path := range []string{`\\owned.invalid\share\input.json`, `\\?\C:\input.json`, `\\.\NUL`, `C:input.json`, `C:\input.json:stream`, `NUL`, `C:\CON.txt`, `C:\COM1`, `C:\trailing.\input.json`, `C:\trailing \input.json`, `/dev/null`} {
		if locationAllowed(path) {
			t.Fatal("nonlocal/device/alias syntax accepted")
		}
	}
}
