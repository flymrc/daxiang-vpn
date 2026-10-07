package proxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zongheng-vpn/shared/paths"
)

func TestOperationLockPreCancellationDoesNotCreateHome(t *testing.T) {
	home := paths.FromRoot(filepath.Join(t.TempDir(), "never-created"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := WithOperationLockContext(ctx, home, func() error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("pre-cancel admitted callback: error=%v called=%v", err, called)
	}
	if _, err := os.Stat(home.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pre-cancel created or accessed the home")
	}
}

func TestOperationLockContextSharesOwnershipAndCancelsContention(t *testing.T) {
	home := paths.FromRoot(t.TempDir())
	prepareTestHome(t, home)
	owned, err := acquireRuntimeLock(home, "operations", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if owned != nil {
			releaseRuntimeLock(owned)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	called := false
	started := time.Now()
	err = WithOperationLockContext(ctx, home, func() error { called = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || called || time.Since(started) > 2*time.Second {
		t.Fatalf("contention did not honor context: error=%v called=%v", err, called)
	}
	releaseRuntimeLock(owned)
	owned = nil
	err = WithOperationLockContext(context.Background(), home, func() error { called = true; return nil })
	if err != nil || !called {
		t.Fatalf("cancelled contender retained ownership: %v", err)
	}
}
