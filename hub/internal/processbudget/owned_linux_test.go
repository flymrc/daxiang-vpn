//go:build linux

package processbudget

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func prepareDetachedFixture(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func killOwnedFixtureSupervisor() bool {
	// Refuse to signal any caller except this binary's private supervisor mode.
	parent := os.Getppid()
	args, err := os.ReadFile("/proc/" + strconv.Itoa(parent) + "/cmdline")
	if err != nil || !strings.Contains(string(args), "\x00"+supervisorCommand+"\x00") {
		return false
	}
	return unix.Kill(parent, unix.SIGKILL) == nil
}

func fixtureProcessAlive(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return true
	}
	fields := strings.Fields(string(stat[end+1:]))
	return len(fields) > 0 && fields[0] != "Z" && fields[0] != "X"
}

func TestNativeSupervisorCrashQuarantinesSeatAndCleansOrdinaryWorker(t *testing.T) {
	root := t.TempDir()
	runner := New(1)
	result, err := runner.Run(context.Background(), fixtureSpec("kill-supervisor", root, 3*time.Second))
	assertFailure(t, result, err, true, Unknown)
	assertFixtureStopped(t, root, "worker")
	if len(runner.slots) != 1 {
		t.Fatal("missing cleanup receipt released quarantined seat")
	}
	result, err = runner.Run(context.Background(), fixtureSpec("success", root, time.Second))
	assertFailure(t, result, err, false, Busy)
	if _, err = os.Stat(filepath.Join(root, "success.pid")); !os.IsNotExist(err) {
		t.Fatal("quarantined seat admitted another child")
	}
}

func cleanupOwnedDetachedFixture(t *testing.T, root, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name+".pid"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil || pid <= 0 {
		t.Error("refused invalid owned fixture identity")
		return
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return
	}
	defer unix.Close(fd)
	args, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(args) == 0 {
		return
	}
	if !strings.Contains(string(args), "\x00"+fixtureCommand+"\x00") || !strings.Contains(string(args), "\x00"+root+"\x00") {
		t.Error("refused a process outside this owned fixture namespace")
		return
	}
	// Only this test's private fixture is cleaned, using its pinned kernel
	// identity. Never signal a raw PID obtained from a directory listing.
	_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
}

func TestNativeDetachedChildAfterSupervisorCrashRemainsUnknownAndQuarantined(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() {
		cleanupOwnedDetachedFixture(t, root, "leaf")
		cleanupOwnedDetachedFixture(t, root, "branch")
		cleanupOwnedDetachedFixture(t, root, "worker")
	})
	runner := New(1)
	result, err := runner.Run(context.Background(), fixtureSpec("detached-kill-supervisor", root, 3*time.Second))
	assertFailure(t, result, err, true, Unknown)
	if BeforeStart(err) || len(runner.slots) != 1 {
		t.Fatal("loss of detached supervision permitted an automatic retry")
	}
	if !waitFile(filepath.Join(root, "leaf.pid"), time.Second) {
		t.Fatal("detached fixture was not exercised")
	}
	result, err = runner.Run(context.Background(), fixtureSpec("success", root, time.Second))
	assertFailure(t, result, err, false, Busy)
	assertFixtureStopped(t, root, "worker")
	// No claim is made that a child which detached and destroyed its supervisor
	// was recovered. Explicit cleanup above is test fixture ownership only.
}
