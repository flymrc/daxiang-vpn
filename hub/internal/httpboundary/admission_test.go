package httpboundary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type admissionTestClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *admissionTestClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *admissionTestClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func admissionTestConfig() AdmissionConfig {
	c := DefaultAdmissionConfig()
	c.Global = TokenBudget{Every: time.Millisecond, Burst: 1000}
	c.PerSource = TokenBudget{Every: time.Millisecond, Burst: 100}
	c.MaxSources = 16
	c.SourceIdleTTL = time.Second
	return c
}

func testAdmission(t *testing.T, config AdmissionConfig) (*Admission, *admissionTestClock) {
	t.Helper()
	a, err := NewAdmission(config)
	if err != nil {
		t.Fatal(err)
	}
	c := &admissionTestClock{t: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)}
	a.now, a.global.last = c.now, c.now()
	return a, c
}

func admissionRequest(source string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/explicit-costly-route", nil)
	r.RemoteAddr = source
	return r
}

func admissionServe(handler http.Handler, source string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, admissionRequest(source))
	return w
}

func admissionError(t *testing.T, response *httptest.ResponseRecorder, status int, code, retry string) {
	t.Helper()
	var body map[string]string
	if response.Code != status || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body) != 1 || body["error"] != code || response.Header().Get("Retry-After") != retry || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected admission refusal: status=%d body=%q retry=%q", response.Code, response.Body.String(), response.Header().Get("Retry-After"))
	}
}

func TestAdmissionConfigBoundsAndCopy(t *testing.T) {
	if _, err := NewAdmission(DefaultAdmissionConfig()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*AdmissionConfig)
	}{
		{"zero-concurrency", func(c *AdmissionConfig) { c.MaxConcurrent = 0 }},
		{"huge-concurrency", func(c *AdmissionConfig) { c.MaxConcurrent = 1025 }},
		{"negative-source-concurrency", func(c *AdmissionConfig) { c.MaxConcurrentPerSource = -1 }},
		{"source-over-global", func(c *AdmissionConfig) { c.MaxConcurrentPerSource = c.MaxConcurrent + 1 }},
		{"zero-sources", func(c *AdmissionConfig) { c.MaxSources = 0 }},
		{"huge-sources", func(c *AdmissionConfig) { c.MaxSources = 4097 }},
		{"zero-global-rate", func(c *AdmissionConfig) { c.Global.Every = 0 }},
		{"negative-rate", func(c *AdmissionConfig) { c.Global.Every = -time.Second }},
		{"too-fast-rate", func(c *AdmissionConfig) { c.PerSource.Every = time.Nanosecond }},
		{"duration-extreme", func(c *AdmissionConfig) { c.Global.Every = time.Duration(1<<63 - 1) }},
		{"zero-global-burst", func(c *AdmissionConfig) { c.Global.Burst = 0 }},
		{"huge-global-burst", func(c *AdmissionConfig) { c.Global.Burst = 65537 }},
		{"negative-source-burst", func(c *AdmissionConfig) { c.PerSource.Burst = -1 }},
		{"integer-extreme", func(c *AdmissionConfig) { c.PerSource.Burst = int(^uint(0) >> 1) }},
		{"short-expiry", func(c *AdmissionConfig) { c.SourceIdleTTL = time.Millisecond }},
		{"long-expiry", func(c *AdmissionConfig) { c.SourceIdleTTL = 25 * time.Hour }},
		{"debt-before-expiry", func(c *AdmissionConfig) { c.SourceIdleTTL = time.Second }},
		{"short-busy-hint", func(c *AdmissionConfig) { c.BusyRetryAfter = time.Millisecond }},
		{"fractional-busy-hint", func(c *AdmissionConfig) { c.BusyRetryAfter = 1500 * time.Millisecond }},
		{"long-busy-hint", func(c *AdmissionConfig) { c.BusyRetryAfter = 61 * time.Second }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := DefaultAdmissionConfig()
			test.change(&c)
			if a, err := NewAdmission(c); a != nil || !errors.Is(err, ErrAdmissionConfig) {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	c := DefaultAdmissionConfig()
	a, err := NewAdmission(c)
	if err != nil {
		t.Fatal(err)
	}
	c.MaxSources, c.Global.Burst = 0, 0
	if a.config.MaxSources != 1024 || a.config.Global.Burst != 40 {
		t.Fatal("caller mutated admission configuration")
	}
	for _, build := range []func(){
		func() { (*Admission)(nil).Middleware(http.NotFoundHandler(), Direct) },
		func() { (&Admission{}).Middleware(http.NotFoundHandler(), Direct) },
		func() { a.Middleware(nil, Direct) },
		func() { a.Middleware(http.NotFoundHandler(), SourcePolicy(99)) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid middleware setup accepted")
				}
			}()
			build()
		}()
	}
}

