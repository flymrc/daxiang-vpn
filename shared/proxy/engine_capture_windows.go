//go:build windows

package proxy

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func suppressNativeOutput(stdout, stderr *os.File) error {
	if err := windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(stdout.Fd())); err != nil {
		return err
	}
	return windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(stderr.Fd()))
}

func retireCachedStandardOutput(stdout, stderr, null, pipe *os.File) error {
	seen := make(map[uintptr]*os.File, 2)
	for _, file := range []*os.File{stdout, stderr} {
		if file == nil {
			continue
		}
		handle := file.Fd()
		if handle == 0 || handle == uintptr(windows.InvalidHandle) {
			continue
		}
		if handle == null.Fd() || handle == pipe.Fd() || handle == os.Stdin.Fd() {
			return errors.New("engine_capture_failed")
		}
		if previous := seen[handle]; previous != nil && previous != file {
			// Closing two independent os.File wrappers cannot atomically mark
			// both closed. The second Close could hit a reused HANDLE. Reject
			// this unsupported launch before starting dependency code.
			return errors.New("engine_capture_failed")
		}
		seen[handle] = file
	}
	for _, file := range seen {
		if err := file.Close(); err != nil && !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			return errors.New("engine_capture_failed")
		}
	}
	return nil
}
