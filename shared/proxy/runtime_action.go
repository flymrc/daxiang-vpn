package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zongheng-vpn/shared/contracts"
	"zongheng-vpn/shared/paths"
)

// RuntimeHooks execute within the owning controller's phase and lifetime gate.
// Hooks must finish synchronously; Guard cannot be used after they return.
// Shared proxy does not know OS settings or import the CLI runtime.
type RuntimeHooks interface {
	Action(context.Context, *RuntimeGuard, RuntimeAction) (contracts.Result, error)
	BeforeStop(context.Context, *RuntimeGuard) error
}

type RuntimeAction struct {
	Command   string `json:"command"`
	UserScope string `json:"user_scope"`
	LeaseID   string `json:"lease_id,omitempty"`
}

// RuntimeGuard is a callback-scoped capability, not a status snapshot. The
// controller holds its phase mutex and lifetime lock until the hook returns.
type RuntimeGuard struct {
	status EngineStatus
	active atomic.Bool
	mu     sync.Mutex
}

func (g *RuntimeGuard) Identity() EngineIdentity { return g.status.Identity }
func (g *RuntimeGuard) WithReady(action func(EngineStatus) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active.Load() || g.status.State != "ready" {
		return &RuntimeError{Code: "engine_not_ready", Message: "引擎没有处于受保护的认证就绪阶段"}
	}
	return action(g.status)
}
func (g *RuntimeGuard) WithRelease(action func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active.Load() || (g.status.State != "ready" && g.status.State != "starting") {
		return &RuntimeError{Code: "engine_operation_pending", Message: "引擎阶段不允许释放代理租约"}
	}
	return action()
}

func (g *RuntimeGuard) finish() { g.mu.Lock(); defer g.mu.Unlock(); g.active.Store(false) }

func (c *engineControl) guard() *RuntimeGuard {
	g := &RuntimeGuard{status: EngineStatus{State: c.phase, Identity: c.record.Identity, ProxyAddr: c.record.ProxyAddr}}
	g.active.Store(true)
	return g
}

type runtimeActionRequest struct {
	Version  int            `json:"version"`
	Nonce    string         `json:"nonce"`
	Identity EngineIdentity `json:"identity"`
	Action   RuntimeAction  `json:"action"`
	Deadline int64          `json:"deadline_unix_nano"`
	MAC      string         `json:"mac"`
}
type runtimeActionResponse struct {
	Version  int              `json:"version"`
	Nonce    string           `json:"nonce"`
	Identity EngineIdentity   `json:"identity"`
	Action   RuntimeAction    `json:"action"`
	Result   contracts.Result `json:"result"`
	MAC      string           `json:"mac"`
}

// RequestRuntimeAction binds payload, nonce and exact live identity in both
// directions. It uses a separate schema so v1 control MAC bytes stay compatible.
func RequestRuntimeAction(ctx paths.Context, action RuntimeAction) (contracts.Result, error) {
	ctx, err := canonicalContext(ctx)
	if err != nil {
		return contracts.Result{}, err
	}
	record, err := loadRecord(ctx)
	if err != nil {
		return contracts.Result{}, err
	}
	if action.Command != "system-proxy-acquire" && action.Command != "system-proxy-release" {
		return contracts.Result{}, ErrControlIdentity
	}
	nonce, err := randomHex(32)
	if err != nil {
		return contracts.Result{}, err
	}
	request := runtimeActionRequest{Version: 1, Nonce: nonce, Identity: record.Identity, Action: action, Deadline: time.Now().Add(15 * time.Second).UnixNano()}
	request.MAC = sign(record.Secret, request)
	body, _ := json.Marshal(request)
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest(http.MethodPost, "http://"+record.Address+"/v1/runtime-action", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return contracts.Result{}, &RuntimeError{Code: "engine_action_result_unknown", Message: "控制动作结果未知；保留引擎和恢复记录，请查询租约后再处理"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return contracts.Result{}, ErrControlIdentity
	}
	var result runtimeActionResponse
	if json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&result) != nil {
		return contracts.Result{}, ErrControlIdentity
	}
	mac := result.MAC
	result.MAC = ""
	if result.Version != 1 || result.Nonce != nonce || result.Identity != record.Identity || result.Action != action || !validMAC(sign(record.Secret, result), mac) {
		return contracts.Result{}, ErrControlIdentity
	}
	if !result.Result.OK {
		if result.Result.Error == "" {
			return contracts.Result{}, ErrControlIdentity
		}
		return result.Result, &RuntimeError{Code: result.Result.ErrorCode, Message: result.Result.Error}
	}
	return result.Result, nil
}

