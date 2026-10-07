//go:build !windows

package updateclient

import (
	"os"
	"testing"
	"zongheng-vpn/shared/paths"
)

func privateHome(t *testing.T) paths.Context {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	return paths.FromRoot(root)
}
