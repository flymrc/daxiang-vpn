//go:build linux

package deviceauth

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (e *WGExecutor) runSupervised(ctx context.Context, args ...string) ([]byte, error) {
	fence, ok := ctx.Value(executionFenceKey{}).(*os.File)
	if !ok || fence == nil || e.supervisor == "" || requireBoundedSupervision(e.supervisor) != nil || trustedExecutionBinary(e.binary) != nil {
		return nil, ErrSupervision
	}
	bounded, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return nil, ErrSupervision
	}
	baseline, err := supervisorDirectChildren()
	if err != nil {
		return nil, ErrSupervision
	}
	readEnd, parentLife, err := os.Pipe()
	if err != nil {
		return nil, ErrExecutionUnknown
	}
	defer readEnd.Close()
	defer parentLife.Close()
	cmd := exec.Command(e.supervisor, append([]string{"zhvpn-supervisor-v1", e.binary}, args...)...)
	cmd.Env = []string{}
	cmd.ExtraFiles = []*os.File{fence, readEnd}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	out := &cappedBuffer{limit: 2 << 20}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return nil, ErrExecutionUnknown
	}
	// Only the Hub retains the write end. Its crash closes it automatically.
	// Do not kill the supervisor: its inherited fd3 owns the execution fence
	// until all WG children have been terminated and reaped.
	_ = readEnd.Close()
	exited := make(chan error, 1)
	go func() { exited <- waitUnreaped(cmd.Process.Pid) }()
	interrupted := false
	select {
	case err = <-exited:
	case <-bounded.Done():
		interrupted = true
		_ = parentLife.Close()
		err = <-exited // Store must not unlock while its helper is alive.
	}
	// Even a helper panic/SIGKILL cannot leave its ordinary WG subtree running.
	// Pin the unreaped helper PGID, kill it, then reap only this execution group.
	// Other Hub/legacy subprocesses are never killed as incidental cleanup.
	if err == nil {
		_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
	}
	waitErr := cmd.Wait()
	for reapSupervisorGroup(cmd.Process.Pid) != nil {
		time.Sleep(time.Second)
	}
	if err != nil || waitErr != nil || interrupted || bounded.Err() != nil {
		// If an abnormal helper also lost track of a new detached child, keep
		// the Store fence until that uncertain process is gone. Unknown concurrent
		// legacy children may delay failure; they are never guessed/terminated.
		for {
			current, observeErr := supervisorDirectChildren()
			unknown := observeErr != nil
			for pid, identity := range current {
				if baseline[pid] != identity {
					unknown = true
				}
			}
			if !unknown {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil, ErrExecutionUnknown
	}
	return append([]byte(nil), out.Bytes()...), nil
}

func waitUnreaped(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			return err
		}
	}
}

// RunExecutionSupervisor is the sole entry point of the trusted dedicated helper.
// fd3 inherits the Store's SAME locked open-file-description; fd4 detects parent
// lifetime. There are no credentials or arbitrary shell/config commands here.
// The live Hub retains its fence and supervises this helper's private process
// group if the helper itself exits. Simultaneous destruction of both supervisors
// while malicious children detach is outside the trusted-WG execution contract.
func RunExecutionSupervisor(args []string) int {
	if !supervisorArguments(args) {
		return 1
	}
	fence := os.NewFile(3, "deviceauth-inherited-fence")
	life := os.NewFile(4, "deviceauth-parent-life")
	if fence == nil || life == nil {
		return 1
	}
	defer fence.Close() // NEVER flock LOCK_UN; inherited OFD preserves ownership.
	defer life.Close()
	var st unix.Stat_t
	if err := unix.Fstat(3, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
		return 1
	}
	if err := unix.Fstat(4, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFIFO {
		return 1
	}
	unix.CloseOnExec(3)
	unix.CloseOnExec(4)
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return 1
	}
	parentEnded := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, life); close(parentEnded) }()
	cmd := exec.Command(args[1], args[2:]...)
	cmd.Env = []string{}
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.Discard
	// Inherit the helper's private PGID so the still-live Hub can terminate the
	// whole ordinary WG subtree if this helper itself exits unexpectedly.
	if err := cmd.Start(); err != nil {
		return 1
	}
	// WNOWAIT pins this unreaped child PID before direct termination.
	exited := make(chan error, 1)
	go func() { exited <- waitUnreaped(cmd.Process.Pid) }()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	completed := false
	var waitError error
	select {
	case err := <-exited:
		waitError = err
		completed = err == nil
	case <-parentEnded:
	case <-deadline.C:
	}
	if waitError == nil {
		_ = unix.Kill(cmd.Process.Pid, unix.SIGKILL)
	}
	result := cmd.Wait()
	// A subreaper adopts descendants after their parent dies. This also cleans
	// children that made their own process group; pidfds prevent kill PID reuse.
	// Keep fd3 held if the kernel cannot yet reap an uninterruptible child. Safety
	// takes precedence over falsely returning a bounded successful completion.
	if reapSupervisorChildren() != nil {
		// Loss of reliable enumeration must NOT release the inherited fence.
		for {
			time.Sleep(time.Second)
			if reapSupervisorChildren() == nil {
				break
			}
		}
		return 1
	}
	if completed && result == nil {
		return 0
	}
	return 1
}

