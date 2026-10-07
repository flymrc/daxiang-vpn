package processbudget

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fixtureCommand = "zhvpn-owned-process-test-v1"
const stderrMarker = "synthetic-stderr-must-not-be-returned"

func TestMain(m *testing.M) {
	if code, handled := SupervisorMain(os.Args[1:]); handled {
		os.Exit(code)
	}
	if len(os.Args) >= 4 && os.Args[1] == fixtureCommand {
		os.Exit(runFixture(os.Args[2], os.Args[3]))
	}
	// Race checking remains enabled in the children; avoid its intentional exit
	// sleep being mistaken for an external command deadline.
	_ = os.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	os.Exit(m.Run())
}

func fixtureSpec(mode, root string, timeout time.Duration) Spec {
	return Spec{Executable: os.Args[0], Args: []string{fixtureCommand, mode, root}, Timeout: timeout, OutputLimit: 4096}
}

func markPID(root, name string) error {
	return os.WriteFile(filepath.Join(root, name+".pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
}

func fixtureHold() {
	// Even a failed cleanup regression leaves no indefinitely running fixture.
	time.Sleep(15 * time.Second)
}

func runFixture(mode, root string) int {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return 90
	}
	switch mode {
	case "success":
		if markPID(root, "success") != nil {
			return 91
		}
		fmt.Fprint(os.Stdout, "owned-fixture-ok")
		fmt.Fprint(os.Stderr, stderrMarker)
		return 0
	case "failed":
		_ = markPID(root, "failed")
		fmt.Fprint(os.Stdout, "discard-on-failure")
		fmt.Fprint(os.Stderr, stderrMarker)
		return 17
	case "stdout-flood", "stderr-flood":
		_ = markPID(root, mode)
		output := os.Stdout
		if mode == "stderr-flood" {
			output = os.Stderr
		}
		for i := 0; i < 256; i++ {
			_, _ = output.Write([]byte(stderrMarker + strings.Repeat("x", 1024)))
		}
		fixtureHold()
		return 0
	case "hold":
		_ = markPID(root, "hold")
		fixtureHold()
		return 0
	case "leaf":
		_ = markPID(root, "leaf")
		fixtureHold()
		return 0
	case "branch":
		_ = markPID(root, "branch")
		cmd := exec.Command(os.Args[0], fixtureCommand, "leaf", root)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if cmd.Start() != nil {
			return 92
		}
		fixtureHold()
		return 0
	case "tree", "detached-tree", "held-pipe":
		_ = markPID(root, "parent")
		cmd := exec.Command(os.Args[0], fixtureCommand, "branch", root)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if mode == "detached-tree" {
			prepareDetachedFixture(cmd)
		}
		if cmd.Start() != nil {
			return 93
		}
		if !waitFile(filepath.Join(root, "leaf.pid"), 4*time.Second) {
			return 94
		}
		if mode == "held-pipe" {
			return 0
		}
		fixtureHold()
		return 0
	case "kill-supervisor", "detached-kill-supervisor":
		_ = markPID(root, "worker")
		if mode == "detached-kill-supervisor" {
			child := exec.Command(os.Args[0], fixtureCommand, "branch", root)
			prepareDetachedFixture(child)
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			if child.Start() != nil || !waitFile(filepath.Join(root, "leaf.pid"), 4*time.Second) {
				return 98
			}
		}
		if !killOwnedFixtureSupervisor() {
			return 95
		}
		fixtureHold()
		return 0
	case "authority":
		_ = markPID(root, "authority")
		_, err := New(1).Run(context.Background(), fixtureSpec("detached-tree", root, 12*time.Second))
		if err != nil {
			return 96
		}
		return 0
	default:
		return 97
	}
}

func waitFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func requireNative(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("owned process supervision is unavailable on this host")
	}
}

