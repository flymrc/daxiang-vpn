package proxygate

import (
	"net"
	"sync"
	"time"
)

const MaxInflightWrites = 1
const MaxInflightDeadlines = 1
const MaxRevokerDeadlines = 1

// AttachFenced transfers the one pre-reserved upstream connection and wraps
// all later writes/deadline updates in the same admission decision lock as
// scope invalidation. A nil error or ErrClosed transfers ownership; ErrClosed
// leaves the late connection in the existing quarantine. ErrProtocol retains
// the caller's ownership of an unexpected second/aborted connection.
// Unmanaged reservations return the original connection unchanged.
func (r *Registration) AttachFenced(c net.Conn) (net.Conn, error) {
	if c == nil {
		return nil, ErrProtocol
	}
	if r.admission == nil {
		return c, nil
	}
	a := r.admission
	g := a.gate
	g.mu.Lock()
	if r.fence != nil && r.fence.Conn == c {
		f := r.fence
		closed := !f.liveLocked()
		g.mu.Unlock()
		if closed {
			return f, ErrClosed
		}
		return f, nil
	}
	if r.attached || r.aborted || r.released || r.fence != nil {
		g.mu.Unlock()
		return nil, ErrProtocol
	}
	f := &writeFencedConn{Conn: c, registration: r}
	r.fence = f
	r.conn = f
	r.attached = true
	closed := !f.liveLocked()
	quarantined, owner := r.quarantined, g.owner
	if !closed {
		close(r.ready)
		g.mu.Unlock()
		return f, nil
	}
	g.mu.Unlock()
	// Do not physically Close here. This same reservation already blocks a new
	// owner until its late attachment and actual cleanup have completed.
	_ = f.SetDeadline(time.Now())
	if !quarantined {
		g.closeFor(owner)
	}
	g.mu.Lock()
	close(r.ready)
	g.mu.Unlock()
	return f, ErrClosed
}

type writeFencedConn struct {
	net.Conn
	registration   *Registration
	deadlineMu     sync.Mutex // only nonblocking deadline syscalls, never Write
	operations     sync.WaitGroup
	closed         bool // below fields are guarded by the gate decision mutex
	completed      bool
	writeActive    bool
	deadlineActive bool
	revokerActive  bool
	closeOnce      sync.Once
	closeErr       error
}

func (c *writeFencedConn) liveLocked() bool {
	r, a := c.registration, c.registration.admission
	g := a.gate
	return !c.closed && !r.released && !r.aborted && !r.quarantined && !a.released && a.ctx.Err() == nil && g.cleanup == nil && g.owner != "" && time.Now().Before(g.expires)
}

func (c *writeFencedConn) begin(deadline bool) error {
	g := c.registration.admission.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if !c.liveLocked() {
		return ErrClosed
	}
	active := &c.writeActive
	if deadline {
		active = &c.deadlineActive
	}
	if *active {
		return ErrCapacity
	}
	*active = true
	c.operations.Add(1)
	return nil
}

func (c *writeFencedConn) end(deadline bool) {
	g := c.registration.admission.gate
	g.mu.Lock()
	if deadline {
		c.deadlineActive = false
	} else {
		c.writeActive = false
	}
	g.mu.Unlock()
	c.operations.Done()
}

// The permit decision linearizes with invalidation. A permit obtained before
// closure is an in-flight write; its already-submitted bytes cannot be recalled.
// No new write permit is issued after closure, and callers do not queue here.
func (c *writeFencedConn) Write(p []byte) (int, error) {
	if err := c.begin(false); err != nil {
		return 0, err
	}
	defer c.end(false)
	return c.Conn.Write(p)
}

func (c *writeFencedConn) live() bool {
	g := c.registration.admission.gate
	g.mu.Lock()
	live := c.liveLocked()
	g.mu.Unlock()
	return live
}

func (c *writeFencedConn) updateDeadline(t time.Time, set func(time.Time) error) error {
	// Revocation's past deadline must remain usable after closure. Only the
	// sole revoker can wait behind one accepted deadline operation. Once physical
	// Close starts, its own raw past deadline replaces external revoker permits.
	// All accepted operations join Close under the same Add/Wait decision guard.
	if !t.IsZero() && !t.After(time.Now()) {
		g := c.registration.admission.gate
		g.mu.Lock()
		if c.closed {
			g.mu.Unlock()
			return ErrClosed
		}
		if c.revokerActive {
			g.mu.Unlock()
			return ErrCapacity
		}
		c.revokerActive = true
		c.operations.Add(1)
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			c.revokerActive = false
			g.mu.Unlock()
			c.operations.Done()
		}()
		c.deadlineMu.Lock()
		defer c.deadlineMu.Unlock()
		return set(t)
	}
	if err := c.begin(true); err != nil {
		return err
	}
	defer c.end(true)
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if !c.live() {
		_ = c.Conn.SetDeadline(time.Now())
		return ErrClosed
	}
	err := set(t)
	// A scope may close while an already-permitted update is entering the
	// transport. Repair that update before returning, and forbid its caller
	// from proceeding to a subsequent command or relay write.
	if !c.live() {
		_ = c.Conn.SetDeadline(time.Now())
		return ErrClosed
	}
	return err
}

func (c *writeFencedConn) SetDeadline(t time.Time) error {
	return c.updateDeadline(t, c.Conn.SetDeadline)
}
func (c *writeFencedConn) SetReadDeadline(t time.Time) error {
	return c.updateDeadline(t, c.Conn.SetReadDeadline)
}
func (c *writeFencedConn) SetWriteDeadline(t time.Time) error {
	return c.updateDeadline(t, c.Conn.SetWriteDeadline)
}

func (c *writeFencedConn) Close() error {
	c.closeOnce.Do(func() {
		g := c.registration.admission.gate
		g.mu.Lock()
		c.closed = true
		g.mu.Unlock()
		c.deadlineMu.Lock()
		_ = c.Conn.SetDeadline(time.Now())
		c.deadlineMu.Unlock()
		c.closeErr = c.Conn.Close()
		// Add was serialized with closed=true under the same decision mutex.
		// Awaiting operations therefore cannot race another accepted Add.
		c.operations.Wait()
		g.mu.Lock()
		c.completed = true
		g.mu.Unlock()
		c.registration.Release()
	})
	return c.closeErr
}
