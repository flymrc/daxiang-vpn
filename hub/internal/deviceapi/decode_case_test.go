package deviceapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	generated "zongheng-vpn/shared/devicecontract"
)

func TestJSONPropertyCaseCannotOverwriteSignedCommand(t *testing.T) {
	for _, body := range []string{
		`{"action":"revoke","Action":"apply","idempotency_key":"key","expected_generation":1}`,
		`{"action":"apply","idempotency_key":"key","expected_generation":1,"Address":"10.66.0.20/32"}`,
		`{"action":"apply","idempotency_key":"key","expected_generation":1,"Wg_public_key":"alias"}`,
	} {
		r := httptest.NewRequest("POST", "/api/v2/commands", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var command generated.Command
		if _, err := decode(httptest.NewRecorder(), r, &command, "action", "idempotency_key", "expected_generation"); err == nil {
			t.Fatalf("case alias accepted: %s", body)
		}
	}
}
