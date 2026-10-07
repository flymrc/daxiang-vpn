package contracts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGeneratedArtifactsHaveNoDrift(t *testing.T) {
	if err := Generate(filepath.Join("..", ".."), true); err != nil {
		t.Fatal(err)
	}
	first, err := GeneratedFiles()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GeneratedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("contract generator is nondeterministic")
	}
}

func TestCheckRejectsEditedProjection(t *testing.T) {
	root := t.TempDir()
	if err := Generate(root, false); err != nil {
		t.Fatal(err)
	}
	if err := Generate(root, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "shared", "contracts", "cli_v1_generated.ts")
	if err := os.WriteFile(path, []byte("// hand edited\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Generate(root, true); err == nil || !strings.Contains(err.Error(), "cli_v1_generated.ts") {
		t.Fatalf("drift check silently accepted an edited consumer type: %v", err)
	}
}

func TestDTOFieldsMatchCanonicalSourceWithoutSecrets(t *testing.T) {
	types := map[string]reflect.Type{
		"Status": reflect.TypeOf(Status{}), "Result": reflect.TypeOf(Result{}),
		"EngineIdentity": reflect.TypeOf(EngineIdentity{}),
	}
	for _, definition := range definitions() {
		dto := types[definition.Name]
		if dto.NumField() != len(definition.Fields) {
			t.Fatalf("%s differs from canonical source", definition.Name)
		}
		for i, field := range definition.Fields {
			actual := dto.Field(i)
			tag := field.JSONName
			if field.OmitEmpty {
				tag += ",omitempty"
			}
			if actual.Name != field.GoName || actual.Tag.Get("json") != tag || strings.TrimPrefix(actual.Type.String(), "contracts.") != field.GoType {
				t.Fatalf("%s.%s no longer matches canonical source", definition.Name, field.GoName)
			}
			for _, private := range privateFieldNames {
				if field.JSONName == private {
					t.Fatalf("%s exposes a private storage field", definition.Name)
				}
			}
		}
	}
	encoded, _ := json.Marshal(Status{EngineState: "stopped"})
	if !bytes.Contains(encoded, []byte(`"running":false`)) || !bytes.Contains(encoded, []byte(`"proxy_reachable":false`)) {
		t.Fatal("required legacy status booleans changed serialization")
	}
	if bytes.Contains(encoded, []byte(`contract_version`)) {
		t.Fatal("optional version must preserve unversioned compatibility until emitter sets it")
	}
}

func TestSchemaExposesOnlyPublicFieldsAndNoFutureHealthClaims(t *testing.T) {
	for name, raw := range schemaDefinitions() {
		definition := raw.(map[string]any)
		properties := definition["properties"].(map[string]any)
		for _, private := range privateFieldNames {
			if _, ok := properties[private]; ok {
				t.Fatalf("%s publicly defines private field %s", name, private)
			}
		}
		for _, future := range []string{"tunnel_healthy", "verified_at", "observed_at", "operation_id", "operation_state"} {
			if _, ok := properties[future]; ok {
				t.Fatalf("%s claims unsupported future field %s", name, future)
			}
		}
	}
	if evidenceSemantics()["time"] == "" || evidenceSemantics()["tunnel"] == "" {
		t.Fatal("missing unknown/freshness semantics")
	}
}

func TestOptionalProxyReceiptPreservesFalseWithoutChangingLegacyResults(t *testing.T) {
	data, _ := json.Marshal(Result{OK: true, SystemProxyState: "acquired", Owned: Bool(true), Noop: Bool(false)})
	if !bytes.Contains(data, []byte(`"noop":false`)) {
		t.Fatal("false lease evidence omitted")
	}
	legacy, _ := json.Marshal(Result{OK: true})
	if bytes.Contains(legacy, []byte(`"noop"`)) || bytes.Contains(legacy, []byte(`"owned"`)) {
		t.Fatal("legacy result gained invented lease evidence")
	}
}
