package proxygate

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fenceProbeConn struct {
	mu              sync.Mutex
	deadline        time.Time
	writeCalls      atomic.Int64
	closeCalls      atomic.Int64
	deadlineEntered chan struct{}
	deadlineRelease chan struct{}
	holdPast        bool
	writeEntered    chan struct{}
	writeRelease    chan struct{}
	closeRelease    chan struct{}
	deadlineOnce    sync.Once
	writeOnce       sync.Once
}

func (c *fenceProbeConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *fenceProbeConn) Write(p []byte) (int, error) {
	if c.writeEntered != nil {
		c.writeOnce.Do(func() { close(c.writeEntered) })
		<-c.writeRelease
	}
	c.writeCalls.Add(1)
	return len(p), nil
}
func (c *fenceProbeConn) Close() error {
	c.closeCalls.Add(1)
	if c.closeRelease != nil {
		<-c.closeRelease
	}
	return nil
}
func (c *fenceProbeConn) LocalAddr() net.Addr  { return &net.TCPAddr{} }
func (c *fenceProbeConn) RemoteAddr() net.Addr { return &net.TCPAddr{} }
func (c *fenceProbeConn) SetDeadline(t time.Time) error {
	if c.deadlineEntered != nil && (c.holdPast || t.IsZero() || t.After(time.Now())) {
		c.deadlineOnce.Do(func() { close(c.deadlineEntered); <-c.deadlineRelease })
	}
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

func TestFencedPastRevokerJoinsCloseAndCannotQueueAfterPhysicalClose(t *testing.T) {
	g, _, r := ownedFenceReservation(t)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	raw := &fenceProbeConn{holdPast: true, deadlineEntered: make(chan struct{}), deadlineRelease: release}
	fenced, err := r.AttachFenced(raw)
	if err != nil {
		t.Fatal(err)
	}
	deadlineDone := make(chan error, 1)
	go func() { deadlineDone <- fenced.SetDeadline(time.Now().Add(-time.Second)) }()
	<-raw.deadlineEntered
	if err := fenced.SetReadDeadline(time.Now().Add(-time.Second)); !errors.Is(err, ErrCapacity) {
		t.Fatal("past revokers exceeded their one nonqueuing permit", err)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- fenced.Close() }()
	until := time.Now().Add(time.Second)
	for {
		g.mu.Lock()
		closing := r.fence.closed
		g.mu.Unlock()
		if closing {
			break
		}
		if time.Now().After(until) {
			t.Fatal("physical Close did not enter its decision fence")
		}
		time.Sleep(time.Millisecond)
	}
	if err := fenced.SetWriteDeadline(time.Now().Add(-time.Second)); !errors.Is(err, ErrClosed) {
		t.Fatal("new past revoker admitted after physical Close started", err)
	}
	select {
	case err := <-closeDone:
		t.Fatal("normal Close returned before accepted past syscall finished", err)
	default:
	}
	// The control's scope invalidation can proceed even while its deadline call
	// waits on this already-owned connection. No false cleanup proof is allowed.
	invalidationDone := make(chan struct{})
	go func() { g.Close(); close(invalidationDone) }()
	until = time.Now().Add(time.Second)
	for {
		g.mu.Lock()
		invalidated := g.cleanup != nil
		g.mu.Unlock()
		if invalidated {
			break
		}
		if time.Now().After(until) {
			t.Fatal("scope invalidation blocked behind a past syscall")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := g.AwaitClosed(ctx); !errors.Is(err, ErrCleanupUnknown) {
		t.Fatal("held past syscall obtained a cleanup acknowledgement", err)
	}
	once.Do(func() { close(release) })
	if err := <-deadlineDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	<-invalidationDone
	if err := g.AwaitClosed(context.Background()); err != nil || raw.closeCalls.Load() != 1 {
		t.Fatal("past syscall cleanup did not complete exactly once", err)
	}
}
func (c *fenceProbeConn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *fenceProbeConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }
func (c *fenceProbeConn) currentDeadline() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline
}

func ownedFenceReservation(t *testing.T) (*Gate, *Admission, *Registration) {
	t.Helper()
	g, _ := New(testPolicy())
	if g.own("owned") != nil || g.grant("owned", time.Now().Add(MaxLease)) != nil {
		t.Fatal("grant failed")
	}
	a, err := g.AcquireSource("127.0.0.2:1")
	if err != nil || a.Track(&fenceProbeConn{}) != nil {
		t.Fatal("client reservation failed", err)
	}
	r, err := a.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Release(); g.Close(); _ = g.AwaitClosed(context.Background()) })
	return g, a, r
}

