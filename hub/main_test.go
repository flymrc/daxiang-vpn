package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"zongheng-vpn/hub/internal/auth"
)

func TestValidateClientListeners(t *testing.T) {
	if err := validateClientListeners("0.0.0.0:18080", "127.0.0.1:18079"); err != nil {
		t.Fatal(err)
	}
	for _, trusted := range []string{"0.0.0.0:18079", "10.0.0.2:18079", "localhost:18079", "127.0.0.1:18080"} {
		if err := validateClientListeners("0.0.0.0:18080", trusted); err == nil {
			t.Fatalf("trusted listener %q accepted", trusted)
		}
	}
}

func TestClientMuxAssignsBootstrapIngress(t *testing.T) {
	for _, tt := range []struct {
		name    string
		ingress auth.ClientIngress
	}{
		{name: "compat", ingress: auth.ClientIngressCompat},
		{name: "trusted proxy", ingress: auth.ClientIngressTrustedProxy},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{
				"ZH-OK": {
					Enabled:    true,
					ClientName: "test-client",
					Egress:     auth.Egress{Name: "test-egress"},
					WireGuard:  auth.WireGuard{PrivateKey: "PRIVATE_KEY_SENTINEL"},
				},
			}}
			server := auth.NewServer(store)
			var event auth.AuditEvent
			server.SetAuditSink(func(value auth.AuditEvent) { event = value })
			req := httptest.NewRequest(http.MethodPost, "/api/client/bootstrap", bytes.NewBufferString(`{"token":"ZH-OK","client_product":"cli","client_version":"1.0.0","protocol_version":2}`))
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set("X-ZHVPN-Ingress", "trusted_proxy")
			rec := httptest.NewRecorder()

			clientMux(server, tt.ingress).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if event.MigrationObservation == nil || event.MigrationObservation.Ingress != string(tt.ingress) {
				t.Fatalf("migration observation = %+v", event.MigrationObservation)
			}
		})
	}
}

func TestClientMuxAssignsRotateIngress(t *testing.T) {
	for _, ingress := range []auth.ClientIngress{auth.ClientIngressCompat, auth.ClientIngressTrustedProxy} {
		t.Run(string(ingress), func(t *testing.T) {
			store := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{
				"ZH-OK": {Enabled: true, ClientName: "test-client", Egress: auth.Egress{Name: "jp-android-01"}},
			}}
			server := auth.NewServer(store)
			server.SetRotateTrigger(func(string, int) error { return nil })
			var event auth.AuditEvent
			server.SetAuditSink(func(v auth.AuditEvent) { event = v })
			req := httptest.NewRequest(http.MethodPost, "/api/client/rotate-ip", bytes.NewBufferString(`{"token":"ZH-OK"}`))
			req.RemoteAddr = "127.0.0.1:1"
			req.Header.Set("X-Forwarded-For", "203.0.113.20")
			req.Header.Set("X-ZHVPN-Ingress", "trusted_proxy")
			rec := httptest.NewRecorder()
			clientMux(server, ingress).ServeHTTP(rec, req)
			want := "127.0.0.1"
			if ingress == auth.ClientIngressTrustedProxy {
				want = "203.0.113.20"
			}
			if rec.Code != http.StatusOK || event.SourceIP != want {
				t.Fatalf("status=%d source=%q, want %q", rec.Code, event.SourceIP, want)
			}
		})
	}
}
