//go:build !windows

package proxy

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"zongheng-vpn/shared/paths"
)

type engineLogDirectory struct {
	file   *os.File
	name   string
	stat   unix.Stat_t
	strict bool
}
type engineLogNamespace struct {
	held         []engineLogDirectory
	createdFiles map[*os.File]bool
}

func logNamespaceError() error { return errors.New(string(EngineLogCodeNamespace)) }
func openEngineLogNamespace(ctx paths.Context) (_ *engineLogNamespace, fresh bool, result error) {
	if ctx.Root == "" || !filepath.IsAbs(ctx.Root) || filepath.Clean(ctx.Root) != ctx.Root || filepath.Clean(ctx.LogDir) != filepath.Join(ctx.Root, "logs") {
		return nil, false, logNamespaceError()
	}
	n := &engineLogNamespace{createdFiles: make(map[*os.File]bool)}
	defer func() {
		if result != nil {
			n.close()
		}
	}()
	fd, e := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, false, e
	}
	f := os.NewFile(uintptr(fd), string(filepath.Separator))
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		f.Close()
		return nil, false, e
	}
	n.held = append(n.held, engineLogDirectory{file: f, stat: st})
	parts := strings.Split(strings.TrimPrefix(ctx.Root, string(filepath.Separator)), string(filepath.Separator))
	for i, name := range parts {
		if name == "" {
			return nil, false, logNamespaceError()
		}
		parent := n.held[len(n.held)-1].file
		fd, e = unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			return nil, false, e
		}
		f = os.NewFile(uintptr(fd), name)
		if e = unix.Fstat(fd, &st); e != nil {
			f.Close()
			return nil, false, e
		}
		// Root-owned sticky system temp is safe as an ancestor; the home itself
		// must be owned by this process and deny other accounts mutation.
		if (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || (st.Mode&0022 != 0 && !(st.Uid == 0 && st.Mode&unix.S_ISVTX != 0)) || (i == len(parts)-1 && (st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0)) {
			f.Close()
			return nil, false, logNamespaceError()
		}
		n.held = append(n.held, engineLogDirectory{file: f, name: name, stat: st})
	}
	for _, name := range []string{"logs", "engine-events-v1"} {
		parent := n.held[len(n.held)-1].file
		created := unix.Mkdirat(int(parent.Fd()), name, 0700) == nil
		fd, e = unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			return nil, false, e
		}
		f = os.NewFile(uintptr(fd), name)
		if e = unix.Fstat(fd, &st); e != nil {
			f.Close()
			return nil, false, e
		}
		strict := name == "engine-events-v1"
		if st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 || (strict && st.Mode&0077 != 0) {
			f.Close()
			return nil, false, logNamespaceError()
		}
		n.held = append(n.held, engineLogDirectory{file: f, name: name, stat: st, strict: strict})
		if strict {
			fresh = created
		}
	}
	if e = n.valid(); e != nil {
		return nil, false, e
	}
	return n, fresh, nil
}
func (n *engineLogNamespace) leaf() *os.File { return n.held[len(n.held)-1].file }
func (n *engineLogNamespace) valid() error {
	for i, d := range n.held {
		var held, actual unix.Stat_t
		if unix.Fstat(int(d.file.Fd()), &held) != nil || held.Dev != d.stat.Dev || held.Ino != d.stat.Ino || held.Mode&unix.S_IFMT != unix.S_IFDIR {
			return logNamespaceError()
		}
		if i > 0 {
			if unix.Fstatat(int(n.held[i-1].file.Fd()), d.name, &actual, unix.AT_SYMLINK_NOFOLLOW) != nil || actual.Dev != held.Dev || actual.Ino != held.Ino || actual.Mode&unix.S_IFMT != unix.S_IFDIR {
				return logNamespaceError()
			}
		}
		if (held.Uid != 0 && held.Uid != uint32(os.Geteuid())) || (held.Mode&0022 != 0 && !(held.Uid == 0 && held.Mode&unix.S_ISVTX != 0)) {
			return logNamespaceError()
		}
		if i >= len(n.held)-3 && (held.Uid != uint32(os.Geteuid()) || held.Mode&0022 != 0) {
			return logNamespaceError()
		}
		if d.strict && held.Mode&0077 != 0 {
			return logNamespaceError()
		}
	}
	return nil
}
func (n *engineLogNamespace) open(name string, create bool) (*os.File, error) {
	if n.valid() != nil {
		return nil, logNamespaceError()
	}
	created := false
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	var fd int
	var e error
	if create {
		fd, e = unix.Openat(int(n.leaf().Fd()), name, flags|unix.O_CREAT|unix.O_EXCL, 0600)
		if e == nil {
			created = true
		}
	}
	if !create || errors.Is(e, unix.EEXIST) {
		fd, e = unix.Openat(int(n.leaf().Fd()), name, flags, 0)
	}
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	if e = n.verify(f, name); e != nil {
		f.Close()
		return nil, e
	}
	n.createdFiles[f] = created
	return f, nil
}
func (n *engineLogNamespace) created(f *os.File) bool { return n.createdFiles[f] }
func (n *engineLogNamespace) verify(f *os.File, name string) error {
	if n.valid() != nil {
		return logNamespaceError()
	}
	var held, actual unix.Stat_t
	if unix.Fstat(int(f.Fd()), &held) != nil || held.Mode&unix.S_IFMT != unix.S_IFREG || held.Uid != uint32(os.Geteuid()) || held.Nlink != 1 || held.Mode&0077 != 0 || unix.Fstatat(int(n.leaf().Fd()), name, &actual, unix.AT_SYMLINK_NOFOLLOW) != nil || actual.Dev != held.Dev || actual.Ino != held.Ino || actual.Mode&unix.S_IFMT != unix.S_IFREG {
		return logNamespaceError()
	}
	return nil
}
func (n *engineLogNamespace) checkEntries() error {
	if n.valid() != nil {
		return logNamespaceError()
	}
	if _, e := n.leaf().Seek(0, 0); e != nil {
		return e
	}
	names, e := n.leaf().Readdirnames(EngineLogSlots + 2)
	if e != nil && !errors.Is(e, io.EOF) {
		return e
	}
	if len(names) > EngineLogSlots+1 {
		return logNamespaceError()
	}
	for _, name := range names {
		valid := name == "owner.v1"
		for i := 0; i < EngineLogSlots; i++ {
			valid = valid || name == engineLogSlotName(i)
		}
		if !valid {
			return logNamespaceError()
		}
	}
	return nil
}
func (n *engineLogNamespace) close() {
	for i := len(n.held) - 1; i >= 0; i-- {
		n.held[i].file.Close()
	}
	n.held = nil
}
