//go:build !windows

package app

import (
	"os"
	"testing"
	"zongheng-vpn/shared/paths"
)

func preparePrivateTestHome(t *testing.T, home paths.Context) {
	t.Helper()
	if err := os.Chmod(home.Root, 0700); err != nil {
		t.Fatal(err)
	}
}
