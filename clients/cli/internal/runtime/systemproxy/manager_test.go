package systemproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"zongheng-vpn/shared/contracts"
	osproxy "zongheng-vpn/shared/systemproxy"
)

type fakeResolver struct {
	root, user string
	err        error
}

func (r *fakeResolver) Resolve(_ context.Context, home string) (osproxy.Scope, error) {
	return osproxy.Scope{UserScope: r.user, HomeIdentity: home, Root: r.root, JournalPath: filepath.Join(r.root, "proxy-backup.json"), LockPath: filepath.Join(r.root, "proxy-operation.lock")}, r.err
}

type fakeStore struct {
	data                             []byte
	creates, removes                 int
	failLoad, failCreate, failRemove bool
	onCreate                         func()
}

func clone(data []byte) []byte {
	if data == nil {
		return nil
	}
	return append([]byte{}, data...)
}
func (s *fakeStore) Load() ([]byte, error) {
	if s.failLoad {
		return nil, errors.New("load denied")
	}
	return clone(s.data), nil
}
func (s *fakeStore) Create(data []byte) error {
	s.creates++
	if s.failCreate {
		return errors.New("persist denied")
	}
	if s.data != nil {
		return errors.New("record already exists")
	}
	s.data = clone(data)
	if s.onCreate != nil {
		s.onCreate()
	}
	return nil
}
func (s *fakeStore) Remove() error {
	s.removes++
	if s.failRemove {
		return errors.New("remove denied")
	}
	s.data = nil
	return nil
}

type fakeTransactions struct {
	mu     sync.Mutex
	store  *fakeStore
	calls  int
	refuse bool
	skip   bool
}

func (x *fakeTransactions) WithLocked(_ context.Context, _ osproxy.Scope, action func(osproxy.Store) error) error {
	if x.refuse || !x.mu.TryLock() {
		return errors.New("transaction busy")
	}
	defer x.mu.Unlock()
	x.calls++
	if x.skip {
		return nil
	}
	return action(x.store)
}

type fakeAdapter struct {
	values                         map[string]*osproxy.Value
	reads, writes, notifies, opens int
	failRead, failWrite            int
	failNotify                     bool
	mutateAt                       int
	mutateField                    string
	onWrite                        func()
	mutateOnNotify                 string
}

func valueClone(value *osproxy.Value) *osproxy.Value {
	if value == nil {
		return nil
	}
	return &osproxy.Value{Kind: value.Kind, Bytes: clone(value.Bytes)}
}
func (a *fakeAdapter) Open(_ osproxy.Scope) (osproxy.Adapter, error) { a.opens++; return a, nil }
func (a *fakeAdapter) Read(name string) (*osproxy.Value, error) {
	a.reads++
	if a.mutateAt == a.reads {
		a.values[a.mutateField] = &osproxy.Value{Kind: 1, Bytes: []byte{99}}
	}
	if a.failRead == a.reads {
		return nil, errors.New("read denied")
	}
	return valueClone(a.values[name]), nil
}
func (a *fakeAdapter) Write(name string, value *osproxy.Value) error {
	a.writes++
	if a.onWrite != nil {
		a.onWrite()
	}
	if a.failWrite == a.writes {
		return errors.New("write denied")
	}
	if value == nil {
		delete(a.values, name)
	} else {
		a.values[name] = valueClone(value)
	}
	return nil
}
func (a *fakeAdapter) Notify() error {
	a.notifies++
	if a.mutateOnNotify != "" {
		a.values[a.mutateOnNotify] = &osproxy.Value{Kind: 1, Bytes: []byte("foreign notification reaction")}
	}
	if a.failNotify {
		return errors.New("notify denied")
	}
	return nil
}

type fakeAuthority struct {
	mu         sync.Mutex
	state      string
	reachable  bool
	addr       string
	wrongOwner bool
	skip       bool
}

