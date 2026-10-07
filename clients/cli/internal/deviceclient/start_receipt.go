package deviceclient

import (
	"encoding/hex"
	"encoding/json"
	"net"
	"net/netip"
	"strconv"

	dc "zongheng-vpn/shared/devicecontract"
)

const StartCommand = "start"
const StartReady = "engine_ready"
const StartRejected = "rejected"
const StartUnknown = "engine_state_unknown"

// StartReceipt records authenticated local engine readiness only. It proves
// neither a successful egress request nor a continuously renewed device lease.
// It intentionally differs from the mutation/recovery Receipt contract.
type StartReceipt struct {
	ContractVersion  int    `json:"contract_version"`
	Command          string `json:"command"`
	OK               bool   `json:"ok"`
	Outcome          string `json:"outcome"`
	Pending          bool   `json:"pending"`
	Code             string `json:"code,omitempty"`
	EngineState      string `json:"engine_state,omitempty"`
	InstanceID       string `json:"instance_id,omitempty"`
	ConfigGeneration string `json:"config_generation,omitempty"`
	Proxy            string `json:"proxy,omitempty"`
	DeviceID         string `json:"device_id,omitempty"`
	Generation       int64  `json:"generation,omitempty"`
}

var receiptErrorCodes = []string{
	"invalid_request", "unauthorized", "conflict", "not_found", "unavailable", "rate_limited", "invalid_server", "connection_failure", "invalid_response", "local_storage_failure", "invalid_arguments", "invalid_ca_file", "command_timeout", "server_mismatch", "pending_resolution_required", "already_activated", "invalid_activation_input", "not_activated", "result_unknown", "no_pending_intent", "stale_generation", "binding_required", "credential_expired", "projection_expired", "authority_epoch_mismatch", "profile_rollback",
}
var startEngineErrorCodes = []string{
	"engine_state_unverified", "engine_already_present", "local_port_occupied", "engine_config_refused", "engine_start_failed", "engine_start_cancelled", "engine_start_timeout", "engine_identity_unverified", "engine_control_unavailable",
}
var receiptCommands = []string{"activate", "status", "apply", "bind", "disable", "revoke", "rotate-credential", "recover", "cancel-pending", "start", "unknown"}
var startSuccessFields = []string{"engine_state", "instance_id", "config_generation", "proxy", "device_id", "generation"}

func startErrorCodes() []string {
	return append(append([]string(nil), receiptErrorCodes...), startEngineErrorCodes...)
}
func inStrings(value string, allowed []string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func DecodeStartReceipt(data []byte) (StartReceipt, error) {
	var r StartReceipt
	if strictDecode(data, &r, "contract_version", "command", "ok", "outcome", "pending") != nil || r.ContractVersion != Version || r.Command != StartCommand || r.Pending {
		return r, errJSON
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return r, errJSON
	}
	if r.OK {
		if _, exists := fields["code"]; exists {
			return r, errJSON
		}
		for _, field := range startSuccessFields {
			if _, exists := fields[field]; !exists {
				return r, errJSON
			}
		}
		if r.Outcome != StartReady || r.EngineState != "ready" || !hexID(r.InstanceID) || !hexID(r.DeviceID) || !hashID(r.ConfigGeneration) || !localProxyAddress(r.Proxy) || r.Generation < 1 || r.Generation > dc.ProxySafeInteger {
			return r, errJSON
		}
	} else {
		if r.Outcome != StartRejected && r.Outcome != StartUnknown {
			return r, errJSON
		}
		if !inStrings(r.Code, startErrorCodes()) {
			return r, errJSON
		}
		for _, field := range startSuccessFields {
			if _, exists := fields[field]; exists {
				return r, errJSON
			}
		}
	}
	return r, nil
}
func hashID(value string) bool {
	b, e := hex.DecodeString(value)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == value
}
func localProxyAddress(value string) bool {
	host, port, e := net.SplitHostPort(value)
	if e != nil {
		return false
	}
	ip, e := netip.ParseAddr(host)
	if e != nil || !ip.IsLoopback() || ip.Is4In6() || ip.String() != host {
		return false
	}
	n, e := strconv.Atoi(port)
	return e == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == port && net.JoinHostPort(host, port) == value
}