// This is the former product sequence: a ctx check before an unguarded deadline
// syscall, then an unguarded command. Closing between the check and the actual
// syscall can restore a future deadline and dispatch after cancellation.
func TestLegacyUnguardedDeadlineSequenceReproducesCanceledCommand(t *testing.T) {
	g, a, r := ownedFenceReservation(t)
	releaseDeadline, releaseClose := make(chan struct{}), make(chan struct{})
	var deadlineOnce, closeOnce sync.Once
	defer deadlineOnce.Do(func() { close(releaseDeadline) })
	defer closeOnce.Do(func() { close(releaseClose) })
	raw := &fenceProbeConn{deadlineEntered: make(chan struct{}), deadlineRelease: releaseDeadline, closeRelease: releaseClose}
	if r.Attach(raw) != nil {
		t.Fatal("legacy attach failed")
	}
	done := make(chan error, 1)
	go func() {
		if err := a.Context().Err(); err != nil {
			done <- err
			return
		}
		_ = raw.SetDeadline(time.Now().Add(time.Minute))
		_, err := raw.Write([]byte("CONNECT owned:443\n"))
		done <- err
	}()
	<-raw.deadlineEntered
	g.Close()
	deadlineOnce.Do(func() { close(releaseDeadline) })
	if err := <-done; err != nil || a.Context().Err() == nil || raw.writeCalls.Load() != 1 || !raw.currentDeadline().After(time.Now()) {
		t.Fatal("old unguarded window was not reproduced", err)
	}
	closeOnce.Do(func() { close(releaseClose) })
	if g.AwaitClosed(context.Background()) != nil {
		t.Fatal("legacy proof cleanup incomplete")
	}
}

func TestFencedDeadlineClosingWindowRepairsAndRejectsCommand(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(map[bool]string{false: "future", true: "clear"}[clear], func(t *testing.T) {
			g, _, r := ownedFenceReservation(t)
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			raw := &fenceProbeConn{deadlineEntered: make(chan struct{}), deadlineRelease: release}
			fenced, err := r.AttachFenced(raw)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				d := time.Now().Add(time.Minute)
				if clear {
					d = time.Time{}
				}
				err := fenced.SetDeadline(d)
				if err == nil {
					_, err = fenced.Write([]byte("CONNECT owned:443\n"))
				}
				done <- err
			}()
			<-raw.deadlineEntered
			if err := fenced.SetWriteDeadline(time.Now().Add(time.Minute)); !errors.Is(err, ErrCapacity) {
				t.Fatal("ordinary deadlines queued beyond their one permit", err)
			}
			closed := make(chan struct{})
			go func() { g.Close(); close(closed) }()
			until := time.Now().Add(time.Second)
			for time.Now().Before(until) {
				probe, err := g.AcquireSource("127.0.0.2:1")
				if errors.Is(err, ErrClosed) {
					break
				}
				if probe != nil {
					probe.Release()
				}
				time.Sleep(time.Millisecond)
			}
			if _, err := fenced.Write([]byte("late command")); !errors.Is(err, ErrClosed) {
				t.Fatal("closed write admitted", err)
			}
			once.Do(func() { close(release) })
			if err := <-done; !errors.Is(err, ErrClosed) {
				t.Fatal("canceled update/command succeeded", err)
			}
			<-closed
			if g.AwaitClosed(context.Background()) != nil || raw.writeCalls.Load() != 0 || raw.currentDeadline().IsZero() || raw.currentDeadline().After(time.Now()) {
				t.Fatal("fenced closing update restored an open deadline or dispatched")
			}
		})
	}
}

