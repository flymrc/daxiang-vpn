package proxy

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"zongheng-vpn/shared/paths"
)

const (
	EngineLogVersion     = 1
	EngineLogSlots       = 4
	EngineLogSlotBytes   = 256 << 10
	EngineLogRecordBytes = 2048
	// Reserve the owner/lock marker budget inside the one-MiB namespace cap.
	EngineLogMetadataBytes = 256
	engineLogMaxSequence   = uint64(1<<53 - 1)
)

type EngineBuildInfo struct{ BuildID string }
type EngineLogEventKind string
type EngineLogCode string

const (
	EngineLogStarting            EngineLogEventKind = "starting"
	EngineLogControlBound        EngineLogEventKind = "control_bound"
	EngineLogInitialized         EngineLogEventKind = "initialized"
	EngineLogReady               EngineLogEventKind = "ready"
	EngineLogStopRequested       EngineLogEventKind = "stop_requested"
	EngineLogStopped             EngineLogEventKind = "stopped"
	EngineLogFailed              EngineLogEventKind = "failed"
	EngineLogDependencyWarning   EngineLogEventKind = "dependency_warning"
	EngineLogDependencyError     EngineLogEventKind = "dependency_error"
	EngineLogRawOutputSuppressed EngineLogEventKind = "raw_output_suppressed"
	EngineLogStartup                                = EngineLogStarting
	EngineLogIdentity                               = EngineLogControlBound
	EngineLogStopping                               = EngineLogStopRequested
	EngineLogStartupFailed                          = EngineLogFailed
	EngineLogRawOutputDiscarded                     = EngineLogRawOutputSuppressed
)

const (
	EngineLogCodeNone                EngineLogCode = ""
	EngineLogCodeOpen                EngineLogCode = "engine_log_open"
	EngineLogCodeWrite               EngineLogCode = "engine_log_write"
	EngineLogCodeRotate              EngineLogCode = "engine_log_rotate"
	EngineLogCodeSync                EngineLogCode = "engine_log_sync"
	EngineLogCodeNamespace           EngineLogCode = "engine_log_namespace"
	EngineLogCodeCodec               EngineLogCode = "engine_log_codec"
	EngineLogCodeClosed              EngineLogCode = "engine_log_closed"
	EngineLogCodeQueueOverflow       EngineLogCode = "engine_log_queue_overflow"
	EngineLogCodeShutdown            EngineLogCode = "engine_log_shutdown"
	EngineLogCodeStartupFailed       EngineLogCode = "engine_startup_failed"
	EngineLogCodeIdentityInvalid     EngineLogCode = "engine_identity_invalid"
	EngineLogCodeConfigRead          EngineLogCode = "engine_config_read"
	EngineLogCodeControlInit         EngineLogCode = "engine_control_init"
	EngineLogCodeConfigDecode        EngineLogCode = "engine_config_decode"
	EngineLogCodeInitFailed          EngineLogCode = "engine_init_failed"
	EngineLogCodeStartFailed         EngineLogCode = "engine_start_failed"
	EngineLogCodeCloseFailed         EngineLogCode = "engine_close_failed"
	EngineLogCodeCaptureFailed       EngineLogCode = "engine_capture_failed"
	EngineLogCodeDependencyWarning   EngineLogCode = "dependency_warning"
	EngineLogCodeDependencyError     EngineLogCode = "dependency_error"
	EngineLogCodeRawOutputSuppressed EngineLogCode = "raw_output_suppressed"
	EngineLogCodeRawOutputDiscarded                = EngineLogCodeRawOutputSuppressed
)

