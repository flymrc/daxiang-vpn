//go:build windows

package proxy

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func engineLogSecurity(t *testing.T, path string) string {
	t.Helper()
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	return sd.String()
}
func TestEngineLogWindowsJunctionRejectsWithoutExternalMutation(t *testing.T) {
	for _, at := range []string{"logs", "namespace"} {
		t.Run(at, func(t *testing.T) {
			ctx := engineLogTestHome(t)
			external := t.TempDir()
			sentinel := filepath.Join(external, "legacy.log")
			raw := []byte("external SECRET_CANARY")
			os.WriteFile(sentinel, raw, 0600)
			before := engineLogSecurity(t, external)
			fileBefore := engineLogSecurity(t, sentinel)
			junction := ctx.LogDir
			if at == "namespace" {
				if e := os.Mkdir(ctx.LogDir, 0700); e != nil {
					t.Fatal(e)
				}
				junction = filepath.Join(ctx.LogDir, "engine-events-v1")
			}
			command := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, external)
			if out, e := command.CombinedOutput(); e != nil {
				t.Fatalf("junction: %v %s", e, out)
			}
			defer os.Remove(junction)
			if l, e := OpenEngineLog(ctx, EngineBuildInfo{}); e == nil {
				l.Close()
				t.Fatal("junction accepted")
			}
			if engineLogSecurity(t, external) != before || engineLogSecurity(t, sentinel) != fileBefore {
				t.Fatal("external ACL changed")
			}
			after, _ := os.ReadFile(sentinel)
			if !bytes.Equal(raw, after) {
				t.Fatal("external data changed")
			}
		})
	}
}
func TestEngineLogWindowsHardlinkRejectsWithoutACLOrDataChange(t *testing.T) {
	for _, name := range []string{"owner.v1", "events-0.v1"} {
		t.Run(name, func(t *testing.T) {
			ctx := engineLogTestHome(t)
			l, e := OpenEngineLog(ctx, EngineBuildInfo{})
			if e != nil {
				t.Fatal(e)
			}
			l.Close()
			slot := filepath.Join(ctx.LogDir, "engine-events-v1", name)
			external := filepath.Join(t.TempDir(), "external.log")
			if e = os.Link(slot, external); e != nil {
				t.Fatal(e)
			}
			before := engineLogSecurity(t, external)
			data, _ := os.ReadFile(external)
			if l, e := OpenEngineLog(ctx, EngineBuildInfo{}); e == nil {
				l.Close()
				t.Fatal("hardlink accepted")
			}
			after, _ := os.ReadFile(external)
			if !bytes.Equal(data, after) || engineLogSecurity(t, external) != before {
				t.Fatal("external hardlink modified")
			}
		})
	}
}
func TestEngineLogWindowsUnsafeACLRejectsWithoutRepair(t *testing.T) {
	for _, name := range []string{"namespace", "owner.v1", "events-0.v1"} {
		t.Run(name, func(t *testing.T) {
			ctx := engineLogTestHome(t)
			l, e := OpenEngineLog(ctx, EngineBuildInfo{})
			if e != nil {
				t.Fatal(e)
			}
			l.Close()
			p := filepath.Join(ctx.LogDir, "engine-events-v1")
			if name != "namespace" {
				p = filepath.Join(p, name)
			}
			owner, e := runtimeOwner(ctx)
			if e != nil {
				t.Fatal(e)
			}
			sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + owner.String() + ")(A;;FA;;;WD)")
			if e != nil {
				t.Fatal(e)
			}
			acl, _, e := sd.DACL()
			if e != nil {
				t.Fatal(e)
			}
			if e = windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
				t.Fatal(e)
			}
			before := engineLogSecurity(t, p)
			var raw []byte
			if name != "namespace" {
				raw, _ = os.ReadFile(p)
			}
			if l, e := OpenEngineLog(ctx, EngineBuildInfo{}); e == nil {
				l.Close()
				t.Fatal("unsafe ACL accepted")
			}
			if engineLogSecurity(t, p) != before {
				t.Fatal("unknown ACL repaired")
			}
			if name != "namespace" {
				after, _ := os.ReadFile(p)
				if !bytes.Equal(raw, after) {
					t.Fatal("unsafe file data changed")
				}
			}
		})
	}
}
func TestEngineLogWindowsHeldNamespaceForbidsReplacementRace(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(ctx.LogDir, "engine-events-v1")
	moved := p + "-moved"
	succeeded := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if os.Rename(p, moved) == nil {
				select {
				case succeeded <- struct{}{}:
				default:
				}
				return
			}
		}
	}()
	for i := 0; i < 100; i++ {
		if e = l.Append(EngineLogEvent{Event: EngineLogReady}); e != nil {
			t.Fatal(e)
		}
	}
	wg.Wait()
	select {
	case <-succeeded:
		t.Fatal("held namespace replaced")
	default:
	}
	l.Close()
	if e = os.Rename(p, moved); e != nil {
		t.Fatalf("held handles leaked after Close: %v", e)
	}
}
func TestEngineLogWindowsRuntimeACLFailureSticky(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	p := l.slots[l.current].file.Name()
	owner, e := runtimeOwner(ctx)
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + owner.String() + ")(A;;FR;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, _ := sd.DACL()
	if e = windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	before := engineLogSecurity(t, p)
	raw, _ := os.ReadFile(p)
	if e = l.Append(EngineLogEvent{Event: EngineLogReady}); e == nil || e.Error() != string(EngineLogCodeNamespace) {
		t.Fatal("unsafe runtime file accepted")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(raw, after) || engineLogSecurity(t, p) != before {
		t.Fatal("runtime denial repaired or wrote")
	}
	if l.State() != (EngineLogState{State: "degraded", Code: EngineLogCodeNamespace}) {
		t.Fatal(l.State())
	}
}
