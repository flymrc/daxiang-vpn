package proxygate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testPolicy() Policy {
	return Policy{Version: Version, Epoch: "test-epoch", Interface: "wg-test", ManagedBy: "zhvpn-device", PolicySHA256: strings.Repeat("a", 64), ProfileSHA256: strings.Repeat("b", 64), Listener: "127.0.0.1:18081", ControllerUID: 0, ManagedSources: []string{"127.0.0.2/32", "127.0.0.4/32"}, RetainedSources: []string{"127.0.0.3/32", "127.0.0.7/32"}}
}
func TestPolicyFrozenScopeAndCanonical(t *testing.T) {
	p := testPolicy()
	raw, _ := json.Marshal(p)
	got, e := DecodePolicy(raw)
	if e != nil || got.SHA256() == "" {
		t.Fatal(e)
	}
	g, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	p.ManagedSources[0] = "127.0.0.8/32"
	clone := g.Policy()
	clone.ManagedSources[0] = "127.0.0.9/32"
	if g.Policy().ManagedSources[0] != "127.0.0.2/32" {
		t.Fatal("scope mutable")
	}
	if !g.Policy().Covers(netip.MustParsePrefix("127.0.0.2/32")) || g.Policy().Covers(netip.MustParsePrefix("127.0.0.0/29")) {
		t.Fatal("coverage gap")
	}
	for _, bad := range [][]byte{append(raw, '\n'), bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), bytes.Replace(raw, []byte(`"version":1`), []byte(`"Version":1`), 1), append(append([]byte{}, raw...), raw...), bytes.Repeat([]byte{'x'}, MaxPolicyBytes+1)} {
		if _, e := DecodePolicy(bad); !errors.Is(e, ErrPolicy) {
			t.Fatal("noncanonical accepted")
		}
	}
	for name, mutate := range map[string]func(*Policy){"overlap": func(p *Policy) { p.ManagedSources = append(p.ManagedSources, "127.0.0.0/24") }, "retained-overlap": func(p *Policy) { p.RetainedSources = append(p.RetainedSources, "127.0.0.2/32") }, "unmasked": func(p *Policy) { p.ManagedSources[0] = "127.0.0.2/24" }, "hostname": func(p *Policy) { p.Listener = "localhost:18081" }, "wildcard": func(p *Policy) { p.Listener = "0.0.0.0:18081" }, "nil-retention": func(p *Policy) { p.RetainedSources = nil }, "wide": func(p *Policy) { p.ManagedSources = []string{"0.0.0.0/0"} }, "mapped": func(p *Policy) { p.ManagedSources = []string{"::ffff:127.0.0.2/128"} }} {
		t.Run(name, func(t *testing.T) {
			p := testPolicy()
			mutate(&p)
			if p.Validate() == nil {
				t.Fatal("invalid policy")
			}
		})
	}
}
func TestGateDefaultClosedRetainsOthersAndRevokesTracked(t *testing.T) {
	g, _ := New(testPolicy())
	if _, e := g.AcquireSource("127.0.0.2:123"); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	for _, ip := range []string{"127.0.0.3:123", "127.0.0.7:123", "127.0.0.9:123"} {
		a, e := g.AcquireSource(ip)
		if e != nil || a.Context().Err() != nil {
			t.Fatal(e)
		}
		a.Release()
	}
	g.own("owner")
	if e := g.grant("owner", time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	a, e := g.AcquireSource("127.0.0.2:123")
	if e != nil {
		t.Fatal(e)
	}
	left, right := net.Pipe()
	defer right.Close()
	if a.Track(left) != nil || a.Track(left) != nil || len(a.registrations) != 1 {
		t.Fatal("tracking")
	}
	g.Close()
	if a.Context().Err() == nil {
		t.Fatal("not canceled")
	}
	right.SetReadDeadline(time.Now().Add(time.Second))
	if _, e := right.Read(make([]byte, 1)); e == nil {
		t.Fatal("not closed")
	}
	if !errors.Is(a.Track(left), ErrClosed) {
		t.Fatal("closed client reattached")
	}
	if _, e := g.AcquireSource("127.0.0.4:123"); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	a.Release()
}
func TestGateBoundedRegistrationAndUntrack(t *testing.T) {
	g, _ := New(testPolicy())
	g.own("owner")
	g.grant("owner", time.Now().Add(MaxLease))
	admissions := make([]*Admission, 0, MaxAdmissions)
	for range MaxAdmissions {
		a, e := g.AcquireSource("127.0.0.2:1")
		if e != nil {
			t.Fatal(e)
		}
		admissions = append(admissions, a)
	}
	if _, e := g.AcquireSource("127.0.0.2:1"); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	if _, e := g.AcquireSource("127.0.0.2:1"); !errors.Is(e, ErrClosed) {
		t.Fatal("admission overflow did not close gate")
	}
	for _, a := range admissions {
		a.Release()
	}
	if e := g.AwaitClosed(context.Background()); e != nil {
		t.Fatal(e)
	}
	g.own("next")
	g.grant("next", time.Now().Add(MaxLease))
	a, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal(e)
	}
	client, peer := net.Pipe()
	defer peer.Close()
	if a.Track(client) != nil {
		t.Fatal("client")
	}
	regs := []*Registration{}
	for range MaxTrackedConnections - 1 {
		r, e := a.Reserve()
		if e != nil {
			t.Fatal(e)
		}
		l, p := net.Pipe()
		defer p.Close()
		if r.Attach(l) != nil {
			t.Fatal("attach")
		}
		regs = append(regs, r)
	}
	old := regs[0].conn
	old.Close()
	a.Untrack(old)
	replace, e := a.Reserve()
	if e != nil {
		t.Fatal("replacement reservation")
	}
	l, p := net.Pipe()
	defer p.Close()
	replace.Attach(l)
	if _, e := a.Reserve(); !errors.Is(e, ErrCapacity) {
		t.Fatal("overflow")
	}
	if _, e := g.AcquireSource("127.0.0.2:1"); !errors.Is(e, ErrClosed) {
		t.Fatal("resource overflow failed open")
	}
	if e := g.AwaitClosed(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestGateRaceCloseAcquireAttachAndOldOwner(t *testing.T) {
	g, _ := New(testPolicy())
	for range 25 {
		if e := g.AwaitClosed(context.Background()); e != nil {
			t.Fatal(e)
		}
		g.own("owner")
		g.grant("owner", time.Now().Add(time.Second))
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() {
				a, e := g.AcquireSource("127.0.0.2:1")
				if e != nil {
					return
				}
				l, r := net.Pipe()
				defer r.Close()
				a.Track(l)
				a.Release()
				l.Close()
			})
		}
		wg.Go(func() { g.Close() })
		wg.Wait()
	}
	if e := g.AwaitClosed(context.Background()); e != nil {
		t.Fatal(e)
	}
	g.own("new")
	g.grant("new", time.Now().Add(time.Second))
	g.closeFor("old")
	a, e := g.AcquireSource("127.0.0.2:1")
	if e != nil {
		t.Fatal("old owner canceled newer")
	}
	a.Release()
	g.Close()
}