func reapSupervisorGroup(pgid int) error {
	for {
		var status unix.WaitStatus
		pid, err := unix.Wait4(-pgid, &status, unix.WNOHANG, nil)
		if err == unix.EINTR {
			continue
		}
		if err == unix.ECHILD {
			return nil
		}
		if err != nil {
			return err
		}
		if pid == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func supervisorDirectChildren() (map[int]string, error) {
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return nil, err
	}
	result := map[int]string{}
	for _, task := range tasks {
		data, err := os.ReadFile("/proc/self/task/" + task.Name() + "/children")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, value := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(value)
			if err != nil || pid <= 0 {
				return nil, ErrExecutionUnknown
			}
			stat, err := os.ReadFile("/proc/" + value + "/stat")
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			index := strings.LastIndexByte(string(stat), ')')
			if index < 0 {
				return nil, ErrExecutionUnknown
			}
			fields := strings.Fields(string(stat[index+1:]))
			if len(fields) < 20 {
				return nil, ErrExecutionUnknown
			}
			result[pid] = fields[19] // Linux starttime: PID reuse is not identity.
		}
	}
	return result, nil
}

func supervisorArguments(args []string) bool {
	if len(args) < 5 || len(args) > 8 || args[0] != "zhvpn-supervisor-v1" || !filepath.IsAbs(args[1]) || len(args[1]) > 4096 || !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`).MatchString(args[3]) {
		return false
	}
	if trustedExecutionBinary(args[1]) != nil {
		return false
	}
	if len(args) == 5 {
		return args[2] == "show" && args[4] == "allowed-ips"
	}
	if args[2] != "set" || args[4] != "peer" || !validKey(args[5]) {
		return false
	}
	if len(args) == 7 {
		return args[6] == "remove"
	}
	if args[6] != "allowed-ips" {
		return false
	}
	// WGExecutor has already checked exact customer address policy. The helper
	// accepts only that single canonical host shape, never cfg/dump/private-key.
	p, err := netip.ParsePrefix(args[7])
	return err == nil && p.Bits() == p.Addr().BitLen() && p.String() == args[7]
}

func reapSupervisorChildren() error {
	for {
		tasks, err := os.ReadDir("/proc/self/task")
		if err != nil {
			return err
		}
		children := map[int]bool{}
		for _, task := range tasks {
			data, err := os.ReadFile("/proc/self/task/" + task.Name() + "/children")
			if errors.Is(err, os.ErrNotExist) {
				continue
			} // Go runtime thread exited
			if err != nil {
				return err
			}
			for _, value := range strings.Fields(string(data)) {
				pid, err := strconv.Atoi(value)
				if err != nil || pid <= 0 {
					return ErrExecutionUnknown
				}
				children[pid] = true
			}
		}
		for pid := range children {
			fd, err := unix.PidfdOpen(pid, 0)
			if err == unix.ESRCH {
				continue
			}
			if err != nil {
				return err
			}
			err = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
			_ = unix.Close(fd)
			if err != nil && err != unix.ESRCH {
				return err
			}
		}
		for {
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if err == unix.EINTR {
				continue
			}
			if err == unix.ECHILD {
				return nil
			}
			if err != nil {
				return err
			}
			if pid == 0 {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
}
