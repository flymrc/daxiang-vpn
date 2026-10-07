//go:build windows

package processbudget

import (
	"golang.org/x/sys/windows"
	"os/exec"
)

func prepareDetachedFixture(*exec.Cmd) {
	// The Job covers new child groups as well as the direct command.
}

func killOwnedFixtureSupervisor() bool { return false }

func fixtureProcessAlive(pid int) bool {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(process)
	status, err := windows.WaitForSingleObject(process, 0)
	return err == nil && status == uint32(windows.WAIT_TIMEOUT)
}
