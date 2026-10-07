//go:build !windows

package proxy

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
	"zongheng-vpn/shared/paths"
)

func TestEngineLogUnixSymlinkHardlinkFIFOAndUnsafeModesPreserved(t *testing.T) {
	for _, shape := range []string{"logs symlink", "namespace symlink", "hardlink owner", "hardlink slot", "file symlink", "fifo", "unsafe mode"} {
		t.Run(shape, func(t *testing.T) {
			ctx := engineLogTestHome(t)
			external := filepath.Join(t.TempDir(), "external.log")
			data := []byte("external SECRET_CANARY\n")
			os.WriteFile(external, data, 0600)
			before, _ := os.Stat(external)
			if shape == "logs symlink" {
				os.Symlink(filepath.Dir(external), ctx.LogDir)
			} else if shape == "namespace symlink" {
				os.Mkdir(ctx.LogDir, 0700)
				os.Symlink(filepath.Dir(external), filepath.Join(ctx.LogDir, "engine-events-v1"))
			} else {
				l, e := OpenEngineLog(ctx, EngineBuildInfo{})
				if e != nil {
					t.Fatal(e)
				}
				l.Close()
				p := filepath.Join(ctx.LogDir, "engine-events-v1", "events-0.v1")
				if shape == "hardlink owner" {
					p = filepath.Join(ctx.LogDir, "engine-events-v1", "owner.v1")
				}
				switch shape {
				case "hardlink owner", "hardlink slot":
					os.Remove(p)
					if e = os.Link(external, p); e != nil {
						t.Fatal(e)
					}
				case "file symlink":
					os.Remove(p)
					os.Symlink(external, p)
				case "fifo":
					os.Remove(p)
					if e = unix.Mkfifo(p, 0600); e != nil {
						t.Fatal(e)
					}
				case "unsafe mode":
					os.Chmod(p, 0644)
				}
			}
			if l, e := OpenEngineLog(ctx, EngineBuildInfo{}); e == nil {
				l.Close()
				t.Fatal("unsafe shape accepted")
			}
			after, _ := os.Stat(external)
			raw, _ := os.ReadFile(external)
			if before.Mode() != after.Mode() || !bytes.Equal(data, raw) {
				t.Fatal("external data or permissions mutated")
			}
		})
	}
}
func TestEngineLogUnixHeldNamespaceReplacementDetected(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	p := filepath.Join(ctx.LogDir, "engine-events-v1")
	old := p + "-old"
	if e = os.Rename(p, old); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(p, 0700); e != nil {
		t.Fatal(e)
	}
	external := filepath.Join(p, "events-0.v1")
	raw := []byte("new unknown data")
	os.WriteFile(external, raw, 0600)
	if e = l.Append(EngineLogEvent{Event: EngineLogReady}); e == nil || e.Error() != string(EngineLogCodeNamespace) {
		t.Fatal("directory replacement accepted")
	}
	after, _ := os.ReadFile(external)
	if !bytes.Equal(raw, after) {
		t.Fatal("replacement directory modified")
	}
}
func TestEngineLogUnixRuntimeHardlinkRejectsBeforeWrite(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	p := filepath.Join(ctx.LogDir, "engine-events-v1", engineLogSlotName(l.current))
	external := filepath.Join(t.TempDir(), "linked.log")
	if e = os.Link(p, external); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(external)
	info, _ := os.Stat(external)
	if e = l.Append(EngineLogEvent{Event: EngineLogReady}); e == nil {
		t.Fatal("live hardlink accepted")
	}
	after, _ := os.ReadFile(external)
	afterInfo, _ := os.Stat(external)
	if !bytes.Equal(raw, after) || info.Mode() != afterInfo.Mode() {
		t.Fatal("live external hardlink changed")
	}
}

func TestEngineLogUnixAncestorPermissionWideningRejectedWithoutRepair(t *testing.T) {
	ancestor := t.TempDir()
	root := filepath.Join(ancestor, "private-parent", "home")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	ctx := paths.FromRoot(root)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	p := filepath.Join(ctx.LogDir, "engine-events-v1", engineLogSlotName(l.current))
	before, _ := os.ReadFile(p)
	if e = os.Chmod(ancestor, 0777); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(ancestor, 0700)
	if e = l.Append(EngineLogEvent{Event: EngineLogReady}); e == nil || e.Error() != string(EngineLogCodeNamespace) {
		t.Fatal("widened ancestor accepted")
	}
	info, _ := os.Stat(ancestor)
	if info.Mode().Perm() != 0777 {
		t.Fatal("ancestor automatically repaired")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("logged through unsafe namespace")
	}
}
