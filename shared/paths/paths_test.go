package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDirsPreservesRuntimeAndLogs(t *testing.T) {
	ctx := FromRoot(t.TempDir())
	if err := ctx.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ctx.SingBoxConfig, ctx.SingBoxLogPath, ctx.SingBoxErrorPath} {
		if err := os.WriteFile(path, []byte("existing evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := ctx.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.RemoveLegacyArtifacts(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ctx.SingBoxConfig, ctx.SingBoxLogPath, ctx.SingBoxErrorPath} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "existing evidence" {
			t.Fatalf("active file changed: %s, data=%q, err=%v", path, data, err)
		}
	}
}

func TestCanonicalRootResolvesMissingDescendants(t *testing.T) {
	root := t.TempDir()
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalRoot(filepath.Join(root, "missing", "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(want, "missing", "nested") {
		t.Fatalf("got %q, want descendants of %q", got, want)
	}
}

func TestCanonicalRootResolvesDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	want, err := CanonicalRoot(filepath.Join(real, "new"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalRoot(filepath.Join(alias, "new"))
	if err != nil || got != want {
		t.Fatalf("alias root=%q, err=%v, want=%q", got, err, want)
	}
}

func TestCanonicalRootRejectsFileAndEmptyRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", file} {
		if _, err := CanonicalRoot(root); err == nil {
			t.Fatalf("accepted invalid root %q", root)
		}
	}
}
