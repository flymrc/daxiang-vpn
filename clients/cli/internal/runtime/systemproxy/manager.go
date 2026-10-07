package systemproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"unicode/utf16"

	osproxy "zongheng-vpn/shared/systemproxy"
)

const bypass = "localhost;*.localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;192.168.*;<local>"

func withPath(err error, path string) error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		copy := *typed
		copy.JournalPath = path
		return &copy
	}
	return &Error{Code: "system_proxy_operation_failed", Message: "系统代理操作未完成，记录已保留", JournalPath: path, Cause: err}
}

func (m *Manager) resolve(ctx context.Context, home string) (osproxy.Scope, error) {
	if m.Resolver == nil || m.Transactions == nil {
		return osproxy.Scope{}, failure("system_proxy_unconfigured", "缺少用户 scope 或事务边界", nil)
	}
	scope, err := m.Resolver.Resolve(ctx, home)
	if err != nil {
		return scope, failure("system_proxy_scope_refused", "无法验证当前 OS 用户与客户端目录，未修改代理", err)
	}
	if scope.UserScope == "" || scope.HomeIdentity == "" || !filepath.IsAbs(scope.Root) ||
		filepath.Dir(scope.JournalPath) != scope.Root || filepath.Dir(scope.LockPath) != scope.Root ||
		filepath.Base(scope.JournalPath) != "proxy-backup.json" || filepath.Base(scope.LockPath) != "proxy-operation.lock" {
		return scope, failure("system_proxy_scope_refused", "用户 scope 路径不满足固定事务合同", nil)
	}
	return scope, nil
}

func (m *Manager) claim(ctx context.Context, claim Claim) (osproxy.Scope, Owner, error) {
	scope, err := m.resolve(ctx, claim.Home)
	owner := Owner{UserScope: claim.UserScope, Home: scope.HomeIdentity, Engine: claim.Engine}
	if err != nil {
		return scope, owner, err
	}
	if claim.UserScope != scope.UserScope || !validOwner(owner) {
		return scope, owner, withPath(failure("system_proxy_owner_refused", "当前 token、原用户或完整引擎身份不匹配，未修改任何 HKCU", nil), scope.JournalPath)
	}
	if m.Authority == nil || m.Factory == nil {
		return scope, owner, withPath(failure("system_proxy_unconfigured", "缺少真实引擎授权边界或 OS adapter", nil), scope.JournalPath)
	}
	return scope, owner, nil
}

// transact detects an incorrectly implemented boundary that silently skips or
// invokes its callback twice. It cannot prove that a caller held the real gate;
// controller integration tests must verify that Authority contract separately.
func (m *Manager) transact(ctx context.Context, scope osproxy.Scope, action func(osproxy.Store) error) error {
	calls := 0
	err := m.Transactions.WithLocked(ctx, scope, func(store osproxy.Store) error {
		calls++
		if calls != 1 {
			return failure("system_proxy_boundary_invalid", "事务 callback 重复，拒绝再次执行", nil)
		}
		return action(store)
	})
	if err == nil && calls != 1 {
		err = failure("system_proxy_boundary_invalid", "未进入受保护事务，不能报告成功", nil)
	}
	return withPath(err, scope.JournalPath)
}

func authorityResult(err error, calls int, path string) error {
	if err == nil && calls != 1 {
		err = failure("system_proxy_boundary_invalid", "授权 callback 未执行，不能报告成功", nil)
	}
	return withPath(err, path)
}

