//go:build !windows && !darwin

package proxy

import (
	"errors"

	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
)

func WriteSingBoxConfig(_ paths.Context, _ config.Config, _ bool) error {
	return errors.New("当前平台未实现 Windows 客户端配置生成")
}

func launchEngine(_ paths.Context, _ bool) error {
	return errors.New("当前平台未实现 Windows 客户端后台启动")
}