func (a *fakeAuthority) WithReady(_ context.Context, owner Owner, action func(ReadyEngine) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.skip {
		return nil
	}
	if a.wrongOwner {
		owner.Engine.InstanceID = strings.Repeat("9", 32)
	}
	return action(ReadyEngine{Owner: owner, State: a.state, ProxyAddr: a.addr, ProxyReachable: a.reachable})
}
func (a *fakeAuthority) WithRelease(_ context.Context, _ Owner, action func() error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return action()
}
func (a *fakeAuthority) WithStopped(_ context.Context, _ Owner, action func() error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != "stopped" {
		return errors.New("lifetime lock held")
	}
	return action()
}

type fixture struct {
	manager      *Manager
	claim        Claim
	store        *fakeStore
	adapter      *fakeAdapter
	authority    *fakeAuthority
	transactions *fakeTransactions
	resolver     *fakeResolver
}

func setup(t *testing.T) *fixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	resolver := &fakeResolver{root: t.TempDir(), user: "windows:S-1-5-21-synthetic"}
	store := &fakeStore{}
	adapter := &fakeAdapter{values: map[string]*osproxy.Value{"AutoConfigURL": {Kind: 2, Bytes: []byte{1, 0, 255}}, "ProxyServer": {Kind: 3, Bytes: []byte{}}}}
	authority := &fakeAuthority{state: "ready", reachable: true, addr: "127.0.0.1:7890"}
	transactions := &fakeTransactions{store: store}
	claim := Claim{UserScope: resolver.user, Home: home, Engine: EngineIdentity{InstanceID: strings.Repeat("1", 32), Home: home, Generation: strings.Repeat("2", 64), ProtocolVersion: contracts.ControlProtocolVersion, PID: 123}}
	return &fixture{manager: &Manager{Resolver: resolver, Transactions: transactions, Factory: adapter, Authority: authority}, claim: claim, store: store, adapter: adapter, authority: authority, transactions: transactions, resolver: resolver}
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func valuesClone(values map[string]*osproxy.Value) map[string]*osproxy.Value {
	copy := map[string]*osproxy.Value{}
	for k, v := range values {
		copy[k] = valueClone(v)
	}
	return copy
}

func TestImmutableWALPrecedesWritesAndRepeatedAcquireReleaseRoundtrip(t *testing.T) {
	f := setup(t)
	original := valuesClone(f.adapter.values)
	f.store.onCreate = func() {
		if f.adapter.writes != 0 {
			t.Fatal("WAL was persisted after registry write")
		}
	}
	receipt, err := f.manager.Acquire(context.Background(), f.claim)
	if err != nil {
		t.Fatal(err)
	}
	wal := clone(f.store.data)
	writes := f.adapter.writes
	second, err := f.manager.Acquire(context.Background(), f.claim)
	if err != nil {
		t.Fatal(err)
	}
	if second.LeaseID != receipt.LeaseID || f.store.creates != 1 || f.adapter.writes != writes || !reflect.DeepEqual(wal, f.store.data) {
		t.Fatal("repeat acquire replaced WAL or rewrote fields")
	}
	if _, err := f.manager.Release(context.Background(), f.claim, receipt.LeaseID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, f.adapter.values) || f.store.data != nil {
		t.Fatal("raw settings were not restored")
	}
	opens := f.adapter.opens
	result, err := f.manager.Release(context.Background(), f.claim, receipt.LeaseID)
	if err != nil || !result.Noop || f.adapter.opens != opens {
		t.Fatal("absent WAL was not adapter-free no-op", err)
	}
}

func TestForeignHomeEngineOrSIDCannotTouchExistingLease(t *testing.T) {
	for _, which := range []string{"home", "engine", "generation", "sid", "protocol"} {
		t.Run(which, func(t *testing.T) {
			f := setup(t)
			if _, err := f.manager.Acquire(context.Background(), f.claim); err != nil {
				t.Fatal(err)
			}
			claim := f.claim
			switch which {
			case "home":
				claim.Home += "-other"
				claim.Engine.Home = claim.Home
			case "engine":
				claim.Engine.InstanceID = strings.Repeat("3", 32)
			case "generation":
				claim.Engine.Generation = strings.Repeat("4", 64)
			case "sid":
				claim.UserScope += "-foreign"
			case "protocol":
				claim.Engine.ProtocolVersion++
			}
			before := valuesClone(f.adapter.values)
			wal := clone(f.store.data)
			opens := f.adapter.opens
			if _, err := f.manager.Acquire(context.Background(), claim); err == nil {
				t.Fatal("foreign acquire accepted")
			}
			f.authority.state = "stopped"
			if _, err := f.manager.Recover(context.Background(), claim); err == nil {
				t.Fatal("foreign recover accepted")
			}
			if !reflect.DeepEqual(before, f.adapter.values) || !reflect.DeepEqual(wal, f.store.data) || f.adapter.opens != opens {
				t.Fatal("foreign operation entered OS adapter")
			}
		})
	}
}

