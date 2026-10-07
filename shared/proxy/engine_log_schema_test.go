package proxy

import (
	"bytes"
	"os"
	"testing"
)

func TestEngineLogSchemaCurrent(t *testing.T) {
	const path = "engine-events-v1.schema.json"
	want := EngineLogSchema()
	actual, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(actual, want) {
		t.Fatal("engine event schema drift: run go run ./shared/proxy/cmd/logschemagen from the repository root")
	}
}
