//go:build linux

package proxygate

import (
	"bytes"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Pin every namespace component, allowing root-owned sticky ancestors (/tmp)
// but requiring a private current-UID leaf. No chmod/chown or repair is done.
type directory struct {
	held []*os.File
	path string
}

func pinDirectory(path string) (*directory, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, ErrPolicy
	}
	d := &directory{path: path}
	fail := func() (*directory, error) { d.close(); return nil, ErrPolicy }
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return fail()
	}
	d.held = append(d.held, os.NewFile(uintptr(fd), "/"))
	current := "/"
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		fd, e = unix.Openat(int(d.held[len(d.held)-1].Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return fail()
		}
		current = filepath.Join(current, part)
		d.held = append(d.held, os.NewFile(uintptr(fd), current))
	}
	if !d.valid() {
		return fail()
	}
	return d, nil
}
func (d *directory) close() {
	for i := len(d.held) - 1; i >= 0; i-- {
		d.held[i].Close()
	}
}
func (d *directory) leaf() *os.File { return d.held[len(d.held)-1] }
func (d *directory) valid() bool {
	for i, f := range d.held {
		info, e := f.Stat()
		now, ne := os.Lstat(f.Name())
		if e != nil || ne != nil || !info.IsDir() || now.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, now) {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
			return false
		}
		stickyRoot := st.Uid == 0 && info.Mode()&os.ModeSticky != 0
		if info.Mode().Perm()&0022 != 0 && !stickyRoot {
			return false
		}
		if i == len(d.held)-1 && (st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0) {
			return false
		}
	}
	return true
}
func protectedRead(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrPolicy
	}
	d, e := pinDirectory(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer d.close()
	fd, e := unix.Openat(int(d.leaf().Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, ErrPolicy
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	before, e := f.Stat()
	if e != nil {
		return nil, ErrPolicy
	}
	valid := func() bool {
		info, e := f.Stat()
		now, ne := os.Lstat(path)
		if e != nil || ne != nil || !info.Mode().IsRegular() || now.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, info) || !os.SameFile(info, now) || info.Size() != before.Size() || !info.ModTime().Equal(before.ModTime()) || info.Size() < 1 || info.Size() > MaxPolicyBytes {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		return ok && st.Uid == uint32(os.Geteuid()) && st.Nlink == 1 && info.Mode().Perm()&0077 == 0 && d.valid()
	}
	read := func() ([]byte, error) {
		if !valid() {
			return nil, ErrPolicy
		}
		if _, e := f.Seek(0, io.SeekStart); e != nil {
			return nil, ErrPolicy
		}
		b, e := io.ReadAll(io.LimitReader(f, MaxPolicyBytes+1))
		if e != nil || int64(len(b)) != before.Size() || !valid() {
			return nil, ErrPolicy
		}
		return b, nil
	}
	b, e := read()
	if e != nil {
		return nil, e
	}
	again, e := read()
	if e != nil || !bytes.Equal(b, again) {
		return nil, ErrPolicy
	}
	return b, nil
}
func LoadPolicy(path string) (*Policy, error) {
	b, e := protectedRead(path)
	if e != nil {
		return nil, e
	}
	p, e := DecodePolicy(b)
	if e != nil {
		return nil, e
	}
	return &p, nil
}