func TestStoppingOtherHomeIsNoopButExplicitRecoverIsRefused(t *testing.T) {
	f := setup(t)
	if _, err := f.manager.Acquire(context.Background(), f.claim); err != nil {
		t.Fatal(err)
	}
	claim := f.claim
	claim.Home += "-other"
	claim.Engine.Home = claim.Home
	before := valuesClone(f.adapter.values)
	opens := f.adapter.opens
	result, err := f.manager.ReleaseOwned(context.Background(), claim)
	if err != nil || !result.Noop || result.Owned {
		t.Fatal("other home stop should skip lease", err)
	}
	f.authority.state = "stopped"
	if _, err := f.manager.Recover(context.Background(), claim); err == nil {
		t.Fatal("explicit other-home recovery accepted")
	}
	if f.adapter.opens != opens || !reflect.DeepEqual(before, f.adapter.values) || f.store.data == nil {
		t.Fatal("other home touched owner lease")
	}
}

func TestReadinessAndBoundaryFailuresCannotEnterStorageOrOS(t *testing.T) {
	for _, which := range []string{"stopped", "degraded", "unreachable", "remote", "wildcard", "zeroport", "wrongowner", "skip", "scope", "sid", "lock"} {
		t.Run(which, func(t *testing.T) {
			f := setup(t)
			switch which {
			case "stopped", "degraded":
				f.authority.state = which
			case "unreachable":
				f.authority.reachable = false
			case "remote":
				f.authority.addr = "192.0.2.1:7890"
			case "wildcard":
				f.authority.addr = "0.0.0.0:7890"
			case "zeroport":
				f.authority.addr = "127.0.0.1:0"
			case "wrongowner":
				f.authority.wrongOwner = true
			case "skip":
				f.authority.skip = true
			case "scope":
				f.resolver.err = errors.New("foreign home owner")
			case "sid":
				f.claim.UserScope += "-other"
			case "lock":
				f.transactions.refuse = true
			}
			if _, err := f.manager.Acquire(context.Background(), f.claim); err == nil {
				t.Fatal("refusal was reported success")
			}
			if f.adapter.opens != 0 || f.adapter.writes != 0 || f.store.data != nil {
				t.Fatal("unauthorized operation entered OS or persisted WAL")
			}
		})
	}
}

func TestSnapshotAndPersistenceFailurePrecedeAnyWrites(t *testing.T) {
	for _, which := range []string{"load", "read", "persist"} {
		t.Run(which, func(t *testing.T) {
			f := setup(t)
			switch which {
			case "load":
				f.store.failLoad = true
			case "read":
				f.adapter.failRead = 3
			case "persist":
				f.store.failCreate = true
			}
			if _, err := f.manager.Acquire(context.Background(), f.claim); err == nil {
				t.Fatal("failure accepted")
			}
			if f.adapter.writes != 0 {
				t.Fatal("wrote without durable WAL")
			}
		})
	}
}

func TestEveryPartialAcquireFailureCanResumeOrRecoverWithoutReplacingWAL(t *testing.T) {
	for offset := 1; offset <= 4; offset++ {
		for _, resume := range []bool{true, false} {
			t.Run(fmt.Sprintf("field%d-resume%t", offset, resume), func(t *testing.T) {
				f := setup(t)
				original := valuesClone(f.adapter.values)
				f.adapter.failWrite = offset
				if _, err := f.manager.Acquire(context.Background(), f.claim); err == nil {
					t.Fatal("partial acquire accepted")
				}
				wal := clone(f.store.data)
				if wal == nil {
					t.Fatal("lost WAL")
				}
				f.adapter.failWrite = 0
				if resume {
					if _, err := f.manager.Acquire(context.Background(), f.claim); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(wal, f.store.data) || f.store.creates != 1 {
						t.Fatal("resume replaced WAL")
					}
				}
				f.authority.state = "stopped"
				if _, err := f.manager.Recover(context.Background(), f.claim); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(original, f.adapter.values) || f.store.data != nil {
					t.Fatal("partial acquisition did not recover")
				}
			})
		}
	}
}

