//go:build linux

package proxygate

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func peerUID(c net.Conn) (uint32, error) {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return 0, ErrProtocol
	}
	raw, e := u.SyscallConn()
	if e != nil {
		return 0, e
	}
	var uid uint32
	var inner error
	e = raw.Control(func(fd uintptr) {
		cred, ce := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if ce != nil {
			inner = ce
			return
		}
		uid = cred.Uid
	})
	if e != nil {
		return 0, e
	}
	return uid, inner
}
func socketInfo(path string) (os.FileInfo, error) {
	info, e := os.Lstat(path)
	if e != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 {
		return nil, ErrPolicy
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		return nil, ErrPolicy
	}
	return info, nil
}
func socketPath(path string) (*directory, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 100 {
		return nil, "", ErrPolicy
	}
	d, e := pinDirectory(filepath.Dir(path))
	if e != nil {
		return nil, "", e
	}
	return d, fmt.Sprintf("/proc/self/fd/%d/%s", d.leaf().Fd(), filepath.Base(path)), nil
}

// ListenControl owns a single sequential control connection. No per-message or
// accept queue goroutines are spawned. The namespace and peer UID are checked
// before commands; absence, EOF, malformed frames and lease expiry close scope.
func ListenControl(ctx context.Context, path string, g *Gate) (*ControlServer, error) {
	if ctx == nil || ctx.Err() != nil || g == nil || !g.initialized || g.policy.ControllerUID != uint32(os.Geteuid()) {
		return nil, ErrPolicy
	}
	g.Close()
	d, pinned, e := socketPath(path)
	if e != nil {
		return nil, e
	}
	if _, e := os.Lstat(path); !os.IsNotExist(e) {
		d.close()
		return nil, ErrPolicy
	}
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: pinned, Net: "unix"})
	if e != nil {
		d.close()
		return nil, ErrPolicy
	}
	l.SetUnlinkOnClose(false)
	if e := unix.Fchmodat(int(d.leaf().Fd()), filepath.Base(path), 0600, 0); e != nil {
		l.Close()
		unix.Unlinkat(int(d.leaf().Fd()), filepath.Base(path), 0)
		d.close()
		return nil, ErrPolicy
	}
	info, e := socketInfo(path)
	if e != nil || !d.valid() {
		l.Close()
		unix.Unlinkat(int(d.leaf().Fd()), filepath.Base(path), 0)
		d.close()
		return nil, ErrPolicy
	}
	live, cancel := context.WithCancel(ctx)
	s := &ControlServer{listener: l, gate: g, cancel: cancel, done: make(chan struct{})}
	s.namespaceValid = func() bool { now, e := socketInfo(path); return e == nil && os.SameFile(info, now) && d.valid() }
	s.validPeer = func(c net.Conn) bool { uid, e := peerUID(c); return e == nil && uid == g.policy.ControllerUID }
	s.cleanup = func() {
		if now, e := os.Lstat(pinned); e == nil && os.SameFile(info, now) {
			unix.Unlinkat(int(d.leaf().Fd()), filepath.Base(path), 0)
		}
		d.close()
	}
	go s.serve(live)
	go func() {
		select {
		case <-live.Done():
			s.stop()
		case <-s.done:
		}
	}()
	return s, nil
}
func DialControl(ctx context.Context, path string, p Policy) (*Controller, error) {
	if p.Validate() != nil || p.ControllerUID != uint32(os.Geteuid()) {
		return nil, ErrPolicy
	}
	d, pinned, e := socketPath(path)
	if e != nil {
		return nil, e
	}
	defer d.close()
	before, e := socketInfo(path)
	if e != nil {
		return nil, e
	}
	deadline, e := commandDeadline(ctx)
	if e != nil {
		return nil, e
	}
	dialer := net.Dialer{Deadline: deadline}
	conn, e := dialer.DialContext(ctx, "unix", pinned)
	if e != nil {
		return nil, ErrClosed
	}
	now, e := socketInfo(path)
	uid, pe := peerUID(conn)
	if e != nil || pe != nil || uid != p.ControllerUID || !os.SameFile(before, now) || !d.valid() {
		conn.Close()
		return nil, ErrPolicy
	}
	return connectController(ctx, conn, p)
}
