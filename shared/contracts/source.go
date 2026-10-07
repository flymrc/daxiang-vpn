// Package contracts owns the public CLI JSON contract. Change this source,
// then regenerate its Go DTOs, JSON Schema and Python validation data together.
package contracts

import "strings"

//go:generate go run ./cmd/contractgen -root ../..

const (
	ContractVersion        = 1
	ControlProtocolVersion = 1
	SchemaID               = "urn:zongheng-vpn:cli:v1"
)

type fieldSpec struct {
	GoName      string
	JSONName    string
	GoType      string
	OmitEmpty   bool
	Required    bool
	Description string
	Constraints map[string]any
}

type definitionSpec struct {
	Name        string
	Description string
	Fields      []fieldSpec
	Rules       []map[string]any
}

func field(goName, jsonName, goType, description string, omitEmpty, required bool, constraints map[string]any) fieldSpec {
	return fieldSpec{goName, jsonName, goType, omitEmpty, required, description, constraints}
}

func property(name string, constraints map[string]any) map[string]any {
	return map[string]any{"required": []string{name}, "properties": map[string]any{name: constraints}}
}

func equals(name string, value any) map[string]any {
	return property(name, map[string]any{"const": value})
}

func present(names ...string) map[string]any {
	return map[string]any{"required": names}
}

func when(condition, consequence map[string]any) map[string]any {
	return map[string]any{"if": condition, "then": consequence}
}

func anyPresent(names ...string) map[string]any {
	options := make([]any, 0, len(names))
	for _, name := range names {
		options = append(options, present(name))
	}
	return map[string]any{"anyOf": options}
}

func noFields(names ...string) map[string]any {
	return map[string]any{"not": anyPresent(names...)}
}

// These fields belong to private storage, never a public CLI response. Other
// unknown properties remain compatible additions; they confer no new evidence.
var privateFieldNames = []string{"control_secret", "private_key", "wireguard_private_key", "token", "authorization_token"}

func commonFields() []fieldSpec {
	return []fieldSpec{
		field("ContractVersion", "contract_version", "int", "Public JSON contract version; absence is a legacy response, not authenticated evidence.", true, false, map[string]any{"const": ContractVersion}),
	}
}

