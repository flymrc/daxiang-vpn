//go:build !windows

package main

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
)

func locationAllowed(path string) bool {
	if path == "" || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "//") {
		return false
	}
	for _, prefix := range []string{"/dev", "/proc", "/sys"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return false
		}
	}
	return true
}
func localFilesystem(fs *unix.Statfs_t) bool {
	// Explicit local filesystem allowlist; unknown/FUSE/9p/network mounts refuse.
	switch uint64(fs.Type) {
	case 0xef53, 0x01021994, 0x9123683e, 0x58465342, 0x794c7630, 0x4d44, 0x5346544e, 0x1a, 0x11:
		return true
	}
	return false
}
func ordinaryDirectory(path string) bool {
	var fs unix.Statfs_t
	return unix.Statfs(path, &fs) == nil && localFilesystem(&fs)
}
func openOrdinary(path string) (*os.File, error) {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return nil, invalidInput
	}
	parts := strings.Split(strings.TrimPrefix(absolute, "/"), "/")
	dir, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	defer func() { unix.Close(dir) }()
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(dir, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return nil, e
		}
		unix.Close(dir)
		dir = next
		var fs unix.Statfs_t
		if unix.Fstatfs(dir, &fs) != nil || !localFilesystem(&fs) {
			return nil, invalidInput
		}
	}
	fd, e := unix.Openat(dir, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		f.Close()
		return nil, invalidInput
	}
	var fs unix.Statfs_t
	if unix.Fstatfs(fd, &fs) != nil || !localFilesystem(&fs) {
		f.Close()
		return nil, invalidInput
	}
	return f, nil
}
