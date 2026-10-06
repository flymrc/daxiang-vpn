//go:build !windows

package deviceauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openRegular(path string, create bool) (*os.File, error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if create {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Open(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = f.Close()
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0077 != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("%w: database/fence must be a private owned single-link regular file", ErrPolicy)
	}
	return f, nil
}

func regularPath(f *os.File) (string, error) { return filepath.EvalSymlinks(f.Name()) }

func lockFile(f *os.File) error   { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func isLockBusy(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