type EngineLogEvent struct {
	Event      EngineLogEventKind
	Code       EngineLogCode
	InstanceID string
	Generation string
	Count      uint64
}
type EngineLogState struct {
	State string        `json:"state"`
	Code  EngineLogCode `json:"code"`
}
type EngineLogRecord struct {
	Version      int                `json:"version"`
	Sequence     uint64             `json:"sequence"`
	TimeUnixNano int64              `json:"time_unix_nano"`
	Event        EngineLogEventKind `json:"event"`
	Code         EngineLogCode      `json:"code"`
	BuildID      string             `json:"build_id"`
	InstanceID   string             `json:"instance_id"`
	Generation   string             `json:"generation"`
	Count        uint64             `json:"count"`
}
type engineLogOwner struct {
	Version   int    `json:"version"`
	Namespace string `json:"namespace"`
	ID        string `json:"id"`
}
type engineLogHeader struct {
	Version     int    `json:"version"`
	NamespaceID string `json:"namespace_id"`
	Slot        int    `json:"slot"`
	Cycle       uint64 `json:"cycle"`
}
type engineLogSlot struct {
	file         *os.File
	header       engineLogHeader
	size         int64
	lastSequence uint64
	tail         int64
}
type engineLogIO struct {
	write    func(*os.File, []byte) (int, error)
	sync     func(*os.File) error
	truncate func(*os.File, int64) error
}

// EngineLog is a dedicated typed event store. It never opens legacy logs and
// never accepts a free-form message, error text, path, or control record.
// A live writer holds the namespace and owner lock until Close. Storage errors
// are sticky: subsequent appends fail without trying repair or widening ACLs.
type EngineLog struct {
	mu       sync.Mutex
	ns       *engineLogNamespace
	owner    *os.File
	slots    [EngineLogSlots]engineLogSlot
	current  int
	sequence uint64
	build    EngineBuildInfo
	health   atomic.Pointer[EngineLogState]
	inFlight atomic.Bool
	closed   bool
	limit    int64
	ops      engineLogIO
}

func logHex(s string, n int, empty bool) bool {
	if empty && s == "" {
		return true
	}
	b, e := hex.DecodeString(s)
	return e == nil && len(s) == n && hex.EncodeToString(b) == s
}

var engineLogRules = []struct {
	event   EngineLogEventKind
	codes   []EngineLogCode
	counted bool
}{
	{EngineLogStarting, []EngineLogCode{EngineLogCodeNone}, false},
	{EngineLogControlBound, []EngineLogCode{EngineLogCodeNone}, false},
	{EngineLogInitialized, []EngineLogCode{EngineLogCodeNone}, false},
	{EngineLogReady, []EngineLogCode{EngineLogCodeNone}, false},
	{EngineLogStopRequested, []EngineLogCode{EngineLogCodeNone}, false},
	{EngineLogStopped, []EngineLogCode{EngineLogCodeNone}, false},
	{EngineLogDependencyWarning, []EngineLogCode{EngineLogCodeDependencyWarning}, true},
	{EngineLogDependencyError, []EngineLogCode{EngineLogCodeDependencyError}, true},
	{EngineLogRawOutputSuppressed, []EngineLogCode{EngineLogCodeRawOutputSuppressed}, true},
	{EngineLogFailed, []EngineLogCode{EngineLogCodeStartupFailed, EngineLogCodeIdentityInvalid, EngineLogCodeConfigRead, EngineLogCodeControlInit, EngineLogCodeConfigDecode, EngineLogCodeInitFailed, EngineLogCodeStartFailed, EngineLogCodeCloseFailed, EngineLogCodeCaptureFailed}, false},
}

func validateEngineLogEvent(e EngineLogEvent) bool {
	if !logHex(e.InstanceID, 32, true) || !logHex(e.Generation, 64, true) {
		return false
	}
	for _, rule := range engineLogRules {
		if rule.event != e.Event {
			continue
		}
		if rule.counted {
			if e.Count == 0 || e.Count > engineLogMaxSequence {
				return false
			}
		} else if e.Count != 0 {
			return false
		}
		for _, code := range rule.codes {
			if e.Code == code {
				return true
			}
		}
	}
	return false
}
func canonicalLogDecode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return errors.New(string(EngineLogCodeCodec))
	}
	encoded, e := json.Marshal(v)
	if e != nil || !bytes.Equal(encoded, b) {
		return errors.New(string(EngineLogCodeCodec))
	}
	return nil
}
func DecodeEngineLogRecord(b []byte) (EngineLogRecord, error) {
	var r EngineLogRecord
	if len(b) == 0 || len(b)+1 > EngineLogRecordBytes || canonicalLogDecode(b, &r) != nil || r.Version != EngineLogVersion || r.Sequence == 0 || r.Sequence > engineLogMaxSequence || r.TimeUnixNano <= 0 || !(logHex(r.BuildID, 40, true) || logHex(r.BuildID, 64, true)) || !validateEngineLogEvent(EngineLogEvent{Event: r.Event, Code: r.Code, InstanceID: r.InstanceID, Generation: r.Generation, Count: r.Count}) {
		return EngineLogRecord{}, errors.New(string(EngineLogCodeCodec))
	}
	return r, nil
}

