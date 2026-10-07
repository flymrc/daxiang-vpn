// Package leasecontrol connects the proxy lease state machine to the actual
// authenticated engine controller. No Inspect snapshot grants OS authority.
package leasecontrol

import (
	"context"
	"errors"
	"net"
	"time"

	lease "zongheng-vpn/clients/cli/internal/runtime/systemproxy"
	"zongheng-vpn/shared/contracts"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
	osproxy "zongheng-vpn/shared/systemproxy"
)

type Controller struct {
	home     paths.Context
	platform func() (osproxy.Platform, error)
	claim    *lease.Claim
}

func New(home paths.Context) *Controller {
	return &Controller{home: home, platform: osproxy.NewPlatform}
}

func manager(platform osproxy.Platform, authority lease.Authority) *lease.Manager {
	return &lease.Manager{Resolver: platform.Resolver, Transactions: platform.Transactions, Factory: platform.Factory, Authority: authority}
}

func resultError(err error) contracts.Result {
	r := contracts.Result{Error: err.Error(), ErrorCode: "system_proxy_action_failed"}
	var e *lease.Error
	if errors.As(err, &e) {
		r.ErrorCode = e.Code
		r.JournalPath = e.JournalPath
	}
	var runtimeErr *proxy.RuntimeError
	if errors.As(err, &runtimeErr) {
		r.ErrorCode = runtimeErr.Code
	}
	return r
}

func (c *Controller) Action(ctx context.Context, guard *proxy.RuntimeGuard, action proxy.RuntimeAction) (contracts.Result, error) {
	platform, err := c.platform()
	if err != nil {
		return resultError(err), err
	}
	scope, err := platform.Resolver.Resolve(ctx, c.home.Root)
	if err != nil {
		return resultError(err), err
	}
	if action.UserScope != scope.UserScope {
		err := &proxy.RuntimeError{Code: "system_proxy_user_refused", Message: "发起用户与引擎进程用户不匹配，未修改任一用户的代理"}
		return resultError(err), err
	}
	claim := lease.Claim{UserScope: scope.UserScope, Home: c.home.Root, Engine: guard.Identity()}
	owner := lease.Owner{UserScope: scope.UserScope, Home: scope.HomeIdentity, Engine: claim.Engine}
	m := manager(platform, &guardAuthority{guard: guard, owner: owner})
	var receipt lease.Receipt
	switch action.Command {
	case "system-proxy-acquire":
		receipt, err = m.Acquire(ctx, claim)
		// The phase gate prevents stop from racing the attempt. Manager exposes
		// ownership before WAL creation, including partial/lost-response cases.
		// Read-only refusals must not give this engine somebody else's journal.
		if receipt.Owner != nil {
			c.claim = &claim
		}
	case "system-proxy-release":
		receipt, err = m.Release(ctx, claim, action.LeaseID)
		if err == nil {
			c.claim = nil
		}
	default:
		err = &proxy.RuntimeError{Code: "system_proxy_command_refused", Message: "未知租约动作"}
	}
	if err != nil {
		return resultError(err), err
	}
	state := "acquired"
	if action.Command == "system-proxy-release" {
		state = "released"
	}
	return contracts.Result{OK: true, SystemProxyState: state, LeaseID: receipt.LeaseID, Owned: contracts.Bool(receipt.Owned), Noop: contracts.Bool(receipt.Noop), JournalPath: scope.JournalPath}, nil
}

func (c *Controller) BeforeStop(ctx context.Context, guard *proxy.RuntimeGuard) error {
	if c.claim == nil {
		return nil
	}
	platform, err := c.platform()
	if err != nil {
		return err
	}
	scope, err := platform.Resolver.Resolve(ctx, c.home.Root)
	if err != nil {
		return err
	}
	owner := lease.Owner{UserScope: c.claim.UserScope, Home: scope.HomeIdentity, Engine: c.claim.Engine}
	m := manager(platform, &guardAuthority{guard: guard, owner: owner})
	_, err = m.ReleaseOwned(ctx, *c.claim)
	return err
}

type guardAuthority struct {
	guard *proxy.RuntimeGuard
	owner lease.Owner
}

