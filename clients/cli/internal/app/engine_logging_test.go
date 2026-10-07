package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"zongheng-vpn/shared/contracts"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

func TestLoggingHealthCategoriesMatchPublicAndStoredContracts(t *testing.T) {
	status := contracts.Schema()["$defs"].(map[string]any)["Status"].(map[string]any)
	codes := status["properties"].(map[string]any)["logging_error_code"].(map[string]any)["enum"].([]string)
	canonical := make(map[string]bool, len(codes))
	for _, code := range codes {
		if canonical[code] || !proxy.IsEngineLogHealthCode(proxy.EngineLogCode(code)) {
			t.Fatal("public log health category differs from the authenticated sink")
		}
		canonical[code] = true
	}
	var events map[string]any
	if err := json.Unmarshal(proxy.EngineLogSchema(), &events); err != nil {
		t.Fatal(err)
	}
	stored := events["x-health"].(map[string]any)["stickyCodes"].([]any)
	if len(stored) != len(canonical) {
		t.Fatal("public and stored log health contracts differ in category count")
	}
	for _, code := range stored {
		if !canonical[code.(string)] {
			t.Fatal("stored sink health category has no public consumer contract")
		}
	}
}

func TestEngineBuildReferenceNeverCopiesArbitraryBuildText(t *testing.T) {
	for _, value := range []string{"unknown", "synthetic-token\nPRIVATE KEY", strings.Repeat("A", 40), strings.Repeat("z", 40), strings.Repeat("a", 64)} {
		if engineBuildReference(value, "clean").BuildID != "" {
			t.Fatal("unapproved build text entered log metadata")
		}
	}
	commit := strings.Repeat("a", 40)
	if engineBuildReference(commit, "clean").BuildID != commit {
		t.Fatal("complete public source SHA not passed to child")
	}
	for _, state := range []string{"dirty", "unknown", ""} {
		if engineBuildReference(commit, state).BuildID != "" {
			t.Fatal("dirty or unlabelled build claimed clean provenance")
		}
	}
}

func TestLoggingObservationIsBoundToAuthenticatedInstanceAndIndependentOfReady(t *testing.T) {
	home := paths.FromRoot(t.TempDir())
	identity := proxy.EngineIdentity{InstanceID: strings.Repeat("a", 32), Generation: strings.Repeat("b", 64), ProtocolVersion: proxy.ControlProtocolVersion, PID: 17, Home: home.Root}
	for _, phase := range []string{"starting", "ready", "stopping"} {
		for _, health := range []proxy.EngineLogHealth{{State: "healthy"}, {State: "unknown"}, {State: "degraded", Code: "engine_log_write"}} {
			engine := proxy.EngineStatus{State: phase, Identity: identity}
			got := observeEngineLogging(home, engine, func(received paths.Context, expected proxy.EngineIdentity) (proxy.EngineLogHealth, error) {
				if received != home || expected != identity {
					t.Fatal("logger observation changed expected instance")
				}
				return health, nil
			})
			if got != health || engine.State != phase {
				t.Fatal("log health corrupted lifecycle evidence")
			}
		}
	}
	for _, phase := range []string{"stopped", "degraded", ""} {
		got := observeEngineLogging(home, proxy.EngineStatus{State: phase, Identity: identity}, func(paths.Context, proxy.EngineIdentity) (proxy.EngineLogHealth, error) {
			t.Fatal("unverified engine queried logger")
			return proxy.EngineLogHealth{}, nil
		})
		if got.State != "unknown" || got.Code != "" {
			t.Fatal("unverified engine invented log health")
		}
	}
}

func TestFailedOrMalformedLoggingObservationStaysUnknown(t *testing.T) {
	home := paths.FromRoot(t.TempDir())
	engine := proxy.EngineStatus{State: "ready", Identity: proxy.EngineIdentity{InstanceID: strings.Repeat("a", 32), Generation: strings.Repeat("b", 64), ProtocolVersion: proxy.ControlProtocolVersion, PID: 23, Home: home.Root}}
	for _, health := range []proxy.EngineLogHealth{
		{State: "healthy", Code: "engine_log_write"}, {State: "degraded"}, {State: "unknown", Code: "engine_log_write"},
		{State: "invented"}, {State: "degraded", Code: "synthetic-token\nPRIVATE KEY"},
		{State: "degraded", Code: "engine_log_synthetic_secret"},
		{State: "degraded", Code: proxy.EngineLogCode("engine_log_" + strings.Repeat("a", 49))},
	} {
		got := observeEngineLogging(home, engine, func(paths.Context, proxy.EngineIdentity) (proxy.EngineLogHealth, error) { return health, nil })
		if got.State != "unknown" || got.Code != "" {
			t.Fatal("malformed response created log health")
		}
	}
	got := observeEngineLogging(home, engine, func(paths.Context, proxy.EngineIdentity) (proxy.EngineLogHealth, error) {
		return proxy.EngineLogHealth{State: "healthy"}, errors.New("synthetic-secret-error")
	})
	if got.State != "unknown" || got.Code != "" {
		t.Fatal("failed response strengthened evidence")
	}
}
