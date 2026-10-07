package proxygate

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

const MaxLease = 5 * time.Second
const MinLease = 100 * time.Millisecond
const CommandTimeout = time.Second
const IOTimeout = CommandTimeout
const ClosedIdleTimeout = 40 * time.Second

type Gate struct {
	mu          sync.Mutex
	initialized bool
	policy      Policy
	managed     []netip.Prefix
	owner       string
	expires     time.Time
	admissions  map[*Admission]struct{}
	cleanup     *cleanupTask
}
type cleanupTask struct{ done chan struct{} }
type cleanupWork struct {
	task          *cleanupTask
	registrations []*Registration
	connections   []net.Conn
}
type Admission struct {
	gate          *Gate
	ctx           context.Context
	cancel        context.CancelFunc
	registrations map[*Registration]struct{}
	client        *Registration
	released      bool
}

// Registration reserves a resource before creation. A canceled pending creation
// stays quarantined until Attach supplies the late resource or Abort confirms
// that no resource was created. Only one terminal Attach or Abort is permitted.
type Registration struct {
	admission   *Admission
	ready       chan struct{}
	conn        net.Conn
	attached    bool
	aborted     bool
	quarantined bool
	released    bool
	fence       *writeFencedConn
}

func New(p Policy) (*Gate, error) {
	if p.Validate() != nil {
		return nil, ErrPolicy
	}
	g := &Gate{initialized: true, policy: p.Clone(), admissions: map[*Admission]struct{}{}}
	for _, s := range p.ManagedSources {
		g.managed = append(g.managed, netip.MustParsePrefix(s))
	}
	return g, nil
}
func (g *Gate) Policy() Policy                         { return g.policy.Clone() }
func (g *Gate) ValidateListener(actual net.Addr) error { return g.policy.ValidateListener(actual) }
func source(raw string) (netip.Addr, error) {
	if ap, e := netip.ParseAddrPort(raw); e == nil {
		return ap.Addr().Unmap(), nil
	}
	if a, e := netip.ParseAddr(raw); e == nil {
		return a.Unmap(), nil
	}
	return netip.Addr{}, ErrPolicy
}
func (g *Gate) Acquire(remote net.Addr) (*Admission, error) {
	if remote == nil {
		return nil, ErrPolicy
	}
	return g.AcquireSource(remote.String())
}
func (g *Gate) AcquireSource(raw string) (*Admission, error) {
	if g == nil || !g.initialized {
		return nil, ErrPolicy
	}
	a, e := source(raw)
	if e != nil {
		return nil, e
	}
	managed := false
	for _, pr := range g.managed {
		if pr.Contains(a) {
			managed = true
			break
		}
	}
	if !managed {
		return &Admission{ctx: context.Background()}, nil
	}
	g.mu.Lock()
	if g.cleanup != nil || g.owner == "" || !time.Now().Before(g.expires) {
		g.mu.Unlock()
		return nil, ErrClosed
	}
	if len(g.admissions) >= MaxAdmissions {
		work := g.beginInvalidateLocked("")
		g.mu.Unlock()
		g.finishCleanup(work)
		return nil, ErrCapacity
	}
	ctx, cancel := context.WithCancel(context.Background())
	ad := &Admission{gate: g, ctx: ctx, cancel: cancel, registrations: map[*Registration]struct{}{}}
	ad.client = &Registration{admission: ad, ready: make(chan struct{})}
	ad.registrations[ad.client] = struct{}{}
	g.admissions[ad] = struct{}{}
	g.mu.Unlock()
	return ad, nil
}
func (a *Admission) Context() context.Context { return a.ctx }

// Reserve must precede upstream OpenStream. The implicit first reservation is
// solely for Track of the existing local HTTP client; client plus upstreams are
// bounded to four simultaneous resources. A failure must not create a stream.
func (a *Admission) Reserve() (*Registration, error) {
	if a.gate == nil {
		return &Registration{}, nil
	}
	g := a.gate
	g.mu.Lock()
	if a.released || a.ctx.Err() != nil || g.cleanup != nil || g.owner == "" || !time.Now().Before(g.expires) {
		g.mu.Unlock()
		return nil, ErrClosed
	}
	if len(a.registrations) >= MaxTrackedConnections {
		work := g.beginInvalidateLocked("")
		g.mu.Unlock()
		g.finishCleanup(work)
		return nil, ErrCapacity
	}
	r := &Registration{admission: a, ready: make(chan struct{})}
	a.registrations[r] = struct{}{}
	g.mu.Unlock()
	return r, nil
}

