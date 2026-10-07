//go:build linux

package processbudget

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const supervisorCommand = "zhvpn-legacy-process-supervisor-v1"

func trustedBinary(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		stickyRoot := ok && info.IsDir() && st.Uid == 0 && info.Mode()&os.ModeSticky != 0
		if !ok || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || (info.Mode().Perm()&0022 != 0 && !stickyRoot) {
			return false
		}
		if current == path && (!info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0) {
			return false
		}
		if current != path && !info.IsDir() {
			return false
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return true
}

func runOwned(ctx context.Context, path string, args []string, out *capture, timeout time.Duration) (bool, bool, error) {
	path, err := filepath.EvalSymlinks(path)
	if err != nil || !trustedBinary(path) {
		return false, true, errors.New("binary unavailable")
	}
	self, err := os.Executable()
	if err != nil {
		return false, true, err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil || !trustedBinary(self) {
		return false, true, errors.New("supervisor unavailable")
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return false, true, err
	}
	unix.Close(fd)
	if _, err = ownChildren(); err != nil {
		return false, true, err
	}
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		return false, true, err
	}
	receiptRead, receiptWrite, err := os.Pipe()
	if err != nil {
		lifeRead.Close()
		lifeWrite.Close()
		return false, true, err
	}
	closeFiles := func() { lifeRead.Close(); lifeWrite.Close(); receiptRead.Close(); receiptWrite.Close() }
	commandArgs := append([]string{supervisorCommand, strconv.FormatInt(timeout.Milliseconds(), 10), path}, args...)
	cmd := exec.Command(self, commandArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{lifeRead, receiptWrite}
	cmd.Stdout = stream{out, true}
	cmd.Stderr = stream{out, false}
	cmd.Stdin = nil
	cmd.WaitDelay = 100 * time.Millisecond
	if ctx.Err() != nil {
		closeFiles()
		return false, true, ctx.Err()
	}
	if err = cmd.Start(); err != nil {
		closeFiles()
		return false, true, err
	}
	lifeRead.Close()
	receiptWrite.Close()
	finished := make(chan ownedReport, 1)
	go func() {
		defer closeFiles()
		waitErr := waitUnreaped(cmd.Process.Pid)
		// Keep the unreaped helper PID pinned while cleaning its ordinary process
		// group. A crashed helper cannot leave such children running. Detached
		// children after loss of the helper remain unknown: quarantine, never guess.
		if waitErr == nil {
			_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		}
		commandErr := cmd.Wait()
		if waitErr == nil {
			waitErr = commandErr
		}
		receipt := make([]byte, 3)
		n, receiptErr := io.ReadFull(receiptRead, receipt)
		verified := receiptErr == nil && n == 3 && receipt[0] == 'C' && (receipt[1] == '0' || receipt[1] == '1' || receipt[1] == '2') && receipt[2] == '\n'
		if !verified {
			finished <- ownedReport{true, false, errors.New("cleanup unknown")}
			return
		}
		if receipt[1] == '2' {
			finished <- ownedReport{false, true, errors.New("command not started")}
			return
		}
		if receipt[1] != '0' || ctx.Err() != nil {
			finished <- ownedReport{true, true, errors.New("command failed")}
			return
		}
		finished <- ownedReport{true, true, waitErr}
	}()
	return awaitOwned(ctx, finished, func() { lifeWrite.Close() })
}

// SupervisorMain handles only a private inherited pipe protocol. It is called
// before Hub initialization; a helper never opens databases or v2 authority.
func SupervisorMain(args []string) (int, bool) {
	if len(args) == 0 || args[0] != supervisorCommand {
		return 0, false
	}
	return supervise(args[1:]), true
}
func supervise(args []string) int {
	if len(args) < 2 || len(args) > 66 {
		return 1
	}
	ms, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || ms < 1 || ms > 30000 || !trustedBinary(args[1]) {
		return 1
	}
	for _, arg := range args {
		if len(arg) > 16<<10 {
			return 1
		}
	}
	life := os.NewFile(3, "legacy-parent-life")
	receipt := os.NewFile(4, "legacy-cleanup-receipt")
	if life == nil || receipt == nil {
		return 1
	}
	defer life.Close()
	defer receipt.Close()
	for _, fd := range []int{3, 4} {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFIFO || st.Uid != uint32(os.Geteuid()) {
			return 1
		}
		unix.CloseOnExec(fd)
	}
	if unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) != nil {
		return 1
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return 1
	}
	unix.Close(fd)
	if _, err = ownChildren(); err != nil {
		return 1
	}
	ended := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, life); close(ended) }()
	cmd := exec.Command(args[1], args[2:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	// A parent's already closed lifetime must not release a real mutation. The
	// read goroutine can lag scheduling; poll the authenticated pipe as well.
	poll := []unix.PollFd{{Fd: 3, Events: unix.POLLIN | unix.POLLHUP}}
	if _, err = unix.Poll(poll, 0); err != nil || poll[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
		_, _ = receipt.Write([]byte("C2\n"))
		return 1
	}
	select {
	case <-ended:
		_, _ = receipt.Write([]byte("C2\n"))
		return 1
	default:
	}
	if cmd.Start() != nil {
		_, _ = receipt.Write([]byte("C2\n"))
		return 1
	}
	exited := make(chan error, 1)
	go func() { exited <- waitUnreaped(cmd.Process.Pid) }()
	deadline := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer deadline.Stop()
	completed := false
	select {
	case err = <-exited:
		completed = err == nil
	case <-ended:
	case <-deadline.C:
	}
	// The helper's private PGID stays pinned by the unreaped helper in Hub.
	// This helper reaps only its own descendants, including detached groups.
	_ = cmd.Process.Kill()
	waitErr := cmd.Wait()
	extra := false
	for {
		children, e := ownChildren()
		if e != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		} // never certify lost enumeration.
		if len(children) == 0 {
			break
		}
		extra = true
		for _, pid := range children {
			pfd, e := unix.PidfdOpen(pid, 0)
			if e == nil {
				_ = unix.PidfdSendSignal(pfd, unix.SIGKILL, nil, 0)
				unix.Close(pfd)
			}
			var status unix.WaitStatus
			_, _ = unix.Wait4(pid, &status, unix.WNOHANG, nil)
		}
		time.Sleep(time.Millisecond)
	}
	if completed && waitErr == nil && !extra {
		_, _ = receipt.Write([]byte("C0\n"))
		return 0
	}
	_, _ = receipt.Write([]byte("C1\n"))
	return 1
}
func waitUnreaped(pid int) error {
	var info unix.Siginfo
	for {
		e := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if e != unix.EINTR {
			return e
		}
	}
}
func ownChildren() ([]int, error) {
	threads, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return nil, err
	}
	set := map[int]bool{}
	for _, thread := range threads {
		b, e := os.ReadFile("/proc/self/task/" + thread.Name() + "/children")
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		for _, value := range strings.Fields(string(b)) {
			pid, e := strconv.Atoi(value)
			if e != nil || pid <= 0 {
				return nil, errors.New("invalid child identity")
			}
			set[pid] = true
		}
	}
	values := []int{}
	for pid := range set {
		values = append(values, pid)
	}
	return values, nil
}
