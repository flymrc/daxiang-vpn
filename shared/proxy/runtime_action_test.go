package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"zongheng-vpn/shared/contracts"
)

type actionHooks struct {
	entered  chan struct{}
	unblock  chan struct{}
	failStop atomic.Bool
	actions  atomic.Int32
	guard    *RuntimeGuard
}

func (h *actionHooks) Action(ctx context.Context, g *RuntimeGuard, a RuntimeAction) (contracts.Result, error) {
	h.guard = g
	err := g.WithReady(func(EngineStatus) error {
		h.actions.Add(1)
		if h.entered != nil {
			close(h.entered)
			<-h.unblock
		}
		return nil
	})
	return contracts.Result{OK: err == nil, SystemProxyState: "acquired", Owned: contracts.Bool(true), Noop: contracts.Bool(false)}, err
}
func (h *actionHooks) BeforeStop(ctx context.Context, g *RuntimeGuard) error {
	return g.WithRelease(func() error {
		if h.failStop.Load() {
			return errors.New("isolated restore failure")
		}
		return nil
	})
}
func actionControl(t *testing.T, h RuntimeHooks) *engineControl {
	t.Helper()
	ctx, cfg, content := syntheticRuntime(t)
	if _, err := prepareLaunch(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	c, err := beginEngineControlWithHooks(ctx, content, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	c.ready()
	if status, err := requestControl(c.record, "activate"); err != nil || status.State != "ready" {
		t.Fatalf("activate: %v", err)
	}
	return c
}
func actionBody(c *engineControl) runtimeActionRequest {
	return runtimeActionRequest{Version: 1, Nonce: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Identity: c.record.Identity, Action: RuntimeAction{Command: "system-proxy-acquire", UserScope: "isolated-user"}, Deadline: time.Now().Add(10 * time.Second).UnixNano()}
}
func postAction(c *engineControl, body runtimeActionRequest) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	recorder := httptest.NewRecorder()
	c.handle(recorder, httptest.NewRequest(http.MethodPost, "/v1/runtime-action", bytes.NewReader(data)))
	return recorder
}
func TestRuntimeActionRejectsReplayPayloadTamperingAndExpiredRequest(t *testing.T) {
	h := &actionHooks{}
	c := actionControl(t, h)
	body := actionBody(c)
	body.MAC = sign(c.record.Secret, body)
	first := postAction(c, body)
	if first.Code != http.StatusOK || h.actions.Load() != 1 {
		t.Fatalf("first action %d", first.Code)
	}
	if replay := postAction(c, body); replay.Code != http.StatusForbidden || h.actions.Load() != 1 {
		t.Fatal("replayed signed mutation accepted")
	}
	tampered := body
	tampered.Action.Command = "system-proxy-release"
	if postAction(c, tampered).Code != http.StatusForbidden {
		t.Fatal("tampered payload accepted")
	}
	expired := actionBody(c)
	expired.Nonce = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	expired.Deadline = time.Now().Add(-time.Second).UnixNano()
	expired.MAC = sign(c.record.Secret, expired)
	if postAction(c, expired).Code != http.StatusForbidden {
		t.Fatal("expired signed action accepted")
	}
	var reply runtimeActionResponse
	if json.Unmarshal(first.Body.Bytes(), &reply) != nil {
		t.Fatal("invalid response")
	}
	mac := reply.MAC
	reply.MAC = ""
	if !validMAC(sign(c.record.Secret, reply), mac) || reply.Action != body.Action || reply.Nonce != body.Nonce || reply.Identity != body.Identity {
		t.Fatal("response did not bind complete operation")
	}
	if err := h.guard.WithReady(func(EngineStatus) error { t.Fatal("escaped capability invoked"); return nil }); err == nil {
		t.Fatal("expired guard accepted")
	}
}
func TestRuntimeActionHoldsPhaseAndLifetimeUntilOSCallbackFinishes(t *testing.T) {
	h := &actionHooks{entered: make(chan struct{}), unblock: make(chan struct{})}
	c := actionControl(t, h)
	completed := make(chan error, 1)
	go func() {
		_, err := RequestRuntimeAction(c.ctx, RuntimeAction{Command: "system-proxy-acquire", UserScope: "isolated-user"})
		completed <- err
	}()
	<-h.entered
	if err := WithStoppedEngine(c.ctx, func() error { t.Error("entered stopped boundary while owning engine lived"); return nil }); err == nil {
		t.Fatal("active lifetime was treated as stopped")
	}
	stopDone := make(chan error, 1)
	go func() { _, err := requestControl(c.record, "stop"); stopDone <- err }()
	select {
	case <-stopDone:
		t.Fatal("stop raced active OS write")
	case <-time.After(50 * time.Millisecond):
	}
	close(h.unblock)
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done:
	default:
		t.Fatal("successful restore did not stop")
	}
}
func TestFailedStopHookPreservesReadyIdentityAndRetry(t *testing.T) {
	h := &actionHooks{}
	h.failStop.Store(true)
	c := actionControl(t, h)
	if _, err := requestControl(c.record, "stop"); err == nil {
		t.Fatal("failed restoration reported stop")
	}
	status, err := requestControl(c.record, "status")
	if err != nil || status.State != "ready" || status.Identity != c.record.Identity {
		t.Fatal("failed stop lost ready identity")
	}
	select {
	case <-c.done:
		t.Fatal("failed restoration cancelled engine")
	default:
	}
	h.failStop.Store(false)
	if _, err := requestControl(c.record, "stop"); err != nil {
		t.Fatal(err)
	}
}
