package proxygate

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Schema projects the actual Go field source. Schema validation alone does not
// prove byte canonicality, namespace ownership, scope disjointness, receiver
// ownership, command sequence, closed acknowledgement, or lease convergence.
func Schema() ([]byte, error) {
	project := func(t reflect.Type) (map[string]any, error) {
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			name := t.Field(i).Tag.Get("json")
			required = append(required, name)
			var prop map[string]any
			switch name {
			case "version":
				prop = map[string]any{"type": "integer", "const": Version}
			case "epoch", "managed_by", "interface":
				max := 32
				if name == "interface" {
					max = 15
				}
				prop = map[string]any{"type": "string", "pattern": identifier.String(), "maxLength": max}
			case "policy_sha256", "profile_sha256", "binding":
				prop = map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"}
			case "listener":
				prop = map[string]any{"type": "string", "maxLength": 21, "description": "Canonical nonzero IPv4 literal AddrPort, nonzero port; no DNS or wildcard."}
			case "controller_uid":
				prop = map[string]any{"type": "integer", "minimum": 0, "maximum": uint64(4294967295)}
			case "managed_sources", "retained_sources":
				min := 0
				if name == "managed_sources" {
					min = 1
				}
				prop = map[string]any{"type": "array", "minItems": min, "maxItems": MaxSources, "uniqueItems": true, "items": map[string]any{"type": "string", "maxLength": 18}, "description": "Canonical masked IPv4 CIDRs /8..32. Every managed and retained prefix pair must be disjoint."}
			case "session", "nonce":
				prop = map[string]any{"type": "string", "pattern": "^[a-f0-9]{32}$"}
			case "sequence":
				prop = map[string]any{"type": "integer", "minimum": 0, "maximum": uint64(9007199254740991)}
			case "action":
				prop = map[string]any{"type": "string", "enum": []string{"hello", "closed", "grant", "ack"}}
			case "until_unix_nano":
				prop = map[string]any{"type": "integer", "minimum": 0, "maximum": int64(9223372036854775807), "description": "Absolute receiver-host lease deadline; hello/closed zero, grant strictly future and at most five seconds. ACK mirrors command."}
			default:
				return nil, fmt.Errorf("proxygate field lacks schema projection")
			}
			props[name] = prop
		}
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}, nil
	}
	policy, e := project(reflect.TypeFor[Policy]())
	if e != nil {
		return nil, e
	}
	frame, e := project(reflect.TypeFor[message]())
	if e != nil {
		return nil, e
	}
	doc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "urn:zhvpn:proxygate:v1", "title": "zhvpn immutable proxy source scope and local control v1", "$defs": map[string]any{"policy": policy, "control_frame": frame},
		"description":        "Linux protected private UDS and SO_PEERCRED. Exact Go compact struct-order JSON; policy has no LF, control frame has one LF. All fields required, nonnull and exact-case; duplicates and trailing bytes refused. A receiver-generated nonce and session, entire policy SHA256 binding, exact sequential commands and CLOSED ACK precede grants. Admission pre-reserves one existing HTTP client slot. Every upstream must Reserve before OpenStream, then AttachFenced the returned connection or Abort only when no connection was created; compatibility Attach retains the original connection contract. AttachFenced success and ErrClosed transfer the same resource, including late creation, to the reservation. Its single write and ordinary deadline permits linearize with invalidation under the gate decision mutex; network I/O executes outside that mutex. A pre-close permit is in flight and cannot recall kernel bytes; post-close writes and future/clear deadline updates are refused. A closing deadline update is repaired to a past deadline, and the independently bounded revoker deadline remains usable until physical Close starts. Physical Close rejects new external operations and waits for previously accepted past-deadline permits too. Fenced Close waits for the underlying Close and existing permits before releasing the registration; public Release/Untrack cannot erase a fenced registration before that completion. Pending and late resources remain in the same bounded quarantine until that completion or explicit Abort; Release cannot erase a pending creation. EOF, timeout, malformed messages and resource overflow invalidate only the immutable managed scope, cancel contexts and set I/O deadlines before bounded quarantined transport cleanup. A CLOSED ACK waits for registered Close calls, permits and pending creation completion; an unknown or blocked cleanup never receives an ACK or a new owner/grant. It does not attest immediate remote FIN receipt, zero in-flight kernel bytes, disappearance of yamux internal objects or global server resource capacity. A command or shutdown timeout does not prove transport resources reaped. The absolute lease deadline cannot be extended by transport delay. Scope-external sources retain previous admission behavior.",
		"x-max-policy-bytes": MaxPolicyBytes, "x-max-message-bytes": MaxMessageBytes, "x-max-admissions": MaxAdmissions, "x-max-tracked-connections": MaxTrackedConnections, "x-max-cleanup-workers": MaxCleanupWorkers, "x-max-cleanup-connections": MaxCleanupConnections, "x-command-timeout-ms": CommandTimeout.Milliseconds(), "x-closed-idle-timeout-ms": ClosedIdleTimeout.Milliseconds(), "x-max-lease-ms": MaxLease.Milliseconds(),
		"x-max-inflight-writes-per-fenced-connection": MaxInflightWrites, "x-max-inflight-deadlines-per-fenced-connection": MaxInflightDeadlines, "x-max-revoker-deadlines-per-fenced-connection": MaxRevokerDeadlines,
	}
	b, e := json.MarshalIndent(doc, "", "  ")
	return append(b, '\n'), e
}
