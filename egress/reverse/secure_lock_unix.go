//go:build !windows

package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"syscall"
)

func lockSecureRegistry(path string) (*os.File, error) {
	if err := validateSecureDirectoryPath(path); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path+".lock")
	if err := validateSecureFileHandle(f); err != nil {
		f.Close()
		return nil, err
	}
	if err := validateSecureAuthorityHandle(f); err != nil {
		f.Close()
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// File modes do not protect a replaceable pathname. In particular, an attacker
// who can rename the parent can replace a lifetime lock or replay an archived
// service-owned registry/watermark without changing those files' inode modes.
// Keep the immediate namespace private and every ancestor service/root owned.
func validateSecureDirectoryPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	immediate := true
	uid := uint32(os.Geteuid())
	for {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("secure directory namespace must contain no symlink or non-directory")
		}
		if immediate {
			if stat.Uid != uid || info.Mode().Perm()&0077 != 0 || info.Mode().Perm()&0500 != 0500 {
				return errors.New("secure file parent must be service-owned and private (0700 recommended)")
			}
		} else {
			if stat.Uid != uid && stat.Uid != 0 {
				return errors.New("secure directory ancestor must be service/root-owned")
			}
			if info.Mode().Perm()&0022 != 0 && !(stat.Uid == 0 && info.Mode()&os.ModeSticky != 0) {
				return errors.New("secure directory ancestor permits untrusted namespace replacement")
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir, immediate = parent, false
	}
}

func validateSecureFileHandle(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Nlink != 1 {
		return errors.New("secure file must be regular and have one link")
	}
	return nil
}

func validateSecurePrivateKeyHandle(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return errors.New("private key must be owned by the service user and not accessible by group/other")
	}
	return nil
}

func validateSecureAuthorityHandle(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != uint32(os.Geteuid()) && stat.Uid != 0) || info.Mode().Perm()&0022 != 0 {
		return errors.New("secure authority must be service/root-owned and not writable by group/other")
	}
	return nil
}

func syncSecureDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func replaceSecureFile(from, to string) error { return os.Rename(from, to) }
