//go:build !windows

package updateclient

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnixInputNoFollowAndNonblockingFIFORefusal(t *testing.T) {
	f := newFixture(t)
	link := filepath.Join(f.root, "input-link")
	if e := os.Symlink(f.artifact, link); e != nil {
		t.Fatal(e)
	}
	if input, _, e := openInput(link, 1<<20); e == nil {
		input.Close()
		t.Fatal("public leaf symlink accepted")
	}
	fifo := filepath.Join(f.root, "fifo")
	if e := unix.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	input, e := openRegularNoFollow(fifo)
	if e == nil {
		input.Close()
		t.Fatal("FIFO opened as regular")
	}
	if time.Since(start) > time.Second {
		t.Fatal("FIFO replacement could hang safe opener")
	}
	linked := filepath.Join(f.root, "public-hardlink")
	if e := os.Link(f.artifact, linked); e != nil {
		t.Fatal(e)
	}
	input, _, e = openInput(linked, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	input.Close()
	home := privateHome(t)
	if !run(t, home, f.approvalArgs(t, "enroll")).OK {
		t.Fatal("enroll")
	}
	if e = os.Chmod(filepath.Join(home.Root, StateName), 0644); e != nil {
		t.Fatal(e)
	}
	expectCode(t, run(t, home, append([]string{"inspect"}, scopeArgs(f.scope)...)), "local_storage_failure")
}
