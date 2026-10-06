//go:build !windows

package deviceauth

import "os"

func createDirectoryAlias(alias, target string) error { return os.Symlink(target, alias) }