func TestAdmissionAllSelectedRoutesShareQuotaAndPreserveContext(t *testing.T) {
	c := admissionTestConfig()
	c.PerSource = TokenBudget{Every: time.Second, Burst: 4}
	c.SourceIdleTTL = 10 * time.Second
	a, _ := testAdmission(t, c)
	var mutations atomic.Int64
	deadline := time.Now().Add(time.Minute)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if SourceFromContext(r) != "198.51.100.7" {
			t.Error("canonical source missing")
		}
		if d, ok := r.Context().Deadline(); !ok || !d.Equal(deadline) {
			t.Error("incoming execution deadline changed")
		}
		mutations.Add(1)
		w.Header().Set("Existing-Contract", "preserved")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"existing":"body"}`)
	})
	// Four mutation routes and an explicitly expensive GET use the same pointer.
	routes := []http.Handler{a.Middleware(inner, Direct), a.Middleware(inner, LoopbackProxy), a.Middleware(inner, Direct), a.Middleware(inner, LoopbackProxy), a.Middleware(inner, Direct)}
	for i, h := range routes {
		r := admissionRequest("198.51.100.7:10")
		if i%2 == 1 {
			r.RemoteAddr = "127.0.0.1:20"
			r.Header.Set("X-Forwarded-For", "::ffff:198.51.100.7")
		}
		if i == 4 {
			r.Method = http.MethodGet
			r.URL.Path = "/admin/api/egress/example/exit-ip"
		}
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		r = r.WithContext(ctx)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		cancel()
		if i < 4 {
			if w.Code != http.StatusCreated || w.Header().Get("Existing-Contract") != "preserved" || w.Body.String() != `{"existing":"body"}` {
				t.Fatal("normal handler behavior changed")
			}
		} else {
			admissionError(t, w, 429, "rate_limited", "1")
		}
	}
	if mutations.Load() != 4 || len(a.sources) != 1 || a.active != 0 {
		t.Fatal("routes did not share one canonical source budget")
	}
	// A caller intentionally leaves its inexpensive health handler outside the pool.
	plain := httptest.NewRecorder()
	inner.ServeHTTP(plain, mustAdmissionContext(t, admissionRequest("198.51.100.7:10"), deadline))
	if plain.Code != 201 || mutations.Load() != 5 {
		t.Fatal("nonwrapped handler was affected")
	}
}

func mustAdmissionContext(t *testing.T, r *http.Request, deadline time.Time) *http.Request {
	t.Helper()
	r, err := RequestWithSource(r, Direct)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	t.Cleanup(cancel)
	return r.WithContext(ctx)
}

func TestAdmissionGlobalRateRefillAndNoRejectedAllocation(t *testing.T) {
	c := admissionTestConfig()
	c.Global = TokenBudget{Every: 250 * time.Millisecond, Burst: 2}
	a, clock := testAdmission(t, c)
	var calls atomic.Int64
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(204) }), Direct)
	for _, source := range []string{"198.51.100.1:1", "198.51.100.2:2"} {
		if admissionServe(h, source).Code != 204 {
			t.Fatal("initial global burst unavailable")
		}
	}
	admissionError(t, admissionServe(h, "198.51.100.3:3"), 429, "rate_limited", "1")
	clock.advance(125 * time.Millisecond)
	admissionError(t, admissionServe(h, "198.51.100.3:3"), 429, "rate_limited", "1")
	if len(a.sources) != 2 || calls.Load() != 2 || a.global.tokens != 0.5 {
		t.Fatal("rejected request allocated or consumed quota")
	}
	clock.advance(125 * time.Millisecond)
	if admissionServe(h, "198.51.100.3:3").Code != 204 || calls.Load() != 3 {
		t.Fatal("global refill did not recover admission")
	}
}

func TestAdmissionSourceRateRoundingAndNoRefundOnRelease(t *testing.T) {
	c := admissionTestConfig()
	c.PerSource = TokenBudget{Every: 1500 * time.Millisecond, Burst: 1}
	c.SourceIdleTTL = 3 * time.Second
	a, clock := testAdmission(t, c)
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), Direct)
	if admissionServe(h, "198.51.100.1:1").Code != 204 {
		t.Fatal("source burst unavailable")
	}
	admissionError(t, admissionServe(h, "[::ffff:198.51.100.1]:2"), 429, "rate_limited", "2")
	if a.active != 0 || a.global.tokens != 999 || a.sources["198.51.100.1"].rate.tokens != 0 {
		t.Fatal("release/rejection refunded or consumed tokens")
	}
	clock.advance(time.Second)
	admissionError(t, admissionServe(h, "198.51.100.1:3"), 429, "rate_limited", "1")
	clock.advance(500 * time.Millisecond)
	if admissionServe(h, "198.51.100.1:4").Code != 204 {
		t.Fatal("source refill did not recover")
	}
}

func TestAdmissionConcurrentGlobalAndSourceLimitsRejectWithoutQueue(t *testing.T) {
	c := admissionTestConfig()
	c.MaxConcurrent = 4
	c.MaxConcurrentPerSource = 1
	c.MaxSources = 8
	a, _ := testAdmission(t, c)
	gate := make(chan struct{})
	entered := make(chan string, 8)
	done := make(chan *httptest.ResponseRecorder, 8)
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	var mutations atomic.Int64
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutations.Add(1)
		entered <- SourceFromContext(r)
		<-gate
		w.WriteHeader(204)
	}), Direct)
	for i := 1; i <= 4; i++ {
		source := fmt.Sprintf("198.51.100.%d:%d", i, i)
		go func() { done <- admissionServe(h, source) }()
		awaitAdmission(t, entered)
	}
	for _, source := range []string{"198.51.100.1:9", "198.51.100.9:9"} {
		response := make(chan *httptest.ResponseRecorder, 1)
		go func() { response <- admissionServe(h, source) }()
		admissionError(t, awaitAdmission(t, response), 503, "resource_exhausted", "1")
	}
	if mutations.Load() != 4 || len(a.sources) != 4 {
		t.Fatal("full request entered mutation or allocated new source")
	}
	release()
	for i := 0; i < 4; i++ {
		if awaitAdmission(t, done).Code != 204 {
			t.Fatal("admitted worker failed")
		}
	}
	if a.active != 0 {
		t.Fatal("completed workers leaked permits")
	}
	// Isolate a source limit with remaining global capacity.
	a, _ = testAdmission(t, c)
	gate = make(chan struct{})
	entered = make(chan string, 8)
	done = make(chan *httptest.ResponseRecorder, 8)
	var secondOnce sync.Once
	secondRelease := func() { secondOnce.Do(func() { close(gate) }) }
	t.Cleanup(secondRelease)
	h = a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- SourceFromContext(r)
		<-gate
		w.WriteHeader(204)
	}), Direct)
	go func() { done <- admissionServe(h, "198.51.100.1:1") }()
	awaitAdmission(t, entered)
	admissionError(t, admissionServe(h, "198.51.100.1:2"), 503, "resource_exhausted", "1")
	go func() { done <- admissionServe(h, "198.51.100.2:2") }()
	awaitAdmission(t, entered)
	secondRelease()
	awaitAdmission(t, done)
	awaitAdmission(t, done)
	if a.active != 0 || len(a.sources) != 2 {
		t.Fatal("source limit blocked independent source or leaked permit")
	}
}

func awaitAdmission[T any](t *testing.T, c <-chan T) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("admission queued or worker failed to complete")
		var zero T
		return zero
	}
}

func TestAdmissionCancellationAndPanicRelease(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint("pre-cancelled-", expired), func(t *testing.T) {
			a, _ := testAdmission(t, admissionTestConfig())
			var calls atomic.Int64
			h := a.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }), Direct)
			r := admissionRequest("198.51.100.1:1")
			var ctx context.Context
			var cancel context.CancelFunc
			if expired {
				ctx, cancel = context.WithDeadline(r.Context(), time.Now().Add(-time.Second))
			} else {
				ctx, cancel = context.WithCancel(r.Context())
				cancel()
			}
			defer cancel()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r.WithContext(ctx))
			admissionError(t, w, 503, "request_cancelled", "")
			if calls.Load() != 0 || a.active != 0 || len(a.sources) != 0 || a.global.tokens != 1000 {
				t.Fatal("pre-cancelled request consumed quota")
			}
		})
	}
	t.Run("cancelled-during-reservation", func(t *testing.T) {
		a, clock := testAdmission(t, admissionTestConfig())
		ctx, cancel := context.WithCancel(context.Background())
		a.now = func() time.Time { cancel(); return clock.now() }
		lease, denial := a.acquire(ctx, "198.51.100.1")
		if lease != nil || denial == nil || denial.code != "request_cancelled" || a.active != 0 || len(a.sources) != 0 || a.global.tokens != 1000 {
			t.Fatal("cancelled reservation consumed quota")
		}
	})
	t.Run("cooperative-cancel", func(t *testing.T) {
		c := admissionTestConfig()
		c.MaxConcurrent = 1
		c.MaxConcurrentPerSource = 1
		a, _ := testAdmission(t, c)
		entered := make(chan struct{}, 1)
		done := make(chan *httptest.ResponseRecorder, 1)
		h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entered <- struct{}{}
			<-r.Context().Done()
			w.WriteHeader(204)
		}), Direct)
		ctx, cancel := context.WithCancel(context.Background())
		r := admissionRequest("198.51.100.1:1").WithContext(ctx)
		go func() { w := httptest.NewRecorder(); h.ServeHTTP(w, r); done <- w }()
		awaitAdmission(t, entered)
		cancel()
		awaitAdmission(t, done)
		if a.active != 0 {
			t.Fatal("cancelled worker leaked concurrency")
		}
		if admissionServe(a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), Direct), "198.51.100.2:2").Code != 204 {
			t.Fatal("cancel release did not restore concurrent capacity")
		}
	})
	t.Run("cancel-does-not-release-running-mutation", func(t *testing.T) {
		c := admissionTestConfig()
		c.MaxConcurrent = 1
		c.MaxConcurrentPerSource = 1
		a, _ := testAdmission(t, c)
		entered := make(chan struct{}, 1)
		gate := make(chan struct{})
		done := make(chan struct{}, 1)
		var once sync.Once
		release := func() { once.Do(func() { close(gate) }) }
		t.Cleanup(release)
		h := a.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { entered <- struct{}{}; <-gate }), Direct)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := admissionRequest("198.51.100.1:1").WithContext(ctx)
		go func() { h.ServeHTTP(httptest.NewRecorder(), r); done <- struct{}{} }()
		awaitAdmission(t, entered)
		cancel()
		admissionError(t, admissionServe(h, "198.51.100.2:2"), 503, "resource_exhausted", "1")
		release()
		awaitAdmission(t, done)
		if a.active != 0 {
			t.Fatal("returned handler leaked permit")
		}
	})
	t.Run("panic", func(t *testing.T) {
		a, _ := testAdmission(t, admissionTestConfig())
		h := a.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("synthetic handler panic") }), Direct)
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("handler panic was swallowed")
				}
			}()
			admissionServe(h, "198.51.100.1:1")
		}()
		if a.active != 0 || a.sources["198.51.100.1"].active != 0 {
			t.Fatal("panic leaked permit")
		}
		if admissionServe(a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), Direct), "198.51.100.1:1").Code != 204 {
			t.Fatal("panic poisoned admission")
		}
	})
}

func TestAdmissionHighCardinalityExpiryAndActiveLease(t *testing.T) {
	c := admissionTestConfig()
	c.MaxSources = 2
	a, clock := testAdmission(t, c)
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), Direct)
	for _, source := range []string{"198.51.100.1:1", "198.51.100.2:2"} {
		if admissionServe(h, source).Code != 204 {
			t.Fatal("initial table not usable")
		}
	}
	for i := 0; i < 5000; i++ {
		admissionError(t, admissionServe(h, fmt.Sprintf("[2001:db8::%x]:3", i+1)), 503, "resource_exhausted", "1")
	}
	if len(a.sources) != 2 || a.global.tokens != 998 || a.active != 0 || !a.nextSweep.Equal(clock.now().Add(time.Second)) {
		t.Fatal("high-cardinality refusal expanded table, consumed rate, or repeated sweep")
	}
	clock.advance(time.Second)
	if admissionServe(h, "198.51.100.3:3").Code != 204 || len(a.sources) != 1 {
		t.Fatal("expired idle sources were not cleaned")
	}

	c.MaxSources = 1
	a, clock = testAdmission(t, c)
	lease, denial := a.acquire(context.Background(), "198.51.100.1")
	if denial != nil {
		t.Fatal(denial)
	}
	old := lease.bucket
	oldSeen := old.lastSeen
	clock.advance(2 * time.Second)
	if got, deny := a.acquire(context.Background(), "198.51.100.2"); got != nil || deny == nil || deny.status != 503 || a.sources["198.51.100.1"] != old || old.active != 1 {
		t.Fatal("expired active lease was evicted")
	}
	lease.release()
	if !old.lastSeen.Equal(oldSeen) || old.rate.tokens != 99 {
		t.Fatal("release changed age or inflated rate")
	}
	// A full-table scan is limited to once a second, even after a lease ends.
	if got, deny := a.acquire(context.Background(), "198.51.100.2"); got != nil || deny == nil || deny.status != 503 {
		t.Fatal("full-table cadence did not apply")
	}
	clock.advance(time.Second)
	newLease, deny := a.acquire(context.Background(), "198.51.100.2")
	if deny != nil {
		t.Fatal(deny)
	}
	lease.release()
	if a.active != 1 || newLease.bucket.active != 1 {
		t.Fatal("old release touched reused source slot")
	}
	newLease.release()
}

func TestAdmissionReleaseIsExactlyOnceAndClockDoesNotRefillBackward(t *testing.T) {
	c := admissionTestConfig()
	c.PerSource = TokenBudget{Every: time.Second, Burst: 1}
	c.SourceIdleTTL = 2 * time.Second
	a, clock := testAdmission(t, c)
	lease, denial := a.acquire(context.Background(), "198.51.100.1")
	if denial != nil {
		t.Fatal(denial)
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); lease.release() }()
	}
	wg.Wait()
	if a.active != 0 || lease.bucket.active != 0 || a.global.tokens != 999 || lease.bucket.rate.tokens != 0 {
		t.Fatal("multiple releases changed quota")
	}
	clock.advance(-time.Hour)
	if got, deny := a.acquire(context.Background(), "198.51.100.1"); got != nil || deny == nil || deny.status != 429 || lease.bucket.rate.tokens != 0 || a.global.tokens != 999 {
		t.Fatal("backward clock refilled rate")
	}
	clock.advance(time.Hour + time.Second)
	got, deny := a.acquire(context.Background(), "198.51.100.1")
	if deny != nil {
		t.Fatal("positive elapsed did not refill")
	}
	got.release()
}

type admissionUnreadBody struct{ reads atomic.Int64 }

func (b *admissionUnreadBody) Read([]byte) (int, error) { b.reads.Add(1); return 0, io.EOF }
func (*admissionUnreadBody) Close() error               { return nil }

func TestAdmissionSourceBoundaryAndRefusalBeforeCredentialRead(t *testing.T) {
	a, _ := testAdmission(t, admissionTestConfig())
	var mutations atomic.Int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutations.Add(1)
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(204)
	})
	for _, test := range []struct {
		name, remote, xff string
		policy            SourcePolicy
	}{
		{"bad-source", "SYNTHETIC_SECRET", "", Direct},
		{"bad-proxy-header", "127.0.0.1:1", "SYNTHETIC_SECRET", LoopbackProxy},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &admissionUnreadBody{}
			r := admissionRequest(test.remote)
			r.Body = body
			r.Header.Set("X-Forwarded-For", test.xff)
			w := httptest.NewRecorder()
			a.Middleware(inner, test.policy).ServeHTTP(w, r)
			admissionError(t, w, 400, "bad_request", "")
			if body.reads.Load() != 0 || strings.Contains(w.Body.String(), "SYNTHETIC_SECRET") {
				t.Fatal("refusal read credentials or exposed source")
			}
		})
	}
	if mutations.Load() != 0 || len(a.sources) != 0 {
		t.Fatal("invalid source entered mutation or allocated bucket")
	}
	r := admissionRequest("127.0.0.1:1")
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	bound, err := RequestWithSource(r, LoopbackProxy)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.Middleware(inner, Direct).ServeHTTP(w, bound)
	admissionError(t, w, 400, "bad_request", "")
	for _, source := range []string{"", "::ffff:198.51.100.1", "2001:0DB8::1", "198.51.100.1:1", "fe80::1%scope"} {
		if lease, deny := a.acquire(context.Background(), source); lease != nil || deny == nil || deny.status != 400 {
			t.Fatal("noncanonical source accepted")
		}
	}
	// Compat ignores a forged XFF and binds the real canonical remote address.
	r = admissionRequest("198.51.100.2:2")
	r.Header.Set("X-Forwarded-For", "SYNTHETIC_SECRET")
	body := &admissionUnreadBody{}
	r.Body = body
	w = httptest.NewRecorder()
	a.Middleware(inner, Direct).ServeHTTP(w, r)
	if w.Code != 204 || body.reads.Load() == 0 || mutations.Load() != 1 || a.sources["198.51.100.2"] == nil {
		t.Fatal("fixed source policy rejected normal request")
	}
	// An exhausted pool rejects before body parsing/authentication/mutation.
	c := admissionTestConfig()
	c.Global.Burst = 1
	a, _ = testAdmission(t, c)
	h := a.Middleware(inner, Direct)
	admissionServe(h, "198.51.100.3:3")
	body = &admissionUnreadBody{}
	r = admissionRequest("198.51.100.4:4")
	r.Body = body
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	admissionError(t, w, 429, "rate_limited", "1")
	if body.reads.Load() != 0 || mutations.Load() != 2 {
		t.Fatal("rejected rate request read credentials or mutated")
	}
}