func OpenEngineLog(ctx paths.Context, info EngineBuildInfo) (*EngineLog, error) {
	return openEngineLog(ctx, info, EngineLogSlotBytes, engineLogIO{write: func(f *os.File, b []byte) (int, error) { return f.Write(b) }, sync: func(f *os.File) error { return f.Sync() }, truncate: func(f *os.File, n int64) error { return f.Truncate(n) }})
}
func openEngineLog(ctx paths.Context, info EngineBuildInfo, limit int64, ops engineLogIO) (_ *EngineLog, result error) {
	// Test-only budgets can tighten the product cap; no public option widens it.
	if !(logHex(info.BuildID, 40, true) || logHex(info.BuildID, 64, true)) || limit < 512 || limit > EngineLogSlotBytes {
		return nil, errors.New(string(EngineLogCodeOpen))
	}
	ns, fresh, e := openEngineLogNamespace(ctx)
	if e != nil {
		return nil, errors.New(string(EngineLogCodeOpen))
	}
	limit -= EngineLogMetadataBytes / EngineLogSlots
	l := &EngineLog{ns: ns, build: info, limit: limit, ops: ops}
	l.health.Store(&EngineLogState{State: "healthy"})
	defer func() {
		if result != nil {
			l.closeFiles()
			result = errors.New(string(EngineLogCodeOpen))
		}
	}()
	l.owner, e = ns.open("owner.v1", fresh)
	if e != nil {
		return nil, e
	}
	if lockFile(l.owner) != nil {
		return nil, errors.New(string(EngineLogCodeOpen))
	}
	ownerInfo, e := l.owner.Stat()
	if e != nil || ownerInfo.Size() > 256 {
		return nil, errors.New(string(EngineLogCodeOpen))
	}
	owner := engineLogOwner{Version: EngineLogVersion, Namespace: "engine-events-v1"}
	if ownerInfo.Size() == 0 && fresh && ns.created(l.owner) {
		id := make([]byte, 16)
		if _, e = rand.Read(id); e != nil {
			return nil, e
		}
		owner.ID = hex.EncodeToString(id)
		b, _ := json.Marshal(owner)
		if e = logWriteAll(l.owner, b); e != nil {
			return nil, e
		}
		if e = l.owner.Sync(); e != nil {
			return nil, e
		}
	} else {
		b, e := io.ReadAll(io.LimitReader(l.owner, 257))
		if e != nil || canonicalLogDecode(b, &owner) != nil || owner.Version != EngineLogVersion || owner.Namespace != "engine-events-v1" || !logHex(owner.ID, 32, false) {
			return nil, errors.New(string(EngineLogCodeOpen))
		}
	}
	if e = ns.verify(l.owner, "owner.v1"); e != nil {
		return nil, e
	}
	if e = ns.checkEntries(); e != nil {
		return nil, e
	}
	// Verify all history before mutating even a crash-partial owned tail.
	for i := range l.slots {
		f, e := ns.open(engineLogSlotName(i), true)
		if e != nil {
			return nil, e
		}
		l.slots[i].file = f
		info, e := f.Stat()
		if e != nil || info.Size() > limit {
			return nil, errors.New(string(EngineLogCodeOpen))
		}
		if info.Size() == 0 {
			// Only an exclusively created empty file has proven ownership.
			if !ns.created(f) {
				return nil, errors.New(string(EngineLogCodeOpen))
			}
			l.slots[i].header = engineLogHeader{Version: EngineLogVersion, NamespaceID: owner.ID, Slot: i, Cycle: 1}
			continue
		}
		s, e := readEngineLogSlot(f, owner.ID, i, limit)
		if e != nil {
			return nil, e
		}
		l.slots[i] = s
		if s.lastSequence > l.sequence {
			l.sequence = s.lastSequence
			l.current = i
		} else if l.sequence == 0 && s.header.Cycle > l.slots[l.current].header.Cycle {
			l.current = i
		}
	}
	for i := range l.slots {
		s := &l.slots[i]
		if s.size == 0 {
			b, _ := json.Marshal(s.header)
			b = append(b, '\n')
			if e = logWriteAll(s.file, b); e != nil {
				return nil, e
			}
			s.size = int64(len(b))
			if e = s.file.Sync(); e != nil {
				return nil, e
			}
		}
		if s.tail > 0 {
			if e = ns.verify(s.file, engineLogSlotName(i)); e != nil {
				return nil, e
			}
			if e = s.file.Truncate(s.size); e != nil {
				return nil, e
			}
			if e = s.file.Sync(); e != nil {
				return nil, e
			}
		}
		if _, e = s.file.Seek(s.size, io.SeekStart); e != nil {
			return nil, e
		}
	}
	if e = ns.valid(); e != nil {
		return nil, e
	}
	if e = ns.checkEntries(); e != nil {
		return nil, e
	}
	return l, nil
}
func engineLogSlotName(i int) string { return fmt.Sprintf("events-%d.v1", i) }
func logWriteAll(f *os.File, b []byte) error {
	n, e := f.Write(b)
	if e == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return e
}
func readEngineLogSlot(f *os.File, id string, index int, limit int64) (engineLogSlot, error) {
	s := engineLogSlot{file: f}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return s, errors.New(string(EngineLogCodeCodec))
	}
	end := bytes.IndexByte(b, '\n')
	if end < 0 || end > 256 || canonicalLogDecode(b[:end], &s.header) != nil || s.header.Version != EngineLogVersion || s.header.NamespaceID != id || s.header.Slot != index || s.header.Cycle == 0 || s.header.Cycle > engineLogMaxSequence {
		return s, errors.New(string(EngineLogCodeCodec))
	}
	s.size = int64(end + 1)
	b = b[end+1:]
	for len(b) > 0 {
		end = bytes.IndexByte(b, '\n')
		if end < 0 {
			if len(b) >= EngineLogRecordBytes {
				return s, errors.New(string(EngineLogCodeCodec))
			}
			s.tail = int64(len(b))
			break
		}
		r, e := DecodeEngineLogRecord(b[:end])
		if e != nil || r.Sequence <= s.lastSequence {
			return s, errors.New(string(EngineLogCodeCodec))
		}
		s.lastSequence = r.Sequence
		s.size += int64(end + 1)
		b = b[end+1:]
	}
	return s, nil
}
func (l *EngineLog) fail(code EngineLogCode) error {
	l.MarkDegraded(code)
	return errors.New(string(l.State().Code))
}

