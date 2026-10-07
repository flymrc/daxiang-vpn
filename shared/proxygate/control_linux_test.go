//go:build linux

package proxygate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func privateDir(t *testing.T) string {
	t.Helper()
	d, e := os.MkdirTemp("/tmp", "zhvpn-proxygate-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}
func TestLinuxControlBlockingCloseNoFalseACKOrOwnerAndFiniteShutdown(t *testing.T) {
	g, s, path, p := linuxFixture(t)
	c := mustController(t, path, p)
	if c.Grant(context.Background(), MaxLease) != nil {
		t.Fatal("grant")
	}
	a, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal(e)
	}
	release := make(chan struct{})
	var started atomic.Int32
	l, r := net.Pipe()
	defer r.Close()
	blocked := &heldCloseConn{Conn: l, release: release, started: &started}
	if a.Track(blocked) != nil {
		t.Fatal("track")
	}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	before := time.Now()
	if c.Closed(context.Background()) == nil {
		t.Fatal("blockedClose falsely acknowledged")
	}
	if elapsed := time.Since(before); elapsed > 1500*time.Millisecond {
		t.Fatal("command exceeded bound", elapsed)
	}
	if a.Context().Err() == nil {
		t.Fatal("I/O context retained")
	}
	for range 3 {
		if next, e := DialControl(context.Background(), path, p); e == nil {
			next.Close()
			t.Fatal("new owner admitted during quarantine")
		}
	}
	g.mu.Lock()
	task := g.cleanup
	g.mu.Unlock()
	if task == nil || started.Load() != 1 {
		t.Fatal("quarantine resources grew")
	}
	before = time.Now()
	if s.Close() != nil || time.Since(before) > 1500*time.Millisecond {
		t.Fatal("shutdown blocked on transport")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if !errors.Is(g.AwaitClosed(ctx), ErrCleanupUnknown) {
		t.Fatal("shutdown claimed resources reaped")
	}
	releaseOnce.Do(func() { close(release) })
	if e := g.AwaitClosed(context.Background()); e != nil {
		t.Fatal("completed cleanup remained quarantined", e)
	}
	resumed, e := ListenControl(context.Background(), path, g)
	if e != nil {
		t.Fatal("fresh receiver after cleanup", e)
	}
	defer resumed.Close()
	next := mustController(t, path, p)
	if e := next.GrantUntil(context.Background(), time.Now().Add(time.Second)); e != nil {
		t.Fatal("fresh owner could not recover", e)
	}
	ad, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal(e)
	}
	ad.Release()
	next.Closed(context.Background())
}
func linuxFixture(t *testing.T) (*Gate, *ControlServer, string, Policy) {
	t.Helper()
	p := testPolicy()
	p.ControllerUID = uint32(os.Geteuid())
	g, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(privateDir(t), "control.sock")
	s, e := ListenControl(context.Background(), path, g)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return g, s, path, p
}
func mustController(t *testing.T, path string, p Policy) *Controller {
	t.Helper()
	c, e := DialControl(context.Background(), path, p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func waitClosed(t *testing.T, g *Gate) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a, e := g.AcquireSource("127.0.0.2:1")
		if errors.Is(e, ErrClosed) {
			return
		}
		if e == nil {
			a.Release()
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("gate remained open")
}
func TestLinuxControlRealPeercredClosedACKGrantAndEOF(t *testing.T) {
	g, _, path, p := linuxFixture(t)
	c := mustController(t, path, p)
	if _, e := g.AcquireSource("127.0.0.2:1"); !errors.Is(e, ErrClosed) {
		t.Fatal("closed ack was open")
	}
	uid, e := peerUID(c.conn)
	if e != nil || uid != uint32(os.Geteuid()) {
		t.Fatal("peer credential")
	}
	if e := c.Grant(context.Background(), time.Second); e != nil {
		t.Fatal(e)
	}
	a, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal(e)
	}
	left, right := net.Pipe()
	defer right.Close()
	a.Track(left)
	c.Close()
	waitClosed(t, g)
	select {
	case <-a.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("EOF did not cancel")
	}
	right.SetReadDeadline(time.Now().Add(time.Second))
	if _, e := right.Read(make([]byte, 1)); e == nil {
		t.Fatal("EOF did not close registered flow")
	}
	for _, source := range []string{"127.0.0.3:1", "127.0.0.7:1"} {
		ad, e := g.AcquireSource(source)
		if e != nil {
			t.Fatal(e)
		}
		ad.Release()
	}
}
func TestLinuxControlAbsoluteLeaseExpiryAndClosedCommand(t *testing.T) {
	g, _, path, p := linuxFixture(t)
	c := mustController(t, path, p)
	if c.Grant(context.Background(), 200*time.Millisecond) != nil {
		t.Fatal("grant")
	}
	a, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal(e)
	}
	l, r := net.Pipe()
	defer r.Close()
	a.Track(l)
	select {
	case <-a.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("lease did not cancel")
	}
	waitClosed(t, g)
	if c.Grant(context.Background(), time.Second) == nil {
		t.Fatal("expired session revived")
	}
	c.Close()
	c = mustController(t, path, p)
	if c.Grant(context.Background(), time.Second) != nil {
		t.Fatal("new owner")
	}
	if c.Closed(context.Background()) != nil {
		t.Fatal("closed command")
	}
	waitClosed(t, g)
	if c.Grant(context.Background(), time.Second) != nil {
		t.Fatal("same closed owner grant")
	}
	c.Closed(context.Background())
}
func TestLinuxControlReplayMalformedBindingAndCapacityFailClosed(t *testing.T) {
	for _, kind := range []string{"sequence", "owner", "nonce", "binding", "oversize", "expired-deadline", "missing-closed"} {
		t.Run(kind, func(t *testing.T) {
			g, _, path, _ := linuxFixture(t)
			conn, e := net.Dial("unix", path)
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			r := bufio.NewReaderSize(conn, MaxMessageBytes+1)
			hello, e := readMessage(conn, r, time.Now().Add(time.Second))
			if e != nil {
				t.Fatal(e)
			}
			m := hello
			m.Sequence = 1
			m.Action = "closed"
			if kind != "missing-closed" {
				writeMessage(conn, m, time.Now().Add(time.Second))
				if _, e := readMessage(conn, r, time.Now().Add(time.Second)); e != nil {
					t.Fatal(e)
				}
				m.Sequence = 2
				m.Action = "grant"
				m.UntilUnixNano = time.Now().Add(time.Second).UnixNano()
				writeMessage(conn, m, time.Now().Add(time.Second))
				if _, e := readMessage(conn, r, time.Now().Add(time.Second)); e != nil {
					t.Fatal(e)
				}
			}
			m.Sequence++
			switch kind {
			case "sequence":
				m.Sequence--
			case "owner":
				m.Session = "dddddddddddddddddddddddddddddddd"
			case "nonce":
				m.Nonce = "dddddddddddddddddddddddddddddddd"
			case "binding":
				m.Binding = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
			case "expired-deadline":
				m.UntilUnixNano = time.Now().Add(-time.Second).UnixNano()
			case "missing-closed":
				m.Sequence = 1
				m.Action = "grant"
				m.UntilUnixNano = time.Now().Add(time.Second).UnixNano()
			}
			if kind == "oversize" {
				conn.Write(make([]byte, MaxMessageBytes+2))
			} else {
				writeMessage(conn, m, time.Now().Add(time.Second))
			}
			waitClosed(t, g)
		})
	}
}
func TestLinuxProtectedPolicyAndSocketNamespaceRejectSubstitution(t *testing.T) {
	d := privateDir(t)
	path := filepath.Join(d, "policy.json")
	p := testPolicy()
	p.ControllerUID = uint32(os.Geteuid())
	raw, _ := json.Marshal(p)
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadPolicy(path); e != nil {
		t.Fatal(e)
	}
	os.Chmod(path, 0644)
	if _, e := LoadPolicy(path); e == nil {
		t.Fatal("wide policy")
	}
	os.Chmod(path, 0600)
	alias := filepath.Join(d, "alias")
	os.Link(path, alias)
	if _, e := LoadPolicy(path); e == nil {
		t.Fatal("hardlink policy")
	}
	os.Remove(alias)
	link := filepath.Join(d, "link")
	os.Symlink(path, link)
	if _, e := LoadPolicy(link); e == nil {
		t.Fatal("symlink policy")
	}
	fifo := filepath.Join(d, "fifo")
	unix.Mkfifo(fifo, 0600)
	if _, e := LoadPolicy(fifo); e == nil {
		t.Fatal("FIFO accepted")
	}
	g, _ := New(p)
	os.Chmod(d, 0755)
	if _, e := ListenControl(context.Background(), filepath.Join(d, "socket"), g); e == nil {
		t.Fatal("wide namespace")
	}
	os.Chmod(d, 0700)
	occupied := filepath.Join(d, "occupied")
	os.WriteFile(occupied, []byte("owned"), 0600)
	if _, e := ListenControl(context.Background(), occupied, g); e == nil {
		t.Fatal("occupied overwritten")
	}
	b, _ := os.ReadFile(occupied)
	if string(b) != "owned" {
		t.Fatal("occupied changed")
	}
	symlink := filepath.Join(d, "symlink-dir")
	os.Symlink(d, symlink)
	if _, e := ListenControl(context.Background(), filepath.Join(symlink, "socket"), g); e == nil {
		t.Fatal("symlink namespace")
	}
}
