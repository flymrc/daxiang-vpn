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
	return cmd.Process.Release()
}
