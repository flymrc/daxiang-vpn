package deviceauth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixtureBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic-wg")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", path, "./testdata/wgfixture")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v %s", err, out)
	}
	return path
}

func fixtureWGAdapter(t *testing.T, binary string, timeout time.Duration) *WGExecutor {
	t.Helper()
	var a *WGExecutor
	var err error
	if runtime.GOOS == "linux" {
		supervisor := filepath.Join(t.TempDir(), "synthetic-supervisor")
		cmd := exec.Command("go", "build", "-o", supervisor, "../../cmd/zhhub-device-executor")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("supervisor fixture build: %v %s", err, out)
		}
		a, err = NewSupervisedWGExecutor(binary, supervisor, "test-wg", testPolicy(), timeout)
	} else {
		a, err = NewWGExecutor(binary, "test-wg", testPolicy(), timeout)
	}
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestWGAdapterExactPeerCommandsAndVerification(t *testing.T) {
	binary := fixtureBinary(t)
	adapter := fixtureWGAdapter(t, binary, time.Second)
	s, _, _ := newTestStore(t)
	enroll(t, s, "d1")
	op := submit(t, s, command("d1", "apply", 0, 1, "10.66.0.30/32", "apply"))
	process(t, s, op, adapter)
	revoke := submit(t, s, command("d1", "revoke", 1, 0, "", "revoke"))
	process(t, s, revoke, adapter)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binary), "args.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	mutations := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if err = json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		if args[0] == "set" {
			mutations++
			if len(args) < 5 || args[1] != "test-wg" || args[2] != "peer" || args[3] != testKey(1) {
				t.Fatalf("non-exact mutation: %v", args)
			}
		} else if strings.Join(args, " ") != "show test-wg allowed-ips" {
			t.Fatalf("unsafe read: %v", args)
		}
	}
	if mutations != 2 {
		t.Fatalf("unexpected mutation count %d", mutations)
	}
	if err = adapter.Apply(context.Background(), Fence{Epoch: testPolicy().Epoch, DeviceID: "d1", Generation: 3, Sequence: 9}, Peer{PublicKey: testKey(90000), AllowedIPs: []string{"10.66.0.30/32"}}); !errors.Is(err, ErrProtected) {
		t.Fatal("protected key accepted")
	}
}
func TestWGAdapterTimeoutKillsAndReapsTreeBeforeReturn(t *testing.T) {
	binary := fixtureBinary(t)
	dir := filepath.Dir(binary)
	if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("hang"), 0600); err != nil {
		t.Fatal(err)
	}
	adapter := fixtureWGAdapter(t, binary, time.Second)
	s, _, _ := newTestStore(t)
	unlock, fence, err := s.acquirePinned(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx := context.WithValue(context.Background(), executionFenceKey{}, fence)
	start := time.Now()
	_, err = adapter.Snapshot(ctx, Fence{Epoch: testPolicy().Epoch, DeviceID: "d1", Generation: 1, Sequence: 1})
	if !errors.Is(err, ErrExecutionUnknown) || time.Since(start) > 3*time.Second {
		t.Fatalf("unbounded/false success: %v", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "parent.pid")); err != nil {
		t.Fatal("fixture never actually started")
	}
	if _, err = os.Stat(filepath.Join(dir, "child.pid")); err != nil {
		t.Fatal("fixture child never actually started")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err = os.Stat(filepath.Join(dir, "escaped")); !os.IsNotExist(err) {
		t.Fatal("side effect escaped released execution fence")
	}
}
