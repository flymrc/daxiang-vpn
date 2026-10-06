//go:build windows

package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"zongheng-vpn/shared/paths"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

func launchEngine(ctx paths.Context, fast bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if fast {
		return runElevated(exe, fmt.Sprintf(`%s %s %s`, EngineCommand, HomeFlag, syscall.EscapeArg(ctx.Root)), false)
	}
	cmd := exec.Command(exe, EngineCommand, HomeFlag, ctx.Root)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewProcessGroup | createNoWindow}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
