//go:build linux

package deviceapi

import (
	"bytes"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"zongheng-vpn/hub/internal/deviceauth"
)

// Pin root->leaf with openat/nofollow and inspect held handles. O_NONBLOCK makes
// substitution by a FIFO fail promptly. Source ownership/mode is never changed.
func readProtectedProfile(path string) ([]byte, error) {
	return readProtectedSource(path, 32768)
}

func readProtectedSource(path string, maxBytes int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, deviceauth.ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(parts) < 2 {
		return nil, deviceauth.ErrInvalid
	}
	held := []*os.File{}
	defer func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i].Close()
		}
	}()
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, deviceauth.ErrInvalid
	}
	current := "/"
	held = append(held, os.NewFile(uintptr(fd), current))
	for _, part := range parts[:len(parts)-1] {
		fd, err = unix.Openat(int(held[len(held)-1].Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, deviceauth.ErrInvalid
		}
		current = filepath.Join(current, part)
		held = append(held, os.NewFile(uintptr(fd), current))
	}
	checkParents := func() error {
		for i, f := range held {
			info, err := f.Stat()
			now, pe := os.Lstat(f.Name())
			if err != nil || pe != nil || !info.IsDir() || now.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, now) {
				return deviceauth.ErrInvalid
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			stickyRoot := ok && st.Uid == 0 && info.Mode()&os.ModeSticky != 0
			if !ok || (st.Uid != uint32(os.Geteuid()) && st.Uid != 0) || (info.Mode().Perm()&0022 != 0 && !stickyRoot) || (i == len(held)-1 && (st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0)) {
				return deviceauth.ErrInvalid
			}
		}
		return nil
	}
	if checkParents() != nil {
		return nil, deviceauth.ErrInvalid
	}
	fd, err = unix.Openat(int(held[len(held)-1].Fd()), parts[len(parts)-1], unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, deviceauth.ErrInvalid
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, deviceauth.ErrInvalid
	}
	check := func() error {
		info, err := f.Stat()
		now, pe := os.Lstat(path)
		if err != nil || pe != nil || !info.Mode().IsRegular() || now.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, info) || !os.SameFile(info, now) || info.Size() != before.Size() || !info.ModTime().Equal(before.ModTime()) || info.Size() < 1 || info.Size() > maxBytes {
			return deviceauth.ErrInvalid
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || info.Mode().Perm()&0077 != 0 {
			return deviceauth.ErrInvalid
		}
		return checkParents()
	}
	if check() != nil {
		return nil, deviceauth.ErrInvalid
	}
	read := func() ([]byte, error) {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, deviceauth.ErrInvalid
		}
		b, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
		if err != nil || int64(len(b)) != before.Size() || check() != nil {
			return nil, deviceauth.ErrInvalid
		}
		return b, nil
	}
	b, err := read()
	if err != nil {
		return nil, err
	}
	again, err := read()
	if err != nil || !bytes.Equal(b, again) {
		return nil, deviceauth.ErrInvalid
	}
	return b, nil
}
