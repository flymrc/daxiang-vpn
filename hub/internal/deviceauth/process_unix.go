//go:build linux

package deviceauth

import (
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// A process group and a parent's deferred cleanup do not protect a crashed
// authority. SIGKILL closes its fence while wg and descendants can still mutate.
// Pdeathsig alone also cannot order child termination before fence release or
// cover all descendants. Unsupervised execution remains disabled; the dedicated
// independent supervisor owns the execution fence through kill and reap.
func requireBoundedSupervision(supervisor string) error {
	if err := trustedExecutionBinary(supervisor); err != nil {
		return err
	}
	// Fail before launching WG on kernels without the cleanup identity primitive
	// or procfs direct-child enumeration required by the subreaper.
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return ErrSupervision
	}
	_ = unix.Close(fd)
	if _, err = os.ReadFile("/proc/self/task/" + strconv.Itoa(os.Getpid()) + "/children"); err != nil {
		return ErrSupervision
	}
	return nil
}

// A trusted executable cannot sit below a renameable low-trust ancestor. The
// only writable ancestor exception is a root-owned sticky directory, whose
// entries owned by this UID/root cannot be replaced by another UID. Same-UID and
// root writers remain the installed authority trust boundary.
func trustedExecutionBinary(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrSupervision
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 {
		return ErrSupervision
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (st.Uid != uint32(os.Geteuid()) && st.Uid != 0) {
		return ErrSupervision
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		ancestor, err := os.Lstat(parent)
		if err != nil {
			return ErrSupervision
		}
		owner, ok := ancestor.Sys().(*syscall.Stat_t)
		trustedOwner := ok && (owner.Uid == uint32(os.Geteuid()) || owner.Uid == 0)
		stickyRoot := trustedOwner && owner.Uid == 0 && ancestor.Mode()&os.ModeSticky != 0
		if !trustedOwner || !ancestor.IsDir() || ancestor.Mode()&os.ModeSymlink != 0 || (ancestor.Mode().Perm()&0022 != 0 && !stickyRoot) {
			return ErrSupervision
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return nil
}

func startBounded(cmd *exec.Cmd) (func(), error) {
	return nil, ErrSupervision
}

func waitBounded(cmd *exec.Cmd, cleanup func()) error {
	return ErrSupervision
}
