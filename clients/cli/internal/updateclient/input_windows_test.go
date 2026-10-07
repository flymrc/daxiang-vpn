//go:build windows

package updateclient

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsInputSyntaxRefusesNetworkDevicesAndAliasesBeforePathAccess(t *testing.T) {
	for _, path := range []string{`\\synthetic-host\share\artifact`, `//synthetic-host/share/artifact`, `\\?\C:\artifact`, `\\?\UNC\synthetic-host\share\artifact`, `\\.\NUL`, `\??\C:\artifact`, `C:relative`, `C:\artifact:stream`, `NUL`, `folder\CON.txt`, `C:\x\COM1.`, `C:\x\LPT9 `, `CONOUT$`, `/rooted-relative`} {
		if ordinaryWindowsInputPath(path) {
			t.Fatalf("unsafe Windows input syntax allowed")
		}
	}
	for _, path := range []string{`artifact.bin`, `folder\artifact.bin`, `.\artifact.bin`, `C:\folder\artifact.bin`, `C:/folder/artifact.bin`} {
		if !ordinaryWindowsInputPath(path) {
			t.Fatal("ordinary local syntax refused")
		}
	}
	path := filepath.Join(t.TempDir(), "public-artifact.bin")
	if e := os.WriteFile(path, []byte("approved-public-input"), 0600); e != nil {
		t.Fatal(e)
	}
	f, _, e := openInput(path, 100)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, access := range []uint32{windows.GENERIC_WRITE, windows.DELETE} {
		h, e := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if e == nil {
			windows.CloseHandle(h)
			t.Fatal("opened public input permitted write/delete handle while pinned")
		}
	}
}

func TestWindowsPublicInputAllowsHardlinkButPrivateAuthorityRejectsIt(t *testing.T) {
	f := newFixture(t)
	linked := filepath.Join(f.root, "public-hardlink.bin")
	if e := os.Link(f.artifact, linked); e != nil {
		t.Fatal(e)
	}
	input, _, e := openInput(linked, 1<<20)
	if e != nil {
		t.Fatal("public content hash input should allow hardlinks")
	}
	input.Close()
	home := privateHome(t)
	if !run(t, home, f.approvalArgs(t, "enroll")).OK {
		t.Fatal("enroll")
	}
	if e = os.Link(filepath.Join(home.Root, StateName), filepath.Join(home.Root, "state-alias.json")); e != nil {
		t.Fatal(e)
	}
	expectCode(t, run(t, home, append([]string{"inspect"}, scopeArgs(f.scope)...)), "local_storage_failure")
}
