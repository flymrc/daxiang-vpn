//go:build windows

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"syscall"
	"unsafe"

	"zongheng-vpn/shared/paths"
)

const (
	waitObject0   = 0x00000000
	waitAbandoned = 0x00000080
	waitFailed    = 0xFFFFFFFF
	infinite      = 0xFFFFFFFF
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW  = kernel32.NewProc("CreateMutexW")
	procWaitForSingle = kernel32.NewProc("WaitForSingleObject")
	procReleaseMutex  = kernel32.NewProc("ReleaseMutex")
	procCloseHandle   = kernel32.NewProc("CloseHandle")
)

func withOperationLock(ctx paths.Context, fn func() error) error {
	sum := sha256.Sum256([]byte(ctx.Root))
	name := "Local\\ZonghengVPN-" + hex.EncodeToString(sum[:8])
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	handle, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		return fmt.Errorf("创建客户端互斥锁失败：%w", err)
	}
	defer procCloseHandle.Call(handle)

	wait, _, err := procWaitForSingle.Call(handle, infinite)
	switch wait {
	case waitObject0, waitAbandoned:
		defer procReleaseMutex.Call(handle)
		return fn()
	case waitFailed:
		return fmt.Errorf("等待客户端互斥锁失败：%w", err)
	default:
		return fmt.Errorf("等待客户端互斥锁返回未知状态：0x%x", wait)
	}
}
