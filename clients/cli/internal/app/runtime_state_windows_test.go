//go:build windows

package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"zongheng-vpn/shared/paths"
)

func TestLogoutRetainsCredentialsWhenLifetimeOwnerLostMetadata(t *testing.T) {
	root, err := paths.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := paths.FromRoot(root)
	if err := ctx.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(ctx.WireGuardKeyPath), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ctx.ConfigPath, ctx.WireGuardKeyPath} {
		if err := os.WriteFile(path, []byte("synthetic retained credential"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	lockPath := filepath.Join(ctx.RunDir, "engine.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rootSD, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := rootSD.Owner()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + owner.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, _ := sd.DACL()
	if err := windows.SetNamedSecurityInfo(lockPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		t.Fatal(err)
	}
	output := captureStdout(t, func() {
		if err := logout(ctx, []string{"--json"}); !errors.Is(err, ErrSilent) {
			t.Fatalf("logout did not fail explicitly: %v", err)
		}
	})
	var result jsonResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.OK || result.ErrorCode != "engine_control_unavailable" {
		t.Fatalf("logout reported success with missing active identity: %+v", result)
	}
	for _, path := range []string{ctx.ConfigPath, ctx.WireGuardKeyPath} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "synthetic retained credential" {
			t.Fatal("logout removed credentials of unresolved lifetime owner")
		}
	}
}
