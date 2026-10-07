//go:build windows

package deviceclient

import (
	"bytes"
	"context"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"zongheng-vpn/shared/paths"
)

// Change only synthetic directories created by this test, never the real user
// profile. The normal client resolver keeps rejecting an unsafe existing ACL.
func privateHome(t *testing.T) paths.Context {
	t.Helper()
	root, e := paths.CanonicalRoot(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if e != nil {
		t.Fatal(e)
	}
	a, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, a, nil); e != nil {
		t.Fatal(e)
	}
	return paths.FromRoot(root)
}

func TestUnsafeHomeACLIsRefusedWithoutRepairOrNetwork(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(home.Root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	before, e := windows.GetNamedSecurityInfo(home.Root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	e = Run(context.Background(), home, []string{"activate", "--server", a.server.URL, "--activation-stdin", "--ca-file", a.caFile}, strings.NewReader("synthetic-activation"), &out, io.Discard)
	r, de := DecodeReceipt(out.Bytes())
	if e == nil || de != nil || r.Code != "local_storage_failure" {
		t.Fatal("unsafe home accepted")
	}
	after, e := windows.GetNamedSecurityInfo(home.Root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if e != nil || after.String() != before.String() {
		t.Fatal("unsafe existing home ACL changed")
	}
	if _, e = os.Stat(filepath.Join(home.Root, "device-v2-state.json")); !os.IsNotExist(e) {
		t.Fatal("unsafe home received private key")
	}
	if n, ch := a.counts(); n != 0 || ch != 0 {
		t.Fatal("unsafe local ACL reached network")
	}
}
