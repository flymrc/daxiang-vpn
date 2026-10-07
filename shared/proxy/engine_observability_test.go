package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"zongheng-vpn/shared/paths"
)

const engineOutputCanary = "engine-private-canary-b72f2a0dc3"

func engineOutputCaptureHelper(home paths.Context) int {
	cachedStdout, cachedStderr := os.Stdout, os.Stderr
	cachedLogger := stdlog.New(cachedStderr, "", 0)
	capture, err := suppressDedicatedOutput()
	if err != nil {
		return 2
	}
	// Separate writes and multiple lines must not require a redactor to retain
	// state. Include native handle writes as well as Go os.File writes.
	_, _ = fmt.Fprint(os.Stdout, "engine-private-")
	_, _ = fmt.Fprint(os.Stdout, "canary-b72f2a0dc3\nAuthorization:\nBearer ")
	_, _ = fmt.Fprint(os.Stderr, engineOutputCanary+"\nprivate_key=\n"+engineOutputCanary)
	nativeEngineOutputCanary(engineOutputCanary)
	_, _ = fmt.Fprint(cachedStdout, "engine-private-")
	_, _ = fmt.Fprint(cachedStdout, "canary-b72f2a0dc3\n")
	_, _ = fmt.Fprint(cachedStderr, engineOutputCanary+"\n")
	cachedLogger.Print(engineOutputCanary)
	stdlog.Print(engineOutputCanary)
	log, err := OpenEngineLog(home, EngineBuildInfo{BuildID: strings.Repeat("a", 40)})
	if err != nil {
		return 3
	}
	recorder := newEngineLogRecorder(log)
	defer recorder.close()
	capture.startLog(recorder)
	defer capture.finish(recorder)
	if recorder.append(EngineLogEvent{Event: EngineLogStarting}) != nil {
		return 4
	}
	_, _ = fmt.Fprint(os.Stderr, "\nWA")
	_, _ = fmt.Fprint(os.Stderr, "RN[0000] warning\n"+engineOutputCanary+"\nER")
	_, _ = fmt.Fprint(os.Stderr, "ROR[0000] error\nAuthorization: Bearer "+engineOutputCanary+"\n")
	if recorder.append(EngineLogEvent{Event: EngineLogStopped}) != nil {
		return 5
	}
	return 0
}

func readEngineEvents(t *testing.T, home paths.Context) []EngineLogRecord {
	t.Helper()
	records, err := tryReadEngineEvents(home)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func tryReadEngineEvents(home paths.Context) ([]EngineLogRecord, error) {
	var records []EngineLogRecord
	for i := 0; i < EngineLogSlots; i++ {
		file, err := os.Open(filepath.Join(home.LogDir, "engine-events-v1", engineLogSlotName(i)))
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		if !scanner.Scan() {
			file.Close()
			return nil, errors.New("log slot has not completed its header")
		}
		for scanner.Scan() {
			record, err := DecodeEngineLogRecord(scanner.Bytes())
			if err != nil {
				file.Close()
				return nil, err
			}
			records = append(records, record)
		}
		err = scanner.Err()
		file.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Sequence < records[j].Sequence })
	return records, nil
}

