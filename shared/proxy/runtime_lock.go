package proxy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"zongheng-vpn/shared/paths"
)

var errLockBusy = errors.New("客户端目录正在被另一操作使用")

// File locks are owned by a handle, not a scheduler thread or Windows session.
// Keep the lock files in place: deleting a locked file would split ownership.
func acquireRuntimeLock(ctx paths.Context, name string, timeout time.Duration) (*os.File, error) {
	if err := secureDirectory(ctx, ctx.RunDir); err != nil {
		return nil, err
	}
	path := filepath.Join(ctx.RunDir, name+".lock")
	file, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	if err := protectFile(ctx, file); err != nil {
		file.Close()
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := lockFile(file); err == nil {
			return file, nil
		} else if !errors.Is(err, errLockBusy) {
			file.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			file.Close()
			return nil, errLockBusy
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func releaseRuntimeLock(file *os.File) {
	_ = unlockFile(file)
	_ = file.Close()
}

// Observing status never creates or repairs a lock. A running engine always
// owns an existing protected lifetime lock, even if its state files are lost.
func acquireExistingInstanceLock(ctx paths.Context) (*os.File, error) {
	if err := validateRuntimePath(ctx, ctx.RunDir); err != nil {
		return nil, err
	}
	file, err := openExistingLockFile(filepath.Join(ctx.RunDir, "engine.lock"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := verifyPrivateFile(ctx, file); err != nil {
		file.Close()
		return nil, err
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// WithOperationLock serializes configuration and lifecycle transactions for
// one canonical home, including callers from other Windows login sessions.
func WithOperationLock(ctx paths.Context, fn func() error) error {
	root, err := paths.CanonicalRoot(ctx.Root)
	if err != nil {
		return err
	}
	lock, err := acquireRuntimeLock(paths.FromRoot(root), "operations", 30*time.Second)
	if err != nil {
		return fmt.Errorf("获取客户端操作锁失败：%w", err)
	}
	defer releaseRuntimeLock(lock)
	return fn()
}
