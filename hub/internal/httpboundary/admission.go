package httpboundary

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// TokenBudget refills one token per Every, up to Burst. Integer periods avoid
// accepting NaN/Inf rates or overflowing a floating point configuration.
type TokenBudget struct {
	Every time.Duration
	Burst int
}

type AdmissionConfig struct {
	MaxConcurrent          int
	MaxConcurrentPerSource int
	MaxSources             int
	Global                 TokenBudget
	PerSource              TokenBudget
	SourceIdleTTL          time.Duration
	BusyRetryAfter         time.Duration
}

// DefaultAdmissionConfig is a local candidate budget, not measured production
// capacity. Share one Admission across the explicitly selected costly routes.
func DefaultAdmissionConfig() AdmissionConfig {
	return AdmissionConfig{
		MaxConcurrent: 8, MaxConcurrentPerSource: 2, MaxSources: 1024,
		Global:        TokenBudget{Every: 50 * time.Millisecond, Burst: 40},
		PerSource:     TokenBudget{Every: time.Second, Burst: 4},
		SourceIdleTTL: 10 * time.Minute, BusyRetryAfter: time.Second,
	}
}

var ErrAdmissionConfig = errors.New("httpboundary: invalid admission configuration")

type admissionTokens struct {
	tokens float64
	last   time.Time
}

func (t *admissionTokens) refill(now time.Time, budget TokenBudget) {
	if elapsed := now.Sub(t.last); elapsed > 0 {
		t.tokens = math.Min(float64(budget.Burst), t.tokens+float64(elapsed)/float64(budget.Every))
		t.last = now
	}
}

func (t *admissionTokens) retry(budget TokenBudget) time.Duration {
	if t.tokens >= 1 {
		return 0
	}
	return time.Duration(math.Ceil((1 - t.tokens) * float64(budget.Every)))
}

type admissionSource struct {
	rate     admissionTokens
	active   int
	lastSeen time.Time
}

// Admission is a per-process pre-authentication resource boundary. A source IP
// is never a device identity or token authority. The zero value is not usable;
// use NewAdmission and share its pointer across all intended costly routes.
// There is no waiting queue, background goroutine, or handler timeout here.
type Admission struct {
	mu          sync.Mutex
	config      AdmissionConfig
	initialized bool
	active      int
	global      admissionTokens
	sources     map[string]*admissionSource
	nextSweep   time.Time
	now         func() time.Time
}

func NewAdmission(config AdmissionConfig) (*Admission, error) {
	validRate := func(rate TokenBudget, maxBurst int) bool {
		return rate.Every >= time.Millisecond && rate.Every <= time.Hour && rate.Burst >= 1 && rate.Burst <= maxBurst
	}
	if config.MaxConcurrent < 1 || config.MaxConcurrent > 1024 || config.MaxConcurrentPerSource < 1 || config.MaxConcurrentPerSource > config.MaxConcurrent || config.MaxSources < 1 || config.MaxSources > 4096 || !validRate(config.Global, 65536) || !validRate(config.PerSource, 1024) || config.SourceIdleTTL < time.Second || config.SourceIdleTTL > 24*time.Hour || config.BusyRetryAfter < time.Second || config.BusyRetryAfter > time.Minute || config.BusyRetryAfter%time.Second != 0 {
		return nil, ErrAdmissionConfig
	}
	// The validated multiplication is at most 1024 hours and cannot overflow.
	// Expiry cannot reset a source before its whole burst could have refilled.
	if config.SourceIdleTTL < time.Duration(config.PerSource.Burst)*config.PerSource.Every {
		return nil, ErrAdmissionConfig
	}
	now := time.Now()
	return &Admission{
		config: config, initialized: true, global: admissionTokens{tokens: float64(config.Global.Burst), last: now},
		sources: make(map[string]*admissionSource), now: time.Now,
	}, nil
}

type admissionDenial struct {
	status int
	code   string
	retry  time.Duration
}

func admissionCancelled() *admissionDenial {
	return &admissionDenial{status: http.StatusServiceUnavailable, code: "request_cancelled"}
}

func (a *Admission) sweep(now time.Time) {
	if now.Before(a.nextSweep) {
		return
	}
	// An occasional fixed-capacity scan bounds cleanup work under high-cardinality
	// rejected sources. Never evict an active lease or reset rate debt.
	a.nextSweep = now.Add(time.Second)
	for source, bucket := range a.sources {
		if bucket.active != 0 || now.Sub(bucket.lastSeen) < a.config.SourceIdleTTL {
			continue
		}
		bucket.rate.refill(now, a.config.PerSource)
		if bucket.rate.tokens >= float64(a.config.PerSource.Burst) {
			delete(a.sources, source)
		}
	}
}

