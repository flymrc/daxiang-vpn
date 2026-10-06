package app

import (
	"encoding/json"
	"errors"
	"testing"

	"zongheng-vpn/shared/contracts"
	"zongheng-vpn/shared/paths"
)

func TestPublicJSONVersionIncludesEarlyFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() error
	}{
		{"success", func() error { return printJSON(jsonResult{OK: true}) }},
		{"command rejection", func() error { return reportErr(true, errors.New("synthetic rejection")) }},
		{"missing config", func() error { return status(paths.FromRoot(t.TempDir()), []string{"--json", "--no-ip-check"}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := captureStdout(t, func() {
				if err := test.run(); err != nil && !errors.Is(err, ErrSilent) {
					t.Fatal(err)
				}
			})
			var value map[string]any
			if err := json.Unmarshal([]byte(output), &value); err != nil {
				t.Fatal(err)
			}
			if value["contract_version"] != float64(contracts.ContractVersion) {
				t.Fatal("public JSON path omitted its contract version")
			}
		})
	}
}