func TestRestoreWriteNotifyOrRemoveFailureRetainsWALAndRetries(t *testing.T) {
	for _, which := range []string{"write1", "write2", "write3", "write4", "notify", "remove"} {
		t.Run(which, func(t *testing.T) {
			f := setup(t)
			original := valuesClone(f.adapter.values)
			receipt, err := f.manager.Acquire(context.Background(), f.claim)
			if err != nil {
				t.Fatal(err)
			}
			switch which {
			case "write1":
				f.adapter.failWrite = f.adapter.writes + 1
			case "write2":
				f.adapter.failWrite = f.adapter.writes + 2
			case "write3":
				f.adapter.failWrite = f.adapter.writes + 3
			case "write4":
				f.adapter.failWrite = f.adapter.writes + 4
			case "notify":
				f.adapter.failNotify = true
			case "remove":
				f.store.failRemove = true
			}
			if _, err := f.manager.Release(context.Background(), f.claim, receipt.LeaseID); err == nil {
				t.Fatal("restore failure accepted")
			}
			if f.store.data == nil {
				t.Fatal("failed recovery removed WAL")
			}
			f.adapter.failWrite = 0
			f.adapter.failNotify = false
			f.store.failRemove = false
			if _, err := f.manager.Release(context.Background(), f.claim, receipt.LeaseID); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, f.adapter.values) {
				t.Fatal("retry did not restore raw originals")
			}
		})
	}
}

func TestNotificationFailureDuringAcquireIsRetainedAndRetryable(t *testing.T) {
	f := setup(t)
	f.adapter.failNotify = true
	if _, err := f.manager.Acquire(context.Background(), f.claim); err == nil {
		t.Fatal("failed notify accepted")
	}
	wal := clone(f.store.data)
	writes := f.adapter.writes
	f.adapter.failNotify = false
	if _, err := f.manager.Acquire(context.Background(), f.claim); err != nil {
		t.Fatal(err)
	}
	if f.store.creates != 1 || f.adapter.writes != writes || !reflect.DeepEqual(wal, f.store.data) {
		t.Fatal("notify retry lost original WAL")
	}
}

func TestForeignChangesAndEveryFreshRestoreReadFailClosed(t *testing.T) {
	for i, name := range osproxy.Fields {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			receipt, err := f.manager.Acquire(context.Background(), f.claim)
			if err != nil {
				t.Fatal(err)
			}
			// Four group reads pass; mutate the next field at its pre-write read.
			f.adapter.mutateAt = f.adapter.reads + 5 + i*2
			f.adapter.mutateField = name
			_, err = f.manager.Release(context.Background(), f.claim, receipt.LeaseID)
			requireCode(t, err, "system_proxy_ownership_conflict")
			if !reflect.DeepEqual(f.adapter.values[name], &osproxy.Value{Kind: 1, Bytes: []byte{99}}) || f.store.data == nil {
				t.Fatal("overwrote third-party field or removed WAL")
			}
		})
	}
}