func definitions() []definitionSpec {
	activeIdentity := []string{"instance_id", "config_generation", "control_protocol_version"}
	activeState := property("engine_state", map[string]any{"enum": []string{"starting", "ready", "stopping"}})
	statusFields := []fieldSpec{
		field("Running", "running", "bool", "Authenticated local engine is ready. Does not establish tunnel or egress health. Legacy responses retain their historical meaning.", false, true, nil),
		field("Proxy", "proxy", "string", "Local proxy address, including configured address when stopped. Address alone is not ownership evidence.", true, false, nil),
		field("ProxyReachable", "proxy_reachable", "bool", "TCP connection succeeded to the authenticated ready instance's proxy. Does not establish HTTP forwarding or tunnel health.", false, true, nil),
		field("Egress", "egress", "string", "Configured egress display name; cached configuration is not live egress evidence.", true, false, nil),
		field("EgressIP", "egress_ip", "string", "IP observed by this command's optional proxy probe. Missing is unknown; no observation timestamp is supplied in v1.", true, false, nil),
		field("EgressIPv4", "egress_ipv4", "string", "Optional IPv4 probe observation. Missing is unknown, not an unhealthy IPv4 verdict.", true, false, map[string]any{"format": "ipv4"}),
		field("EgressIPv6", "egress_ipv6", "string", "Optional IPv6 probe observation. Missing is unknown, not an unhealthy IPv6 verdict.", true, false, map[string]any{"format": "ipv6"}),
		field("Error", "error", "string", "Human-readable diagnostic; no credentials may be included.", true, false, nil),
		field("ErrorCode", "error_code", "string", "Stable diagnostic category; new codes are compatible additions.", true, false, map[string]any{"pattern": "^[a-z][a-z0-9_]*$"}),
		field("EngineState", "engine_state", "string", "Local lifecycle phase only. Absence is a legacy response. Ready may coexist with proxy_reachable=false.", false, false, map[string]any{"enum": []string{"stopped", "starting", "ready", "stopping", "degraded"}}),
		field("InstanceID", "instance_id", "string", "Authenticated random instance identity; never a credential or PID-based authority.", true, false, map[string]any{"pattern": "^[0-9a-f]{32}$"}),
		field("ConfigGeneration", "config_generation", "string", "SHA-256 generation of engine configuration; no private configuration contents.", true, false, map[string]any{"pattern": "^[0-9a-f]{64}$"}),
		field("ControlProtocolVersion", "control_protocol_version", "int", "Local authentication protocol version, separate from contract_version.", true, false, map[string]any{"const": ControlProtocolVersion}),
		field("PortOccupied", "port_occupied", "bool", "A configured TCP port responds while the authenticated engine is not ready. Does not identify its owner.", true, false, nil),
		field("LoggingState", "logging_state", "string", "Optional independently authenticated health of this exact live instance's typed log sink. Missing/unknown supplies no evidence; healthy does not establish process, tunnel or egress health.", true, false, map[string]any{"enum": []string{"healthy", "degraded", "unknown"}}),
		field("LoggingErrorCode", "logging_error_code", "string", "Sticky fixed log-sink failure category, separate from lifecycle errors. No raw error or dependency message.", true, false, map[string]any{"enum": []string{"engine_log_open", "engine_log_write", "engine_log_rotate", "engine_log_sync", "engine_log_namespace", "engine_log_codec", "engine_log_closed", "engine_log_queue_overflow", "engine_log_shutdown"}}),
	}
	statusFields = append(statusFields, commonFields()...)
	statusRules := []map[string]any{
		when(equals("proxy_reachable", true), equals("running", true)),
		when(equals("engine_state", "ready"), equals("running", true)),
		when(map[string]any{"allOf": []any{equals("running", true), present("engine_state")}}, equals("engine_state", "ready")),
		when(property("engine_state", map[string]any{"enum": []string{"stopped", "starting", "stopping", "degraded"}}), map[string]any{"properties": map[string]any{"running": map[string]any{"const": false}, "proxy_reachable": map[string]any{"const": false}}}),
		when(activeState, present(activeIdentity...)),
		when(anyPresent(activeIdentity...), map[string]any{"allOf": []any{present(activeIdentity...), activeState}}),
		when(equals("port_occupied", true), equals("running", false)),
		when(anyPresent("egress_ip", "egress_ipv4", "egress_ipv6"), map[string]any{"allOf": []any{equals("running", true), equals("proxy_reachable", true)}}),
		when(present("error_code"), property("error", map[string]any{"minLength": 1})),
		when(property("error", map[string]any{"minLength": 1}), map[string]any{"properties": map[string]any{"running": map[string]any{"const": false}, "proxy_reachable": map[string]any{"const": false}}}),
		when(equals("logging_state", "degraded"), present("logging_error_code")),
		when(present("logging_error_code"), equals("logging_state", "degraded")),
		when(property("logging_state", map[string]any{"enum": []string{"healthy", "degraded"}}), map[string]any{"allOf": []any{present(activeIdentity...), activeState}}),
		noFields(privateFieldNames...),
	}
	resultFields := []fieldSpec{
		field("OK", "ok", "bool", "Outcome of the requested command; it does not imply tunnel or egress health.", false, true, nil),
		field("Status", "status", "string", "Existing command-specific result text, including rotate trigger outcome; not a durable operation protocol.", true, false, nil),
		field("Egress", "egress", "string", "Configured egress display name.", true, false, nil),
		field("Proxy", "proxy", "string", "Configured local proxy address.", true, false, nil),
		field("Before", "before", "string", "Existing rotate probe observation before a trigger.", true, false, nil),
		field("After", "after", "string", "Existing rotate probe observation after a trigger.", true, false, nil),
		field("Message", "message", "string", "Human-readable outcome.", true, false, nil),
		field("Product", "product", "string", "Binary product identifier.", true, false, nil),
		field("Version", "version", "string", "Binary build version, separate from the JSON contract version.", true, false, nil),
		field("ProtocolVersion", "protocol_version", "int", "Existing binary/sidecar identity protocol; separate from local control and public JSON contract versions.", true, false, map[string]any{"minimum": 1}),
		field("SourceCommit", "source_commit", "string", "Explicit repository commit, independent of embedded parent repository VCS metadata. Unknown for unlabelled developer builds.", true, false, map[string]any{"pattern": "^([0-9a-f]{40}|unknown)$"}),
		field("SourceState", "source_state", "string", "Clean/dirty source state at build time; clean does not imply a signed release.", true, false, map[string]any{"enum": []string{"clean", "dirty", "unknown"}}),
		field("GoVersion", "go_version", "string", "Actual Go toolchain embedded in this binary.", true, false, nil),
		field("Error", "error", "string", "Human-readable diagnostic; no credentials may be included.", true, false, nil),
		field("ErrorCode", "error_code", "string", "Stable diagnostic category; new codes are compatible additions.", true, false, map[string]any{"pattern": "^[a-z][a-z0-9_]*$"}),
	}
	resultFields = append(resultFields, commonFields()...)
	resultFields = append(resultFields,
		field("SystemProxyState", "system_proxy_state", "string", "Proxy lease outcome. Recorded is durable intent only, not live OS or engine health.", true, false, map[string]any{"enum": []string{"absent", "recorded", "foreign", "acquired", "released", "recovered"}}),
		field("LeaseID", "lease_id", "string", "Opaque non-secret lease identity; explicit release must name this lease.", true, false, map[string]any{"pattern": "^[0-9a-f]{32}$"}),
		field("Owned", "owned", "*bool", "This command operated on its exact instance's lease; absent is unknown.", true, false, nil),
		field("Noop", "noop", "*bool", "Acquire reused a lease, or release/recover found none. Reused intent may still require OS repair; not evidence of OS proxy health.", true, false, nil),
		field("JournalPath", "journal_path", "string", "Private recovery file location, without its contents.", true, false, nil),
	)
	return []definitionSpec{
		{Name: "Status", Description: "status --json. Compatible extra fields carry no additional evidence. Missing legacy lifecycle fields and all absent observations are unknown.", Fields: statusFields, Rules: statusRules},
		{Name: "Result", Description: "Existing login/start/stop/logout/rotate-ip/version JSON result. This contract does not introduce operation IDs, retries or terminal-operation guarantees.", Fields: resultFields, Rules: []map[string]any{
			when(equals("ok", true), map[string]any{"not": property("error", map[string]any{"minLength": 1})}),
			when(present("error_code"), property("error", map[string]any{"minLength": 1})),
			when(present("system_proxy_state"), present("owned", "noop")),
			when(equals("system_proxy_state", "acquired"), map[string]any{"allOf": []any{present("lease_id"), equals("owned", true)}}),
			noFields(privateFieldNames...),
		}},
		{Name: "EngineIdentity", Description: "Public instance identity shared by the local authenticated control protocol. PID is diagnostic only; identity contains no control secret.", Fields: []fieldSpec{
			field("InstanceID", "instance_id", "string", "Random instance identifier.", false, true, map[string]any{"pattern": "^[0-9a-f]{32}$"}),
			field("Home", "home", "string", "Canonical client home identity; Windows identity comparison uses case folding.", false, true, map[string]any{"minLength": 1}),
			field("Generation", "config_generation", "string", "Configuration SHA-256 generation.", false, true, map[string]any{"pattern": "^[0-9a-f]{64}$"}),
			field("ProtocolVersion", "control_protocol_version", "int", "Local control protocol version.", false, true, map[string]any{"const": ControlProtocolVersion}),
			field("PID", "pid", "int", "Diagnostic process integer; never an authorization to signal a process.", false, true, map[string]any{"minimum": 1}),
		}, Rules: []map[string]any{noFields(privateFieldNames...)}},
	}
}

