//go:build !windows

package proxy

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"syscall"

	"zongheng-vpn/shared/paths"
)

func secureDirectory(ctx paths.Context, path string) error {
	if err := validateRuntimePath(ctx, path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("运行目录必须是实际目录")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("运行目录不属于当前用户")
	}
	if err := validateRuntimePath(ctx, path); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}

func protectFile(ctx paths.Context, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("运行文件不是当前用户的单链接普通文件")
	}
	if err := file.Chmod(0600); err != nil {
		return err
	}
	return verifyPrivateFile(ctx, file)
}

func verifyPrivateFile(_ paths.Context, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("运行凭据的用户或访问权限不安全")
	}
	return nil
}

func openLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openPrivateRead(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openExistingLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