// Track attaches only the implicit, pre-reserved local HTTP client. It is safe
// when closure races acquisition: the same reservation is already quarantined.
// A second distinct client or Track after Release/Abort is a programming error;
// ownership remains with the caller. Use Reserve/Attach for every upstream.
func (a *Admission) Track(c net.Conn) error {
	if c == nil {
		return ErrProtocol
	}
	if a.gate == nil {
		return nil
	}
	return a.client.Attach(c)
}
func (r *Registration) Attach(c net.Conn) error {
	if c == nil {
		return ErrProtocol
	}
	if r.admission == nil {
		return nil
	}
	a := r.admission
	g := a.gate
	g.mu.Lock()
	if r.attached {
		same := r.conn == c
		closed := a.ctx.Err() != nil || r.quarantined || r.released
		g.mu.Unlock()
		if !same {
			return ErrProtocol
		}
		if closed {
			return ErrClosed
		}
		return nil
	}
	if r.aborted || r.released {
		g.mu.Unlock()
		return ErrProtocol
	}
	r.conn = c
	r.attached = true
	closed := a.ctx.Err() != nil || r.quarantined || g.owner == "" || !time.Now().Before(g.expires)
	quarantined := r.quarantined
	owner := g.owner
	if !closed {
		close(r.ready)
		g.mu.Unlock()
		return nil
	}
	g.mu.Unlock()
	// Attach never calls physical Close, even for a late stream. Stop application
	// I/O before releasing the same reserved resource to its cleanup worker.
	c.SetDeadline(time.Now())
	// An existing quarantine already owns this reservation. Otherwise capture
	// the old owner before publishing ready; completion can then permit a fresh
	// owner, which this stale Attach must never close afterwards.
	if !quarantined {
		g.closeFor(owner)
	}
	g.mu.Lock()
	close(r.ready)
	g.mu.Unlock()
	return ErrClosed
}

// Abort confirms that OpenStream created no connection. It must never be used
// to abandon a creation that may still return a late stream.
func (r *Registration) Abort() {
	if r.admission == nil {
		return
	}
	g := r.admission.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.attached || r.aborted || r.released {
		return
	}
	r.aborted = true
	close(r.ready)
	if !r.quarantined {
		r.released = true
		delete(r.admission.registrations, r)
		if r.admission.released && len(r.admission.registrations) == 0 {
			delete(g.admissions, r.admission)
		}
	}
}

// Release is called only after the underlying transport Close has returned.
// During quarantine the worker owns the completion proof and Release cannot
// turn a still-running Close into an early CLOSED acknowledgement.
func (r *Registration) Release() {
	if r.admission == nil {
		return
	}
	g := r.admission.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.quarantined || r.released {
		return
	}
	if r.fence != nil && !r.fence.completed {
		return
	}
	if !r.attached && !r.aborted {
		return
	}
	r.released = true
	delete(r.admission.registrations, r)
	if r.admission.released && len(r.admission.registrations) == 0 {
		delete(g.admissions, r.admission)
	}
}
func (a *Admission) Untrack(c net.Conn) {
	if a.gate == nil {
		return
	}
	g := a.gate
	g.mu.Lock()
	var found *Registration
	for r := range a.registrations {
		if r.attached && r.conn == c {
			found = r
			break
		}
	}
	g.mu.Unlock()
	if found != nil {
		found.Release()
	}
}

// Release ends admission. Unused local-client reservations can be aborted; each
// explicit upstream reservation must still Attach/Abort, even after this call.
func (a *Admission) Release() {
	if a.gate == nil {
		return
	}
	g := a.gate
	g.mu.Lock()
	if !a.released {
		a.released = true
		a.cancel()
		client := a.client
		if !client.attached && !client.aborted && !client.released {
			client.aborted = true
			close(client.ready)
		}
		if !client.quarantined && !client.released {
			if !client.attached && !client.aborted {
				client.aborted = true
				close(client.ready)
			}
			// The request is complete; the HTTP server owns any idle client
			// connection. Explicit pending upstream creations remain reserved.
			client.released = true
			delete(a.registrations, client)
		}
		if len(a.registrations) == 0 {
			delete(g.admissions, a)
		}
	}
	g.mu.Unlock()
}

