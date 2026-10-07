//go:build windows

package updateclient

import (
	"golang.org/x/sys/windows"
	"testing"
	"zongheng-vpn/shared/paths"
)

// Only synthetic newly created directories are protected. Production/profile
// ACLs are never repaired by these fixtures or the update command.
func privateHome(t *testing.T) paths.Context {
	t.Helper()
	root, e := paths.CanonicalRoot(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if e != nil {
		t.Fatal(e)
	}
	dacl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); e != nil {
		t.Fatal(e)
	}
	return paths.FromRoot(root)
}
