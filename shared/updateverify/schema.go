package updateverify

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Schema deterministically projects the Go metadata field source. Relational
// rules, canonical bytes, key policy, streamed hash and anti-replay are enforced
// by Verify in addition to this schema; plain JSON Schema validation is not proof.
func Schema() ([]byte, error) {
	properties := map[string]any{}
	for _, name := range fieldNames(reflect.TypeFor[Metadata]()) {
		var property map[string]any
		switch name {
		case "product":
			property = map[string]any{"type": "string", "enum": products}
		case "platform":
			property = map[string]any{"type": "string", "enum": platforms}
		case "architecture":
			property = map[string]any{"type": "string", "enum": architectures}
		case "channel":
			property = map[string]any{"type": "string", "enum": channels}
		case "version":
			property = map[string]any{"type": "string", "minLength": 5, "maxLength": 128, "pattern": `^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)){0,7}))?(\+([0-9A-Za-z-]+)(\.[0-9A-Za-z-]+){0,7})?$`, "description": "Strict SemVer: core components <= uint32 max; each prerelease/build identifier <= 64 bytes. Stable channel refuses prerelease. Build metadata has no precedence."}
		case "protocol":
			property = map[string]any{"type": "integer", "minimum": 1, "maximum": 1024}
		case "source_commit":
			property = map[string]any{"type": "string", "pattern": "^[a-f0-9]{40}$"}
		case "artifact_name":
			property = map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$"}
		case "artifact_size":
			property = map[string]any{"type": "integer", "minimum": 1, "maximum": MaxArtifactBytes}
		case "artifact_sha256":
			property = map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"}
		case "issued_at", "expires_at":
			property = map[string]any{"type": "integer", "minimum": 1, "maximum": MaxTimestamp}
		case "release_sequence", "security_version", "security_floor":
			property = map[string]any{"type": "integer", "minimum": 1, "maximum": MaxSafeInteger}
		default:
			return nil, fmt.Errorf("metadata field lacks schema projection")
		}
		properties[name] = property
	}
	definition := map[string]any{"type": "object", "additionalProperties": false, "required": fieldNames(reflect.TypeFor[Metadata]()), "properties": properties}
	document := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "urn:zhvpn:update-metadata:v1",
		"title":   "zhvpn signed single-artifact update envelope v1",
		"type":    "object", "additionalProperties": false,
		"required": fieldNames(reflect.TypeFor[envelope]()),
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "integer", "const": SchemaVersion},
			"key_id":         map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]{0,63}$"},
			"metadata":       map[string]any{"$ref": "#/$defs/metadata"},
			"signature":      map[string]any{"type": "string", "pattern": "^[a-f0-9]{128}$"},
		},
		"$defs":                         map[string]any{"metadata": definition},
		"description":                   "All keys are exact case, required and non-null. Duplicate keys and trailing data are refused. Metadata must equal CanonicalMetadata bytes in Go struct order. Ed25519 framing: domain zhvpn/update-metadata/v1 followed by NUL, uint32BE key_id byte length/key_id, uint32BE metadata byte length/metadata. Verify adds pinned key/scope/protocol/time/floor/replay checks and actual size/SHA256. Schema validation alone never grants staging.",
		"x-zongheng-max-envelope-bytes": MaxEnvelopeBytes,
		"x-zongheng-max-metadata-bytes": MaxMetadataBytes,
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	return append(raw, '\n'), err
}
