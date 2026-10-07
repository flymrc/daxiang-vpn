package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"zongheng-vpn/shared/paths"
)

func engineLogTestHome(t *testing.T) paths.Context {
	t.Helper()
	root, e := paths.CanonicalRoot(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx := paths.FromRoot(root)
	prepareTestHome(t, ctx)
	return ctx
}
func engineLogTestOps() engineLogIO {
	return engineLogIO{write: func(f *os.File, b []byte) (int, error) { return f.Write(b) }, sync: func(f *os.File) error { return f.Sync() }, truncate: func(f *os.File, n int64) error { return f.Truncate(n) }}
}
func engineLogTestRead(t *testing.T, ctx paths.Context) (records []EngineLogRecord, total int64) {
	t.Helper()
	for i := 0; i < EngineLogSlots; i++ {
		b, e := os.ReadFile(filepath.Join(ctx.LogDir, "engine-events-v1", engineLogSlotName(i)))
		if e != nil {
			t.Fatal(e)
		}
		total += int64(len(b))
		if len(b) > EngineLogSlotBytes {
			t.Fatal("unbounded slot")
		}
		lines := bytes.Split(b, []byte{'\n'})
		if len(lines) < 2 {
			t.Fatal("missing owned header")
		}
		for _, line := range lines[1:] {
			if len(line) == 0 {
				continue
			}
			r, e := DecodeEngineLogRecord(line)
			if e != nil {
				t.Fatal(e)
			}
			records = append(records, r)
		}
	}
	return
}
func TestEngineLogTypedCodecRejectsRawAndSecretShapes(t *testing.T) {
	r := EngineLogRecord{Version: 1, Sequence: 1, TimeUnixNano: 1, Event: EngineLogStarting, BuildID: strings.Repeat("a", 40)}
	b, _ := json.Marshal(r)
	if _, e := DecodeEngineLogRecord(b); e != nil {
		t.Fatal(e)
	}
	for name, bad := range map[string][]byte{
		"raw": []byte("token SECRET_CANARY"), "path": bytes.Replace(b, []byte(`"starting"`), []byte(`"C:\\secret"`), 1), "message": append(b[:len(b)-1], []byte(`,"message":"SECRET_CANARY"}`)...), "control": []byte(`{"Identity":{"Home":"secret"},"Secret":"SECRET_CANARY"}`), "newline": append(append([]byte{}, b...), '\n'), "pretty": []byte(strings.ReplaceAll(string(b), ",", ", ")), "duplicate": append(b[:len(b)-1], []byte(`,"version":1}`)...), "unknown event": bytes.Replace(b, []byte("starting"), []byte("freeform"), 1), "casealias": bytes.Replace(b, []byte("version"), []byte("Version"), 1), "missing": bytes.Replace(b, []byte(`"code":"",`), nil, 1), "null": bytes.Replace(b, []byte(`"code":""`), []byte(`"code":null`), 1), "oversize": bytes.Repeat([]byte{'x'}, EngineLogRecordBytes), "invalid pair": bytes.Replace(b, []byte(`"code":""`), []byte(`"code":"engine_start_failed"`), 1), "bad metadata": bytes.Replace(b, []byte(`"instance_id":""`), []byte(`"instance_id":"SECRET_CANARY"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := DecodeEngineLogRecord(bad); e == nil {
				t.Fatal("unsafe codec accepted")
			}
		})
	}
}
func TestEngineLogRotationReopenAndLegacyPreservation(t *testing.T) {
	ctx := engineLogTestHome(t)
	if e := os.Mkdir(ctx.LogDir, 0700); e != nil {
		t.Fatal(e)
	}
	legacy := []byte("legacy SECRET_CANARY\n")
	for _, name := range []string{"zhvpn.log", "zhvpn.err.log", "sing-box.log"} {
		if e := os.WriteFile(filepath.Join(ctx.LogDir, name), legacy, 0600); e != nil {
			t.Fatal(e)
		}
	}
	l, e := openEngineLog(ctx, EngineBuildInfo{BuildID: strings.Repeat("b", 40)}, 1024, engineLogTestOps())
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 80; i++ {
		if e = l.Append(EngineLogEvent{Event: EngineLogReady, InstanceID: strings.Repeat("a", 32), Generation: strings.Repeat("c", 64)}); e != nil {
			t.Fatal(e)
		}
	}
	if l.State().State != "healthy" {
		t.Fatal(l.State())
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	records, total := engineLogTestRead(t, ctx)
	if total > 4*1024 || len(records) == 0 || len(records) >= 80 {
		t.Fatalf("not bounded history: %d %d", total, len(records))
	}
	var max uint64
	for _, r := range records {
		if r.Sequence > max {
			max = r.Sequence
		}
		if strings.Contains(stringMustJSON(r), "SECRET_CANARY") {
			t.Fatal("raw legacy copied")
		}
	}
	l, e = openEngineLog(ctx, EngineBuildInfo{}, 1024, engineLogTestOps())
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if e = l.Append(EngineLogEvent{Event: EngineLogStopped}); e != nil {
		t.Fatal(e)
	}
	if l.sequence != max+1 {
		t.Fatalf("lost sequence: %d %d", l.sequence, max)
	}
	for _, name := range []string{"zhvpn.log", "zhvpn.err.log", "sing-box.log"} {
		b, e := os.ReadFile(filepath.Join(ctx.LogDir, name))
		if e != nil || !bytes.Equal(b, legacy) {
			t.Fatal("legacy mutated")
		}
	}
}
func stringMustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func TestEngineLogActualProductionCapAndConcurrentWrites(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	var wg sync.WaitGroup
	errorsOut := make(chan error, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 700; i++ {
				if e := l.Append(EngineLogEvent{Event: EngineLogReady, InstanceID: strings.Repeat("a", 32), Generation: strings.Repeat("b", 64)}); e != nil {
					errorsOut <- e
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errorsOut)
	for e := range errorsOut {
		t.Fatal(e)
	}
	records, total := engineLogTestRead(t, ctx)
	if total > 1<<20 || len(records) >= 5600 {
		t.Fatalf("cap not enforced: %d %d", total, len(records))
	}
	ownerInfo, e := os.Stat(filepath.Join(ctx.LogDir, "engine-events-v1", "owner.v1"))
	if e != nil || total+ownerInfo.Size() > 1<<20 {
		t.Fatalf("metadata escaped total cap: slots=%d owner=%v err=%v", total, ownerInfo, e)
	}
	var sizes []int64
	for i := 0; i < EngineLogSlots; i++ {
		info, e := os.Stat(filepath.Join(ctx.LogDir, "engine-events-v1", engineLogSlotName(i)))
		if e != nil {
			t.Fatal(e)
		}
		sizes = append(sizes, info.Size())
	}
	t.Logf("actual owned files slots=%v marker=%d total=%d retained_records=%d", sizes, ownerInfo.Size(), total+ownerInfo.Size(), len(records))
	seen := map[uint64]bool{}
	for _, r := range records {
		if seen[r.Sequence] {
			t.Fatal("duplicate sequence")
		}
		seen[r.Sequence] = true
	}
}
func TestEngineLogCrashPartialTailOnlyOwnedV1Recovery(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	if e = l.Append(EngineLogEvent{Event: EngineLogStarting}); e != nil {
		t.Fatal(e)
	}
	slot := l.current
	l.Close()
	p := filepath.Join(ctx.LogDir, "engine-events-v1", engineLogSlotName(slot))
	before, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	f, e := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Write([]byte(`{"version":1,"sequence":2`)); e != nil {
		t.Fatal(e)
	}
	f.Close()
	l, e = OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	l.Close()
	after, e := os.ReadFile(p)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("partial tail not safely recovered")
	}
	f, e = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString("unknown complete line\n")
	f.Close()
	bad, _ := os.ReadFile(p)
	if x, e := OpenEngineLog(ctx, EngineBuildInfo{}); e == nil {
		x.Close()
		t.Fatal("complete unknown line adopted")
	}
	after, _ = os.ReadFile(p)
	if !bytes.Equal(after, bad) {
		t.Fatal("unknown data truncated")
	}
}
func TestEngineLogUnknownNamespaceAndSlotAreNeverAdopted(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		ctx := engineLogTestHome(t)
		p := filepath.Join(ctx.LogDir, "engine-events-v1")
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
		if x, e := OpenEngineLog(ctx, EngineBuildInfo{}); e == nil {
			x.Close()
			t.Fatal("unknown namespace adopted")
		}
		entries, _ := os.ReadDir(p)
		if len(entries) != 0 {
			t.Fatal("unknown namespace changed")
		}
	})
	t.Run("slot", func(t *testing.T) {
		ctx := engineLogTestHome(t)
		l, e := OpenEngineLog(ctx, EngineBuildInfo{})
		if e != nil {
			t.Fatal(e)
		}
		l.Close()
		p := filepath.Join(ctx.LogDir, "engine-events-v1", engineLogSlotName(2))
		f, e := os.OpenFile(p, os.O_TRUNC|os.O_WRONLY, 0)
		if e != nil {
			t.Fatal(e)
		}
		raw := []byte("unknown SECRET_CANARY\n")
		f.Write(raw)
		f.Close()
		if x, e := OpenEngineLog(ctx, EngineBuildInfo{}); e == nil {
			x.Close()
			t.Fatal("unknown slot adopted")
		}
		after, _ := os.ReadFile(p)
		if !bytes.Equal(raw, after) {
			t.Fatal("unknown slot mutated")
		}
	})
	t.Run("extra", func(t *testing.T) {
		ctx := engineLogTestHome(t)
		l, e := OpenEngineLog(ctx, EngineBuildInfo{})
		if e != nil {
			t.Fatal(e)
		}
		l.Close()
		p := filepath.Join(ctx.LogDir, "engine-events-v1", "unowned.txt")
		os.WriteFile(p, []byte("unowned"), 0600)
		if x, e := OpenEngineLog(ctx, EngineBuildInfo{}); e == nil {
			x.Close()
			t.Fatal("extra file accepted")
		}
		b, _ := os.ReadFile(p)
		if string(b) != "unowned" {
			t.Fatal("extra modified")
		}
	})
}
func TestEngineLogStickyStorageFaults(t *testing.T) {
	for _, code := range []EngineLogCode{EngineLogCodeWrite, EngineLogCodeRotate, EngineLogCodeSync} {
		t.Run(string(code), func(t *testing.T) {
			ctx := engineLogTestHome(t)
			ops := engineLogTestOps()
			l, e := openEngineLog(ctx, EngineBuildInfo{}, 512, ops)
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			calls := 0
			switch code {
			case EngineLogCodeWrite:
				l.ops.write = func(*os.File, []byte) (int, error) { calls++; return 0, errors.New("SECRET_CANARY") }
			case EngineLogCodeSync:
				l.ops.sync = func(*os.File) error { calls++; return errors.New("SECRET_CANARY") }
			case EngineLogCodeRotate:
				l.ops.truncate = func(*os.File, int64) error { calls++; return errors.New("SECRET_CANARY") }
			}
			var got error
			for i := 0; i < 4; i++ {
				got = l.Append(EngineLogEvent{Event: EngineLogReady, InstanceID: strings.Repeat("a", 32), Generation: strings.Repeat("b", 64)})
				if got != nil {
					break
				}
			}
			if got == nil || got.Error() != string(code) || l.State() != (EngineLogState{State: "degraded", Code: code}) {
				t.Fatalf("noncanonical failure: %v %+v", got, l.State())
			}
			before := calls
			if e = l.Append(EngineLogEvent{Event: EngineLogStopped}); e == nil || calls != before {
				t.Fatal("sticky failure retried I/O")
			}
			l.Close()
			if l.State().Code != code {
				t.Fatal("close reset fault")
			}
		})
	}
	t.Run("short write", func(t *testing.T) {
		ctx := engineLogTestHome(t)
		l, e := OpenEngineLog(ctx, EngineBuildInfo{})
		if e != nil {
			t.Fatal(e)
		}
		defer l.Close()
		l.ops.write = func(f *os.File, b []byte) (int, error) { return f.Write(b[:7]) }
		if e = l.Append(EngineLogEvent{Event: EngineLogStarting}); e == nil || e.Error() != string(EngineLogCodeWrite) {
			t.Fatal("short write ignored")
		}
	})
	t.Run("invalid input", func(t *testing.T) {
		ctx := engineLogTestHome(t)
		l, e := OpenEngineLog(ctx, EngineBuildInfo{})
		if e != nil {
			t.Fatal(e)
		}
		defer l.Close()
		if e = l.Append(EngineLogEvent{Event: "SECRET_CANARY"}); e == nil || e.Error() != string(EngineLogCodeCodec) {
			t.Fatal("bad event accepted")
		}
	})
}
func TestEngineLogCrossProcessSingleWriter(t *testing.T) {
	if root := os.Getenv("ZHVPN_ENGINE_LOG_LOCK_TEST"); root != "" {
		ctx := paths.FromRoot(root)
		l, e := OpenEngineLog(ctx, EngineBuildInfo{})
		if e == nil {
			l.Close()
			os.Exit(4)
		}
		if e.Error() != string(EngineLogCodeOpen) {
			os.Exit(5)
		}
		return
	}
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestEngineLogCrossProcessSingleWriter$")
	command.Env = append(os.Environ(), "ZHVPN_ENGINE_LOG_LOCK_TEST="+ctx.Root)
	if b, e := command.CombinedOutput(); e != nil {
		t.Fatalf("second process writer: %v %s", e, b)
	}
	if e = l.Append(EngineLogEvent{Event: EngineLogStarting}); e != nil {
		t.Fatal(e)
	}
	l.Close()
	l, e = OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	l.Close()
}
func TestEngineLogNilUnknownAndClose(t *testing.T) {
	var l *EngineLog
	if l.State() != (EngineLogState{State: "unknown"}) {
		t.Fatal(l.State())
	}
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	l.Close()
	l.Close()
	if l.State() != (EngineLogState{State: "degraded", Code: EngineLogCodeClosed}) {
		t.Fatal(l.State())
	}
	if e = l.Append(EngineLogEvent{Event: EngineLogStopped}); e == nil {
		t.Fatal("append closed succeeded")
	}
	_ = io.ErrShortWrite
}

func TestEngineLogStateAndStickyFailureNeverWaitForBlockedDisk(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	l.ops.write = func(f *os.File, b []byte) (int, error) { close(entered); <-release; return f.Write(b) }
	go func() { finished <- l.Append(EngineLogEvent{Event: EngineLogReady}) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("write did not enter")
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if state := l.State(); state != (EngineLogState{State: "unknown"}) {
		t.Fatalf("blocked write claimed healthy: %+v", state)
	}
	observed := make(chan EngineLogState, 1)
	go func() { l.MarkDegraded(EngineLogCodeQueueOverflow); observed <- l.State() }()
	select {
	case state := <-observed:
		if state != (EngineLogState{State: "degraded", Code: EngineLogCodeQueueOverflow}) {
			t.Fatal(state)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("health waited for disk lock")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("owned write not returned")
	}
	if l.State().Code != EngineLogCodeQueueOverflow {
		t.Fatal("write reset sticky fault")
	}
	if e = l.Append(EngineLogEvent{Event: EngineLogStopped}); e == nil || e.Error() != string(EngineLogCodeQueueOverflow) {
		t.Fatal("continued after queue failure")
	}
}

func TestEngineLogFiniteEventCodeCountContract(t *testing.T) {
	for _, rule := range engineLogRules {
		for _, code := range rule.codes {
			t.Run(string(rule.event)+"/"+string(code), func(t *testing.T) {
				event := EngineLogEvent{Event: rule.event, Code: code}
				if rule.counted {
					event.Count = 1
				}
				if !validateEngineLogEvent(event) {
					t.Fatal("finite contract rejected")
				}
				if rule.counted {
					event.Count = engineLogMaxSequence
					if !validateEngineLogEvent(event) {
						t.Fatal("max count rejected")
					}
					event.Count++
					if validateEngineLogEvent(event) {
						t.Fatal("overflow count accepted")
					}
					event.Count = 0
					if validateEngineLogEvent(event) {
						t.Fatal("zero category count accepted")
					}
				} else {
					event.Count = 1
					if validateEngineLogEvent(event) {
						t.Fatal("lifecycle count accepted")
					}
				}
			})
		}
	}
}

func TestEngineLogOversizedOwnedHistoryRefusesWithoutTruncation(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := OpenEngineLog(ctx, EngineBuildInfo{})
	if e != nil {
		t.Fatal(e)
	}
	l.Close()
	p := filepath.Join(ctx.LogDir, "engine-events-v1", "events-0.v1")
	before, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	data := append(before, bytes.Repeat([]byte{'x'}, EngineLogSlotBytes)...)
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if e != nil {
		t.Fatal(e)
	}
	f.Write(data)
	f.Close()
	if l, e := OpenEngineLog(ctx, EngineBuildInfo{}); e == nil {
		l.Close()
		t.Fatal("oversize history accepted")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(data, after) {
		t.Fatal("oversize history truncated")
	}
}

func TestEngineLogRotationCrashPartialHeaderRefusesWithoutRepair(t *testing.T) {
	ctx := engineLogTestHome(t)
	l, e := openEngineLog(ctx, EngineBuildInfo{}, 512, engineLogTestOps())
	if e != nil {
		t.Fatal(e)
	}
	// Complete the first slot, then emulate a crash during its successor's
	// header publication. Unlike an event tail, that header cannot prove owner.
	if e = l.Append(EngineLogEvent{Event: EngineLogReady, InstanceID: strings.Repeat("a", 32), Generation: strings.Repeat("b", 64)}); e != nil {
		t.Fatal(e)
	}
	next := (l.current + 1) % EngineLogSlots
	p := l.slots[next].file.Name()
	if !filepath.IsAbs(p) {
		p = filepath.Join(ctx.LogDir, "engine-events-v1", p)
	}
	l.ops.write = func(f *os.File, b []byte) (int, error) { return f.Write(b[:9]) }
	if e = l.Append(EngineLogEvent{Event: EngineLogReady, InstanceID: strings.Repeat("a", 32), Generation: strings.Repeat("b", 64)}); e == nil || e.Error() != string(EngineLogCodeRotate) {
		t.Fatal("partial rotation ignored")
	}
	l.Close()
	before, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	if l, e := openEngineLog(ctx, EngineBuildInfo{}, 512, engineLogTestOps()); e == nil {
		l.Close()
		t.Fatal("partial header adopted")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("unproven partial header repaired")
	}
}
