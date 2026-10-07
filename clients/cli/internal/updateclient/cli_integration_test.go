//go:build integration

package updateclient

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/updateverify"
)

func childEnv(extra map[string]string) []string {
	var env []string
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		drop := false
		for key := range extra {
			if strings.EqualFold(key, name) {
				drop = true
			}
		}
		if !drop {
			env = append(env, entry)
		}
	}
	for name, value := range extra {
		env = append(env, name+"="+value)
	}
	return env
}

func buildCLI(t *testing.T) string {
	t.Helper()
	root, e := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	module, e := os.ReadFile(filepath.Join(root, "go.mod"))
	if e != nil || !bytes.HasPrefix(module, []byte("module zongheng-vpn")) {
		t.Fatal("CLI integration fixture lost module root")
	}
	name := "zhvpn"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", exe, "./clients/cli")
	cmd.Dir = root
	cmd.Env = childEnv(map[string]string{"GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH, "CGO_ENABLED": "0", "GOWORK": "off", "GOFLAGS": ""})
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("actual CLI fixture build failed: %v %s", e, output)
	}
	return exe
}

func invokeCLI(exe string, home paths.Context, args []string) (Receipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, append([]string{"update"}, args...)...)
	cmd.Env = childEnv(map[string]string{"ZHVPN_HOME": home.Root})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	e := cmd.Run()
	r, de := DecodeReceipt(stdout.Bytes())
	if de != nil || stderr.Len() != 0 || (e == nil) != r.OK || bytes.Count(stdout.Bytes(), []byte("\n")) != 1 {
		return r, errors.New("actual CLI violated single JSON/exit/redaction contract")
	}
	return r, nil
}

func cli(t *testing.T, exe string, home paths.Context, args []string) Receipt {
	t.Helper()
	r, e := invokeCLI(exe, home, args)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func nativeArtifact(t *testing.T, f fixture) (fixture, string) {
	t.Helper()
	marker := filepath.Join(f.root, "payload-must-never-execute.marker")
	t.Setenv("ZHVPN_UPDATE_FIXTURE_MARKER", marker)
	source := filepath.Join(f.root, "payload.go")
	if e := os.WriteFile(source, []byte("package main\nimport \"os\"\nfunc main(){ _ = os.WriteFile(os.Getenv(\"ZHVPN_UPDATE_FIXTURE_MARKER\"), []byte(\"executed\"), 0600) }\n"), 0600); e != nil {
		t.Fatal(e)
	}
	name := "payload"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	f.artifact = filepath.Join(f.root, name)
	f.doc.MaxArtifactSize = 16 << 20
	f.writePolicy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", f.artifact, source)
	cmd.Dir = f.root
	cmd.Env = childEnv(map[string]string{"GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH, "CGO_ENABLED": "0", "GOWORK": "off", "GOFLAGS": ""})
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("owned native artifact fixture failed: %v %s", e, out)
	}
	return f, marker
}

func TestCLIUpdateOfflineWorkflow(t *testing.T) {
	exe := buildCLI(t)
	f, marker := nativeArtifact(t, newFixture(t))
	home := privateHome(t)
	if r := cli(t, exe, home, f.approvalArgs(t, "enroll")); !r.OK {
		t.Fatal("real CLI enrollment failed")
	}
	metadata := f.metadata(t, 20, nil)
	if r := cli(t, exe, home, f.verifyArgs(metadata)); !r.OK || !r.WatermarkCommitted || r.Staging.Metadata.ReleaseSequence != 20 {
		t.Fatal("real CLI watermark commit failed")
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("offline verifier executed the real native artifact")
	}
	if r := cli(t, exe, home, append([]string{"inspect"}, scopeArgs(f.scope)...)); !r.OK || r.Floors.MinSequence != 20 || r.Staging != nil {
		t.Fatal("fresh CLI process lost watermark or claimed new permit")
	}
	if r := cli(t, exe, home, f.verifyArgs(metadata)); !r.OK || !r.Staging.Idempotent {
		t.Fatal("fresh CLI process did not revalidate exact candidate")
	}
	expectCode(t, cli(t, exe, home, f.verifyArgs(f.metadata(t, 19, nil))), "rollback_refused")
	expectCode(t, cli(t, exe, home, f.verifyArgs(f.metadata(t, 20, func(m *updateverify.Metadata) { m.SourceCommit = strings.Repeat("b", 40) }))), "replay_refused")
	f.doc.Revision = 2
	f.doc.MinVersion = "2.0.0"
	f.doc.MinSequence = 20
	f.doc.SecurityFloor = 4
	f.writePolicy(t)
	if !cli(t, exe, home, f.approvalArgs(t, "policy-approve")).OK {
		t.Fatal("real CLI approval update failed")
	}
	expectCode(t, cli(t, exe, home, f.verifyArgs(metadata)), "rollback_refused")
	if r := cli(t, exe, home, append([]string{"inspect"}, scopeArgs(f.scope)...)); !r.OK || r.PolicyRevision != 2 || r.Floors.SecurityFloor != 4 {
		t.Fatal("failed candidate undid atomic approved floors")
	}
	for _, args := range [][]string{{"inspect"}, append(append([]string{"inspect"}, scopeArgs(f.scope)...), "--product", "cli"), append(append([]string{"inspect"}, scopeArgs(f.scope)...), "--now", "1"), append(append([]string{}, f.verifyArgs(metadata)...), "--policy-file", f.policy)} {
		expectCode(t, cli(t, exe, home, args), "invalid_arguments")
	}
	invalidHome := paths.FromRoot(f.artifact) // an existing regular file cannot be an application home
	expectCode(t, cli(t, exe, invalidHome, append([]string{"inspect"}, scopeArgs(f.scope)...)), "local_storage_failure")
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("native artifact was executed")
	}
}

func TestCLIUpdateCrossProcessWatermarks(t *testing.T) {
	exe := buildCLI(t)
	f := newFixture(t)
	home := privateHome(t)
	if !cli(t, exe, home, f.approvalArgs(t, "enroll")).OK {
		t.Fatal("enroll")
	}
	paths := []string{}
	for sequence := uint64(40); sequence < 48; sequence++ {
		paths = append(paths, f.metadata(t, sequence, nil))
	}
	gate := make(chan struct{})
	results := make(chan Receipt, len(paths))
	failures := make(chan error, len(paths))
	var wg sync.WaitGroup
	for _, metadata := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			<-gate
			r, e := invokeCLI(exe, home, f.verifyArgs(path))
			if e != nil {
				failures <- e
			}
			results <- r
		}(metadata)
	}
	close(gate)
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	for r := range results {
		if !r.OK && r.Code != "rollback_refused" && r.Code != "replay_refused" {
			t.Fatalf("cross-process unexpected %s", r.Code)
		}
	}
	if r := cli(t, exe, home, append([]string{"inspect"}, scopeArgs(f.scope)...)); !r.OK || r.Floors.MinSequence != 47 {
		t.Fatal("eight actual CLI processes lowered latest watermark")
	}
	for _, file := range []string{RegistrationName, StateName} {
		t.Run(file, func(t *testing.T) {
			h := privateHome(t)
			if !cli(t, exe, h, f.approvalArgs(t, "enroll")).OK {
				t.Fatal("enroll")
			}
			if e := os.Remove(filepath.Join(h.Root, file)); e != nil {
				t.Fatal(e)
			}
			expectCode(t, cli(t, exe, h, f.verifyArgs(paths[0])), "registration_incomplete")
			expectCode(t, cli(t, exe, h, f.approvalArgs(t, "enroll")), "registration_incomplete")
		})
	}
}
