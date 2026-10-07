//go:build windows

package leasecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
	osproxy "zongheng-vpn/shared/systemproxy"
)

// This helper starts the real sing-box child and authenticated controller, with
// package-private injection into a file-backed fake OS. No HKCU/WinINET access.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == proxy.EngineCommand && os.Args[2] == proxy.HomeFlag {
		home := paths.FromRoot(os.Args[3])
		controller := New(home)
		controller.platform = func() (osproxy.Platform, error) { return fakePlatform(home.Root), nil }
		if proxy.RunDedicatedEngineWithHooks(home, controller) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type diskPlatform struct {
	root string
	mu   sync.Mutex
}

func fakePlatform(home string) osproxy.Platform {
	p := &diskPlatform{root: filepath.Join(home, "isolated-os")}
	return osproxy.Platform{Resolver: p, Transactions: p, Factory: p}
}
func (p *diskPlatform) Resolve(ctx context.Context, home string) (osproxy.Scope, error) {
	return osproxy.Scope{UserScope: "synthetic-original-user", HomeIdentity: strings.ToLower(home), Root: p.root, JournalPath: filepath.Join(p.root, "proxy-backup.json"), LockPath: filepath.Join(p.root, "proxy-operation.lock")}, ctx.Err()
}
func (p *diskPlatform) WithLocked(ctx context.Context, s osproxy.Scope, fn func(osproxy.Store) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(p.root, 0700); err != nil {
		return err
	}
	return fn(diskStore{s.JournalPath})
}

type diskStore struct{ path string }

