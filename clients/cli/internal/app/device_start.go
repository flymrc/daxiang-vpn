package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"zongheng-vpn/clients/cli/internal/deviceclient"
	"zongheng-vpn/clients/cli/internal/netcheck"
	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

// Device start deliberately uses contract 2 rather than a legacy login result.
// EngineReady reports the authenticated local engine's startup, not successful
// egress traffic or a continuously renewed authority/session lease.
type deviceStartReceipt = deviceclient.StartReceipt

func emitDeviceStart(out io.Writer, r deviceStartReceipt) error {
	if err := json.NewEncoder(out).Encode(r); err != nil {
		return ErrSilent
	}
	if !r.OK {
		return ErrSilent
	}
	return nil
}

func reportDeviceStartFailure(out io.Writer, code string) error {
	return emitDeviceStart(out, deviceStartReceipt{ContractVersion: 2, Command: "start", Outcome: "rejected", Code: code})
}

func reportDeviceSetupFailure(out io.Writer, args []string) error {
	command := "unknown"
	if len(args) > 0 {
		switch args[0] {
		case "activate", "bind", "start", "apply", "disable", "revoke", "status", "rotate-credential", "recover", "cancel-pending":
			command = args[0]
		}
	}
	if json.NewEncoder(out).Encode(deviceclient.Receipt{ContractVersion: 2, Command: command, Outcome: "local_error", Code: "local_storage_failure"}) != nil {
		return ErrSilent
	}
	return ErrSilent
}

func deviceStart(operation context.Context, home paths.Context, args []string, out io.Writer) error {
	timeout, err := deviceclient.StartTimeout(args)
	if err != nil {
		return reportDeviceStartFailure(out, deviceclient.FailureCode(err))
	}
	operation, cancel := context.WithTimeout(operation, timeout)
	defer cancel()
	r := deviceStartReceipt{ContractVersion: 2, Command: "start", Outcome: "rejected", Code: "local_storage_failure"}
	err = proxy.WithOperationLockContext(operation, home, func() error {
		// This entrypoint never takes over, stops or relabels an existing engine.
		// A caller must explicitly stop it using the authenticated stop command.
		live, inspectErr := proxy.Inspect(home)
		if inspectErr != nil {
			r.Code = "engine_state_unverified"
			return nil
		}
		if live.State != "stopped" {
			r.Code = "engine_already_present"
			return nil
		}
		cfg, prepareErr := deviceclient.PrepareStartLocked(operation, home, args)
		if prepareErr != nil {
			r.Code = deviceclient.FailureCode(prepareErr)
			return nil
		}
		if cfg.Authorization.Source != config.DeviceV2Source || cfg.ValidateForProxyStart(time.Now()) != nil {
			r.Code = "invalid_response"
			return nil
		}
		if occupied, _ := netcheck.TCP(cfg.LocalProxy.Addr(), netcheck.ShortTimeout); occupied {
			r.Code = "local_port_occupied"
			return nil
		}
		if operation.Err() != nil {
			r.Code = "command_timeout"
			return nil
		}
		if saveErr := saveClientConfigCache(home, cfg); saveErr != nil {
			return saveErr
		}
		if writeErr := proxy.WriteSingBoxConfig(home, cfg, false); writeErr != nil {
			r.Code = "engine_config_refused"
			return nil
		}
		defer os.Remove(home.SingBoxConfig)
		if startErr := proxy.StartContext(operation, home, cfg, false); startErr != nil {
			r.Code = "engine_start_failed"
			var runtimeErr *proxy.RuntimeError
			if errors.As(startErr, &runtimeErr) {
				switch runtimeErr.Code {
				case "engine_start_cancelled", "engine_start_timeout", "engine_identity_unverified", "engine_control_unavailable":
					r.Code = runtimeErr.Code
				}
			}
			r.Outcome = "engine_state_unknown"
			return nil
		}
		live, inspectErr = proxy.Inspect(home)
		if operation.Err() != nil {
			// The home transaction still excludes other launchers. Only this
			// authenticated current instance can have been created by our Start.
			_, _ = proxy.Stop(home)
			r.Code = "command_timeout"
			r.Outcome = "engine_state_unknown"
			return nil
		}
		if inspectErr != nil || live.State != "ready" || live.ProxyAddr != cfg.LocalProxy.Addr() {
			r.Code = "engine_state_unverified"
			r.Outcome = "engine_state_unknown"
			return nil
		}
		r = deviceStartReceipt{ContractVersion: 2, Command: "start", OK: true, Outcome: "engine_ready", EngineState: live.State, InstanceID: live.Identity.InstanceID, ConfigGeneration: live.Identity.Generation, Proxy: live.ProxyAddr, DeviceID: cfg.Authorization.DeviceID, Generation: cfg.Authorization.DesiredGeneration}
		return nil
	})
	if err != nil && operation.Err() != nil {
		r.Code = "command_timeout"
	}
	return emitDeviceStart(out, r)
}
