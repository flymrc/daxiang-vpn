package proxy

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"zongheng-vpn/shared/paths"
)

// The disk-operation hook synchronously holds the actual typed Store Append.
// It proves control/cancellation do not depend on that I/O completing; it does
// not manufacture an operating-system disk stall or a filesystem latency SLA.
func engineBlockedLogHelper(home paths.Context, mode string) int {
	outerFinished := make(chan struct{})
	defer close(outerFinished)
	capture, err := suppressDedicatedOutput()
	if err != nil {
		return 2
	}
	log, err := OpenEngineLog(home, EngineBuildInfo{})
	if err != nil {
		return 3
	}
	log.ops.write = func(*os.File, []byte) (int, error) { select {} }
	recorder := newEngineLogRecorder(log)
	capture.startLog(recorder)
	var once sync.Once
	finalize := func() { once.Do(func() { capture.finish(recorder); recorder.close() }) }
	defer finalize()
	_ = recorder.append(EngineLogEvent{Event: EngineLogStarting})
	hooks := &actionHooks{}
	hooks.failStop.Store(mode == "restore-refused")
	if runEngineWithLog(home, func() { os.Exit(77) }, hooks, recorder, outerFinished, finalize) != nil {
		return 4
	}
	return 0
}

func TestEngineLogHeldWriteQueuesLifecycleAndOverflowIsSticky(t *testing.T) {
	home, _, _ := syntheticRuntime(t)
	log, err := OpenEngineLog(home, EngineBuildInfo{})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	log.ops.write = func(file *os.File, data []byte) (int, error) {
		once.Do(func() { close(entered); <-release })
		return file.Write(data)
	}
	recorder := newEngineLogRecorder(log)
	defer recorder.close()
	if recorder.append(EngineLogEvent{Event: EngineLogStarting}) != nil {
		t.Fatal("initial queue refused")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("owned Store write hook not reached")
	}
	if state := log.State(); state.State != "unknown" || state.Code != "" {
		t.Fatal("held I/O falsely reported healthy or blocked State")
	}
	for _, event := range []EngineLogEventKind{EngineLogControlBound, EngineLogInitialized, EngineLogReady, EngineLogStopped} {
		if recorder.append(EngineLogEvent{Event: event}) != nil {
			t.Fatal("unknown in-flight I/O discarded lifecycle event")
		}
	}
	close(release)
	recorder.close()
	var seen = map[EngineLogEventKind]bool{}
	for _, record := range readEngineEvents(t, home) {
		seen[record.Event] = true
	}
	for _, event := range []EngineLogEventKind{EngineLogStarting, EngineLogControlBound, EngineLogInitialized, EngineLogReady, EngineLogStopped} {
		if !seen[event] {
			t.Fatal("queued lifecycle record disappeared during ordinary held write")
		}
	}

	// Separate owner/log namespace for an actual full finite queue.
	home2, _, _ := syntheticRuntime(t)
	log2, err := OpenEngineLog(home2, EngineBuildInfo{})
	if err != nil {
		t.Fatal(err)
	}
	entered2, release2 := make(chan struct{}), make(chan struct{})
	log2.ops.write = func(file *os.File, data []byte) (int, error) { close(entered2); <-release2; return file.Write(data) }
	recorder2 := newEngineLogRecorder(log2)
	defer recorder2.close()
	_ = recorder2.append(EngineLogEvent{Event: EngineLogStarting})
	select {
	case <-entered2:
	case <-time.After(time.Second):
		t.Fatal("queue overflow fixture did not reach Store")
	}
	for i := 0; i < engineLogQueueCapacity; i++ {
		if recorder2.append(EngineLogEvent{Event: EngineLogInitialized}) != nil {
			t.Fatal("finite queue rejected below capacity")
		}
	}
	if recorder2.append(EngineLogEvent{Event: EngineLogReady}) == nil {
		t.Fatal("full queue did not report sticky degradation")
	}
	if state := log2.State(); state.State != "degraded" || state.Code != EngineLogCodeQueueOverflow {
		t.Fatal("full queue health did not remain queryable")
	}
	close(release2)
	recorder2.close()
	if log2.State().Code != EngineLogCodeQueueOverflow {
		t.Fatal("later cleanup erased queue overflow")
	}
}

