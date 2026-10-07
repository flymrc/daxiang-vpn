package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"zongheng-vpn/clients/cli/internal/deviceclient"

	"zongheng-vpn/shared/config"
)

func TestDeviceCacheCannotRefreshThroughLegacyBootstrap(t *testing.T) {
	home := privateTestHome(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	t.Setenv("ZHVPN_API_BASE", server.URL)
	c := testClientConfig("owned-legacy-canary")
	c.Authorization.Source = config.DeviceV2Source
	if _, err := refreshInstalledConfig(home, c); err == nil {
		t.Fatal("v2 cache refreshed through token authority")
	}
	if calls.Load() != 0 {
		t.Fatal("legacy bootstrap was called")
	}
	if _, err := os.Stat(home.WireGuardKeyPath); !os.IsNotExist(err) {
		t.Fatal("legacy WG key was read or created")
	}
}

func TestDeviceStartStrictArgumentsAndCancelledLockEmitOneReceipt(t *testing.T) {
	home := privateTestHome(t)
	for _, args := range [][]string{{"--expected-generation", "0"}, {"--expected-generation", "1", "--fast"}, {"--expected-generation", "1", "--port", "0"}, {"--expected-generation", "1", "--json=false"}, {"--expected-generation", "1", "--timeout", "999h"}} {
		var out bytes.Buffer
		if err := deviceStart(context.Background(), home, args, &out); !errors.Is(err, ErrSilent) {
			t.Fatalf("invalid arguments not rejected: %v", err)
		}
		r, decodeErr := deviceclient.DecodeStartReceipt(out.Bytes())
		if decodeErr != nil || r.ContractVersion != 2 || r.Command != "start" || r.OK || r.Code != "invalid_arguments" || strings.Count(out.String(), "\n") != 1 {
			t.Fatal("early path returned inconsistent receipt")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := deviceStart(ctx, home, []string{"--expected-generation", "1"}, &out); !errors.Is(err, ErrSilent) {
		t.Fatal("cancelled start did not report")
	}
	var r deviceStartReceipt
	if json.Unmarshal(out.Bytes(), &r) != nil || r.OK || r.Code != "command_timeout" {
		t.Fatal("cancelled lock claimed engine start")
	}
	if _, err := os.Stat(home.SingBoxConfig); !os.IsNotExist(err) {
		t.Fatal("refused start left a WG engine config")
	}
}

func TestDeviceSetupErrorDoesNotReflectArbitraryArgs(t *testing.T) {
	var out bytes.Buffer
	if !errors.Is(reportDeviceSetupFailure(&out, []string{"owned-secret-canary"}), ErrSilent) {
		t.Fatal("setup error not reported")
	}
	if strings.Contains(out.String(), "owned-secret-canary") || !strings.Contains(out.String(), `"command":"unknown"`) || strings.Count(out.String(), "\n") != 1 {
		t.Fatal("setup failure leaked arguments")
	}
}
