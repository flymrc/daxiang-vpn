//go:build integration && !windows

package deviceapi

import (
	"os"
	"testing"
)

func cliExecutableSuffix() string { return "" }
func prepareCLIPrivateHome(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
}
