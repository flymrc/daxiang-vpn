package deviceclient

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	dc "zongheng-vpn/shared/devicecontract"
)

func readyStartFixture() StartReceipt {
	return StartReceipt{ContractVersion: Version, Command: StartCommand, OK: true, Outcome: StartReady, EngineState: "ready", InstanceID: strings.Repeat("1", 32), ConfigGeneration: strings.Repeat("2", 64), Proxy: "127.0.0.1:17890", DeviceID: strings.Repeat("3", 32), Generation: 1}
}
func TestStartReceiptRejectsFalseReadyAndErrorIdentityOrSecretFields(t *testing.T) {
	ready := readyStartFixture()
	b, _ := json.Marshal(ready)
	if r, e := DecodeStartReceipt(b); e != nil || r != ready {
		t.Fatal("valid local readiness receipt rejected")
	}
	for _, code := range startErrorCodes() {
		for _, outcome := range []string{StartRejected, StartUnknown} {
			b, _ := json.Marshal(StartReceipt{ContractVersion: Version, Command: StartCommand, Outcome: outcome, Code: code})
			if _, e := DecodeStartReceipt(b); e != nil {
				t.Fatal("fixed error code rejected")
			}
		}
	}
	cases := map[string]func(map[string]any){
		"wrong-command": func(m map[string]any) { m["command"] = "apply" }, "missing-bool": func(m map[string]any) { delete(m, "ok") }, "null-bool": func(m map[string]any) { m["ok"] = nil }, "pending": func(m map[string]any) { m["pending"] = true }, "wrong-state": func(m map[string]any) { m["engine_state"] = "starting" }, "missing-instance": func(m map[string]any) { delete(m, "instance_id") }, "short-instance": func(m map[string]any) { m["instance_id"] = "1" }, "wrong-device": func(m map[string]any) { m["device_id"] = strings.Repeat("A", 32) }, "wrong-generation-digest": func(m map[string]any) { m["config_generation"] = strings.Repeat("A", 64) }, "wide-generation": func(m map[string]any) { m["generation"] = dc.ProxySafeInteger + 1 }, "zero-generation": func(m map[string]any) { m["generation"] = 0 }, "public-proxy": func(m map[string]any) { m["proxy"] = "192.0.2.1:7890" }, "zero-port": func(m map[string]any) { m["proxy"] = "127.0.0.1:0" }, "alias-port": func(m map[string]any) { m["proxy"] = "127.0.0.1:07890" }, "dns-proxy": func(m map[string]any) { m["proxy"] = "localhost:7890" }, "mapped-proxy": func(m map[string]any) { m["proxy"] = "[::ffff:127.0.0.1]:7890" }, "success-code": func(m map[string]any) { m["code"] = "" }, "secret": func(m map[string]any) { m["private_key"] = "SYNTHETIC_SECRET" }, "wrong-outcome": func(m map[string]any) { m["outcome"] = "accepted" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			change(m)
			raw, _ := json.Marshal(m)
			if _, e := DecodeStartReceipt(raw); e == nil {
				t.Fatal("invalid readiness receipt accepted")
			}
		})
	}
	for _, field := range startSuccessFields {
		t.Run("error-"+field, func(t *testing.T) {
			m := map[string]any{"contract_version": Version, "command": StartCommand, "ok": false, "pending": false, "outcome": StartUnknown, "code": "engine_start_timeout", field: ""}
			if field == "generation" {
				m[field] = 0
			}
			raw, _ := json.Marshal(m)
			if _, e := DecodeStartReceipt(raw); e == nil {
				t.Fatal("error carried success identity")
			}
		})
	}
	for _, raw := range []string{
		`{"contract_version":2,"command":"start","ok":false,"pending":false,"outcome":"rejected","code":"SYNTHETIC_SECRET"}`,
		`{"contract_version":2,"command":"start","ok":false,"pending":false,"outcome":"local_error","code":"local_storage_failure"}`,
		`{"contract_version":2,"command":"start","ok":false,"pending":false,"outcome":"rejected","code":"conflict","code":"conflict"}`,
		`{"contract_version":2,"command":"start","ok":false,"pending":false,"outcome":"rejected","Code":"conflict"}`,
	} {
		if _, e := DecodeStartReceipt([]byte(raw)); e == nil {
			t.Fatal("invalid startup error accepted")
		}
	}
	setup, _ := json.Marshal(Receipt{ContractVersion: Version, Command: StartCommand, Outcome: "local_error", Code: "local_storage_failure"})
	if _, e := DecodeReceipt(setup); e != nil {
		t.Fatal("generic startup setup error rejected")
	}
	if _, e := DecodeReceipt(b); e == nil {
		t.Fatal("startup readiness accepted as mutation receipt")
	}
	if strings.Contains(string(b), "SYNTHETIC_SECRET") {
		t.Fatal("fixture secret leaked")
	}
}

func TestStartSchemaMatchesSourceFieldsConstraintsAndMutationSchemaEnums(t *testing.T) {
	expected, e := StartReceiptSchema()
	if e != nil {
		t.Fatal(e)
	}
	actual, e := os.ReadFile("start-receipt.schema.json")
	if e != nil || !bytes.Equal(expected, actual) {
		t.Fatal("generated startup schema drift")
	}
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(actual, &s) != nil || len(s.Properties) != reflect.TypeOf(StartReceipt{}).NumField() {
		t.Fatal("schema/source field drift")
	}
	var proxyProp struct {
		Pattern string `json:"pattern"`
	}
	_ = json.Unmarshal(s.Properties["proxy"], &proxyProp)
	pattern, e := regexp.Compile(proxyProp.Pattern)
	if e != nil {
		t.Fatal("invalid proxy schema regex")
	}
	for _, value := range []string{"127.0.0.1:1", "127.255.255.255:65535", "[::1]:7890", "127.0.0.1:0", "127.0.0.1:65536", "127.000.0.1:7890", "127.0.0.1:07890", "127.0.0.1:7890/path", "192.0.2.1:7890", "[::ffff:127.0.0.1]:7890"} {
		if pattern.MatchString(value) != localProxyAddress(value) {
			t.Fatal("schema/runtime loopback boundary drift")
		}
	}
	b, e := os.ReadFile("receipt.schema.json")
	if e != nil || CheckReceiptSchema(b) != nil {
		t.Fatal("mutation schema drift")
	}
	for _, name := range []string{"command", "code", "contract_version", "device_id"} {
		var doc map[string]any
		_ = json.Unmarshal(b, &doc)
		if name == "device_id" {
			doc["$defs"].(map[string]any)["credential"].(map[string]any)["properties"].(map[string]any)[name] = nil
			delete(doc["$defs"].(map[string]any)["credential"].(map[string]any)["properties"].(map[string]any), name)
		} else {
			doc["properties"].(map[string]any)[name] = map[string]any{"enum": []string{"synthetic-drift"}}
		}
		bad, _ := json.Marshal(doc)
		if CheckReceiptSchema(bad) == nil {
			t.Fatal("mutation schema negative drift accepted")
		}
	}
}