type admissionLease struct {
	owner  *Admission
	bucket *admissionSource
	once   sync.Once
}

func (l *admissionLease) release() {
	l.once.Do(func() {
		l.owner.mu.Lock()
		defer l.owner.mu.Unlock()
		l.owner.active--
		l.bucket.active--
		// Release does not refund rate tokens, move lastSeen, or look up a slot
		// that might have belonged to another source after eviction.
	})
}

func (a *Admission) acquire(ctx context.Context, source string) (*admissionLease, *admissionDenial) {
	if ctx.Err() != nil {
		return nil, admissionCancelled()
	}
	ip, err := parseAddress(source)
	if err != nil || ip.String() != source {
		return nil, &admissionDenial{status: http.StatusBadRequest, code: "bad_request"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ctx.Err() != nil {
		return nil, admissionCancelled()
	}
	now := a.now()
	a.global.refill(now, a.config.Global)
	if a.active >= a.config.MaxConcurrent {
		return nil, &admissionDenial{status: http.StatusServiceUnavailable, code: "resource_exhausted", retry: a.config.BusyRetryAfter}
	}
	bucket := a.sources[source]
	if bucket != nil && bucket.active >= a.config.MaxConcurrentPerSource {
		return nil, &admissionDenial{status: http.StatusServiceUnavailable, code: "resource_exhausted", retry: a.config.BusyRetryAfter}
	}
	retry := a.global.retry(a.config.Global)
	if bucket != nil {
		bucket.rate.refill(now, a.config.PerSource)
		if sourceRetry := bucket.rate.retry(a.config.PerSource); sourceRetry > retry {
			retry = sourceRetry
		}
	}
	if retry > 0 {
		return nil, &admissionDenial{status: http.StatusTooManyRequests, code: "rate_limited", retry: retry}
	}
	if bucket == nil {
		if len(a.sources) >= a.config.MaxSources {
			a.sweep(now)
			if len(a.sources) >= a.config.MaxSources {
				return nil, &admissionDenial{status: http.StatusServiceUnavailable, code: "resource_exhausted", retry: a.config.BusyRetryAfter}
			}
		}
		bucket = &admissionSource{rate: admissionTokens{tokens: float64(a.config.PerSource.Burst), last: now}, lastSeen: now}
	}
	// Cancellation observed before reservation charges no tokens or map slot.
	if ctx.Err() != nil {
		return nil, admissionCancelled()
	}
	a.sources[source] = bucket
	a.global.tokens--
	bucket.rate.tokens--
	a.active++
	bucket.active++
	if now.After(bucket.lastSeen) {
		bucket.lastSeen = now
	}
	return &admissionLease{owner: a, bucket: bucket}, nil
}

func writeAdmissionDenial(w http.ResponseWriter, denial *admissionDenial) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if denial.retry > 0 {
		seconds := int64(denial.retry / time.Second)
		if denial.retry%time.Second != 0 {
			seconds++
		}
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
	w.WriteHeader(denial.status)
	_, _ = fmt.Fprintf(w, "{\"error\":\"%s\"}\n", denial.code)
}

// Middleware reserves global and canonical-source budgets before the handler
// reads credentials or begins mutation. Invalid policies fail at route setup.
// Existing context source bindings must agree with this fixed listener policy.
// A cancelled request observed before reservation consumes no quota. A token
// reserved before a later cancellation remains spent; only concurrency releases.
// The handler must honor its context and impose its own execution deadline.
// A permit remains occupied until the handler returns, even if context expires,
// so ignored cancellation cannot create extra concurrent mutations. Panic also
// releases the permit and is rethrown for the usual net/http recovery behavior.
func (a *Admission) Middleware(handler http.Handler, policy SourcePolicy) http.Handler {
	if a == nil || !a.initialized || handler == nil || (policy != Direct && policy != LoopbackProxy) {
		panic("httpboundary: invalid admission middleware configuration")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			writeAdmissionDenial(w, admissionCancelled())
			return
		}
		verified, err := RequestWithSource(r, policy)
		if err != nil || (SourceFromContext(r) != "" && SourceFromContext(r) != SourceFromContext(verified)) {
			writeAdmissionDenial(w, &admissionDenial{status: http.StatusBadRequest, code: "bad_request"})
			return
		}
		lease, denial := a.acquire(verified.Context(), SourceFromContext(verified))
		if denial != nil {
			writeAdmissionDenial(w, denial)
			return
		}
		defer lease.release()
		if verified.Context().Err() != nil {
			writeAdmissionDenial(w, admissionCancelled())
			return
		}
		handler.ServeHTTP(w, verified)
	})
}
