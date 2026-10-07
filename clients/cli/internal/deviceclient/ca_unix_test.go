//go:build !windows

package deviceclient

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCARefusesFIFOAndSymlinkWithoutWaitingOrChangingSource(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "ca-fifo")
	if e := unix.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	_, e := loadRoots(context.Background(), fifo)
	if FailureCode(e) != "invalid_ca_file" || time.Since(start) > time.Second {
		t.Fatal("FIFO CA blocked or was accepted")
	}
	a := authority(t)
	link := filepath.Join(root, "linked-ca.pem")
	if e = os.Symlink(a.caFile, link); e != nil {
		t.Fatal(e)
	}
	_, e = loadRoots(context.Background(), link)
	if FailureCode(e) != "invalid_ca_file" {
		t.Fatal("symlink CA accepted")
	}
	if info, e := os.Lstat(fifo); e != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatal("CA validation modified FIFO")
	}
}
