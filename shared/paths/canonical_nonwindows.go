//go:build !windows

package paths

import "path/filepath"

func canonicalExistingRoot(root string) (string, error) {
	return filepath.EvalSymlinks(root)
}
