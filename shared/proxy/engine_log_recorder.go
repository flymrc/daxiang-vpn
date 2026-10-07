package proxy

import (
	"sync"
	"time"
)

const engineLogQueueCapacity = 64

// One finite queue and one writer own all disk append/close work. Lifecycle
// threads never wait for disk I/O, spawn replacement workers, or widen the
// queue. Sticky health snapshots remain independent of the store I/O mutex.
type engineLogRecorder struct {
	log        *EngineLog
	queue      chan EngineLogEvent
	stop, done chan struct{}
	mu         sync.Mutex
	closed     bool
	identity   EngineIdentity
}

func (r *engineLogRecorder) bindIdentity(identity EngineIdentity) {
	r.mu.Lock()
	r.identity = identity
	r.mu.Unlock()
}

func newEngineLogRecorder(log *EngineLog) *engineLogRecorder {
	r := &engineLogRecorder{log: log, queue: make(chan EngineLogEvent, engineLogQueueCapacity), stop: make(chan struct{}), done: make(chan struct{})}
	go r.run()
	return r
}

func (r *engineLogRecorder) append(event EngineLogEvent) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return safeEngineFailure(EngineLogCodeClosed)
	}
	if event.InstanceID == "" && r.identity.InstanceID != "" {
		event.InstanceID, event.Generation = r.identity.InstanceID, r.identity.Generation
	}
	if state := r.log.State(); state.State == "degraded" {
		return safeEngineFailure(state.Code)
	}
	select {
	case r.queue <- event:
		return nil
	default:
		r.log.MarkDegraded(EngineLogCodeQueueOverflow)
		return safeEngineFailure(EngineLogCodeQueueOverflow)
	}
}

func (r *engineLogRecorder) run() {
	defer close(r.done)
	for {
		select {
		case event := <-r.queue:
			_ = r.log.Append(event)
		case <-r.stop:
			for {
				select {
				case event := <-r.queue:
					_ = r.log.Append(event)
				default:
					_ = r.log.Close()
					return
				}
			}
		}
	}
}

// A timeout is unknown cleanup, not a claim that bytes were flushed or held
// handles released. Only this already-existing writer may remain blocked.
func (r *engineLogRecorder) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.stop)
	}
	r.mu.Unlock()
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-r.done:
	case <-timer.C:
		r.log.MarkDegraded(EngineLogCodeShutdown)
	}
}
