//go:build darwin

package proxy

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
)

// EngineCommand is the hidden subcommand that runs sing-box in-process. Start
// re-executes this binary with this argument as a detached background process.
const EngineCommand = "__engine"

// KillCommand is kept for CLI parity with Windows; macOS Stop uses POSIX
// signals directly and does not invoke it.
const KillCommand = "__killpid"

// HomeFlag passes the resolved app root to the engine child.
const HomeFlag = "--home"

func Start(ctx paths.Context, cfg config.Config, fast bool) error {
	_ = cfg
	if fast {
		return errors.New("macOS CLI 暂不支持 --fast；请使用默认用户态 WireGuard 代理模式")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ctx.LogDir, 0700); err != nil {
		return err
	}
	stdout, err := os.OpenFile(ctx.SingBoxLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(ctx.SingBoxErrorPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer stderr.Close()

	cmd := exec.Command(exe, EngineCommand, HomeFlag, ctx.Root)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func Stop(ctx paths.Context) (bool, error) {
	pid, err := readPID(ctx.PIDPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !pidRunning(pid) {
		_ = os.Remove(ctx.PIDPath)
		return false, nil
	}
	if err := KillPID(pid); err != nil && pidRunning(pid) {
		return false, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !pidRunning(pid) {
			_ = os.Remove(ctx.PIDPath)
			return true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := killProcess(pid, syscall.SIGKILL); err != nil && pidRunning(pid) {
		return false, fmt.Errorf("停止失败：%w", err)
	}
	_ = os.Remove(ctx.PIDPath)
	return true, nil
}

func KillPID(pid int) error {
	return killProcess(pid, syscall.SIGTERM)
}

func IsRunning(ctx paths.Context) (bool, int) {
	pid, err := readPID(ctx.PIDPath)
	if err != nil {
		return false, 0
	}
	if !pidRunning(pid) {
		return false, pid
	}
	return true, pid
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func pidRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func killProcess(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("无效 pid：%d", pid)
	}
	// The engine is started as its own process group. Signal the group first so
	// future helper children do not survive, then fall back to the process PID.
	if err := syscall.Kill(-pid, sig); err == nil {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
