package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"zongheng-vpn/hub/admin/internal/db"
	"zongheng-vpn/hub/internal/auth"
)

func registerFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	raw, e := json.Marshal(db.Inventory{Version: 1, RegistryID: strings.Repeat("1", 32), Members: []db.InventoryMember{{TokenID: auth.TokenID("owned-only-token"), OwnerRef: "", Shared: false, InstallationRefs: []string{}}}})
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	path := filepath.Join(t.TempDir(), "public-inventory.json")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("fixture write failed")
	}
	return path, hex.EncodeToString(sum[:]), raw
}
func registerArgs(dbpath, input, digest string) []string {
	return []string{"--db", dbpath, "--inventory-file", input, "--approve-inventory-sha256", digest}
}
func TestCampaignRegisterNativeApprovalAndSourceZeroDatabaseEffects(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "campaign-register")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, e := build.CombinedOutput(); e != nil {
		t.Fatalf("owned CLI build: %s", b)
	}
	input, digest, raw := registerFixture(t)
	parent := t.TempDir()
	cases := []struct {
		name, path, approval string
		args                 []string
	}{{"wrong_sha", input, strings.Repeat("0", 64), nil}, {"no_sha", input, "", nil}, {"missing_input", filepath.Join(parent, "missing.json"), digest, nil}, {"input_directory", parent, digest, nil}, {"unknown_option", input, digest, []string{"--t0", "2026-10-07"}}, {"duplicate_approval", input, digest, []string{"--approve-inventory-sha256", digest}}}
	malformed := filepath.Join(parent, "malformed.json")
	bad := append([]byte{}, raw...)
	bad = bytes.Replace(bad, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
	os.WriteFile(malformed, bad, 0600)
	sum := sha256.Sum256(bad)
	cases = append(cases, struct {
		name, path, approval string
		args                 []string
	}{"duplicate_json", malformed, hex.EncodeToString(sum[:]), nil})
	hard := filepath.Join(parent, "hardlink.json")
	hardSource := filepath.Join(parent, "hardlink-source.json")
	if e := os.WriteFile(hardSource, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(hardSource, hard); e != nil {
		t.Fatal(e)
	}
	cases = append(cases, struct {
		name, path, approval string
		args                 []string
	}{"hardlink", hard, digest, nil})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dbparent := filepath.Join(parent, c.name, "must-not-exist")
			path := filepath.Join(dbparent, "state.db")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			args := append(registerArgs(path, c.path, c.approval), c.args...)
			command := exec.CommandContext(ctx, bin, args...)
			out, e := command.Output()
			if e == nil || ctx.Err() != nil {
				t.Fatal("refusal missing or blocked")
			}
			var result receipt
			if json.Unmarshal(out, &result) != nil || result.OK || result.Ready || result.T0 != nil {
				t.Fatal("invalid refusal receipt")
			}
			if c.name == "wrong_sha" && result.Code != "inventory_approval_required" {
				t.Fatal("wrong SHA test refused an earlier unrelated condition")
			}
			if c.name == "duplicate_json" && result.Code != "invalid_inventory" {
				t.Fatal("duplicate JSON test did not reach strict parser")
			}
			if _, e = os.Lstat(filepath.Dir(dbparent)); !os.IsNotExist(e) {
				t.Fatal("approval/source refusal created database directory")
			}
			if bytes.Contains(out, []byte(input)) || bytes.Contains(out, []byte("owned-only-token")) {
				t.Fatal("machine receipt disclosed input")
			}
		})
	}
	if e := os.Remove(hard); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(parent, "approved.db")
	for index := 0; index < 2; index++ {
		out, e := exec.Command(bin, registerArgs(path, input, digest)...).Output()
		if e != nil {
			t.Fatal("approved native registration failed")
		}
		var result receipt
		if json.Unmarshal(out, &result) != nil || !result.OK || result.Ready || result.T0 != nil || result.BaselineCount != 1 {
			t.Fatal("wrong provisional receipt")
		}
		expected := "registered_provisional"
		if index > 0 {
			expected = "already_registered"
		}
		if result.Outcome != expected {
			t.Fatal("sameSHA rewrote baseline")
		}
	}
}

type failedReceiptWriter struct{}

func (failedReceiptWriter) Write([]byte) (int, error) { return 0, errors.New("private-writer-canary") }
func TestCampaignRegisterReceiptFailureReturnsUnknownAndRecoversSameSHA(t *testing.T) {
	input, digest, _ := registerFixture(t)
	path := filepath.Join(t.TempDir(), "owned.db")
	if code := run(registerArgs(path, input, digest), failedReceiptWriter{}); code == 0 {
		t.Fatal("failed machine receipt reported success")
	}
	var out bytes.Buffer
	if code := run(registerArgs(path, input, digest), &out); code != 0 {
		t.Fatal("idempotent recovery failed")
	}
	var result receipt
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Outcome != "already_registered" || !result.OK || result.Ready {
		t.Fatal("receipt loss caused a second enrollment")
	}
}