func TestFencedCloseWaitsForOneInflightWriteAndRetainsQuarantine(t *testing.T) {
	g, _, r := ownedFenceReservation(t)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	raw := &fenceProbeConn{writeEntered: make(chan struct{}), writeRelease: release}
	fenced, err := r.AttachFenced(raw)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := fenced.Write([]byte("permitted before close")); done <- err }()
	<-raw.writeEntered
	if _, err := fenced.Write([]byte("second write")); !errors.Is(err, ErrCapacity) {
		t.Fatal("writes queued beyond their one permit", err)
	}
	g.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if !errors.Is(g.AwaitClosed(ctx), ErrCleanupUnknown) || g.own("later") == nil {
		t.Fatal("in-flight write was falsely reaped")
	}
	if _, err := fenced.Write([]byte("post-close write")); !errors.Is(err, ErrClosed) {
		t.Fatal("late write was admitted", err)
	}
	if err := fenced.SetDeadline(time.Time{}); !errors.Is(err, ErrClosed) {
		t.Fatal("closed deadline was cleared", err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal("pre-close write lost its in-flight semantics", err)
	}
	if g.AwaitClosed(context.Background()) != nil || raw.writeCalls.Load() != 1 || raw.closeCalls.Load() != 1 {
		t.Fatal("quarantine did not finish exactly one accepted write and Close")
	}
	if g.own("later") != nil || g.grant("later", time.Now().Add(time.Second)) != nil {
		t.Fatal("new owner could not resume")
	}
	if _, err := fenced.Write([]byte("old owner")); !errors.Is(err, ErrClosed) {
		t.Fatal("old fenced connection wrote during a new activation", err)
	}
}

func TestFencedAttachmentReusesBudgetAndUnmanagedRemainsUnwrapped(t *testing.T) {
	g, a, r := ownedFenceReservation(t)
	r.Abort()
	for range 12 {
		r, err := a.Reserve()
		if err != nil {
			t.Fatal("normal fenced close leaked a registration", err)
		}
		raw := &fenceProbeConn{}
		fenced, err := r.AttachFenced(raw)
		if err != nil {
			t.Fatal(err)
		}
		if again, err := r.AttachFenced(raw); again != fenced || err != nil {
			t.Fatal("identical attachment was not idempotent", err)
		}
		if fenced.Close() != nil {
			t.Fatal("fenced close failed")
		}
	}
	outside, err := g.AcquireSource("127.0.0.7:1")
	if err != nil {
		t.Fatal(err)
	}
	r, _ = outside.Reserve()
	raw := &fenceProbeConn{}
	conn, err := r.AttachFenced(raw)
	if err != nil || conn != raw {
		t.Fatal("retained connection was wrapped or altered", err)
	}
}

func TestFencedPublicReleaseCannotEraseRawClosedInflightPermit(t *testing.T) {
	g, a, r := ownedFenceReservation(t)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	raw := &fenceProbeConn{writeEntered: make(chan struct{}), writeRelease: release}
	fenced, err := r.AttachFenced(raw)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = fenced.Write([]byte("accepted permit")); close(done) }()
	<-raw.writeEntered
	// A caller may see the raw Close return before the fenced write permit
	// finishes. Neither public release path can treat that as fence completion.
	_ = raw.Close()
	r.Release()
	a.Untrack(fenced)
	g.mu.Lock()
	count := len(a.registrations)
	g.mu.Unlock()
	if count != 2 {
		t.Fatalf("public release erased a raw-closed in-flight resource: count=%d", count)
	}
	g.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if !errors.Is(g.AwaitClosed(ctx), ErrCleanupUnknown) || g.own("later") == nil {
		t.Fatal("public Release produced false completion")
	}
	once.Do(func() { close(release) })
	<-done
	if err := g.AwaitClosed(context.Background()); err != nil {
		t.Fatal("fenced cleanup did not finish after the accepted permit", err)
	}
}
