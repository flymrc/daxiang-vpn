//go:build windows

package app

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
	"zongheng-vpn/shared/paths"
)

func TestLegacyClientSecretsWithoutPrivateDACLRefuseRead(t *testing.T) {
	home := privateTestHome(t)
	if err := os.WriteFile(home.ConfigPath, []byte("license:\n  token: synthetic-legacy-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPrivateConfig(home); err == nil {
		t.Fatal("inherited legacy secret accepted")
	}
	if err := savePrivateConfig(home, testClientConfig("synthetic-new-secret")); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadPrivateConfig(home)
	if err != nil || cfg.License.Token != "synthetic-new-secret" {
		t.Fatal("protected replacement cannot be read")
	}
}

func preparePrivateTestHome(t *testing.T, home paths.Context) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(home.Root, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	private, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + owner.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := private.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(home.Root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}
