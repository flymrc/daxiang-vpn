//go:build windows

package deviceauth

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

// Only newly owned synthetic fixture objects are changed; never production or
// external paths. Call before introducing a hardlink in refusal fixtures.
func restoreFixturePrivate(t *testing.T, path string) {
	t.Helper()
	sd, err := restoreWindowsSD()
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreProtectedReaderAcceptsOwnedFixture(t *testing.T) {
	dir := restoreTestDir(t)
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	restoreFixturePrivate(t, path)
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		f, e := restoreWindowsOpen(parent, true)
		if e != nil {
			t.Fatalf("ancestor open: %v", e)
		}
		e = restoreWindowsSecurity(windows.Handle(f.Fd()), parent == dir)
		f.Close()
		if e != nil {
			t.Fatalf("ancestor security: depth path basename %s: %v", filepath.Base(parent), e)
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	f, e := restoreWindowsOpen(path, false)
	if e != nil {
		t.Fatalf("file open: %v", e)
	}
	r := &restoreInput{file: f}
	r.info, e = f.Stat()
	if e != nil {
		f.Close()
		t.Fatal(e)
	}
	e = restoreVerifyPinned(r)
	f.Close()
	if e != nil {
		t.Fatalf("pinned file security: %v", e)
	}
	input, _, e := restoreOpenInput(path, 100)
	if e != nil {
		t.Fatal(e)
	}
	input.Close()
}

func TestRestoreWindowsRefusesUntrustedDACLWithoutRepair(t *testing.T) {
	for _, target := range []string{"file", "parent"} {
		t.Run(target, func(t *testing.T) {
			dir := restoreTestDir(t)
			path := filepath.Join(dir, "input.json")
			if err := os.WriteFile(path, []byte("private fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			restoreFixturePrivate(t, path)
			unsafePath := path
			if target == "parent" {
				unsafePath = dir
			}
			sid, err := restoreWindowsSID()
			if err != nil {
				t.Fatal(err)
			}
			sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FR;;;WD)")
			if err != nil {
				t.Fatal(err)
			}
			acl, _, err := sd.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err = windows.SetNamedSecurityInfo(unsafePath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			before, err := windows.GetNamedSecurityInfo(unsafePath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			if input, _, err := restoreOpenInput(path, 100); err == nil {
				input.Close()
				t.Fatal("untrusted DACL accepted")
			}
			after, err := windows.GetNamedSecurityInfo(unsafePath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil || before.String() != after.String() {
				t.Fatal("planner altered an untrusted input ACL")
			}
		})
	}
}

func TestRestoreWindowsPinnedInputBlocksWriteDeleteAndRename(t *testing.T) {
	dir := restoreTestDir(t)
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	restoreFixturePrivate(t, path)
	input, _, err := restoreOpenInput(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err = os.WriteFile(path, []byte("changed fixture"), 0600); err == nil {
		t.Fatal("source writer was not excluded")
	}
	if err = os.Remove(path); err == nil {
		t.Fatal("source delete was not excluded")
	}
	if err = os.Rename(path, filepath.Join(dir, "moved.json")); err == nil {
		t.Fatal("source rename was not excluded")
	}
	if err = os.Rename(dir, dir+"-moved"); err == nil {
		t.Fatal("private source namespace rename was not excluded")
	}
	if err = input.unchanged(); err != nil {
		t.Fatal("failed concurrent operations changed pinned input")
	}
}
