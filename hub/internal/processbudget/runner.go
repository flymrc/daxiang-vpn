// Package processbudget bounds only locally owned subprocesses. It cannot
// cancel or prove completion of a command already dispatched through SSH.
package processbudget

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

type Kind string

const (
	Cancelled   Kind = "request_cancelled"
	Busy        Kind = "process_budget_exhausted"
	Invalid     Kind = "invalid_process_spec"
	Unavailable Kind = "process_start_failed"
	TimedOut    Kind = "process_deadline"
	OutputLimit Kind = "process_output_limit"
	Failed      Kind = "process_failed"
	Unknown     Kind = "process_supervision_unknown"
)

type Failure struct {
	Kind    Kind
	Started bool
}

func (e *Failure) Error() string { return "legacy control: " + string(e.Kind) }
func BeforeStart(err error) bool { var e *Failure; return errors.As(err, &e) && !e.Started }
func Reject(kind Kind) error     { return &Failure{Kind: kind} }

type Spec struct {
	Executable  string
	Args        []string
	Timeout     time.Duration
	OutputLimit int
}
type Result struct {
	Stdout  []byte
	Started bool
	Kind    Kind
}
type Runner struct{ slots chan struct{} }
type ownedReport struct {
	started, verified bool
	err               error
}

const cleanupGrace = 500 * time.Millisecond

// The monitor owns native handles until reaping finishes. Returning Unknown
// does not abandon it or release the quarantined Runner seat.
func awaitOwned(ctx context.Context, done <-chan ownedReport, cancel func()) (bool, bool, error) {
	select {
	case report := <-done:
		return report.started, report.verified, report.err
	case <-ctx.Done():
		cancel()
	}
	timer := time.NewTimer(cleanupGrace)
	defer timer.Stop()
	select {
	case report := <-done:
		return report.started, report.verified, report.err
	case <-timer.C:
		return true, false, errors.New("cleanup pending")
	}
}

func New(concurrency int) *Runner {
	if concurrency < 1 || concurrency > 32 {
		panic("invalid internal process concurrency")
	}
	return &Runner{slots: make(chan struct{}, concurrency)}
}

// Run admits without a queue. A seat is released only after local supervision
// confirms cleanup. Lost supervision quarantines that seat for this process.
func (r *Runner) Run(ctx context.Context, spec Spec) (Result, error) {
	if ctx == nil || spec.Executable == "" || spec.Timeout < time.Millisecond || spec.Timeout > 30*time.Second || spec.OutputLimit < 1 || spec.OutputLimit > 64<<10 || len(spec.Args) > 64 {
		return Result{Kind: Invalid}, Reject(Invalid)
	}
	for _, arg := range spec.Args {
		if len(arg) > 16<<10 {
			return Result{Kind: Invalid}, Reject(Invalid)
		}
	}
	if ctx.Err() != nil {
		return Result{Kind: Cancelled}, Reject(Cancelled)
	}
	bounded, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	select {
	case r.slots <- struct{}{}:
	default:
		return Result{Kind: Busy}, Reject(Busy)
	}
	cleaned := true
	defer func() {
		if cleaned {
			<-r.slots
		}
	}()
	if bounded.Err() != nil {
		return Result{Kind: Cancelled}, Reject(Cancelled)
	}
	path, err := exec.LookPath(spec.Executable)
	if err != nil {
		return Result{Kind: Unavailable}, Reject(Unavailable)
	}
	output := &capture{limit: spec.OutputLimit, cancel: cancel}
	started, verified, runErr := runOwned(bounded, path, spec.Args, output, spec.Timeout)
	cleaned = verified
	result := Result{Started: started}
	switch {
	case !verified:
		result.Kind = Unknown
	case output.exceeded():
		result.Kind = OutputLimit
	case bounded.Err() != nil:
		if ctx.Err() != nil {
			result.Kind = Cancelled
		} else {
			result.Kind = TimedOut
		}
	case runErr != nil:
		if started {
			result.Kind = Failed
		} else {
			result.Kind = Unavailable
		}
	default:
		result.Stdout = output.bytes()
		return result, nil
	}
	return result, &Failure{Kind: result.Kind, Started: started}
}

type capture struct {
	mu           sync.Mutex
	stdout       bytes.Buffer
	count, limit int
	overflow     bool
	cancel       context.CancelFunc
}
type stream struct {
	c      *capture
	stdout bool
}

func (w stream) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	remaining := w.c.limit - w.c.count
	if remaining < 0 {
		remaining = 0
	}
	n := len(p)
	if n > remaining {
		n = remaining
	}
	w.c.count += n
	if w.stdout {
		_, _ = w.c.stdout.Write(p[:n])
	}
	if n < len(p) {
		w.c.overflow = true
		w.c.cancel()
	}
	w.c.mu.Unlock()
	return len(p), nil
}
func (c *capture) exceeded() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.overflow }
func (c *capture) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.stdout.Bytes()...)
}