type heldCloseConn struct {
	net.Conn
	release <-chan struct{}
	started *atomic.Int32
	once    sync.Once
}

func (c *heldCloseConn) Close() error {
	var e error
	c.once.Do(func() { c.started.Add(1); <-c.release; e = c.Conn.Close() })
	return e
}
func TestGateBlockingCloseQuarantineBoundedAndZeroValueClosed(t *testing.T) {
	var zero Gate
	if _, e := zero.AcquireSource("127.0.0.7:1"); !errors.Is(e, ErrPolicy) {
		t.Fatal("zero gate admitted traffic")
	}
	g, _ := New(testPolicy())
	g.own("owner")
	g.grant("owner", time.Now().Add(MaxLease))
	release := make(chan struct{})
	var started atomic.Int32
	defer close(release)
	ads := []*Admission{}
	for range 9 {
		a, e := g.AcquireSource("127.0.0.2:1")
		if e != nil {
			t.Fatal(e)
		}
		ads = append(ads, a)
		for i := range MaxTrackedConnections {
			l, r := net.Pipe()
			defer r.Close()
			conn := &heldCloseConn{Conn: l, release: release, started: &started}
			if i == 0 {
				if a.Track(conn) != nil {
					t.Fatal("client")
				}
			} else {
				reg, e := a.Reserve()
				if e != nil || reg.Attach(conn) != nil {
					t.Fatal("registration")
				}
			}
		}
	}
	before := time.Now()
	if g.Close() != nil || time.Since(before) > 100*time.Millisecond {
		t.Fatal("invalidate blocked")
	}
	for _, a := range ads {
		if a.Context().Err() == nil {
			t.Fatal("context survived invalidate")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if !errors.Is(g.AwaitClosed(ctx), ErrCleanupUnknown) {
		t.Fatal("false reaped proof")
	}
	g.mu.Lock()
	task := g.cleanup
	g.mu.Unlock()
	if task == nil {
		t.Fatal("missing quarantine")
	}
	for range 30 {
		g.Close()
		if g.own("new") == nil || g.grant("owner", time.Now().Add(time.Second)) == nil {
			t.Fatal("quarantine revived")
		}
	}
	g.mu.Lock()
	same := g.cleanup == task
	g.mu.Unlock()
	if !same || started.Load() > 16 {
		t.Fatal("cleanup task/workers grew")
	}
	if ad, e := g.AcquireSource("127.0.0.3:1"); e != nil {
		t.Fatal("protected interrupted")
	} else {
		ad.Release()
	}
}
func TestCodecRejectsFiniteNonCanonicalShapes(t *testing.T) {
	m := message{Version: Version, Binding: strings.Repeat("c", 64), Session: strings.Repeat("a", 32), Nonce: strings.Repeat("b", 32), Action: "closed", Sequence: 1}
	raw, _ := json.Marshal(m)
	if _, e := decodeMessage(raw); e != nil {
		t.Fatal(e)
	}
	for name, b := range map[string][]byte{"duplicate": bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), "case": bytes.Replace(raw, []byte(`"version":1`), []byte(`"Version":1`), 1), "null": bytes.Replace(raw, []byte(`"until_unix_nano":0`), []byte(`"until_unix_nano":null`), 1), "trailing": append(raw, ' '), "oversize": bytes.Repeat([]byte{'x'}, MaxMessageBytes+1), "fraction": bytes.Replace(raw, []byte(`"sequence":1`), []byte(`"sequence":1.0`), 1), "unknown": bytes.Replace(raw, []byte(`"closed"`), []byte(`"open"`), 1)} {
		t.Run(name, func(t *testing.T) {
			if _, e := decodeMessage(b); e == nil {
				t.Fatal("invalid frame")
			}
		})
	}
}
