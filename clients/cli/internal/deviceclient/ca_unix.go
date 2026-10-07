//go:build !windows

package deviceclient

import (
	"golang.org/x/sys/unix"
	"os"
	"strings"
)

func openCA(path string) (*os.File, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return nil, errJSON
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		f.Close()
		return nil, errJSON
	}
	return f, nil
}
