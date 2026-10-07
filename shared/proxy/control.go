package proxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/contracts"
	"zongheng-vpn/shared/paths"
)

const (
	EngineCommand          = "__engine"
	KillCommand            = "__killpid" // Retained only to explicitly reject old unsafe callers.
	HomeFlag               = "--home"
	ControlProtocolVersion = contracts.ControlProtocolVersion
)

// EngineIdentity identifies an instance, not an integer process slot. The
// control secret is deliberately absent from all public status structures.
type EngineIdentity = contracts.EngineIdentity

type EngineStatus struct {
	State     string         `json:"engine_state"`
	Identity  EngineIdentity `json:"identity"`
	ProxyAddr string         `json:"proxy_addr"`
}

type RuntimeError struct {
	Code    string
	Message string
}

func (e *RuntimeError) Error() string { return e.Message }

var (
	ErrLegacyState        = &RuntimeError{Code: "engine_legacy_state", Message: "旧版引擎仅有 PID 记录，无法验证进程身份；请人工核验旧引擎的可执行路径、客户端目录和运行身份后停止，确认已退出再归档或清理旧 PID 记录。未发送终止信号"}
	ErrControlIdentity    = &RuntimeError{Code: "engine_identity_unverified", Message: "引擎控制身份无法验证，未执行停止；请检查本地状态及客户端版本"}
	ErrControlUnavailable = &RuntimeError{Code: "engine_control_unavailable", Message: "引擎控制通道不可达，未向任何 PID 发送信号；请检查引擎日志后恢复"}
)

type controlRecord struct {
	Identity              EngineIdentity              `json:"identity"`
	Address               string                      `json:"control_address"`
	Secret                string                      `json:"control_secret"`
	ProxyAddr             string                      `json:"proxy_addr"`
	StartDeadlineUnixNano int64                       `json:"start_deadline_unix_nano"`
	Authorization         *config.AuthorizationConfig `json:"authorization,omitempty"`
}

type controlRequest struct {
	Command  string         `json:"command"`
	Nonce    string         `json:"nonce"`
	Identity EngineIdentity `json:"identity"`
	MAC      string         `json:"mac"`
}

type controlResponse struct {
	Command string       `json:"command"`
	Nonce   string       `json:"nonce"`
	Status  EngineStatus `json:"status"`
	MAC     string       `json:"mac"`
}

func statePath(ctx paths.Context) string  { return filepath.Join(ctx.RunDir, "engine-state.json") }
func launchPath(ctx paths.Context) string { return filepath.Join(ctx.RunDir, "engine-launch.json") }

func homeIdentity(root string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(root)
	}
	return root
}

func canonicalContext(ctx paths.Context) (paths.Context, error) {
	root, err := paths.CanonicalRoot(ctx.Root)
	if err != nil {
		return paths.Context{}, err
	}
	return paths.FromRoot(root), nil
}

