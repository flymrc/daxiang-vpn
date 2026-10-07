package deviceclient

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	dc "zongheng-vpn/shared/devicecontract"
)

func receiptProperties(typ reflect.Type) map[string]any {
	properties := map[string]any{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		kind := "string"
		switch f.Type.Kind() {
		case reflect.Bool:
			kind = "boolean"
		case reflect.Int, reflect.Int64:
			kind = "integer"
		case reflect.Pointer, reflect.Struct:
			kind = "object"
		}
		properties[name] = map[string]any{"type": kind}
	}
	return properties
}

// StartReceiptSchema is generated from the public receipt type and the exact
// decoder enums/constraints. This is not a schema for raw device state/config.
func StartReceiptSchema() ([]byte, error) {
	properties := receiptProperties(reflect.TypeOf(StartReceipt{}))
	properties["contract_version"] = map[string]any{"const": Version}
	properties["command"] = map[string]any{"const": StartCommand}
	properties["outcome"] = map[string]any{"enum": []string{StartReady, StartRejected, StartUnknown}}
	properties["code"] = map[string]any{"enum": startErrorCodes()}
	properties["engine_state"] = map[string]any{"const": "ready"}
	for _, name := range []string{"instance_id", "device_id"} {
		properties[name] = map[string]any{"type": "string", "pattern": "^[a-f0-9]{32}$"}
	}
	properties["config_generation"] = map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"}
	octet := `(?:[0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])`
	port := `(?:[1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])`
	properties["proxy"] = map[string]any{"type": "string", "pattern": `^(?:127\.` + octet + `\.` + octet + `\.` + octet + `|\[::1\]):` + port + `$`}
	properties["generation"] = map[string]any{"type": "integer", "minimum": 1, "maximum": dc.ProxySafeInteger}
	forbidden := []any{}
	for _, field := range startSuccessFields {
		forbidden = append(forbidden, map[string]any{"required": []string{field}})
	}
	schema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://zongheng.invalid/contracts/device-start-receipt-v2", "title": "Explicit device start local-engine receipt", "type": "object", "additionalProperties": false,
		"description": "Authenticated local engine readiness only; not proof of WG handshake, egress traffic, or continuously renewed authorization.",
		"required":    []string{"contract_version", "command", "ok", "outcome", "pending"}, "properties": properties,
		"allOf": []any{map[string]any{"if": map[string]any{"properties": map[string]any{"ok": map[string]any{"const": true}}}, "then": map[string]any{"properties": map[string]any{"pending": map[string]any{"const": false}, "outcome": map[string]any{"const": StartReady}}, "required": startSuccessFields, "not": map[string]any{"required": []string{"code"}}}, "else": map[string]any{"required": []string{"code"}, "properties": map[string]any{"pending": map[string]any{"const": false}, "outcome": map[string]any{"enum": []string{StartRejected, StartUnknown}}}, "not": map[string]any{"anyOf": forbidden}}}},
	}
	b, e := json.MarshalIndent(schema, "", "  ")
	if e != nil {
		return nil, e
	}
	return append(b, '\n'), nil
}

// CheckReceiptSchema keeps the existing handwritten mutation schema's public
// property sets/enums aligned without rewriting its conditional contract. The
// separately generated startup schema receives byte-for-byte drift checking.
func CheckReceiptSchema(data []byte) error {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if json.Unmarshal(data, &s) != nil {
		return errJSON
	}
	for _, item := range []struct {
		typ   reflect.Type
		props map[string]json.RawMessage
	}{{reflect.TypeOf(Receipt{}), s.Properties}, {reflect.TypeOf(dc.Credential{}), s.Defs["credential"].Properties}, {reflect.TypeOf(dc.Operation{}), s.Defs["operation"].Properties}} {
		expected := receiptProperties(item.typ)
		if len(expected) != len(item.props) {
			return errJSON
		}
		for name := range expected {
			if item.props[name] == nil {
				return errJSON
			}
		}
	}
	for _, item := range []struct {
		name   string
		values []string
	}{{"command", receiptCommands}, {"code", receiptErrorCodes}} {
		var prop struct {
			Enum []string `json:"enum"`
		}
		if json.Unmarshal(s.Properties[item.name], &prop) != nil || !sameStrings(prop.Enum, item.values) {
			return errJSON
		}
	}
	var version struct {
		Const int `json:"const"`
	}
	if json.Unmarshal(s.Properties["contract_version"], &version) != nil || version.Const != Version {
		return errJSON
	}
	return nil
}
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = append([]string(nil), a...)
	b = append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
