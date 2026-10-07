package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"zongheng-vpn/hub/internal/deviceauth"
)

func TestRestoreCLIRefusalNeverEchoesInputs(t *testing.T) {
	for _, args := range [][]string{{"--secret-request-body"}, {"--backup", "secret-activation-material", "--latest-authority", "secret-key", "--latest-checkpoint", "secret-checkpoint", "--as-of", "secret-body", "--max-checkpoint-age", "1m"}, {"--max-checkpoint-age", "secret-token"}} {
		var out, errOut bytes.Buffer
		if run(context.Background(), args, &out, &errOut) == 0 || out.Len() != 0 || !strings.Contains(errOut.String(), "refused") {
			t.Fatal("missing fail-closed CLI refusal")
		}
		if strings.Contains(errOut.String(), "secret-") {
			t.Fatal("CLI echoed confidential input")
		}
	}
}

func TestRestoreCLIProducesReadOnlyPlanFromActualSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "offline authority # copy %2.sqlite")
	restoreCLIPrivate(t, filepath.Dir(path))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(filepath.Join("..", "..", "internal", "deviceauth", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	policy := deviceauth.Policy{Epoch: "fixture-epoch", ManagedBy: "fixture-customer-authority", AddressPools: []string{"10.66.0.0/24"}, Protected: []deviceauth.Protection{}}
	policyJSON, _ := json.Marshal(policy)
	if _, err = db.Exec("INSERT INTO deviceauth_meta(singleton,schema_version,epoch,managed_by,policy_json,fence_path) VALUES(1,2,?,?,?,?)", policy.Epoch, policy.ManagedBy, string(policyJSON), "fixture-original-fence.lock"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	restoreCLIPrivate(t, path)
	hash := func(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	checkpoint := deviceauth.RestoreCheckpoint{SchemaVersion: 1, AuthoritySchemaVersion: 2, AuthoritySHA256: hash(before), Epoch: policy.Epoch, ManagedBy: policy.ManagedBy, PolicySHA256: hash(policyJSON), VerifiedAt: at, LatestRevocationFactsConfirmed: true}
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	restoreCLIPrivate(t, filepath.Dir(checkpointPath))
	b, _ := json.Marshal(checkpoint)
	if err = os.WriteFile(checkpointPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	restoreCLIPrivate(t, checkpointPath)
	key := make([]byte, 32)
	key[0] = 1
	public := base64.StdEncoding.EncodeToString(key)
	peersPath := filepath.Join(t.TempDir(), "peers.json")
	if err = os.WriteFile(peersPath, []byte(`[{"public_key":"`+public+`","allowed_ips":["10.77.0.1/32"]}]`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--backup", path, "--latest-authority", path, "--latest-checkpoint", checkpointPath, "--as-of", at.Format(time.RFC3339Nano), "--max-checkpoint-age", "5m", "--observed-peers", peersPath}, &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatalf("CLI plan refused: %s", errOut.String())
	}
	var plan deviceauth.RestorePlan
	if err = json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.ReadyToRestore || len(plan.Peers) != 1 || plan.Peers[0].Decision != "preserve_unknown_peer" || plan.LatestAuthority.SHA256 != hash(before) || len(plan.Facts) != 14 {
		t.Fatal("CLI plan scope or facts incorrect")
	}
	if strings.Contains(out.String(), public) || strings.Contains(out.String(), path) {
		t.Fatal("CLI revealed input contents or path")
	}
	after, err := os.ReadFile(path)
	if err != nil || hash(after) != hash(before) {
		t.Fatal("CLI changed authority DB")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err = os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatal("CLI created a SQLite sidecar")
		}
	}
}
