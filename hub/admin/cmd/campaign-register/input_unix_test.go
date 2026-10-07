//go:build !windows

package main

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCampaignRegisterNativeFIFOAndSymlinkRefuseBeforeDatabase(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "register")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, e := build.CombinedOutput(); e != nil {
		t.Fatalf("owned command build %s", b)
	}
	input, digest, _ := registerFixture(t)
	dir := t.TempDir()
	fifo := filepath.Join(dir, "owned.fifo")
	if e := unix.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(dir, "owned.symlink")
	if e := os.Symlink(input, link); e != nil {
		t.Fatal(e)
	}
	parentAlias := filepath.Join(dir, "parent-alias")
	if e := os.Symlink(filepath.Dir(input), parentAlias); e != nil {
		t.Fatal(e)
	}
	for index, source := range []string{fifo, link, filepath.Join(parentAlias, filepath.Base(input)), "/dev/null"} {
		dbdir := filepath.Join(dir, string(rune('a'+index)))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, bin, registerArgs(filepath.Join(dbdir, "db.sqlite"), source, digest)...)
		_, e := cmd.Output()
		cancel()
		if e == nil || ctx.Err() == context.DeadlineExceeded {
			t.Fatal("unsafe source accepted or blocked")
		}
		if _, e := os.Lstat(dbdir); !os.IsNotExist(e) {
			t.Fatal("unsafe source mutated database")
		}
	}
}
