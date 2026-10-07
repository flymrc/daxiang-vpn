//go:build windows

package proxy

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"testing"
	"zongheng-vpn/shared/paths"
)

func engineOutputAliasHelper(home paths.Context) int {
	// Distinct Go objects alias one valid inherited handle. They cannot both
	// be retired safely by two Close calls without a handle-reuse window.
	os.Stderr = os.NewFile(os.Stdout.Fd(), "owned-aliased-stderr")
	err := RunDedicatedEngine(home)
	var refusal *RuntimeError
	if !errors.As(err, &refusal) || refusal.Code != string(EngineLogCodeCaptureFailed) {
		return 2
	}
	if os.Stdout != dedicatedOutput.capture.null || os.Stderr != dedicatedOutput.capture.null {
		return 3
	}
	nativeEngineOutputCanary(engineOutputCanary)
	_, _ = os.Stdout.WriteString(engineOutputCanary)
	_, _ = os.Stderr.WriteString(engineOutputCanary)
	return 0
}

func TestDedicatedWindowsDistinctFileHandleAliasRefusesBeforeEngineStartup(t *testing.T) {
	home, _, _ := syntheticRuntime(t)
	cmd := exec.Command(os.Args[0], "--engine-output-alias-helper", home.Root)
	output, err := engineChildSeparateOutput(cmd)
	if err != nil || len(output) != 0 {
		t.Fatal("unsafe standard-handle alias did not refuse with current/native output suppressed")
	}
	if _, err := os.Stat(statePath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsafe alias published an engine identity")
	}
	entries, err := os.ReadDir(home.LogDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("unsafe alias ran logging/dependency startup")
	}
}

func nativeEngineOutputCanary(value string) {
	for _, kind := range []uint32{windows.STD_OUTPUT_HANDLE, windows.STD_ERROR_HANDLE} {
		handle, err := windows.GetStdHandle(kind)
		if err == nil {
			var written uint32
			_ = windows.WriteFile(handle, []byte(value+"\n"), &written, nil)
		}
	}
}
