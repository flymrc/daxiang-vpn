package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
)

// Test subprocesses run the actual runtime with synthetic, loopback-only
// configuration. No bootstrap, WireGuard peer, TUN or system proxy is touched.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == EngineCommand {
		if len(os.Args) != 4 || os.Args[2] != HomeFlag {
			os.Exit(2)
		}
		if err := RunDedicatedEngine(paths.FromRoot(os.Args[3])); err != nil {
			fmt.Fprintln(os.Stderr, "isolated engine failed")
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "--unrelated-helper" {
		fmt.Println("ready")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if scanner.Text() == "ping" {
				fmt.Println("alive")
			}
		}
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == "--lock-helper" {
		err := WithOperationLock(paths.FromRoot(os.Args[2]), func() error { fmt.Println("locked"); scanner := bufio.NewScanner(os.Stdin); scanner.Scan(); return nil })
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func syntheticRuntime(t *testing.T) (paths.Context, config.Config, []byte) {
	t.Helper()
	root, err := paths.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := paths.FromRoot(root)
	prepareTestHome(t, ctx)
	if err := ctx.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	cfg := config.Config{LocalProxy: config.LocalProxyConfig{ListenAddr: "127.0.0.1", ListenPort: port}}
	content := []byte(fmt.Sprintf(`{"log":{"level":"error"},"inbounds":[{"type":"mixed","tag":"local-test","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, port))
	if err := writePrivateFile(ctx, ctx.SingBoxConfig, content); err != nil {
		t.Fatal(err)
	}
	return ctx, cfg, content
}

func waitReady(t *testing.T, ctx paths.Context) EngineStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if record, err := loadRecord(ctx); err == nil {
			_, _ = requestControl(record, "activate")
		}
		status, err := Inspect(ctx)
		if err == nil && status.State == "ready" {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("isolated engine did not become ready")
	return EngineStatus{}
}

func TestUnacknowledgedLaunchExpiresAndCannotBecomeReady(t *testing.T) {
	ctx, cfg, content := syntheticRuntime(t)
	if _, err := prepareLaunchWithTimeout(ctx, cfg, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	control, err := beginEngineControl(ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	defer control.close()
	control.ready()
	status, err := Inspect(ctx)
	if err != nil || status.State != "starting" {
		t.Fatalf("child claimed ready without launcher ack: %s %v", status.State, err)
	}
	select {
	case <-control.done:
	case <-time.After(time.Second):
		t.Fatal("expired child kept running")
	}
	status, err = requestControl(control.record, "activate")
	if err != nil || status.State != "stopping" {
		t.Fatalf("late activation resurrected expired child: %s %v", status.State, err)
	}
}

func TestExpiredLaunchCannotPublishIdentity(t *testing.T) {
	ctx, cfg, content := syntheticRuntime(t)
	if _, err := prepareLaunchWithTimeout(ctx, cfg, -time.Second); err != nil {
		t.Fatal(err)
	}
	if control, err := beginEngineControl(ctx, content); err == nil {
		control.close()
		t.Fatal("expired launch started")
	}
	if _, err := os.Stat(statePath(ctx)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired launch published state")
	}
	lock, err := acquireRuntimeLock(ctx, "engine", 0)
	if err != nil {
		t.Fatal("expired child retained instance lock")
	}
	releaseRuntimeLock(lock)
}

func TestRealEngineAuthenticatedLifecycle(t *testing.T) {
	ctx, cfg, _ := syntheticRuntime(t)
	if err := Start(ctx, cfg, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = Stop(ctx) })
	status := waitReady(t, ctx)
	if status.Identity.PID == os.Getpid() || status.Identity.InstanceID == "" || status.Identity.ProtocolVersion != ControlProtocolVersion {
		t.Fatal("runtime did not identify a real child")
	}
	if status.Identity.Home != homeIdentity(ctx.Root) || status.ProxyAddr != cfg.LocalProxy.Addr() {
		t.Fatal("runtime identity did not bind home/config")
	}
	// Prove actual data-plane readiness using only another loopback server.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "local-only-ok") }))
	defer origin.Close()
	proxyURL, _ := url.Parse("http://" + cfg.LocalProxy.Addr())
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	client.CloseIdleConnections()
	if string(body) != "local-only-ok" {
		t.Fatal("local proxy did not serve expected data")
	}
	stopped, err := Stop(ctx)
	if err != nil || !stopped {
		t.Fatalf("authenticated stop: stopped=%v error=%v", stopped, err)
	}
	status, err = Inspect(ctx)
	if err != nil || status.State != "stopped" {
		t.Fatalf("stopped state: %s %v", status.State, err)
	}
	if _, err := os.Stat(ctx.PIDPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned diagnostic PID was not cleaned")
	}
	conn, err := net.DialTimeout("tcp", cfg.LocalProxy.Addr(), time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("engine listener survived stop")
	}
}

func TestLegacyPIDCannotKillUnrelatedRealProcess(t *testing.T) {
	ctx, _, _ := syntheticRuntime(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "--unrelated-helper")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatal("unrelated process did not start")
	}
	if err := os.WriteFile(ctx.PIDPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if stopped, err := Stop(ctx); stopped || !errors.Is(err, ErrLegacyState) {
		t.Fatalf("legacy PID authorized stop: %v %v", stopped, err)
	}
	if err := KillPID(cmd.Process.Pid); err == nil {
		t.Fatal("hidden PID kill remained available")
	}
	_, _ = fmt.Fprintln(stdin, "ping")
	if !scanner.Scan() || scanner.Text() != "alive" {
		t.Fatal("unrelated process was harmed")
	}
}

func TestForgedControlEndpointRejectedWithoutLeakingSecret(t *testing.T) {
	ctx, _, _ := syntheticRuntime(t)
	secret, _ := randomHex(32)
	id, _ := randomHex(16)
	var leaked atomic.Bool
	var stopped atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		leaked.Store(bytes.Contains(body, []byte(secret)))
		var request controlRequest
		_ = json.Unmarshal(body, &request)
		if request.Command == "stop" {
			stopped.Store(true)
		}
		_ = json.NewEncoder(w).Encode(controlResponse{Nonce: request.Nonce, Status: EngineStatus{State: "ready", Identity: request.Identity}, MAC: strings.Repeat("0", 64)})
	}))
	defer server.Close()
	record := controlRecord{Identity: EngineIdentity{InstanceID: id, Home: homeIdentity(ctx.Root), Generation: strings.Repeat("a", 64), ProtocolVersion: ControlProtocolVersion, PID: os.Getpid()}, Address: strings.TrimPrefix(server.URL, "http://"), Secret: secret}
	encoded, _ := json.Marshal(record)
	if err := writePrivateFile(ctx, statePath(ctx), encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := Stop(ctx); !errors.Is(err, ErrControlIdentity) {
		t.Fatalf("forged controller was accepted: %v", err)
	}
	if leaked.Load() || stopped.Load() {
		t.Fatal("secret or stop command sent to unverified endpoint")
	}
}

func TestConcurrentEngineAndIdentityMismatch(t *testing.T) {
	ctx, cfg, content := syntheticRuntime(t)
	if _, err := prepareLaunch(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	first, err := beginEngineControl(ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	first.ready()
	if _, err := beginEngineControl(ctx, content); !errors.Is(err, errLockBusy) {
		t.Fatalf("duplicate engine was admitted: %v", err)
	}
	record := first.record
	record.Identity.InstanceID = strings.Repeat("f", 32)
	if _, err := requestControl(record, "stop"); !errors.Is(err, ErrControlIdentity) {
		t.Fatalf("wrong instance accepted stop: %v", err)
	}
	select {
	case <-first.done:
		t.Fatal("identity mismatch stopped active instance")
	default:
	}
}

func TestRealEngineCrashReleasesOwnershipAndRecovers(t *testing.T) {
	ctx, cfg, _ := syntheticRuntime(t)
	if _, err := prepareLaunch(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, EngineCommand, HomeFlag, ctx.Root)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	old := waitReady(t, ctx)
	// This is our manufactured child, and this retained process handle belongs
	// to that child. Production Stop never uses this test-only crash mechanism.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	status, err := Inspect(ctx)
	if err != nil || status.State != "stopped" {
		t.Fatalf("crash was not reconciled: %s %v", status.State, err)
	}
	if err := Start(ctx, cfg, false); err != nil {
		t.Fatal(err)
	}
	defer Stop(ctx)
	current := waitReady(t, ctx)
	if current.Identity.InstanceID == old.Identity.InstanceID {
		t.Fatal("crash recovery reused identity")
	}
}

func TestOperationLockSerializesAndReleasesAfterCrash(t *testing.T) {
	ctx, _, _ := syntheticRuntime(t)
	var active, max atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			err := WithOperationLock(ctx, func() error {
				n := active.Add(1)
				for prior := max.Load(); n > prior && !max.CompareAndSwap(prior, n); prior = max.Load() {
				}
				time.Sleep(5 * time.Millisecond)
				active.Add(-1)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if max.Load() != 1 {
		t.Fatal("operation lock admitted concurrent owners")
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "--lock-helper", ctx.Root)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "locked" {
		t.Fatal("lock helper failed")
	}
	if file, err := acquireRuntimeLock(ctx, "operations", 0); err == nil {
		releaseRuntimeLock(file)
		t.Fatal("cross-process lock was not held")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if err := WithOperationLock(ctx, func() error { return nil }); err != nil {
		t.Fatalf("crashed lock owner blocked recovery: %v", err)
	}
}

func TestCleanupCannotRemoveNewerIdentity(t *testing.T) {
	ctx, cfg, content := syntheticRuntime(t)
	if _, err := prepareLaunch(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	control, err := beginEngineControl(ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	defer control.close()
	newer := control.record
	newer.Identity.InstanceID = strings.Repeat("b", 32)
	data, _ := json.Marshal(newer)
	if err := writePrivateFile(ctx, statePath(ctx), data); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedRecord(ctx, control.record.Identity); !errors.Is(err, ErrControlIdentity) {
		t.Fatal("old instance cleaned newer record")
	}
	if _, err := os.Stat(filepath.Join(ctx.RunDir, "engine-state.json")); err != nil {
		t.Fatal("new identity was removed")
	}
}

func TestRealEngineMissingStateAndPIDRemainDegraded(t *testing.T) {
	ctx, cfg, _ := syntheticRuntime(t)
	if err := Start(ctx, cfg, false); err != nil {
		t.Fatal(err)
	}
	record, err := loadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(record)
	t.Cleanup(func() { _ = writePrivateFile(ctx, statePath(ctx), encoded); _, _ = Stop(ctx) })
	if err := os.Remove(statePath(ctx)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ctx.PIDPath); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(ctx)
	if !errors.Is(err, ErrControlUnavailable) || status.State != "degraded" {
		t.Fatalf("lost identity claimed stopped: %s %v", status.State, err)
	}
	if stopped, err := Stop(ctx); stopped || !errors.Is(err, ErrControlUnavailable) {
		t.Fatalf("lost identity claimed stop success: %v %v", stopped, err)
	}
	connection, err := net.DialTimeout("tcp", cfg.LocalProxy.Addr(), time.Second)
	if err != nil {
		t.Fatal("isolated engine unexpectedly exited; missing-state test did not exercise live data plane")
	}
	connection.Close()
}

func TestStopCannotSucceedFromDeletedStateWhileLifetimeLockHeld(t *testing.T) {
	ctx, cfg, content := syntheticRuntime(t)
	if _, err := prepareLaunch(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	control, err := beginEngineControl(ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			control.close()
		}
	}()
	control.ready()
	if _, err := requestControl(control.record, "activate"); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		stopped bool
		err     error
	}
	result := make(chan outcome, 1)
	go func() { stopped, err := Stop(ctx); result <- outcome{stopped, err} }()
	select {
	case <-control.done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop was not delivered")
	}
	if err := os.Remove(statePath(ctx)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ctx.PIDPath); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		t.Fatalf("metadata loss falsely finished stop while lifetime owner remained: %v %v", got.stopped, got.err)
	case <-time.After(150 * time.Millisecond):
	}
	control.close()
	closed = true
	select {
	case got := <-result:
		if !got.stopped || got.err != nil {
			t.Fatalf("released lifetime owner did not finish stop: %v %v", got.stopped, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not finish after ownership release")
	}
}
