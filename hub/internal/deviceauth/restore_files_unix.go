//go:build !windows

package deviceauth

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"syscall"
)

func restoreOpenPrivate(path string) (*os.File, []*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, ErrRestorePlan
	}
	parents := []*os.File{}
	chain := []string{}
	for parent := filepath.Dir(abs); ; parent = filepath.Dir(parent) {
		chain = append(chain, parent)
		if filepath.Dir(parent) == parent {
			break
		}
	}
	fail := func() (*os.File, []*os.File, error) {
		for _, f := range parents {
			f.Close()
		}
		return nil, nil, ErrRestorePlan
	}
	for i := len(chain) - 1; i >= 0; i-- {
		parent := chain[i]
		var fd int
		var e error
		if len(parents) == 0 {
			fd, e = unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		} else {
			fd, e = unix.Openat(int(parents[len(parents)-1].Fd()), filepath.Base(parent), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if e != nil {
			return fail()
		}
		f := os.NewFile(uintptr(fd), parent)
		parents = append(parents, f)
		info, e := f.Stat()
		if e != nil {
			return fail()
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		stickyRoot := ok && st.Uid == 0 && info.Mode()&os.ModeSticky != 0
		if !ok || !info.IsDir() || (st.Uid != uint32(os.Geteuid()) && st.Uid != 0) || (info.Mode().Perm()&0022 != 0 && !stickyRoot) {
			return fail()
		}
		if i == 0 && (st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0) {
			return fail()
		}
	}
	fd, err := unix.Openat(int(parents[len(parents)-1].Fd()), filepath.Base(abs), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	f := os.NewFile(uintptr(fd), abs)
	r := &restoreInput{file: f, parents: parents}
	r.info, err = f.Stat()
	if err != nil || restoreVerifyPinned(r) != nil {
		f.Close()
		return fail()
	}
	return f, parents, nil
}
func restoreVerifyPinned(r *restoreInput) error {
	info, err := r.file.Stat()
	if err != nil {
		return restoreFailure("input_stat")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 || !os.SameFile(r.info, info) || r.info.Size() != info.Size() || !r.info.ModTime().Equal(info.ModTime()) {
		return restoreFailure("unsafe_or_changed_input")
	}
	current, err := os.Lstat(r.file.Name())
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, current) {
		return restoreFailure("input_path_changed")
	}
	for i, f := range r.parents {
		held, e := f.Stat()
		now, le := os.Lstat(f.Name())
		if e != nil || le != nil || now.Mode()&os.ModeSymlink != 0 || !os.SameFile(held, now) {
			return restoreFailure("input_ancestor_changed")
		}
		st, ok := held.Sys().(*syscall.Stat_t)
		stickyRoot := ok && st.Uid == 0 && held.Mode()&os.ModeSticky != 0
		if !ok || !held.IsDir() || (st.Uid != uint32(os.Geteuid()) && st.Uid != 0) || (held.Mode().Perm()&0022 != 0 && !stickyRoot) || (i == len(r.parents)-1 && (st.Uid != uint32(os.Geteuid()) || held.Mode().Perm()&0077 != 0)) {
			return restoreFailure("input_ancestor_security_changed")
		}
	}
	return nil
}
func restoreOwnedCopy(parent string, b []byte) (string, func(), error) {
	dir, err := os.MkdirTemp(parent, ".zhvpn-restore-read-")
	if err != nil {
		return "", nil, restoreFailure("private_copy_directory")
	}
	path := filepath.Join(dir, "snapshot.sqlite")
	cleanup := func() { os.Remove(path); os.Remove(dir) }
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		cleanup()
		return "", nil, restoreFailure("private_copy_file")
	}
	f := os.NewFile(uintptr(fd), path)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil || ce != nil {
		cleanup()
		return "", nil, restoreFailure("private_copy_write")
	}
	return path, cleanup, nil
}
