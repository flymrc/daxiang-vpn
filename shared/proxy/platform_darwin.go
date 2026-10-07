//go:build darwin

package proxy

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"zongheng-vpn/shared/paths"
)

func launchEngine(ctx paths.Context, fast bool) error {
	if fast {
		return errors.New("macOS CLI 暂不支持 --fast；请使用默认用户态 WireGuard 代理模式")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, EngineCommand, HomeFlag, ctx.Root)
	// Unassigned standard streams are connected to /dev/null by os/exec.
	// The dedicated child owns its bounded typed log; raw dependency output
	// must not become an unbounded plaintext launch log.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