func randomHex(bytes int) (string, error) {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func validRecord(ctx paths.Context, record controlRecord) bool {
	if record.Authorization != nil && record.Authorization.Validate() != nil {
		return false
	}
	secret, err := hex.DecodeString(record.Secret)
	if err != nil || len(secret) != 32 || len(record.Identity.InstanceID) != 32 || len(record.Identity.Generation) != 64 {
		return false
	}
	if record.Identity.Home != homeIdentity(ctx.Root) || record.Identity.ProtocolVersion != ControlProtocolVersion || record.Identity.PID <= 0 {
		return false
	}
	host, port, err := net.SplitHostPort(record.Address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	portNumber, err := strconv.Atoi(port)
	return err == nil && portNumber > 0 && portNumber <= 65535 && ip != nil && ip.IsLoopback()
}

func loadRecord(ctx paths.Context) (controlRecord, error) {
	data, err := readPrivateFile(ctx, statePath(ctx))
	if err != nil {
		return controlRecord{}, err
	}
	var record controlRecord
	if err := json.Unmarshal(data, &record); err != nil || !validRecord(ctx, record) {
		return controlRecord{}, ErrControlIdentity
	}
	return record, nil
}

func sign(secret string, value any) string {
	key, _ := hex.DecodeString(secret)
	data, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func validMAC(expected, received string) bool {
	want, err := hex.DecodeString(expected)
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(received)
	return err == nil && hmac.Equal(want, got)
}

// Request and response MACs bind the command, nonce and complete identity.
// The secret is never sent to a possibly reused or forged loopback endpoint.
func requestControl(record controlRecord, command string) (EngineStatus, error) {
	nonce, err := randomHex(32)
	if err != nil {
		return EngineStatus{}, err
	}
	request := controlRequest{Command: command, Nonce: nonce, Identity: record.Identity}
	request.MAC = sign(record.Secret, request)
	body, _ := json.Marshal(request)
	timeout := time.Second
	if command == "stop" {
		timeout = 15 * time.Second
	}
	client := &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest(http.MethodPost, "http://"+record.Address+"/v1/control", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return EngineStatus{}, ErrControlUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if command == "stop" && response.StatusCode == http.StatusConflict {
			return EngineStatus{}, &RuntimeError{Code: "engine_stop_refused", Message: "代理租约恢复未完成；引擎继续运行，请检查并处理恢复记录后重试"}
		}
		return EngineStatus{}, ErrControlIdentity
	}
	var result controlResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&result); err != nil {
		return EngineStatus{}, ErrControlIdentity
	}
	mac := result.MAC
	result.MAC = ""
	if result.Command != command || result.Nonce != nonce || result.Status.Identity != record.Identity || !validMAC(sign(record.Secret, result), mac) {
		return EngineStatus{}, ErrControlIdentity
	}
	if result.Status.State != "starting" && result.Status.State != "ready" && result.Status.State != "stopping" {
		return EngineStatus{}, ErrControlIdentity
	}
	return result.Status, nil
}

// Inspect authenticates a live engine. A reachable local proxy port alone is
// never evidence of engine ownership. Stale new-format records are reconciled
// only while holding the instance lock; legacy PID-only state is never killed.
func Inspect(ctx paths.Context) (EngineStatus, error) {
	ctx, err := canonicalContext(ctx)
	if err != nil {
		return EngineStatus{State: "degraded"}, err
	}
	record, err := loadRecord(ctx)
	if errors.Is(err, os.ErrNotExist) {
		lock, lockErr := acquireExistingInstanceLock(ctx)
		if lockErr != nil {
			return EngineStatus{State: "degraded"}, ErrControlUnavailable
		}
		if lock != nil {
			defer releaseRuntimeLock(lock)
		}
		// A child can publish state while we acquire the lock. A held lifetime
		// lock without readable identity is degraded, never "stopped".
		if _, recheck := loadRecord(ctx); !errors.Is(recheck, os.ErrNotExist) {
			return EngineStatus{State: "degraded"}, ErrControlIdentity
		}
		if _, pidErr := os.Stat(ctx.PIDPath); pidErr == nil {
			return EngineStatus{State: "degraded"}, ErrLegacyState
		}
		return EngineStatus{State: "stopped"}, nil
	}
	if err != nil {
		return EngineStatus{State: "degraded"}, err
	}
	status, err := requestControl(record, "status")
	if err == nil {
		return status, nil
	}
	if !errors.Is(err, ErrControlUnavailable) {
		return EngineStatus{State: "degraded"}, err
	}
	lock, lockErr := acquireRuntimeLock(ctx, "engine", 0)
	if lockErr != nil {
		return EngineStatus{State: "degraded"}, ErrControlUnavailable
	}
	defer releaseRuntimeLock(lock)
	// Recheck under the lock so a newly published identity is never removed.
	current, readErr := loadRecord(ctx)
	if readErr == nil && current.Identity == record.Identity {
		if _, retryErr := requestControl(record, "status"); retryErr == nil {
			return EngineStatus{State: "degraded"}, ErrControlIdentity
		}
		if err := removeOwnedRecord(ctx, record.Identity); err != nil {
			return EngineStatus{State: "degraded"}, err
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return EngineStatus{State: "degraded"}, ErrControlIdentity
	}
	return EngineStatus{State: "stopped"}, nil
}

func IsRunning(ctx paths.Context) (bool, int) {
	status, err := Inspect(ctx)
	return err == nil && status.State == "ready", status.Identity.PID
}

func KillPID(_ int) error {
	return &RuntimeError{Code: "unsafe_pid_stop_disabled", Message: "按 PID 强杀已禁用，请使用经身份验证的 stop 命令"}
}

func Stop(ctx paths.Context) (bool, error) {
	ctx, err := canonicalContext(ctx)
	if err != nil {
		return false, err
	}
	status, err := Inspect(ctx)
	if err != nil {
		return false, err
	}
	if status.State == "stopped" {
		return false, nil
	}
	record, err := loadRecord(ctx)
	if err != nil || record.Identity != status.Identity {
		return false, ErrControlIdentity
	}
	if _, err := requestControl(record, "stop"); err != nil {
		return false, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err := loadRecord(ctx)
		if errors.Is(err, os.ErrNotExist) {
			lock, lockErr := acquireExistingInstanceLock(ctx)
			if errors.Is(lockErr, errLockBusy) {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if lockErr != nil {
				return false, lockErr
			}
			if lock != nil {
				_, recheck := loadRecord(ctx)
				releaseRuntimeLock(lock)
				if !errors.Is(recheck, os.ErrNotExist) {
					return false, ErrControlIdentity
				}
			} else if _, recheck := loadRecord(ctx); !errors.Is(recheck, os.ErrNotExist) {
				return false, ErrControlIdentity
			}
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if current.Identity != record.Identity {
			return false, ErrControlIdentity
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false, &RuntimeError{Code: "engine_stop_timeout", Message: "已向验证过的引擎请求停止，但尚未完成；保留恢复状态，未执行 PID 强杀"}
}

func removeOwnedRecord(ctx paths.Context, identity EngineIdentity) error {
	record, err := loadRecord(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.Identity != identity {
		return ErrControlIdentity
	}
	if err := os.Remove(statePath(ctx)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// PID is diagnostic only. Remove only this instance's matching diagnostic.
	if data, err := os.ReadFile(ctx.PIDPath); err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(identity.PID) {
		_ = os.Remove(ctx.PIDPath)
	}
	return nil
}

func prepareLaunch(ctx paths.Context, cfg config.Config) (controlRecord, error) {
	return prepareLaunchWithTimeout(ctx, cfg, 8*time.Second)
}

func prepareLaunchWithTimeout(ctx paths.Context, cfg config.Config, timeout time.Duration) (controlRecord, error) {
	return prepareLaunchMode(ctx, cfg, timeout, false)
}

func prepareLaunchMode(ctx paths.Context, cfg config.Config, timeout time.Duration, fast bool) (controlRecord, error) {
	if err := cfg.ValidateForProxyStart(time.Now()); err != nil {
		return controlRecord{}, err
	}
	status, err := Inspect(ctx)
	if err != nil {
		return controlRecord{}, err
	}
	if status.State != "stopped" {
		return controlRecord{}, fmt.Errorf("已存在引擎实例（%s），请先停止该实例", status.State)
	}
	data, err := readPrivateFile(ctx, ctx.SingBoxConfig)
	if err != nil {
		return controlRecord{}, err
	}
	if cfg.Authorization.Source == config.DeviceV2Source {
		expected, err := singBoxConfigBytes(cfg, fast)
		if err != nil || !bytes.Equal(expected, data) {
			return controlRecord{}, &RuntimeError{Code: "engine_config_refused", Message: "设备启动配置与授权投影不一致"}
		}
	}
	id, err := randomHex(16)
	if err != nil {
		return controlRecord{}, err
	}
	secret, err := randomHex(32)
	if err != nil {
		return controlRecord{}, err
	}
	var authorization *config.AuthorizationConfig
	if cfg.Authorization.Source == config.DeviceV2Source {
		stamp := cfg.Authorization
		authorization = &stamp
	}
	record := controlRecord{Identity: EngineIdentity{InstanceID: id, Home: homeIdentity(ctx.Root), Generation: configGeneration(data, authorization), ProtocolVersion: ControlProtocolVersion}, Secret: secret, ProxyAddr: cfg.LocalProxy.Addr(), StartDeadlineUnixNano: time.Now().Add(timeout).UnixNano(), Authorization: authorization}
	encoded, _ := json.Marshal(record)
	return record, writePrivateFile(ctx, launchPath(ctx), encoded)
}

// Legacy generations retain their existing SHA256. V2 binds the exact
// sing-box bytes and the whole startup authority projection, including epoch
// and applied generation, even when endpoint/routing bytes happen to match.
func configGeneration(content []byte, authorization *config.AuthorizationConfig) string {
	h := sha256.New()
	if authorization != nil {
		h.Write([]byte("zhvpn-device-v2-engine-config\x00"))
		metadata, _ := json.Marshal(authorization)
		h.Write(metadata)
		h.Write([]byte{0})
	}
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// Start's caller must hold the operation transaction lock. The child also
// holds an independent lifetime lock, defending against duplicate launches.
func Start(ctx paths.Context, cfg config.Config, fast bool) error {
	return StartContext(context.Background(), ctx, cfg, fast)
}

// StartContext uses the same exact-child readiness protocol. Cancellation
// withdraws this launch and asks only its authenticated child to stop.
// File I/O and an in-flight control request retain their own bounded limits.
func StartContext(operation context.Context, ctx paths.Context, cfg config.Config, fast bool) error {
	if operation.Err() != nil {
		return &RuntimeError{Code: "engine_start_cancelled", Message: "本次引擎启动已取消"}
	}
	ctx, err := canonicalContext(ctx)
	if err != nil {
		return err
	}
	timeout := 8 * time.Second
	if fast {
		timeout = 20 * time.Second
	}
	record, err := prepareLaunchMode(ctx, cfg, timeout, fast)
	if err != nil {
		return err
	}
	defer removeLaunch(ctx, record.Identity.InstanceID)
	if err := launchEngine(ctx, fast); err != nil {
		return err
	}
	deadline := time.Unix(0, record.StartDeadlineUnixNano)
	for time.Now().Before(deadline) {
		if operation.Err() != nil {
			break
		}
		live, err := loadRecord(ctx)
		if err == nil {
			if live.Identity.InstanceID != record.Identity.InstanceID || live.Identity.Generation != record.Identity.Generation {
				return ErrControlIdentity
			}
			status, err := requestControl(live, "activate")
			if err == nil && status.State == "ready" {
				if operation.Err() != nil {
					break
				}
				return nil
			}
			if operation.Err() != nil {
				break
			}
			if err != nil && !errors.Is(err, ErrControlUnavailable) {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-operation.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	// Cancelling the launch request prevents a late child from starting. If it
	// already owns a control endpoint, request self-shutdown of that exact ID.
	removeLaunch(ctx, record.Identity.InstanceID)
	if live, err := loadRecord(ctx); err == nil && live.Identity.InstanceID == record.Identity.InstanceID {
		_, _ = requestControl(live, "stop")
	}
	if operation.Err() != nil {
		return &RuntimeError{Code: "engine_start_cancelled", Message: "本次引擎启动已取消，残留实例须经控制通道确认恢复"}
	}
	return &RuntimeError{Code: "engine_start_timeout", Message: "引擎未在期限内完成认证就绪；已取消本次启动，残留实例须经控制通道确认恢复"}
}

func removeLaunch(ctx paths.Context, id string) {
	data, err := readPrivateFile(ctx, launchPath(ctx))
	if err != nil {
		return
	}
	var record controlRecord
	if json.Unmarshal(data, &record) == nil && record.Identity.InstanceID == id {
		_ = os.Remove(launchPath(ctx))
	}
}

type engineControl struct {
	hooks        RuntimeHooks
	actionNonces map[string]int64
	ctx          paths.Context
	record       controlRecord
	lock         *os.File
	server       *http.Server
	listener     net.Listener
	mu           sync.RWMutex
	phase        string
	dataStarted  bool
	leaseTimer   *time.Timer
	stopOnce     sync.Once
	done         chan struct{}
}

func beginEngineControl(ctx paths.Context, content []byte) (*engineControl, error) {
	return beginEngineControlWithHooks(ctx, content, nil)
}
func beginEngineControlWithHooks(ctx paths.Context, content []byte, hooks RuntimeHooks) (*engineControl, error) {
	ctx, err := canonicalContext(ctx)
	if err != nil {
		return nil, err
	}
	lock, err := acquireRuntimeLock(ctx, "engine", 0)
	if err != nil {
		return nil, fmt.Errorf("引擎实例锁已被占用：%w", err)
	}
	release := true
	defer func() {
		if release {
			releaseRuntimeLock(lock)
		}
	}()
	data, err := readPrivateFile(ctx, launchPath(ctx))
	if err != nil {
		return nil, fmt.Errorf("缺少有效启动请求：%w", err)
	}
	var record controlRecord
	if json.Unmarshal(data, &record) != nil {
		return nil, ErrControlIdentity
	}
	if record.StartDeadlineUnixNano <= time.Now().UnixNano() {
		return nil, &RuntimeError{Code: "engine_launch_expired", Message: "启动请求已过期，拒绝启动迟到引擎"}
	}
	if record.Authorization != nil && record.Authorization.ValidateFresh(time.Now()) != nil {
		return nil, &RuntimeError{Code: "engine_authority_projection_expired", Message: "设备启动授权投影无效或已过期"}
	}
	if record.Identity.Home != homeIdentity(ctx.Root) || record.Identity.ProtocolVersion != ControlProtocolVersion || record.Identity.Generation != configGeneration(content, record.Authorization) || len(record.Identity.InstanceID) != 32 || len(record.Secret) != 64 {
		return nil, ErrControlIdentity
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	record.Identity.PID = os.Getpid()
	record.Address = listener.Addr().String()
	if !validRecord(ctx, record) {
		listener.Close()
		return nil, ErrControlIdentity
	}
	control := &engineControl{hooks: hooks, ctx: ctx, record: record, lock: lock, listener: listener, phase: "starting", done: make(chan struct{})}
	control.server = &http.Server{Handler: http.HandlerFunc(control.handle), ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 2 * time.Second}
	encoded, _ := json.Marshal(record)
	if err := writePrivateFile(ctx, statePath(ctx), encoded); err != nil {
		listener.Close()
		return nil, err
	}
	if err := writePrivateFile(ctx, ctx.PIDPath, []byte(strconv.Itoa(record.Identity.PID))); err != nil {
		_ = removeOwnedRecord(ctx, record.Identity)
		listener.Close()
		return nil, err
	}
	removeLaunch(ctx, record.Identity.InstanceID)
	// Publishing the control endpoint does not claim readiness. The original
	// launcher must activate this exact identity before its lease expires.
	control.leaseTimer = time.AfterFunc(time.Until(time.Unix(0, record.StartDeadlineUnixNano)), control.expireLease)
	release = false
	go func() { _ = control.server.Serve(listener) }()
	return control, nil
}

func (c *engineControl) ready() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataStarted = true
}

func (c *engineControl) requestStop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == "stopping" {
		return nil
	}
	if c.hooks != nil {
		guard := c.guard()
		err := c.hooks.BeforeStop(context.Background(), guard)
		guard.finish()
		if err != nil {
			return err
		}
	}
	c.phase = "stopping"
	c.stopOnce.Do(func() { close(c.done) })
	return nil
}

func (c *engineControl) expireLease() {
	c.mu.Lock()
	if c.phase == "ready" {
		c.mu.Unlock()
		return
	}
	c.phase = "stopping"
	c.mu.Unlock()
	c.stopOnce.Do(func() { close(c.done) })
}

func (c *engineControl) handle(writer http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/v1/runtime-action" {
		c.handleRuntimeAction(writer, req)
		return
	}
	if req.Method != http.MethodPost || req.URL.Path != "/v1/control" {
		http.NotFound(writer, req)
		return
	}
	var request controlRequest
	if json.NewDecoder(http.MaxBytesReader(writer, req.Body, 8192)).Decode(&request) != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	mac := request.MAC
	request.MAC = ""
	if len(request.Nonce) != 64 || request.Identity != c.record.Identity || (request.Command != "status" && request.Command != "stop" && request.Command != "activate") || !validMAC(sign(c.record.Secret, request), mac) {
		http.Error(writer, "unauthorized", http.StatusForbidden)
		return
	}
	if request.Command == "stop" {
		if err := c.requestStop(); err != nil {
			http.Error(writer, "proxy restoration incomplete", http.StatusConflict)
			return
		}
	}
	c.mu.Lock()
	if request.Command == "activate" && c.phase == "starting" && c.record.Authorization != nil && c.record.Authorization.ValidateFresh(time.Now()) != nil {
		c.phase = "stopping"
		c.stopOnce.Do(func() { close(c.done) })
	}
	if request.Command == "activate" && c.phase == "starting" && c.dataStarted && time.Now().UnixNano() < c.record.StartDeadlineUnixNano {
		c.phase = "ready"
		c.leaseTimer.Stop()
	}
	result := controlResponse{Command: request.Command, Nonce: request.Nonce, Status: EngineStatus{State: c.phase, Identity: c.record.Identity, ProxyAddr: c.record.ProxyAddr}}
	c.mu.Unlock()
	result.MAC = sign(c.record.Secret, result)
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	if request.Command == "stop" {
		c.stopOnce.Do(func() { close(c.done) })
	}
}

func (c *engineControl) close() {
	if c.leaseTimer != nil {
		c.leaseTimer.Stop()
	}
	defer releaseRuntimeLock(c.lock)
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.server.Shutdown(shutdown)
	_ = c.listener.Close()
	_ = removeOwnedRecord(c.ctx, c.record.Identity)
}
