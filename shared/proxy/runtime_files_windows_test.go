//go:build windows

package proxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"zongheng-vpn/shared/paths"
)

func TestWindowsRuntimeRejectsJunctionBeforeExternalACLChange(t *testing.T) {
	root, err := paths.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := paths.FromRoot(root)
	prepareTestHome(t, ctx)
	external := t.TempDir()
	before, err := windows.GetNamedSecurityInfo(external, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("cmd.exe", "/c", "mklink", "/J", ctx.RunDir, external)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create isolated junction: %v %s", err, output)
	}
	defer os.Remove(ctx.RunDir)
	if err := secureDirectory(ctx, ctx.RunDir); err == nil {
		t.Fatal("external junction was accepted")
	}
	after, err := windows.GetNamedSecurityInfo(external, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatal("rejected junction changed external ACL")
	}
}

func TestWindowsRuntimeRejectsHardlinkBeforeExternalACLChange(t *testing.T) {
	root, err := paths.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := paths.FromRoot(root)
	prepareTestHome(t, ctx)
	if err := ctx.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "unrelated.txt")
	content := []byte("unrelated external content")
	if err := os.WriteFile(external, content, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(external, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(external, filepath.Join(ctx.RunDir, "operations.lock")); err != nil {
		t.Fatal(err)
	}
	entered := false
	if err := WithOperationLock(ctx, func() error { entered = true; return nil }); err == nil || entered {
		t.Fatal("hardlinked lock was accepted")
	}
	after, err := windows.GetNamedSecurityInfo(external, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatal("rejected hardlink changed external ACL")
	}
	actual, err := os.ReadFile(external)
	if err != nil || string(actual) != string(content) {
		t.Fatal("rejected hardlink changed external content")
	}
	if err := os.Link(external, statePath(ctx)); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateFile(ctx, statePath(ctx)); err == nil {
		t.Fatal("multi-link credential file was readable")
	}
}

func prepareTestHome(t *testing.T, ctx paths.Context) {
	t.Helper()
	// TEMP on this host grants other users modification rights. Only the
	// manufactured home is made private; external hardlink/junction targets
	// keep their original unprotected ACLs, which the negative tests compare.
	acl, err := runtimeDACL(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(ctx.Root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRuntimeRejectsForeignOwnerEvenWithRestrictedDACL(t *testing.T) {
	ctx, _, _ := syntheticRuntime(t)
	owner, err := runtimeOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic SD verifies the ownership rule without changing a real user
	// account's objects. Cross-account UAC remains a platform acceptance gap.
	sd, err := windows.SecurityDescriptorFromString("O:WDD:P(A;;FA;;;" + owner.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyRuntimeDACL(ctx, sd); err == nil {
		t.Fatal("foreign owner retained implicit WRITE_DAC authority")
	}
}

func TestWindowsRuntimeRejectsPermissiveCredentialACL(t *testing.T) {
	ctx, _, _ := syntheticRuntime(t)
	path := filepath.Join(ctx.RunDir, "synthetic-secret.json")
	if err := writePrivateFile(ctx, path, []byte(`{"synthetic":true}`)); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, _ := sd.DACL()
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateFile(ctx, path); err == nil {
		t.Fatal("Everyone-readable credentials were accepted")
	}
}