func evidenceSemantics() map[string]any {
	return map[string]any{
		"engine":  "Only engine_state=ready plus complete v1 authenticated identity gives local process readiness evidence. Legacy running alone gives no authenticated identity evidence.",
		"proxy":   "proxy_reachable is an instantaneous TCP observation of a ready local instance, not HTTP forwarding health. Ready plus false is valid partial health.",
		"tunnel":  "No tunnel health observation is emitted in v1; always unknown.",
		"egress":  "egress is a cached configured label. Optional IPs are probe observations, not a claim that the selected residential exit or a WireGuard tunnel was authenticated.",
		"time":    "v1 emits no verified_at/observed_at timestamp. Observation freshness is unknown; SDKs must not synthesize verification time from decode time or reuse previous IPs as current evidence.",
		"missing": "Absent IP, lifecycle identity, family or verification time means unknown. Compatible unknown fields must not strengthen evidence without an explicitly supported contract.",
		"logging": "logging_state is a separate authenticated live-instance sink observation. Healthy does not imply complete history, raw dependency messages, crash durability or network health. Missing and unknown are unverified.",
	}
}

func schemaDefinitions() map[string]any {
	result := make(map[string]any)
	for _, definition := range definitions() {
		properties := make(map[string]any)
		required := []string{}
		for _, field := range definition.Fields {
			kind := map[string]string{"string": "string", "bool": "boolean", "int": "integer"}[strings.TrimPrefix(field.GoType, "*")]
			property := map[string]any{"type": kind, "description": field.Description}
			for key, value := range field.Constraints {
				property[key] = value
			}
			properties[field.JSONName] = property
			if field.Required {
				required = append(required, field.JSONName)
			}
		}
		result[definition.Name] = map[string]any{"type": "object", "description": definition.Description, "properties": properties, "required": required, "additionalProperties": true, "allOf": definition.Rules}
	}
	return result
}

// Bool preserves an explicit false for optional result evidence.
func Bool(value bool) *bool { return &value }

// Schema returns the current public schema as detached JSON-compatible data.
// The generated on-disk artifact and SDK validation data come from this source.
func Schema() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  SchemaID,
		"title":                "Zongheng VPN CLI JSON v1",
		"$defs":                schemaDefinitions(),
		"anyOf":                []any{map[string]any{"$ref": "#/$defs/Status"}, map[string]any{"$ref": "#/$defs/Result"}, map[string]any{"$ref": "#/$defs/EngineIdentity"}},
		"x-evidence-semantics": evidenceSemantics(),
	}
}
