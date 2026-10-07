package proxygate

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLateClientTrackAndReservedStreamStayInSingleQuarantine(t *testing.T) {
	for _, kind := range []string{"client", "upstream"} {
		t.Run(kind, func(t *testing.T) {
			g, _ := New(testPolicy())
			g.own("owner")
			g.grant("owner", time.Now().Add(MaxLease))
			a, e := g.AcquireSource("127.0.0.2:1")
			if e != nil {
				t.Fatal(e)
			}
			var reg *Registration
			if kind == "upstream" {
				l, r := net.Pipe()
				defer r.Close()
				if a.Track(l) != nil {
					t.Fatal("client")
				}
				reg, e = a.Reserve()
				if e != nil {
					t.Fatal(e)
				}
			}
			g.Close()
			g.mu.Lock()
			task := g.cleanup
			g.mu.Unlock()
			if task == nil {
				t.Fatal("uncompleted creation not quarantined")
			}
			for range 10 {
				g.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if !errors.Is(g.AwaitClosed(ctx), ErrCleanupUnknown) {
				t.Fatal("pending stream falsely reaped")
			}
			if g.own("new") == nil {
				t.Fatal("new owner ignored pending stream")
			}
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var started atomic.Int32
			l, r := net.Pipe()
			defer r.Close()
			conn := &heldCloseConn{Conn: l, release: release, started: &started}
			before := time.Now()
			if kind == "client" {
				e = a.Track(conn)
			} else {
				e = reg.Attach(conn)
			}
			if !errors.Is(e, ErrClosed) || time.Since(before) > 100*time.Millisecond {
				t.Fatal("late attach blocked or admitted", e)
			}
			g.mu.Lock()
			same := g.cleanup == task
			g.mu.Unlock()
			if !same {
				t.Fatal("late attach replaced quarantine task")
			}
			ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel2()
			if !errors.Is(g.AwaitClosed(ctx2), ErrCleanupUnknown) {
				t.Fatal("blocked Close falsely acknowledged")
			}
			once.Do(func() { close(release) })
			if e := g.AwaitClosed(context.Background()); e != nil {
				t.Fatal("cleanup did not finish", e)
			}
			if started.Load() != 1 {
				t.Fatal("multiple close jobs")
			}
			if g.own("fresh") != nil || g.grant("fresh", time.Now().Add(time.Second)) != nil {
				t.Fatal("fresh owner could not resume")
			}
			g.Close()
		})
	}
}
func TestLateAttachCompletionCannotInvalidateRacingFreshOwner(t *testing.T) {
	for range 250 {
		g, _ := New(testPolicy())
		g.own("old")
		g.grant("old", time.Now().Add(time.Second))
		a, e := g.AcquireSource("127.0.0.2:1")
		if e != nil {
			t.Fatal(e)
		}
		g.Close()
		left, right := net.Pipe()
		defer right.Close()
		attached := make(chan error, 1)
		ownerDone := make(chan error, 1)
		go func() {
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if e := g.own("fresh"); e == nil {
					ownerDone <- g.grant("fresh", time.Now().Add(time.Second))
					return
				}
				runtime.Gosched()
			}
			ownerDone <- ErrCleanupUnknown
		}()
		go func() { attached <- a.Track(left) }()
		if e := <-ownerDone; e != nil {
			t.Fatal("fresh owner did not acquire completed cleanup", e)
		}
		if e := <-attached; !errors.Is(e, ErrClosed) {
			t.Fatal("late attach admitted", e)
		}
		probe, e := g.AcquireSource("127.0.0.2:1")
		if e != nil {
			t.Fatal("old late attach invalidated fresh owner", e)
		}
		probe.Release()
		g.Close()
	}
}
func TestPendingReservationSurvivesAdmissionReleaseUntilExplicitAbort(t *testing.T) {
	g, _ := New(testPolicy())
	g.own("owner")
	g.grant("owner", time.Now().Add(MaxLease))
	a, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal(e)
	}
	r, e := a.Reserve()
	if e != nil {
		t.Fatal(e)
	}
	a.Release()
	r.Release()
	g.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if !errors.Is(g.AwaitClosed(ctx), ErrCleanupUnknown) {
		t.Fatal("Release erased uncompleted reservation")
	}
	r.Abort()
	if e := g.AwaitClosed(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestAwaitClosedCannotAttestOpenOrExpiredUnreapedScope(t *testing.T) {
	g, _ := New(testPolicy())
	g.own("owner")
	g.grant("owner", time.Now().Add(MaxLease))
	a, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal(e)
	}
	if !errors.Is(g.AwaitClosed(context.Background()), ErrClosed) {
		t.Fatal("open gate attested closed")
	}
	g.mu.Lock()
	g.expires = time.Now().Add(-time.Second)
	g.mu.Unlock()
	if !errors.Is(g.AwaitClosed(context.Background()), ErrCleanupUnknown) {
		t.Fatal("expired but unreaped scope attested closed")
	}
	g.Close()
	a.Release()
	if e := g.AwaitClosed(context.Background()); e != nil {
		t.Fatal(e)
	}
}