func assertFailure(t *testing.T, result Result, err error, started bool, kind Kind) {
	t.Helper()
	var failure *Failure
	if !errors.As(err, &failure) || failure.Started != started || failure.Kind != kind || result.Started != started || result.Kind != kind {
		t.Fatalf("result=%+v err=%v; want started=%t kind=%q", result, err, started, kind)
	}
	if len(result.Stdout) != 0 || strings.Contains(err.Error(), stderrMarker) {
		t.Fatal("failure exposed subprocess output")
	}
}

type completion struct {
	result Result
	err    error
}

func beginRun(runner *Runner, ctx context.Context, spec Spec) <-chan completion {
	done := make(chan completion, 1)
	go func() {
		result, err := runner.Run(ctx, spec)
		done <- completion{result, err}
	}()
	return done
}

func finishRun(t *testing.T, done <-chan completion) completion {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("owned run did not finish within the cleanup test budget")
		return completion{}
	}
}

func assertFixtureStopped(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, name+".pid"))
		if err != nil {
			t.Fatalf("missing owned fixture %s: %v", name, err)
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil || pid <= 0 {
			t.Fatal("invalid owned fixture PID")
		}
		deadline := time.Now().Add(3 * time.Second)
		for fixtureProcessAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if fixtureProcessAlive(pid) {
			t.Fatalf("owned fixture %s remains alive", name)
		}
	}
}

func TestNativeRunSuccessAndFailureKeepStderrPrivate(t *testing.T) {
	requireNative(t)
	runner := New(1)
	root := t.TempDir()
	result, err := runner.Run(context.Background(), fixtureSpec("success", root, 3*time.Second))
	if err != nil || !result.Started || result.Kind != "" || string(result.Stdout) != "owned-fixture-ok" {
		t.Fatalf("success result=%+v err=%v", result, err)
	}
	if strings.Contains(string(result.Stdout), stderrMarker) {
		t.Fatal("stderr returned as stdout")
	}
	result, err = runner.Run(context.Background(), fixtureSpec("failed", root, 3*time.Second))
	assertFailure(t, result, err, true, Failed)
	if BeforeStart(err) {
		t.Fatal("started failure was classified as safe to retry")
	}
}

func TestNativePreCancelAndMissingExecutableStartNothing(t *testing.T) {
	requireNative(t)
	runner := New(1)
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := runner.Run(ctx, fixtureSpec("success", root, time.Second))
	assertFailure(t, result, err, false, Cancelled)
	if !BeforeStart(err) {
		t.Fatal("pre-cancel did not preserve before-start evidence")
	}
	if _, err = os.Stat(filepath.Join(root, "success.pid")); !os.IsNotExist(err) {
		t.Fatal("pre-cancel fixture started")
	}
	result, err = runner.Run(context.Background(), Spec{Executable: filepath.Join(root, "missing-program"), Timeout: time.Second, OutputLimit: 4096})
	assertFailure(t, result, err, false, Unavailable)
	if len(runner.slots) != 0 {
		t.Fatal("definite before-start refusal consumed a seat")
	}
}

func TestNativeBusyRefusesWithoutSpawningAndReleasesAfterCleanup(t *testing.T) {
	requireNative(t)
	runner := New(1)
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := beginRun(runner, ctx, fixtureSpec("hold", root, 10*time.Second))
	if !waitFile(filepath.Join(root, "hold.pid"), 4*time.Second) {
		t.Fatal("owned hold fixture did not start")
	}
	result, err := runner.Run(context.Background(), fixtureSpec("success", root, time.Second))
	assertFailure(t, result, err, false, Busy)
	if _, err = os.Stat(filepath.Join(root, "success.pid")); !os.IsNotExist(err) {
		t.Fatal("busy refusal started a second child")
	}
	cancel()
	finished := finishRun(t, done)
	assertFailure(t, finished.result, finished.err, true, Cancelled)
	assertFixtureStopped(t, root, "hold")
	result, err = runner.Run(context.Background(), fixtureSpec("success", root, 3*time.Second))
	if err != nil || string(result.Stdout) != "owned-fixture-ok" {
		t.Fatalf("confirmed cleanup failed to release seat: result=%+v err=%v", result, err)
	}
}

