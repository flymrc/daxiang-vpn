package proxy

import (
	"context"
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
	return acquireRuntimeLockContext(context.Background(), ctx, name, timeout)
}

func acquireRuntimeLockContext(operation context.Context, ctx paths.Context, name string, timeout time.Duration) (*os.File, error) {
	if operation == nil {
		return nil, errors.New("操作 context 缺失")
	}
	if err := operation.Err(); err != nil {
		return nil, err
	}
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
		if err := operation.Err(); err != nil {
			file.Close()
			return nil, err
		}
		if err := lockFile(file); err == nil {
			if err := operation.Err(); err != nil {
				releaseRuntimeLock(file)
				return nil, err
			}
			return file, nil
		} else if !errors.Is(err, errLockBusy) {
			file.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			file.Close()
			return nil, errLockBusy
		}
		pause := min(25*time.Millisecond, time.Until(deadline))
		timer := time.NewTimer(pause)
		select {
		case <-operation.Done():
			timer.Stop()
			file.Close()
			return nil, operation.Err()
		case <-timer.C:
		}
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
	return WithOperationLockContext(context.Background(), ctx, fn)
}

// WithOperationLockContext uses the same ownership lock as existing callers.
// Cancellation before acquisition never invokes fn; it cannot interrupt fn.
func WithOperationLockContext(operation context.Context, ctx paths.Context, fn func() error) error {
	if operation == nil || fn == nil {
		return errors.New("操作 context 或回调缺失")
	}
	if err := operation.Err(); err != nil {
		return err
	}
	root, err := paths.CanonicalRoot(ctx.Root)
	if err != nil {
		return err
	}
	lock, err := acquireRuntimeLockContext(operation, paths.FromRoot(root), "operations", 30*time.Second)
	if err != nil {
		return fmt.Errorf("获取客户端操作锁失败：%w", err)
	}
	defer releaseRuntimeLock(lock)
	if err := operation.Err(); err != nil {
		return err
	}
	return fn()
}
