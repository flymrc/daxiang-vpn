package proxygate

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type dropACKConn struct {
	net.Conn
	action  string
	dropped chan struct{}
	once    sync.Once
}

func (c *dropACKConn) Write(b []byte) (int, error) {
	if len(b) > 1 && b[len(b)-1] == '\n' {
		m, e := decodeMessage(b[:len(b)-1])
		if e == nil && m.Action == "ack" && ((c.action == "closed" && m.UntilUnixNano == 0) || (c.action == "grant" && m.UntilUnixNano > 0)) {
			c.once.Do(func() { close(c.dropped) })
			return len(b), nil
		}
	}
	return c.Conn.Write(b)
}

type retainedEcho struct {
	left, right net.Conn
	done        chan struct{}
}

func ownedRetainedEcho(t *testing.T, g *Gate, source string) *retainedEcho {
	t.Helper()
	a, e := g.AcquireSource(source)
	if e != nil {
		t.Fatal(e)
	}
	left, right := net.Pipe()
	if a.Track(left) != nil {
		t.Fatal("retained track")
	}
	echo := &retainedEcho{left: left, right: right, done: make(chan struct{})}
	go func() { defer close(echo.done); io.Copy(left, left) }()
	t.Cleanup(func() {
		a.Release()
		left.Close()
		right.Close()
		select {
		case <-echo.done:
		case <-time.After(time.Second):
			t.Error("retained echo goroutine remained")
		}
	})
	return echo
}
func (e *retainedEcho) check(t *testing.T) {
	t.Helper()
	marker := []byte("retained-real-stream")
	e.right.SetDeadline(time.Now().Add(time.Second))
	if _, err := e.right.Write(marker); err != nil {
		t.Fatal("retained write", err)
	}
	got := make([]byte, len(marker))
	if _, err := io.ReadFull(e.right, got); err != nil || string(got) != string(marker) {
		t.Fatal("retained flow affected", err)
	}
}

// These exercise the actual receiver session and canonical byte codec. The
// wrapper reports a successful ACK write while discarding the bytes, modeling
// lost acknowledgements rather than a fake Service factory failure.
func TestActualReceiverDroppedClosedAndGrantACKFailAndReap(t *testing.T) {
	for _, kind := range []string{"closed", "grant"} {
		t.Run(kind, func(t *testing.T) {
			g, _ := New(testPolicy())
			protected := ownedRetainedEcho(t, g, "127.0.0.3:1")
			unknown := ownedRetainedEcho(t, g, "127.0.0.7:1")
			client, rawServer := net.Pipe()
			defer client.Close()
			defer rawServer.Close()
			drop := &dropACKConn{Conn: rawServer, action: kind, dropped: make(chan struct{})}
			server := &ControlServer{gate: g, namespaceValid: func() bool { return true }}
			receiverDone := make(chan struct{})
			go func() { defer close(receiverDone); server.session(context.Background(), drop); drop.Close() }()
			var admission *Admission
			var managedTarget net.Conn
			if kind == "closed" {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				if control, e := connectController(ctx, client, testPolicy()); e == nil {
					control.Close()
					t.Fatal("lost CLOSED ACK accepted")
				}
				if _, e := g.AcquireSource("127.0.0.2:1"); !errors.Is(e, ErrClosed) {
					t.Fatal("lost closed handshake admitted source", e)
				}
			} else {
				control, e := connectController(context.Background(), client, testPolicy())
				if e != nil {
					t.Fatal(e)
				}
				defer control.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				result := make(chan error, 1)
				go func() { result <- control.GrantUntil(ctx, time.Now().Add(time.Second)) }()
				select {
				case <-drop.dropped:
				case <-time.After(time.Second):
					t.Fatal("receiver did not write grant ACK")
				}
				admission, e = g.AcquireSource("127.0.0.2:1")
				if e != nil {
					t.Fatal("receiver grant was not actually applied", e)
				}
				managedClient, target := net.Pipe()
				managedTarget = target
				defer target.Close()
				if admission.Track(managedClient) != nil {
					t.Fatal("managed stream not registered")
				}
				if e := <-result; e == nil {
					t.Fatal("lost GRANT ACK accepted")
				}
			}
			select {
			case <-drop.dropped:
			case <-time.After(time.Second):
				t.Fatal("ACK was not dropped")
			}
			select {
			case <-receiverDone:
			case <-time.After(time.Second):
				t.Fatal("receiver did not observe actual socket EOF")
			}
			if e := g.AwaitClosed(context.Background()); e != nil {
				t.Fatal("EOF did not finish real registered cleanup", e)
			}
			if admission != nil {
				if admission.Context().Err() == nil {
					t.Fatal("managed context survived lost grant ACK")
				}
				managedTarget.SetReadDeadline(time.Now().Add(time.Second))
				if _, e := managedTarget.Read(make([]byte, 1)); e == nil {
					t.Fatal("registered managed stream survived physical cleanup")
				}
			}
			if _, e := g.AcquireSource("127.0.0.2:1"); !errors.Is(e, ErrClosed) {
				t.Fatal("lost ACK session remained active", e)
			}
			protected.check(t)
			unknown.check(t)
		})
	}
}