var engineLogHealthCodes = []EngineLogCode{EngineLogCodeOpen, EngineLogCodeWrite, EngineLogCodeRotate, EngineLogCodeSync, EngineLogCodeNamespace, EngineLogCodeCodec, EngineLogCodeClosed, EngineLogCodeQueueOverflow, EngineLogCodeShutdown}

func IsEngineLogHealthCode(code EngineLogCode) bool {
	for _, known := range engineLogHealthCodes {
		if code == known {
			return true
		}
	}
	return false
}

// MarkDegraded publishes a fixed sticky failure without waiting for filesystem
// I/O or the append lock. The dedicated engine uses this for its bounded queue
// overflow. It cannot reset a failed store or publish caller-controlled text.
func (l *EngineLog) MarkDegraded(code EngineLogCode) {
	if l == nil {
		return
	}
	if !IsEngineLogHealthCode(code) {
		code = EngineLogCodeCodec
	}
	for {
		previous := l.health.Load()
		if previous != nil && previous.State == "degraded" {
			return
		}
		next := &EngineLogState{State: "degraded", Code: code}
		if l.health.CompareAndSwap(previous, next) {
			return
		}
	}
}
func (l *EngineLog) State() EngineLogState {
	if l == nil {
		return EngineLogState{State: "unknown"}
	}
	if state := l.health.Load(); state != nil {
		// An in-flight filesystem operation is not evidence of current durable
		// logging. Return unknown immediately until it finishes or fails.
		if state.State == "healthy" && l.inFlight.Load() {
			return EngineLogState{State: "unknown"}
		}
		return *state
	}
	return EngineLogState{State: "unknown"}
}
func (l *EngineLog) Append(event EngineLogEvent) error {
	if l == nil {
		return errors.New(string(EngineLogCodeOpen))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if state := l.State(); state.State == "degraded" {
		return errors.New(string(state.Code))
	}
	if l.closed {
		return l.fail(EngineLogCodeClosed)
	}
	if l.ns == nil {
		return l.fail(EngineLogCodeOpen)
	}
	l.inFlight.Store(true)
	defer l.inFlight.Store(false)
	if !validateEngineLogEvent(event) || l.sequence == engineLogMaxSequence {
		return l.fail(EngineLogCodeCodec)
	}
	if l.ns.valid() != nil || l.ns.verify(l.owner, "owner.v1") != nil || l.ns.checkEntries() != nil {
		return l.fail(EngineLogCodeNamespace)
	}
	r := EngineLogRecord{EngineLogVersion, l.sequence + 1, time.Now().UnixNano(), event.Event, event.Code, l.build.BuildID, event.InstanceID, event.Generation, event.Count}
	b, e := json.Marshal(r)
	if e != nil || len(b)+1 > EngineLogRecordBytes {
		return l.fail(EngineLogCodeCodec)
	}
	b = append(b, '\n')
	s := &l.slots[l.current]
	headerBytes, _ := json.Marshal(s.header)
	if int64(len(headerBytes)+1+len(b)) > l.limit {
		return l.fail(EngineLogCodeCodec)
	}
	if l.ns.verify(s.file, engineLogSlotName(l.current)) != nil {
		return l.fail(EngineLogCodeNamespace)
	}
	if s.size+int64(len(b)) > l.limit {
		next := (l.current + 1) % EngineLogSlots
		t := &l.slots[next]
		if l.ns.verify(t.file, engineLogSlotName(next)) != nil {
			return l.fail(EngineLogCodeNamespace)
		}
		cycle := s.header.Cycle + 1
		if cycle > engineLogMaxSequence {
			return l.fail(EngineLogCodeRotate)
		}
		if e = l.ops.truncate(t.file, 0); e != nil {
			return l.fail(EngineLogCodeRotate)
		}
		if _, e = t.file.Seek(0, io.SeekStart); e != nil {
			return l.fail(EngineLogCodeRotate)
		}
		t.header.Cycle = cycle
		header, _ := json.Marshal(t.header)
		header = append(header, '\n')
		if n, e := l.ops.write(t.file, header); e != nil || n != len(header) {
			return l.fail(EngineLogCodeRotate)
		}
		t.size = int64(len(header))
		t.lastSequence = 0
		if e = l.ops.sync(t.file); e != nil {
			return l.fail(EngineLogCodeSync)
		}
		l.current = next
		s = t
	}
	n, e := l.ops.write(s.file, b)
	if e != nil || n != len(b) {
		return l.fail(EngineLogCodeWrite)
	}
	s.size += int64(n)
	s.lastSequence = r.Sequence
	l.sequence = r.Sequence
	if e = l.ops.sync(s.file); e != nil {
		return l.fail(EngineLogCodeSync)
	}
	if l.ns.verify(s.file, engineLogSlotName(l.current)) != nil {
		return l.fail(EngineLogCodeNamespace)
	}
	return nil
}
func (l *EngineLog) closeFiles() {
	for i := range l.slots {
		if f := l.slots[i].file; f != nil {
			f.Close()
			l.slots[i].file = nil
		}
	}
	if l.owner != nil {
		unlockFile(l.owner)
		l.owner.Close()
		l.owner = nil
	}
	if l.ns != nil {
		l.ns.close()
		l.ns = nil
	}
}
func (l *EngineLog) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	l.MarkDegraded(EngineLogCodeClosed)
	l.closeFiles()
	return nil
}
