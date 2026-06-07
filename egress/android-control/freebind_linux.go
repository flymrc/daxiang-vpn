//go:build linux

package main

import "syscall"

// setFreebind 设置 IP_FREEBIND(15),允许 bind 当前不存在于接口上的地址,
// 解决开机时隧道 IP(10.66.0.101)尚未就绪的时序问题。
func setFreebind(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_IP, 15, 1)
}
