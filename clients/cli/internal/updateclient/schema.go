package updateclient

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"zongheng-vpn/shared/updateverify"
)

// ReceiptSchema projects the public Go field source. DecodeReceipt additionally
// enforces command/result correlation, digest and relational floor invariants.
func ReceiptSchema() ([]byte, error) {
	return schema(reflect.TypeFor[Receipt](), "urn:zhvpn:update-receipt:v1")
}
func PolicySchema() ([]byte, error) {
	return schema(reflect.TypeFor[PolicyDocument](), "urn:zhvpn:update-approved-policy:v1")
}

func schema(t reflect.Type, id string) ([]byte, error) {
	value, e := schemaType(t)
	if e != nil {
		return nil, e
	}
	value["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	value["$id"] = id
	value["description"] = "Exact non-null UTF-8 keys; duplicate keys and trailing input refused. Go validators enforce semantic correlations, bounded SemVer/key authorization, digest/floors and provenance. This schema never authorizes installation or establishes initial policy trust."
	raw, e := json.MarshalIndent(value, "", "  ")
	return append(raw, '\n'), e
}

func schemaType(t reflect.Type) (map[string]any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "" || name == "-" {
				return nil, fmt.Errorf("field missing schema source")
			}
			p, e := schemaType(f.Type)
			if e != nil {
				return nil, e
			}
			constrain(p, t, name)
			properties[name] = p
			if !strings.Contains(tag, ",omitempty") {
				required = append(required, name)
			}
		}
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}, nil
	case reflect.Slice:
		item, e := schemaType(t.Elem())
		return map[string]any{"type": "array", "minItems": 1, "maxItems": 16, "items": item}, e
	case reflect.String:
		return map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer", "minimum": 1, "maximum": updateverify.MaxTimestamp}, nil
	case reflect.Uint64:
		return map[string]any{"type": "integer", "minimum": 1, "maximum": updateverify.MaxSafeInteger}, nil
	}
	return nil, fmt.Errorf("unsupported schema field")
}

func constrain(p map[string]any, t reflect.Type, name string) {
	switch name {
	case "schema_version", "contract_version":
		p["const"] = Version
	case "registration_id":
		p["pattern"] = "^[a-f0-9]{32}$"
		p["maxLength"] = 32
	case "policy_sha256", "metadata_sha256", "artifact_sha256":
		p["pattern"] = "^[a-f0-9]{64}$"
		p["maxLength"] = 64
	case "source_commit":
		p["pattern"] = "^[a-f0-9]{40}$"
		p["maxLength"] = 40
	case "public_key":
		p["pattern"] = "^[A-Za-z0-9+/]{43}=$"
		p["maxLength"] = 44
	case "key_id":
		p["pattern"] = "^[a-z][a-z0-9-]{0,63}$"
		p["maxLength"] = 64
	case "product", "platform", "architecture", "channel":
		p["enum"] = updateverify.ScopeEnums()[name]
	case "protocol", "min_protocol", "max_protocol":
		p["maximum"] = 1024
	case "artifact_size", "max_artifact_size":
		p["maximum"] = updateverify.MaxArtifactBytes
	case "max_validity_seconds":
		p["maximum"] = int64(updateverify.MaxValidity.Seconds())
	case "status":
		p["const"] = updateverify.StagingStatus
	case "command":
		p["enum"] = []string{"enroll", "policy-approve", "verify", "inspect", "unknown"}
	case "outcome":
		p["enum"] = []string{"enrolled", "policy_approved", "watermark_committed", "inspected", "rejected", "result_unknown"}
	case "code":
		p["enum"] = receiptCodes
	}
	if t == reflect.TypeFor[updateverify.Metadata]() && name == "artifact_name" {
		p["pattern"] = "^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$"
	}
}
