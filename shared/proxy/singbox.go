//go:build windows

package proxy

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
)

// EngineCommand is the hidden subcommand that runs sing-box in-process. Start
// re-executes this binary with this argument as a detached background process.
const EngineCommand = "__engine"

// KillCommand is the hidden subcommand that terminates a PID. It exists so the
// elevated kill path (for --fast engines) shows our app name in the UAC prompt
// rather than taskkill.
const KillCommand = "__killpid"

// HomeFlag passes the resolved app root to the engine child, so an elevated
// engine reads the same config/PID paths even if UAC ran under another account.
const HomeFlag = "--home"

// Windows process creation flags used to launch the engine without a console
// window and detached from this CLI process group.
const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

// Start launches the in-process sing-box engine in a detached background child
// process (this same binary re-executed with EngineCommand). The engine writes
// its own PID file. When fast is true the engine needs administrator rights to
// create the system TUN, so it is launched elevated via UAC.
func Start(ctx paths.Context, cfg config.Config, fast bool) error {
	_ = cfg
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	if fast {
		// System TUN requires admin; elevate the engine (UAC prompt). The
		// resolved root is passed so the elevated process reads the same paths.
		return runElevated(exe, fmt.Sprintf(`%s %s "%s"`, EngineCommand, HomeFlag, ctx.Root), false)
	}

	cmd := exec.Command(exe, EngineCommand, HomeFlag, ctx.Root)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach: the engine writes its own PID and keeps running after we exit.
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
	// Normal kill — works for the default (non-elevated) gVisor engine.
	_ = KillPID(pid)
	if pidRunning(pid) {
		// Still alive: likely an elevated --fast engine. Retry the kill with
		// elevation via our own __killpid (so UAC shows the app name).
		if exe, e := os.Executable(); e == nil {
			_ = runElevated(exe, fmt.Sprintf("%s %d", KillCommand, pid), true)
		}
	}
	if pidRunning(pid) {
		return false, errors.New("停止失败，请重试")
	}
	_ = os.Remove(ctx.PIDPath)
	return true, nil
}

// KillPID force-terminates a process tree by PID. Invoked directly by Stop and,
// elevated, via the KillCommand subcommand.
func KillPID(pid int) error {
	taskkill := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
	return exec.Command(taskkill, "/PID", strconv.Itoa(pid), "/T", "/F").Run()
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
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}
