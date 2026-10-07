//go:build windows

package processbudget

import (
	"context"
	"golang.org/x/sys/windows"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

func SupervisorMain([]string) (int, bool) { return 0, false }
func runOwned(ctx context.Context, path string, args []string, out *capture, _ time.Duration) (bool, bool, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return false, true, err
	}
	var jobMu sync.Mutex
	closeJob := func() {
		jobMu.Lock()
		defer jobMu.Unlock()
		if job != 0 {
			windows.CloseHandle(job)
			job = 0
		}
	}
	terminateJob := func() {
		jobMu.Lock()
		defer jobMu.Unlock()
		if job != 0 {
			_ = windows.TerminateJobObject(job, 1)
		}
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		closeJob()
		return false, true, err
	}
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW}
	cmd.Stdout = stream{out, true}
	cmd.Stderr = stream{out, false}
	cmd.Stdin = nil
	cmd.WaitDelay = 100 * time.Millisecond
	if ctx.Err() != nil {
		closeJob()
		return false, true, ctx.Err()
	}
	if err = cmd.Start(); err != nil {
		closeJob()
		return false, true, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|0x0800, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
		status, _, resumeErr := resume.Call(uintptr(process))
		if status != 0 {
			err = resumeErr
		}
	}
	if process != 0 {
		windows.CloseHandle(process)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		setupErr := err
		done := make(chan ownedReport, 1)
		go func() { _ = cmd.Wait(); closeJob(); done <- ownedReport{false, true, setupErr} }()
		return awaitOwned(ctx, done, func() { _ = cmd.Process.Kill() })
	}
	finished := make(chan ownedReport, 1)
	go func() {
		defer closeJob()
		err := cmd.Wait()
		terminateJob()
		// A Job handle identifies this tree even after the direct child exits. Do
		// not release its admission seat until the kernel reports no active process.
		for {
			var accounting struct {
				TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
				TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
			}
			e := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
			if e != nil {
				finished <- ownedReport{true, false, e}
				return
			}
			if accounting.ActiveProcesses == 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		finished <- ownedReport{true, true, err}
	}()
	return awaitOwned(ctx, finished, terminateJob)
}
