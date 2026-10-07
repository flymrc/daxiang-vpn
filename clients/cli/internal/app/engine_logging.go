package app

import (
	"encoding/hex"
	"strings"

	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

// Provenance is a public source reference, never a free-form build message.
func engineBuildReference(commit, sourceState string) proxy.EngineBuildInfo {
	if sourceState != "clean" || len(commit) != 40 || strings.ToLower(commit) != commit {
		return proxy.EngineBuildInfo{}
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return proxy.EngineBuildInfo{}
	}
	return proxy.EngineBuildInfo{BuildID: commit}
}

// A separate observation must name the exact instance authenticated by Inspect.
// Older children, stopped instances and failed/contradictory observations remain
// unknown. Logging errors cannot change process or network readiness evidence.
func observeEngineLogging(home paths.Context, engine proxy.EngineStatus, inspect func(paths.Context, proxy.EngineIdentity) (proxy.EngineLogHealth, error)) proxy.EngineLogHealth {
	unknown := proxy.EngineLogHealth{State: "unknown"}
	if inspect == nil || (engine.State != "starting" && engine.State != "ready" && engine.State != "stopping") || len(engine.Identity.InstanceID) != 32 || len(engine.Identity.Generation) != 64 || engine.Identity.ProtocolVersion != proxy.ControlProtocolVersion || engine.Identity.PID <= 0 {
		return unknown
	}
	health, err := inspect(home, engine.Identity)
	if err != nil {
		return unknown
	}
	switch health.State {
	case "healthy", "unknown":
		if health.Code == "" {
			return health
		}
	case "degraded":
		if proxy.IsEngineLogHealthCode(health.Code) {
			return health
		}
	}
	return unknown
}
