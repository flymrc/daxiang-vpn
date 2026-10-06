//go:build windows

package systemproxy

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "--system-proxy-lock-helper" {
		p := &windowsPlatform{knownFolder: func() (string, error) { return os.Args[2], nil }}
		scope, err := p.Resolve(context.Background(), os.Args[3])
		if err == nil {
			err = p.WithLocked(context.Background(), scope, func(Store) error { fmt.Println("locked"); bufio.NewScanner(os.Stdin).Scan(); return nil })
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func privateTestDirectory(t *testing.T, path string) {
	t.Helper()
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := userSecurity(sid, true)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	// Only newly manufactured test directories are adjusted. No profile,
	// Internet Settings, external link target or production directory is changed.
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := verifyDirectory(path, sid); err != nil {
		t.Fatal(err)
	}
}

func syntheticPlatform(t *testing.T) (*windowsPlatform, Scope) {
	t.Helper()
	folder, home := t.TempDir(), t.TempDir()
	privateTestDirectory(t, folder)
	privateTestDirectory(t, home)
	keyPath := fmt.Sprintf(`Software\ZonghengVPNTests\SystemProxy-%d-%d`, os.Getpid(), time.Now().UnixNano())
	if !strings.HasPrefix(keyPath, `Software\ZonghengVPNTests\SystemProxy-`) || strings.Contains(keyPath, "Internet Settings") {
		t.Fatal("invalid synthetic registry prefix")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	key.Close()
	t.Cleanup(func() {
		if err := registry.DeleteKey(registry.CURRENT_USER, keyPath); err != nil {
			t.Error(err)
		}
	})
	p := &windowsPlatform{knownFolder: func() (string, error) { return folder, nil }, keyPath: keyPath, notify: false}
	scope, err := p.Resolve(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	return p, scope
}

func TestWindowsRawAdapterOnlyUsesSyntheticKeyAndPreservesRawValues(t *testing.T) {
	p, scope := syntheticPlatform(t)
	adapter, err := p.Open(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []*Value{nil, {Kind: 3, Bytes: []byte{0, 1, 255}}, {Kind: 1, Bytes: []byte{0, 0}}, {Kind: 0, Bytes: []byte{}}, {Kind: 4, Bytes: []byte{1, 0, 0, 0}}} {
		if err := adapter.Write("ProxyServer", value); err != nil {
			t.Fatal(err)
		}
		actual, err := adapter.Read("ProxyServer")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, value) {
			t.Fatalf("raw value changed: %#v != %#v", actual, value)
		}
	}
	if err := adapter.Notify(); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Write("UnrelatedValue", &Value{Kind: 3}); err == nil {
		t.Fatal("field outside allowlist accepted")
	}
	foreign := scope
	foreign.UserScope = "windows:S-1-5-21-foreign"
	if _, err := p.Open(foreign); err == nil {
		t.Fatal("foreign SID opened HKCU adapter")
	}
	unsafeAdapter := &windowsAdapter{scope: foreign, keyPath: p.keyPath, notify: false}
	if err := unsafeAdapter.Write("ProxyServer", &Value{Kind: 3, Bytes: []byte("foreign")}); err == nil {
		t.Fatal("foreign token wrote HKCU")
	}
}

func TestWindowsStorageKeepsImmutableWALAndLockNamespace(t *testing.T) {
	p, scope := syntheticPlatform(t)
	err := p.WithLocked(context.Background(), scope, func(store Store) error {
		if data, err := store.Load(); err != nil || data != nil {
			t.Fatal("expected absent journal", err)
		}
		if err := store.Create([]byte("original WAL")); err != nil {
			t.Fatal(err)
		}
		if err := store.Create([]byte("replacement")); err == nil {
			t.Fatal("immutable WAL was replaced")
		}
		entered := false
		if err := p.WithLocked(context.Background(), scope, func(Store) error { entered = true; return nil }); err == nil || entered {
			t.Fatal("independent handle entered locked transaction")
		}
		if err := os.Remove(scope.LockPath); err == nil {
			t.Fatal("held lock filename could be deleted")
		}
		if err := os.Rename(scope.LockPath, scope.LockPath+".renamed"); err == nil {
			t.Fatal("held lock filename could be renamed")
		}
		if err := os.Rename(scope.Root, scope.Root+".renamed"); err == nil {
			t.Fatal("held parent directory could split lock namespace")
		}
		data, err := store.Load()
		if err != nil || string(data) != "original WAL" {
			t.Fatal("WAL changed", err)
		}
		return fmt.Errorf("synthetic operation failure")
	})
	if err == nil {
		t.Fatal("action failure swallowed")
	}
	if err := p.WithLocked(context.Background(), scope, func(store Store) error { return store.Remove() }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scope.LockPath); err != nil {
		t.Fatal("lock file deleted on release", err)
	}
}

func TestRealChildLockExcludesTransactionsAndReleasesAfterCrash(t *testing.T) {
	p, scope := syntheticPlatform(t)
	child := exec.Command(os.Args[0], "--system-proxy-lock-helper", filepath.Dir(scope.Root), scope.HomeIdentity)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "locked" {
		t.Fatal("child did not acquire synthetic lock")
	}
	entered := false
	if err := p.WithLocked(context.Background(), scope, func(Store) error { entered = true; return nil }); err == nil || entered {
		t.Fatal("entered another process's transaction")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if err := p.WithLocked(context.Background(), scope, func(Store) error { return nil }); err != nil {
		t.Fatal("crashed process retained lock", err)
	}
	if _, err := os.Stat(scope.LockPath); err != nil {
		t.Fatal("persistent lock disappeared", err)
	}
}

func TestDirectoryReplacementIsRefusedDuringPostLockScopeCheck(t *testing.T) {
	p, scope := syntheticPlatform(t)
	folder := filepath.Dir(scope.Root)
	calls := 0
	attempted := false
	p.knownFolder = func() (string, error) {
		calls++
		if calls == 2 {
			attempted = true
			if err := os.Rename(scope.Root, scope.Root+".replacement"); err == nil {
				t.Fatal("directory could be replaced between kernel lock and callback")
			}
		}
		return folder, nil
	}
	if err := p.WithLocked(context.Background(), scope, func(Store) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !attempted {
		t.Fatal("post-lock scope validation was not executed")
	}
}

func TestMappedRemoteDriveClassificationCannotSplitUserScope(t *testing.T) {
	for _, kind := range []uint32{windows.DRIVE_REMOTE, windows.DRIVE_REMOVABLE, windows.DRIVE_RAMDISK, windows.DRIVE_UNKNOWN, windows.DRIVE_NO_ROOT_DIR} {
		calls := 0
		if fixedLocalPath(`Z:\mapped\profile`, func(root *uint16) uint32 {
			calls++
			if windows.UTF16PtrToString(root) != `Z:\` {
				t.Fatal("did not classify actual drive root")
			}
			return kind
		}) || calls != 1 {
			t.Fatal("non-fixed/mapped volume accepted", kind)
		}
	}
	if !fixedLocalPath(`C:\synthetic\profile`, func(*uint16) uint32 { return windows.DRIVE_FIXED }) {
		t.Fatal("fixed drive was rejected")
	}
}

func TestWindowsScopeIgnoresEnvironmentAndRejectsUNCAndTampering(t *testing.T) {
	platform, err := NewPlatform()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	privateTestDirectory(t, home)
	actualFolder, err := windows.KnownFolderPath(windows.FOLDERID_RoamingAppData, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("ZHVPN_HOME", t.TempDir())
	scope, err := platform.Resolver.Resolve(context.Background(), home)
	// A real profile may have unsafe ACLs; refusal is intentional and must not
	// be "fixed" by mutating the user's folder or loosening the policy.
	if err == nil && !strings.EqualFold(scope.Root, filepath.Join(actualFolder, applicationID)) {
		t.Fatal("environment changed user lease scope")
	}
	production := platform.Resolver.(*windowsPlatform)
	observed, err := production.knownFolder()
	if err != nil || !strings.EqualFold(observed, actualFolder) {
		t.Fatal("environment changed production KnownFolder", err)
	}
	p, synthetic := syntheticPlatform(t)
	resolved, err := p.Resolve(context.Background(), synthetic.HomeIdentity)
	if err != nil || resolved != synthetic {
		t.Fatal("environment changed isolated user lease scope", err)
	}
	if !localPath(synthetic.Root) || localPath(`\\server\share\profile`) {
		t.Fatal("drive classification failed")
	}
	p.knownFolder = func() (string, error) { return `\\server\share\profile`, nil }
	if _, err := p.Resolve(context.Background(), synthetic.HomeIdentity); err == nil {
		t.Fatal("UNC scope accepted")
	}
	p.knownFolder = func() (string, error) { return filepath.Dir(synthetic.Root), nil }
	synthetic.Root += "-other"
	synthetic.LockPath = filepath.Join(synthetic.Root, "proxy-operation.lock")
	synthetic.JournalPath = filepath.Join(synthetic.Root, "proxy-backup.json")
	entered := false
	if err := p.WithLocked(context.Background(), synthetic, func(Store) error { entered = true; return nil }); err == nil || entered {
		t.Fatal("tampered directory scope accepted")
	}
}

func TestWindowsRejectsReparseOrHardlinkBeforeExternalMutation(t *testing.T) {
	p, scope := syntheticPlatform(t)
	external := t.TempDir()
	privateTestDirectory(t, external)
	command := exec.Command("cmd.exe", "/c", "mklink", "/J", scope.Root, external)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("synthetic junction: %v %s", err, output)
	}
	t.Cleanup(func() { os.Remove(scope.Root) })
	entered := false
	if err := p.WithLocked(context.Background(), scope, func(Store) error { entered = true; return nil }); err == nil || entered {
		t.Fatal("junction scope accepted")
	}
	if _, err := os.Stat(filepath.Join(external, "proxy-operation.lock")); !os.IsNotExist(err) {
		t.Fatal("rejected junction modified external directory")
	}
	if err := os.Remove(scope.Root); err != nil {
		t.Fatal(err)
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureUserDirectory(scope.Root, sid); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(external, "external.txt")
	if err := os.WriteFile(target, []byte("external unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, scope.LockPath); err != nil {
		t.Fatal(err)
	}
	entered = false
	if err := p.WithLocked(context.Background(), scope, func(Store) error { entered = true; return nil }); err == nil || entered {
		t.Fatal("hardlink lock accepted")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "external unchanged" {
		t.Fatal("external hardlink target changed", err)
	}
}
