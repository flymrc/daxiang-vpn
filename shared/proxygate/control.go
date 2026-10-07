package proxygate

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"sync"
	"time"
)

// Controller owns one receiver-issued session. Calls never queue: concurrent
// commands fail closed. Close drops the socket, requiring the receiver to close
// registered streams on EOF; Closed waits for an explicit closed acknowledgement.
type Controller struct {
	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
	policy Policy
	base   message
	closed bool
}

func (c *Controller) Policy() Policy { return c.policy.Clone() }
func commandDeadline(ctx context.Context) (time.Time, error) {
	if ctx == nil || ctx.Err() != nil {
		return time.Time{}, ErrClosed
	}
	d := time.Now().Add(CommandTimeout)
	if cd, ok := ctx.Deadline(); ok && cd.Before(d) {
		d = cd
	}
	return d, nil
}
func (c *Controller) command(ctx context.Context, action string, until int64) error {
	if c == nil || c.conn == nil {
		return ErrClosed
	}
	if !c.mu.TryLock() {
		c.conn.Close()
		return ErrClosed
	}
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	deadline, e := commandDeadline(ctx)
	if e != nil {
		c.closed = true
		c.conn.Close()
		return e
	}
	fail := func() error { c.closed = true; c.conn.Close(); return ErrProtocol }
	if c.base.Sequence >= 9007199254740991 {
		return fail()
	}
	m := c.base
	m.Sequence++
	m.Action = action
	m.UntilUnixNano = until
	if writeMessage(c.conn, m, deadline) != nil {
		return fail()
	}
	ack, e := readMessage(c.conn, c.reader, deadline)
	if e != nil || ack.Version != m.Version || ack.Binding != m.Binding || ack.Session != m.Session || ack.Nonce != m.Nonce || ack.Sequence != m.Sequence || ack.Action != "ack" || ack.UntilUnixNano != until || ctx.Err() != nil || (until != 0 && time.Now().UnixNano() >= until) {
		return fail()
	}
	c.base = m
	return nil
}
func (c *Controller) Grant(ctx context.Context, lease time.Duration) error {
	if lease < MinLease || lease > MaxLease || lease%time.Millisecond != 0 {
		c.Close()
		return ErrProtocol
	}
	return c.GrantUntil(ctx, time.Now().Add(lease))
}

// GrantUntil preserves the authority's chosen absolute validity boundary.
func (c *Controller) GrantUntil(ctx context.Context, until time.Time) error {
	remaining := time.Until(until)
	if remaining < MinLease || remaining > MaxLease {
		c.Close()
		return ErrProtocol
	}
	return c.command(ctx, "grant", until.UnixNano())
}
func (c *Controller) Closed(ctx context.Context) error { return c.command(ctx, "closed", 0) }
func (c *Controller) Close() error {
	if c == nil || c.conn == nil {
		return ErrClosed
	}
	return c.conn.Close()
}

type ControlServer struct {
	mu             sync.Mutex
	listener       net.Listener
	conn           net.Conn
	gate           *Gate
	cancel         context.CancelFunc
	done           chan struct{}
	cleanup        func()
	validPeer      func(net.Conn) bool
	namespaceValid func() bool
}

func (s *ControlServer) stop() {
	s.cancel()
	s.listener.Close()
	s.mu.Lock()
	if s.conn != nil {
		s.conn.Close()
	}
	s.mu.Unlock()
	s.gate.Close()
}
func (s *ControlServer) Close() error {
	s.stop()
	select {
	case <-s.done:
		return nil
	case <-time.After(CommandTimeout):
		return ErrCleanupUnknown
	}
}
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func (s *ControlServer) serve(ctx context.Context) {
	defer close(s.done)
	defer s.cleanup()
	defer s.gate.Close()
	for {
		conn, e := s.listener.Accept()
		if e != nil {
			return
		}
		if ctx.Err() != nil || !s.namespaceValid() || !s.validPeer(conn) {
			conn.Close()
			if ctx.Err() != nil {
				return
			}
			continue
		}
		s.mu.Lock()
		s.conn = conn
		s.mu.Unlock()
		s.session(ctx, conn)
		conn.Close()
		s.mu.Lock()
		s.conn = nil
		s.mu.Unlock()
	}
}
func (s *ControlServer) session(ctx context.Context, conn net.Conn) {
	session, e := randomID()
	if e != nil {
		return
	}
	nonce, e := randomID()
	if e != nil {
		return
	}
	if s.gate.own(session) != nil {
		return
	}
	defer s.gate.closeFor(session)
	base := message{Version: Version, Binding: s.gate.policy.SHA256(), Session: session, Nonce: nonce, Action: "hello"}
	r := bufio.NewReaderSize(conn, MaxMessageBytes+1)
	if writeMessage(conn, base, time.Now().Add(CommandTimeout)) != nil {
		return
	}
	expires := time.Time{}
	closedACK := false
	for {
		deadline := time.Now().Add(ClosedIdleTimeout)
		if !expires.IsZero() {
			deadline = expires
		}
		m, e := readMessage(conn, r, deadline)
		if e != nil || ctx.Err() != nil || (!expires.IsZero() && !time.Now().Before(expires)) || !s.namespaceValid() || m.Binding != base.Binding || m.Session != base.Session || m.Nonce != base.Nonce || m.Sequence != base.Sequence+1 || m.Sequence > 9007199254740991 {
			return
		}
		switch m.Action {
		case "closed":
			if m.UntilUnixNano != 0 {
				return
			}
			s.gate.closeFor(session)
			if s.gate.AwaitClosed(ctx) != nil || s.gate.own(session) != nil {
				return
			}
			expires = time.Time{}
			closedACK = true
		case "grant":
			if !closedACK || m.UntilUnixNano <= 0 {
				return
			}
			until := time.Unix(0, m.UntilUnixNano)
			if s.gate.grant(session, until) != nil {
				return
			}
			expires = until
		default:
			return
		}
		base = m
		ack := m
		ack.Action = "ack"
		ackDeadline := time.Now().Add(CommandTimeout)
		if !expires.IsZero() && expires.Before(ackDeadline) {
			ackDeadline = expires
		}
		if writeMessage(conn, ack, ackDeadline) != nil {
			return
		}
	}
}
func connectController(ctx context.Context, conn net.Conn, p Policy) (*Controller, error) {
	deadline, e := commandDeadline(ctx)
	if e != nil {
		conn.Close()
		return nil, e
	}
	r := bufio.NewReaderSize(conn, MaxMessageBytes+1)
	hello, e := readMessage(conn, r, deadline)
	if e != nil || hello.Binding != p.SHA256() || hello.Action != "hello" || hello.Sequence != 0 || hello.UntilUnixNano != 0 {
		conn.Close()
		return nil, ErrProtocol
	}
	c := &Controller{conn: conn, reader: r, policy: p.Clone(), base: hello}
	if c.Closed(ctx) != nil {
		conn.Close()
		return nil, ErrProtocol
	}
	return c, nil
}
