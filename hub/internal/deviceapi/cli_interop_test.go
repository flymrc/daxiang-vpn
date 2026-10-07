//go:build integration

package deviceapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zongheng-vpn/hub/internal/deviceauth"
	dc "zongheng-vpn/shared/devicecontract"
)

type cliDeviceReceipt struct {
	Version    int            `json:"contract_version"`
	OK         bool           `json:"ok"`
	Pending    bool           `json:"pending"`
	Outcome    string         `json:"outcome"`
	Credential *dc.Credential `json:"credential"`
	Operation  *dc.Operation  `json:"operation"`
}

// This connects the actual CLI executable to the actual Hub HTTP handler and
// SQLite authority. Only WG is fake; no production host or user proxy is used.
func TestRealCLIAndHubTLSRecoverLostMutationResponses(t *testing.T) {
	store, db := apiStore(t)
	handler, err := NewServer(store)
	if err != nil {
		t.Fatal(err)
	}
	var drop atomic.Bool
	var hold atomic.Bool
	heldRequest := make(chan *http.Request, 1)
	dropPath := ""
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/commands" && hold.CompareAndSwap(true, false) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error("fixture delayed body read failed")
				return
			}
			late := r.Clone(context.Background())
			late.Body = io.NopCloser(bytes.NewReader(body))
			heldRequest <- late
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error("fixture delayed response setup failed")
				return
			}
			_ = connection.Close()
			return
		}
		if drop.Load() && r.URL.Path == dropPath {
			recorded := httptest.NewRecorder()
			handler.ServeHTTP(recorded, r)
			if recorded.Code >= 200 && recorded.Code < 300 && drop.CompareAndSwap(true, false) {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error("fixture response-loss setup failed")
					return
				}
				_ = connection.Close()
				return
			}
			for k, values := range recorded.Header() {
				for _, v := range values {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(recorded.Code)
			_, _ = w.Write(recorded.Body.Bytes())
			return
		}
		handler.ServeHTTP(w, r)
	})
	server := httptest.NewUnstartedServer(wrapped)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	dir := t.TempDir()
	cli := filepath.Join(dir, "zhvpn-fixture"+cliExecutableSuffix())
	repository, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-tags", "with_gvisor", "-buildvcs=false", "-o", cli, "./clients/cli")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture CLI build failed: %s", output)
	}
	home := filepath.Join(dir, "owned-client-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	prepareCLIPrivateHome(t, home)
	ca := filepath.Join(dir, "fixture-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	activation, err := store.IssueActivation(context.Background(), "owner-cli", time.Now().Add(time.Hour), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	call := func(input string, success bool, args ...string) cliDeviceReceipt {
		t.Helper()
		command := exec.Command(cli, append([]string{"device"}, append(args, "--ca-file", ca)...)...)
		command.Env = append(os.Environ(), "ZHVPN_HOME="+home)
		command.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		if (err == nil) != success {
			t.Fatal("CLI result/exit disagreement")
		}
		if strings.Contains(stdout.String(), activation.Credential) || strings.Contains(stderr.String(), activation.Credential) || strings.Contains(stdout.String(), "private_key") {
			t.Fatal("secret in CLI diagnostic")
		}
		var receipt cliDeviceReceipt
		if json.Unmarshal(stdout.Bytes(), &receipt) != nil || receipt.Version != 2 || receipt.OK != success {
			t.Fatal("invalid public v2 CLI receipt")
		}
		return receipt
	}
	lost := func(path string, input string, args ...string) cliDeviceReceipt {
		t.Helper()
		dropPath = path
		drop.Store(true)
		r := call(input, false, args...)
		if !r.Pending || r.Outcome != "result_unknown" || drop.Load() {
			t.Fatal("committed loss not retained as unknown")
		}
		recovered := call("", true, "recover")
		if recovered.Pending {
			t.Fatal("receipt recovery did not commit local intent")
		}
		return recovered
	}
	active := lost("/api/v2/activate", activation.Credential, "activate", "--server", server.URL, "--activation-stdin")
	if active.Credential == nil {
		t.Fatal("activation receipt lost")
	}
	unknownKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{99}, 32))
	wgKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32))
	wg := &memoryWG{peers: map[string][]string{unknownKey: {"10.250.0.11/32"}}}
	scheduler, err := deviceauth.NewScheduler(store, wg)
	if err != nil {
		t.Fatal(err)
	}
	applied := lost("/api/v2/commands", "", "apply", "--expected-generation", "0", "--idempotency-key", "cli-apply", "--wg-public-key", wgKey, "--address", "10.250.0.30/32")
	if applied.Operation == nil || !applied.Operation.Accepted || applied.Operation.Effective {
		t.Fatal("accepted incorrectly presented as effective")
	}
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	queried := call("", true, "status", "--operation-id", applied.Operation.OperationId)
	if queried.Operation == nil || !queried.Operation.Effective {
		t.Fatal("latest generation not verified")
	}
	rotated := lost("/api/v2/credentials/rotate", "", "rotate-credential")
	if rotated.Credential == nil || rotated.Credential.DeviceId != active.Credential.DeviceId || rotated.Credential.CredentialId == active.Credential.CredentialId {
		t.Fatal("rotation receipt identity invalid")
	}
	// A failed connection may precede commit. Only an explicit, persisted Hub
	// cancellation can release the intent and reject a later original request.
	hold.Store(true)
	delayed := call("", false, "apply", "--expected-generation", "1", "--idempotency-key", "cli-delayed", "--wg-public-key", wgKey, "--address", "10.250.0.30/32")
	if !delayed.Pending || delayed.Outcome != "result_unknown" {
		t.Fatal("delayed mutation was not retained")
	}
	late := <-heldRequest
	cancelled := call("", true, "cancel-pending")
	if cancelled.Pending || cancelled.Outcome != "cancelled" {
		t.Fatal("uncommitted request was not cancelled")
	}
	lateResult := httptest.NewRecorder()
	handler.ServeHTTP(lateResult, late)
	if lateResult.Code != 401 && lateResult.Code != 409 {
		t.Fatal("late mutation after cancellation was not refused")
	}
	revoked := lost("/api/v2/commands", "", "revoke", "--expected-generation", "1", "--idempotency-key", "cli-revoke")
	if revoked.Operation == nil || !revoked.Operation.Accepted || revoked.Operation.Effective {
		t.Fatal("revoke receipt not pending")
	}
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, found := wg.peers[wgKey]; found || len(wg.peers) != 1 || wg.peers[unknownKey] == nil {
		t.Fatal("revoke failed or removed unknown peer")
	}
	call("", false, "status", "--operation-id", revoked.Operation.OperationId)
	var activations, credentials, operations int
	if err := db.QueryRow("SELECT COUNT(*) FROM deviceauth_devices").Scan(&activations); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM deviceauth_credentials").Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM deviceauth_operations").Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if activations != 1 || credentials != 2 || operations != 2 {
		t.Fatal("recovery resubmitted mutation")
	}
}