func TestLiveEngineCannotRecoverAndPhaseGateSerializesNormalStop(t *testing.T) {
	f := setup(t)
	entered := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	f.adapter.onWrite = func() { once.Do(func() { close(entered); <-resume }) }
	acquired := make(chan error, 1)
	go func() { _, err := f.manager.Acquire(context.Background(), f.claim); acquired <- err }()
	<-entered
	stopped := make(chan struct{})
	go func() { f.authority.mu.Lock(); f.authority.state = "stopped"; f.authority.mu.Unlock(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop crossed live acquire phase gate")
	case <-time.After(30 * time.Millisecond):
	}
	close(resume)
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
	<-stopped
	f.authority.state = "ready"
	opens := f.adapter.opens
	if _, err := f.manager.Recover(context.Background(), f.claim); err == nil {
		t.Fatal("recovered live engine lease")
	}
	if f.adapter.opens != opens {
		t.Fatal("live recover entered adapter")
	}
	f.authority.state = "stopped"
	if _, err := f.manager.Recover(context.Background(), f.claim); err != nil {
		t.Fatal(err)
	}
}

func TestCompetingRecoveryCannotDeleteInflightPersistedJournal(t *testing.T) {
	f := setup(t)
	f.store.onCreate = func() {
		other := *f.manager
		other.Authority = &fakeAuthority{state: "stopped"}
		if _, err := other.Recover(context.Background(), f.claim); err == nil {
			t.Fatal("competing transaction accepted")
		}
		if f.store.data == nil || f.adapter.writes != 0 {
			t.Fatal("competing restore removed inflight WAL")
		}
	}
	if _, err := f.manager.Acquire(context.Background(), f.claim); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyCorruptAndIncompatibleRecordsNeverEnterAdapter(t *testing.T) {
	for _, data := range [][]byte{[]byte("not-json"), []byte(`{"schema_version":1,"owner":"desktop-gui","fields":[]}`), []byte(`{"schema_version":99}`)} {
		f := setup(t)
		f.store.data = clone(data)
		f.authority.state = "stopped"
		_, err := f.manager.Recover(context.Background(), f.claim)
		if err == nil || !strings.Contains(err.Error(), "将记录重命名归档") || !strings.Contains(err.Error(), f.resolver.root) {
			t.Fatal("missing manual recovery path", err)
		}
		if f.adapter.opens != 0 || !reflect.DeepEqual(data, f.store.data) {
			t.Fatal("invalid record entered adapter or was changed")
		}
	}
}

func TestNumericBytesStrictRoundtripAndJournalContractValidation(t *testing.T) {
	encoded, err := json.Marshal(NumericBytes{0, 1, 255})
	if err != nil || string(encoded) != "[0,1,255]" {
		t.Fatal("bytes became base64", string(encoded), err)
	}
	for _, data := range []string{`"AAH/"`, `null`, `[-1]`, `[256]`, `[1.5]`, `[null]`, `[true]`, `["1"]`, `[65536]`, `{}`} {
		var value NumericBytes
		if json.Unmarshal([]byte(data), &value) == nil {
			t.Fatal("invalid numeric bytes accepted", data)
		}
	}
	f := setup(t)
	if _, err := f.manager.Acquire(context.Background(), f.claim); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(f.store.data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, which := range []string{"unknown", "protocol", "missing-original", "base64", "duplicate", "uppercase", "null-kind"} {
		t.Run(which, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(f.store.data, &value); err != nil {
				t.Fatal(err)
			}
			switch which {
			case "uppercase":
				value["lease_owner"].(map[string]any)["engine_identity"].(map[string]any)["instance_id"] = strings.Repeat("A", 32)
			case "null-kind":
				value["fields"].([]any)[0].(map[string]any)["written"].(map[string]any)["kind"] = nil
			case "unknown":
				value["extra"] = true
			case "protocol":
				value["lease_owner"].(map[string]any)["engine_identity"].(map[string]any)["control_protocol_version"] = 999
			case "missing-original":
				delete(value["fields"].([]any)[0].(map[string]any), "original")
			case "base64":
				value["fields"].([]any)[0].(map[string]any)["written"].(map[string]any)["bytes"] = "AQAAAA=="
			}
			data, _ := json.Marshal(value)
			if which == "duplicate" {
				data = []byte(strings.Replace(string(data), `"schema_version":2`, `"schema_version":2,"schema_version":2`, 1))
			}
			if _, err := decode(data); err == nil {
				t.Fatal("invalid journal accepted", which)
			}
		})
	}
}

func TestRepeatedRecoveryAndForeignGroupConflictAreAdapterSafe(t *testing.T) {
	f := setup(t)
	if _, err := f.manager.Acquire(context.Background(), f.claim); err != nil {
		t.Fatal(err)
	}
	writes := f.adapter.writes
	f.adapter.values["ProxyServer"] = &osproxy.Value{Kind: 1, Bytes: []byte("foreign settings")}
	f.authority.state = "stopped"
	if _, err := f.manager.Recover(context.Background(), f.claim); err == nil {
		t.Fatal("foreign group accepted")
	}
	if f.adapter.writes != writes || f.store.data == nil {
		t.Fatal("known group conflict caused a partial restore")
	}
	// Put the lease's written value back only in this fake registry, then verify
	// recovery and repeated recovery. This is not automated conflict resolution.
	record, err := decode(f.store.data)
	if err != nil {
		t.Fatal(err)
	}
	f.adapter.values["ProxyServer"] = fromRaw(record.Fields[1].Written)
	if _, err := f.manager.Recover(context.Background(), f.claim); err != nil {
		t.Fatal(err)
	}
	opens := f.adapter.opens
	result, err := f.manager.Recover(context.Background(), f.claim)
	if err != nil || !result.Noop || f.adapter.opens != opens {
		t.Fatal("repeat recovery entered adapter", err)
	}
}

func TestEveryRestoreReadFailurePreservesRetryableJournal(t *testing.T) {
	for offset := 1; offset <= 20; offset++ {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			f := setup(t)
			original := valuesClone(f.adapter.values)
			receipt, err := f.manager.Acquire(context.Background(), f.claim)
			if err != nil {
				t.Fatal(err)
			}
			f.adapter.failRead = f.adapter.reads + offset
			if _, err := f.manager.Release(context.Background(), f.claim, receipt.LeaseID); err == nil {
				t.Fatal("read failure ignored")
			}
			if f.store.data == nil {
				t.Fatal("failed read removed journal")
			}
			f.adapter.failRead = 0
			if _, err := f.manager.Release(context.Background(), f.claim, receipt.LeaseID); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, f.adapter.values) {
				t.Fatal("retry lost originals")
			}
		})
	}
}