func TestNativeStdoutAndStderrFloodsAreBoundedAndPrivate(t *testing.T) {
	requireNative(t)
	for _, mode := range []string{"stdout-flood", "stderr-flood"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			spec := fixtureSpec(mode, root, 4*time.Second)
			spec.OutputLimit = 1024
			result, err := New(1).Run(context.Background(), spec)
			assertFailure(t, result, err, true, OutputLimit)
			assertFixtureStopped(t, root, mode)
		})
	}
}

func TestNativeDeadlineCleansOrdinaryAndDetachedTrees(t *testing.T) {
	requireNative(t)
	for _, mode := range []string{"tree", "detached-tree"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			result, err := New(1).Run(context.Background(), fixtureSpec(mode, root, 2*time.Second))
			assertFailure(t, result, err, true, TimedOut)
			assertFixtureStopped(t, root, "parent", "branch", "leaf")
		})
	}
}

func TestNativeExitedParentCannotLeaveInheritedPipesOrChildren(t *testing.T) {
	requireNative(t)
	root := t.TempDir()
	result, err := New(1).Run(context.Background(), fixtureSpec("held-pipe", root, 4*time.Second))
	assertFailure(t, result, err, true, Failed)
	assertFixtureStopped(t, root, "parent", "branch", "leaf")
}

func TestNativeCleanupPreservesUnrelatedOwnedRun(t *testing.T) {
	requireNative(t)
	unrelatedRoot := t.TempDir()
	unrelatedCtx, unrelatedCancel := context.WithCancel(context.Background())
	t.Cleanup(unrelatedCancel)
	unrelated := beginRun(New(1), unrelatedCtx, fixtureSpec("hold", unrelatedRoot, 12*time.Second))
	if !waitFile(filepath.Join(unrelatedRoot, "hold.pid"), 4*time.Second) {
		t.Fatal("unrelated owned fixture did not start")
	}
	root := t.TempDir()
	result, err := New(1).Run(context.Background(), fixtureSpec("detached-tree", root, 2*time.Second))
	assertFailure(t, result, err, true, TimedOut)
	assertFixtureStopped(t, root, "parent", "branch", "leaf")
	pidData, err := os.ReadFile(filepath.Join(unrelatedRoot, "hold.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(pidData))
	if !fixtureProcessAlive(pid) {
		t.Fatal("cleanup killed an unrelated owned process")
	}
	select {
	case finished := <-unrelated:
		t.Fatalf("unrelated run ended during another cleanup: %+v", finished)
	default:
	}
	unrelatedCancel()
	finished := finishRun(t, unrelated)
	assertFailure(t, finished.result, finished.err, true, Cancelled)
	assertFixtureStopped(t, unrelatedRoot, "hold")
}

func TestNativeAuthorityDeathDoesNotLeaveOwnedTree(t *testing.T) {
	requireNative(t)
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], fixtureCommand, "authority", root)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	if !waitFile(filepath.Join(root, "leaf.pid"), 4*time.Second) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("authority fixture did not dispatch its owned tree")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	assertFixtureStopped(t, root, "parent", "branch", "leaf")
}

func TestPendingCleanupReturnsUnknownWithinGraceWithoutAbandoningMonitor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan ownedReport, 1)
	cancelled := false
	startedAt := time.Now()
	started, verified, err := awaitOwned(ctx, done, func() { cancelled = true })
	if !started || verified || err == nil || !cancelled {
		t.Fatal("pending cleanup was certified or cancellation was not forwarded")
	}
	if elapsed := time.Since(startedAt); elapsed < cleanupGrace || elapsed > 2*time.Second {
		t.Fatalf("pending cleanup exceeded finite grace: %s", elapsed)
	}
	// A late monitor still owns cleanup and can report to its buffered channel;
	// the caller does not close it or refund its quarantined admission seat.
	done <- ownedReport{started: true, verified: true}
}
