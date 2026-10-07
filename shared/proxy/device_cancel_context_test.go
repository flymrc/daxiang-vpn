//go:build windows || darwin

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// This relay forwards authenticated messages to the real owned native child.
// It cancels the caller only after the child produced its actual ready reply.
// Neither the request nor the response MAC/secret is synthesized by the relay.
func TestCancelledReadyResponseWithdrawsActualChild(t *testing.T) {
	// The watcher can lose an initial publication race to a normal ready
	// response. Such a successful, uncancelled fixture is stopped and retried;
	// only a response actually intercepted after child readiness proves this
	// cancellation boundary. Retries never relabel a regression as fixture noise.
	for attempt := 0; attempt < 3; attempt++ {
		if cancelledReadyResponseAttempt(t) {
			return
		}
		if t.Failed() {
			return
		}
	}
	t.Fatal("actual child ready reply did not traverse cancellation fixture")
}

func cancelledReadyResponseAttempt(t *testing.T) bool {
	home, cfg, _ := syntheticRuntime(t)
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer deadlineCancel()
	operation, cancel := context.WithCancel(deadlineCtx)
	defer cancel()
	var mu sync.RWMutex
	var original controlRecord
	intercepted := make(chan struct{}, 1)
	forward := &http.Client{Timeout: 16 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	defer forward.CloseIdleConnections()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
		if err != nil || len(body) > 8192 {
			http.Error(w, "fixture refused", 400)
			return
		}
		mu.RLock()
		actual := original
		mu.RUnlock()
		request, _ := http.NewRequest(http.MethodPost, "http://"+actual.Address+"/v1/control", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response, err := forward.Do(request)
		if err != nil {
			http.Error(w, "fixture unavailable", 503)
			return
		}
		defer response.Body.Close()
		answer, err := io.ReadAll(io.LimitReader(response.Body, 8193))
		if err != nil || len(answer) > 8192 {
			http.Error(w, "fixture refused", 503)
			return
		}
		var result controlResponse
		if response.StatusCode == http.StatusOK && json.Unmarshal(answer, &result) == nil && result.Command == "activate" && result.Status.State == "ready" {
			cancel()
			select {
			case intercepted <- struct{}{}:
			default:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(answer)
	}))
	defer relay.Close()
	monitorDone := make(chan error, 1)
	monitorStop := make(chan struct{})
	go func() {
		deadline := time.Now().Add(7 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-monitorStop:
				monitorDone <- errors.New("fixture stopped before interception")
				return
			default:
			}
			live, err := loadRecord(home)
			if err == nil {
				mu.Lock()
				original = live
				mu.Unlock()
				live.Address = strings.TrimPrefix(relay.URL, "http://")
				encoded, _ := json.Marshal(live)
				monitorDone <- writePrivateFile(home, statePath(home), encoded)
				return
			}
			time.Sleep(time.Millisecond)
		}
		monitorDone <- errors.New("fixture publication not observed")
	}()
	defer func() {
		mu.RLock()
		actual := original
		mu.RUnlock()
		if actual.Address != "" {
			_, _ = requestControl(actual, "stop")
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				status, err := Inspect(home)
				if err == nil && status.State == "stopped" {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Error("fixture cleanup could not confirm exact child stopped")
		}
	}()
	err := WithOperationLockContext(operation, home, func() error { return StartContext(operation, home, cfg, false) })
	close(monitorStop)
	if monitorErr := <-monitorDone; monitorErr != nil {
		t.Fatal("actual child relay fixture not established")
	}
	select {
	case <-intercepted:
	default:
		if err != nil || operation.Err() != nil {
			t.Fatal("fixture missed interception and start did not complete normally")
		}
		return false
	}
	if operation.Err() != context.Canceled {
		t.Fatal("fixture cancellation not observed")
	}
	var runtimeError *RuntimeError
	if !errors.As(err, &runtimeError) || runtimeError.Code != "engine_start_cancelled" {
		t.Fatal("cancelled native ready response returned success or wrong fixed failure")
	}
	if _, err := os.Stat(launchPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled launch remained published")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, inspectErr := Inspect(home)
		if inspectErr == nil && status.State == "stopped" {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("cancelled actual child retained active instance after bounded cleanup")
	return false
}