// closeFor invalidates without waiting for any physical Close or pending Open.
// The single bounded quarantined task includes all pre-reserved late resources.
func (g *Gate) closeFor(owner string) {
	g.mu.Lock()
	work := g.beginInvalidateLocked(owner)
	g.mu.Unlock()
	g.finishCleanup(work)
}

// beginInvalidateLocked is the single atomic rejection decision. Capacity must
// invalidate while holding the same lock as its limit check; a deferred owner
// string check could otherwise close a later activation of the same session.
// This function performs no connection I/O and starts no goroutines.
func (g *Gate) beginInvalidateLocked(owner string) *cleanupWork {
	if owner != "" && g.owner != owner {
		return nil
	}
	g.owner = ""
	g.expires = time.Time{}
	if g.cleanup != nil {
		return nil
	}
	regs := []*Registration{}
	conns := []net.Conn{}
	for a := range g.admissions {
		a.cancel()
		for r := range a.registrations {
			if !r.released {
				r.quarantined = true
				regs = append(regs, r)
				if r.attached {
					conns = append(conns, r.conn)
				}
			}
		}
	}
	if len(regs) == 0 {
		return nil
	}
	task := &cleanupTask{done: make(chan struct{})}
	g.cleanup = task
	return &cleanupWork{task: task, registrations: regs, connections: conns}
}

// finishCleanup acts only on an already-captured batch. It never changes the
// current owner or invalidates another activation after the decision lock.
func (g *Gate) finishCleanup(work *cleanupWork) {
	if work == nil {
		return
	}
	task, regs, conns := work.task, work.registrations, work.connections
	deadline := time.Now()
	for _, c := range conns {
		c.SetDeadline(deadline)
	}
	go func() {
		var next atomic.Int64
		var workers sync.WaitGroup
		for range min(len(regs), MaxCleanupWorkers) {
			workers.Go(func() {
				for {
					i := int(next.Add(1) - 1)
					if i >= len(regs) {
						return
					}
					r := regs[i]
					<-r.ready
					g.mu.Lock()
					c := r.conn
					g.mu.Unlock()
					if c != nil {
						c.Close()
					}
					g.mu.Lock()
					r.released = true
					delete(r.admission.registrations, r)
					g.mu.Unlock()
				}
			})
		}
		workers.Wait()
		g.mu.Lock()
		for a := range g.admissions {
			if a.ctx.Err() != nil && len(a.registrations) == 0 {
				a.released = true
				delete(g.admissions, a)
			}
		}
		if g.cleanup == task {
			g.cleanup = nil
		}
		close(task.done)
		g.mu.Unlock()
	}()
}

// Close invalidates immediately; it does not claim physical resources reaped.
func (g *Gate) Close() error {
	if g == nil {
		return ErrPolicy
	}
	g.closeFor("")
	return nil
}

// AwaitClosed proves current managed scope is closed and all reserved cleanup
// finished. It waits at most one command budget; a blocked Close or late Open
// stays quarantined rather than admitting a new owner or creating another task.
func (g *Gate) AwaitClosed(ctx context.Context) error {
	if g == nil || ctx == nil {
		return ErrCleanupUnknown
	}
	g.mu.Lock()
	if g.owner != "" && time.Now().Before(g.expires) {
		g.mu.Unlock()
		return ErrClosed
	}
	task := g.cleanup
	if task == nil && len(g.admissions) > 0 {
		g.mu.Unlock()
		return ErrCleanupUnknown
	}
	g.mu.Unlock()
	if task == nil {
		return nil
	}
	wait, cancel := context.WithTimeout(ctx, CommandTimeout)
	defer cancel()
	select {
	case <-task.done:
		g.mu.Lock()
		open := g.owner != "" && time.Now().Before(g.expires)
		g.mu.Unlock()
		if open {
			return ErrClosed
		}
		return nil
	case <-wait.Done():
		return ErrCleanupUnknown
	}
}
func (g *Gate) own(owner string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.initialized || g.cleanup != nil || len(g.admissions) > 0 {
		return ErrCleanupUnknown
	}
	if g.owner != "" && g.owner != owner {
		return ErrClosed
	}
	g.owner = owner
	g.expires = time.Time{}
	return nil
}
func (g *Gate) grant(owner string, until time.Time) error {
	now := time.Now()
	if !until.After(now) || until.After(now.Add(MaxLease)) {
		return ErrProtocol
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cleanup != nil || g.owner != owner || owner == "" {
		return ErrClosed
	}
	g.expires = until
	return nil
}
