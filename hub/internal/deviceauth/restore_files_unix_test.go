//go:build !windows

package deviceauth

import (
	"os"
	"path/filepath"
	"testing"
)

func restoreFixturePrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0600)
	if info.IsDir() {
		mode = 0700
	}
	if err = os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreUnixRefusesUnsafeModesWithoutRepair(t *testing.T) {
	for _, target := range []string{"file", "parent"} {
		t.Run(target, func(t *testing.T) {
			dir := restoreTestDir(t)
			path := filepath.Join(dir, "input.json")
			if err := os.WriteFile(path, []byte("private fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			unsafePath, mode := path, os.FileMode(0644)
			if target == "parent" {
				unsafePath, mode = dir, 0755
			}
			if err := os.Chmod(unsafePath, mode); err != nil {
				t.Fatal(err)
			}
			if input, _, err := restoreOpenInput(path, 100); err == nil {
				input.Close()
				t.Fatal("unsafe permission accepted")
			}
			info, err := os.Stat(unsafePath)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatal("planner repaired unsafe source permissions")
			}
		})
	}
}

func TestRestoreUnixRefusesSymlinkAndDetectsReplacedPath(t *testing.T) {
	dir := restoreTestDir(t)
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "alias.json")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if input, _, err := restoreOpenInput(symlink, 100); err == nil {
		input.Close()
		t.Fatal("symlink source accepted")
	}
	input, _, err := restoreOpenInput(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err = os.Rename(path, filepath.Join(dir, "old.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = input.unchanged(); err == nil {
		t.Fatal("same-byte replacement pathname was accepted")
	}
}

func TestRestoreUnixRejectsAncestorPermissionChangeWhilePinned(t *testing.T) {
	dir := restoreTestDir(t)
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	input, _, err := restoreOpenInput(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err = os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if err = input.unchanged(); err == nil {
		t.Fatal("unsafe pinned ancestor permission change accepted")
	}
}