func load(store osproxy.Store) (*journal, error) {
	data, err := store.Load()
	if err != nil {
		return nil, failure("system_proxy_journal_read_failed", "无法读取恢复记录，未修改系统代理", err)
	}
	if data == nil {
		return nil, nil
	}
	value, err := decode(data)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func stringValue(value string) *rawValue {
	units := append(utf16.Encode([]rune(value)), 0)
	data := make(NumericBytes, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return &rawValue{Kind: 1, Bytes: data}
}

func desired(ready ReadyEngine) ([4]*rawValue, error) {
	var result [4]*rawValue
	host, portText, err := net.SplitHostPort(ready.ProxyAddr)
	port, portErr := strconv.Atoi(portText)
	// The authenticated engine must identify a reachable local endpoint, not
	// a wildcard bind, remote proxy, or arbitrary injected WinINET expression.
	ip := net.ParseIP(host)
	if err != nil || portErr != nil || port < 1 || port > 65535 || ip == nil || !ip.IsLoopback() {
		return result, failure("system_proxy_ready_refused", "认证引擎的本地代理地址无效", nil)
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	result[0] = &rawValue{Kind: 4, Bytes: NumericBytes{1, 0, 0, 0}}
	result[1] = stringValue("http=" + address + ";https=" + address + ";socks=" + address)
	result[2] = stringValue(bypass)
	return result, nil
}

func equal(a, b *rawValue) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Kind == b.Kind && bytes.Equal(a.Bytes, b.Bytes)
}

func read(adapter osproxy.Adapter, name string) (*rawValue, error) {
	value, err := adapter.Read(name)
	if err != nil {
		return nil, failure("system_proxy_read_failed", "读取代理字段失败："+name, err)
	}
	if value != nil && value.Kind > 11 {
		return nil, failure("system_proxy_read_failed", "注册表字段类型不支持："+name, nil)
	}
	return toRaw(value), nil
}

func conflict(name string) error {
	return failure("system_proxy_ownership_conflict", name+" 已被其他程序修改；未覆盖新设置，恢复记录已保留。请核对 Windows 代理设置；若确认保留新设置，可将记录重命名归档后重试断开或退出", nil)
}

func apply(adapter osproxy.Adapter, record *journal, restoring bool) error {
	// Group preflight prevents knowingly combining somebody else's ProxyServer
	// with our ProxyEnable. Fresh per-field reads below narrow, but cannot remove,
	// the race with unrelated registry writers: Windows offers no global CAS here.
	for _, field := range record.Fields {
		current, err := read(adapter, field.Name)
		if err != nil {
			return err
		}
		if !equal(current, field.Original) && !equal(current, field.Written) {
			return conflict(field.Name)
		}
	}
	for _, field := range record.Fields {
		target := field.Written
		if restoring {
			target = field.Original
		}
		current, err := read(adapter, field.Name)
		if err != nil {
			return err
		}
		if equal(current, target) {
			continue
		}
		if !equal(current, field.Original) && !equal(current, field.Written) {
			return conflict(field.Name)
		}
		if err := adapter.Write(field.Name, fromRaw(target)); err != nil {
			return failure("system_proxy_write_failed", "代理字段写入失败："+field.Name+"；恢复记录已保留，请重试释放或恢复", err)
		}
		current, err = read(adapter, field.Name)
		if err != nil {
			return err
		}
		if !equal(current, target) {
			return conflict(field.Name)
		}
	}
	for _, field := range record.Fields {
		target := field.Written
		if restoring {
			target = field.Original
		}
		current, err := read(adapter, field.Name)
		if err != nil {
			return err
		}
		if !equal(current, target) {
			return conflict(field.Name)
		}
	}
	if err := adapter.Notify(); err != nil {
		return failure("system_proxy_notify_failed", "WinINET 通知失败；恢复记录已保留，请重试释放或恢复", err)
	}
	// Notification may itself trigger another proxy application's reaction.
	// Recheck before success/removing the WAL; this still is not a global CAS.
	for _, field := range record.Fields {
		target := field.Written
		if restoring {
			target = field.Original
		}
		current, err := read(adapter, field.Name)
		if err != nil {
			return err
		}
		if !equal(current, target) {
			return conflict(field.Name)
		}
	}
	return nil
}

func (m *Manager) Acquire(ctx context.Context, claim Claim) (Receipt, error) {
	scope, owner, err := m.claim(ctx, claim)
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	calls := 0
	err = m.Authority.WithReady(ctx, owner, func(ready ReadyEngine) error {
		calls++
		if calls != 1 {
			return failure("system_proxy_boundary_invalid", "授权 callback 重复", nil)
		}
		if ready.Owner != owner || ready.State != "ready" || !ready.ProxyReachable {
			return failure("system_proxy_ready_refused", "没有当前引擎的认证就绪与可达授权，未修改代理", nil)
		}
		written, err := desired(ready)
		if err != nil {
			return err
		}
		return m.transact(ctx, scope, func(store osproxy.Store) error {
			record, err := load(store)
			if err != nil {
				return err
			}
			reused := record != nil
			if record != nil {
				if record.LeaseOwner != owner {
					return failure("system_proxy_lease_busy", "系统代理已有其他 home 或引擎的租约，未认领或恢复", nil)
				}
				for i, field := range record.Fields {
					if !equal(field.Written, written[i]) {
						return failure("system_proxy_lease_settings_changed", "本次代理地址与已有租约不一致，请先释放原租约", nil)
					}
				}
			}
			adapter, err := m.Factory.Open(scope)
			if err != nil {
				return failure("system_proxy_adapter_refused", "无法验证当前用户的 OS adapter", err)
			}
			if record == nil {
				var id [16]byte
				if _, err := rand.Read(id[:]); err != nil {
					return failure("system_proxy_journal_write_failed", "无法生成租约身份", err)
				}
				record = &journal{SchemaVersion: journalVersion, Owner: "cli-runtime", Scope: "wininet-hkcu", LeaseID: hex.EncodeToString(id[:]), LeaseOwner: owner}
				for i, name := range osproxy.Fields {
					original, err := read(adapter, name)
					if err != nil {
						return err
					}
					record.Fields = append(record.Fields, fieldChange{Name: name, Original: original, Written: written[i]})
				}
				data, err := json.Marshal(record)
				if err != nil || len(data) > maxJournalBytes {
					return failure("system_proxy_journal_write_failed", "无法编码有界恢复记录", err)
				}
				// Preserve ownership even when Create leaves a partial durable
				// record. The controller then retains its restoration obligation.
				receipt = Receipt{LeaseID: record.LeaseID, Owner: &record.LeaseOwner, Owned: true}
				if err := store.Create(data); err != nil {
					return failure("system_proxy_journal_write_failed", "恢复记录未成功持久化，未写代理；保留可能的部分记录", err)
				}
			}
			receipt = Receipt{LeaseID: record.LeaseID, Owner: &record.LeaseOwner, Owned: true, Noop: reused}
			if err := apply(adapter, record, false); err != nil {
				return err
			}
			return nil
		})
	})
	return receipt, authorityResult(err, calls, scope.JournalPath)
}

func (m *Manager) Release(ctx context.Context, claim Claim, leaseID string) (Receipt, error) {
	if !exactHex(leaseID, 16) {
		return Receipt{}, failure("system_proxy_lease_refused", "显式释放需要有效的 lease ID", nil)
	}
	return m.restore(ctx, claim, leaseID, false, false)
}

// ReleaseOwned is a stop/shutdown hook: another home's lease is a no-op,
// allowing this engine to stop without touching or stopping the other engine.
func (m *Manager) ReleaseOwned(ctx context.Context, claim Claim) (Receipt, error) {
	return m.restore(ctx, claim, "", false, true)
}

func (m *Manager) Recover(ctx context.Context, claim Claim) (Receipt, error) {
	return m.restore(ctx, claim, "", true, false)
}

func (m *Manager) restore(ctx context.Context, claim Claim, leaseID string, recovering, foreignNoop bool) (Receipt, error) {
	scope, owner, err := m.claim(ctx, claim)
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{Noop: true}
	calls := 0
	action := func() error {
		calls++
		if calls != 1 {
			return failure("system_proxy_boundary_invalid", "授权 callback 重复", nil)
		}
		return m.transact(ctx, scope, func(store osproxy.Store) error {
			record, err := load(store)
			if err != nil {
				return err
			}
			if record == nil {
				return nil
			} // no adapter is opened, even for reads.
			if record.LeaseOwner != owner {
				if foreignNoop {
					return nil
				}
				return failure("system_proxy_owner_refused", "SID、home 或引擎身份不属于该租约，未修改代理", nil)
			}
			if leaseID != "" && leaseID != record.LeaseID {
				return failure("system_proxy_lease_refused", "租约 ID 已改变，未修改代理", nil)
			}
			adapter, err := m.Factory.Open(scope)
			if err != nil {
				return failure("system_proxy_adapter_refused", "无法验证当前用户的 OS adapter", err)
			}
			if err := apply(adapter, record, true); err != nil {
				return err
			}
			if err := store.Remove(); err != nil {
				return failure("system_proxy_journal_remove_failed", "设置已恢复，但恢复记录删除失败；请重试释放或恢复", err)
			}
			receipt = Receipt{LeaseID: record.LeaseID, Owner: &record.LeaseOwner, Owned: true}
			return nil
		})
	}
	if recovering {
		err = m.Authority.WithStopped(ctx, owner, action)
	} else {
		err = m.Authority.WithRelease(ctx, owner, action)
	}
	return receipt, authorityResult(err, calls, scope.JournalPath)
}

// Inspect is a read-only journal projection. "recorded" is deliberately not
// "active": persisted intent does not establish engine readiness or OS state.
func (m *Manager) Inspect(ctx context.Context, home string) (Observation, error) {
	scope, err := m.resolve(ctx, home)
	result := Observation{State: "absent", JournalPath: scope.JournalPath}
	if err != nil {
		return result, err
	}
	err = m.transact(ctx, scope, func(store osproxy.Store) error {
		record, err := load(store)
		if err != nil {
			return err
		}
		if record == nil {
			return nil
		}
		result.State = "recorded"
		if record.LeaseOwner.UserScope != scope.UserScope || record.LeaseOwner.Home != scope.HomeIdentity {
			result.State = "foreign"
		}
		result.LeaseID, result.Owner = record.LeaseID, &record.LeaseOwner
		return nil
	})
	return result, err
}
