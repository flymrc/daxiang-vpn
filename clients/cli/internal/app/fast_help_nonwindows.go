//go:build !windows

package app

func fastModeHelp() string {
	return "--fast：Windows 专用高性能模式。macOS CLI 当前使用默认用户态 WireGuard，\n        暂不支持 --fast。"
}
