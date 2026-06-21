//go:build windows

package app

func fastModeHelp() string {
	return "--fast：高性能模式，使用系统网络栈（延迟更低、速度更快），\n        需要管理员权限，启动时会弹出 UAC 授权窗口。"
}
