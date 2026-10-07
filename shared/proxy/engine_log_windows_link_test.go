//go:build windows

package proxy

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEngineLogWindowsRuntimeHardlinkBlockedOrRejectedBeforeWrite(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	p := l.slots[l.current].file.Name()
	external := filepath.Join(t.TempDir(), "external-linked.log")
	before, _ := os.ReadFile(p)
	if e = os.Link(p, external); e != nil {
		t.Log("held Windows writer handle refused creation of an external hardlink")
		if e = l.Append(EngineLogEvent{Event: EngineLogReady}); e != nil {
			t.Fatal(e)
		}
		return
	}
	t.Log("external hardlink created; next append must refuse before writing")
	if e = l.Append(EngineLogEvent{Event: EngineLogReady}); e == nil || e.Error() != string(EngineLogCodeNamespace) {
		t.Fatal("live hardlink accepted")
	}
	after, _ := os.ReadFile(external)
	if !bytes.Equal(before, after) {
		t.Fatal("external linked content changed")
	}
}
