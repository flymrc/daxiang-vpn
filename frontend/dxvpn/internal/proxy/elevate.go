package proxy

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// errUACDeclined is returned when the user dismisses the UAC prompt.
var errUACDeclined = errors.New("已取消管理员授权")

const (
	seeMaskNoCloseProcess = 0x00000040
	swHide                = 0
	errorCancelled        = 1223 // ERROR_CANCELLED：用户拒绝了 UAC
	waitInfinite          = 0xFFFFFFFF
)

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     uintptr
}

var (
	modShell32              = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteEx      = modShell32.NewProc("ShellExecuteExW")
	modKernel32             = syscall.NewLazyDLL("kernel32.dll")
	procWaitForSingleObject = modKernel32.NewProc("WaitForSingleObject")
	procCloseHandle         = modKernel32.NewProc("CloseHandle")
)

// runElevated launches exe with the given argument string via the "runas" verb,
// triggering a UAC prompt when the current process is not already elevated. The
// window is hidden. If wait is true it blocks until the elevated process exits.
//
// Returns errUACDeclined if the user dismisses the UAC prompt.
func runElevated(exe string, args string, wait bool) error {
	verbPtr, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	exePtr, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	argPtr, err := syscall.UTF16PtrFromString(args)
	if err != nil {
		return err
	}

	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess,
		lpVerb:       verbPtr,
		lpFile:       exePtr,
		lpParameters: argPtr,
		nShow:        swHide,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	ret, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && uintptr(errno) == errorCancelled {
			return errUACDeclined
		}
		return fmt.Errorf("提权启动失败：%v", callErr)
	}
	if info.hProcess == 0 {
		return nil
	}
	defer procCloseHandle.Call(info.hProcess)
	if wait {
		procWaitForSingleObject.Call(info.hProcess, waitInfinite)
	}
	return nil
}