func (s diskStore) Load() ([]byte, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}
func (s diskStore) Create(data []byte) error {
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}
func (s diskStore) Remove() error                                   { return os.Remove(s.path) }
func (p *diskPlatform) Open(osproxy.Scope) (osproxy.Adapter, error) { return p, nil }
func (p *diskPlatform) Read(name string) (*osproxy.Value, error) {
	data, err := os.ReadFile(filepath.Join(p.root, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var fields map[string]*osproxy.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return fields[name], nil
}
func (p *diskPlatform) Write(name string, value *osproxy.Value) error {
	if _, err := os.Stat(filepath.Join(p.root, "deny-write")); err == nil {
		return errors.New("isolated OS write denied")
	}
	fields := map[string]*osproxy.Value{}
	data, err := os.ReadFile(filepath.Join(p.root, "settings.json"))
	if err == nil {
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if value == nil {
		delete(fields, name)
	} else {
		fields[name] = value
	}
	data, _ = json.Marshal(fields)
	return os.WriteFile(filepath.Join(p.root, "settings.json"), data, 0600)
}
func (p *diskPlatform) Notify() error { return nil }

func syntheticHome(t *testing.T) (paths.Context, config.Config) {
	t.Helper()
	root, err := paths.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	home := paths.FromRoot(root)
	if err := home.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cfg := config.Config{LocalProxy: config.LocalProxyConfig{ListenAddr: "127.0.0.1", ListenPort: port}}
	content := fmt.Sprintf(`{"log":{"level":"error"},"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, port)
	if err := os.WriteFile(home.SingBoxConfig, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(home.SingBoxConfig, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	return home, cfg
}
func launch(t *testing.T, home paths.Context, cfg config.Config) {
	t.Helper()
	if err := proxy.WithOperationLock(home, func() error { return proxy.Start(home, cfg, false) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(home.Root, "isolated-os", "deny-write")); _, _ = proxy.Stop(home) })
}
func acquire(t *testing.T, home paths.Context) string {
	t.Helper()
	r, err := proxy.RequestRuntimeAction(home, proxy.RuntimeAction{Command: "system-proxy-acquire", UserScope: "synthetic-original-user"})
	if err != nil || r.LeaseID == "" || r.Owned == nil || !*r.Owned || r.Noop == nil || *r.Noop {
		t.Fatalf("acquire %v %+v", err, r)
	}
	return r.LeaseID
}

func TestRealChildLeaseAcquireFailedRestoreKeepsEngineAndRetryRestores(t *testing.T) {
	home, cfg := syntheticHome(t)
	launch(t, home, cfg)
	before, err := proxy.Inspect(home)
	if err != nil {
		t.Fatal(err)
	}
	id := acquire(t, home)
	r, err := proxy.RequestRuntimeAction(home, proxy.RuntimeAction{Command: "system-proxy-acquire", UserScope: "synthetic-original-user"})
	if err != nil || r.LeaseID != id || r.Noop == nil || !*r.Noop {
		t.Fatal("repeated acquire lost ownership/no-op evidence")
	}
	if err := os.WriteFile(filepath.Join(home.Root, "isolated-os", "deny-write"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.Stop(home); err == nil {
		t.Fatal("failed restore stopped engine")
	}
	current, err := proxy.Inspect(home)
	if err != nil || current.State != "ready" || current.Identity != before.Identity {
		t.Fatal("restore failure changed actual live engine")
	}
	if _, err := os.Stat(filepath.Join(home.Root, "isolated-os", "proxy-backup.json")); err != nil {
		t.Fatal("failed restore lost WAL")
	}
	if err := os.Remove(filepath.Join(home.Root, "isolated-os", "deny-write")); err != nil {
		t.Fatal(err)
	}
	if stopped, err := proxy.Stop(home); err != nil || !stopped {
		t.Fatalf("retry stop %v", err)
	}
	if _, err := os.Stat(filepath.Join(home.Root, "isolated-os", "proxy-backup.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful stop retained lease")
	}
	p := &diskPlatform{root: filepath.Join(home.Root, "isolated-os")}
	for _, name := range osproxy.Fields {
		v, err := p.Read(name)
		if err != nil || v != nil {
			t.Fatalf("original absent field %s was not restored", name)
		}
	}
}
func TestRealChildRefusesForeignUserAndLeaseID(t *testing.T) {
	home, cfg := syntheticHome(t)
	launch(t, home, cfg)
	if _, err := proxy.RequestRuntimeAction(home, proxy.RuntimeAction{Command: "system-proxy-acquire", UserScope: "other-user"}); err == nil {
		t.Fatal("foreign user acquired HKCU")
	}
	id := acquire(t, home)
	if _, err := proxy.RequestRuntimeAction(home, proxy.RuntimeAction{Command: "system-proxy-release", UserScope: "synthetic-original-user", LeaseID: strings.Repeat("0", 32)}); err == nil {
		t.Fatal("wrong lease ID released lease")
	}
	r, err := proxy.RequestRuntimeAction(home, proxy.RuntimeAction{Command: "system-proxy-release", UserScope: "synthetic-original-user", LeaseID: id})
	if err != nil || r.SystemProxyState != "released" {
		t.Fatalf("release %v", err)
	}
	if s, err := proxy.Inspect(home); err != nil || s.State != "ready" {
		t.Fatal("lease release stopped engine")
	}
}
func TestRealChildCrashCannotTransferLeaseAndStoppedRecoveryRestores(t *testing.T) {
	home, cfg := syntheticHome(t)
	launch(t, home, cfg)
	acquire(t, home)
	first, err := proxy.Inspect(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recoverWithPlatform(context.Background(), home, fakePlatform(home.Root)); err == nil {
		t.Fatal("live engine recovered as crashed")
	}
	// Kill only the child whose HMAC-authenticated full identity we just proved;
	// this is a synthetic crash fixture, never the production PID stop path.
	process, err := os.FindProcess(first.Identity.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = process.Wait()
	if s, err := proxy.Inspect(home); err != nil || s.State != "stopped" {
		t.Fatalf("crash inspect %v", err)
	}
	launch(t, home, cfg)
	if _, err := proxy.RequestRuntimeAction(home, proxy.RuntimeAction{Command: "system-proxy-acquire", UserScope: "synthetic-original-user"}); err == nil {
		t.Fatal("new generation adopted old lease")
	}
	if stopped, err := proxy.Stop(home); err != nil || !stopped {
		t.Fatalf("foreign old lease blocked new engine stop: %v", err)
	}
	err = proxy.WithOperationLock(home, func() error {
		result, err := recoverWithPlatform(context.Background(), home, fakePlatform(home.Root))
		if err == nil && result.SystemProxyState != "recovered" {
			return errors.New("missing recovered receipt")
		}
		return err
	})
	if err != nil {
		t.Fatalf("stopped recovery %v", err)
	}
}