func TestDedicatedBlockedLogStillReadyAndStopExitBudgetCoversOuterCleanup(t *testing.T) {
	for _, mode := range []string{"stop", "expiry", "restore-refused"} {
		t.Run(mode, func(t *testing.T) {
			home, cfg, _ := syntheticRuntime(t)
			timeout := 5 * time.Second
			if _, err := prepareLaunchWithTimeout(home, cfg, timeout); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "--engine-blocked-log-helper", home.Root, mode)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			wait := make(chan error, 1)
			go func() { wait <- cmd.Wait() }()
			defer func() {
				_ = cmd.Process.Kill()
				select {
				case <-wait:
				default:
				}
			}()
			var status EngineStatus
			if mode != "expiry" {
				status = waitReady(t, home)
			} else {
				deadline := time.Now().Add(4 * time.Second)
				for time.Now().Before(deadline) {
					status, _ = Inspect(home)
					if status.State == "starting" {
						break
					}
					time.Sleep(time.Millisecond)
				}
				if status.State != "starting" {
					t.Fatal("expiry fixture never published its starting control")
				}
			}
			healthDeadline := time.Now().Add(2 * time.Second)
			unknown := false
			for time.Now().Before(healthDeadline) {
				health, err := InspectEngineLog(home, status.Identity)
				if err != nil {
					t.Fatal("blocked writer prevented bounded authenticated log-health query")
				}
				if health.State == "unknown" {
					unknown = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !unknown {
				t.Fatal("blocked writer never reached observable in-flight Store I/O")
			}
			record, err := loadRecord(home)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "restore-refused" {
				if _, err := requestControl(record, "stop"); err == nil {
					t.Fatal("blocked logging bypassed failed proxy restoration")
				}
				select {
				case <-wait:
					t.Fatal("restore refusal improperly started dedicated exit budget")
				case <-time.After(3200 * time.Millisecond):
				}
				if live, err := requestControl(record, "status"); err != nil || live.State != "ready" {
					t.Fatal("restore refusal lost the engine/control lifetime")
				}
				return
			}
			if mode == "stop" {
				if _, err := requestControl(record, "stop"); err != nil {
					t.Fatal("blocked disk I/O prevented stop response")
				}
			} else if mode == "expiry" {
				// Observe the real timer transition rather than invoking stop or
				// treating an already-stopped/failed cold launch as expiry proof.
				// The product timer anchors one remaining wall duration. Anchor
				// that same remaining lease + 1s observation budget to this
				// sample's monotonic clock; later wall adjustment must not shorten
				// the observer while the real timer is still pending.
				observationStarted := time.Now()
				leaseDeadline := time.Unix(0, record.StartDeadlineUnixNano)
				remaining := leaseDeadline.Sub(observationStarted)
				deadline := observationStarted.Add(remaining + time.Second)
				lastSignedPhase := status.State
				observations := 0
				failObservation := func(reason string) {
					t.Helper()
					t.Fatalf("%s: monotonic_observed_ms=%d initial_remaining_ms=%d wall_lease_remaining_ms=%d last_signed_phase=%q samples=%d", reason, time.Since(observationStarted).Milliseconds(), remaining.Milliseconds(), time.Until(leaseDeadline).Milliseconds(), lastSignedPhase, observations)
				}
				stopping := false
				for time.Now().Before(deadline) {
					live, err := requestControl(record, "status")
					if err != nil {
						failObservation("expiry fixture lost authenticated control before observing its timer")
					}
					observations++
					lastSignedPhase = live.State
					if live.State == "stopping" {
						stopping = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !stopping {
					failObservation("actual startup lease timer did not cancel the blocked logger child")
				}
			}
			// Logging cannot release the instance gate merely because its wait
			// budget ended; authorized cancellation still owns it until exit.
			time.Sleep(900 * time.Millisecond)
			if lock, err := acquireRuntimeLock(home, "engine", 0); !errors.Is(err, errLockBusy) {
				if lock != nil {
					releaseRuntimeLock(lock)
				}
				t.Fatal("unfinished owned logger released the engine lifetime early")
			}
			select {
			case err := <-wait:
				var exited *exec.ExitError
				if !errors.As(err, &exited) || exited.ExitCode() != 77 {
					t.Fatal("authorized dedicated exit budget failed to cover blocked outer cleanup")
				}
			case <-time.After(4 * time.Second):
				t.Fatal("blocked logger left an orphaned dedicated child")
			}
			if status, err := Inspect(home); err != nil || status.State != "stopped" {
				t.Fatal("dead owned child could not reconcile its stale metadata")
			}
		})
	}
}
