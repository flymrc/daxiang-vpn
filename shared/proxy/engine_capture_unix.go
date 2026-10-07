//go:build !windows

package proxy

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func suppressNativeOutput(stdout, stderr *os.File) error {
	if err := unix.Dup2(int(stdout.Fd()), 1); err != nil {
		return err
	}
	return unix.Dup2(int(stderr.Fd()), 2)
}

func retireCachedStandardOutput(stdout, stderr, null, pipe *os.File) error {
	seen := make(map[uintptr]bool, 2)
	for _, file := range []*os.File{stdout, stderr} {
		if file == nil {
			continue
		}
		descriptor := file.Fd()
		// dup2 already retargeted ordinary cached fd1/fd2 objects to the
		// anonymous sinks. Only a replaced Go global needs retiring here.
		if descriptor == 1 || descriptor == 2 || seen[descriptor] {
			continue
		}
		if descriptor == null.Fd() || descriptor == pipe.Fd() || descriptor == os.Stdin.Fd() {
			return errors.New("engine_capture_failed")
		}
		seen[descriptor] = true
		if err := file.Close(); err != nil {
			return errors.New("engine_capture_failed")
		}
	}
	return nil
}
