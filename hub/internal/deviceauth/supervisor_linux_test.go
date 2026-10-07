//go:build linux

package deviceauth

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSupervisedWGAuthorityCrashChild(t *testing.T) {
	if os.Getenv("ZHVPN_SUPERVISOR_CRASH_HELPER") != "1" {
		return
	}
	s := reopen(t, os.Getenv("ZHVPN_SUPERVISOR_CRASH_DB"))
	a, err := NewSupervisedWGExecutor(os.Getenv("ZHVPN_SUPERVISOR_CRASH_WG"), os.Getenv("ZHVPN_SUPERVISOR_CRASH_BIN"), "test-wg", testPolicy(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Process(context.Background(), os.Getenv("ZHVPN_SUPERVISOR_CRASH_OP"), a)
}

func TestSupervisedWGParentCrashRetainsFenceUntilTreeReaped(t *testing.T) {
	for _, mode := range []string{"hang", "hang-detached"} {
		t.Run(mode, func(t *testing.T) {
			s, _, dbPath := newTestStore(t)
			enroll(t, s, "d1")
			op := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "supervised-crash"))
			binary := fixtureBinary(t)
			a := fixtureWGAdapter(t, binary, 10*time.Second)
			dir := filepath.Dir(binary)
			if err := os.WriteFile(filepath.Join(dir, "mode"), []byte(mode), 0600); err != nil {
				t.Fatal(err)
			}
			helper := exec.Command(os.Args[0], "-test.run=^TestSupervisedWGAuthorityCrashChild$", "-test.timeout=15s")
			helper.Env = append(os.Environ(), "ZHVPN_SUPERVISOR_CRASH_HELPER=1", "ZHVPN_SUPERVISOR_CRASH_DB="+dbPath, "ZHVPN_SUPERVISOR_CRASH_WG="+binary, "ZHVPN_SUPERVISOR_CRASH_BIN="+a.supervisor, "ZHVPN_SUPERVISOR_CRASH_OP="+op.ID)
			if err := helper.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = helper.Process.Kill() })
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "child.pid")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = helper.Process.Kill()
					_ = helper.Wait()
					t.Fatal("WG descendant never actually started")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err := helper.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = helper.Wait()
			// The next authority's lock acquisition is the ordering assertion:
			// inherited flock must stay held until every old execution PID is gone.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			unlock, err := s.acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			for _, file := range []string{"parent.pid", "child.pid"} {
				data, err := os.ReadFile(filepath.Join(dir, file))
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(string(data))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); !os.IsNotExist(err) {
					t.Fatalf("execution fence released while %s PID %d still exists", file, pid)
				}
			}
			time.Sleep(2200 * time.Millisecond)
			if _, err := os.Stat(filepath.Join(dir, "escaped")); !os.IsNotExist(err) {
				t.Fatal("old WG tree performed a delayed side effect after Hub crash")
			}
		})
	}
}

func TestSupervisedWGRequiresLiveStoreFenceAndRestrictedArguments(t *testing.T) {
	binary := fixtureBinary(t)
	a := fixtureWGAdapter(t, binary, time.Second)
	if _, err := a.Snapshot(context.Background(), Fence{Epoch: testPolicy().Epoch, DeviceID: "d1", Generation: 1, Sequence: 1}); err != ErrSupervision {
		t.Fatalf("missing fence capability accepted: %v", err)
	}
	for _, args := range [][]string{{"zhvpn-supervisor-v1", binary, "show", "test-wg", "dump"}, {"zhvpn-supervisor-v1", binary, "set", "test-wg", "private-key", "anything"}, {"zhvpn-supervisor-v1", binary, "set", "test-wg", "peer", testKey(1), "allowed-ips", "10.66.0.0/24"}} {
		if supervisorArguments(args) {
			t.Fatal("unsafe supervisor command accepted")
		}
	}
}

func TestSupervisedWGHelperCrashStopsGroupBeforeReturn(t *testing.T) {
	s, _, _ := newTestStore(t)
	binary := fixtureBinary(t)
	a := fixtureWGAdapter(t, binary, 10*time.Second)
	dir := filepath.Dir(binary)
	if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("hang"), 0600); err != nil {
		t.Fatal(err)
	}
	unlock, fence, err := s.acquirePinned(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx := context.WithValue(context.Background(), executionFenceKey{}, fence)
	result := make(chan error, 1)
	go func() {
		_, err := a.Snapshot(ctx, Fence{Epoch: testPolicy().Epoch, DeviceID: "d1", Generation: 1, Sequence: 1})
		result <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "child.pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("synthetic WG child never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	data, err := os.ReadFile(filepath.Join(dir, "supervisor.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err = helper.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != ErrExecutionUnknown {
			t.Fatalf("helper crash reported success: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ordinary WG tree not synchronously cleaned after helper crash")
	}
	for _, file := range []string{"parent.pid", "child.pid"} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); !os.IsNotExist(err) {
			t.Fatalf("adapter returned before %s PID %d was reaped", file, pid)
		}
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "escaped")); !os.IsNotExist(err) {
		t.Fatal("WG side effect continued after helper crash")
	}
}

func TestSupervisedWGHelperCrashWithDetachedChildRetainsFence(t *testing.T) {
	s, _, _ := newTestStore(t)
	binary := fixtureBinary(t)
	a := fixtureWGAdapter(t, binary, 10*time.Second)
	dir := filepath.Dir(binary)
	if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("hang-detached"), 0600); err != nil {
		t.Fatal(err)
	}
	unlock, fence, err := s.acquirePinned(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	result := make(chan error, 1)
	go func() {
		_, err := a.Snapshot(context.WithValue(context.Background(), executionFenceKey{}, fence), Fence{Epoch: testPolicy().Epoch, DeviceID: "d1", Generation: 1, Sequence: 1})
		result <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "child.pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached synthetic child never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	process := func(file string) *os.Process {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			t.Fatal(err)
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := process("supervisor.pid").Kill(); err != nil {
		t.Fatal(err)
	}
	child := process("child.pid")
	t.Cleanup(func() { _ = child.Kill(); _, _ = child.Wait() })
	time.Sleep(100 * time.Millisecond)
	select {
	case <-result:
		t.Fatal("helper loss released execution with an uncertain detached child")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if release, err := s.acquire(ctx); err == nil {
		release()
		t.Fatal("uncertain execution did not retain Store fence")
	}
	// Only this controlled test knows the detached child's identity/provenance.
	// Production must not guess and kill an unrelated legacy subprocess.
	if err := child.Kill(); err != nil {
		t.Fatal(err)
	}
	if _, err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != ErrExecutionUnknown {
			t.Fatalf("helper loss returned success: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fence did not recover after controlled orphan was reaped")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped")); !os.IsNotExist(err) {
		t.Fatal("controlled orphan wrote after helper loss")
	}
}
