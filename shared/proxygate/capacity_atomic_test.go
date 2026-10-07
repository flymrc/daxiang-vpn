package proxygate

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// This executes the old decision sequence with an explicit scheduling stop:
// capture owner under lock, unlock, close/regrant the same receiver session,
// then resume the stale closeFor(owner). It records why the delayed decision
// must not remain in either product capacity path.
func TestOldCapacityOwnerStringSequenceDemonstratesActivationABA(t *testing.T) {
	g, _ := New(testPolicy())
	g.own("same-session")
	g.grant("same-session", time.Now().Add(MaxLease))
	old, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal(e)
	}
	oldClient, oldPeer := net.Pipe()
	defer oldPeer.Close()
	if old.Track(oldClient) != nil {
		t.Fatal("old client")
	}
	for range MaxTrackedConnections - 1 {
		r, e := old.Reserve()
		if e != nil {
			t.Fatal(e)
		}
		l, p := net.Pipe()
		defer p.Close()
		if r.Attach(l) != nil {
			t.Fatal("old upstream")
		}
	}
	g.mu.Lock()
	if len(old.registrations) != MaxTrackedConnections {
		g.mu.Unlock()
		t.Fatal("old capacity precondition not reached")
	}
	staleOwner := g.owner
	g.mu.Unlock()
	g.Close()
	if e := g.AwaitClosed(context.Background()); e != nil {
		t.Fatal(e)
	}
	g.own("same-session")
	g.grant("same-session", time.Now().Add(MaxLease))
	fresh, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal(e)
	}
	left, right := net.Pipe()
	defer right.Close()
	if fresh.Track(left) != nil {
		t.Fatal("fresh client")
	}
	g.closeFor(staleOwner)
	if fresh.Context().Err() == nil {
		t.Fatal("old sequence did not reproduce same-owner activation collision")
	}
	if _, e := g.AcquireSource("127.0.0.2:1"); !errors.Is(e, ErrClosed) {
		t.Fatal("old sequence unexpectedly retained fresh grant")
	}
	if e := g.AwaitClosed(context.Background()); e != nil {
		t.Fatal(e)
	}
}

type deadlineBarrierConn struct {
	net.Conn
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (c *deadlineBarrierConn) SetDeadline(d time.Time) error {
	c.once.Do(func() { close(c.entered); <-c.release })
	return c.Conn.SetDeadline(d)
}

// The actual product capacity calls are held in their first lock-external I/O.
// Scope invalidation and the old-resource task must already be atomic; repeated
// closure and same-session activation cannot slip past that captured task.
func TestProductCapacityInvalidatesBeforeExternalIOAndSameSessionRegrant(t *testing.T) {
	for _, kind := range []string{"admissions", "reservations"} {
		t.Run(kind, func(t *testing.T) {
			g, _ := New(testPolicy())
			g.own("same-session")
			g.grant("same-session", time.Now().Add(MaxLease))
			a, e := g.AcquireSource("127.0.0.2:1")
			if e != nil {
				t.Fatal(e)
			}
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			left, right := net.Pipe()
			defer right.Close()
			barrier := &deadlineBarrierConn{Conn: left, entered: make(chan struct{}), release: release}
			if a.Track(barrier) != nil {
				t.Fatal("client")
			}
			ads := []*Admission{a}
			if kind == "admissions" {
				for range MaxAdmissions - 1 {
					ad, e := g.AcquireSource("127.0.0.2:1")
					if e != nil {
						t.Fatal(e)
					}
					ads = append(ads, ad)
				}
			} else {
				for range MaxTrackedConnections - 1 {
					reg, e := a.Reserve()
					if e != nil {
						t.Fatal(e)
					}
					l, r := net.Pipe()
					defer r.Close()
					if reg.Attach(l) != nil {
						t.Fatal("upstream")
					}
				}
			}
			result := make(chan error, 1)
			go func() {
				if kind == "admissions" {
					_, e := g.AcquireSource("127.0.0.2:1")
					result <- e
				} else {
					_, e := a.Reserve()
					result <- e
				}
			}()
			select {
			case <-barrier.entered:
			case <-time.After(time.Second):
				t.Fatal("capacity call did not reach held external I/O")
			}
			g.mu.Lock()
			task := g.cleanup
			closed := g.owner == ""
			g.mu.Unlock()
			if !closed || task == nil || a.Context().Err() == nil {
				t.Fatal("limit decision not atomic before lock-external I/O")
			}
			for range 10 {
				g.Close()
				if g.own("same-session") == nil || g.grant("same-session", time.Now().Add(time.Second)) == nil {
					t.Fatal("same session bypassed pending activation cleanup")
				}
			}
			g.mu.Lock()
			same := g.cleanup == task
			g.mu.Unlock()
			if !same {
				t.Fatal("capacity callback cleanup was replaced")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if !errors.Is(g.AwaitClosed(ctx), ErrCleanupUnknown) {
				t.Fatal("held cleanup falsely acknowledged")
			}
			for _, ad := range ads {
				ad.Release()
			}
			releaseOnce.Do(func() { close(release) })
			if e := <-result; !errors.Is(e, ErrCapacity) {
				t.Fatal(e)
			}
			if e := g.AwaitClosed(context.Background()); e != nil {
				t.Fatal(e)
			}
			if g.own("same-session") != nil || g.grant("same-session", time.Now().Add(time.Second)) != nil {
				t.Fatal("completed resources prevented fresh activation")
			}
			fresh, e := g.AcquireSource("127.0.0.2:1")
			if e != nil {
				t.Fatal("old capacity decision closed fresh activation", e)
			}
			fresh.Release()
			g.Close()
		})
	}
}
