//go:build !windows

package updateclient

import (
	"golang.org/x/sys/unix"
	"os"
	"strings"
)

func inputLocationAllowed(path string) bool { return path != "" && !strings.ContainsRune(path, 0) }

func openRegularNoFollow(path string) (*os.File, error) {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fail("invalid_input_file")
	}
	return f, nil
}