func TestDedicatedOutputSuppressesSplitMultilineNativeAndGoCanaries(t *testing.T) {
	home, _, _ := syntheticRuntime(t)
	cmd := exec.Command(os.Args[0], "--engine-output-capture-helper", home.Root)
	output, err := engineChildSeparateOutput(cmd)
	if err != nil {
		t.Fatalf("owned capture child failed: %v", err)
	}
	if len(output) != 0 {
		t.Fatal("dedicated capture child leaked raw standard output")
	}
	var warning, failure bool
	for _, record := range readEngineEvents(t, home) {
		warning = warning || record.Event == EngineLogDependencyWarning
		failure = failure || record.Event == EngineLogDependencyError
		if record.BuildID != strings.Repeat("a", 40) {
			t.Fatal("typed events lost public build provenance")
		}
	}
	if !warning || !failure {
		t.Fatal("typed warning/error categories were discarded with raw messages")
	}
	err = filepath.WalkDir(home.LogDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbidden := range []string{engineOutputCanary, "Authorization", "Bearer", "private_key", "engine-private-", "canary-b72f2a0dc3"} {
			if bytes.Contains(content, []byte(forbidden)) {
				return errors.New("raw canary content reached an owned log file")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDedicatedRealSingBoxStartFailureRetainsOnlyFixedStage(t *testing.T) {
	home, cfg, _ := syntheticRuntime(t)
	occupied, err := net.Listen("tcp", cfg.LocalProxy.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	outputPath := filepath.Join(home.LogDir, "forbidden-dependency-append.log")
	content := fmt.Sprintf(`{"log":{"level":"debug","output":%q},"inbounds":[{"type":"mixed","tag":%q,"listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, outputPath, engineOutputCanary, cfg.LocalProxy.ListenPort)
	if err := writePrivateFile(home, home.SingBoxConfig, []byte(content)); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareLaunch(home, cfg); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], EngineCommand, HomeFlag, home.Root)
	output, err := engineChildSeparateOutput(cmd)
	if err == nil {
		t.Fatal("actual sing-box unexpectedly bound an occupied owned port")
	}
	if len(output) != 0 {
		t.Fatal("actual start failure leaked raw output")
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dependency bypassed typed store through configured append file")
	}
	found := false
	var stages []string
	for _, record := range readEngineEvents(t, home) {
		stages = append(stages, string(record.Event)+":"+string(record.Code))
		if record.Event == EngineLogReady {
			t.Fatal("failed actual engine claimed ready")
		}
		found = found || record.Event == EngineLogFailed && record.Code == EngineLogCodeStartFailed
	}
	if !found {
		t.Fatalf("actual sing-box bind failure lost fixed startup stage: %v", stages)
	}
	status, err := Inspect(home)
	if err != nil || status.State != "stopped" {
		t.Fatal("startup failure retained an engine identity/lifetime lock")
	}
}

// Independent pipes avoid introducing the Windows unsupported two-File/one-
// HANDLE alias merely through the test harness's CombinedOutput shorthand.
func engineChildSeparateOutput(cmd *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return append(stdout.Bytes(), stderr.Bytes()...), err
}

func TestGeneralRunEnginePreservesStandardHandlesAndDoesNotCreateLog(t *testing.T) {
	home, cfg, _ := syntheticRuntime(t)
	stdout, stderr := os.Stdout, os.Stderr
	standardLogWriter := stdlog.Writer()
	if _, err := prepareLaunch(home, cfg); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- RunEngine(home) }()
	status := waitReady(t, home)
	health, err := InspectEngineLog(home, status.Identity)
	if err != nil || health.State != "unknown" {
		t.Fatal("general library engine claimed dedicated logging")
	}
	if _, err := Stop(home); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("general library engine failed to stop")
	}
	if os.Stdout != stdout || os.Stderr != stderr {
		t.Fatal("general library engine changed global standard handles")
	}
	if stdlog.Writer() != standardLogWriter {
		t.Fatal("general library engine changed the global standard logger")
	}
	entries, err := os.ReadDir(home.LogDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("general library engine created dedicated log storage")
	}
}

func TestEngineLogStatusStickyFailurePreservesSignedControlAndRestoreGate(t *testing.T) {
	hooks := &actionHooks{}
	control := actionControl(t, hooks)
	log, err := OpenEngineLog(control.ctx, EngineBuildInfo{})
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	control.setEngineLog(log)
	defer control.logRecorder.Load().close()
	health, err := InspectEngineLog(control.ctx, control.record.Identity)
	if err != nil || health.State != "healthy" {
		t.Fatal("authenticated current log health absent")
	}
	// Actual held file failure, not a mock log-status response. The first
	// storage failure stays sticky after subsequent public queries/appends.
	if err := log.slots[log.current].file.Close(); err != nil {
		t.Fatal(err)
	}
	if control.appendLog(EngineLogInitialized, "") != nil {
		t.Fatal("bounded recorder refused the actual failure fixture")
	}
	deadline := time.Now().Add(time.Second)
	for log.State().State != "degraded" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if log.State().State != "degraded" {
		t.Fatal("closed underlying log handle reported healthy storage")
	}
	first := log.State()
	health, err = InspectEngineLog(control.ctx, control.record.Identity)
	if err != nil || health.State != "degraded" || health.Code != first.Code {
		t.Fatal("runtime logging failure was not safely published")
	}
	legacy, err := requestControl(control.record, "status")
	if err != nil || legacy.State != "ready" {
		t.Fatal("logging failure changed legacy control phase/MAC")
	}
	hooks.failStop.Store(true)
	if _, err := requestControl(control.record, "stop"); err == nil {
		t.Fatal("logging failure bypassed failed proxy restoration")
	}
	select {
	case <-control.done:
		t.Fatal("logging failure released runtime lifetime before restoration")
	default:
	}
	hooks.failStop.Store(false)
	if _, err := requestControl(control.record, "stop"); err != nil {
		t.Fatal(err)
	}
	if log.State() != first {
		t.Fatal("subsequent lifecycle event erased sticky logging failure")
	}
}

func TestEngineLogStatusRequiresExactFreshIdentityAndPurpose(t *testing.T) {
	control := actionControl(t, nil)
	if health, err := InspectEngineLog(control.ctx, EngineIdentity{}); err == nil || health.State != "unknown" {
		t.Fatal("log status merged an unauthenticated or replaced identity")
	}
	for _, purpose := range []string{"legacy", "response"} {
		t.Run(purpose, func(t *testing.T) {
			request := engineLogStatusRequest{Version: 1, Command: "log-status", Nonce: strings.Repeat("a", 64), Identity: control.record.Identity}
			if purpose == "legacy" {
				request.MAC = sign(control.record.Secret, request)
			} else {
				request.MAC = signEngineLogStatus(control.record.Secret, "response", request)
			}
			content, _ := json.Marshal(request)
			response, err := http.Post("http://"+control.record.Address+"/v1/log-status", "application/json", bytes.NewReader(content))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatal("cross-purpose signature accepted")
			}
		})
	}
}

func TestEngineLogStatusOld404IsUnknownAndTamperingFails(t *testing.T) {
	for _, mode := range []string{"old", "mac", "nonce", "identity", "health", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			home, _, _ := syntheticRuntime(t)
			var record controlRecord
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "old" {
					http.NotFound(w, r)
					return
				}
				var request engineLogStatusRequest
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("fixture did not receive log-status request")
					return
				}
				result := engineLogStatusResponse{Version: 1, Command: "log-status", Nonce: request.Nonce, Identity: request.Identity, Health: EngineLogHealth{State: "healthy"}}
				switch mode {
				case "nonce":
					result.Nonce = strings.Repeat("b", 64)
				case "identity":
					result.Identity.InstanceID = strings.Repeat("c", 32)
				case "health":
					result.Health.Code = EngineLogCodeWrite
				}
				result.MAC = signEngineLogStatus(record.Secret, "response", result)
				if mode == "mac" {
					result.MAC = strings.Repeat("0", 64)
				}
				_ = json.NewEncoder(w).Encode(result)
				if mode == "oversized" {
					_, _ = io.WriteString(w, strings.Repeat(" ", 2048))
				}
			}))
			defer server.Close()
			record = controlRecord{Identity: EngineIdentity{InstanceID: strings.Repeat("a", 32), Home: homeIdentity(home.Root), Generation: strings.Repeat("b", 64), ProtocolVersion: ControlProtocolVersion, PID: os.Getpid()}, Address: strings.TrimPrefix(server.URL, "http://"), Secret: strings.Repeat("c", 64)}
			content, _ := json.Marshal(record)
			if err := writePrivateFile(home, statePath(home), content); err != nil {
				t.Fatal(err)
			}
			health, err := InspectEngineLog(home, record.Identity)
			if mode == "old" {
				if err != nil || health.State != "unknown" {
					t.Fatal("old child 404 invented healthy logging")
				}
			} else if err == nil || health.State != "unknown" {
				t.Fatal("unbound log health accepted")
			}
		})
	}
}
