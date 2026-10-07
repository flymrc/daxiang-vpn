//go:build windows

package deviceclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCARejectsNetworkDevicesStreamsBeforePathAccessAndHonorsReadSharing(t *testing.T) {
	for _, path := range []string{`\\synthetic-never-connect\share\ca.pem`, `//synthetic-never-connect/share/ca.pem`, `\\?\C:\ca.pem`, `\\.\pipe\synthetic`, `C:ca.pem`, `C:\ca.pem:secret`, `NUL`, `COM1.pem`, `C:\x\CONIN$`} {
		if localCAPath(path) {
			t.Fatal("unsafe CA syntax accepted")
		}
		if _, e := loadRoots(context.Background(), path); FailureCode(e) != "invalid_ca_file" {
			t.Fatal("unsafe CA input did not produce fixed failure")
		}
	}
	a := authority(t)
	f, e := openCA(a.caFile)
	if e != nil {
		t.Fatal(e)
	}
	// The real kernel handle permits reads but refuses concurrent replacement.
	if e = os.Rename(a.caFile, filepath.Join(filepath.Dir(a.caFile), "moved.pem")); e == nil {
		f.Close()
		t.Fatal("held CA read allowed source replacement")
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = loadRoots(context.Background(), a.caFile); e != nil {
		t.Fatal("ordinary CA rejected")
	}
}