func (a *guardAuthority) WithReady(ctx context.Context, owner lease.Owner, action func(lease.ReadyEngine) error) error {
	if owner != a.owner || owner.Engine != a.guard.Identity() {
		return proxy.ErrControlIdentity
	}
	return a.guard.WithReady(func(status proxy.EngineStatus) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dialer := net.Dialer{Timeout: 250 * time.Millisecond}
		conn, err := dialer.DialContext(ctx, "tcp", status.ProxyAddr)
		if err != nil {
			return &proxy.RuntimeError{Code: "system_proxy_ready_refused", Message: "当前认证引擎的代理不可达，未修改系统代理"}
		}
		_ = conn.Close()
		return action(lease.ReadyEngine{Owner: owner, State: status.State, ProxyAddr: status.ProxyAddr, ProxyReachable: true})
	})
}
func (a *guardAuthority) WithRelease(ctx context.Context, owner lease.Owner, action func() error) error {
	if owner != a.owner || owner.Engine != a.guard.Identity() {
		return proxy.ErrControlIdentity
	}
	return a.guard.WithRelease(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return action()
	})
}
func (a *guardAuthority) WithStopped(context.Context, lease.Owner, func() error) error {
	return proxy.ErrControlIdentity
}

// Inspect reads durable intent only; it does not contact or trust an engine.
func Inspect(ctx context.Context, home paths.Context) (lease.Observation, error) {
	platform, err := osproxy.NewPlatform()
	if err != nil {
		return lease.Observation{}, err
	}
	return manager(platform, nil).Inspect(ctx, home.Root)
}

// Recover's caller holds the home's operation transaction. The callback itself
// holds the free lifetime lock; another generation cannot start during restore.
func Recover(ctx context.Context, home paths.Context) (contracts.Result, error) {
	platform, err := osproxy.NewPlatform()
	if err != nil {
		return contracts.Result{}, err
	}
	return recoverWithPlatform(ctx, home, platform)
}
func recoverWithPlatform(ctx context.Context, home paths.Context, platform osproxy.Platform) (contracts.Result, error) {
	scope, err := platform.Resolver.Resolve(ctx, home.Root)
	if err != nil {
		return contracts.Result{}, err
	}
	m := manager(platform, &stoppedAuthority{home: home})
	observation, err := m.Inspect(ctx, home.Root)
	if err != nil {
		return contracts.Result{}, err
	}
	if observation.Owner == nil {
		return contracts.Result{OK: true, SystemProxyState: "absent", Owned: contracts.Bool(false), Noop: contracts.Bool(true), JournalPath: scope.JournalPath}, nil
	}
	if observation.State != "recorded" {
		return contracts.Result{}, &proxy.RuntimeError{Code: "system_proxy_owner_refused", Message: "租约属于其他 home，未恢复"}
	}
	claim := lease.Claim{UserScope: scope.UserScope, Home: home.Root, Engine: observation.Owner.Engine}
	status, err := proxy.Inspect(home)
	if err != nil {
		return contracts.Result{}, err
	}
	if status.State != "stopped" {
		return contracts.Result{}, &proxy.RuntimeError{Code: "engine_operation_pending", Message: "此 home 的引擎仍在运行，不能按崩溃恢复租约"}
	}
	receipt, err := m.Recover(ctx, claim)
	if err != nil {
		return contracts.Result{}, err
	}
	return contracts.Result{OK: true, SystemProxyState: "recovered", LeaseID: receipt.LeaseID, Owned: contracts.Bool(receipt.Owned), Noop: contracts.Bool(receipt.Noop), JournalPath: scope.JournalPath}, nil
}

type stoppedAuthority struct{ home paths.Context }

func (*stoppedAuthority) WithReady(context.Context, lease.Owner, func(lease.ReadyEngine) error) error {
	return proxy.ErrControlIdentity
}
func (*stoppedAuthority) WithRelease(context.Context, lease.Owner, func() error) error {
	return proxy.ErrControlIdentity
}
func (a *stoppedAuthority) WithStopped(ctx context.Context, owner lease.Owner, action func() error) error {
	return proxy.WithStoppedEngine(a.home, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return action()
	})
}

func UserScope(ctx context.Context, home paths.Context) (string, error) {
	platform, err := osproxy.NewPlatform()
	if err != nil {
		return "", err
	}
	scope, err := platform.Resolver.Resolve(ctx, home.Root)
	return scope.UserScope, err
}