func (c *engineControl) handleRuntimeAction(writer http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.NotFound(writer, req)
		return
	}
	var request runtimeActionRequest
	if json.NewDecoder(http.MaxBytesReader(writer, req.Body, 8192)).Decode(&request) != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	mac := request.MAC
	request.MAC = ""
	if request.Version != 1 || len(request.Nonce) != 64 || request.Identity != c.record.Identity || (request.Action.Command != "system-proxy-acquire" && request.Action.Command != "system-proxy-release") || !validMAC(sign(c.record.Secret, request), mac) {
		http.Error(writer, "unauthorized", http.StatusForbidden)
		return
	}
	c.mu.Lock()
	now := time.Now().UnixNano()
	for nonce, deadline := range c.actionNonces {
		if deadline <= now {
			delete(c.actionNonces, nonce)
		}
	}
	_, reused := c.actionNonces[request.Nonce]
	if request.Deadline <= now || request.Deadline > time.Now().Add(30*time.Second).UnixNano() || reused || len(c.actionNonces) >= 1024 || req.Context().Err() != nil {
		c.mu.Unlock()
		http.Error(writer, "expired or reused action", http.StatusForbidden)
		return
	}
	if c.actionNonces == nil {
		c.actionNonces = make(map[string]int64)
	}
	c.actionNonces[request.Nonce] = request.Deadline
	result := contracts.Result{ErrorCode: "engine_action_unsupported", Error: "此引擎不支持代理租约动作"}
	if c.hooks != nil {
		guard := c.guard()
		var err error
		result, err = c.hooks.Action(req.Context(), guard, request.Action)
		guard.finish()
		if err != nil {
			result.OK = false
			result.Error = err.Error()
			if result.ErrorCode == "" {
				result.ErrorCode = "engine_action_failed"
			}
		}
	}
	c.mu.Unlock()
	result.ContractVersion = contracts.ContractVersion
	response := runtimeActionResponse{Version: 1, Nonce: request.Nonce, Identity: c.record.Identity, Action: request.Action, Result: result}
	response.MAC = sign(c.record.Secret, response)
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(response)
}

// WithStoppedEngine holds the free lifetime lock for the whole callback. The
// caller also holds WithOperationLock, preventing launches during recovery.
// Stale records must first be reconciled by Inspect; unknown state is refused.
func WithStoppedEngine(ctx paths.Context, action func() error) error {
	ctx, err := canonicalContext(ctx)
	if err != nil {
		return err
	}
	lock, err := acquireRuntimeLock(ctx, "engine", 0)
	if err != nil {
		return ErrControlUnavailable
	}
	defer releaseRuntimeLock(lock)
	if _, err := loadRecord(ctx); !errors.Is(err, os.ErrNotExist) {
		return ErrControlIdentity
	}
	if _, err := readPrivateFile(ctx, ctx.PIDPath); !errors.Is(err, os.ErrNotExist) {
		return ErrLegacyState
	}
	if _, err := readPrivateFile(ctx, launchPath(ctx)); !errors.Is(err, os.ErrNotExist) {
		return &RuntimeError{Code: "engine_operation_pending", Message: "存在未处理的启动请求，拒绝恢复代理"}
	}
	return action()
}
