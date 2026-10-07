//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zongheng-vpn/shared/proxygate"
)

type admissionDeadlineWindowConn struct {
	net.Conn
	clear            bool
	entered          chan struct{}
	releaseDeadline  chan struct{}
	closeEntered     chan struct{}
	deadlineFinished chan struct{}
	repaired         chan struct{}
	writeFinished    chan struct{}
	releaseClose     chan struct{}
	deadlineOnce     sync.Once
	closeOnce        sync.Once
	deadlineReleased sync.Once
	closeReleased    sync.Once
	repairOnce       sync.Once
	writeOnce        sync.Once
	heldFinished     atomic.Bool
	writeCalls       atomic.Int64
	mu               sync.Mutex
	lastDeadline     time.Time
}

func (c *admissionDeadlineWindowConn) SetDeadline(deadline time.Time) error {
	held := false
	if (c.clear && deadline.IsZero()) || (!c.clear && deadline.After(time.Now())) {
		c.deadlineOnce.Do(func() { held = true; close(c.entered); <-c.releaseDeadline })
	}
	err := c.Conn.SetDeadline(deadline)
	c.mu.Lock()
	c.lastDeadline = deadline
	c.mu.Unlock()
	if held {
		c.heldFinished.Store(true)
		close(c.deadlineFinished)
	} else if c.heldFinished.Load() && !deadline.IsZero() && !deadline.After(time.Now()) {
		c.repairOnce.Do(func() { close(c.repaired) })
	}
	return err
}
func (c *admissionDeadlineWindowConn) Write(payload []byte) (int, error) {
	c.writeCalls.Add(1)
	n, err := c.Conn.Write(payload)
	c.writeOnce.Do(func() { close(c.writeFinished) })
	return n, err
}
func (c *admissionDeadlineWindowConn) Close() error {
	c.closeOnce.Do(func() { close(c.closeEntered) })
	<-c.releaseClose
	return c.Conn.Close()
}
func (c *admissionDeadlineWindowConn) release() {
	c.deadlineReleased.Do(func() { close(c.releaseDeadline) })
	c.closeReleased.Do(func() { close(c.releaseClose) })
}
func (c *admissionDeadlineWindowConn) deadline() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastDeadline
}

type admissionDeadlineWindowSession struct {
	tunnelSession
	window *admissionDeadlineWindowConn
}

func (s *admissionDeadlineWindowSession) OpenStream(ctx context.Context) (net.Conn, error) {
	conn, err := s.tunnelSession.OpenStream(ctx)
	if conn != nil {
		s.window.Conn = conn
		return s.window, err
	}
	return conn, err
}

// Hold only a deadline syscall and the owned stream's physical Close, while
// using the actual product handler, actual yamux, and owned TCP echo targets.
// Without the fence, the future case writes CONNECT after cancellation and
// both cases restore the deadline after the gate's abort deadline was applied.
func TestProxyGateLinuxActualYamuxDeadlineWindowCannotDispatchOrReopen(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(map[bool]string{false: "future_command", true: "clear_deadline"}[clear], func(t *testing.T) {
			manager, gate, policy, proxy := newAdmissionProxy(t)
			target := newAdmissionEchoTarget(t)
			protected := admissionConnect(t, proxy, "127.0.0.3", target.listener.Addr().String(), false)
			defer protected.Close()
			admissionEcho(t, protected, "retained-before-deadline-window")
			controller, _ := startAdmissionControl(t, gate, policy)
			window := &admissionDeadlineWindowConn{
				clear: clear, entered: make(chan struct{}), releaseDeadline: make(chan struct{}),
				closeEntered: make(chan struct{}), releaseClose: make(chan struct{}),
				deadlineFinished: make(chan struct{}), repaired: make(chan struct{}), writeFinished: make(chan struct{}),
			}
			defer func() {
				window.release()
				if err := gate.AwaitClosed(context.Background()); err != nil {
					t.Errorf("deadline counterexample left an owned resource: %v", err)
				}
			}()
			manager.mu.Lock()
			session := &admissionDeadlineWindowSession{tunnelSession: manager.sessions[0], window: window}
			manager.sessions = nil
			manager.sessionStats = nil
			manager.mu.Unlock()
			manager.set(session)
			dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}, Timeout: time.Second}
			managed, err := dialer.Dial("tcp", proxy)
			if err != nil {
				t.Fatal(err)
			}
			defer managed.Close()
			if _, err := fmt.Fprintf(managed, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target.listener.Addr(), target.listener.Addr()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-window.entered:
			case <-time.After(time.Second):
				t.Fatal("actual command did not reach its synchronized deadline window")
			}
			beforeWrites := window.writeCalls.Load()
			closed := make(chan error, 1)
			go func() { closed <- controller.Closed(context.Background()) }()
			waitAdmission(t, "scope did not invalidate during the held deadline syscall", func() bool {
				probe, err := gate.AcquireSource("127.0.0.2:1")
				if probe != nil {
					probe.Release()
				}
				return errors.Is(err, proxygate.ErrClosed)
			})
			earlyClose := false
			select {
			case <-window.closeEntered:
				earlyClose = true
				t.Error("raw Close started before the admitted deadline operation finished")
			case <-time.After(50 * time.Millisecond):
			}
			window.deadlineReleased.Do(func() { close(window.releaseDeadline) })
			if earlyClose {
				<-window.deadlineFinished
				if !clear {
					select {
					case <-window.writeFinished:
					case <-time.After(time.Second):
						t.Fatal("old raw command did not finish its synchronized dispatch")
					}
				}
			} else {
				select {
				case <-window.repaired:
				case <-time.After(time.Second):
					t.Fatal("closing update was not repaired after the admitted operation completed")
				}
			}
			select {
			case <-window.closeEntered:
			case <-time.After(time.Second):
				t.Fatal("owned stream did not reach the quarantined physical Close")
			}
			if d := window.deadline(); d.IsZero() || d.After(time.Now()) {
				t.Errorf("closing syscall reopened its deadline: clear=%t zero=%t future=%t", clear, d.IsZero(), d.After(time.Now()))
			}
			if writes := window.writeCalls.Load(); writes != beforeWrites {
				t.Errorf("actual managed command dispatched after scope invalidation: before=%d after=%d", beforeWrites, writes)
			}
			if !clear && target.accepted.Load() != 1 {
				t.Errorf("closing command created an owned upstream target: accepted=%d", target.accepted.Load())
			}
			select {
			case err := <-closed:
				t.Fatalf("CLOSED finished before the held actual Close returned: %v", err)
			case <-time.After(10 * time.Millisecond):
			}
			window.closeReleased.Do(func() { close(window.releaseClose) })
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal("CLOSED did not follow actual transport/permit completion", err)
				}
			case <-time.After(time.Second):
				t.Fatal("CLOSED did not complete after releasing the owned window")
			}
			if err := gate.AwaitClosed(context.Background()); err != nil {
				t.Fatal(err)
			}
			_ = managed.SetReadDeadline(time.Now().Add(time.Second))
			body, err := io.ReadAll(managed)
			if err != nil || strings.Contains(string(body), "200 Connection Established") {
				t.Fatalf("canceled setup returned a successful CONNECT or remained readable: err=%v bytes=%d", err, len(body))
			}
			if manager.sessionCount() != 1 {
				t.Fatal("a scoped cancellation removed the shared egress session")
			}
			admissionEcho(t, protected, "retained-after-owned-deadline-window")
		})
	}
}
