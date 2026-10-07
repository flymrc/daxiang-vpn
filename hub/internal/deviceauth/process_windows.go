//go:build windows

package deviceauth

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// Windows execution is fixture-only: production hosting is separately refused.
// Kernel job lifetime protects fixture descendants if their authority exits.
func trustedExecutionBinary(string) error { return nil }

func requireBoundedSupervision(supervisor string) error {
	if supervisor != "" {
		return ErrSupervision
	}
	return nil
}

func startBounded(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	// Start suspended: no child can escape before job membership is assigned.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW}
	if err = cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|0x0800, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
	}
	if err == nil {
		resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
		status, _, callErr := resume.Call(uintptr(process))
		if status != 0 {
			err = callErr
		}
	}
	if process != 0 {
		windows.CloseHandle(process)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { _ = windows.TerminateJobObject(job, 1); _ = windows.CloseHandle(job) }) }, nil
}

func waitBounded(cmd *exec.Cmd, cleanup func()) error { return cmd.Wait() }
