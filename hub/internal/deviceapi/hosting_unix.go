//go:build linux

package deviceapi

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func privateHostingDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("device authority requires a preinstalled private service directory")
	}
	// A private leaf underneath an attacker-writable non-sticky ancestor can be
	// renamed/replaced. Validate every ancestor before creating the authority DB.
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		ancestor, err := os.Lstat(parent)
		if err != nil {
			return err
		}
		owner, ok := ancestor.Sys().(*syscall.Stat_t)
		trustedOwner := ok && (int(owner.Uid) == os.Geteuid() || owner.Uid == 0)
		stickyRoot := trustedOwner && owner.Uid == 0 && ancestor.Mode()&os.ModeSticky != 0
		if !trustedOwner || !ancestor.IsDir() || ancestor.Mode()&os.ModeSymlink != 0 || (ancestor.Mode().Perm()&0022 != 0 && !stickyRoot) {
			return fmt.Errorf("device authority service directory has an untrusted ancestor")
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return nil
}