func TestNotificationTriggeredForeignChangesRetainJournal(t *testing.T) {
	for _, restoring := range []bool{false, true} {
		t.Run(fmt.Sprint(restoring), func(t *testing.T) {
			f := setup(t)
			if restoring {
				if _, err := f.manager.Acquire(context.Background(), f.claim); err != nil {
					t.Fatal(err)
				}
			}
			f.adapter.mutateOnNotify = "ProxyServer"
			var err error
			if restoring {
				f.authority.state = "stopped"
				_, err = f.manager.Recover(context.Background(), f.claim)
			} else {
				_, err = f.manager.Acquire(context.Background(), f.claim)
			}
			requireCode(t, err, "system_proxy_ownership_conflict")
			if f.store.data == nil || f.store.removes != 0 {
				t.Fatal("notification race removed recovery evidence")
			}
			if !reflect.DeepEqual(f.adapter.values["ProxyServer"], &osproxy.Value{Kind: 1, Bytes: []byte("foreign notification reaction")}) {
				t.Fatal("notification conflict was overwritten")
			}
		})
	}
}

func TestCaseInsensitiveFieldAliasesCannotPanicOrGrantRecovery(t *testing.T) {
	f := setup(t)
	if _, err := f.manager.Acquire(context.Background(), f.claim); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(f.store.data, &envelope); err != nil {
		t.Fatal(err)
	}
	var fields []json.RawMessage
	if err := json.Unmarshal(envelope["fields"], &fields); err != nil {
		t.Fatal(err)
	}
	short, _ := json.Marshal(fields[:1])
	data := strings.Replace(string(f.store.data), `"fields":`+string(envelope["fields"]), `"fields":`+string(short), 1)
	data = strings.TrimSuffix(data, "}") + `,"Fields":` + string(envelope["fields"]) + `}`
	f.store.data = []byte(data)
	f.authority.state = "stopped"
	opens := f.adapter.opens
	_, err := f.manager.Recover(context.Background(), f.claim)
	requireCode(t, err, "system_proxy_journal_invalid")
	if f.adapter.opens != opens || string(f.store.data) != data {
		t.Fatal("invalid alias record entered adapter or was changed")
	}
}
