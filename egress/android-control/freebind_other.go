//go:build !linux

package main

// setFreebind 在非 Linux 平台为空操作(本服务实际只在 Android/linux 上运行,
// 此 stub 仅为在开发机上能通过编译)。
func setFreebind(fd uintptr) error { return nil }
