//go:build windows

package proxy

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestDedicatedWindowsProductDetachedLifecyclePersistsReadyStopped(t *testing.T) {
	home, cfg, _ := syntheticRuntime(t)
	if err := Start(home, cfg, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = Stop(home) })
	status := waitReady(t, home)
	if status.Identity.PID == os.Getpid() {
		t.Fatal("product launch did not create its detached child")
	}
	deadline := time.Now().Add(time.Second)
	var healthy bool
	for time.Now().Before(deadline) {
		health, err := InspectEngineLog(home, status.Identity)
		if err != nil {
			t.Fatal(err)
		}
		if health.State == "healthy" {
			healthy = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !healthy {
		t.Fatal("real detached child never published healthy dedicated logging")
	}
	if stopped, err := Stop(home); err != nil || !stopped {
		t.Fatal("real detached child did not complete owned stop")
	}
	var ready, stopped bool
	for _, record := range readEngineEvents(t, home) {
		if record.InstanceID == status.Identity.InstanceID && record.Generation == status.Identity.Generation {
			ready = ready || record.Event == EngineLogReady
			stopped = stopped || record.Event == EngineLogStopped
		}
	}
	if !ready || !stopped {
		t.Fatal("successful product stop returned before typed lifecycle persistence")
	}
	// Immediate restart must not collide with the old log namespace lock.
	if err := Start(home, cfg, false); err != nil {
		t.Fatal("successful Stop retained the previous logging owner")
	}
	if _, err := Stop(home); err != nil {
		t.Fatal(err)
	}
}

func TestDedicatedWindowsProductDetachedBindFailurePersistsFixedCode(t *testing.T) {
	home, cfg, _ := syntheticRuntime(t)
	occupied, err := net.Listen("tcp", cfg.LocalProxy.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if _, err := prepareLaunch(home, cfg); err != nil {
		t.Fatal(err)
	}
	if err := launchEngine(home, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		// The released child exposes no inherited stderr. Read only the
		// existing finite typed files once its actual owner exited.
		if _, err := os.Stat(statePath(home)); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Stat(home.LogDir + string(os.PathSeparator) + "engine-events-v1" + string(os.PathSeparator) + engineLogSlotName(0)); err == nil {
				if records, err := tryReadEngineEvents(home); err == nil {
					for _, record := range records {
						found = found || record.Event == EngineLogFailed && record.Code == EngineLogCodeStartFailed
					}
				}
				if found {
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatal("released actual child lost real sing-box bind failure")
	}
	if status, err := Inspect(home); err != nil || status.State != "stopped" {
		t.Fatal("failed detached child retained an engine lifetime")
	}
}
