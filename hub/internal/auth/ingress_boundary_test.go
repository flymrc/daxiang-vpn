package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"zongheng-vpn/hub/internal/httpboundary"
)

func boundaryBootstrap(s *Server, ingress ClientIngress, remote, xff, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/client/bootstrap", strings.NewReader(body))
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	w := httptest.NewRecorder()
	s.BootstrapHandler(ingress)(w, r)
	return w
}

func TestCompatPrivateSourceCannotImpersonateTokenLease(t *testing.T) {
	s := testBootstrapServer()
	const body = `{"token":"ZH-OK"}`
	if got := boundaryBootstrap(s, ClientIngressCompat, "10.66.0.30:1", "", body); got.Code != 200 {
		t.Fatalf("initial bootstrap = %d", got.Code)
	}
	for _, xff := range []string{"", "10.66.0.30", "not-an-ip", "10.66.0.30, 127.0.0.1"} {
		if got := boundaryBootstrap(s, ClientIngressCompat, "10.66.0.31:2", xff, body); got.Code != 409 {
			t.Fatalf("second source, xff=%q: status = %d", xff, got.Code)
		}
	}
	leases := s.TokenLeasesSnapshot(time.Now())
	if len(leases) != 1 || leases[0].SourceIP != "10.66.0.30" {
		t.Fatalf("lease owner changed: %+v", leases)
	}
}

func TestTrustedIngressRejectsInvalidSourceWithoutClaimingLease(t *testing.T) {
	for _, xff := range []string{"not-an-ip", "203.0.113.2,", "203.0.113.2:443", strings.Repeat(" ", httpboundary.MaxXFFBytes+1)} {
		s := testBootstrapServer()
		if got := boundaryBootstrap(s, ClientIngressTrustedProxy, "127.0.0.1:1", xff, `{"token":"ZH-OK"}`); got.Code != 400 {
			t.Fatalf("malformed forwarded source accepted: %d", got.Code)
		}
		if leases := s.TokenLeasesSnapshot(time.Now()); len(leases) != 0 {
			t.Fatal("invalid source acquired a token lease")
		}
	}
	s := testBootstrapServer()
	if got := boundaryBootstrap(s, ClientIngressCompat, "not-an-ip", "203.0.113.2", `{"token":"ZH-OK"}`); got.Code != 400 {
		t.Fatalf("invalid TCP source accepted: %d", got.Code)
	}
}

func TestTrustedIngressStopsAtUntrustedForwardedHop(t *testing.T) {
	s := testBootstrapServer()
	const body = `{"token":"ZH-OK"}`
	if got := boundaryBootstrap(s, ClientIngressTrustedProxy, "127.0.0.1:1", "203.0.113.20", body); got.Code != 200 {
		t.Fatalf("initial trusted bootstrap = %d", got.Code)
	}
	if got := boundaryBootstrap(s, ClientIngressTrustedProxy, "127.0.0.1:2", "203.0.113.20, 198.51.100.30, 127.0.0.1", body); got.Code != 409 {
		t.Fatalf("forged earlier hop acquired lease: %d", got.Code)
	}
}

func TestLegacyRequestBodiesAreBoundedAndCompleteBeforeEffects(t *testing.T) {
	for _, endpoint := range []string{"bootstrap", "rotate-ip"} {
		for _, tt := range []struct {
			name, body string
			want       int
		}{
			{"oversized unknown field", `{"token":"ZH-OK","extra":"` + strings.Repeat("x", httpboundary.MaxBodyBytes) + `"}`, 413},
			{"oversized whitespace", `{"token":"ZH-OK"}` + strings.Repeat(" ", httpboundary.MaxBodyBytes), 413},
			{"second object", `{"token":"ZH-OK"}{}`, 400},
			{"trailing garbage", `{"token":"ZH-OK"} not-json`, 400},
		} {
			t.Run(endpoint+"/"+tt.name, func(t *testing.T) {
				s := testBootstrapServer()
				calls := 0
				peerCalls := 0
				rotations := 0
				s.SetAuditSink(func(AuditEvent) { calls++ })
				s.applyClientPeer = func(context.Context, string, string) error { peerCalls++; return nil }
				s.SetRotateTrigger(func(context.Context, string, int) error { rotations++; return nil })
				r := httptest.NewRequest(http.MethodPost, "/api/client/"+endpoint, strings.NewReader(tt.body))
				r.ContentLength = -1
				w := httptest.NewRecorder()
				if endpoint == "bootstrap" {
					s.BootstrapHandler(ClientIngressCompat)(w, r)
				} else {
					s.RotateIP(w, r)
				}
				if w.Code != tt.want || calls != 0 || peerCalls != 0 || rotations != 0 || len(s.TokenLeasesSnapshot(time.Now())) != 0 {
					t.Fatalf("rejected request had effects: code=%d audit=%d peer=%d rotate=%d", w.Code, calls, peerCalls, rotations)
				}
			})
		}
	}
}

func TestLegacyUnknownFieldsStayCompatibleAndRotateSourceIsBound(t *testing.T) {
	for _, ingress := range []ClientIngress{ClientIngressCompat, ClientIngressTrustedProxy} {
		s := testBootstrapServer()
		w := boundaryBootstrap(s, ingress, "127.0.0.1:1", "203.0.113.20", `{"token":"ZH-OK","future_field":true}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"private_key":"CLIENT_PRIVATE_KEY"`) {
			t.Fatalf("legacy bootstrap changed: %d", w.Code)
		}
		var audit AuditEvent
		s.SetAuditSink(func(v AuditEvent) { audit = v })
		s.SetRotateTrigger(func(context.Context, string, int) error { return nil })
		r := httptest.NewRequest(http.MethodPost, "/api/client/rotate-ip", strings.NewReader(`{"token":"ZH-OK","future_field":true}`))
		r.RemoteAddr = "127.0.0.1:2"
		r.Header.Set("X-Forwarded-For", "203.0.113.20")
		w = httptest.NewRecorder()
		s.RotateIPHandler(ingress)(w, r)
		want := "127.0.0.1"
		if ingress == ClientIngressTrustedProxy {
			want = "203.0.113.20"
		}
		if w.Code != 200 || audit.SourceIP != want {
			t.Fatalf("rotate ingress=%s code=%d source=%q, want %q", ingress, w.Code, audit.SourceIP, want)
		}
	}
}
