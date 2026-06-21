//go:build !windows && !darwin

package proxy

import (
	"errors"

	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
)

// Hidden subcommands are only used by the Windows launcher path.
const (
	EngineCommand = "__engine"
	KillCommand   = "__killpid"
	HomeFlag      = "--home"
)

func WriteSingBoxConfig(_ paths.Context, _ config.Config, _ bool) error {
	return errors.New("当前平台未实现 Windows 客户端配置生成")
}

func Start(_ paths.Context, _ config.Config, _ bool) error {
	return errors.New("当前平台未实现 Windows 客户端后台启动")
}

func Stop(_ paths.Context) (bool, error) {
	return false, errors.New("当前平台未实现 Windows 客户端停止逻辑")
}

func KillPID(_ int) error {
	return errors.New("当前平台未实现 Windows 客户端进程终止逻辑")
}

func IsRunning(_ paths.Context) (bool, int) {
	return false, 0
}
