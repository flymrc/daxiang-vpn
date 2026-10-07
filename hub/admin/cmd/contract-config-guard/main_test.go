package main

import (
	"strings"
	"testing"
)

const valid = `version: "2"
sql:
  - engine: sqlite
    schema: schema.sql
    queries: queries.sql
    gen:
      go:
        package: generated
        out: generated
        emit_json_tags: true
        emit_prepared_queries: false
        emit_interface: false
        emit_exact_table_names: false
`

func TestGeneratorPathsRefusedBeforeExecution(t *testing.T) {
	if err := validate([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../escape", "/tmp/escape", "C:/escape", "generated/../escape", `\\host\share`, "."} {
		t.Run(path, func(t *testing.T) {
			if validate([]byte(strings.Replace(valid, "out: generated", "out: "+path, 1))) == nil {
				t.Fatal("accepted external output")
			}
		})
	}
	for _, bad := range []string{
		strings.Replace(valid, "schema: schema.sql", "schema: ../outside.sql", 1),
		strings.Replace(valid, "queries: queries.sql", "queries: C:/outside.sql", 1),
		valid + "plugins:\n  - name: arbitrary\n    wasm:\n      url: https://example.invalid/tool\n",
		valid + "---\nversion: 2\n",
		strings.Replace(valid, "out: generated", "out: generated\n        out: ../escape", 1),
		strings.Replace(valid, "emit_json_tags: true", "emit_json_tags: true\n        output_models_file_name: ../escape.go", 1),
	} {
		if validate([]byte(bad)) == nil {
			t.Fatal("unsupported config accepted")
		}
	}
	// Supported options are passed through, never replaced with fixed defaults.
	if validate([]byte(strings.Replace(valid, "emit_interface: false", "emit_interface: true", 1))) != nil {
		t.Fatal("supported option refused")
	}
}
